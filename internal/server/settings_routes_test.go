package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestSettingsAreAdminOnly(t *testing.T) {
	store := newPanelUserStore(filepath.Join(t.TempDir(), "users.json"))
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) })
	handler := store.middleware(inner)
	for _, role := range []string{"viewer", "operator", "admin"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			token := "sess-" + role
			store.users[role] = panelUser{ID: role, Username: role, Role: role, Active: true}
			store.sessions[token] = panelSession{Identity: panelIdentity{UserID: role, Username: role, Role: role}, ExpiresAt: time.Now().Add(time.Hour)}
			req := httptest.NewRequest(method, "/api/settings", nil)
			req.AddCookie(&http.Cookie{Name: "panel_session", Value: token})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			want := http.StatusForbidden
			if role == "admin" {
				want = 299
			}
			if rec.Code != want {
				t.Errorf("%s %s: status %d, want %d", role, method, rec.Code, want)
			}
		}
	}
}
