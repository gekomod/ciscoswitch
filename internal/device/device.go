package device

import (
	"crypto/subtle"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosnmp/gosnmp"
	"golang.org/x/crypto/ssh"
)

type Config struct {
	Host, SSHPort, SSHUser, SSHPassword, SSHHostKeyFingerprint string
	SNMPCommunity, SNMPPort, SNMPVersion                       string
	SNMPV3User, SNMPV3AuthPassword, SNMPV3PrivacyPassword      string
	// SNMPV3PrivacyProtocol: "des" (domyślnie, zgodność wsteczna) lub "aes" (AES-128).
	SNMPV3PrivacyProtocol string
	Timeout               time.Duration
	// StatusCacheTTL i ReadCacheTTL: 0 = wartość domyślna (5 s), wartość ujemna wyłącza cache.
	StatusCacheTTL, ReadCacheTTL time.Duration
	// CommandTimeout: maksymalny czas jednego polecenia show (0 = 30 s).
	CommandTimeout time.Duration
}
type Service struct {
	cfg      Config
	ssh      sshPool
	reads    readCache
	flights  flightGroup
	statusMu sync.Mutex
	tuneMu   sync.RWMutex
	status   Status
	statusAt time.Time
}
type Port struct {
	Index        int    `json:"index"`
	Name         string `json:"name"`
	Admin        string `json:"admin"`
	Oper         string `json:"oper"`
	Speed        string `json:"speed"`
	MTU          uint32 `json:"mtu"`
	InOctets     uint64 `json:"inOctets"`
	OutOctets    uint64 `json:"outOctets"`
	InErrors     uint64 `json:"inErrors"`
	OutErrors    uint64 `json:"outErrors"`
	InDiscards   uint64 `json:"inDiscards"`
	OutDiscards  uint64 `json:"outDiscards"`
	InUcastPkts  uint64 `json:"inUcastPkts"`
	OutUcastPkts uint64 `json:"outUcastPkts"`
	Description  string `json:"description,omitempty"` // IF-MIB ifAlias (opis portu)
}
type Status struct {
	Host        string `json:"host"`
	Reachable   bool   `json:"reachable"`
	Ports       []Port `json:"ports"`
	Warning     string `json:"warning,omitempty"`
	SNMPVersion string `json:"snmpVersion"`
}

func New(c Config) *Service { return &Service{cfg: c} }

func newSNMPClient(c Config) (*gosnmp.GoSNMP, error) {
	if strings.TrimSpace(c.Host) == "" {
		return nil, fmt.Errorf("brak SWITCH_HOST")
	}
	g := *gosnmp.Default
	g.Target = c.Host
	g.Port = uint16(port(c.SNMPPort, 161))
	g.Timeout = c.Timeout
	g.Retries = 1
	g.MaxRepetitions = 32
	version := strings.ToLower(strings.TrimSpace(c.SNMPVersion))
	if version == "" || version == "2" || version == "2c" {
		if c.SNMPCommunity == "" {
			return nil, fmt.Errorf("brak SWITCH_SNMP_COMMUNITY dla SNMPv2c")
		}
		g.Version = gosnmp.Version2c
		g.Community = c.SNMPCommunity
		return &g, nil
	}
	if version != "3" && version != "v3" {
		return nil, fmt.Errorf("nieobsługiwana wersja SNMP %q; użyj 2c lub 3", c.SNMPVersion)
	}
	if strings.TrimSpace(c.SNMPV3User) == "" || len(c.SNMPV3AuthPassword) < 8 || len(c.SNMPV3PrivacyPassword) < 8 {
		return nil, fmt.Errorf("SNMPv3 wymaga SWITCH_SNMP_V3_USER oraz haseł auth/priv o długości co najmniej 8 znaków")
	}
	g.Version = gosnmp.Version3
	g.SecurityModel = gosnmp.UserSecurityModel
	g.MsgFlags = gosnmp.AuthPriv
	g.SecurityParameters = &gosnmp.UsmSecurityParameters{
		UserName:                 strings.TrimSpace(c.SNMPV3User),
		AuthenticationProtocol:   gosnmp.SHA,
		AuthenticationPassphrase: c.SNMPV3AuthPassword,
		PrivacyProtocol:          privacyProtocol(c.SNMPV3PrivacyProtocol),
		PrivacyPassphrase:        c.SNMPV3PrivacyPassword,
	}
	return &g, nil
}

type Identity struct {
	Description string `json:"description"`
	ObjectID    string `json:"objectId"`
	Name        string `json:"name"`
	Uptime      string `json:"uptime"`
	UptimeTicks uint64 `json:"uptimeTicks"`
}

// DeviceIdentity reads standard SNMPv2-MIB identity objects. It does not
// depend on vendor-specific CLI output and is safe for read-only discovery.
func (s *Service) DeviceIdentity() (Identity, error) {
	var out Identity
	g, err := newSNMPClient(s.snmpConfig())
	if err != nil {
		return out, err
	}
	if err = g.Connect(); err != nil {
		return out, err
	}
	defer g.Conn.Close()
	result, err := g.Get([]string{
		".1.3.6.1.2.1.1.1.0", // sysDescr
		".1.3.6.1.2.1.1.2.0", // sysObjectID
		".1.3.6.1.2.1.1.3.0", // sysUpTime
		".1.3.6.1.2.1.1.5.0", // sysName
	})
	if err != nil {
		return out, fmt.Errorf("SNMP system identity: %w", err)
	}
	if result == nil {
		return out, fmt.Errorf("switch nie zwrócił danych identyfikacyjnych SNMP")
	}
	for _, pdu := range result.Variables {
		value := ""
		switch raw := pdu.Value.(type) {
		case []byte:
			value = strings.TrimSpace(string(raw))
		case string:
			value = strings.TrimSpace(raw)
		default:
			value = strings.TrimSpace(fmt.Sprint(raw))
		}
		switch pdu.Name {
		case ".1.3.6.1.2.1.1.1.0":
			out.Description = value
		case ".1.3.6.1.2.1.1.2.0":
			out.ObjectID = value
		case ".1.3.6.1.2.1.1.5.0":
			out.Name = value
		case ".1.3.6.1.2.1.1.3.0":
			var ticks uint64
			switch v := pdu.Value.(type) {
			case uint32:
				ticks = uint64(v)
			case uint64:
				ticks = v
			case uint:
				ticks = uint64(v)
			case int:
				if v > 0 {
					ticks = uint64(v)
				}
			case int64:
				if v > 0 {
					ticks = uint64(v)
				}
			}
			out.UptimeTicks = ticks
			seconds := ticks / 100
			days := seconds / 86400
			hours := (seconds % 86400) / 3600
			minutes := (seconds % 3600) / 60
			secs := seconds % 60
			out.Uptime = fmt.Sprintf("%d d %02d:%02d:%02d", days, hours, minutes, secs)
		}
	}
	if out.Description == "" && out.Name == "" && out.ObjectID == "" {
		return out, fmt.Errorf("switch nie udostępnia standardowych obiektów identyfikacyjnych SNMP")
	}
	return out, nil
}

