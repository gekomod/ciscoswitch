package server

import (
 "context"
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
 "sync"

 "gs948mx-manager/internal/device"
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
 got := compareConfigLines("hostname sw1\ninterface port1.0.1\n description AP\n", "hostname sw1\r\ninterface port1.0.1\r\n description old\r\n")
 if got.Added != 1 { t.Fatalf("added=%d want 1", got.Added) }
 if got.Removed != 1 { t.Fatalf("removed=%d want 1", got.Removed) }
 if got.RunningLines != 3 || got.StartupLines != 3 { t.Fatalf("line counts: %+v", got) }
}

func TestCompareConfigLinesHandlesDuplicateLines(t *testing.T) {
 got := compareConfigLines("x\nx\ny\n", "x\ny\ny\n")
 if got.Added != 1 { t.Fatalf("added=%d want 1", got.Added) }
 if got.Removed != 1 { t.Fatalf("removed=%d want 1", got.Removed) }
}

func TestConfigCompareHandlerReadsBothSources(t *testing.T) {
 calls := []string{}
 h := configCompareHandler(func(command string) (string, error) {
  calls = append(calls, command)
  if command == "show running-config" { return "hostname sw1\n", nil }
  if command == "show startup-config" { return "hostname sw0\n", nil }
  t.Fatalf("unexpected command %q", command)
  return "", nil
 })
 rec := httptest.NewRecorder()
 h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/compare", nil))
 if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
 if len(calls) != 2 { t.Fatalf("commands=%v", calls) }
 if !strings.Contains(rec.Body.String(), `"added":1`) || !strings.Contains(rec.Body.String(), `"removed":1`) { t.Fatalf("unexpected JSON: %s", rec.Body.String()) }
 if strings.Contains(rec.Body.String(), "hostname sw1") || strings.Contains(rec.Body.String(), "hostname sw0") { t.Fatalf("configuration lines leaked: %s", rec.Body.String()) }
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


func TestConfigBackupRestorePreviewOnlyValidatesAndNeverTouchesSwitch(t *testing.T) {
 dir := t.TempDir()
 fixed := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
 entry, err := writeConfigBackup(dir, "hostname sw1\ninterface port1.0.1\n", fixed)
 if err != nil { t.Fatal(err) }
 h := configBackupRestorePreviewHandler(dir)
 rec := httptest.NewRecorder()
 h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/backups/restore-preview?id="+entry.ID, nil))
 if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
 var result map[string]any
 if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil { t.Fatal(err) }
 if result["integrityVerified"] != true || result["restoreAvailable"] != false { t.Fatalf("unexpected preview: %v", result) }
 if result["lineCount"] != float64(2) { t.Fatalf("lineCount=%v", result["lineCount"]) }
 if strings.Contains(rec.Body.String(), "hostname sw1") { t.Fatalf("configuration content leaked: %s", rec.Body.String()) }
 if rec.Header().Get("Cache-Control") != "no-store" { t.Fatal("expected no-store") }
}

func TestConfigBackupRestorePreviewBlocksTamperedSnapshot(t *testing.T) {
 dir := t.TempDir()
 fixed := time.Date(2026, 11, 7, 10, 1, 0, 0, time.UTC)
 entry, err := writeConfigBackup(dir, "hostname sw1\n", fixed)
 if err != nil { t.Fatal(err) }
 if err := os.WriteFile(filepath.Join(dir, entry.ID+".cfg"), []byte("hostname altered\n"), 0600); err != nil { t.Fatal(err) }
 rec := httptest.NewRecorder()
 configBackupRestorePreviewHandler(dir).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/backups/restore-preview?id="+entry.ID, nil))
 if rec.Code != http.StatusConflict { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
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

func TestProtectedPortsHandlerReportsConfiguredInterfaces(t *testing.T) {
 t.Setenv("PROTECTED_PORTS","port1.0.49, PORT1.0.52,port1.0.49,invalid,port1.0.99")
 h:=protectedPortsHandler()
 rec:=httptest.NewRecorder()
 h.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/ports/protection",nil))
 if rec.Code!=http.StatusOK { t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String()) }
 var got struct { Configured bool `json:"configured"`; Ports []string `json:"ports"`; Count int `json:"count"`; AutomaticDetection bool `json:"automaticDetection"` }
 if err:=json.Unmarshal(rec.Body.Bytes(),&got);err!=nil { t.Fatal(err) }
 if !got.Configured || got.Count!=2 || len(got.Ports)!=2 || got.Ports[0]!="port1.0.49" || got.Ports[1]!="port1.0.52" { t.Fatalf("unexpected protection state: %+v",got) }
 if got.AutomaticDetection { t.Fatal("protection must not claim automatic detection") }
 if rec.Header().Get("Cache-Control")!="no-store" { t.Fatal("expected no-store") }
}

func TestProtectedPortsHandlerRejectsWrites(t *testing.T) {
 rec:=httptest.NewRecorder()
 protectedPortsHandler().ServeHTTP(rec,httptest.NewRequest(http.MethodPost,"/api/ports/protection",nil))
 if rec.Code!=http.StatusMethodNotAllowed { t.Fatalf("status=%d",rec.Code) }
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

func TestConfigBackupCompareReturnsCountsWithoutConfigurationContent(t *testing.T) {
 dir := t.TempDir()
 fixed := time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)
 backupContent := "hostname old\ninterface port1.0.1\n"
 entry, err := writeConfigBackup(dir, backupContent, fixed)
 if err != nil { t.Fatal(err) }

 calls := 0
 h := configBackupCompareHandler(func(command string) (string, error) {
  calls++
  if command != "show running-config" { t.Fatalf("unexpected command %q", command) }
  return "hostname new\ninterface port1.0.1\n", nil
 }, dir)
 rec := httptest.NewRecorder()
 h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/backups/compare?id="+entry.ID, nil))
 if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
 if calls != 1 { t.Fatalf("switch reads=%d", calls) }

 var result configBackupCompareResult
 if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil { t.Fatal(err) }
 if result.BackupID != entry.ID || result.SHA256 != entry.SHA256 { t.Fatalf("metadata mismatch: %+v", result) }
 if result.BackupLines != 2 || result.CurrentLines != 2 { t.Fatalf("line counts: %+v", result) }
 if result.AddedInCurrent != 1 || result.RemovedFromCurrent != 1 || result.Identical { t.Fatalf("unexpected comparison: %+v", result) }
 if strings.Contains(rec.Body.String(), "hostname old") || strings.Contains(rec.Body.String(), "hostname new") {
  t.Fatalf("configuration content must not be returned: %s", rec.Body.String())
 }
 if rec.Header().Get("Cache-Control") != "no-store" { t.Fatal("expected no-store") }
}

func TestConfigBackupCompareBlocksUnverifiedOrTamperedBackups(t *testing.T) {
 dir := t.TempDir()
 fixed := time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC)
 verified, err := writeConfigBackup(dir, "hostname verified\n", fixed)
 if err != nil { t.Fatal(err) }
 if err := os.WriteFile(filepath.Join(dir, verified.ID+".cfg"), []byte("hostname tampered\n"), 0600); err != nil { t.Fatal(err) }

 legacyID := fixed.Add(time.Second).Format("20060102T150405.000000000Z")
 if err := os.WriteFile(filepath.Join(dir, legacyID+".cfg"), []byte("hostname legacy\n"), 0600); err != nil { t.Fatal(err) }

 h := configBackupCompareHandler(func(string) (string, error) {
  t.Fatal("switch must not be queried for an unverified backup")
  return "", nil
 }, dir)
 for _, id := range []string{verified.ID, legacyID} {
  rec := httptest.NewRecorder()
  h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/backups/compare?id="+id, nil))
  if rec.Code != http.StatusConflict { t.Fatalf("id=%s status=%d body=%s", id, rec.Code, rec.Body.String()) }
 }
}

func TestConfigBackupCompareValidatesMethodAndBackupID(t *testing.T) {
 h := configBackupCompareHandler(func(string) (string, error) {
  t.Fatal("switch must not be queried")
  return "", nil
 }, t.TempDir())
 rec := httptest.NewRecorder()
 h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/config/backups/compare?id=invalid", nil))
 if rec.Code != http.StatusMethodNotAllowed { t.Fatalf("method status=%d", rec.Code) }
 rec = httptest.NewRecorder()
 h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/backups/compare?id=../../.env", nil))
 if rec.Code != http.StatusBadRequest { t.Fatalf("id status=%d", rec.Code) }
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
 for i:=0;i<205;i++ { fmt.Fprintf(file,"{\"timestamp\":\"2026-11-06T12:00:00Z\",\"username\":\"admin\",\"method\":\"POST\",\"path\":\"/api/test\",\"status\":200,\"durationMs\":%d}\n",i) }
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

func TestEdgePortChangeRejectsProtectedPortBeforeBackupOrSSH(t *testing.T) {
 t.Setenv("PROTECTED_PORTS", "port1.0.49")
 mux := http.NewServeMux()
 Register(mux, device.New(device.Config{}))
 req := httptest.NewRequest(http.MethodPost, "/api/edgeport", strings.NewReader(`{"port":"port1.0.49","enabled":true}`))
 req.Header.Set("Content-Type", "application/json")
 rec := httptest.NewRecorder()
 mux.ServeHTTP(rec, req)
 if rec.Code != http.StatusBadRequest { t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String()) }
 if !strings.Contains(rec.Body.String(), "chroniony przez PROTECTED_PORTS") { t.Fatalf("unexpected response: %s", rec.Body.String()) }
}

func TestSwitchportDetailsHandlerReturnsParsedExactPort(t *testing.T) {
 calls := 0
 raw := "Interface name : port1.0.1\nSwitchport mode : access\nDefault Vlan : 2\nConfigured Vlans : 2\n\nInterface name : port1.0.10\nSwitchport mode : trunk\nDefault Vlan : 1\nConfigured Vlans : 1 4 5\n"
 h := switchportDetailsHandler(func(command string)(string,error) {
  calls++
  if command != "show interface switchport" { t.Fatalf("unexpected command %q",command) }
  return raw,nil
 })
 rec:=httptest.NewRecorder()
 h.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/port/switchport?port=port1.0.10",nil))
 if rec.Code!=http.StatusOK { t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String()) }
 if calls!=1 { t.Fatalf("read calls=%d",calls) }
 if !strings.Contains(rec.Body.String(),"\"mode\":\"trunk\"") || !strings.Contains(rec.Body.String(),"\"configuredVlans\":[1,4,5]") { t.Fatalf("unexpected JSON: %s",rec.Body.String()) }
}
func TestSwitchportDetailsHandlerRejectsUnknownPortBlock(t *testing.T) {
 h:=switchportDetailsHandler(func(string)(string,error){return "Interface name : port1.0.10\nSwitchport mode : trunk\n",nil})
 rec:=httptest.NewRecorder()
 h.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/port/switchport?port=port1.0.1",nil))
 if rec.Code!=http.StatusNotFound { t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String()) }
}


