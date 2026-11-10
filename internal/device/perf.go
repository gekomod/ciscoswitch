package device

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

// Domyślne czasy życia cache. Krótkie, bo panel ma pokazywać bieżący stan,
// ale wystarczające, by wiele równoległych zapytań (dashboard, kolektor
// monitoringu, diagnostyka, kilka kart przeglądarki) nie odpytywało switcha
// osobno.
const (
	defaultStatusCacheTTL = 5 * time.Second
	defaultReadCacheTTL   = 5 * time.Second
	// Połączenie SSH nieużywane dłużej niż ten czas jest zamykane i
	// otwierane od nowa, zanim AlliedWare Plus samo je zerwie.
	sshIdleLimit = 45 * time.Second
	// Zapytania wolniejsze niż ten próg są zawsze logowane.
	slowThreshold = 1500 * time.Millisecond
	// Maksymalny czas jednego polecenia show na współdzielonym połączeniu.
	// Po jego przekroczeniu połączenie jest zrywane, żeby zawieszony switch
	// nie zablokował całego panelu.
	defaultCommandTimeout = 30 * time.Second
)

// perfLogEnabled włącza logowanie czasu każdego zapytania (PERF_LOG=1).
// Bez tej zmiennej logowane są tylko zapytania wolniejsze niż slowThreshold.
var perfLog atomic.Bool

func init() {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("PERF_LOG")))
	perfLog.Store(v == "1" || v == "true" || v == "yes")
}

// SetPerfLog włącza lub wyłącza logowanie czasu każdego zapytania.
func SetPerfLog(on bool) { perfLog.Store(on) }

func logTiming(kind, what string, started time.Time, err error) {
	d := time.Since(started)
	if !perfLog.Load() && d < slowThreshold {
		return
	}
	status := "ok"
	if err != nil {
		status = "błąd"
	}
	log.Printf("[perf] %s %q: %s (%s)", kind, what, d.Round(time.Millisecond), status)
}

// flightGroup łączy równoległe wywołania z tym samym kluczem w jedno.
// Prosty odpowiednik golang.org/x/sync/singleflight bez nowej zależności.
type flightGroup struct {
	mu    sync.Mutex
	calls map[string]*flightCall
}

type flightCall struct {
	wg  sync.WaitGroup
	val any
	err error
}

func (g *flightGroup) do(key string, fn func() (any, error)) (any, error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = map[string]*flightCall{}
	}
	if c, ok := g.calls[key]; ok {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := &flightCall{}
	c.wg.Add(1)
	g.calls[key] = c
	g.mu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	g.mu.Lock()
	delete(g.calls, key)
	g.mu.Unlock()
	return c.val, c.err
}

type readEntry struct {
	out string
	at  time.Time
}

// readCache przechowuje wyniki poleceń show przez krótki czas.
// Każda zmiana konfiguracji wykonana przez panel czyści cały cache
// (invalidate), więc odczyt zwrotny po zmianie zawsze jest świeży.
type readCache struct {
	mu         sync.Mutex
	entries    map[string]readEntry
	generation uint64
}

func (c *readCache) get(cmd string, ttl time.Duration) (string, bool) {
	if ttl <= 0 {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[cmd]
	if !ok || time.Since(e.at) > ttl {
		return "", false
	}
	return e.out, true
}

func (c *readCache) gen() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generation
}

// put zapisuje wynik tylko wtedy, gdy w trakcie odczytu nie było zmiany
// konfiguracji; inaczej moglibyśmy zachować stan sprzed zmiany.
func (c *readCache) put(cmd, out string, startedGen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != startedGen {
		return
	}
	if c.entries == nil {
		c.entries = map[string]readEntry{}
	}
	c.entries[cmd] = readEntry{out: out, at: time.Now()}
}

func (c *readCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.entries = nil
}

// sshPool utrzymuje jedno połączenie SSH do odczytów i otwiera na nim
// kolejne sesje exec. Dostęp jest szeregowany, bo procesor switcha jest słaby,
// a AlliedWare Plus ogranicza liczbę równoległych sesji.
type sshPool struct {
	mu       sync.Mutex
	client   *ssh.Client
	lastUsed time.Time
}

func (p *sshPool) closeLocked() {
	if p.client != nil {
		_ = p.client.Close()
		p.client = nil
	}
}

// output wykonuje polecenie na współdzielonym połączeniu. Jeśli połączenie
// okaże się martwe (switch je zamknął), łączy się ponownie raz i powtarza.
func (p *sshPool) output(dial func() (*ssh.Client, error), cmd string, timeout time.Duration) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != nil && time.Since(p.lastUsed) > sshIdleLimit {
		p.closeLocked()
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if p.client == nil {
			c, err := dial()
			if err != nil {
				return "", err
			}
			p.client = c
		}
		session, err := p.client.NewSession()
		if err != nil {
			lastErr = err
			p.closeLocked()
			continue
		}
		b, err, timedOut := runWithTimeout(session, cmd, timeout)
		_ = session.Close()
		p.lastUsed = time.Now()
		if timedOut {
			// Zerwanie połączenia odblokowuje sesję; kolejne polecenie połączy się od nowa.
			p.closeLocked()
			return string(b), fmt.Errorf("switch nie odpowiedział na %q w ciągu %s", cmd, timeout)
		}
		if err != nil {
			var exitErr *ssh.ExitError
			if asExitError(err, &exitErr) {
				// Polecenie zakończyło się kodem błędu, ale połączenie jest zdrowe.
				return string(b), err
			}
			lastErr = err
			p.closeLocked()
			if len(b) > 0 {
				return string(b), err
			}
			continue
		}
		return string(b), nil
	}
	return "", lastErr
}