// readStatus odpytuje switch przez SNMP bez użycia cache (patrz Status w perf.go).
func (s *Service) readStatus() Status {
	version := strings.ToLower(strings.TrimSpace(s.cfg.SNMPVersion))
	if version == "" {
		version = "2c"
	}
	if version == "v3" {
		version = "3"
	}
	o := Status{Host: s.cfg.Host, Ports: []Port{}, SNMPVersion: version}
	g, err := newSNMPClient(s.snmpConfig())
	if err != nil {
		o.Warning = err.Error()
		return o
	}
	if e := g.Connect(); e != nil {
		o.Warning = "SNMPv" + version + " connect: " + e.Error()
		return o
	}
	defer g.Conn.Close()
	d, e := walkText(g, ".1.3.6.1.2.1.2.2.1.2")
	if e != nil {
		o.Warning = "IF-MIB ifDescr: " + e.Error()
		return o
	}
	if len(d) == 0 {
		o.Warning = "SNMP działa, ale IF-MIB ifDescr jest pusty."
		return o
	}
	o.Reachable = true
	mtu, _ := walkNum(g, ".1.3.6.1.2.1.2.2.1.4")
	speed, _ := walkNum(g, ".1.3.6.1.2.1.2.2.1.5")
	highSpeed, _ := walkNum(g, ".1.3.6.1.2.1.31.1.1.1.15")
	adm, _ := walkNum(g, ".1.3.6.1.2.1.2.2.1.7")
	op, _ := walkNum(g, ".1.3.6.1.2.1.2.2.1.8")
	inOctets, _ := walkCounter(g, ".1.3.6.1.2.1.31.1.1.1.6")
	outOctets, _ := walkCounter(g, ".1.3.6.1.2.1.31.1.1.1.10")
	inErrors, _ := walkCounter(g, ".1.3.6.1.2.1.2.2.1.14")
	outErrors, _ := walkCounter(g, ".1.3.6.1.2.1.2.2.1.20")
	inDiscards, _ := walkCounter(g, ".1.3.6.1.2.1.2.2.1.13")
	outDiscards, _ := walkCounter(g, ".1.3.6.1.2.1.2.2.1.19")
	inUcast, _ := walkCounter(g, ".1.3.6.1.2.1.31.1.1.1.7")
	outUcast, _ := walkCounter(g, ".1.3.6.1.2.1.31.1.1.1.11")
	alias, _ := walkText(g, ".1.3.6.1.2.1.31.1.1.1.18")
	ix := make([]int, 0, len(d))
	for i := range d {
		ix = append(ix, i)
	}
	sort.Ints(ix)
	for _, i := range ix {
		p := Port{Index: i, Name: d[i], Admin: state(adm[i]), Oper: state(op[i]), MTU: mtu[i], InOctets: inOctets[i], OutOctets: outOctets[i], InErrors: inErrors[i], OutErrors: outErrors[i], InDiscards: inDiscards[i], OutDiscards: outDiscards[i], InUcastPkts: inUcast[i], OutUcastPkts: outUcast[i], Description: strings.TrimSpace(alias[i])}
		if highSpeed[i] > 0 {
			p.Speed = fmt.Sprintf("%d Mb/s", highSpeed[i])
		} else if speed[i] > 0 {
			p.Speed = fmt.Sprintf("%d Mb/s", speed[i]/1000000)
		}
		o.Ports = append(o.Ports, p)
	}
	return o
}

// walk używa GETBULK (SNMPv2c/v3), który pobiera wiele wierszy tabeli w jednym
// zapytaniu zamiast jednego GETNEXT na wiersz. Dla SNMPv1 zostaje zwykły Walk.
func walk(g *gosnmp.GoSNMP, oid string, fn gosnmp.WalkFunc) error {
	if g.Version == gosnmp.Version1 {
		return g.Walk(oid, fn)
	}
	return g.BulkWalk(oid, fn)
}

func walkText(g *gosnmp.GoSNMP, oid string) (map[int]string, error) {
	m := map[int]string{}
	e := walk(g, oid, func(p gosnmp.SnmpPDU) error {
		a := strings.Split(p.Name, ".")
		i, x := strconv.Atoi(a[len(a)-1])
		if x != nil {
			return nil
		}
		switch v := p.Value.(type) {
		case []byte:
			m[i] = strings.TrimSpace(string(v))
		case string:
			m[i] = strings.TrimSpace(v)
		default:
			m[i] = fmt.Sprint(v)
		}
		return nil
	})
	return m, e
}
func walkNum(g *gosnmp.GoSNMP, oid string) (map[int]uint32, error) {
	m := map[int]uint32{}
	e := walk(g, oid, func(p gosnmp.SnmpPDU) error {
		a := strings.Split(p.Name, ".")
		i, x := strconv.Atoi(a[len(a)-1])
		if x != nil {
			return nil
		}
		switch v := p.Value.(type) {
		case uint:
			m[i] = uint32(v)
		case uint32:
			m[i] = v
		case int:
			if v > 0 {
				m[i] = uint32(v)
			}
		case int64:
			if v > 0 {
				m[i] = uint32(v)
			}
		case uint64:
			m[i] = uint32(v)
		}
		return nil
	})
	return m, e
}
func walkCounter(g *gosnmp.GoSNMP, oid string) (map[int]uint64, error) {
	m := map[int]uint64{}
	e := walk(g, oid, func(p gosnmp.SnmpPDU) error {
		a := strings.Split(p.Name, ".")
		i, x := strconv.Atoi(a[len(a)-1])
		if x != nil {
			return nil
		}
		var n uint64
		switch v := p.Value.(type) {
		case uint:
			n = uint64(v)
		case uint32:
			n = uint64(v)
		case uint64:
			n = v
		case int:
			if v > 0 {
				n = uint64(v)
			}
		case int64:
			if v > 0 {
				n = uint64(v)
			}
		case []byte:
			n, _ = strconv.ParseUint(strings.TrimSpace(string(v)), 10, 64)
		default:
			n, _ = strconv.ParseUint(fmt.Sprint(v), 10, 64)
		}
		m[i] = n
		return nil
	})
	return m, e
}
func state(v uint32) string {
	switch v {
	case 1:
		return "up"
	case 2:
		return "down"
	case 3:
		return "testing"
	default:
		return "unknown"
	}
}
func validPort(v string) bool {
	if !strings.HasPrefix(v, "port1.0.") {
		return false
	}
	n, e := strconv.Atoi(strings.TrimPrefix(v, "port1.0."))
	return e == nil && n >= 1 && n <= 52
}
func port(v string, f int) int {
	n, e := strconv.Atoi(v)
	if e != nil || n < 1 || n > 65535 {
		return f
	}
	return n
}