func TestBulkPortPreviewUsesSingleSwitchReadAndReturnsEveryPort(t *testing.T) {
 calls := 0
 raw := "Interface name : port1.0.1\nSwitchport mode : access\nDefault Vlan : 10\nConfigured Vlans : 10\n\nInterface name : port1.0.2\nSwitchport mode : trunk\nDefault Vlan : 1\nConfigured Vlans : 1 10 20\n"
 h := bulkPortPreviewHandler(func(command string)(string,error) {
  if command == "show interface switchport" { calls++; return raw,nil }
  if command == "show running-config" || command == "show lldp neighbors" { return "",fmt.Errorf("not available in unit fixture") }
  t.Fatalf("unexpected command %q",command); return "",nil
 })
 body := strings.NewReader(`{"ports":["port1.0.1","port1.0.2"],"mode":"trunk","accessVlan":10,"allowed":"1,10,20","native":1,"speed":"auto","duplex":"auto","description":""}`)
 rec := httptest.NewRecorder()
 h.ServeHTTP(rec,httptest.NewRequest(http.MethodPost,"/api/ports/preview/bulk",body))
 if rec.Code != http.StatusOK { t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String()) }
 if calls != 1 { t.Fatalf("switch reads=%d want 1",calls) }
 var result struct { Previews []bulkPortPreviewItem `json:"previews"`; Applies bool `json:"applies"`; ReadOperations int `json:"readOperations"` }
 if err:=json.Unmarshal(rec.Body.Bytes(),&result);err!=nil { t.Fatal(err) }
 if len(result.Previews)!=2 || result.Applies || result.ReadOperations!=3 { t.Fatalf("unexpected result: %+v",result) }
 if result.Previews[0].Current["defaultVlan"]!="10" || result.Previews[1].Current["mode"]!="trunk" { t.Fatalf("incorrect port state: %+v",result.Previews) }
 if rec.Header().Get("Cache-Control")!="no-store" { t.Fatal("expected no-store") }
 if strings.Contains(rec.Body.String(),"Interface name") { t.Fatalf("raw CLI output leaked: %s",rec.Body.String()) }
}

