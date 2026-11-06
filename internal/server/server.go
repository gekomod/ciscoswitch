package server

import (
 "encoding/json"
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
 m.HandleFunc("/api/status",func(w http.ResponseWriter,r *http.Request){if r.Method!="GET"{http.Error(w,"method not allowed",405);return};w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(svc.Status())})
 m.HandleFunc("/api/port",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Port string `json:"port"`;Action string `json:"action"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigurePort(q.Port,q.Action)});write(w,v,e)})
 m.HandleFunc("/api/management-dhcp",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,1024);var q struct{Enabled bool `json:"enabled"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.SetManagementDHCP(q.Enabled)});write(w,v,e)})
 m.HandleFunc("/api/ssh/user",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Username string `json:"username"`;Password string `json:"password"`;Privilege int `json:"privilege"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureSSHUser(q.Username,q.Password,q.Privilege,q.Delete)});write(w,v,e)})
 m.HandleFunc("/api/snmp/trap-host",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Address string `json:"address"`;Community string `json:"community"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureSNMPTrapHost(q.Address,q.Community,q.Delete)});write(w,v,e)})
 m.HandleFunc("/api/network/probe",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Kind string `json:"kind"`;Target string `json:"target"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=svc.ProbeNetwork(q.Kind,q.Target);write(w,v,e)})
 m.HandleFunc("/api/route/static",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Prefix string `json:"prefix"`;Gateway string `json:"gateway"`;Distance int `json:"distance"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureStaticRoute(q.Prefix,q.Gateway,q.Distance,q.Delete)});write(w,v,e)})
 m.HandleFunc("/api/dhcp/pool",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,4096);var q struct{Name string `json:"name"`;Network string `json:"network"`;Mask string `json:"mask"`;Low string `json:"low"`;High string `json:"high"`;Router string `json:"router"`;DNS string `json:"dns"`;Enable bool `json:"enable"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureDHCPPool(q.Name,q.Network,q.Mask,q.Low,q.High,q.Router,q.DNS,q.Enable)});write(w,v,e)})
 m.HandleFunc("/api/vlan",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{ID int `json:"id"`;Name string `json:"name"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureVLAN(q.ID,q.Name,q.Delete)});write(w,v,e)})
 m.HandleFunc("/api/port/settings",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,4096);var q struct{Port string `json:"port"`;Mode string `json:"mode"`;AccessVLAN int `json:"accessVlan"`;Allowed string `json:"allowed"`;Native int `json:"native"`;Speed string `json:"speed"`;Duplex string `json:"duplex"`;Description string `json:"description"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigurePortSettings(q.Port,q.Mode,q.AccessVLAN,q.Allowed,q.Native,q.Speed,q.Duplex,q.Description)});write(w,v,e)})
 m.HandleFunc("/api/lacp",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Group int `json:"group"`;Ports string `json:"ports"`;Mode string `json:"mode"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureLACP(q.Group,q.Ports,q.Mode,q.Delete)});write(w,v,e)})
 m.HandleFunc("/api/stp",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,1024);var q struct{Mode string `json:"mode"`;Priority int `json:"priority"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureSTP(q.Mode,q.Priority)});write(w,v,e)})
 m.HandleFunc("/api/edgeport",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,1024);var q struct{Port string `json:"port"`;Enabled bool `json:"enabled"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.SetPortEdgePort(q.Port,q.Enabled)});write(w,v,e)})
 m.HandleFunc("/api/save",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.SaveConfig()});write(w,v,e)})
 m.HandleFunc("/api/system",func(w http.ResponseWriter,r *http.Request){if r.Method!="GET"{http.Error(w,"method not allowed",405);return};w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(svc.SystemInfo())})
 m.HandleFunc("/api/command",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,4096);var q struct{Command string `json:"command"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=svc.RunReadOnly(strings.TrimSpace(q.Command));write(w,v,e)})
}
func write(w http.ResponseWriter,v string,e error){w.Header().Set("Content-Type","application/json");if e!=nil{w.WriteHeader(400);_ = json.NewEncoder(w).Encode(map[string]string{"error":e.Error(),"output":v});return};_ = json.NewEncoder(w).Encode(map[string]string{"output":v,"ok":"true"})}


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
 return configBackup{ID:id, CreatedAt:now.UTC(), Size:int64(n), SHA256:hex.EncodeToString(sum[:])}, nil
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
  entries = append(entries, configBackup{ID:id, CreatedAt:info.ModTime().UTC(), Size:info.Size(), SHA256:hex.EncodeToString(sum[:])})
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
 for _,entry:=range entries[keep:] { if err:=os.Remove(filepath.Join(dir,entry.ID+".cfg"));err!=nil&&!os.IsNotExist(err){return err} }
 return nil
}