// dialSSH verifies the switch host key against a SHA256 fingerprint configured out of band.
func (s *Service) dialSSH() (*ssh.Client, error) {
	if s.cfg.SSHHostKeyFingerprint == "" {
		return nil, fmt.Errorf("brak SWITCH_SSH_HOST_KEY: połączenie SSH zablokowane do czasu przypięcia klucza hosta")
	}
	expected := strings.TrimSpace(s.cfg.SSHHostKeyFingerprint)
	callback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		actual := ssh.FingerprintSHA256(key)
		if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
			return fmt.Errorf("niezgodny klucz SSH hosta %s (%s); oczekiwano %s", hostname, actual, expected)
		}
		return nil
	}
	return ssh.Dial("tcp", net.JoinHostPort(s.cfg.Host, s.cfg.SSHPort), &ssh.ClientConfig{
		User:            s.cfg.SSHUser,
		Auth:            []ssh.AuthMethod{ssh.Password(s.cfg.SSHPassword)},
		HostKeyCallback: callback,
		Timeout:         s.cfg.Timeout,
	})
}

func (s *Service) runConfig(cmds []string) (string, error) {
	return s.runConfigTimeout(cmds, 15*time.Second)
}

// runConfigTimeout działa jak runConfig, z własnym limitem czasu sesji
// (np. kopiowanie firmware trwa minuty).
func (s *Service) runConfigTimeout(cmds []string, timeout time.Duration) (string, error) {
	if s.cfg.Host == "" || s.cfg.SSHUser == "" || s.cfg.SSHPassword == "" {
		return "", fmt.Errorf("brak konfiguracji SSH w .env")
	}
	// Każda próba zmiany (także nieudana) unieważnia cache, aby kolejne odczyty
	// pokazały rzeczywisty stan switcha.
	defer s.InvalidateCache()
	started := time.Now()
	c, e := s.dialSSH()
	if e != nil {
		return "", e
	}
	defer c.Close()
	ss, e := c.NewSession()
	if e != nil {
		return "", e
	}
	defer ss.Close()
	in, e := ss.StdinPipe()
	if e != nil {
		return "", e
	}
	var out safeBuffer
	ss.Stdout = &out
	ss.Stderr = &out
	if e = ss.Shell(); e != nil {
		return "", e
	}
	script := configScript(cmds)
	for _, x := range script {
		if _, e = fmt.Fprintln(in, x); e != nil {
			return out.String(), e
		}
	}
	_ = in.Close()
	done := make(chan error, 1)
	go func() { done <- ss.Wait() }()
	select {
	case e = <-done:
		// Część implementacji SSH zgłasza zamknięcie sesji przez "exit" jako błąd
		// bez kodu wyjścia; liczy się wtedy treść odpowiedzi CLI.
		if e != nil {
			if _, ok := e.(*ssh.ExitMissingError); !ok {
				logTiming("ssh-config", configLabel(cmds), started, e)
				return out.String(), fmt.Errorf("CLI: %w; %s", e, out.String())
			}
		}
	case <-time.After(timeout):
		_ = ss.Close()
		logTiming("ssh-config", configLabel(cmds), started, fmt.Errorf("timeout"))
		return out.String(), fmt.Errorf("timeout CLI (%s) — switch nie zamknął sesji; sprawdź wynik odczytem zwrotnym", timeout)
	}
	result := out.String()
	if msg := cliError(result); msg != "" {
		logTiming("ssh-config", configLabel(cmds), started, fmt.Errorf("%s", msg))
		return result, fmt.Errorf("switch odrzucił polecenie: %s", msg)
	}
	logTiming("ssh-config", configLabel(cmds), started, nil)
	return result, nil
}

// configLabel opisuje zmianę w logach bez treści poleceń (mogą zawierać hasła).
func configLabel(cmds []string) string { return fmt.Sprintf("%d poleceń", len(cmds)) }

// configScript wyłącza stronicowanie (--More--) i kończy sesję poleceniem exit,
// dzięki czemu panel nie czeka na timeout po wykonaniu zmian.
func configScript(cmds []string) []string {
	script := make([]string, 0, len(cmds)+4)
	// AlliedWare Plus po zalogowaniu jest w trybie użytkownika (awplus>);
	// configure terminal, copy i boot wymagają trybu uprzywilejowanego (awplus#).
	// W trybie uprzywilejowanym enable niczego nie zmienia.
	if len(cmds) > 0 && strings.TrimSpace(cmds[0]) == "enable" {
		cmds = cmds[1:]
	}
	script = append(script, "enable", "terminal length 0")
	script = append(script, cmds...)
	last := ""
	if len(cmds) > 0 {
		last = strings.TrimSpace(cmds[len(cmds)-1])
	}
	if strings.HasPrefix(last, "configure") || (last != "end" && inConfigMode(cmds)) {
		script = append(script, "end")
	}
	return append(script, "exit")
}

// inConfigMode sprawdza, czy po liście poleceń sesja może pozostać w trybie konfiguracji.
func inConfigMode(cmds []string) bool {
	inside := false
	for _, c := range cmds {
		c = strings.TrimSpace(c)
		if strings.HasPrefix(c, "configure") {
			inside = true
		}
		if c == "end" {
			inside = false
		}
	}
	return inside
}

// cliErrorRE rozpoznaje komunikaty błędów AlliedWare Plus. AW+ zaczyna od "% " także
// zwykłe linie informacyjne (np. "% Static Aggregator: sa1"), więc nie wystarczy sam prefiks.
var cliErrorRE = regexp.MustCompile(`(?i)^%\s*(invalid|incomplete|unrecognized|ambiguous|error|unknown command|.*\b(fail(ed|ure)?|not (found|exist|allowed|permitted|supported)|does not exist|cannot|can't|unable)\b)`)

// cliError zwraca pierwszy komunikat błędu AlliedWare Plus z odpowiedzi CLI.
func cliError(out string) string {
	for _, line := range strings.Split(out, "\n") {
		l := strings.TrimSpace(line)
		if cliErrorRE.MatchString(l) {
			return l
		}
	}
	return ""
}

// safeBuffer jest bezpiecznym wątkowo buforem dla stdout i stderr sesji SSH.
type safeBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *safeBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}
func (w *safeBuffer) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.b.String() }

func (s *Service) ConfigurePort(name, action string) (string, error) {
	if !validPort(name) {
		return "", fmt.Errorf("nieprawidłowy port")
	}
	cmd := "shutdown"
	if action == "enable" {
		cmd = "no shutdown"
	} else if action != "disable" {
		return "", fmt.Errorf("nieobsługiwana operacja")
	}
	return s.runConfig([]string{"configure terminal", "interface " + name, cmd, "end"})
}
func (s *Service) SetManagementDHCP(on bool) (string, error) {
	cmd := "no ip address dhcp"
	if on {
		cmd = "ip address dhcp"
	}
	return s.runConfig([]string{"configure terminal", "interface vlan1", cmd, "end"})
}
func (s *Service) SaveConfig() (string, error) {
	return s.runConfig([]string{"copy running-config startup-config"})
}

