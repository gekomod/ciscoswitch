package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gs948mx-manager/internal/device"
)

// ---------- Konfiguracja ----------

type notifyConfig struct {
	Telegram struct {
		Enabled bool   `json:"enabled"`
		Token   string `json:"token"`
		ChatID  string `json:"chatId"`
	} `json:"telegram"`
	Webhook struct {
		Enabled bool   `json:"enabled"`
		URL     string `json:"url"`
	} `json:"webhook"`
	Email struct {
		Enabled  bool   `json:"enabled"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
		From     string `json:"from"`
		To       string `json:"to"`
	} `json:"email"`
	Rules struct {
		Switch bool   `json:"switch"` // utrata i powrót połączenia ze switchem
		Ports  string `json:"ports"`  // "" = żadne, "all" = wszystkie, albo lista np. "1,49"
		Errors bool   `json:"errors"` // przyrost błędów na portach
	} `json:"rules"`
}

// Widok dla przeglądarki: bez tokenów, haseł i adresu webhooka (bywa sekretem).
type notifyView struct {
	Telegram struct {
		Enabled  bool   `json:"enabled"`
		HasToken bool   `json:"hasToken"`
		ChatID   string `json:"chatId"`
	} `json:"telegram"`
	Webhook struct {
		Enabled bool   `json:"enabled"`
		HasURL  bool   `json:"hasUrl"`
		Host    string `json:"host"`
	} `json:"webhook"`
	Email struct {
		Enabled     bool   `json:"enabled"`
		Host        string `json:"host"`
		Port        int    `json:"port"`
		User        string `json:"user"`
		HasPassword bool   `json:"hasPassword"`
		From        string `json:"from"`
		To          string `json:"to"`
	} `json:"email"`
	Rules  any                      `json:"rules"`
	Status map[string]channelStatus `json:"status"`
}

type channelStatus struct {
	At    string `json:"at"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type notifier struct {
	path   string
	mu     sync.Mutex
	cfg    notifyConfig
	status map[string]channelStatus
	client *http.Client
	// grupowanie i limit powtórzeń
	pending  []string
	timer    *time.Timer
	lastSent map[string]time.Time
	batchFor time.Duration
	cooldown time.Duration
	// adres API Telegrama (zmieniany w testach)
	telegramBase string
}

func newNotifier(path string) *notifier {
	n := &notifier{
		path: path, status: map[string]channelStatus{}, lastSent: map[string]time.Time{},
		client: &http.Client{Timeout: 10 * time.Second}, batchFor: 10 * time.Second, cooldown: 2 * time.Minute,
		telegramBase: "https://api.telegram.org",
	}
	n.cfg.Rules.Switch = true
	n.cfg.Rules.Errors = true
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &n.cfg); err != nil {
			log.Printf("notify: uszkodzony plik %s: %v", path, err)
		}
	}
	return n
}

func (n *notifier) view() notifyView {
	n.mu.Lock()
	defer n.mu.Unlock()
	var v notifyView
	c := n.cfg
	v.Telegram.Enabled, v.Telegram.HasToken, v.Telegram.ChatID = c.Telegram.Enabled, c.Telegram.Token != "", c.Telegram.ChatID
	v.Webhook.Enabled, v.Webhook.HasURL = c.Webhook.Enabled, c.Webhook.URL != ""
	if u, err := url.Parse(c.Webhook.URL); err == nil {
		v.Webhook.Host = u.Host
	}
	v.Email.Enabled, v.Email.Host, v.Email.Port, v.Email.User = c.Email.Enabled, c.Email.Host, c.Email.Port, c.Email.User
	v.Email.HasPassword, v.Email.From, v.Email.To = c.Email.Password != "", c.Email.From, c.Email.To
	v.Rules = c.Rules
	v.Status = map[string]channelStatus{}
	for k, s := range n.status {
		v.Status[k] = s
	}
	return v
}

