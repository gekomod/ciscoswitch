package device

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestConfigScriptAddsPagingAndExit(t *testing.T) {
	got := configScript([]string{"configure terminal", "interface port1.0.5", "no shutdown", "end"})
	want := []string{"terminal length 0", "configure terminal", "interface port1.0.5", "no shutdown", "end", "exit"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	got = configScript([]string{"configure terminal", "vlan database", "vlan 10"})
	if got[len(got)-2] != "end" || got[len(got)-1] != "exit" {
		t.Fatalf("missing end/exit: %v", got)
	}
	got = configScript([]string{"copy running-config startup-config"})
	want = []string{"terminal length 0", "copy running-config startup-config", "exit"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestCLIError(t *testing.T) {
	if cliError("awplus(config)#\n% Invalid input detected at '^' marker.\n") == "" {
		t.Fatal("expected invalid input to be detected")
	}
	if cliError("% Warning: something informational\nawplus#") != "" {
		t.Fatal("warning must not be treated as error")
	}
	if cliError("Building configuration...\n[OK]\n") != "" {
		t.Fatal("unexpected error")
	}
}

func TestReadCacheGenerationGuard(t *testing.T) {
	var c readCache
	gen := c.gen()
	c.invalidate() // zmiana konfiguracji w trakcie odczytu
	c.put("show vlan all", "stare", gen)
	if _, ok := c.get("show vlan all", time.Minute); ok {
		t.Fatal("stale read must not be cached after invalidation")
	}
	c.put("show vlan all", "nowe", c.gen())
	if v, ok := c.get("show vlan all", time.Minute); !ok || v != "nowe" {
		t.Fatalf("got %q %v", v, ok)
	}
	if _, ok := c.get("show vlan all", 0); ok {
		t.Fatal("ttl 0 must disable cache")
	}
}

func TestFlightGroupDeduplicates(t *testing.T) {
	var g flightGroup
	var calls int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = g.do("k", func() (any, error) {
				atomic.AddInt32(&calls, 1)
				<-release
				return "x", nil
			})
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

// fakeSwitch to minimalny serwer SSH udający AlliedWare Plus: exec zwraca
// tekst, a shell wykonuje linie do polecenia "exit".
type fakeSwitch struct {
	addr        string
	fingerprint string
	conns       int32
	execs       int32
	lines       chan string
}

func startFakeSwitch(t *testing.T) *fakeSwitch {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) { return nil, nil }}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeSwitch{addr: ln.Addr().String(), fingerprint: ssh.FingerprintSHA256(signer.PublicKey()), lines: make(chan string, 64)}
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt32(&f.conns, 1)
			go f.serve(nc, cfg)
		}
	}()
	return f
}

func (f *fakeSwitch) serve(nc net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nch := range chans {
		ch, requests, err := nch.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range requests {
				switch req.Type {
				case "exec":
					atomic.AddInt32(&f.execs, 1)
					cmd := string(req.Payload[4:])
					req.Reply(true, nil)
					if cmd == "show hang" {
						select {} // switch zawieszony: nigdy nie odpowiada
					}
					ch.Write([]byte("output of " + cmd + "\n"))
					ch.SendRequest("exit-status", false, ssh.Marshal(struct{ C uint32 }{0}))
					return
				case "shell":
					req.Reply(true, nil)
					sc := bufio.NewScanner(ch)
					for sc.Scan() {
						line := strings.TrimSpace(sc.Text())
						f.lines <- line
						if line == "vlan 4000" {
							ch.Write([]byte("% Invalid input detected at '^' marker.\n"))
						}
						if line == "exit" {
							ch.SendRequest("exit-status", false, ssh.Marshal(struct{ C uint32 }{0}))
							return
						}
					}
					return // bez "exit" sesja wisiałaby do timeoutu
				default:
					req.Reply(false, nil)
				}
			}
		}()
	}
}