var allowed = []string{"show system", "show running-config", "show startup-config", "show system environment", "show version", "show cpu", "show memory", "show interfaces status", "show interfaces", "show vlan", "show vlan all", "show ip interface brief", "show ip route", "show arp", "show lldp neighbors", "show snmp-server", "show snmp-server community", "show snmp-server user", "show snmp-server group", "show users", "show ssh server", "show running-config ssh", "show running-config snmp", "show counter snmp-server", "show spanning-tree", "show boot", "show http", "show interface switchport", "show running-config interface", "show etherchannel", "show etherchannel detail", "show etherchannel summary", "show static-channel-group", "show mac address-table", "show log", "show log tail 30", "show system mac", "show system serialnumber", "show ip dhcp server summary", "show telnet", "show lldp", "dir", "show clock", "show ntp associations", "show mls qos", "show ip dhcp binding", "show running-config dhcp"}

// SystemInfo returns raw, read-only AlliedWare Plus diagnostics. Values are deliberately
// kept as device output because available sensors and command formats vary by firmware.
func (s *Service) SystemInfo() map[string]string {
	out := map[string]string{}
	for _, item := range []struct{ key, cmd string }{
		{"version", "show version"},
		{"cpu", "show cpu"},
		{"memory", "show memory"},
		{"environment", "show system environment"},
		{"boot", "show boot"},
	} {
		value, err := s.RunReadOnly(item.cmd)
		if err != nil {
			out[item.key] = "Błąd odczytu: " + err.Error()
		} else if strings.TrimSpace(value) == "" {
			out[item.key] = "Urządzenie nie zwróciło danych."
		} else {
			out[item.key] = strings.TrimSpace(value)
		}
	}
	return out
}

func (s *Service) RunReadOnly(cmd string) (string, error) {
	ok := false
	for _, v := range allowed {
		if cmd == v {
			ok = true
			break
		}
	}
	if strings.HasPrefix(cmd, "show vlan ") {
		id, err := strconv.Atoi(strings.TrimPrefix(cmd, "show vlan "))
		if err == nil && id >= 1 && id <= 4094 {
			ok = true
		}
	}
	if !ok {
		return "", fmt.Errorf("polecenie niedozwolone")
	}
	if s.cfg.Host == "" || s.cfg.SSHUser == "" || s.cfg.SSHPassword == "" {
		return "", fmt.Errorf("brak SWITCH_HOST lub danych SSH w .env")
	}
	if out, ok := s.reads.get(cmd, s.readTTL()); ok {
		return out, nil
	}
	v, err := s.flights.do("cmd:"+cmd, func() (any, error) {
		gen := s.reads.gen()
		out, err := s.readFromSwitch(cmd)
		if err == nil {
			s.reads.put(cmd, out, gen)
		}
		return out, err
	})
	return v.(string), err
}

// RunReadOnlyFresh działa jak RunReadOnly, ale zawsze pyta switch
// (konsola diagnostyczna, w której użytkownik oczekuje bieżącego wyniku).
func (s *Service) RunReadOnlyFresh(cmd string) (string, error) {
	s.reads.mu.Lock()
	delete(s.reads.entries, cmd)
	s.reads.mu.Unlock()
	return s.RunReadOnly(cmd)
}

func (s *Service) readFromSwitch(cmd string) (string, error) {
	started := time.Now()
	out, err := s.ssh.output(s.dialSSH, cmd, s.commandTimeout())
	logTiming("ssh", cmd, started, err)
	return out, err
}

func (s *Service) ConfigureSSHUser(username, password string, privilege int, remove bool) (string, error) {
	username = strings.TrimSpace(username)
	if len(username) < 1 || len(username) > 32 || !regexp.MustCompile("^[A-Za-z0-9_.-]+$").MatchString(username) {
		return "", fmt.Errorf("login: 1–32 znaków A-Z, a-z, 0-9, _ . -")
	}
	if remove {
		if strings.EqualFold(username, strings.TrimSpace(s.cfg.SSHUser)) {
			return "", fmt.Errorf("nie można usunąć konta używanego przez panel (SWITCH_SSH_USER); najpierw skonfiguruj i przetestuj inne konto")
		}
		return s.runConfig([]string{"configure terminal", "no username " + username, "end"})
	}
	if privilege < 1 || privilege > 15 {
		return "", fmt.Errorf("poziom uprawnień musi wynosić 1–15")
	}
	if len(password) < 8 || len(password) > 23 || !regexp.MustCompile("^[A-Za-z0-9!@#$%^&*()_+={}.,:-]+$").MatchString(password) {
		return "", fmt.Errorf("hasło musi mieć 8–23 znaki; dozwolone litery, cyfry i bezpieczne znaki specjalne")
	}
	if !regexp.MustCompile("[A-Z]").MatchString(password) || !regexp.MustCompile("[a-z]").MatchString(password) || !regexp.MustCompile("[0-9]").MatchString(password) {
		return "", fmt.Errorf("hasło musi zawierać wielką i małą literę oraz cyfrę")
	}
	return s.runConfig([]string{"configure terminal", fmt.Sprintf("username %s privilege %d password %s", username, privilege, password), "end"})
}

// privacyProtocol zamienia nazwę z .env na protokół gosnmp. Puste lub nieznane = DES.
func privacyProtocol(name string) gosnmp.SnmpV3PrivProtocol {
	if NormalizePrivacyProtocol(name) == "aes" {
		return gosnmp.AES
	}
	return gosnmp.DES
}

// NormalizePrivacyProtocol zwraca "aes" albo "des" (domyślnie).
func NormalizePrivacyProtocol(name string) string {
	if strings.EqualFold(strings.TrimSpace(name), "aes") {
		return "aes"
	}
	return "des"
}

