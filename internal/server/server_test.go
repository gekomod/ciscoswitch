package server

import (
 "errors"
 "net/http"
 "net/http/httptest"
 "testing"
 "strings"
)

func TestConfigExportHandlerSelectsCorrectSource(t *testing.T) {
 for _, tc := range []struct{ name, query, wantCommand, wantFile string }{
  {"default running", "", "show running-config", "running-config.txt"},
  {"explicit running", "?source=running", "show running-config", "running-config.txt"},
  {"startup", "?source=startup", "show startup-config", "startup-config.txt"},
 } {
  t.Run(tc.name, func(t *testing.T) {
   called := ""
   h := configExportHandler(func(command string) (string, error) {
    called = command
    return "hostname test-switch\n", nil
   })
   rec := httptest.NewRecorder()
   req := httptest.NewRequest(http.MethodGet, "/api/config/export"+tc.query, nil)
   h.ServeHTTP(rec, req)
   if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
   if called != tc.wantCommand { t.Fatalf("command=%q want %q", called, tc.wantCommand) }
   if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="`+tc.wantFile+`"` { t.Fatalf("Content-Disposition=%q", got) }
   if rec.Body.String() != "hostname test-switch\n" { t.Fatalf("unexpected body %q", rec.Body.String()) }
   if rec.Header().Get("Cache-Control") != "no-store" { t.Fatal("expected no-store") }
  })
 }
}

func TestConfigExportHandlerRejectsInvalidSourceWithoutCallingSwitch(t *testing.T) {
 called := false
 h := configExportHandler(func(string) (string, error) { called = true; return "", nil })
 rec := httptest.NewRecorder()
 h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/export?source=backup", nil))
 if rec.Code != http.StatusBadRequest { t.Fatalf("status=%d", rec.Code) }
 if called { t.Fatal("switch command must not run for invalid source") }
}

func TestConfigExportHandlerRejectsNonGet(t *testing.T) {
 h := configExportHandler(func(string) (string, error) { t.Fatal("switch command must not run"); return "", nil })
 rec := httptest.NewRecorder()
 h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/config/export", nil))
 if rec.Code != http.StatusMethodNotAllowed { t.Fatalf("status=%d", rec.Code) }
}

func TestConfigExportHandlerHidesDeviceErrorDetails(t *testing.T) {
 h := configExportHandler(func(string) (string, error) { return "", errors.New("sensitive ssh detail") })
 rec := httptest.NewRecorder()
 h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/export", nil))
 if rec.Code != http.StatusBadGateway { t.Fatalf("status=%d", rec.Code) }
 if rec.Body.String() == "" || rec.Body.String() == "sensitive ssh detail\n" { t.Fatalf("unexpected error body %q", rec.Body.String()) }
}


func TestCompareConfigLinesReportsAddedAndRemoved(t *testing.T) {
 got := compareConfigLines("hostname sw1\\ninterface port1.0.1\\n description AP\\n", "hostname sw1\\r\\ninterface port1.0.1\\r\\n description old\\r\\n")
 if len(got.Added) != 1 || got.Added[0] != "description AP" { t.Fatalf("added=%v", got.Added) }
 if len(got.Removed) != 1 || got.Removed[0] != "description old" { t.Fatalf("removed=%v", got.Removed) }
 if got.RunningLines != 3 || got.StartupLines != 3 { t.Fatalf("line counts: %+v", got) }
}

func TestCompareConfigLinesHandlesDuplicateLines(t *testing.T) {
 got := compareConfigLines("x\\nx\\ny\\n", "x\\ny\\ny\\n")
 if len(got.Added) != 1 || got.Added[0] != "x" { t.Fatalf("added=%v", got.Added) }
 if len(got.Removed) != 1 || got.Removed[0] != "y" { t.Fatalf("removed=%v", got.Removed) }
}

func TestConfigCompareHandlerReadsBothSources(t *testing.T) {
 calls := []string{}
 h := configCompareHandler(func(command string) (string, error) {
  calls = append(calls, command)
  if command == "show running-config" { return "hostname sw1\\n", nil }
  if command == "show startup-config" { return "hostname sw0\\n", nil }
  t.Fatalf("unexpected command %q", command)
  return "", nil
 })
 rec := httptest.NewRecorder()
 h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/compare", nil))
 if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
 if len(calls) != 2 { t.Fatalf("commands=%v", calls) }
 if !strings.Contains(rec.Body.String(), `"added":["hostname sw1"]`) { t.Fatalf("unexpected JSON: %s", rec.Body.String()) }
 if rec.Header().Get("Cache-Control") != "no-store" { t.Fatal("expected no-store") }
}
