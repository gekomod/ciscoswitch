package server

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testNotifier(t *testing.T) *notifier {
	t.Helper()
	n := newNotifier(filepath.Join(t.TempDir(), "notify.json"))
	n.batchFor = 50 * time.Millisecond
	return n
}

func TestNotifySecretsNeverReturned(t *testing.T) {
	n := testNotifier(t)
	var c notifyConfig
	c.Telegram.Enabled, c.Telegram.Token, c.Telegram.ChatID = true, "123:SECRET", "-1001"
	c.Webhook.Enabled, c.Webhook.URL = true, "http://ha.local:8123/api/webhook/tajny-id"
	c.Email.Host, c.Email.Port, c.Email.From, c.Email.To, c.Email.Password = "smtp.x", 587, "a@x", "b@x", "haslo"
	if err := n.update(c, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(n.view())
	for _, s := range []string{"SECRET", "tajny-id", "haslo"} {
		if strings.Contains(string(b), s) {
			t.Fatalf("secret %q leaked: %s", s, b)
		}
	}
	// zapis bez sekretów = bez zmian, a plik ma uprawnienia tylko dla właściciela
	c.Telegram.Token, c.Webhook.URL, c.Email.Password = "", "", ""
	if err := n.update(c, nil); err != nil || n.cfg.Telegram.Token != "123:SECRET" || n.cfg.Email.Password != "haslo" {
		t.Fatalf("empty secret must keep the old one: %v", err)
	}
	if st, _ := os.Stat(n.path); st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", st.Mode().Perm())
	}
	if err := n.update(c, []string{"emailPassword"}); err != nil || n.cfg.Email.Password != "" {
		t.Fatal("clear secret")
	}
}

func TestNotifyValidation(t *testing.T) {
	n := testNotifier(t)
	bad := []func(*notifyConfig){
		func(c *notifyConfig) {
			c.Telegram.Enabled, c.Telegram.Token, c.Telegram.ChatID = true, "bez-dwukropka", "1"
		},
		func(c *notifyConfig) { c.Telegram.Enabled, c.Telegram.Token, c.Telegram.ChatID = true, "1:a", "abc" },
		func(c *notifyConfig) { c.Webhook.Enabled, c.Webhook.URL = true, "ftp://x" },
		func(c *notifyConfig) { c.Email.Enabled, c.Email.Host = true, "" },
		func(c *notifyConfig) {
			c.Email.Enabled, c.Email.Host, c.Email.Port, c.Email.From, c.Email.To = true, "s", 25, "a@x", "b@x\r\nBcc: x@y"
		},
		func(c *notifyConfig) { c.Rules.Ports = "1;reload" },
	}
	for i, f := range bad {
		var c notifyConfig
		f(&c)
		if err := n.update(c, nil); err == nil {
			t.Errorf("case %d must be rejected", i)
		}
	}
}

func TestNotifyRulesBatchingCooldown(t *testing.T) {
	n := testNotifier(t)
	n.cfg.Rules.Ports = "1,49"
	var mu sync.Mutex
	var got []map[string]any
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	}))
	defer hook.Close()
	n.cfg.Webhook.Enabled, n.cfg.Webhook.URL = true, hook.URL
	now := time.Now()
	recs := []monitorRecord{
		{Kind: "port", Port: "port1.0.1", State: "down"},
		{Kind: "port", Port: "port1.0.7", State: "down"}, // nieobserwowany port
		{Kind: "port", Port: "port1.0.1", State: "down"}, // powtórka w cooldown
		{Kind: "event", Type: "error", Message: "Utracono połączenie SNMP ze switchem"},
		{Kind: "counter", Port: "port1.0.23", ErrorRXDelta: 42},
	}
	for _, r := range recs {
		k, txt := n.messageFor(r)
		n.enqueue(k, txt, now)
	}
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected one batched message, got %d", len(got))
	}
	msg := got[0]["message"].(string)
	if strings.Count(msg, "port1.0.1") != 1 || strings.Contains(msg, "port1.0.7") || !strings.Contains(msg, "SNMP") || !strings.Contains(msg, "przybyły 42 błędy") {
		t.Fatalf("message:\n%s", msg)
	}
}

func TestTelegramSend(t *testing.T) {
	n := testNotifier(t)
	var path string
	var body map[string]any
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		json.NewDecoder(r.Body).Decode(&body)
		if strings.Contains(path, "zly") {
			w.WriteHeader(401)
			w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
		}
	}))
	defer tg.Close()
	n.telegramBase = tg.URL
	n.cfg.Telegram.Enabled, n.cfg.Telegram.Token, n.cfg.Telegram.ChatID = true, "1:ok", "42"
	res := n.send("T", "treść")
	if !res["telegram"].OK || path != "/bot1:ok/sendMessage" || body["chat_id"] != "42" || body["text"] != "T\ntreść" {
		t.Fatalf("%v %s %v", res, path, body)
	}
	n.cfg.Telegram.Token = "1:zly"
	res = n.send("T", "x")
	if res["telegram"].OK || !strings.Contains(res["telegram"].Error, "Unauthorized") || strings.Contains(res["telegram"].Error, "1:zly") {
		t.Fatalf("error must be readable and must not contain token: %+v", res)
	}
}

// Minimalny serwer SMTP (bez TLS) do sprawdzenia treści wiadomości.
func TestEmailSend(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	data := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		io.WriteString(c, "220 test\r\n")
		var buf strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					data <- buf.String()
					io.WriteString(c, "250 ok\r\n")
					continue
				}
				buf.WriteString(line)
				continue
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				io.WriteString(c, "250 test\r\n")
			case strings.HasPrefix(line, "DATA"):
				inData = true
				io.WriteString(c, "354 go\r\n")
			case strings.HasPrefix(line, "QUIT"):
				io.WriteString(c, "221 bye\r\n")
				return
			default:
				io.WriteString(c, "250 ok\r\n")
			}
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	var c notifyConfig
	c.Email.Host, c.Email.From, c.Email.To = host, "panel@dom.lan", "ja@dom.lan"
	c.Email.Port = mustAtoi(port)
	if err := sendEmail(c, "Switch GS948MX", "🔻 port1.0.1: łącze w dół"); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-data:
		if !strings.Contains(m, "To: ja@dom.lan") || !strings.Contains(m, "Subject: Switch GS948MX") || !strings.Contains(m, "łącze w dół") {
			t.Fatalf("mail:\n%s", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no mail")
	}
}

func mustAtoi(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}
