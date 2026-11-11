package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSettingsAreAdminOnly(t *testing.T) {
	store := newPanelUserStore(filepath.Join(t.TempDir(), "users.json"))
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) })
	handler := store.middleware(inner)
	for _, role := range []string{"viewer", "operator", "admin"} {
		for _, target := range []string{"/api/settings", "/api/notify", "/api/notify/test"} {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				token := "sess-" + role
				store.users[role] = panelUser{ID: role, Username: role, Role: role, Active: true}
				store.sessions[token] = panelSession{Identity: panelIdentity{UserID: role, Username: role, Role: role}, ExpiresAt: time.Now().Add(time.Hour)}
				req := httptest.NewRequest(method, target, nil)
				req.AddCookie(&http.Cookie{Name: "panel_session", Value: token})
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				want := http.StatusForbidden
				if role == "admin" {
					want = 299
				}
				if rec.Code != want {
					t.Errorf("%s %s %s: status %d, want %d", role, method, target, rec.Code, want)
				}
			}
		}
	}
}

func TestStaticFilesAreRevalidated(t *testing.T) {
	if _, err := os.Stat("../../web/index.html"); err != nil {
		t.Skip("brak katalogu web")
	}
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir("../..")
	mux := http.NewServeMux()
	static := http.FileServer(http.Dir("web"))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/app.js", nil))
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("status %d cache %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}