func BuildSNMPv3UserCommands(username, group, authPassword, privacyPassword, privProtocol string, remove bool) ([]string, error) {
	validName := regexp.MustCompile("^[A-Za-z0-9_.-]{1,32}$")
	username = strings.TrimSpace(username)
	group = strings.TrimSpace(group)
	if !validName.MatchString(username) {
		return nil, fmt.Errorf("login SNMPv3: 1–32 znaków A-Z, a-z, 0-9, _ . -")
	}
	if remove {
		return []string{"configure terminal", "no snmp-server user " + username, "end"}, nil
	}
	if !validName.MatchString(group) {
		return nil, fmt.Errorf("grupa SNMPv3: 1–32 znaków A-Z, a-z, 0-9, _ . -")
	}
	validPassword := regexp.MustCompile("^[A-Za-z0-9!@#$%^&*()_+={}.,:-]{8,64}$")
	if !validPassword.MatchString(authPassword) || !validPassword.MatchString(privacyPassword) {
		return nil, fmt.Errorf("hasła SNMPv3 muszą mieć 8–64 znaki; dozwolone litery, cyfry i bezpieczne znaki specjalne bez spacji")
	}
	if authPassword == privacyPassword {
		return nil, fmt.Errorf("hasło uwierzytelniania i szyfrowania muszą być różne")
	}
	return []string{"configure terminal", "snmp-server view panel-readonly 1.3.6.1.2.1 included", "snmp-server group " + group + " priv read panel-readonly", "snmp-server user " + username + " " + group + " auth sha " + authPassword + " priv " + NormalizePrivacyProtocol(privProtocol) + " " + privacyPassword, "end"}, nil
}
func (s *Service) ConfigureSNMPv3User(username, group, authPassword, privacyPassword, privProtocol string, remove bool) (string, error) {
	cmds, err := BuildSNMPv3UserCommands(username, group, authPassword, privacyPassword, privProtocol, remove)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

func BuildSNMPv3TrapHostCommands(address, username string, remove bool) ([]string, error) {
	ip := net.ParseIP(strings.TrimSpace(address))
	if ip == nil || ip.To4() == nil {
		return nil, fmt.Errorf("adres odbiorcy Trap musi być poprawnym IPv4")
	}
	username = strings.TrimSpace(username)
	if !regexp.MustCompile("^[A-Za-z0-9_.-]{1,32}$").MatchString(username) {
		return nil, fmt.Errorf("użytkownik SNMPv3: 1–32 znaki A-Z, a-z, 0-9, _ . -")
	}
	command := "snmp-server host " + ip.To4().String() + " version 3 priv " + username
	if remove {
		command = "no " + command
	}
	return []string{"configure terminal", command, "end"}, nil
}
func (s *Service) ConfigureSNMPv3TrapHost(address, username string, remove bool) (string, error) {
	cmds, err := BuildSNMPv3TrapHostCommands(address, username, remove)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

func (s *Service) ConfigureSNMPTrapHost(address, community string, remove bool) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(address))
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("adres odbiorcy trap musi być poprawnym IPv4")
	}
	community = strings.TrimSpace(community)
	if len(community) < 1 || len(community) > 32 || !regexp.MustCompile("^[A-Za-z0-9_.-]+$").MatchString(community) {
		return "", fmt.Errorf("community: 1–32 znaków A-Z, a-z, 0-9, _ . -")
	}
	command := "snmp-server host " + ip.To4().String() + " traps version 2c " + community
	if remove {
		command = "no " + command
	}
	return s.runConfig([]string{"configure terminal", command, "end"})
}