func TestBulkPortPreviewBlocksProtectedPortsBeforeSwitchRead(t *testing.T) {
 t.Setenv("PROTECTED_PORTS","port1.0.49")
 called:=false
 h:=bulkPortPreviewHandler(func(string)(string,error){called=true;return "",nil})
 body:=strings.NewReader(`{"ports":["port1.0.49"],"mode":"access","accessVlan":10,"speed":"auto","duplex":"auto"}`)
 rec:=httptest.NewRecorder()
 h.ServeHTTP(rec,httptest.NewRequest(http.MethodPost,"/api/ports/preview/bulk",body))
 if rec.Code!=http.StatusBadRequest { t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String()) }
 if called { t.Fatal("protected port preview must not query switch") }
}

func TestBulkPortPreviewFailsClosedWhenAnyPortCannotBeParsed(t *testing.T) {
 calls:=0
 h:=bulkPortPreviewHandler(func(command string)(string,error){if command=="show interface switchport"{calls++;return "Interface name : port1.0.1\nSwitchport mode : access\nDefault Vlan : 10\nConfigured Vlans : 10\n",nil};if command=="show running-config"||command=="show lldp neighbors"{return "",fmt.Errorf("not available in unit fixture")};return "",fmt.Errorf("unexpected command %q",command)})
 body:=strings.NewReader(`{"ports":["port1.0.1","port1.0.2"],"mode":"access","accessVlan":10,"speed":"auto","duplex":"auto"}`)
 rec:=httptest.NewRecorder()
 h.ServeHTTP(rec,httptest.NewRequest(http.MethodPost,"/api/ports/preview/bulk",body))
 if rec.Code!=http.StatusConflict { t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String()) }
 if calls!=1 { t.Fatalf("switch reads=%d want 1",calls) }
 if strings.Contains(rec.Body.String(),"\"previews\"") { t.Fatalf("partial preview must not be returned: %s",rec.Body.String()) }
}


func TestBulkSwitchportDetailsHandlerUsesOneReadForAllPorts(t *testing.T) {
 calls:=0
 raw:="Interface name : port1.0.1\nSwitchport mode : access\nDefault Vlan : 10\nConfigured Vlans : 10\n\nInterface name : port1.0.2\nSwitchport mode : trunk\nDefault Vlan : 1\nConfigured Vlans : 1 10 20\n"
 h:=bulkSwitchportDetailsHandler(func(command string)(string,error){calls++;if command!="show interface switchport"{t.Fatalf("unexpected command %q",command)};return raw,nil})
 rec:=httptest.NewRecorder()
 h.ServeHTTP(rec,httptest.NewRequest(http.MethodPost,"/api/ports/switchport/bulk",strings.NewReader(`{"ports":["port1.0.1","port1.0.2"]}`)))
 if rec.Code!=http.StatusOK{t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String())}
 if calls!=1{t.Fatalf("switch reads=%d want 1",calls)}
 var result struct{Ports []map[string]any `json:"ports"`;ReadOperations int `json:"readOperations"`}
 if err:=json.Unmarshal(rec.Body.Bytes(),&result);err!=nil{t.Fatal(err)}
 if len(result.Ports)!=2||result.ReadOperations!=1{t.Fatalf("unexpected result: %+v",result)}
 if rec.Header().Get("Cache-Control")!="no-store"{t.Fatal("expected no-store")}
 if strings.Contains(rec.Body.String(),"Interface name"){t.Fatalf("raw CLI output leaked: %s",rec.Body.String())}
}

func TestBulkSwitchportDetailsHandlerRejectsIncompleteReadback(t *testing.T) {
 calls:=0
 h:=bulkSwitchportDetailsHandler(func(string)(string,error){calls++;return "Interface name : port1.0.1\nSwitchport mode : access\nDefault Vlan : 10\nConfigured Vlans : 10\n",nil})
 rec:=httptest.NewRecorder()
 h.ServeHTTP(rec,httptest.NewRequest(http.MethodPost,"/api/ports/switchport/bulk",strings.NewReader(`{"ports":["port1.0.1","port1.0.2"]}`)))
 if rec.Code!=http.StatusConflict{t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String())}
 if calls!=1{t.Fatalf("switch reads=%d want 1",calls)}
}

func TestPortProfilesHandlerCRUDAndPersistsLocally(t *testing.T) {
 path:=filepath.Join(t.TempDir(),"profiles","port-profiles.json")
 h:=portProfilesHandler(path)
 request:=func(method,body string)*httptest.ResponseRecorder{rec:=httptest.NewRecorder();req:=httptest.NewRequest(method,"/api/port-profiles",strings.NewReader(body));h.ServeHTTP(rec,req);return rec}
 get:=request(http.MethodGet,"")
 if get.Code!=http.StatusOK {t.Fatalf("GET status=%d body=%s",get.Code,get.Body.String())}
 var initial struct{Profiles []portProfile `json:"profiles"`}
 if err:=json.Unmarshal(get.Body.Bytes(),&initial);err!=nil{t.Fatal(err)}
 if len(initial.Profiles)!=0{t.Fatalf("expected empty profile list, got %d",len(initial.Profiles))}
 created:=request(http.MethodPost,`{"name":"AP biuro","description":"Punkty dostępowe","portDescription":"AP BIURO","mode":"trunk","native":10,"allowed":"10,20,30","speed":"auto","duplex":"auto"}`)
 if created.Code!=http.StatusOK{t.Fatalf("POST status=%d body=%s",created.Code,created.Body.String())}
 var result struct{Profile portProfile `json:"profile"`;Profiles []portProfile `json:"profiles"`}
 if err:=json.Unmarshal(created.Body.Bytes(),&result);err!=nil{t.Fatal(err)}
 if !validPortProfileID(result.Profile.ID)||len(result.Profiles)!=1||result.Profile.Mode!="trunk"||result.Profile.PortDescription!="AP BIURO"{t.Fatalf("unexpected created profile: %+v",result)}
 updated:=request(http.MethodPost,`{"id":"`+result.Profile.ID+`","name":"AP biuro 2","mode":"access","accessVlan":30,"speed":"auto","duplex":"auto"}`)
 if updated.Code!=http.StatusOK{t.Fatalf("update status=%d body=%s",updated.Code,updated.Body.String())}
 if err:=json.Unmarshal(updated.Body.Bytes(),&result);err!=nil{t.Fatal(err)}
 if result.Profile.Name!="AP biuro 2"||result.Profile.Mode!="access"{t.Fatalf("profile was not updated: %+v",result.Profile)}
 if _,err:=os.Stat(path);err!=nil{t.Fatalf("profile file not persisted: %v",err)}
 del:=httptest.NewRecorder();h.ServeHTTP(del,httptest.NewRequest(http.MethodDelete,"/api/port-profiles?id="+result.Profile.ID,nil))
 if del.Code!=http.StatusOK{t.Fatalf("DELETE status=%d body=%s",del.Code,del.Body.String())}
 final:=request(http.MethodGet,"");if err:=json.Unmarshal(final.Body.Bytes(),&result);err!=nil{t.Fatal(err)}
 if len(result.Profiles)!=0{t.Fatalf("expected no profiles after delete, got %d",len(result.Profiles))}
}

