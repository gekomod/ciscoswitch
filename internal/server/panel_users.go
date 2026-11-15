package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type panelIdentity struct {
	UserID   string
	Username string
	Role     string
}
type identityContextKey struct{}
type panelUser struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"passwordHash"`
	Role         string    `json:"role"`
	Active       bool      `json:"active"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}
type panelUserView struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type panelSession struct {
	Identity  panelIdentity
	ExpiresAt time.Time
}
type panelUserStore struct {
	mu       sync.Mutex
	path     string
	users    map[string]panelUser
	sessions map[string]panelSession
	loadErr  error
	limiter  loginLimiter
	// setupCode: jednorazowy kod instalatora wypisywany w logu, gdy nie ma kont.
	setupCode string
}

// panelPasswordMin to minimalna długość hasła konta panelu.
const panelPasswordMin = 8
const panelSessionTTL = 8 * time.Hour

var panelUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,31}$`)

func validPanelCredentials(username, password string) error {
	if !panelUsernamePattern.MatchString(username) {
		return fmt.Errorf("login: 3–32 znaki (litery, cyfry, kropka, myślnik, podkreślenie)")
	}
	if len([]rune(password)) < panelPasswordMin {
		return fmt.Errorf("hasło musi mieć co najmniej %d znaków", panelPasswordMin)
	}
	if len(password) > 72 {
		return fmt.Errorf("hasło może mieć najwyżej 72 bajty")
	}
	return nil
}

func newPanelUserStore(path string) *panelUserStore {
	s := &panelUserStore{path: path, users: map[string]panelUser{}, sessions: map[string]panelSession{}}
	if err := s.load(); err != nil {
		// Uszkodzony rejestr nie może wyglądać jak „brak kont”, bo instalator
		// pozwoliłby wtedy komukolwiek utworzyć administratora.
		s.loadErr = err
		log.Printf("Nie można odczytać rejestru użytkowników %s: %v", path, err)
	}
	return s
}

// needsSetup: brak jakiegokolwiek konta (pierwsze uruchomienie).
func (s *panelUserStore) needsSetup() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadErr == nil && len(s.users) == 0
}

func (s *panelUserStore) newSession(w http.ResponseWriter, r *http.Request, u panelUser) bool {
	token := randomPanelToken()
	if token == "" {
		return false
	}
	s.mu.Lock()
	now := time.Now()
	for k, v := range s.sessions {
		if now.After(v.ExpiresAt) {
			delete(s.sessions, k)
		}
	}
	s.sessions[token] = panelSession{Identity: panelIdentity{UserID: u.ID, Username: u.Username, Role: u.Role}, ExpiresAt: now.Add(panelSessionTTL)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "panel_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: int(panelSessionTTL.Seconds())})
	return true
}

// loginLimiter ogranicza próby logowania (i kodu instalatora) z jednego adresu:
// po 10 nieudanych próbach w ciągu 15 minut kolejne są odrzucane do końca okna.
type loginLimiter struct {
	mu   sync.Mutex
	hits map[string]loginHits
}
type loginHits struct {
	count int
	first time.Time
}

const loginMaxFailures = 10
const loginWindow = 15 * time.Minute

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	h, ok := l.hits[ip]
	if !ok {
		return false
	}
	if time.Since(h.first) > loginWindow {
		delete(l.hits, ip)
		return false
	}
	return h.count >= loginMaxFailures
}
func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string]loginHits{}
	}
	h := l.hits[ip]
	if h.first.IsZero() || time.Since(h.first) > loginWindow {
		h = loginHits{first: time.Now()}
	}
	h.count++
	l.hits[ip] = h
}
func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	delete(l.hits, ip)
	l.mu.Unlock()
}

// sameOrigin odrzuca zmiany wysłane z obcej strony (CSRF). Żądania bez
// nagłówka Origin (curl, starsze przeglądarki) są przepuszczane — ciasteczko
// sesji i tak ma SameSite=Strict.
func sameOrigin(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host) && (u.Scheme == "http" || u.Scheme == "https")
}

// AuditUser to miejsce na nazwę zalogowanego użytkownika dla dziennika
// operacji (main.go). Middleware wypełnia je po rozpoznaniu sesji.
type AuditUser struct{ Name string }
type auditUserKey struct{}

// WithAuditUser dołącza do żądania pole, które middleware uzupełni loginem.
func WithAuditUser(r *http.Request) (*http.Request, *AuditUser) {
	slot := &AuditUser{}
	return r.WithContext(context.WithValue(r.Context(), auditUserKey{}, slot)), slot
}

// SetAuditUser wpisuje login do dziennika operacji (gdy żądanie przeszło przez WithAuditUser).
func SetAuditUser(r *http.Request, name string) {
	if slot, ok := r.Context().Value(auditUserKey{}).(*AuditUser); ok {
		slot.Name = name
	}
}
func (s *panelUserStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		// Backward-compatible bootstrap: keep the credentials used by earlier
		// releases (PANEL_USER/PANEL_PASSWORD). PANEL_ADMIN_* remains supported.
		username := strings.TrimSpace(os.Getenv("PANEL_USER"))
		password := os.Getenv("PANEL_PASSWORD")
		if v := strings.TrimSpace(os.Getenv("PANEL_ADMIN_USER")); v != "" {
			username = v
		}
		if v := os.Getenv("PANEL_ADMIN_PASSWORD"); v != "" {
			password = v
		}
		if username != "" && validPanelCredentials(username, password) == nil {
			hash, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if e != nil {
				return e
			}
			role := strings.TrimSpace(os.Getenv("PANEL_ROLE"))
			if !validPanelRole(role) {
				role = "admin"
			}
			now := time.Now().UTC()
			id := randomPanelToken()
			s.users[id] = panelUser{ID: id, Username: username, PasswordHash: string(hash), Role: role, Active: true, CreatedAt: now, UpdatedAt: now}
			return s.writeLocked()
		}
		return nil
	}
	if err != nil {
		return err
	}
	var rows []panelUser
	if err = json.Unmarshal(raw, &rows); err != nil {
		return err
	}
	for _, u := range rows {
		if u.ID == "" || u.Username == "" || u.PasswordHash == "" || !validPanelRole(u.Role) {
			return fmt.Errorf("nieprawidłowy wpis użytkownika %q", u.Username)
		}
		s.users[u.ID] = u
	}
	if len(s.users) == 0 {
		return fmt.Errorf("rejestr jest pusty; usuń plik, aby uruchomić instalator")
	}
	return nil
}
func (s *panelUserStore) writeLocked() error {
	rows := make([]panelUser, 0, len(s.users))
	for _, u := range s.users {
		rows = append(rows, u)
	}
	raw, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".panel-users-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err = tmp.Chmod(0600); err != nil {
		return err
	}
	if _, err = tmp.Write(raw); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, s.path); err != nil {
		return err
	}
	ok = true
	return os.Chmod(s.path, 0600)
}
func randomPanelToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}
func validPanelRole(role string) bool {
	return role == "viewer" || role == "operator" || role == "admin"
}
func (s *panelUserStore) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	ip := clientIP(r)
	if s.limiter.blocked(ip) {
		http.Error(w, "Zbyt wiele nieudanych prób logowania. Spróbuj ponownie za kilkanaście minut.", http.StatusTooManyRequests)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2048)
	var q struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&q) != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	name := strings.TrimSpace(q.Username)
	if panelUsernamePattern.MatchString(name) {
		SetAuditUser(r, name)
	}
	s.mu.Lock()
	var found panelUser
	for _, u := range s.users {
		if strings.EqualFold(u.Username, name) {
			found = u
			break
		}
	}
	s.mu.Unlock()
	hash := found.PasswordHash
	if hash == "" {
		// Podobny czas odpowiedzi dla nieistniejącego loginu.
		hash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"
	}
	passErr := bcrypt.CompareHashAndPassword([]byte(hash), []byte(q.Password))
	if found.ID == "" || !found.Active || passErr != nil {
		s.limiter.fail(ip)
		http.Error(w, "Nieprawidłowy login lub hasło", 401)
		return
	}
	s.limiter.reset(ip)
	if !s.newSession(w, r, found) {
		http.Error(w, "session creation failed", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
func (s *panelUserStore) identity(r *http.Request) (panelIdentity, bool) {
	c, err := r.Cookie("panel_session")
	if err != nil {
		return panelIdentity{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[c.Value]
	if !ok {
		return panelIdentity{}, false
	}
	if time.Now().After(session.ExpiresAt) {
		delete(s.sessions, c.Value)
		return panelIdentity{}, false
	}
	id := session.Identity
	u, exists := s.users[id.UserID]
	if !exists || !u.Active {
		return panelIdentity{}, false
	}
	id.Role = u.Role
	id.Username = u.Username
	return id, true
}
func (s *panelUserStore) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setup := s.needsSetup()
		switch r.URL.Path {
		case "/login.css", "/login.js", "/setup.css", "/setup.js":
			// Login assets are public, but remain same-origin and are covered by CSP.
			next.ServeHTTP(w, r)
			return
		case "/setup", "/setup.html":
			if !setup {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			r.URL.Path = "/setup.html"
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
			return
		case "/login":
			if setup {
				http.Redirect(w, r, "/setup", http.StatusSeeOther)
				return
			}
			// Keep the login page reachable even if the outer router sends it through
			// the authenticated catch-all handler. Otherwise an unauthenticated
			// request can redirect /login back to /login indefinitely.
			panelLoginPageHandler(w, r)
			return
		case "/api/login":
			s.login(w, r)
			return
		case "/api/logout":
			if r.Method != "POST" {
				http.Error(w, "method not allowed", 405)
				return
			}
			if !sameOrigin(r) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
			if c, e := r.Cookie("panel_session"); e == nil {
				s.mu.Lock()
				delete(s.sessions, c.Value)
				s.mu.Unlock()
			}
			http.SetCookie(w, &http.Cookie{Name: "panel_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
			w.WriteHeader(204)
			return
		case "/api/session":
			if r.Method != "GET" {
				http.Error(w, "method not allowed", 405)
				return
			}
			id, ok := s.identity(r)
			if !ok {
				http.Error(w, "unauthorized", 401)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": id.UserID, "username": id.Username, "role": id.Role})
			return
		}
		id, ok := s.identity(r)
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "unauthorized", 401)
				return
			}
			target := "/login"
			if setup {
				target = "/setup"
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		SetAuditUser(r, id.Username)
		if !sameOrigin(r) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		if id.Role == "viewer" && r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "forbidden: rola Podgląd nie może zmieniać konfiguracji", 403)
			return
		}
		if (strings.HasPrefix(r.URL.Path, "/api/users") || strings.HasPrefix(r.URL.Path, "/api/settings") || strings.HasPrefix(r.URL.Path, "/api/notify") || strings.HasPrefix(r.URL.Path, "/api/connection")) && id.Role != "admin" {
			http.Error(w, "forbidden", 403)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityContextKey{}, id)))
	})
}
func (s *panelUserStore) usersHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/api/users" {
			if r.Method == "GET" {
				s.mu.Lock()
				rows := make([]panelUserView, 0, len(s.users))
				for _, u := range s.users {
					rows = append(rows, panelUserView{ID: u.ID, Username: u.Username, Role: u.Role, Active: u.Active, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt})
				}
				s.mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]any{"users": rows})
				return
			}
			if r.Method != "POST" {
				http.Error(w, "method not allowed", 405)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			var q struct {
				Username string `json:"username"`
				Password string `json:"password"`
				Role     string `json:"role"`
			}
			if json.NewDecoder(r.Body).Decode(&q) != nil || !validPanelRole(q.Role) {
				http.Error(w, "nieprawidłowe dane lub rola", 400)
				return
			}
			q.Username = strings.TrimSpace(q.Username)
			if err := validPanelCredentials(q.Username, q.Password); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(q.Password), bcrypt.DefaultCost)
			if err != nil {
				http.Error(w, "password hashing failed", 500)
				return
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			for _, u := range s.users {
				if strings.EqualFold(u.Username, q.Username) {
					http.Error(w, "login już istnieje", 409)
					return
				}
			}
			now := time.Now().UTC()
			id := randomPanelToken()
			s.users[id] = panelUser{ID: id, Username: q.Username, PasswordHash: string(hash), Role: q.Role, Active: true, CreatedAt: now, UpdatedAt: now}
			if err = s.writeLocked(); err != nil {
				delete(s.users, id)
				http.Error(w, "nie można zapisać konta", 500)
				return
			}
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"created": true, "id": id})
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/users/") {
			http.NotFound(w, r)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/users/")
		if id == "" || strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		if r.Method != "PATCH" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var q struct {
			Role     string `json:"role"`
			Active   *bool  `json:"active"`
			Password string `json:"password"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		u, ok := s.users[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if q.Role != "" && !validPanelRole(q.Role) {
			http.Error(w, "invalid role", 400)
			return
		}
		if q.Password != "" {
			if err := validPanelCredentials(u.Username, q.Password); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		}
		losesAdmin := u.Role == "admin" && u.Active && ((q.Role != "" && q.Role != "admin") || (q.Active != nil && !*q.Active))
		if losesAdmin {
			admins := 0
			for _, x := range s.users {
				if x.Role == "admin" && x.Active {
					admins++
				}
			}
			if admins <= 1 {
				http.Error(w, "nie można zablokować ani zdegradować ostatniego aktywnego administratora", 409)
				return
			}
		}
		if q.Role != "" {
			u.Role = q.Role
		}
		if q.Active != nil {
			u.Active = *q.Active
		}
		if q.Password != "" {
			hash, err := bcrypt.GenerateFromPassword([]byte(q.Password), bcrypt.DefaultCost)
			if err != nil {
				http.Error(w, "password hashing failed", 500)
				return
			}
			u.PasswordHash = string(hash)
		}
		u.UpdatedAt = time.Now().UTC()
		s.users[id] = u
		if err := s.writeLocked(); err != nil {
			http.Error(w, "nie można zapisać zmian", 500)
			return
		}
		if !u.Active || q.Password != "" {
			// Po zablokowaniu konta lub zmianie hasła wylogowujemy inne sesje
			// tego konta (bieżąca sesja zostaje, chyba że konto zablokowano).
			current := ""
			if c, err := r.Cookie("panel_session"); err == nil {
				current = c.Value
			}
			for token, identity := range s.sessions {
				if identity.Identity.UserID == id && (token != current || !u.Active) {
					delete(s.sessions, token)
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"saved": true})
	})
}
func panelLoginPageHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="pl">
<head>
 <meta charset="utf-8">
 <meta name="viewport" content="width=device-width,initial-scale=1">
 <meta name="color-scheme" content="light">
 <title>Logowanie · Allied Network</title>
 <link rel="stylesheet" href="/login.css">
 <script src="/login.js" defer></script>
