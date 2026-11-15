package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gs948mx-manager/internal/device"
	"gs948mx-manager/internal/settings"
)

// Instalator pierwszego uruchomienia: gdy nie ma żadnego konta panelu,
// /setup pozwala utworzyć administratora i ustawić połączenie ze switchem.
// Wymaga jednorazowego kodu wypisanego w logu panelu, aby nikt w sieci nie
// przejął świeżej instalacji przed właścicielem.

const setupCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newSetupCode() string {
	var b strings.Builder
	for i := 0; i < 8; i++ {
		if i == 4 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(setupCodeAlphabet))))
		if err != nil {
			return ""
		}
		b.WriteByte(setupCodeAlphabet[n.Int64()])
	}
	return b.String()
}

func normalizeSetupCode(v string) string {
	v = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(v), " ", ""))
	if len(v) == 8 && !strings.Contains(v, "-") {
		v = v[:4] + "-" + v[4:]
	}
	return v
}

// prepareSetup wypisuje kod instalatora, jeśli panel nie ma jeszcze kont.
func (s *panelUserStore) prepareSetup() {
	if !s.needsSetup() {
		return
	}
	s.mu.Lock()
	if s.setupCode == "" {
		s.setupCode = newSetupCode()
	}
	code := s.setupCode
	s.mu.Unlock()
	log.Printf("============================================================")
	log.Printf(" PIERWSZE URUCHOMIENIE: otwórz panel w przeglądarce (/setup)")
	log.Printf(" Kod instalatora: %s", code)
	log.Printf("============================================================")
}