func TestPortProfilesHandlerRejectsInvalidProfilesAndIDs(t *testing.T) {
 h:=portProfilesHandler(filepath.Join(t.TempDir(),"profiles.json"))
 for _,body:=range []string{
  `{"name":"Bad trunk","mode":"trunk","native":10,"allowed":"20","speed":"auto","duplex":"auto"}`,
  `{"name":"Bad mode","mode":"dynamic","accessVlan":10,"speed":"auto","duplex":"auto"}`,
  `{"name":"Invalid speed","mode":"access","accessVlan":10,"speed":"9999","duplex":"auto"}`,
 }{rec:=httptest.NewRecorder();h.ServeHTTP(rec,httptest.NewRequest(http.MethodPost,"/api/port-profiles",strings.NewReader(body)));if rec.Code!=http.StatusBadRequest{t.Fatalf("expected validation failure, status=%d body=%s",rec.Code,rec.Body.String())}}
 rec:=httptest.NewRecorder();h.ServeHTTP(rec,httptest.NewRequest(http.MethodDelete,"/api/port-profiles?id=../../users.json",nil))
 if rec.Code!=http.StatusBadRequest{t.Fatalf("expected invalid ID rejection, status=%d",rec.Code)}
}
func TestValidatePortRepairRequiresFreshMatchingModeAndOneRead(t *testing.T) {
 calls:=0
 raw:="Interface name : port1.0.7\nSwitchport mode : access\nDefault Vlan : 10\nConfigured Vlans : 10\n"
 q:=device.PortRepairRequest{Port:"port1.0.7",Mode:"access",AccessVLAN:20,UpdateAccessVLAN:true}
 commands,current,err:=validatePortRepair(func(command string)(string,error){calls++;if command!="show interface switchport"{t.Fatalf("unexpected read %q",command)};return raw,nil},q)
 if err!=nil{t.Fatal(err)}
 if calls!=1||current.DefaultVLAN!="10"||!strings.Contains(strings.Join(commands,"\n"),"switchport access vlan 20"){t.Fatalf("unexpected preview: calls=%d current=%+v commands=%v",calls,current,commands)}
 calls=0
 q.Mode="trunk"
 if _,_,err:=validatePortRepair(func(string)(string,error){calls++;return raw,nil},q);err==nil{t.Fatal("mode mismatch must block partial repair")}
 if calls!=1{t.Fatalf("expected one fresh read, got %d",calls)}
}
func TestValidatePortRepairBlocksProtectedPortsBeforeRead(t *testing.T) {
 t.Setenv("PROTECTED_PORTS","port1.0.49")
 called:=false
 q:=device.PortRepairRequest{Port:"port1.0.49",Mode:"access",AccessVLAN:20,UpdateAccessVLAN:true}
 if _,_,err:=validatePortRepair(func(string)(string,error){called=true;return "",nil},q);err==nil{t.Fatal("protected port must be rejected")}
 if called{t.Fatal("protected port must be blocked before querying switch")}
}

func TestAnalyzeSecurityConfigurationFindsExplicitRisksOnly(t *testing.T) {
 config:=`snmp-server community public ro
ip http server
ip telnet server
no spanning-tree
interface port1.0.1
 description BIURO
`
 findings:=analyzeSecurityConfiguration(config)
 if len(findings)!=4{t.Fatalf("expected four explicit findings, got %+v",findings)}
 codes:=map[string]bool{};for _,f:=range findings{codes[f.Code]=true}
 for _,code:=range []string{"snmp-default-community","http-management-enabled","telnet-management-enabled","stp-disabled"}{if !codes[code]{t.Errorf("missing finding %s",code)}}
}
func TestAnalyzeSecurityConfigurationDoesNotInventRisks(t *testing.T) {
 config:=`snmp-server community panel-readonly ro
spanning-tree mode rstp
interface port1.0.1
 description BIURO
`
 if findings:=analyzeSecurityConfiguration(config);len(findings)!=0{t.Fatalf("unexpected findings: %+v",findings)}
}
func TestSecurityAuditHandlerReadOnlyAndFailsClosed(t *testing.T) {
 calls:=0
 h:=securityAuditHandler(func(command string)(string,error){calls++;if command!="show running-config"{t.Fatalf("unexpected command %q",command)};return "snmp-server community public ro\n",nil})
 rec:=httptest.NewRecorder();h.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/security-audit",nil))
 if rec.Code!=http.StatusOK||calls!=1{t.Fatalf("status=%d calls=%d body=%s",rec.Code,calls,rec.Body.String())}
 if rec.Header().Get("Cache-Control")!="no-store"{t.Fatal("expected no-store")}
 calls=0
 fail:=securityAuditHandler(func(string)(string,error){calls++;return "",fmt.Errorf("offline")})
 bad:=httptest.NewRecorder();fail.ServeHTTP(bad,httptest.NewRequest(http.MethodGet,"/api/security-audit",nil))
 if bad.Code!=http.StatusBadGateway||calls!=1{t.Fatalf("failed read must abort audit: status=%d calls=%d",bad.Code,calls)}
}

func TestAnalyzeVLANAndPortSecurity(t *testing.T) {
 config:=`vlan 10
vlan 20
interface port1.0.1
 switchport mode trunk
 switchport trunk native vlan 10
 switchport trunk allowed vlan 20
interface port1.0.2
 switchport mode access
 switchport access vlan 30
`
 findings:=analyzeVLANAndPortSecurity(config,"")
 codes:=map[string]bool{};for _,f:=range findings{codes[f.Code]=true}
 for _,code:=range []string{"trunk-native-not-allowed","vlan-no-explicit-port-reference","port-references-undeclared-vlan"}{if !codes[code]{t.Errorf("missing %s in %+v",code,findings)}}
}
func TestAnalyzeVLANAndPortSecurityDoesNotCallUnassignedVLANUnused(t *testing.T) {
 config:=`vlan 100
interface port1.0.1
 switchport mode access
 switchport access vlan 1
`
 findings:=analyzeVLANAndPortSecurity(config,"")
 if len(findings)!=1||findings[0].Code!="vlan-no-explicit-port-reference"{t.Fatalf("expected cautious candidate finding, got %+v",findings)}
 if !strings.Contains(findings[0].Recommendation,"nie dowód"){t.Fatalf("recommendation must be cautious: %+v",findings[0])}
}
func TestSecurityAuditHandlerUsesFreshSwitchportAndKeepsPartialAvailability(t *testing.T) {
 commands:=[]string{}
 h:=securityAuditHandler(func(command string)(string,error){commands=append(commands,command);if command=="show running-config"{return "vlan 10\ninterface port1.0.1\n switchport mode trunk\n switchport trunk native vlan 10\n switchport trunk allowed vlan 20\n",nil};if command=="show interface switchport"{return "Interface name: port1.0.1\nSwitchport mode: trunk\nDefault Vlan: 10\nConfigured Vlans: 20\n",nil};return "",fmt.Errorf("unexpected")})
 rec:=httptest.NewRecorder();h.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/security-audit",nil))
 if rec.Code!=200||len(commands)!=2{t.Fatalf("status=%d commands=%v body=%s",rec.Code,commands,rec.Body.String())}
 if !strings.Contains(rec.Body.String(),"trunk-native-not-allowed"){t.Fatalf("missing trunk finding: %s",rec.Body.String())}
}