</head>
<body>
 <main class="login-shell">
  <section class="login-card">
   <div class="brand">
    <div class="brand-mark" aria-hidden="true">AN</div>
    <div><b>Allied Network</b><small>NETWORK MANAGEMENT</small></div>
   </div>
   <div class="login-intro">
    <span class="eyebrow">PANEL ADMINISTRACYJNY</span>
    <h1>Witaj ponownie</h1>
    <p>Zaloguj się do lokalnego panelu zarządzania przełącznikiem.</p>
   </div>
   <form id="login-form">
    <label for="username">Login</label>
    <input id="username" name="username" type="text" autocomplete="username" required autofocus>
    <label for="password">Hasło</label>
    <input id="password" name="password" type="password" autocomplete="current-password" required>
    <button id="login-button" type="submit">Zaloguj się <span>→</span></button>
    <p id="login-error" class="error" role="alert" aria-live="polite"></p>
   </form>
   <div class="login-footer"><i></i><span>Bezpieczne połączenie lokalne</span><small>Dostęp tylko dla upoważnionych użytkowników</small></div>
  </section>
  <aside class="login-side">
   <div class="side-orb orb-one"></div><div class="side-orb orb-two"></div>
   <div class="side-content">
    <span>NETWORK CONTROL CENTER</span>
    <h2>Pełna kontrola.<br>Jedno miejsce.</h2>
    <p>Zarządzaj infrastrukturą sieciową, monitoruj porty i konfigurację przełącznika z jednego, przejrzystego panelu.</p>
    <div class="side-device"><div class="device-lights"><i></i><i></i><i></i><i></i><i></i></div><b>SWITCH MANAGEMENT</b><small>SECURE LOCAL ACCESS</small></div>
   </div>
  </aside>
 </main>
</body>
</html>`))
}
