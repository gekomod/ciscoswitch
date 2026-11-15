package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisterMountsLoginAndAuthenticatedPanelRoutes(t *testing.T) {
	t.Setenv("PANEL_USERS_PATH", filepath.Join(t.TempDir(), "users.json"))
	t.Setenv("PANEL_USER", "rootadmin")
	t.Setenv("PANEL_PASSWORD", "password8")
	mux := http.NewServeMux()
	Register(mux, nil)
	login := httptest.NewRecorder()
	mux.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/login", nil))
	if login.Code != http.StatusOK {
		t.Fatalf("login page status=%d", login.Code)
	}
	root := httptest.NewRecorder()
	mux.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/", nil))
	if root.Code != http.StatusSeeOther {
		t.Fatalf("unauthenticated root status=%d", root.Code)
	}
	if got := root.Header().Get("Location"); got != "/login" {
		t.Fatalf("root redirect=%q", got)
	}
	api := httptest.NewRecorder()
	mux.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if api.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API status=%d", api.Code)
	}
	// Panel ma konto, więc instalator jest wyłączony.
	setup := httptest.NewRecorder()
	mux.ServeHTTP(setup, httptest.NewRequest(http.MethodGet, "/setup", nil))
	if setup.Code != http.StatusSeeOther || setup.Header().Get("Location") != "/login" {
		t.Fatalf("setup on configured panel status=%d location=%q", setup.Code, setup.Header().Get("Location"))
	}
}

func TestSetupWizardCreatesFirstAdminOnlyWithCode(t *testing.T) {
	t.Setenv("PANEL_USERS_PATH", filepath.Join(t.TempDir(), "users.json"))
	t.Setenv("PANEL_USER", "")
	t.Setenv("PANEL_PASSWORD", "")
	t.Setenv("PANEL_ADMIN_USER", "")
	t.Setenv("PANEL_ADMIN_PASSWORD", "")
	store := newPanelUserStore(filepath.Join(t.TempDir(), "users.json"))
	store.prepareSetup()
	if !store.needsSetup() || store.setupCode == "" {
		t.Fatal("empty panel should require setup and have a code")
	}
	mux := http.NewServeMux()
	registerSetupRoutes(mux, http.NewServeMux(), store, nil)
	mux.Handle("/", store.middleware(http.NotFoundHandler()))

	root := httptest.NewRecorder()
	mux.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/login", nil))
	if root.Header().Get("Location") != "/setup" {
		t.Fatalf("login before setup should redirect to /setup, got %q", root.Header().Get("Location"))
	}

	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/setup/complete", strings.NewReader(body)))
		return rec
	}
	if rec := post(`{"code":"WRONG-CODE","username":"admin","password":"password8"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong code status=%d", rec.Code)
	}
	code := strings.ToLower(strings.ReplaceAll(store.setupCode, "-", ""))
	if rec := post(`{"code":"` + code + `","username":"admin","password":"short"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("short password status=%d", rec.Code)
	}
	rec := post(`{"code":"` + code + `","username":"admin","password":"password8"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(rec.Result().Cookies()) == 0 {
		t.Fatal("setup should log the new admin in")
	}
	if store.needsSetup() {
		t.Fatal("setup still needed after creating admin")
	}
	if rec := post(`{"code":"` + code + `","username":"other","password":"password8"}`); rec.Code != http.StatusConflict {
		t.Fatalf("second setup status=%d", rec.Code)
	}
	status := httptest.NewRecorder()
	mux.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/setup/status", nil))
	var st map[string]any
	_ = json.Unmarshal(status.Body.Bytes(), &st)
	if st["needed"] != false {
		t.Fatalf("status after setup: %v", st)
	}
}