func TestAuditTrunkAllowedVLANAddSyntax(t *testing.T) {
 config:=`vlan 10
vlan 20
interface port1.0.1
 switchport mode trunk
 switchport trunk native vlan 10
 switchport trunk allowed vlan none
 switchport trunk allowed vlan add 10,20
`
 findings:=analyzeVLANAndPortSecurity(config,"")
 for _,f:=range findings{if f.Code=="trunk-native-not-allowed"{t.Fatalf("native VLAN should be in allowed list after add: %+v",findings)}}
}

func TestAuditDetectsEdgePortOnTrunkAndLACP(t *testing.T) {
 config:=`interface port1.0.1
 switchport mode trunk
 spanning-tree edgeport
interface port1.0.2
 channel-group 1 mode active
 spanning-tree edgeport
`
 findings:=analyzeVLANAndPortSecurity(config,"")
 codes:=map[string]bool{};for _,f:=range findings{codes[f.Code]=true}
 if !codes["edgeport-on-trunk"]||!codes["edgeport-on-lacp-member"]{t.Fatalf("expected topology-aware EdgePort findings, got %+v",findings)}
}

func TestTopologyMapParsersBuildPortsAggregationsAndLLDP(t *testing.T) {
 config:=`vlan 10
interface port1.0.1
 switchport mode trunk
 switchport trunk native vlan 10
 channel-group 1 mode active
interface port1.0.2
 switchport mode access
 switchport access vlan 20
 spanning-tree edgeport
`
 sw:=`Interface name: port1.0.1
Switchport mode: trunk
Default Vlan: 10
Configured Vlans: 10 20
Interface name: port1.0.2
Switchport mode: access
Default Vlan: 20
Configured Vlans: 20
`
 ports,lags,vlans:=parseTopologyConfig(config,sw)
 if len(ports)!=2||len(lags)!=1||len(vlans)!=1{t.Fatalf("unexpected topology parse: ports=%+v lags=%+v vlans=%+v",ports,lags,vlans)}
 if lags[0].Name!="Po1"||lags[0].Members[0]!="port1.0.1"{t.Fatalf("unexpected LAG: %+v",lags)}
 if ports[1].Mode!="access"||!ports[1].EdgePort{t.Fatalf("unexpected access port: %+v",ports[1])}
}
func TestTopologyLLDPAndLACPParsersIgnoreHeaders(t *testing.T) {
 links:=parseTopologyLLDP("Local Interface Neighbor System\nport1.0.1 SW-CORE Ethernet1/1\n")
 if len(links)!=1||links[0].Neighbor!="SW-CORE Ethernet1/1"{t.Fatalf("unexpected LLDP links: %+v",links)}
 groups:=parseTopologyLACP("Aggregator po1\nPartner LAG: SW-CORE\nIndividual: 2 - Ready: 1\nLink: port1.0.1 synchronized\n")
 if len(groups)!=1||groups[0].Partner!="SW-CORE"||groups[0].Ready!="Gotowy"||len(groups[0].Members)!=1{t.Fatalf("unexpected LACP groups: %+v",groups)}
}

func TestBuildTopologyImpactsMapsDegradedLACPToPortsVLANsAndNeighbors(t *testing.T) {
 ports:=[]topologyPort{
  {Port:"port1.0.1",VLANs:[]int{10,20},NativeVLAN:10,LAG:"Po1"},
  {Port:"port1.0.2",VLANs:[]int{10,20},NativeVLAN:10,LAG:"Po1"},
 }
 links:=[]topologyLink{{LocalPort:"port1.0.1",Neighbor:"CORE-SWITCH Ethernet1/1"}}
 lags:=[]topologyAggregation{{Name:"Po1",Members:[]string{"port1.0.1","port1.0.2"},Live:true,LiveMembers:[]string{"port1.0.1"},MemberMismatch:true,Ready:"Gotowy"}}
 risks:=[]topologyRisk{{"warning","lag-member-mismatch","Niespójność członków LACP","Po1: configured members differ","Sprawdź partnera."}}
 impacts:=buildTopologyImpacts(ports,links,lags,risks)
 if len(impacts)!=1 { t.Fatalf("expected one impact, got %+v",impacts) }
 got:=impacts[0]
 if got.Subject!="Po1"||len(got.ConfiguredPorts)!=2||len(got.LivePorts)!=1||len(got.VLANs)!=2||got.VLANs[0]!=10||got.VLANs[1]!=20||len(got.Neighbors)!=1||!got.VLANEvidenceAvailable {
  t.Fatalf("unexpected impact mapping: %+v",got)
 }
}
func TestBuildTopologyImpactsDoesNotInventVLANsWhenUnavailable(t *testing.T) {
 ports:=[]topologyPort{{Port:"port1.0.48",Role:"Uplink / switch"}}
 risks:=[]topologyRisk{{"warning","single-explicit-uplink","Single uplink","port1.0.48 is sole explicit uplink","Check paths."}}
 impacts:=buildTopologyImpacts(ports,nil,nil,risks)
 if len(impacts)!=1||impacts[0].VLANEvidenceAvailable||len(impacts[0].VLANs)!=0 {
  t.Fatalf("must not invent VLAN impact when no VLAN evidence exists: %+v",impacts)
 }
}

func TestTopologyMapHandlerOnlyReadsAndRequiresConfig(t *testing.T) {
 calls:=[]string{}
 h:=topologyMapHandler(func(command string)(string,error){calls=append(calls,command);switch command{case "show running-config":return "vlan 10\ninterface port1.0.1\n switchport mode access\n switchport access vlan 10\n",nil;case "show interface switchport":return "Interface name: port1.0.1\nSwitchport mode: access\nDefault Vlan: 10\nConfigured Vlans: 10\n",nil;case "show lldp neighbors":return "Local Interface Neighbor\n",nil;case "show etherchannel detail":return "",fmt.Errorf("unsupported")};return "",fmt.Errorf("unexpected command")})
 rec:=httptest.NewRecorder();h.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/topology/map",nil))
 if rec.Code!=200||len(calls)!=4{t.Fatalf("status=%d calls=%v body=%s",rec.Code,calls,rec.Body.String())}
 if !strings.Contains(rec.Body.String(),"\"available\":true")||!strings.Contains(rec.Body.String(),"\"switchport\":true"){t.Fatalf("missing topology metadata: %s",rec.Body.String())}
 fail:=topologyMapHandler(func(string)(string,error){return "",fmt.Errorf("offline")})
 bad:=httptest.NewRecorder();fail.ServeHTTP(bad,httptest.NewRequest(http.MethodGet,"/api/topology/map",nil))
 if bad.Code!=http.StatusBadGateway{t.Fatalf("expected bad gateway, got %d",bad.Code)}
}

