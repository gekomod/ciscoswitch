package server

import (
 "encoding/json"
 "bufio"
 "crypto/sha256"
 "encoding/hex"
 "fmt"
 "strconv"
 "os"
 "path/filepath"
 "sort"
 "time"
 "net/http"
 "strings"
 "gs948mx-manager/internal/device"
)

func Register(m *http.ServeMux,svc *device.Service){
 m.Handle("/",http.FileServer(http.Dir("web")))
 m.Handle("/api/config/export", configExportHandler(svc.RunReadOnly))
 m.Handle("/api/config/compare", configCompareHandler(svc.RunReadOnly))
 backupDir := os.Getenv("CONFIG_BACKUP_DIR")
 if backupDir == "" { backupDir = filepath.Join("data", "config-backups") }
 retention := 100
 if raw := os.Getenv("CONFIG_BACKUP_RETENTION"); raw != "" { if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 10000 { retention = n } }
 m.Handle("/api/config/backups", configBackupsHandler(svc.RunReadOnly, backupDir, retention))
 m.Handle("/api/config/backups/download", configBackupDownloadHandler(backupDir))
 m.Handle("/api/audit", auditHistoryHandler(os.Getenv("AUDIT_LOG_PATH")))
 m.HandleFunc("/api/status",func(w http.ResponseWriter,r *http.Request){if r.Method!="GET"{http.Error(w,"method not allowed",405);return};w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(svc.Status())})
 m.HandleFunc("/api/port/preview",func(w http.ResponseWriter,r *http.Request){
 if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
 r.Body=http.MaxBytesReader(w,r.Body,4096)
 var q struct{Port string `json:"port"`;Mode string `json:"mode"`;AccessVLAN int `json:"accessVlan"`;Allowed string `json:"allowed"`;Native int `json:"native"`;Speed string `json:"speed"`;Duplex string `json:"duplex"`;Description string `json:"description"`}
 if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
 if isProtectedPort(q.Port){http.Error(w,"port chroniony przez PROTECTED_PORTS",400);return}
 cmds,err:=device.BuildPortSettingsCommands(q.Port,q.Mode,q.AccessVLAN,q.Allowed,q.Native,q.Speed,q.Duplex,q.Description)
 if err!=nil{http.Error(w,err.Error(),400);return}
 warnings:=[]string{"Podgląd pokazuje komendy do wysłania; nie odczytuje jeszcze bieżącej konfiguracji portu."}
 if q.Mode=="trunk"{warnings=append(warnings,"Zmiana listy allowed VLAN może przerwać ruch urządzeń korzystających z tego portu.")}
 w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(map[string]any{"commands":cmds,"warnings":warnings,"applies":false})
})
m.HandleFunc("/api/vlan/preview",func(w http.ResponseWriter,r *http.Request){
 if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
 r.Body=http.MaxBytesReader(w,r.Body,2048)
 var q struct{ID int `json:"id"`;Name string `json:"name"`;Delete bool `json:"delete"`}
 if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
 cmds,err:=device.BuildVLANCommands(q.ID,q.Name,q.Delete);if err!=nil{http.Error(w,err.Error(),400);return}
 warnings:=[]string{"Podgląd nie zmienia konfiguracji przełącznika."}
 if q.Delete{warnings=append(warnings,"Usunięcie VLAN może przerwać łączność wszystkich przypisanych do niego urządzeń.")}
 w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(map[string]any{"commands":cmds,"warnings":warnings,"applies":false})
})
m.HandleFunc("/api/port",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Port string `json:"port"`;Action string `json:"action"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};if isProtectedPort(q.Port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",q.Port));return};if q.Action=="disable"&&!portShutdownEnabled(){write(w,"",fmt.Errorf("wyłączanie portów jest domyślnie zablokowane; ustaw ALLOW_PORT_SHUTDOWN=true po sprawdzeniu ryzyka utraty dostępu"));return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigurePort(q.Port,q.Action)});write(w,v,e)})
 m.HandleFunc("/api/management-dhcp",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,1024);var q struct{Enabled bool `json:"enabled"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};if !managementDHCPChangeEnabled(){write(w,"",fmt.Errorf("zmiana DHCP interfejsu zarządzającego jest domyślnie zablokowana; ustaw ALLOW_MANAGEMENT_DHCP_CHANGE=true po zapewnieniu alternatywnego dostępu"));return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.SetManagementDHCP(q.Enabled)});write(w,v,e)})
 m.HandleFunc("/api/ssh/user",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Username string `json:"username"`;Password string `json:"password"`;Privilege int `json:"privilege"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureSSHUser(q.Username,q.Password,q.Privilege,q.Delete)});write(w,v,e)})
 m.HandleFunc("/api/snmp/trap-host",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Address string `json:"address"`;Community string `json:"community"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureSNMPTrapHost(q.Address,q.Community,q.Delete)});write(w,v,e)})
 m.HandleFunc("/api/snmp/v3-user",func(w http.ResponseWriter,r *http.Request){
   if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
   r.Body=http.MaxBytesReader(w,r.Body,4096)
   var q struct{Username string `json:"username"`;Group string `json:"group"`;AuthPassword string `json:"authPassword"`;PrivacyPassword string `json:"privacyPassword"`;Delete bool `json:"delete"`}
   if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
   v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureSNMPv3User(q.Username,q.Group,q.AuthPassword,q.PrivacyPassword,q.Delete)})
   write(w,v,e)
  })
  m.HandleFunc("/api/snmp/v3-trap-host",func(w http.ResponseWriter,r *http.Request){
   if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
   r.Body=http.MaxBytesReader(w,r.Body,2048)
   var q struct{Address string `json:"address"`;Username string `json:"username"`;Delete bool `json:"delete"`}
   if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
   v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureSNMPv3TrapHost(q.Address,q.Username,q.Delete)})
   write(w,v,e)
  })
  m.HandleFunc("/api/network/probe",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Kind string `json:"kind"`;Target string `json:"target"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=svc.ProbeNetwork(q.Kind,q.Target);write(w,v,e)})
 m.HandleFunc("/api/route/static",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Prefix string `json:"prefix"`;Gateway string `json:"gateway"`;Distance int `json:"distance"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureStaticRoute(q.Prefix,q.Gateway,q.Distance,q.Delete)});write(w,v,e)})
 m.HandleFunc("/api/dhcp/pool",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,4096);var q struct{Name string `json:"name"`;Network string `json:"network"`;Mask string `json:"mask"`;Low string `json:"low"`;High string `json:"high"`;Router string `json:"router"`;DNS string `json:"dns"`;Enable bool `json:"enable"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureDHCPPool(q.Name,q.Network,q.Mask,q.Low,q.High,q.Router,q.DNS,q.Enable)});write(w,v,e)})
 m.HandleFunc("/api/vlan",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{ID int `json:"id"`;Name string `json:"name"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureVLAN(q.ID,q.Name,q.Delete)});write(w,v,e)})
 m.HandleFunc("/api/port/settings",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,4096);var q struct{Port string `json:"port"`;Mode string `json:"mode"`;AccessVLAN int `json:"accessVlan"`;Allowed string `json:"allowed"`;Native int `json:"native"`;Speed string `json:"speed"`;Duplex string `json:"duplex"`;Description string `json:"description"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};if isProtectedPort(q.Port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",q.Port));return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigurePortSettings(q.Port,q.Mode,q.AccessVLAN,q.Allowed,q.Native,q.Speed,q.Duplex,q.Description)});write(w,v,e)})
 m.HandleFunc("/api/lacp",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Group int `json:"group"`;Ports string `json:"ports"`;Mode string `json:"mode"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};if protectedPortNumberList(q.Ports){write(w,"",fmt.Errorf("LACP obejmuje port chroniony przez PROTECTED_PORTS"));return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureLACP(q.Group,q.Ports,q.Mode,q.Delete)});write(w,v,e)})
 m.HandleFunc("/api/stp",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,1024);var q struct{Mode string `json:"mode"`;Priority int `json:"priority"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureSTP(q.Mode,q.Priority)});write(w,v,e)})
 m.HandleFunc("/api/edgeport",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,1024);var q struct{Port string `json:"port"`;Enabled bool `json:"enabled"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.SetPortEdgePort(q.Port,q.Enabled)});write(w,v,e)})
 m.HandleFunc("/api/save",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.SaveConfig()});write(w,v,e)})
 m.HandleFunc("/api/system",func(w http.ResponseWriter,r *http.Request){if r.Method!="GET"{http.Error(w,"method not allowed",405);return};w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(svc.SystemInfo())})
 m.HandleFunc("/api/command",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,4096);var q struct{Command string `json:"command"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=svc.RunReadOnly(strings.TrimSpace(q.Command));write(w,v,e)})
}
func write(w http.ResponseWriter,v string,e error){w.Header().Set("Content-Type","application/json");if e!=nil{w.WriteHeader(400);_ = json.NewEncoder(w).Encode(map[string]string{"error":e.Error(),"output":v});return};_ = json.NewEncoder(w).Encode(map[string]string{"output":v,"ok":"true"})}



type auditHistoryRecord struct {
 Timestamp time.Time `json:"timestamp"`
 Username string `json:"username"`
 Method string `json:"method"`
 Path string `json:"path"`
 Status int `json:"status"`
 DurationMS int64 `json:"durationMs"`
}

func auditHistoryHandler(path string) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  if r.Method != http.MethodGet { http.Error(w,"method not allowed",http.StatusMethodNotAllowed); return }
  if path == "" { path = filepath.Join("data","audit.jsonl") }
  info,err:=os.Stat(path)
  if os.IsNotExist(err) { w.Header().Set("Content-Type","application/json"); _=json.NewEncoder(w).Encode([]auditHistoryRecord{}); return }
  if err != nil { http.Error(w,"audit history unavailable",http.StatusInternalServerError); return }
  if info.Size() > 10<<20 { http.Error(w,"audit history exceeds 10 MiB safety limit",http.StatusRequestEntityTooLarge); return }
  file,err:=os.Open(path); if err!=nil { http.Error(w,"audit history unavailable",http.StatusInternalServerError); return }; defer file.Close()
  records:=make([]auditHistoryRecord,0,200)
  scanner:=bufio.NewScanner(file); scanner.Buffer(make([]byte,4096),64*1024)
  for scanner.Scan() {
   var item auditHistoryRecord
   if json.Unmarshal(scanner.Bytes(),&item)!=nil { continue }
   records=append(records,item)
   if len(records)>200 { records=records[1:] }
  }
  if err:=scanner.Err();err!=nil { http.Error(w,"audit history read failed",http.StatusInternalServerError); return }
  w.Header().Set("Content-Type","application/json; charset=utf-8")
  w.Header().Set("Cache-Control","no-store")
  _=json.NewEncoder(w).Encode(records)
 })
}

// configExportHandler keeps the export endpoint independently testable without a live switch.
func configExportHandler(run func(string) (string, error)) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  if r.Method != http.MethodGet {
   http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
   return
  }
  source := r.URL.Query().Get("source")
  command, filename := "show running-config", "running-config.txt"
  switch source {
  case "", "running":
  case "startup":
   command, filename = "show startup-config", "startup-config.txt"
  default:
   http.Error(w, "invalid source", http.StatusBadRequest)
   return
  }
  output, err := run(command)
  if err != nil {
   http.Error(w, "switch configuration export failed", http.StatusBadGateway)
   return
  }
  w.Header().Set("Content-Type", "text/plain; charset=utf-8")
  w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
  w.Header().Set("Cache-Control", "no-store")
  w.Header().Set("X-Content-Type-Options", "nosniff")
  _, _ = w.Write([]byte(output))
 })
}


type configCompareResult struct {
 Added []string `json:"added"`
 Removed []string `json:"removed"`
 RunningLines int `json:"runningLines"`
 StartupLines int `json:"startupLines"`
}

func configCompareHandler(run func(string) (string, error)) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  if r.Method != http.MethodGet {
   http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
   return
  }
  running, err := run("show running-config")
  if err != nil {
   http.Error(w, "running configuration read failed", http.StatusBadGateway)
   return
  }
  startup, err := run("show startup-config")
  if err != nil {
   http.Error(w, "startup configuration read failed", http.StatusBadGateway)
   return
  }
  result := compareConfigLines(running, startup)
  w.Header().Set("Content-Type", "application/json; charset=utf-8")
  w.Header().Set("Cache-Control", "no-store")
  _ = json.NewEncoder(w).Encode(result)
 })
}

// compareConfigLines compares line multiplicity, so duplicate directives are handled
// correctly. Added means present in running-config but absent from startup-config.
func compareConfigLines(running, startup string) configCompareResult {
 rLines := nonEmptyConfigLines(running)
 sLines := nonEmptyConfigLines(startup)
 counts := make(map[string]int, len(sLines))
 for _, line := range sLines { counts[line]++ }
 result := configCompareResult{Added: []string{}, Removed: []string{}, RunningLines: len(rLines), StartupLines: len(sLines)}
 for _, line := range rLines {
  if counts[line] > 0 { counts[line]-- } else { result.Added = append(result.Added, line) }
 }
 counts = make(map[string]int, len(rLines))
 for _, line := range rLines { counts[line]++ }
 for _, line := range sLines {
  if counts[line] > 0 { counts[line]-- } else { result.Removed = append(result.Removed, line) }
 }
 return result
}

func nonEmptyConfigLines(raw string) []string {
 lines := strings.Split(strings.ReplaceAll(raw, "\\r\\n", "\\n"), "\\n")
 out := make([]string, 0, len(lines))
 for _, line := range lines {
  line = strings.TrimSpace(line)
  if line != "" { out = append(out, line) }
 }
 return out
}


type configBackup struct {
 ID string `json:"id"`
 CreatedAt time.Time `json:"createdAt"`
 Size int64 `json:"size"`
 SHA256 string `json:"sha256"`
 Verified bool `json:"verified"`
}

func configBackupsHandler(run func(string) (string, error), dir string, retention int) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  switch r.Method {
  case http.MethodGet:
   entries, err := listConfigBackups(dir)
   if err != nil { http.Error(w, "backup listing failed", http.StatusInternalServerError); return }
   w.Header().Set("Content-Type", "application/json; charset=utf-8")
   w.Header().Set("Cache-Control", "no-store")
   _ = json.NewEncoder(w).Encode(entries)
  case http.MethodPost:
   output, err := run("show running-config")
   if err != nil { http.Error(w, "running configuration backup failed", http.StatusBadGateway); return }
   if strings.TrimSpace(output) == "" { http.Error(w, "switch returned an empty configuration; backup not created", http.StatusBadGateway); return }
   entry, err := writeConfigBackup(dir, output, time.Now().UTC())
   if err != nil { http.Error(w, "configuration backup could not be stored", http.StatusInternalServerError); return }
   if err := pruneConfigBackups(dir, retention); err != nil { http.Error(w, "backup saved but retention cleanup failed", http.StatusInternalServerError); return }
   w.Header().Set("Content-Type", "application/json; charset=utf-8")
   w.Header().Set("Cache-Control", "no-store")
   w.WriteHeader(http.StatusCreated)
   _ = json.NewEncoder(w).Encode(entry)
  default:
   http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
  }
 })
}

func writeConfigBackup(dir, content string, now time.Time) (configBackup, error) {
 if err := os.MkdirAll(dir, 0700); err != nil { return configBackup{}, err }
 if err := os.Chmod(dir, 0700); err != nil { return configBackup{}, err }
 id := now.UTC().Format("20060102T150405.000000000Z")
 path := filepath.Join(dir, id+".cfg")
 tmp, err := os.CreateTemp(dir, ".backup-*.tmp")
 if err != nil { return configBackup{}, err }
 tmpName := tmp.Name()
 defer os.Remove(tmpName)
 if err := tmp.Chmod(0600); err != nil { _ = tmp.Close(); return configBackup{}, err }
 n, err := tmp.WriteString(content)
 if err != nil { _ = tmp.Close(); return configBackup{}, err }
 if err := tmp.Sync(); err != nil { _ = tmp.Close(); return configBackup{}, err }
 if err := tmp.Close(); err != nil { return configBackup{}, err }
 if _, err := os.Stat(path); err == nil { return configBackup{}, fmt.Errorf("backup id already exists") } else if !os.IsNotExist(err) { return configBackup{}, err }
 if err := os.Rename(tmpName, path); err != nil { return configBackup{}, err }
 sum := sha256.Sum256([]byte(content))
 digest := hex.EncodeToString(sum[:])
 if err := writeBackupDigest(dir, id, digest); err != nil {
  _ = os.Remove(path)
  return configBackup{}, err
 }
 return configBackup{ID:id, CreatedAt:now.UTC(), Size:int64(n), SHA256:digest, Verified:true}, nil
}


// writeBackupDigest stores the expected SHA-256 separately from the configuration
// file so later reads can detect accidental or unauthorized modification.
func writeBackupDigest(dir, id, digest string) error {
 path := filepath.Join(dir, id+".sha256")
 tmp, err := os.CreateTemp(dir, ".digest-*.tmp")
 if err != nil { return err }
 tmpName := tmp.Name()
 defer os.Remove(tmpName)
 if err := tmp.Chmod(0600); err != nil { _ = tmp.Close(); return err }
 if _, err := tmp.WriteString(digest+"\n"); err != nil { _ = tmp.Close(); return err }
 if err := tmp.Sync(); err != nil { _ = tmp.Close(); return err }
 if err := tmp.Close(); err != nil { return err }
 return os.Rename(tmpName, path)
}

func listConfigBackups(dir string) ([]configBackup, error) {
 files, err := os.ReadDir(dir)
 if os.IsNotExist(err) { return []configBackup{}, nil }
 if err != nil { return nil, err }
 entries := make([]configBackup, 0, len(files))
 for _, file := range files {
  if file.IsDir() || !strings.HasSuffix(file.Name(), ".cfg") { continue }
  id := strings.TrimSuffix(file.Name(), ".cfg")
  if !validBackupID(id) { continue }
  info, err := file.Info()
  if err != nil { continue }
  data, err := os.ReadFile(filepath.Join(dir, file.Name()))
  if err != nil { continue }
  sum := sha256.Sum256(data)
  actual := hex.EncodeToString(sum[:])
  expected, digestErr := os.ReadFile(filepath.Join(dir, id+".sha256"))
  verified := digestErr == nil && strings.TrimSpace(string(expected)) == actual
  entries = append(entries, configBackup{ID:id, CreatedAt:info.ModTime().UTC(), Size:info.Size(), SHA256:actual, Verified:verified})
 }
 sort.Slice(entries, func(i,j int) bool { return entries[i].CreatedAt.After(entries[j].CreatedAt) })
 return entries, nil
}

func validBackupID(id string) bool {
 if len(id) != len("20060102T150405.000000000Z") { return false }
 _, err := time.Parse("20060102T150405.000000000Z", id)
 return err == nil
}

func configBackupDownloadHandler(dir string) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  if r.Method != http.MethodGet { http.Error(w, "method not allowed", http.StatusMethodNotAllowed); return }
  id := r.URL.Query().Get("id")
  if !validBackupID(id) { http.Error(w, "invalid backup id", http.StatusBadRequest); return }
  path := filepath.Join(dir, id+".cfg")
  data, err := os.ReadFile(path)
  if os.IsNotExist(err) { http.NotFound(w, r); return }
  if err != nil { http.Error(w, "backup read failed", http.StatusInternalServerError); return }
  expected, digestErr := os.ReadFile(filepath.Join(dir, id+".sha256"))
  if digestErr == nil {
   sum := sha256.Sum256(data)
   if strings.TrimSpace(string(expected)) != hex.EncodeToString(sum[:]) {
    http.Error(w, "backup integrity check failed; download blocked", http.StatusConflict)
    return
   }
   w.Header().Set("X-Backup-Integrity", "verified")
  } else if os.IsNotExist(digestErr) {
   w.Header().Set("X-Backup-Integrity", "unverified-legacy")
  } else {
   http.Error(w, "backup integrity metadata read failed", http.StatusInternalServerError)
   return
  }
  w.Header().Set("Content-Type", "text/plain; charset=utf-8")
  w.Header().Set("Content-Disposition", `attachment; filename="running-config-`+id+`.txt"`)
  w.Header().Set("Cache-Control", "no-store")
  w.Header().Set("X-Content-Type-Options", "nosniff")
  _, _ = w.Write(data)
 })
}


// runWithPreChangeBackup is fail-closed: a config change is not sent unless the
// current running-config was read and stored successfully first.
func runWithPreChangeBackup(dir string, run func(string) (string, error), change func() (string, error)) (string, error) {
 content, err := run("show running-config")
 if err != nil { return "", fmt.Errorf("zmiana zablokowana: nie udało się wykonać kopii bezpieczeństwa running-config: %w", err) }
 if strings.TrimSpace(content) == "" { return "", fmt.Errorf("zmiana zablokowana: switch zwrócił pustą konfigurację; kopia nie została utworzona") }
 if _, err := writeConfigBackup(dir, content, time.Now().UTC()); err != nil {
  return "", fmt.Errorf("zmiana zablokowana: nie udało się zapisać kopii bezpieczeństwa: %w", err)
 }
 if err := pruneConfigBackups(dir, backupRetentionFromEnv()); err != nil {
  return "", fmt.Errorf("zmiana zablokowana: kopia została zapisana, ale porządkowanie historii nie powiodło się: %w", err)
 }
 return change()
}

func backupRetentionFromEnv() int { n,err:=strconv.Atoi(os.Getenv("CONFIG_BACKUP_RETENTION")); if err!=nil||n<1||n>10000{return 100};return n }

func pruneConfigBackups(dir string, keep int) error {
 if keep < 1 { return fmt.Errorf("retention must be at least 1") }
 entries,err:=listConfigBackups(dir); if err!=nil{return err}
 if len(entries) <= keep { return nil }
 for _,entry:=range entries[keep:] { if err:=os.Remove(filepath.Join(dir,entry.ID+".cfg"));err!=nil&&!os.IsNotExist(err){return err}; if err:=os.Remove(filepath.Join(dir,entry.ID+".sha256"));err!=nil&&!os.IsNotExist(err){return err} }
 return nil
}


// Port shutdown is opt-in because the panel cannot reliably infer which physical
// interface carries its own management session. Protected ports remain immutable.
func portShutdownEnabled() bool { return strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_PORT_SHUTDOWN")), "true") }
func managementDHCPChangeEnabled() bool { return strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_MANAGEMENT_DHCP_CHANGE")), "true") }
func isProtectedPort(name string) bool {
 name=strings.TrimSpace(name)
 if name=="" { return false }
 for _,item:=range strings.Split(os.Getenv("PROTECTED_PORTS"),",") {
  if strings.EqualFold(strings.TrimSpace(item),name) { return true }
 }
 return false
}

func protectedPortNumberList(raw string) bool {
 for _,part:=range strings.Split(raw,",") {
  n,err:=strconv.Atoi(strings.TrimSpace(part)); if err!=nil || n<1 || n>52 { continue }
  if isProtectedPort(fmt.Sprintf("port1.0.%d",n)) { return true }
 }
 return false
}
