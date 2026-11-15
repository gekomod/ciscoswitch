package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gs948mx-manager/internal/server"
)

func TestAuditLogStoresMetadataWithoutRequestBody(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	h := auditLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Middleware panelu wpisuje login zalogowanego użytkownika.
		server.SetAuditUser(r, "operator")
		w.WriteHeader(http.StatusCreated)
	}), path)
	req := httptest.NewRequest(http.MethodPost, "/api/snmp/trap-host", strings.NewReader(`{"community":"TOP-SECRET"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	if !strings.Contains(line, `"username":"operator"`) || !strings.Contains(line, `"status":201`) || !strings.Contains(line, `"/api/snmp/trap-host"`) {
		t.Fatalf("missing audit metadata: %s", line)
	}
	if strings.Contains(line, "TOP-SECRET") {
		t.Fatal("audit log contains request secret")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("audit file mode=%o", info.Mode().Perm())
	}
}

func TestAuditLogIgnoresReadOnlyAndNonAPIRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	h := auditLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }), path)
	for _, target := range []string{"/api/status", "/"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unexpected audit file, err=%v", err)
	}
}
