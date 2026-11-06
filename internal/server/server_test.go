package server

import (
 "encoding/json"
 "errors"
 "fmt"
 "os"
 "path/filepath"
 "time"
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


func TestConfigBackupCreatesRestrictedSnapshotAndListsIt(t *testing.T) {
 dir := t.TempDir()
 fixed := time.Date(2026, 11, 6, 12, 30, 0, 123, time.UTC)
 entry, err := writeConfigBackup(dir, "hostname sw1\\n", fixed)
 if err != nil { t.Fatal(err) }
 if entry.Size == 0 || entry.SHA256 == "" { t.Fatalf("incomplete metadata: %+v", entry) }
 info, err := os.Stat(dir)
 if err != nil { t.Fatal(err) }
 if info.Mode().Perm() != 0700 { t.Fatalf("directory permissions=%o", info.Mode().Perm()) }
 fileInfo, err := os.Stat(filepath.Join(dir, entry.ID+".cfg"))
 if err != nil { t.Fatal(err) }
 if fileInfo.Mode().Perm() != 0600 { t.Fatalf("file permissions=%o", fileInfo.Mode().Perm()) }
 listed, err := listConfigBackups(dir)
 if err != nil { t.Fatal(err) }
 if len(listed) != 1 || listed[0].ID != entry.ID { t.Fatalf("listed=%+v", listed) }
}

func TestConfigBackupsHandlerCreatesOnlyNonEmptyRunningConfig(t *testing.T) {
 dir := t.TempDir()
 calls := 0
 h := configBackupsHandler(func(command string) (string, error) {
  calls++
  if command != "show running-config" { t.Fatalf("unexpected command %q", command) }
  return "hostname sw1\\n", nil
 }, dir, 100)
 rec := httptest.NewRecorder()
 h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/config/backups", nil))
 if rec.Code != http.StatusCreated { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
 if calls != 1 { t.Fatalf("calls=%d", calls) }
 listed, err := listConfigBackups(dir)
 if err != nil || len(listed) != 1 { t.Fatalf("listed=%+v err=%v", listed, err) }
}

func TestConfigBackupDownloadRejectsTraversal(t *testing.T) {
 h := configBackupDownloadHandler(t.TempDir())
 rec := httptest.NewRecorder()
 h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/backups/download?id=../../.env", nil))
 if rec.Code != http.StatusBadRequest { t.Fatalf("status=%d", rec.Code) }
}


func TestRunWithPreChangeBackupBacksUpBeforeChange(t *testing.T) {
 dir := t.TempDir()
 order := []string{}
 output, err := runWithPreChangeBackup(dir, func(command string) (string,error) {
  order = append(order, "backup:"+command)
  return "hostname sw1\\n", nil
 }, func() (string,error) {
  order = append(order, "change")
  return "configured", nil
 })
 if err != nil { t.Fatal(err) }
 if output != "configured" { t.Fatalf("output=%q", output) }
 if len(order)!=2 || order[0]!="backup:show running-config" || order[1]!="change" { t.Fatalf("order=%v", order) }
 entries,err:=listConfigBackups(dir)
 if err!=nil || len(entries)!=1 { t.Fatalf("backups=%v err=%v",entries,err) }
}

func TestRunWithPreChangeBackupFailsClosedOnReadError(t *testing.T) {
 dir := t.TempDir()
 changed := false
 _,err:=runWithPreChangeBackup(dir,func(string)(string,error){return "",errors.New("ssh offline")},func()(string,error){changed=true;return "bad",nil})
 if err==nil { t.Fatal("expected backup error") }
 if changed { t.Fatal("configuration change must not run without backup") }
 entries,listErr:=listConfigBackups(dir)
 if listErr!=nil || len(entries)!=0 { t.Fatalf("backups=%v err=%v",entries,listErr) }
}

func TestRunWithPreChangeBackupFailsClosedOnEmptyConfig(t *testing.T) {
 changed:=false
 _,err:=runWithPreChangeBackup(t.TempDir(),func(string)(string,error){return "  \n",nil},func()(string,error){changed=true;return "",nil})
 if err==nil { t.Fatal("expected empty configuration error") }
 if changed { t.Fatal("configuration change must not run") }
}


func TestPruneConfigBackupsKeepsNewest(t *testing.T) {
 dir:=t.TempDir()
 base:=time.Date(2026,11,6,12,0,0,0,time.UTC)
 for i:=0;i<4;i++ {
  if _,err:=writeConfigBackup(dir,fmt.Sprintf("hostname sw%d",i),base.Add(time.Duration(i)*time.Second));err!=nil{t.Fatal(err)}
 }
 if err:=pruneConfigBackups(dir,2);err!=nil{t.Fatal(err)}
 entries,err:=listConfigBackups(dir)
 if err!=nil{t.Fatal(err)}
 if len(entries)!=2{t.Fatalf("expected 2 backups, got %d",len(entries))}
 if entries[0].ID!=base.Add(3*time.Second).Format("20060102T150405.000000000Z") {t.Fatalf("newest=%s",entries[0].ID)}
 if entries[1].ID!=base.Add(2*time.Second).Format("20060102T150405.000000000Z") {t.Fatalf("second=%s",entries[1].ID)}
}

func TestPruneConfigBackupsRejectsZeroRetention(t *testing.T) {
 if err:=pruneConfigBackups(t.TempDir(),0);err==nil{t.Fatal("expected invalid retention error")}
}


func TestPortShutdownRequiresExplicitOptIn(t *testing.T) {
 t.Setenv("ALLOW_PORT_SHUTDOWN","")
 if portShutdownEnabled(){t.Fatal("shutdown must be disabled by default")}
 t.Setenv("ALLOW_PORT_SHUTDOWN","false")
 if portShutdownEnabled(){t.Fatal("false must not enable shutdown")}
 t.Setenv("ALLOW_PORT_SHUTDOWN","true")
 if !portShutdownEnabled(){t.Fatal("explicit true should enable shutdown")}
}

func TestProtectedPortsAreMatchedExactly(t *testing.T) {
 t.Setenv("PROTECTED_PORTS","port1.0.49, port1.0.52")
 if !isProtectedPort("port1.0.49") || !isProtectedPort("port1.0.52"){t.Fatal("configured protected port not recognized")}
 if isProtectedPort("port1.0.4") || isProtectedPort("port1.0.50"){t.Fatal("partial port match must not protect unrelated interface")}
}


func TestManagementDHCPChangeRequiresExplicitOptIn(t *testing.T) {
 t.Setenv("ALLOW_MANAGEMENT_DHCP_CHANGE","")
 if managementDHCPChangeEnabled(){t.Fatal("management DHCP change must be disabled by default")}
 t.Setenv("ALLOW_MANAGEMENT_DHCP_CHANGE","true")
 if !managementDHCPChangeEnabled(){t.Fatal("explicit true should enable management DHCP change")}
}

func TestProtectedPortNumberList(t *testing.T) {
 t.Setenv("PROTECTED_PORTS","port1.0.49,port1.0.52")
 if !protectedPortNumberList("5,49"){t.Fatal("protected LACP member not detected")}
 if protectedPortNumberList("5,6"){t.Fatal("unprotected ports incorrectly blocked")}
}

func TestConfigBackupDetectsTamperingAndBlocksDownload(t *testing.T) {
 dir:=t.TempDir()
 fixed:=time.Date(2026,11,6,13,0,0,0,time.UTC)
 entry,err:=writeConfigBackup(dir,"hostname original\\n",fixed)
 if err!=nil{t.Fatal(err)}
 listed,err:=listConfigBackups(dir)
 if err!=nil||len(listed)!=1||!listed[0].Verified{t.Fatalf("fresh backup should verify: %+v err=%v",listed,err)}
 if err:=os.WriteFile(filepath.Join(dir,entry.ID+".cfg"),[]byte("hostname changed\\n"),0600);err!=nil{t.Fatal(err)}
 listed,err=listConfigBackups(dir)
 if err!=nil||len(listed)!=1||listed[0].Verified{t.Fatalf("tampered backup must be unverified: %+v err=%v",listed,err)}
 h:=configBackupDownloadHandler(dir)
 rec:=httptest.NewRecorder()
 h.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/config/backups/download?id="+entry.ID,nil))
 if rec.Code!=http.StatusConflict{t.Fatalf("tampered download status=%d body=%s",rec.Code,rec.Body.String())}
}

func TestLegacyBackupCanBeDownloadedAsUnverified(t *testing.T) {
 dir:=t.TempDir()
 id:=time.Date(2026,11,6,14,0,0,0,time.UTC).Format("20060102T150405.000000000Z")
 if err:=os.WriteFile(filepath.Join(dir,id+".cfg"),[]byte("hostname legacy\\n"),0600);err!=nil{t.Fatal(err)}
 h:=configBackupDownloadHandler(dir)
 rec:=httptest.NewRecorder()
 h.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/config/backups/download?id="+id,nil))
 if rec.Code!=http.StatusOK{t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String())}
 if got:=rec.Header().Get("X-Backup-Integrity");got!="unverified-legacy"{t.Fatalf("integrity header=%q",got)}
}

func TestAuditHistoryReturnsOnlyLatest200Records(t *testing.T) {
 path:=filepath.Join(t.TempDir(),"audit.jsonl")
 file,err:=os.Create(path);if err!=nil{t.Fatal(err)}
 for i:=0;i<205;i++ { fmt.Fprintf(file,`{"timestamp":"2026-11-06T12:00:00Z","username":"admin","method":"POST","path":"/api/test","status":200,"durationMs":%d}\n`,i) }
 if err:=file.Close();err!=nil{t.Fatal(err)}
 h:=auditHistoryHandler(path)
 rec:=httptest.NewRecorder()
 h.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/audit",nil))
 if rec.Code!=http.StatusOK{t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String())}
 var items []auditHistoryRecord
 if err:=json.Unmarshal(rec.Body.Bytes(),&items);err!=nil{t.Fatal(err)}
 if len(items)!=200{t.Fatalf("records=%d want 200",len(items))}
 if items[0].DurationMS!=5||items[199].DurationMS!=204{t.Fatalf("unexpected retained range: first=%d last=%d",items[0].DurationMS,items[199].DurationMS)}
}

func TestAuditHistoryRejectsLargeFile(t *testing.T) {
 path:=filepath.Join(t.TempDir(),"audit.jsonl")
 if err:=os.WriteFile(path,make([]byte,10<<20+1),0600);err!=nil{t.Fatal(err)}
 rec:=httptest.NewRecorder()
 auditHistoryHandler(path).ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/audit",nil))
 if rec.Code!=http.StatusRequestEntityTooLarge{t.Fatalf("status=%d",rec.Code)}
}