func fakeService(f *fakeSwitch) *Service {
	host, port, _ := net.SplitHostPort(f.addr)
	return New(Config{Host: host, SSHPort: port, SSHUser: "manager", SSHPassword: "x", SSHHostKeyFingerprint: f.fingerprint, Timeout: 2 * time.Second})
}

func TestReadsReuseOneSSHConnectionAndCache(t *testing.T) {
	f := startFakeSwitch(t)
	s := fakeService(f)
	defer s.Close()
	for _, cmd := range []string{"show version", "show vlan all", "show version", "show vlan all", "show lldp neighbors"} {
		out, err := s.RunReadOnly(cmd)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, cmd) {
			t.Fatalf("unexpected output %q", out)
		}
	}
	if f.conns != 1 {
		t.Fatalf("expected 1 SSH connection, got %d", f.conns)
	}
	if f.execs != 3 {
		t.Fatalf("expected 3 commands sent (rest from cache), got %d", f.execs)
	}
	if _, err := s.RunReadOnlyFresh("show version"); err != nil {
		t.Fatal(err)
	}
	if f.execs != 4 {
		t.Fatalf("fresh read must bypass cache, execs=%d", f.execs)
	}
}

func TestConfigChangeFinishesQuicklyAndInvalidatesCache(t *testing.T) {
	f := startFakeSwitch(t)
	s := fakeService(f)
	defer s.Close()
	if _, err := s.RunReadOnly("show vlan all"); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := s.ConfigurePort("port1.0.5", "enable"); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(started); d > 3*time.Second {
		t.Fatalf("config took %s; session should end on exit", d)
	}
	var sent []string
	for len(f.lines) > 0 {
		sent = append(sent, <-f.lines)
	}
	if sent[0] != "terminal length 0" || sent[len(sent)-1] != "exit" {
		t.Fatalf("unexpected script %v", sent)
	}
	before := f.execs
	if _, err := s.RunReadOnly("show vlan all"); err != nil {
		t.Fatal(err)
	}
	if f.execs != before+1 {
		t.Fatal("read after change must not come from cache")
	}
}

func TestConfigErrorIsReported(t *testing.T) {
	f := startFakeSwitch(t)
	s := fakeService(f)
	defer s.Close()
	_, err := s.runConfig([]string{"configure terminal", "vlan database", "vlan 4000", "end"})
	if err == nil || !strings.Contains(err.Error(), "Invalid input") {
		t.Fatalf("expected CLI error, got %v", err)
	}
}

func TestPoolReconnectsAfterConnectionLoss(t *testing.T) {
	f := startFakeSwitch(t)
	s := fakeService(f)
	defer s.Close()
	if _, err := s.RunReadOnly("show version"); err != nil {
		t.Fatal(err)
	}
	s.ssh.mu.Lock()
	s.ssh.client.Conn.Close() // switch zerwał połączenie
	s.ssh.mu.Unlock()
	if _, err := s.RunReadOnlyFresh("show version"); err != nil {
		t.Fatalf("expected transparent reconnect, got %v", err)
	}
	if f.conns != 2 {
		t.Fatalf("expected reconnect, conns=%d", f.conns)
	}
}

func TestHungCommandDoesNotBlockPanel(t *testing.T) {
	f := startFakeSwitch(t)
	s := fakeService(f)
	s.cfg.CommandTimeout = 300 * time.Millisecond
	defer s.Close()
	started := time.Now()
	if _, err := s.readFromSwitch("show hang"); err == nil || !strings.Contains(err.Error(), "nie odpowiedział") {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if d := time.Since(started); d > 2*time.Second {
		t.Fatalf("timeout took %s", d)
	}
	// Panel musi dalej działać: kolejne polecenie przez nowe połączenie.
	out, err := s.RunReadOnlyFresh("show version")
	if err != nil || !strings.Contains(out, "show version") {
		t.Fatalf("panel blocked after hung command: %q %v", out, err)
	}
}