// update stosuje zmiany z panelu. Puste token, hasło i adres webhooka
// oznaczają „bez zmian”, żeby nie trzeba było ich wpisywać ponownie.
func (n *notifier) update(in notifyConfig, clearSecrets []string) error {
	n.mu.Lock()
	next := n.cfg
	n.mu.Unlock()

	next.Telegram.Enabled, next.Telegram.ChatID = in.Telegram.Enabled, strings.TrimSpace(in.Telegram.ChatID)
	if t := strings.TrimSpace(in.Telegram.Token); t != "" {
		next.Telegram.Token = t
	}
	next.Webhook.Enabled = in.Webhook.Enabled
	if u := strings.TrimSpace(in.Webhook.URL); u != "" {
		next.Webhook.URL = u
	}
	next.Email.Enabled, next.Email.Host, next.Email.Port = in.Email.Enabled, strings.TrimSpace(in.Email.Host), in.Email.Port
	next.Email.User, next.Email.From, next.Email.To = strings.TrimSpace(in.Email.User), strings.TrimSpace(in.Email.From), strings.TrimSpace(in.Email.To)
	if p := in.Email.Password; p != "" {
		next.Email.Password = p
	}
	next.Rules = in.Rules
	for _, s := range clearSecrets {
		switch s {
		case "telegramToken":
			next.Telegram.Token = ""
		case "webhookUrl":
			next.Webhook.URL = ""
		case "emailPassword":
			next.Email.Password = ""
		}
	}
	if err := validateNotify(next); err != nil {
		return err
	}
	if err := writeSecretJSON(n.path, next); err != nil {
		return err
	}
	n.mu.Lock()
	n.cfg = next
	n.mu.Unlock()
	return nil
}

func validateNotify(c notifyConfig) error {
	if c.Telegram.Enabled {
		if c.Telegram.Token == "" || !strings.Contains(c.Telegram.Token, ":") {
			return fmt.Errorf("Telegram: podaj token bota (od @BotFather, postać 123456:ABC...)")
		}
		if _, err := strconv.ParseInt(c.Telegram.ChatID, 10, 64); err != nil {
			return fmt.Errorf("Telegram: chat ID to liczba (dla grup ujemna)")
		}
	}
	if c.Webhook.Enabled {
		u, err := url.Parse(c.Webhook.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("Webhook: podaj pełny adres http(s)://…")
		}
	}
	if c.Email.Enabled {
		if c.Email.Host == "" || c.Email.Port < 1 || c.Email.Port > 65535 || !strings.Contains(c.Email.From, "@") || !strings.Contains(c.Email.To, "@") {
			return fmt.Errorf("E-mail: podaj serwer SMTP, port, nadawcę i odbiorcę")
		}
		if strings.ContainsAny(c.Email.From+c.Email.To, "\r\n") {
			return fmt.Errorf("E-mail: nieprawidłowy adres")
		}
	}
	if p := strings.TrimSpace(c.Rules.Ports); p != "" && p != "all" {
		if _, err := device.ParsePortRanges(p); err != nil {
			return fmt.Errorf("Porty do powiadomień: %w", err)
		}
	}
	return nil
}

func writeSecretJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".notify-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ---------- Zdarzenia → wiadomości ----------

func portMatches(rule, port string) bool {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return false
	}
	if rule == "all" {
		return true
	}
	ports, err := device.ParsePortRanges(rule)
	if err != nil {
		return false
	}
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}

// messageFor zamienia zdarzenie monitoringu na linijkę powiadomienia i klucz
// do limitu powtórzeń. Zwraca pusty tekst, gdy reguły go wykluczają.
func (n *notifier) messageFor(r monitorRecord) (key, text string) {
	n.mu.Lock()
	rules := n.cfg.Rules
	n.mu.Unlock()
	switch r.Kind {
	case "event":
		switch r.Type {
		case "error", "success":
			if rules.Switch {
				mark := "🔴"
				if r.Type == "success" {
					mark = "🟢"
				}
				return "switch:" + r.Type, mark + " " + r.Message
			}
		case "warning":
			if rules.Errors {
				return "warn:" + r.Message, "⚠️ " + r.Message
			}
		}
	case "port":
		if portMatches(rules.Ports, r.Port) {
			state := "łącze w dół"
			mark := "🔻"
			if r.State == "up" {
				state, mark = "łącze w górę", "🔺"
			}
			return "port:" + r.Port + ":" + r.State, fmt.Sprintf("%s %s: %s", mark, r.Port, state)
		}
	case "counter":
		if rules.Errors {
			total := int(r.ErrorRXDelta + r.ErrorTXDelta)
			if total > 0 {
				return "err:" + r.Port, fmt.Sprintf("⚠️ %s: %s", r.Port, errorsCountPhrase(total))
			}
		}
	}
	return "", ""
}