func TestTopologyConfigIncludesDescriptionsAndExplicitUplinkRole(t *testing.T) {
 config:=`interface port1.0.48
 description UPLINK-CORE
 switchport mode trunk
 channel-group 2 mode active
`
 ports,_,_:=parseTopologyConfig(config,"")
 if len(ports)!=1||ports[0].Description!="UPLINK-CORE"||ports[0].Role!="Uplink / switch"{t.Fatalf("unexpected uplink metadata: %+v",ports)}
}

func TestTopologyRiskAnalysisFlagsSingleUplinkLACPDegradationAndProtectedPort(t *testing.T) {
 ports:=[]topologyPort{
  {Port:"port1.0.48",Role:"Uplink / switch",Protected:true},
  {Port:"port1.0.1",Role:"",Protected:false},
 }
 lags:=[]topologyAggregation{
  {Name:"Po1",Members:[]string{"port1.0.1","port1.0.2"},Live:true,LiveMembers:[]string{"port1.0.1"},MemberMismatch:true,Ready:"Niegotowy"},
  {Name:"Po2",Members:[]string{"port1.0.3"}},
 }
 risks:=analyzeTopologyRisks(ports,nil,lags,true)
 codes:=map[string]bool{}
 for _,risk:=range risks { codes[risk.Code]=true }
 for _,code:=range []string{"single-explicit-uplink","protected-port","uplink-no-lldp","lag-member-mismatch","lag-not-ready","lag-single-member"} {
  if !codes[code] { t.Errorf("expected risk %q, got %+v",code,risks) }
 }
}

func TestTopologyRiskDoesNotAssumeUplinkFromPortNumberOrMissingLLDP(t *testing.T) {
 ports:=[]topologyPort{{Port:"port1.0.52"}}
 risks:=analyzeTopologyRisks(ports,nil,nil,false)
 if len(risks)!=1||risks[0].Code!="uplink-role-unknown" { t.Fatalf("unexpected conservative risks: %+v",risks) }
}

func TestTopologyMapHandlerExposesProtectedPortsAndUplinkRisk(t *testing.T) {
 t.Setenv("PROTECTED_PORTS","port1.0.48")
 h:=topologyMapHandler(func(command string)(string,error){
  switch command {
  case "show running-config":
   return "interface port1.0.48\n description UPLINK-CORE\n switchport mode trunk\n",nil
  case "show interface switchport":
   return "Interface name: port1.0.48\nSwitchport mode: trunk\nDefault Vlan: 1\nConfigured Vlans: 1\n",nil
  case "show lldp neighbors":
   return "Local Interface Neighbor System\n",nil
  case "show etherchannel detail":
   return "",fmt.Errorf("unsupported")
  }
  return "",fmt.Errorf("unexpected command")
 })
 rec:=httptest.NewRecorder()
 h.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/topology/map",nil))
 body:=rec.Body.String()
 if rec.Code!=http.StatusOK { t.Fatalf("status=%d body=%s",rec.Code,body) }
 for _,want:=range []string{"\"protected\":true","single-explicit-uplink","uplink-no-lldp","protected-port"} {
  if !strings.Contains(body,want) { t.Errorf("expected %q in response: %s",want,body) }
 }
}

func TestTopologyChangeRisksWarnForUplinkLLDPAndLACPMember(t *testing.T) {
 commands:=map[string]string{
  "show running-config":"interface port1.0.48\n description UPLINK-CORE\n switchport mode trunk\n channel-group 3 mode active\ninterface port1.0.47\n channel-group 3 mode active\n",
  "show interface switchport":"Interface name: port1.0.48\nSwitchport mode: trunk\nDefault Vlan: 1\nConfigured Vlans: 1\n",
  "show lldp neighbors":"Local Interface Neighbor System\nport1.0.48 CORE-SWITCH Ethernet1/1\n",
 }
 risks:=topologyChangeRisks(func(command string)(string,error){value,ok:=commands[command];if !ok{return "",fmt.Errorf("unsupported")};return value,nil},"port1.0.48")
 codes:=map[string]bool{}
 for _,risk:=range risks{codes[risk.Code]=true}
 for _,code:=range []string{"change-uplink-risk","change-lldp-neighbor","change-lacp-member"}{if !codes[code]{t.Errorf("expected %s in %+v",code,risks)}}
}

func TestTopologyChangeRisksFailInformativelyWhenTopologyCannotBeRead(t *testing.T) {
 risks:=topologyChangeRisks(func(string)(string,error){return "",fmt.Errorf("offline")},"port1.0.7")
 if len(risks)!=1||risks[0].Code!="topology-check-unavailable"{t.Fatalf("expected conservative unavailable warning, got %+v",risks)}
}


func TestPortRepairApplyRequestJSONDecodesEmbeddedFields(t *testing.T) {
 var request struct {
  device.PortRepairRequest
  AcknowledgeConnectivityRisk bool `json:"acknowledgeConnectivityRisk"`
 }
 payload := `{"port":"port1.0.7","mode":"access","accessVlan":20,"allowed":"","native":1,"description":"BIURO","updateAccessVLAN":true,"updateTrunk":false,"updateDescription":true,"acknowledgeConnectivityRisk":true}`
 if err := json.Unmarshal([]byte(payload), &request); err != nil { t.Fatal(err) }
 q := request.PortRepairRequest
 if q.Port != "port1.0.7" || q.Mode != "access" || q.AccessVLAN != 20 || q.Description != "BIURO" || !q.UpdateAccessVLAN || !q.UpdateDescription || !request.AcknowledgeConnectivityRisk {
  t.Fatalf("embedded request fields were not decoded: %+v ack=%v", q, request.AcknowledgeConnectivityRisk)
 }
}