type outputResult struct {
	b   []byte
	err error
}

// runWithTimeout wykonuje polecenie i przerywa czekanie po upływie timeout.
func runWithTimeout(session *ssh.Session, cmd string, timeout time.Duration) ([]byte, error, bool) {
	done := make(chan outputResult, 1)
	go func() {
		b, err := session.Output(cmd)
		done <- outputResult{b, err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.b, r.err, false
	case <-timer.C:
		return nil, nil, true
	}
}

func asExitError(err error, target **ssh.ExitError) bool {
	e, ok := err.(*ssh.ExitError)
	if ok {
		*target = e
	}
	return ok
}

// Close zamyka współdzielone połączenie SSH (np. przy wyłączaniu panelu).
func (s *Service) Close() {
	s.ssh.mu.Lock()
	defer s.ssh.mu.Unlock()
	s.ssh.closeLocked()
}

// InvalidateCache czyści cache odczytów i statusu. Wywoływane automatycznie
// po każdej zmianie konfiguracji wykonanej przez panel.
func (s *Service) InvalidateCache() {
	s.reads.invalidate()
	s.statusMu.Lock()
	s.statusAt = time.Time{}
	s.statusMu.Unlock()
}

func (s *Service) statusTTL() time.Duration {
	s.tuneMu.RLock()
	defer s.tuneMu.RUnlock()
	if s.cfg.StatusCacheTTL < 0 {
		return 0
	}
	if s.cfg.StatusCacheTTL == 0 {
		return defaultStatusCacheTTL
	}
	return s.cfg.StatusCacheTTL
}

func (s *Service) commandTimeout() time.Duration {
	s.tuneMu.RLock()
	defer s.tuneMu.RUnlock()
	if s.cfg.CommandTimeout > 0 {
		return s.cfg.CommandTimeout
	}
	return defaultCommandTimeout
}

func (s *Service) readTTL() time.Duration {
	s.tuneMu.RLock()
	defer s.tuneMu.RUnlock()
	if s.cfg.ReadCacheTTL < 0 {
		return 0
	}
	if s.cfg.ReadCacheTTL == 0 {
		return defaultReadCacheTTL
	}
	return s.cfg.ReadCacheTTL
}

// Status zwraca stan portów z SNMP. Wynik jest buforowany przez StatusCacheTTL,
// a równoległe wywołania są łączone w jedno odpytanie switcha.
func (s *Service) Status() Status {
	ttl := s.statusTTL()
	if ttl > 0 {
		s.statusMu.Lock()
		if !s.statusAt.IsZero() && time.Since(s.statusAt) <= ttl {
			cached := cloneStatus(s.status)
			s.statusMu.Unlock()
			return cached
		}
		s.statusMu.Unlock()
	}
	v, _ := s.flights.do("status", func() (any, error) {
		started := time.Now()
		st := s.readStatus()
		var err error
		if st.Warning != "" {
			err = errString(st.Warning)
		}
		logTiming("snmp", "status", started, err)
		if st.Reachable {
			s.statusMu.Lock()
			s.status = st
			s.statusAt = time.Now()
			s.statusMu.Unlock()
		}
		return st, nil
	})
	return cloneStatus(v.(Status))
}

func cloneStatus(st Status) Status {
	out := st
	out.Ports = append([]Port(nil), st.Ports...)
	return out
}

type errString string

func (e errString) Error() string { return string(e) }

// Tuning to ustawienia zmieniane w panelu bez restartu.
type Tuning struct {
	StatusCacheTTL, ReadCacheTTL, CommandTimeout time.Duration
	SNMPV3PrivacyProtocol                        string
}

// SetTuning stosuje nowe ustawienia i czyści cache.
func (s *Service) SetTuning(t Tuning) {
	s.tuneMu.Lock()
	s.cfg.StatusCacheTTL = t.StatusCacheTTL
	s.cfg.ReadCacheTTL = t.ReadCacheTTL
	s.cfg.CommandTimeout = t.CommandTimeout
	if t.SNMPV3PrivacyProtocol != "" {
		s.cfg.SNMPV3PrivacyProtocol = t.SNMPV3PrivacyProtocol
	}
	s.tuneMu.Unlock()
	s.InvalidateCache()
}

func (s *Service) snmpConfig() Config {
	s.tuneMu.RLock()
	defer s.tuneMu.RUnlock()
	return s.cfg
}

// LocalIP zwraca adres IP tego komputera, przez który panel łączy się ze switchem
// (ten adres switch widzi jako adres panelu). Nie wysyła żadnych pakietów.
func (s *Service) LocalIP() (net.IP, error) {
	if strings.TrimSpace(s.cfg.Host) == "" {
		return nil, fmt.Errorf("brak SWITCH_HOST")
	}
	c, err := net.Dial("udp", net.JoinHostPort(s.cfg.Host, "22"))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	a, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok || a.IP == nil || a.IP.To4() == nil {
		return nil, fmt.Errorf("nie udało się ustalić adresu IPv4")
	}
	return a.IP.To4(), nil
}