func errorsCountPhrase(n int) string {
	t, u := n%100, n%10
	switch {
	case n == 1:
		return "przybył 1 błąd"
	case u >= 2 && u <= 4 && (t < 12 || t > 14):
		return fmt.Sprintf("przybyły %d błędy", n)
	}
	return fmt.Sprintf("przybyło %d błędów", n)
}

// enqueue dodaje linijkę do paczki wysyłanej po batchFor; pomija powtórzenia
// tego samego zdarzenia w czasie cooldown.
func (n *notifier) enqueue(key, text string, now time.Time) {
	if text == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if t, ok := n.lastSent[key]; ok && now.Sub(t) < n.cooldown {
		return
	}
	n.lastSent[key] = now
	n.pending = append(n.pending, text)
	if n.timer == nil {
		n.timer = time.AfterFunc(n.batchFor, n.flush)
	}
}

func (n *notifier) flush() {
	n.mu.Lock()
	lines := n.pending
	n.pending, n.timer = nil, nil
	n.mu.Unlock()
	if len(lines) == 0 {
		return
	}
	if len(lines) > 20 {
		lines = append(lines[:20], fmt.Sprintf("… i %d więcej", len(lines)-20))
	}
	n.send("Switch GS948MX", strings.Join(lines, "\n"))
}

// run nasłuchuje zdarzeń monitoringu do końca działania panelu.
func (n *notifier) run(ctx context.Context) {
	id, ch := sharedMonitorEventHub.subscribe()
	defer sharedMonitorEventHub.unsubscribe(id)
	for {
		select {
		case <-ctx.Done():
			return
		case r, ok := <-ch:
			if !ok {
				return
			}
			key, text := n.messageFor(r)
			n.enqueue(key, text, time.Now())
		}
	}
}

// ---------- Wysyłanie ----------

// send wysyła do wszystkich włączonych kanałów i zwraca wynik każdego.
func (n *notifier) send(title, body string) map[string]channelStatus {
	n.mu.Lock()
	c := n.cfg
	n.mu.Unlock()
	type job struct {
		name string
		fn   func() error
	}
	var jobs []job
	if c.Telegram.Enabled {
		jobs = append(jobs, job{"telegram", func() error { return n.sendTelegram(c, title, body) }})
	}
	if c.Webhook.Enabled {
		jobs = append(jobs, job{"webhook", func() error { return n.sendWebhook(c, title, body) }})
	}
	if c.Email.Enabled {
		jobs = append(jobs, job{"email", func() error { return sendEmail(c, title, body) }})
	}
	res := map[string]channelStatus{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			err := j.fn()
			st := channelStatus{At: time.Now().Format("2006-01-02 15:04:05"), OK: err == nil}
			if err != nil {
				st.Error = err.Error()
				log.Printf("notify %s: %v", j.name, err)
			}
			mu.Lock()
			res[j.name] = st
			mu.Unlock()
		}(j)
	}
	wg.Wait()
	n.mu.Lock()
	for k, v := range res {
		n.status[k] = v
	}
	n.mu.Unlock()
	return res
}