func validProbeTarget(target string) bool {
	target = strings.TrimSpace(target)
	if ip := net.ParseIP(target); ip != nil {
		return ip.To4() != nil
	}
	if len(target) < 1 || len(target) > 253 {
		return false
	}
	for _, label := range strings.Split(target, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
func (s *Service) ProbeNetwork(kind, target string) (string, error) {
	target = strings.TrimSpace(target)
	if !validProbeTarget(target) {
		return "", fmt.Errorf("podaj poprawny adres IPv4 lub nazwę DNS")
	}
	var cmd string
	switch kind {
	case "ping":
		cmd = "ping " + target
	case "traceroute":
		cmd = "traceroute " + target
	default:
		return "", fmt.Errorf("nieznany test sieciowy")
	}
	return s.RunCLICommand(cmd)
}
func (s *Service) RunCLICommand(cmd string) (string, error) {
	if s.cfg.Host == "" || s.cfg.SSHUser == "" || s.cfg.SSHPassword == "" {
		return "", fmt.Errorf("brak SWITCH_HOST lub danych SSH w .env")
	}
	client, e := s.dialSSH()
	if e != nil {
		return "", e
	}
	defer client.Close()
	session, e := client.NewSession()
	if e != nil {
		return "", e
	}
	defer session.Close()
	output, e := session.Output(cmd)
	return string(output), e
}
func (s *Service) ConfigureStaticRoute(prefix, gateway string, distance int, remove bool) (string, error) {
	_, network, e := net.ParseCIDR(strings.TrimSpace(prefix))
	if e != nil {
		return "", fmt.Errorf("podaj poprawną sieć CIDR, np. 192.168.20.0/24")
	}
	ip := net.ParseIP(gateway)
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("brama musi być poprawnym adresem IPv4")
	}
	if distance < 1 || distance > 255 {
		return "", fmt.Errorf("dystans administracyjny musi być w zakresie 1–255")
	}
	command := "ip route " + network.String() + " " + ip.String()
	if distance != 1 {
		command += " " + strconv.Itoa(distance)
	}
	if remove {
		command = "no " + command
	}
	return s.runConfig([]string{"configure terminal", command, "end"})
}

func validVLANID(v int) bool { return v >= 1 && v <= 4094 }
func parseVLANList(raw string) ([]int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("podaj co najmniej jeden VLAN")
	}
	seen := map[int]bool{}
	out := []int{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			a, b, ok := strings.Cut(part, "-")
			if !ok {
				return nil, fmt.Errorf("nieprawidłowy zakres VLAN")
			}
			lo, e1 := strconv.Atoi(strings.TrimSpace(a))
			hi, e2 := strconv.Atoi(strings.TrimSpace(b))
			if e1 != nil || e2 != nil || lo < 1 || hi > 4094 || lo > hi || hi-lo > 256 {
				return nil, fmt.Errorf("nieprawidłowy zakres VLAN: %s", part)
			}
			for n := lo; n <= hi; n++ {
				if !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
			}
		} else {
			n, e := strconv.Atoi(part)
			if e != nil || !validVLANID(n) {
				return nil, fmt.Errorf("nieprawidłowy VLAN: %s", part)
			}
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("pusta lista VLAN")
	}
	return out, nil
}
func validIPv4(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	return ip != nil && ip.To4() != nil
}
func (s *Service) ConfigureDHCPPool(name, network, mask, low, high, router, dns string, enable bool) (string, error) {
	name = strings.TrimSpace(name)
	if len(name) < 1 || len(name) > 32 || !regexp.MustCompile("^[A-Za-z0-9_-]+$").MatchString(name) {
		return "", fmt.Errorf("nazwa puli: 1–32 znaków A-Z, a-z, 0-9, _ lub -")
	}
	for label, value := range map[string]string{"sieć": network, "maska": mask, "początek zakresu": low, "koniec zakresu": high, "brama": router, "DNS": dns} {
		if !validIPv4(value) {
			return "", fmt.Errorf("nieprawidłowy adres IPv4 (%s)", label)
		}
	}
	n := net.ParseIP(network).To4()
	m := net.IPMask(net.ParseIP(mask).To4())
	ones, bits := m.Size()
	if bits != 32 || ones < 1 || ones > 30 {
		return "", fmt.Errorf("maska musi być poprawną maską IPv4 od /1 do /30")
	}
	ipnet := &net.IPNet{IP: n.Mask(m), Mask: m}
	lo, hi, gw := net.ParseIP(low).To4(), net.ParseIP(high).To4(), net.ParseIP(router).To4()
	if !ipnet.Contains(lo) || !ipnet.Contains(hi) || !ipnet.Contains(gw) {
		return "", fmt.Errorf("zakres i brama muszą należeć do podanej podsieci")
	}
	if !n.Equal(ipnet.IP) {
		return "", fmt.Errorf("adres sieci musi być adresem sieci, np. 192.168.10.0")
	}
	if bytesCompare(lo, hi) > 0 {
		return "", fmt.Errorf("początek zakresu jest większy niż koniec")
	}
	if lo.Equal(ipnet.IP) || hi.Equal(ipnet.IP) || lo.Equal(net.IP{byte(n[0] | ^m[0]), byte(n[1] | ^m[1]), byte(n[2] | ^m[2]), byte(n[3] | ^m[3])}) {
		return "", fmt.Errorf("zakres nie może zawierać adresu sieci ani broadcast")
	}
	if bytesCompare(gw, lo) >= 0 && bytesCompare(gw, hi) <= 0 {
		return "", fmt.Errorf("wyłącz adres bramy z zakresu DHCP")
	}
	cmds := []string{"configure terminal", "ip dhcp pool " + name, "network " + network + " " + mask, "range " + low + " " + high, "default-router " + router, "dns-server " + dns, "exit"}
	if enable {
		cmds = append(cmds, "service dhcp-server")
	}
	cmds = append(cmds, "end")
	return s.runConfig(cmds)
}
func bytesCompare(a, b net.IP) int {
	for i := 0; i < 4; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

func BuildVLANCommands(id int, name string, remove bool) ([]string, error) {
	if id < 2 || id > 4094 {
		return nil, fmt.Errorf("VLAN ID musi być z zakresu 2–4094; VLAN 1 jest domyślny")
	}
	if remove {
		return []string{"configure terminal", "vlan database", fmt.Sprintf("no vlan %d", id), "exit", "end"}, nil
	}
	name = strings.TrimSpace(name)
	if name != "" {
		ok, _ := regexp.MatchString("^[A-Za-z0-9_-]+$", name)
		if len(name) > 16 || !ok {
			return nil, fmt.Errorf("nazwa VLAN: 1–16 znaków A-Z, a-z, 0-9, _ lub -")
		}
	}
	cmd := fmt.Sprintf("vlan %d state enable", id)
	if name != "" {
		cmd = fmt.Sprintf("vlan %d name %s state enable", id, name)
	}
	return []string{"configure terminal", "vlan database", cmd, "exit", "end"}, nil
}

// IsFiberPort mówi, czy port to gniazdo SFP/SFP+ (49–52 na AT-GS948MX).
func IsFiberPort(name string) bool {
	if !strings.HasPrefix(name, "port1.0.") {
		return false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(name, "port1.0."))
	return err == nil && n >= 49 && n <= 52
}

func BuildPortSettingsCommands(name, mode string, access int, allowed string, native int, speed, duplex, description string) ([]string, error) {
	if !validPort(name) {
		return nil, fmt.Errorf("nieprawidłowy port")
	}
	if mode != "access" && mode != "trunk" {
		return nil, fmt.Errorf("tryb portu musi być access lub trunk")
	}
	fiber := IsFiberPort(name)
	switch {
	case fiber && speed != "auto" && speed != "1000" && speed != "10000":
		return nil, fmt.Errorf("port %s to gniazdo SFP/SFP+: prędkość auto, 1000 (moduł 1 Gb/s) albo 10000 Mb/s (moduł 10 Gb/s)", name)
	case !fiber && speed != "auto" && speed != "10" && speed != "100" && speed != "1000":
		return nil, fmt.Errorf("port %s jest miedziany: prędkość auto, 10, 100 lub 1000 Mb/s", name)
	}
	if duplex != "auto" && duplex != "full" && duplex != "half" {
		return nil, fmt.Errorf("duplex musi być auto, full lub half")
	}
	if fiber && duplex == "half" {
		return nil, fmt.Errorf("port %s to gniazdo SFP/SFP+: półdupleks nie jest obsługiwany", name)
	}
	description = strings.TrimSpace(description)
	ok, _ := regexp.MatchString("^[A-Za-z0-9 _./:-]*$", description)
	if len(description) > 64 || !ok {
		return nil, fmt.Errorf("opis portu: maks. 64 znaki; dozwolone litery łacińskie, cyfry, spacja i _ . / : -")
	}
	cmds := []string{"configure terminal", "interface " + name}
	if description == "" {
		cmds = append(cmds, "no description")
	} else {
		cmds = append(cmds, "description "+description)
	}
	cmds = append(cmds, "speed "+speed)
	// Na gniazdach SFP/SFP+ duplex wynika z modułu; "duplex auto" bywa odrzucane,
	// więc wysyłamy go tylko przy jawnym wyborze.
	if !fiber || duplex == "full" {
		cmds = append(cmds, "duplex "+duplex)
	}
	cmds = append(cmds, "switchport")
	if mode == "access" {
		if access < 1 || access > 4094 {
			return nil, fmt.Errorf("access VLAN musi być z zakresu 1–4094")
		}
		cmds = append(cmds, "switchport mode access", fmt.Sprintf("switchport access vlan %d", access))
	} else {
		vlans, e := parseVLANList(allowed)
		if e != nil {
			return nil, e
		}
		if native < 1 || native > 4094 {
			return nil, fmt.Errorf("native VLAN musi być z zakresu 1–4094")
		}
		found := false
		for _, v := range vlans {
			if v == native {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("native VLAN musi znajdować się na liście dozwolonych VLAN-ów")
		}
		canonical := make([]string, 0, len(vlans))
		for _, v := range vlans {
			canonical = append(canonical, strconv.Itoa(v))
		}
		cmds = append(cmds, "switchport mode trunk", "switchport trunk allowed vlan none", "switchport trunk allowed vlan add "+strings.Join(canonical, ","), fmt.Sprintf("switchport trunk native vlan %d", native))
	}
	cmds = append(cmds, "end")
	return cmds, nil
}

type vlanPortConfig struct {
	mode   string
	native int
	routed bool
}

// BuildVLANPortMembershipCommands changes membership only when the existing
// interface mode is compatible. It never silently converts access ports to trunk
// or trunk ports to access.
func BuildVLANPortMembershipCommands(runningConfig string, ports []string, vlan int, membership string) ([]string, error) {
	runningConfig = NormalizeInterfaceRanges(runningConfig)
	if vlan < 1 || vlan > 4094 {
		return nil, fmt.Errorf("VLAN ID musi być z zakresu 1–4094")
	}
	if len(ports) < 1 || len(ports) > 52 {
		return nil, fmt.Errorf("wybierz od 1 do 52 portów")
	}
	configs := map[string]vlanPortConfig{}
	current := ""
	for _, raw := range strings.Split(runningConfig, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "interface ") {
			fields := strings.Fields(line)
			current = ""
			if len(fields) == 2 && validPort(fields[1]) {
				current = fields[1]
				configs[current] = vlanPortConfig{mode: "access", native: 1}
			}
			continue
		}
		if current == "" {
			continue
		}
		cfg := configs[current]
		if lower == "no switchport" {
			cfg.routed = true
		}
		if lower == "switchport mode trunk" || strings.HasPrefix(lower, "switchport mode trunk ") {
			cfg.mode = "trunk"
		}
		if lower == "switchport mode access" {
			cfg.mode = "access"
		}
		if strings.HasPrefix(lower, "switchport trunk native vlan ") {
			fields := strings.Fields(line)
			if len(fields) == 5 {
				if strings.EqualFold(fields[4], "none") {
					cfg.native = 0
				} else if n, err := strconv.Atoi(fields[4]); err == nil && n >= 1 && n <= 4094 {
					cfg.native = n
				}
			}
		}
		configs[current] = cfg
	}
	seen := map[string]bool{}
	commands := []string{"configure terminal"}
	for _, raw := range ports {
		port := strings.TrimSpace(raw)
		if !validPort(port) {
			return nil, fmt.Errorf("nieprawidłowy port: %s", port)
		}
		if seen[port] {
			continue
		}
		seen[port] = true
		cfg, exists := configs[port]
		if !exists {
			cfg = vlanPortConfig{mode: "access", native: 1}
		}
		if cfg.routed {
			return nil, fmt.Errorf("%s: port działa w trybie routowanym (no switchport)", port)
		}
		switch membership {
		case "tagged":
			if cfg.mode != "trunk" {
				return nil, fmt.Errorf("%s: port nie jest trunk; najpierw skonfiguruj i sprawdź profil trunk", port)
			}
			if cfg.native == vlan {
				return nil, fmt.Errorf("%s: VLAN %d jest native/untagged na tym porcie, nie można dodać go jako tagged", port, vlan)
			}
			commands = append(commands, "interface "+port, fmt.Sprintf("switchport trunk allowed vlan add %d", vlan))
		case "untagged":
			if cfg.mode == "trunk" {
				return nil, fmt.Errorf("%s: port jest trunk; zmiana na untagged wymaga świadomej zmiany profilu portu", port)
			}
			commands = append(commands, "interface "+port, fmt.Sprintf("switchport access vlan %d", vlan))
		default:
			return nil, fmt.Errorf("typ członkostwa musi być tagged lub untagged")
		}
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("nie wybrano portów")
	}
	commands = append(commands, "end")
	return commands, nil
}

type SwitchportDetails struct {
	Port                 string `json:"port"`
	Mode                 string `json:"mode"`
	IngressFilter        string `json:"ingressFilter"`
	AcceptableFrameTypes string `json:"acceptableFrameTypes"`
	DefaultVLAN          string `json:"defaultVlan"`
	ConfiguredVLANs      []int  `json:"configuredVlans"`
	Raw                  string `json:"raw"`
}

// SwitchportVLANField zwraca wartość pola listy VLAN z bloku "show interface switchport"
// razem z liniami kontynuacji: AW+ zawija długie listy do kolejnych linii zawierających
// wyłącznie numery VLAN.
func SwitchportVLANField(block, label string) string {
	lines := strings.Split(block, "\n")
	head := regexp.MustCompile(`(?i)^\s*` + regexp.QuoteMeta(label) + `\s*:\s*(.*?)\s*$`)
	onlyNumbers := regexp.MustCompile(`^\s*[0-9][0-9,\s-]*$`)
	for i, line := range lines {
		m := head.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		parts := []string{m[1]}
		for _, next := range lines[i+1:] {
			next = strings.TrimRight(next, "\r")
			if !onlyNumbers.MatchString(next) {
				break
			}
			parts = append(parts, strings.TrimSpace(next))
		}
		return strings.TrimSpace(strings.Join(parts, " "))
	}
	return ""
}

// ParseSwitchportDetails extracts one exact interface block from AlliedWare Plus
// "show interface switchport". Missing fields stay empty; the parser never guesses.
func ParseSwitchportDetails(output, port string) (SwitchportDetails, error) {
	if !validPort(port) {
		return SwitchportDetails{}, fmt.Errorf("nieprawidłowy port")
	}
	blocks := regexp.MustCompile(`(?im)^\s*Interface name\s*:\s*(\S+)\s*$`).FindAllStringSubmatchIndex(output, -1)
	for i, loc := range blocks {
		name := strings.TrimSpace(output[loc[2]:loc[3]])
		if !strings.EqualFold(name, port) {
			continue
		}
		end := len(output)
		if i+1 < len(blocks) {
			end = blocks[i+1][0]
		}
		block := output[loc[0]:end]
		value := func(label string) string {
			re := regexp.MustCompile(`(?im)^\s*` + regexp.QuoteMeta(label) + `\s*:\s*(.*?)\s*$`)
			m := re.FindStringSubmatch(block)
			if len(m) < 2 {
				return ""
			}
			return strings.TrimSpace(m[1])
		}
		details := SwitchportDetails{Port: port, Mode: value("Switchport mode"), IngressFilter: value("Ingress filter"), AcceptableFrameTypes: value("Acceptable frame types"), DefaultVLAN: value("Default Vlan"), Raw: strings.TrimSpace(block)}
		if raw := SwitchportVLANField(block, "Configured Vlans"); raw != "" {
			for _, token := range strings.Fields(raw) {
				n, err := strconv.Atoi(strings.Trim(token, ","))
				if err == nil && n >= 1 && n <= 4094 {
					details.ConfiguredVLANs = append(details.ConfiguredVLANs, n)
				}
			}
		}
		return details, nil
	}
	return SwitchportDetails{}, fmt.Errorf("brak bloku konfiguracji dla portu %s w odpowiedzi show interface switchport", port)
}

func (s *Service) ConfigureVLANPortMembership(ports []string, vlan int, membership string) (string, error) {
	running, err := s.RunReadOnly("show running-config")
	if err != nil {
		return "", fmt.Errorf("nie udało się odczytać bieżącej konfiguracji portów: %w", err)
	}
	commands, err := BuildVLANPortMembershipCommands(running, ports, vlan, membership)
	if err != nil {
		return "", err
	}
	return s.runConfig(commands)
}

func (s *Service) ConfigureVLAN(id int, name string, remove bool) (string, error) {
	cmds, err := BuildVLANCommands(id, name, remove)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

// PortRepairRequest describes an intentionally partial port update. Unselected
// fields are never emitted as commands and therefore remain untouched.
type PortRepairRequest struct {
	Port              string
	Mode              string
	AccessVLAN        int
	Allowed           string
	Native            int
	Description       string
	UpdateAccessVLAN  bool
	UpdateTrunk       bool
	UpdateDescription bool
}

func BuildPortRepairCommands(q PortRepairRequest) ([]string, error) {
	if !validPort(q.Port) {
		return nil, fmt.Errorf("nieprawidłowy port")
	}
	if q.Mode != "access" && q.Mode != "trunk" {
		return nil, fmt.Errorf("nieprawidłowy tryb bieżącego portu")
	}
	if !q.UpdateAccessVLAN && !q.UpdateTrunk && !q.UpdateDescription {
		return nil, fmt.Errorf("nie wybrano żadnego parametru do naprawy")
	}
	if q.UpdateAccessVLAN && q.Mode != "access" {
		return nil, fmt.Errorf("Access VLAN można naprawiać wyłącznie na porcie pozostającym w trybie access")
	}
	if q.UpdateTrunk && q.Mode != "trunk" {
		return nil, fmt.Errorf("VLAN trunk można naprawiać wyłącznie na porcie pozostającym w trybie trunk")
	}
	commands := []string{"configure terminal", "interface " + q.Port}
	if q.UpdateDescription {
		description := strings.TrimSpace(q.Description)
		ok, _ := regexp.MatchString("^[A-Za-z0-9 _./:-]*$", description)
		if len(description) > 64 || !ok {
			return nil, fmt.Errorf("opis portu: maks. 64 znaki; dozwolone litery łacińskie, cyfry, spacja i _ . / : -")
		}
		if description == "" {
			commands = append(commands, "no description")
		} else {
			commands = append(commands, "description "+description)
		}
	}
	if q.UpdateAccessVLAN {
		if q.AccessVLAN < 1 || q.AccessVLAN > 4094 {
			return nil, fmt.Errorf("access VLAN musi być z zakresu 1–4094")
		}
		commands = append(commands, fmt.Sprintf("switchport access vlan %d", q.AccessVLAN))
	}
	if q.UpdateTrunk {
		vlans, err := parseVLANList(q.Allowed)
		if err != nil {
			return nil, err
		}
		if q.Native < 1 || q.Native > 4094 {
			return nil, fmt.Errorf("native VLAN musi być z zakresu 1–4094")
		}
		found := false
		canonical := make([]string, 0, len(vlans))
		for _, v := range vlans {
			if v == q.Native {
				found = true
			}
			canonical = append(canonical, strconv.Itoa(v))
		}
		if !found {
			return nil, fmt.Errorf("native VLAN musi znajdować się na liście dozwolonych VLAN-ów")
		}
		commands = append(commands, "switchport trunk allowed vlan none", "switchport trunk allowed vlan add "+strings.Join(canonical, ","), fmt.Sprintf("switchport trunk native vlan %d", q.Native))
	}
	commands = append(commands, "end")
	return commands, nil
}

func (s *Service) ConfigurePortRepair(q PortRepairRequest) (string, error) {
	commands, err := BuildPortRepairCommands(q)
	if err != nil {
		return "", err
	}
	return s.runConfig(commands)
}

func BuildBulkPortSettingsCommands(ports []string, mode string, access int, allowed string, native int, speed, duplex, description string) ([]string, error) {
	if len(ports) < 1 || len(ports) > 52 {
		return nil, fmt.Errorf("wybierz od 1 do 52 portów")
	}
	seen := map[string]bool{}
	commands := []string{"configure terminal"}
	for _, raw := range ports {
		name := strings.TrimSpace(raw)
		if !validPort(name) {
			return nil, fmt.Errorf("nieprawidłowy port: %s", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		part, err := BuildPortSettingsCommands(name, mode, access, allowed, native, speed, duplex, description)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		commands = append(commands, part[1:len(part)-1]...)
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("nie wybrano portów")
	}
	commands = append(commands, "end")
	return commands, nil
}
func (s *Service) ConfigureBulkPortSettings(ports []string, mode string, access int, allowed string, native int, speed, duplex, description string) (string, error) {
	cmds, err := BuildBulkPortSettingsCommands(ports, mode, access, allowed, native, speed, duplex, description)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

// BuildBulkPortActionCommands builds one validated admin-state change for a set of ports.
func BuildBulkPortActionCommands(ports []string, action string) ([]string, error) {
	if len(ports) < 1 || len(ports) > 52 {
		return nil, fmt.Errorf("wybierz od 1 do 52 portów")
	}
	command := ""
	switch action {
	case "enable":
		command = "no shutdown"
	case "disable":
		command = "shutdown"
	default:
		return nil, fmt.Errorf("nieobsługiwana operacja portu")
	}
	seen := map[string]bool{}
	commands := []string{"configure terminal"}
	for _, raw := range ports {
		name := strings.TrimSpace(raw)
		if !validPort(name) {
			return nil, fmt.Errorf("nieprawidłowy port: %s", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		commands = append(commands, "interface "+name, command)
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("nie wybrano portów")
	}
	commands = append(commands, "end")
	return commands, nil
}
func (s *Service) ConfigureBulkPortAction(ports []string, action string) (string, error) {
	cmds, err := BuildBulkPortActionCommands(ports, action)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

func (s *Service) ConfigurePortSettings(name, mode string, access int, allowed string, native int, speed, duplex, description string) (string, error) {
	cmds, err := BuildPortSettingsCommands(name, mode, access, allowed, native, speed, duplex, description)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}
func parsePortNumbers(raw string) ([]int, error) {
	seen := map[int]bool{}
	out := []int{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 52 {
			return nil, fmt.Errorf("porty podaj jako listę numerów 1–52, np. 5,6,51,52")
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) < 2 {
		return nil, fmt.Errorf("agregacja wymaga co najmniej dwóch portów")
	}
	sort.Ints(out)
	return out, nil
}
func (s *Service) ConfigureLACP(group int, rawPorts, mode string, remove bool) (string, error) {
	if group < 1 || group > 32 {
		return "", fmt.Errorf("numer grupy LACP musi być z zakresu 1–32")
	}
	ports, e := parsePortNumbers(rawPorts)
	if e != nil {
		return "", e
	}
	if mode != "active" && mode != "passive" {
		return "", fmt.Errorf("tryb LACP musi być active lub passive")
	}
	names := make([]string, 0, len(ports))
	for _, n := range ports {
		names = append(names, fmt.Sprintf("port1.0.%d", n))
	}
	cmd := "channel-group " + strconv.Itoa(group) + " mode " + mode
	if remove {
		cmd = "no channel-group"
	}
	return s.runConfig([]string{"configure terminal", "interface " + strings.Join(names, ","), cmd, "end"})
}
func (s *Service) ConfigureSTP(mode string, priority int) (string, error) {
	if mode != "rstp" && mode != "mstp" && mode != "stp" {
		return "", fmt.Errorf("tryb STP musi być stp, rstp lub mstp")
	}
	if priority < 0 || priority > 61440 || priority%4096 != 0 {
		return "", fmt.Errorf("priorytet musi być wielokrotnością 4096 w zakresie 0–61440")
	}
	return s.runConfig([]string{"configure terminal", "spanning-tree mode " + mode, fmt.Sprintf("spanning-tree priority %d", priority), "end"})
}
func BuildBulkPortEdgePortCommands(ports []string, enabled bool) ([]string, error) {
	if len(ports) < 1 || len(ports) > 52 {
		return nil, fmt.Errorf("wybierz od 1 do 52 portów")
	}
	command := "no spanning-tree edgeport"
	if enabled {
		command = "spanning-tree edgeport"
	}
	seen := map[string]bool{}
	commands := []string{"configure terminal"}
	for _, raw := range ports {
		name := strings.TrimSpace(raw)
		if !validPort(name) {
			return nil, fmt.Errorf("nieprawidłowy port: %s", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		commands = append(commands, "interface "+name, command)
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("nie wybrano portów")
	}
	commands = append(commands, "end")
	return commands, nil
}
func (s *Service) ConfigureBulkPortEdgePort(ports []string, enabled bool) (string, error) {
	cmds, err := BuildBulkPortEdgePortCommands(ports, enabled)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}
func (s *Service) SetPortEdgePort(name string, enabled bool) (string, error) {
	if !validPort(name) {
		return "", fmt.Errorf("nieprawidłowy port")
	}
	cmd := "no spanning-tree edgeport"
	if enabled {
		cmd = "spanning-tree edgeport"
	}
	return s.runConfig([]string{"configure terminal", "interface " + name, cmd, "end"})
}
