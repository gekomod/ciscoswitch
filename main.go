package main

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gs948mx-manager/internal/device"
	"gs948mx-manager/internal/server"
	"gs948mx-manager/internal/settings"
)

func main() {
	if err := loadEnv(".env"); err != nil && !os.IsNotExist(err) {
		log.Fatalf("Nie można odczytać .env: %v", err)
	}
	settingsPath := env("PANEL_SETTINGS_PATH", filepath.Join("data", "settings.json"))
	if err := settings.Load(settingsPath); err != nil {
		log.Fatalf("Nie można odczytać ustawień: %v", err)
	}
	// Dane dostępu do switcha: data/connection.json (instalator / panel),
	// a gdy pliku nie ma — zmienne SWITCH_* z .env.
	if err := settings.LoadConnection(env("PANEL_CONNECTION_PATH", filepath.Join("data", "connection.json"))); err != nil {
		log.Fatalf("Nie można odczytać połączenia ze switchem: %v", err)
	}
	cfg := device.Config{
		Timeout:        4 * time.Second,
		StatusCacheTTL: cacheTTL("STATUS_CACHE_TTL"),
		ReadCacheTTL:   cacheTTL("READ_CACHE_TTL"),
		CommandTimeout: commandTimeout(),
	}
	settings.CurrentConnection().Apply(&cfg)
	svc := device.New(cfg)
	device.SetPerfLog(settings.Bool("PERF_LOG"))
	settings.OnChange(func() {
		svc.SetTuning(tuningFromSettings())
		device.SetPerfLog(settings.Bool("PERF_LOG"))
		log.Printf("Zastosowano nowe ustawienia panelu")
	})
	defer svc.Close()
	addr := env("LISTEN_ADDR", "127.0.0.1:8090")
	mux := http.NewServeMux()
	// Logowanie, użytkownicy i instalator pierwszego uruchomienia są w server.Register.
	server.Register(mux, svc)
	handler := securityHeaders(auditLog(mux, env("AUDIT_LOG_PATH", filepath.Join("data", "audit.jsonl"))))
	log.Printf("Panel GS948MX nasłuchuje na http://%s (switch=%s, snmp=%s, połączenie z: %s)", addr, orNone(cfg.Host), snmpMode(cfg), settings.ConnectionSource())
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	diagnosticsPath := env("DIAGNOSTICS_HISTORY_PATH", filepath.Join("data", "diagnostics-history.json"))
	diagnosticsInterval := 60 * time.Second
	if raw := strings.TrimSpace(os.Getenv("DIAGNOSTICS_INTERVAL")); raw != "" {
		if raw == "0" {
			diagnosticsInterval = 0
		} else if parsed, parseErr := time.ParseDuration(raw); parseErr != nil || parsed < 10*time.Second {
			log.Printf("Nieprawidłowy DIAGNOSTICS_INTERVAL=%q; używam 60s (minimum 10s, 0 wyłącza sampler)", raw)
		} else {
			diagnosticsInterval = parsed
		}
	}
	collectorCtx, cancelCollector := context.WithCancel(context.Background())
	server.StartDiagnosticsCollector(collectorCtx, svc.RunReadOnly, diagnosticsPath, diagnosticsInterval)
	monitorInterval := 30 * time.Second
	if raw := strings.TrimSpace(os.Getenv("MONITOR_INTERVAL")); raw != "" {
		if raw == "0" {
			monitorInterval = 0
		} else if parsed, parseErr := time.ParseDuration(raw); parseErr != nil || parsed < 10*time.Second {
			log.Printf("Nieprawidłowy MONITOR_INTERVAL=%q; używam 30s (minimum 10s, 0 wyłącza sampler)", raw)
		} else {
			monitorInterval = parsed
		}
	}
	monitorPath := env("MONITOR_HISTORY_PATH", filepath.Join("data", "monitor-history.jsonl"))
	server.StartMonitorCollector(collectorCtx, svc, monitorPath, monitorInterval)
	if monitorInterval > 0 {
		log.Printf("Switch monitor collector enabled (interval=%s, history=%s)", monitorInterval, monitorPath)
	} else {
		log.Printf("Switch monitor collector disabled")
	}
	if diagnosticsInterval > 0 {
		log.Printf("Switch diagnostics sampler enabled (interval=%s, history=%s)", diagnosticsInterval, diagnosticsPath)
	} else {
		log.Printf("Switch diagnostics sampler disabled")
	}
	err := httpServer.ListenAndServe()
	cancelCollector()
	if err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
func loadEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if k != "" && os.Getenv(k) == "" {
			if err := os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}
func env(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
func snmpMode(c device.Config) string {
	v := strings.TrimSpace(c.SNMPVersion)
	if v == "" {
		return "2c"
	}
	return v
}
func orNone(v string) string {
	if v == "" {
		return "nieustawiony"
	}
	return v
}
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}

type auditRecord struct {
	Timestamp  time.Time `json:"timestamp"`
	Username   string    `json:"username"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMS int64     `json:"durationMs"`
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap pozwala http.ResponseController wydłużyć limity czasu dla długich operacji
// (wgrywanie firmware).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

var auditMu sync.Mutex

// auditLog records request metadata only; it never stores request bodies, CLI output,
// passwords, SNMP communities, query strings, or configuration contents.
func auditLog(next http.Handler, path string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /api/monitor/history: wpisy wysyłane automatycznie przez przeglądarkę,
		// zalałyby dziennik i ukryły prawdziwe zmiany.
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/monitor/history" || (r.Method != "POST" && r.Method != "PUT" && r.Method != "PATCH" && r.Method != "DELETE") {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		r, who := server.WithAuditUser(r)
		next.ServeHTTP(sw, r)
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		username := "anonim"
		if who.Name != "" {
			username = who.Name
		}
		rec := auditRecord{Timestamp: time.Now().UTC(), Username: username, Method: r.Method, Path: r.URL.Path, Status: status, DurationMS: time.Since(started).Milliseconds()}
		auditMu.Lock()
		defer auditMu.Unlock()
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0700); err != nil {
			log.Printf("audit log unavailable: %v", err)
			return
		}
		if err := os.Chmod(dir, 0700); err != nil {
			log.Printf("audit directory permissions: %v", err)
			return
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			log.Printf("audit log unavailable: %v", err)
			return
		}
		if err := file.Chmod(0600); err != nil {
			_ = file.Close()
			log.Printf("audit file permissions: %v", err)
			return
		}
		encErr := json.NewEncoder(file).Encode(rec)
		closeErr := file.Close()
		if encErr != nil {
			log.Printf("audit write failed: %v", encErr)
		} else if closeErr != nil {
			log.Printf("audit close failed: %v", closeErr)
		}
	})
}

// cacheTTL czyta czas życia cache z .env: pusty = domyślne 5 s, "0" lub "off" wyłącza cache.
func cacheTTL(name string) time.Duration {
	raw := strings.ToLower(strings.TrimSpace(settings.Get(name)))
	switch raw {
	case "":
		return 0
	case "0", "0s", "off", "false":
		return -1
	}
	if !strings.HasSuffix(raw, "s") && !strings.HasSuffix(raw, "m") {
		raw += "s"
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 || d > 5*time.Minute {
		log.Printf("Nieprawidłowe %s=%q; używam wartości domyślnej 5s", name, raw)
		return 0
	}
	return d
}

// commandTimeout czyta SSH_COMMAND_TIMEOUT (np. 30s); puste lub błędne = domyślne 30 s.
func commandTimeout() time.Duration {
	raw := strings.TrimSpace(settings.Get("SSH_COMMAND_TIMEOUT"))
	if raw == "" {
		return 0
	}
	if !strings.HasSuffix(raw, "s") && !strings.HasSuffix(raw, "m") {
		raw += "s"
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 5*time.Second || d > 5*time.Minute {
		log.Printf("Nieprawidłowe SSH_COMMAND_TIMEOUT=%q (zakres 5s–5m); używam 30s", raw)
		return 0
	}
	return d
}

// tuningFromSettings zbiera ustawienia, które panel stosuje bez restartu.
func tuningFromSettings() device.Tuning {
	return device.Tuning{
		StatusCacheTTL:        cacheTTL("STATUS_CACHE_TTL"),
		ReadCacheTTL:          cacheTTL("READ_CACHE_TTL"),
		CommandTimeout:        commandTimeout(),
		SNMPV3PrivacyProtocol: settings.Get("SWITCH_SNMP_V3_PRIV_PROTOCOL"),
	}
}