func TestRequireTopologyRiskAcknowledgementBlocksUnconfirmedAndAllowsConfirmed(t *testing.T) {
 fixture:=func(command string)(string,error){
  switch command {
  case "show running-config": return "interface port1.0.48\n description UPLINK-CORE\n switchport mode trunk\n",nil
  case "show interface switchport": return "Interface name: port1.0.48\nSwitchport mode: trunk\nDefault Vlan: 1\nConfigured Vlans: 1\n",nil
  case "show lldp neighbors": return "Local Interface Neighbor System\nport1.0.48 CORE-SWITCH Ethernet1/1\n",nil
  }
  return "",fmt.Errorf("unexpected command %s",command)
 }
 if err:=requireTopologyRiskAcknowledgement(fixture,[]string{"port1.0.48"},false);err==nil{t.Fatal("unconfirmed uplink risk must block apply")}
 if err:=requireTopologyRiskAcknowledgement(fixture,[]string{"port1.0.48"},true);err!=nil{t.Fatalf("explicit acknowledgement should allow reviewed change: %v",err)}
}


func TestDiagnosticsTransitionsRequireTwoComparableSamples(t *testing.T) {
 first:=diagnosticsSample{CapturedAt:time.Unix(100,0),Checks:map[string]bool{"lacp":true},Aggregations:[]topologyAggregation{{Name:"Po1",Live:true,Ready:"Gotowy",LiveMembers:[]string{"port1.0.1","port1.0.2"}}}}
 if got:=diagnosticsTransitions([]diagnosticsSample{first});len(got)!=0{t.Fatalf("first sample must not create transitions: %+v",got)}
 second:=diagnosticsSample{CapturedAt:time.Unix(200,0),Checks:map[string]bool{"lacp":true},Aggregations:[]topologyAggregation{{Name:"Po1",Live:true,Ready:"Niegotowy",LiveMembers:[]string{"port1.0.1"}}}}
 got:=diagnosticsTransitions([]diagnosticsSample{first,second})
 if len(got)!=2{t.Fatalf("transitions=%+v; want ready change and lost member",got)}
 codes:=map[string]bool{};for _,v:=range got{codes[v.Type]=true}
 if !codes["lacp-ready"]||!codes["lacp-member-lost"]{t.Fatalf("missing transitions: %+v",got)}
}
func TestDiagnosticsTransitionsDoNotTreatMissingDataAsRecovery(t *testing.T) {
 first:=diagnosticsSample{Checks:map[string]bool{"lacp":true},Aggregations:[]topologyAggregation{{Name:"Po1",Live:true,Ready:"Niegotowy"}}}
 gap:=diagnosticsSample{Checks:map[string]bool{"lacp":false}}
 recovered:=diagnosticsSample{Checks:map[string]bool{"lacp":true},Aggregations:[]topologyAggregation{{Name:"Po1",Live:true,Ready:"Gotowy"}}}
 if got:=diagnosticsTransitions([]diagnosticsSample{first,gap});len(got)!=0{t.Fatalf("gap must not imply recovery: %+v",got)}
 if got:=diagnosticsTransitions([]diagnosticsSample{gap,recovered});len(got)!=0{t.Fatalf("recovery after gap must not be inferred: %+v",got)}
}
func TestDiagnosticsStoreRetainsBoundedHistoryAndSecurePermissions(t *testing.T) {
 path:=filepath.Join(t.TempDir(),"nested","history.json")
 store:=newDiagnosticsStore(path,2)
 for i:=1;i<=3;i++{if err:=store.append(diagnosticsSample{CapturedAt:time.Unix(int64(i),0),Checks:map[string]bool{}});err!=nil{t.Fatal(err)}}
 items,err:=store.list();if err!=nil{t.Fatal(err)}
 if len(items)!=2||items[0].CapturedAt.Unix()!=2||items[1].CapturedAt.Unix()!=3{t.Fatalf("unexpected retained items: %+v",items)}
 info,err:=os.Stat(path);if err!=nil{t.Fatal(err)}
 if info.Mode().Perm()!=0600{t.Fatalf("file permissions=%o want 600",info.Mode().Perm())}
 dirInfo,err:=os.Stat(filepath.Dir(path));if err!=nil{t.Fatal(err)}
 if dirInfo.Mode().Perm()!=0700{t.Fatalf("directory permissions=%o want 700",dirInfo.Mode().Perm())}
}
func TestDiagnosticsCurrentAlertsDistinguishSeverity(t *testing.T) {
 sample:=diagnosticsSample{CapturedAt:time.Unix(10,0),Checks:map[string]bool{"lacp":true,"runningConfig":true},Uplinks:[]string{"port1.0.48"},Aggregations:[]topologyAggregation{{Name:"Po1",Ready:"Niegotowy",MemberMismatch:true}}}
 alerts:=diagnosticsCurrentAlerts(sample)
 found:=map[string]string{};for _,a:=range alerts{found[a.Code]=a.Severity}
 if found["lacp-not-ready"]!="critical"||found["lacp-member-mismatch"]!="warning"||found["single-explicit-uplink"]!="info"{t.Fatalf("unexpected alert severity map: %+v",found)}
}

func TestDiagnosticsTransitionsCollectChangesAcrossAllSamples(t *testing.T) {
 samples:=[]diagnosticsSample{}
 for i,state:=range []string{"Gotowy","Niegotowy","Gotowy","Niegotowy"} {
  samples=append(samples,diagnosticsSample{CapturedAt:time.Now().Add(time.Duration(i)*time.Minute),Checks:map[string]bool{"lacp":true},Aggregations:[]topologyAggregation{{Name:"Po1",Live:true,Ready:state}}})
 }
 got:=diagnosticsTransitions(samples)
 if len(got)!=3{t.Fatalf("expected all 3 transitions, got %+v",got)}
}
func TestDiagnosticsFlappingWindowEndpoint(t *testing.T) {
 path:=filepath.Join(t.TempDir(),"history.json")
 store:=newDiagnosticsStore(path,20)
 states:=[]string{"Gotowy","Niegotowy","Gotowy","Niegotowy"}
 for i,state:=range states {
  sample:=diagnosticsSample{CapturedAt:time.Now().Add(time.Duration(i-3)*time.Minute),Checks:map[string]bool{"lacp":true},Aggregations:[]topologyAggregation{{Name:"Po1",Live:true,Ready:state}}}
  if err:=store.append(sample);err!=nil{t.Fatal(err)}
 }
 h:=diagnosticsHistoryHandler(store)
 rec:=httptest.NewRecorder();h.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/diagnostics/history?windowMinutes=60",nil))
 if rec.Code!=http.StatusOK{t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String())}
 if !strings.Contains(rec.Body.String(),"lacp-flapping"){t.Fatalf("expected flapping alert, got %s",rec.Body.String())}
}