// setupAuthorized: w trybie instalatora wymaga poprawnego kodu, po instalacji
// — zalogowanego administratora (te same narzędzia służą do zmiany połączenia).
func (s *panelUserStore) setupAuthorized(w http.ResponseWriter, r *http.Request, code string) bool {
	if !sameOrigin(r) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return false
	}
	if s.needsSetup() {
		ip := clientIP(r)
		if s.limiter.blocked(ip) {
			http.Error(w, "Zbyt wiele błędnych kodów. Spróbuj ponownie za kilkanaście minut.", http.StatusTooManyRequests)
			return false
		}
		s.mu.Lock()
		expected := s.setupCode
		s.mu.Unlock()
		if expected == "" || subtle.ConstantTimeCompare([]byte(normalizeSetupCode(code)), []byte(expected)) != 1 {
			s.limiter.fail(ip)
			jsonError(w, http.StatusForbidden, "Nieprawidłowy kod instalatora. Kod jest wypisany w logu panelu (konsola lub docker logs).")
			return false
		}
		return true
	}
	id, ok := s.identity(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	if id.Role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// connectionInput to dane połączenia z formularza; puste hasła oznaczają
// „zostaw obecne” (z data/connection.json albo .env).
type connectionInput struct {
	Host               string `json:"host"`
	SSHPort            string `json:"sshPort"`
	SSHUser            string `json:"sshUser"`
	SSHPassword        string `json:"sshPassword"`
	SSHHostKey         string `json:"sshHostKey"`
	SNMPVersion        string `json:"snmpVersion"`
	SNMPPort           string `json:"snmpPort"`
	SNMPCommunity      string `json:"snmpCommunity"`
	SNMPV3User         string `json:"snmpV3User"`
	SNMPV3AuthPassword string `json:"snmpV3AuthPassword"`
	SNMPV3PrivPassword string `json:"snmpV3PrivPassword"`
	SNMPV3PrivProtocol string `json:"snmpV3PrivProtocol"`
}

func (in connectionInput) merged() settings.Connection {
	cur := settings.CurrentConnection()
	keep := func(v, old string) string {
		if v == "" {
			return old
		}
		return v
	}
	t := strings.TrimSpace
	return settings.Connection{
		Host: t(in.Host), SSHPort: t(in.SSHPort), SSHUser: t(in.SSHUser),
		SSHPassword: keep(in.SSHPassword, cur.SSHPassword), SSHHostKey: t(in.SSHHostKey),
		SNMPVersion: t(in.SNMPVersion), SNMPPort: t(in.SNMPPort),
		SNMPCommunity:      keep(in.SNMPCommunity, cur.SNMPCommunity),
		SNMPV3User:         t(in.SNMPV3User),
		SNMPV3AuthPassword: keep(in.SNMPV3AuthPassword, cur.SNMPV3AuthPassword),
		SNMPV3PrivPassword: keep(in.SNMPV3PrivPassword, cur.SNMPV3PrivPassword),
	}
}

func (in connectionInput) deviceConfig() device.Config {
	cfg := device.Config{Timeout: 5 * time.Second}
	in.merged().Apply(&cfg)
	if p := device.NormalizePrivacyProtocol(in.SNMPV3PrivProtocol); in.SNMPV3PrivProtocol != "" {
		cfg.SNMPV3PrivacyProtocol = p
	}
	return cfg
}

// connectionView: dane połączenia bez sekretów (tylko informacja, czy są ustawione).
func connectionView() map[string]any {
	c := settings.CurrentConnection()
	return map[string]any{
		"source": settings.ConnectionSource(),
		"host":   c.Host, "sshPort": c.SSHPort, "sshUser": c.SSHUser, "sshHostKey": c.SSHHostKey,
		"snmpVersion": c.SNMPVersion, "snmpPort": c.SNMPPort, "snmpV3User": c.SNMPV3User,
		"snmpV3PrivProtocol":    settings.Get("SWITCH_SNMP_V3_PRIV_PROTOCOL"),
		"hasSSHPassword":        c.SSHPassword != "",
		"hasSNMPCommunity":      c.SNMPCommunity != "",
		"hasSNMPV3AuthPassword": c.SNMPV3AuthPassword != "",
		"hasSNMPV3PrivPassword": c.SNMPV3PrivPassword != "",
	}
}

func saveConnection(svc *device.Service, in connectionInput) error {
	c := in.merged()
	if err := settings.SaveConnection(c); err != nil {
		return err
	}
	if in.SNMPV3PrivProtocol != "" && in.SNMPV3PrivProtocol != settings.Get("SWITCH_SNMP_V3_PRIV_PROTOCOL") {
		if err := settings.Save(map[string]string{"SWITCH_SNMP_V3_PRIV_PROTOCOL": in.SNMPV3PrivProtocol}); err != nil {
			return err
		}
	}
	cfg := device.Config{}
	c.Apply(&cfg)
	svc.SetConnection(cfg)
	log.Printf("Zapisano nowe połączenie ze switchem (%s)", c.Host)
	return nil
}

// Trasy /api/setup/* idą na mux zewnętrzny (bez logowania), /api/connection
// na apiMux (za middleware: tylko administrator).
func registerSetupRoutes(m, apiMux *http.ServeMux, s *panelUserStore, svc *device.Service) {
	var completeMu sync.Mutex
	decode := func(w http.ResponseWriter, r *http.Request, v any) bool {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return false
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := json.NewDecoder(r.Body).Decode(v); err != nil {
			jsonError(w, http.StatusBadRequest, "Nieprawidłowe dane formularza.")
			return false
		}
		return true
	}

	m.HandleFunc("/api/setup/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		needed := s.needsSetup()
		out := map[string]any{"needed": needed}
		if needed {
			// Bez sekretów; adres i loginy z .env pomagają przy migracji.
			out["connection"] = connectionView()
		}
		writeJSON(w, out, nil)
	})

	m.HandleFunc("/api/setup/hostkey", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Code string `json:"code"`
			Host string `json:"host"`
			Port string `json:"port"`
		}
		if !decode(w, r, &q) || !s.setupAuthorized(w, r, q.Code) {
			return
		}
		fp, err := device.FetchHostKeyFingerprint(strings.TrimSpace(q.Host), strings.TrimSpace(q.Port), 6*time.Second)
		if err != nil {
			jsonError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, map[string]string{"fingerprint": fp}, nil)
	})

	m.HandleFunc("/api/setup/test", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Code       string          `json:"code"`
			Connection connectionInput `json:"connection"`
		}
		if !decode(w, r, &q) || !s.setupAuthorized(w, r, q.Code) {
			return
		}
		writeJSON(w, device.TestConnection(q.Connection.deviceConfig()), nil)
	})

	m.HandleFunc("/api/setup/complete", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Code           string           `json:"code"`
			Username       string           `json:"username"`
			Password       string           `json:"password"`
			Connection     *connectionInput `json:"connection"`
			ProtectedPorts string           `json:"protectedPorts"`
		}
		if !decode(w, r, &q) {
			return
		}
		completeMu.Lock()
		defer completeMu.Unlock()
		if !s.needsSetup() {
			jsonError(w, http.StatusConflict, "Panel jest już skonfigurowany. Zaloguj się.")
			return
		}
		if !s.setupAuthorized(w, r, q.Code) {
			return
		}
		q.Username = strings.TrimSpace(q.Username)
		if err := validPanelCredentials(q.Username, q.Password); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		if q.Connection != nil {
			if err := q.Connection.merged().Validate(); err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		if strings.TrimSpace(q.ProtectedPorts) != "" {
			if _, err := settings.Normalize("PROTECTED_PORTS", q.ProtectedPorts); err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(q.Password), bcrypt.DefaultCost)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "Nie można zahashować hasła.")
			return
		}
		if q.Connection != nil {
			if err := saveConnection(svc, *q.Connection); err != nil {
				jsonError(w, http.StatusInternalServerError, "Nie zapisano połączenia: "+err.Error())
				return
			}
		}
		if strings.TrimSpace(q.ProtectedPorts) != "" {
			if err := settings.Save(map[string]string{"PROTECTED_PORTS": q.ProtectedPorts}); err != nil {
				jsonError(w, http.StatusInternalServerError, "Nie zapisano portów chronionych: "+err.Error())
				return
			}
		}
		now := time.Now().UTC()
		user := panelUser{ID: randomPanelToken(), Username: q.Username, PasswordHash: string(hash), Role: "admin", Active: true, CreatedAt: now, UpdatedAt: now}
		s.mu.Lock()
		s.users[user.ID] = user
		if err := s.writeLocked(); err != nil {
			delete(s.users, user.ID)
			s.mu.Unlock()
			jsonError(w, http.StatusInternalServerError, "Nie zapisano konta: "+err.Error())
			return
		}
		s.setupCode = ""
		s.mu.Unlock()
		SetAuditUser(r, user.Username)
		log.Printf("Instalator: utworzono administratora %q", user.Username)
		s.newSession(w, r, user)
		writeJSON(w, map[string]any{"ok": true}, nil)
	})

	// Zmiana połączenia po instalacji (tylko administrator; middleware).
	apiMux.HandleFunc("/api/connection", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, connectionView(), nil)
		case http.MethodPost:
			var q connectionInput
			if !decode(w, r, &q) {
				return
			}
			if !s.setupAuthorized(w, r, "") {
				return
			}
			if err := saveConnection(svc, q); err != nil {
				jsonError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, connectionView(), nil)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}