func (n *notifier) sendTelegram(c notifyConfig, title, body string) error {
	payload, _ := json.Marshal(map[string]any{"chat_id": c.Telegram.ChatID, "text": title + "\n" + body, "disable_web_page_preview": true})
	resp, err := n.client.Post(n.telegramBase+"/bot"+c.Telegram.Token+"/sendMessage", "application/json", bytes.NewReader(payload))
	if err != nil {
		// błąd sieci zawiera adres z tokenem; nie zapisujemy go w logu ani w panelu
		return fmt.Errorf("brak połączenia z api.telegram.org")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var e struct {
			Description string `json:"description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("Telegram odrzucił wiadomość (%d): %s", resp.StatusCode, e.Description)
	}
	return nil
}

func (n *notifier) sendWebhook(c notifyConfig, title, body string) error {
	payload, _ := json.Marshal(map[string]any{"source": "gs948mx-panel", "title": title, "message": body, "lines": strings.Split(body, "\n"), "timestamp": time.Now().Format(time.RFC3339)})
	resp, err := n.client.Post(c.Webhook.URL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("brak połączenia z adresem webhooka")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhook odpowiedział kodem %d", resp.StatusCode)
	}
	return nil
}

func sendEmail(c notifyConfig, title, body string) error {
	addr := net.JoinHostPort(c.Email.Host, strconv.Itoa(c.Email.Port))
	msg := "From: " + c.Email.From + "\r\nTo: " + c.Email.To + "\r\nSubject: " + mimeHeader(title) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\nDate: " +
		time.Now().Format(time.RFC1123Z) + "\r\n\r\n" + strings.ReplaceAll(body, "\n", "\r\n") + "\r\n"
	var auth smtp.Auth
	if c.Email.User != "" {
		auth = smtp.PlainAuth("", c.Email.User, c.Email.Password, c.Email.Host)
	}
	to := strings.Split(c.Email.To, ",")
	for i := range to {
		to[i] = strings.TrimSpace(to[i])
	}
	if c.Email.Port == 465 { // TLS od początku połączenia
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, &tls.Config{ServerName: c.Email.Host})
		if err != nil {
			return fmt.Errorf("SMTP: %w", err)
		}
		cl, err := smtp.NewClient(conn, c.Email.Host)
		if err != nil {
			return fmt.Errorf("SMTP: %w", err)
		}
		defer cl.Close()
		if auth != nil {
			if err := cl.Auth(auth); err != nil {
				return fmt.Errorf("SMTP logowanie: %w", err)
			}
		}
		if err := cl.Mail(c.Email.From); err != nil {
			return err
		}
		for _, r := range to {
			if err := cl.Rcpt(r); err != nil {
				return err
			}
		}
		w, err := cl.Data()
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(msg)); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		return cl.Quit()
	}
	// port 587/25: smtp.SendMail sam włącza STARTTLS, jeśli serwer go oferuje
	if err := smtp.SendMail(addr, auth, c.Email.From, to, []byte(msg)); err != nil {
		return fmt.Errorf("SMTP: %w", err)
	}
	return nil
}

func mimeHeader(s string) string {
	for _, r := range s {
		if r > 127 {
			return "=?UTF-8?B?" + base64Std(s) + "?="
		}
	}
	return s
}

// ---------- API ----------

func registerNotifyRoutes(apiMux *http.ServeMux, backupDir string) {
	path := envOr("NOTIFY_PATH", filepath.Join(filepath.Dir(backupDir), "notify.json"))
	n := newNotifier(path)
	go n.run(context.Background())

	apiMux.HandleFunc("/api/notify", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, n.view(), nil)
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			var q struct {
				Config notifyConfig `json:"config"`
				Clear  []string     `json:"clear"`
			}
			if json.NewDecoder(r.Body).Decode(&q) != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if err := n.update(q.Config, q.Clear); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, n.view(), nil)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	apiMux.HandleFunc("/api/notify/test", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		res := n.send("Switch GS948MX", "✅ Test powiadomień z panelu ("+time.Now().Format("15:04:05")+")")
		names := make([]string, 0, len(res))
		for k := range res {
			names = append(names, k)
		}
		sort.Strings(names)
		writeJSON(w, map[string]any{"results": res, "channels": names}, nil)
	})
}

func base64Std(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