func TestDiagnosticsStoreConcurrentAppendsAreSerialized(t *testing.T) {
 store:=newDiagnosticsStore(filepath.Join(t.TempDir(),"history.json"),500)
 var wg sync.WaitGroup
 errs:=make(chan error,32)
 for i:=0;i<32;i++{wg.Add(1);go func(n int){defer wg.Done();errs<-store.append(diagnosticsSample{CapturedAt:time.Unix(int64(n),0),Checks:map[string]bool{}})}(i)}
 wg.Wait();close(errs);for err:=range errs{if err!=nil{t.Fatal(err)}}
 items,err:=store.list();if err!=nil{t.Fatal(err)}
 if len(items)!=32{t.Fatalf("concurrent append lost samples: got %d want 32",len(items))}
}
func TestDiagnosticsHistoryReportsManualModeWhenSamplerDisabled(t *testing.T) {
 t.Setenv("DIAGNOSTICS_INTERVAL","0")
 store:=newDiagnosticsStore(filepath.Join(t.TempDir(),"history.json"),10)
 h:=diagnosticsHistoryHandler(store)
 rec:=httptest.NewRecorder();h.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/diagnostics/history",nil))
 if rec.Code!=http.StatusOK||!strings.Contains(rec.Body.String(),"\"collectionMode\":\"manual\""){t.Fatalf("expected manual collection mode, status=%d body=%s",rec.Code,rec.Body.String())}
}

func TestDiagnosticsAcknowledgeHandlerPersistsOperatorAndComment(t *testing.T) {
 path:=filepath.Join(t.TempDir(),"history.json")
 store:=newDiagnosticsStore(path,10)
 sample:=diagnosticsSample{CapturedAt:time.Now().UTC(),Checks:map[string]bool{"lacp":true},Aggregations:[]topologyAggregation{{Name:"Po1",Ready:"Niegotowy"}}}
 if err:=store.append(sample);err!=nil{t.Fatal(err)}
 alert:=diagnosticsCurrentAlerts(sample)[0]
 id:=diagnosticsAlertID(alert.Code,alert.Evidence,alert.At)
 handler:=diagnosticsAcknowledgeHandler(store,path+".acks.json")
 req:=httptest.NewRequest(http.MethodPost,"/api/diagnostics/acknowledge",strings.NewReader(`{"id":"`+id+`","comment":"Sprawdzam przewód uplink"}`))
 req=req.WithContext(context.WithValue(req.Context(),identityContextKey{},panelIdentity{Username:"technik",Role:"operator"}))
 rec:=httptest.NewRecorder();handler.ServeHTTP(rec,req)
 if rec.Code!=http.StatusOK{t.Fatalf("status=%d body=%s",rec.Code,rec.Body.String())}
 var result struct{Saved bool `json:"saved"`;Acknowledgement diagnosticsAcknowledgement `json:"acknowledgement"`}
 if err:=json.Unmarshal(rec.Body.Bytes(),&result);err!=nil{t.Fatal(err)}
 if !result.Saved||result.Acknowledgement.Username!="technik"||result.Acknowledgement.Comment!="Sprawdzam przewód uplink"{t.Fatalf("unexpected acknowledgement: %+v",result)}
}
func TestDiagnosticsAcknowledgeRejectsStaleAlert(t *testing.T) {
 store:=newDiagnosticsStore(filepath.Join(t.TempDir(),"history.json"),10)
 handler:=diagnosticsAcknowledgeHandler(store,filepath.Join(t.TempDir(),"acks.json"))
 req:=httptest.NewRequest(http.MethodPost,"/api/diagnostics/acknowledge",strings.NewReader(`{"id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`))
 rec:=httptest.NewRecorder();handler.ServeHTTP(rec,req)
 if rec.Code!=http.StatusConflict{t.Fatalf("status=%d want 409",rec.Code)}
}

func TestDiagnosticsHistoryReturnsAcknowledgementMetadata(t *testing.T) {
 path:=filepath.Join(t.TempDir(),"history.json")
 store:=newDiagnosticsStore(path,10)
 sample:=diagnosticsSample{CapturedAt:time.Now().UTC(),Checks:map[string]bool{"lacp":true},Aggregations:[]topologyAggregation{{Name:"Po1",Ready:"Niegotowy"}}}
 if err:=store.append(sample);err!=nil{t.Fatal(err)}
 alert:=diagnosticsCurrentAlerts(sample)[0]
 ackStore:=&diagnosticsAckStore{path:path+".acks.json"}
 if err:=ackStore.save(diagnosticsAcknowledgement{ID:diagnosticsAlertID(alert.Code,alert.Evidence,alert.At),Code:alert.Code,Evidence:alert.Evidence,At:alert.At,AcknowledgedAt:time.Now().UTC(),Username:"operator1",Comment:"Zgłoszone do serwisu"});err!=nil{t.Fatal(err)}
 handler:=diagnosticsHistoryHandler(store)
 rec:=httptest.NewRecorder();handler.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/diagnostics/history",nil))
 if rec.Code!=http.StatusOK||!strings.Contains(rec.Body.String(),"\"acknowledged\":true")||!strings.Contains(rec.Body.String(),"operator1"){t.Fatalf("acknowledgement not returned: status=%d body=%s",rec.Code,rec.Body.String())}
}


func TestDiagnosticsAvailabilitySeparatesUnknownGapsFromDowntime(t *testing.T) {
 now:=time.Now().UTC().Truncate(time.Second)
 samples:=[]diagnosticsSample{
  {CapturedAt:now.Add(-4*time.Minute),Checks:map[string]bool{"runningConfig":true}},
  {CapturedAt:now.Add(-3*time.Minute),Checks:map[string]bool{"runningConfig":false}},
  {CapturedAt:now.Add(-2*time.Minute),Checks:map[string]bool{"runningConfig":false}},
  {CapturedAt:now,Checks:map[string]bool{"runningConfig":true}},
 }
 start:=now.Add(-5*time.Minute)
 metric:=diagnosticsAvailabilityMetric("CLI",samples,start,now,2*time.Minute,func(s diagnosticsSample)(bool,bool){return s.Checks!=nil,s.Checks["runningConfig"]})
 if metric.OutageCount!=1 {t.Fatalf("outage count=%d want 1: %+v",metric.OutageCount,metric)}
 if metric.OutageSeconds<60||metric.OutageSeconds>121 {t.Fatalf("outage duration=%d want about 120s",metric.OutageSeconds)}
 if metric.UnknownSeconds<60 {t.Fatalf("expected unobserved time to remain unknown: %+v",metric)}
}
func TestDiagnosticsAvailabilityEndpointRejectsUnsupportedWindow(t *testing.T) {
 h:=diagnosticsAvailabilityHandler(newDiagnosticsStore(filepath.Join(t.TempDir(),"history.json"),10))
 rec:=httptest.NewRecorder();h.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/diagnostics/availability?window=30d",nil))
 if rec.Code!=http.StatusBadRequest {t.Fatalf("status=%d want 400",rec.Code)}
}
