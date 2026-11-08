package server

import (
 "encoding/json"
 "crypto/rand"
 "sync"
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
 m.Handle("/api/config/backups/compare", configBackupCompareHandler(svc.RunReadOnly, backupDir))
 m.Handle("/api/config/backups/restore-preview", configBackupRestorePreviewHandler(backupDir))
 m.Handle("/api/audit", auditHistoryHandler(os.Getenv("AUDIT_LOG_PATH")))
 m.Handle("/api/security-audit", securityAuditHandler(svc.RunReadOnly))
 m.Handle("/api/monitor/history", monitorHistoryHandler(filepath.Join("data", "monitor-history.jsonl")))
 m.Handle("/api/monitor/stream", monitorStreamHandler())
 m.HandleFunc("/api/status",func(w http.ResponseWriter,r *http.Request){if r.Method!="GET"{http.Error(w,"method not allowed",405);return};w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(svc.Status())})
 m.Handle("/api/ports/protection", protectedPortsHandler())
 profilePath:=os.Getenv("PORT_PROFILES_PATH");if profilePath==""{profilePath=filepath.Join("data","port-profiles.json")}
 m.Handle("/api/port-profiles",portProfilesHandler(profilePath))
 m.Handle("/api/ports/preview/bulk", bulkPortPreviewHandler(svc.RunReadOnly))
 m.Handle("/api/ports/switchport/bulk", bulkSwitchportDetailsHandler(svc.RunReadOnly))
 m.HandleFunc("/api/port/preview",func(w http.ResponseWriter,r *http.Request){
 if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
 r.Body=http.MaxBytesReader(w,r.Body,4096)
 var q struct{Port string `json:"port"`;Mode string `json:"mode"`;AccessVLAN int `json:"accessVlan"`;Allowed string `json:"allowed"`;Native int `json:"native"`;Speed string `json:"speed"`;Duplex string `json:"duplex"`;Description string `json:"description"`}
 if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
 if isProtectedPort(q.Port){http.Error(w,"port chroniony przez PROTECTED_PORTS",400);return}
 cmds,err:=device.BuildPortSettingsCommands(q.Port,q.Mode,q.AccessVLAN,q.Allowed,q.Native,q.Speed,q.Duplex,q.Description)
 if err!=nil{http.Error(w,err.Error(),400);return}
 raw,err:=svc.RunReadOnly("show interface switchport")
 if err!=nil{http.Error(w,"nie udało się odczytać bieżącej konfiguracji portu; podgląd i zapis zostały zablokowane",http.StatusBadGateway);return}
 current,err:=device.ParseSwitchportDetails(raw,q.Port)
 if err!=nil{http.Error(w,"nie udało się jednoznacznie rozpoznać bieżącej konfiguracji portu; podgląd i zapis zostały zablokowane",http.StatusConflict);return}
 warnings:=[]string{"Bieżąca konfiguracja została odczytana bez wprowadzania zmian."}
 if strings.ToLower(current.Mode)!=q.Mode{warnings=append(warnings,"Zmiana trybu portu z "+current.Mode+" na "+q.Mode+" może przerwać łączność urządzeń.")}
 if q.Mode=="trunk"{warnings=append(warnings,"Zmiana listy allowed VLAN może przerwać ruch urządzeń korzystających z tego portu.")}
 w.Header().Set("Content-Type","application/json")
 w.Header().Set("Cache-Control","no-store")
 _=json.NewEncoder(w).Encode(map[string]any{
  "commands":cmds,"warnings":warnings,"applies":false,
  "current":map[string]any{"port":current.Port,"mode":current.Mode,"defaultVlan":current.DefaultVLAN,"configuredVlans":current.ConfiguredVLANs},
 })
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

 m.HandleFunc("/api/vlan/ports/preview",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  r.Body=http.MaxBytesReader(w,r.Body,4096)
  var q struct{Ports []string `json:"ports"`;VLAN int `json:"vlan"`;Membership string `json:"membership"`}
  if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
  for _,port:=range q.Ports{if isProtectedPort(port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",port));return}}
  running,err:=svc.RunReadOnly("show running-config");if err!=nil{write(w,"",fmt.Errorf("nie udało się odczytać running-config: %w",err));return}
  cmds,err:=device.BuildVLANPortMembershipCommands(running,q.Ports,q.VLAN,q.Membership);if err!=nil{write(w,"",err);return}
  warning:="Operacja zmienia wyłącznie członkostwo VLAN na wybranych portach; nie zmienia automatycznie trybu access/trunk."
  if q.Membership=="untagged"{warning+=" Porty muszą być w trybie access."}else{warning+=" Porty muszą już być w trybie trunk; istniejąca lista VLAN i native VLAN pozostają zachowane."}
  w.Header().Set("Content-Type","application/json");w.Header().Set("Cache-Control","no-store")
  _=json.NewEncoder(w).Encode(map[string]any{"commands":cmds,"warnings":[]string{warning},"applies":false})
 })
 m.HandleFunc("/api/vlan/ports/apply",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  r.Body=http.MaxBytesReader(w,r.Body,4096)
  var q struct{Ports []string `json:"ports"`;VLAN int `json:"vlan"`;Membership string `json:"membership"`}
  if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
  for _,port:=range q.Ports{if isProtectedPort(port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",port));return}}
  v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureVLANPortMembership(q.Ports,q.VLAN,q.Membership)})
  write(w,v,e)
 })
 m.HandleFunc("/api/port/settings",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,4096);var q struct{Port string `json:"port"`;Mode string `json:"mode"`;AccessVLAN int `json:"accessVlan"`;Allowed string `json:"allowed"`;Native int `json:"native"`;Speed string `json:"speed"`;Duplex string `json:"duplex"`;Description string `json:"description"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};if isProtectedPort(q.Port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",q.Port));return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigurePortSettings(q.Port,q.Mode,q.AccessVLAN,q.Allowed,q.Native,q.Speed,q.Duplex,q.Description)});write(w,v,e)})
 m.HandleFunc("/api/ports/repair/preview",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  r.Body=http.MaxBytesReader(w,r.Body,4096)
  var q device.PortRepairRequest
  if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
  if isProtectedPort(q.Port){http.Error(w,"port "+q.Port+" jest chroniony przez PROTECTED_PORTS",400);return}
  commands,current,err:=validatePortRepair(svc.RunReadOnly,q)
  if err!=nil{http.Error(w,err.Error(),400);return}
  w.Header().Set("Content-Type","application/json");w.Header().Set("Cache-Control","no-store")
  _=json.NewEncoder(w).Encode(map[string]any{"port":q.Port,"current":current,"commands":commands,"applies":false})
 })
 m.HandleFunc("/api/ports/repair/apply",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  r.Body=http.MaxBytesReader(w,r.Body,4096)
  var q device.PortRepairRequest
  if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
  if isProtectedPort(q.Port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",q.Port));return}
  if _,_,err:=validatePortRepair(svc.RunReadOnly,q);err!=nil{write(w,"",err);return}
  output,err:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){if _,_,checkErr:=validatePortRepair(svc.RunReadOnly,q);checkErr!=nil{return "",checkErr};return svc.ConfigurePortRepair(q)})
  write(w,output,err)
 })
 m.HandleFunc("/api/ports/settings/bulk",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  r.Body=http.MaxBytesReader(w,r.Body,8192)
  var q struct{Ports []string `json:"ports"`;Mode string `json:"mode"`;AccessVLAN int `json:"accessVlan"`;Allowed string `json:"allowed"`;Native int `json:"native"`;Speed string `json:"speed"`;Duplex string `json:"duplex"`;Description string `json:"description"`}
  if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
  for _,port:=range q.Ports{if isProtectedPort(port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",port));return}}
  if _,err:=device.BuildBulkPortSettingsCommands(q.Ports,q.Mode,q.AccessVLAN,q.Allowed,q.Native,q.Speed,q.Duplex,q.Description);err!=nil{write(w,"",err);return}
  v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureBulkPortSettings(q.Ports,q.Mode,q.AccessVLAN,q.Allowed,q.Native,q.Speed,q.Duplex,q.Description)})
  write(w,v,e)
 })
 m.HandleFunc("/api/ports/action/bulk",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  r.Body=http.MaxBytesReader(w,r.Body,4096)
  var q struct{Ports []string `json:"ports"`;Action string `json:"action"`}
  if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
  for _,port:=range q.Ports{if isProtectedPort(port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",port));return}}
  if q.Action=="disable"&&!portShutdownEnabled(){write(w,"",fmt.Errorf("wyłączanie portów jest domyślnie zablokowane; ustaw ALLOW_PORT_SHUTDOWN=true po sprawdzeniu ryzyka utraty dostępu"));return}
  if _,err:=device.BuildBulkPortActionCommands(q.Ports,q.Action);err!=nil{write(w,"",err);return}
  v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureBulkPortAction(q.Ports,q.Action)})
  write(w,v,e)
 })
 m.HandleFunc("/api/lacp",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Group int `json:"group"`;Ports string `json:"ports"`;Mode string `json:"mode"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};if protectedPortNumberList(q.Ports){write(w,"",fmt.Errorf("LACP obejmuje port chroniony przez PROTECTED_PORTS"));return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureLACP(q.Group,q.Ports,q.Mode,q.Delete)});write(w,v,e)})
 m.HandleFunc("/api/stp",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,1024);var q struct{Mode string `json:"mode"`;Priority int `json:"priority"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureSTP(q.Mode,q.Priority)});write(w,v,e)})
 m.HandleFunc("/api/ports/edgeport/bulk",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  r.Body=http.MaxBytesReader(w,r.Body,4096)
  var q struct{Ports []string `json:"ports"`;Enabled bool `json:"enabled"`}
  if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
  for _,port:=range q.Ports{if isProtectedPort(port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",port));return}}
  if _,err:=device.BuildBulkPortEdgePortCommands(q.Ports,q.Enabled);err!=nil{write(w,"",err);return}
  v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureBulkPortEdgePort(q.Ports,q.Enabled)})
  write(w,v,e)
 })
 m.HandleFunc("/api/edgeport",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,1024);var q struct{Port string `json:"port"`;Enabled bool `json:"enabled"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};if isProtectedPort(q.Port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",q.Port));return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.SetPortEdgePort(q.Port,q.Enabled)});write(w,v,e)})
 m.HandleFunc("/api/save",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.SaveConfig()});write(w,v,e)})
 m.HandleFunc("/api/system",func(w http.ResponseWriter,r *http.Request){if r.Method!="GET"{http.Error(w,"method not allowed",405);return};w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(svc.SystemInfo())})
 m.Handle("/api/port/switchport", switchportDetailsHandler(svc.RunReadOnly))
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
 Added int `json:"added"`
 Removed int `json:"removed"`
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
 result := configCompareResult{Added: 0, Removed: 0, RunningLines: len(rLines), StartupLines: len(sLines)}
 for _, line := range rLines {
  if counts[line] > 0 { counts[line]-- } else { result.Added++ }
 }
 counts = make(map[string]int, len(rLines))
 for _, line := range rLines { counts[line]++ }
 for _, line := range sLines {
  if counts[line] > 0 { counts[line]-- } else { result.Removed++ }
 }
 return result
}

func nonEmptyConfigLines(raw string) []string {
 lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
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


type configBackupCompareResult struct {
 BackupID string `json:"backupId"`
 SHA256 string `json:"sha256"`
 BackupLines int `json:"backupLines"`
 CurrentLines int `json:"currentLines"`
 AddedInCurrent int `json:"addedInCurrent"`
 RemovedFromCurrent int `json:"removedFromCurrent"`
 Identical bool `json:"identical"`
 ComparedAt time.Time `json:"comparedAt"`
}

// configBackupCompareHandler compares a verified local snapshot with the current
// running-config. It deliberately returns counts only: configuration lines can
// contain passwords, communities, and other management secrets.
func configBackupCompareHandler(run func(string) (string, error), dir string) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  if r.Method != http.MethodGet {
   http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
   return
  }
  id := r.URL.Query().Get("id")
  if !validBackupID(id) {
   http.Error(w, "invalid backup id", http.StatusBadRequest)
   return
  }
  data, err := os.ReadFile(filepath.Join(dir, id+".cfg"))
  if os.IsNotExist(err) {
   http.NotFound(w, r)
   return
  }
  if err != nil {
   http.Error(w, "backup read failed", http.StatusInternalServerError)
   return
  }
  expected, err := os.ReadFile(filepath.Join(dir, id+".sha256"))
  if os.IsNotExist(err) {
   http.Error(w, "backup is not verified; comparison blocked", http.StatusConflict)
   return
  }
  if err != nil {
   http.Error(w, "backup integrity metadata read failed", http.StatusInternalServerError)
   return
  }
  sum := sha256.Sum256(data)
  digest := hex.EncodeToString(sum[:])
  if strings.TrimSpace(string(expected)) != digest {
   http.Error(w, "backup integrity check failed; comparison blocked", http.StatusConflict)
   return
  }

  current, err := run("show running-config")
  if err != nil {
   http.Error(w, "current running configuration read failed", http.StatusBadGateway)
   return
  }
  if strings.TrimSpace(current) == "" {
   http.Error(w, "switch returned an empty running configuration", http.StatusBadGateway)
   return
  }
  diff := compareConfigLines(current, string(data))
  result := configBackupCompareResult{
   BackupID: id,
   SHA256: digest,
   BackupLines: len(nonEmptyConfigLines(string(data))),
   CurrentLines: len(nonEmptyConfigLines(current)),
   AddedInCurrent: diff.Added,
   RemovedFromCurrent: diff.Removed,
   Identical: diff.Added == 0 && diff.Removed == 0,
   ComparedAt: time.Now().UTC(),
  }
  w.Header().Set("Content-Type", "application/json; charset=utf-8")
  w.Header().Set("Cache-Control", "no-store")
  _ = json.NewEncoder(w).Encode(result)
 })
}

// configBackupRestorePreviewHandler validates a snapshot for inspection only.
// It deliberately does not send configuration to the switch: AlliedWare Plus restore
// semantics are not yet verified for the supported hardware/firmware combination.
func configBackupRestorePreviewHandler(dir string) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  if r.Method != http.MethodGet { http.Error(w, "method not allowed", http.StatusMethodNotAllowed); return }
  id := r.URL.Query().Get("id")
  if !validBackupID(id) { http.Error(w, "invalid backup id", http.StatusBadRequest); return }
  data, err := os.ReadFile(filepath.Join(dir, id+".cfg"))
  if os.IsNotExist(err) { http.NotFound(w, r); return }
  if err != nil { http.Error(w, "backup read failed", http.StatusInternalServerError); return }
  expected, err := os.ReadFile(filepath.Join(dir, id+".sha256"))
  if os.IsNotExist(err) { http.Error(w, "backup is not verified; restore preview blocked", http.StatusConflict); return }
  if err != nil { http.Error(w, "backup integrity metadata read failed", http.StatusInternalServerError); return }
  sum := sha256.Sum256(data)
  digest := hex.EncodeToString(sum[:])
  if strings.TrimSpace(string(expected)) != digest { http.Error(w, "backup integrity check failed; restore preview blocked", http.StatusConflict); return }
  lines := nonEmptyConfigLines(string(data))
  if len(lines) == 0 { http.Error(w, "backup is empty; restore preview blocked", http.StatusConflict); return }
  w.Header().Set("Content-Type", "application/json; charset=utf-8")
  w.Header().Set("Cache-Control", "no-store")
  _ = json.NewEncoder(w).Encode(map[string]any{
   "backupId": id, "sha256": digest, "size": len(data), "lineCount": len(lines),
   "integrityVerified": true, "restoreAvailable": false,
   "message": "Kopia jest integralna i może być dalej analizowana. Przywracanie pozostaje zablokowane do czasu potwierdzenia bezpiecznej procedury dla AlliedWare Plus.",
  })
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
func protectedPortNames() []string {
 seen:=map[string]bool{}
 ports:=[]string{}
 for _,item:=range strings.Split(os.Getenv("PROTECTED_PORTS"),",") {
  name:=strings.TrimSpace(item)
  if !strings.HasPrefix(strings.ToLower(name),"port1.0.") { continue }
  number,err:=strconv.Atoi(name[len("port1.0."):])
  if err!=nil || number<1 || number>52 || !strings.EqualFold(name,fmt.Sprintf("port1.0.%d",number)) { continue }
  key:=strings.ToLower(name)
  if seen[key] { continue }
  seen[key]=true
  ports=append(ports,key)
 }
 sort.Strings(ports)
 return ports
}

func protectedPortsHandler() http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  if r.Method!=http.MethodGet { http.Error(w,"method not allowed",http.StatusMethodNotAllowed); return }
  ports:=protectedPortNames()
  w.Header().Set("Content-Type","application/json")
  w.Header().Set("Cache-Control","no-store")
  _=json.NewEncoder(w).Encode(map[string]any{
   "configured":len(ports)>0,
   "ports":ports,
   "count":len(ports),
   "automaticDetection":false,
  })
 })
}

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

type bulkPortPreviewRequest struct {
 Ports []string `json:"ports"`
 Mode string `json:"mode"`
 AccessVLAN int `json:"accessVlan"`
 Allowed string `json:"allowed"`
 Native int `json:"native"`
 Speed string `json:"speed"`
 Duplex string `json:"duplex"`
 Description string `json:"description"`
}

type bulkPortPreviewItem struct {
 Port string `json:"port"`
 Current map[string]any `json:"current"`
 Warnings []string `json:"warnings"`
}

func bulkSwitchportDetailsHandler(read func(string) (string,error)) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  if r.Method != http.MethodPost { http.Error(w,"method not allowed",http.StatusMethodNotAllowed); return }
  r.Body = http.MaxBytesReader(w,r.Body,4096)
  var q struct { Ports []string `json:"ports"` }
  if err:=json.NewDecoder(r.Body).Decode(&q);err!=nil { http.Error(w,"invalid JSON",http.StatusBadRequest); return }
  if _,err:=device.BuildBulkPortActionCommands(q.Ports,"enable");err!=nil { http.Error(w,err.Error(),http.StatusBadRequest); return }
  ports:=make([]string,0,len(q.Ports))
  seen:=make(map[string]bool,len(q.Ports))
  for _,raw:=range q.Ports {
   port:=strings.TrimSpace(raw)
   key:=strings.ToLower(port)
   if !seen[key] { seen[key]=true;ports=append(ports,port) }
  }
  output,err:=read("show interface switchport")
  if err!=nil { http.Error(w,"nie udało się odczytać konfiguracji portów po zmianie",http.StatusBadGateway);return }
  details:=make([]map[string]any,0,len(ports))
  for _,port:=range ports {
   current,parseErr:=device.ParseSwitchportDetails(output,port)
   if parseErr!=nil { http.Error(w,"nie udało się potwierdzić konfiguracji portu "+port,http.StatusConflict);return }
   details=append(details,map[string]any{"port":port,"mode":current.Mode,"defaultVlan":current.DefaultVLAN,"configuredVlans":current.ConfiguredVLANs})
  }
  w.Header().Set("Content-Type","application/json")
  w.Header().Set("Cache-Control","no-store")
  _=json.NewEncoder(w).Encode(map[string]any{"ports":details,"readOperations":1})
 })
}

func validatePortRepair(read func(string)(string,error),q device.PortRepairRequest)([]string,device.SwitchportDetails,error){
 q.Port=strings.TrimSpace(q.Port)
 if isProtectedPort(q.Port){return nil,device.SwitchportDetails{},fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",q.Port)}
 raw,err:=read("show interface switchport");if err!=nil{return nil,device.SwitchportDetails{},fmt.Errorf("nie udało się odczytać bieżącej konfiguracji switchport")}
 current,err:=device.ParseSwitchportDetails(raw,q.Port);if err!=nil{return nil,device.SwitchportDetails{},fmt.Errorf("nie udało się jednoznacznie odczytać portu %s",q.Port)}
 if !strings.EqualFold(strings.TrimSpace(current.Mode),q.Mode){return nil,device.SwitchportDetails{},fmt.Errorf("tryb portu zmienił się od audytu lub nie zgadza się z profilem; wymagany jest osobny audyt i pełna zmiana trybu")}
 commands,err:=device.BuildPortRepairCommands(q);if err!=nil{return nil,device.SwitchportDetails{},err}
 return commands,current,nil
}

func bulkPortPreviewHandler(read func(string) (string,error)) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  if r.Method != http.MethodPost { http.Error(w,"method not allowed",http.StatusMethodNotAllowed); return }
  r.Body = http.MaxBytesReader(w,r.Body,8192)
  var q bulkPortPreviewRequest
  if err := json.NewDecoder(r.Body).Decode(&q); err != nil { http.Error(w,"invalid JSON",http.StatusBadRequest); return }
  if _,err := device.BuildBulkPortSettingsCommands(q.Ports,q.Mode,q.AccessVLAN,q.Allowed,q.Native,q.Speed,q.Duplex,q.Description); err != nil {
   http.Error(w,err.Error(),http.StatusBadRequest); return
  }
  ports := make([]string,0,len(q.Ports))
  seen := make(map[string]bool,len(q.Ports))
  for _,raw := range q.Ports {
   port := strings.TrimSpace(raw)
   if isProtectedPort(port) { http.Error(w,"port "+port+" jest chroniony przez PROTECTED_PORTS",http.StatusBadRequest); return }
   key := strings.ToLower(port)
   if !seen[key] { seen[key]=true; ports=append(ports,port) }
  }
  raw,err := read("show interface switchport")
  if err != nil { http.Error(w,"nie udało się odczytać bieżącej konfiguracji portów; podgląd i zapis zostały zablokowane",http.StatusBadGateway); return }
  previews := make([]bulkPortPreviewItem,0,len(ports))
  for _,port := range ports {
   current,parseErr := device.ParseSwitchportDetails(raw,port)
   if parseErr != nil {
    http.Error(w,"nie udało się jednoznacznie rozpoznać bieżącej konfiguracji portu "+port+"; podgląd i zapis zostały zablokowane",http.StatusConflict); return
   }
   warnings := []string{"Bieżąca konfiguracja została odczytana bez wprowadzania zmian."}
   if strings.ToLower(current.Mode) != q.Mode { warnings=append(warnings,"Zmiana trybu portu z "+current.Mode+" na "+q.Mode+" może przerwać łączność urządzeń.") }
   if q.Mode == "trunk" { warnings=append(warnings,"Zmiana listy allowed VLAN może przerwać ruch urządzeń korzystających z tego portu.") }
   previews=append(previews,bulkPortPreviewItem{
    Port:port,
    Current:map[string]any{"port":current.Port,"mode":current.Mode,"defaultVlan":current.DefaultVLAN,"configuredVlans":current.ConfiguredVLANs},
    Warnings:warnings,
   })
  }
  w.Header().Set("Content-Type","application/json")
  w.Header().Set("Cache-Control","no-store")
  _ = json.NewEncoder(w).Encode(map[string]any{"previews":previews,"applies":false,"readOperations":1})
 })
}

func switchportDetailsHandler(read func(string) (string,error)) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  if r.Method != http.MethodGet { http.Error(w,"method not allowed",http.StatusMethodNotAllowed); return }
  port := strings.TrimSpace(r.URL.Query().Get("port"))
  raw,err := read("show interface switchport")
  if err != nil { http.Error(w,"nie udało się odczytać switchport",http.StatusBadGateway); return }
  details,err := device.ParseSwitchportDetails(raw,port)
  if err != nil {
   w.Header().Set("Content-Type","application/json")
   w.WriteHeader(http.StatusNotFound)
   _ = json.NewEncoder(w).Encode(map[string]string{"error":err.Error(),"raw":raw})
   return
  }
  w.Header().Set("Content-Type","application/json")
  w.Header().Set("Cache-Control","no-store")
  _ = json.NewEncoder(w).Encode(details)
 })
}


type portProfile struct {
 ID string `json:"id"`
 Name string `json:"name"`
 Description string `json:"description"`
 PortDescription string `json:"portDescription"`
 Mode string `json:"mode"`
 AccessVLAN int `json:"accessVlan"`
 Allowed string `json:"allowed"`
 Native int `json:"native"`
 Speed string `json:"speed"`
 Duplex string `json:"duplex"`
 UpdatedAt time.Time `json:"updatedAt"`
}
type portProfileStore struct { mu sync.Mutex; path string }

func portProfilesHandler(path string) http.Handler {
 store:=&portProfileStore{path:path}
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  w.Header().Set("Cache-Control","no-store")
  switch r.Method {
  case http.MethodGet:
   store.mu.Lock();defer store.mu.Unlock()
   profiles,err:=store.load();if err!=nil{http.Error(w,"nie udało się odczytać profili portów",500);return}
   w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(map[string]any{"profiles":profiles})
  case http.MethodPost:
   r.Body=http.MaxBytesReader(w,r.Body,8192)
   var q portProfile;dec:=json.NewDecoder(r.Body);dec.DisallowUnknownFields()
   if err:=dec.Decode(&q);err!=nil{http.Error(w,"nieprawidłowe dane profilu",400);return}
   q.Name=strings.TrimSpace(q.Name);q.Description=strings.TrimSpace(q.Description)
   if q.Name==""||len([]rune(q.Name))>40||len([]rune(q.Description))>120{http.Error(w,"nazwa jest wymagana (maks. 40 znaków), opis maks. 120 znaków",400);return}
   if q.Speed==""{q.Speed="auto"};if q.Duplex==""{q.Duplex="auto"}
   access:=q.AccessVLAN;if access==0{access=1}
   if _,err:=device.BuildPortSettingsCommands("port1.0.1",q.Mode,access,q.Allowed,q.Native,q.Speed,q.Duplex,q.PortDescription);err!=nil{http.Error(w,err.Error(),400);return}
   store.mu.Lock();defer store.mu.Unlock()
   profiles,err:=store.load();if err!=nil{http.Error(w,"nie udało się odczytać rejestru profili",500);return}
   if q.ID!=""&&!validPortProfileID(q.ID){http.Error(w,"nieprawidłowy identyfikator profilu",400);return}
   found:=false
   if q.ID!=""{for i:=range profiles{if profiles[i].ID==q.ID{found=true;break}};if !found{http.Error(w,"profil nie istnieje",404);return}}
   for _,existing:=range profiles{if strings.EqualFold(existing.Name,q.Name)&&existing.ID!=q.ID{http.Error(w,"profil o takiej nazwie już istnieje",http.StatusConflict);return}}
   if q.ID==""{if len(profiles)>=100{http.Error(w,"osiągnięto limit 100 profili",409);return};var id [6]byte;if _,err:=rand.Read(id[:]);err!=nil{http.Error(w,"nie udało się utworzyć identyfikatora",500);return};q.ID="profile-"+hex.EncodeToString(id[:])}
   q.UpdatedAt=time.Now().UTC()
   if found{for i:=range profiles{if profiles[i].ID==q.ID{profiles[i]=q;break}}}else{profiles=append(profiles,q)}
   if err:=store.save(profiles);err!=nil{http.Error(w,"nie udało się zapisać profilu",500);return}
   w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(map[string]any{"profile":q,"profiles":profiles})
  case http.MethodDelete:
   id:=r.URL.Query().Get("id");if !validPortProfileID(id){http.Error(w,"nieprawidłowy identyfikator profilu",400);return}
   store.mu.Lock();defer store.mu.Unlock()
   profiles,err:=store.load();if err!=nil{http.Error(w,"nie udało się odczytać rejestru profili",500);return}
   next:=make([]portProfile,0,len(profiles));found:=false
   for _,p:=range profiles{if p.ID==id{found=true;continue};next=append(next,p)}
   if !found{http.Error(w,"profil nie istnieje",404);return}
   if err:=store.save(next);err!=nil{http.Error(w,"nie udało się zapisać rejestru profili",500);return}
   w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(map[string]any{"profiles":next})
  default:
   w.Header().Set("Allow","GET, POST, DELETE");http.Error(w,"method not allowed",405)
  }
 })
}
func validPortProfileID(id string)bool{
 if !strings.HasPrefix(id,"profile-")||len(id)!=20{return false}
 for _,r:=range id[8:]{if !(r>='0'&&r<='9'||r>='a'&&r<='f'){return false}}
 return true
}
func (s *portProfileStore) load()([]portProfile,error){
 data,err:=os.ReadFile(s.path);if os.IsNotExist(err){return []portProfile{},nil};if err!=nil{return nil,err}
 var profiles []portProfile;if err:=json.Unmarshal(data,&profiles);err!=nil{return nil,err};if profiles==nil{profiles=[]portProfile{}}
 return profiles,nil
}
func (s *portProfileStore) save(profiles []portProfile)error{
 if err:=os.MkdirAll(filepath.Dir(s.path),0700);err!=nil{return err}
 data,err:=json.MarshalIndent(profiles,"","  ");if err!=nil{return err}
 tmp:=s.path+".tmp";if err:=os.WriteFile(tmp,data,0600);err!=nil{return err}
 if err:=os.Rename(tmp,s.path);err!=nil{_ = os.Remove(tmp);return err};return nil
}

type securityAuditFinding struct {
 Severity string `json:"severity"`
 Code string `json:"code"`
 Title string `json:"title"`
 Evidence string `json:"evidence"`
 Recommendation string `json:"recommendation"`
}
func analyzeSecurityConfiguration(config string) []securityAuditFinding {
 findings:=make([]securityAuditFinding,0)
 for _,line:=range strings.Split(config,"\n") {
  normalized:=strings.ToLower(strings.TrimSpace(line))
  fields:=strings.Fields(normalized)
  if len(fields)>=3&&fields[0]=="snmp-server"&&fields[1]=="community"&&(fields[2]=="public"||fields[2]=="private") {
   findings=append(findings,securityAuditFinding{"critical","snmp-default-community","Wykryto domyślną nazwę community SNMP","Konfiguracja zawiera community o nazwie "+fields[2]+".","Zastąp community unikalną wartością lub przejdź na SNMPv3; sprawdź urządzenia monitorujące przed zmianą."})
  }
  if normalized=="ip http server"||normalized=="http server enable" {
   findings=append(findings,securityAuditFinding{"warning","http-management-enabled","W konfiguracji jawnie włączono serwer HTTP","Wykryto wpis: "+strings.TrimSpace(line),"Zweryfikuj, czy panel HTTP jest potrzebny. Jeśli nie, wyłącz go po zapewnieniu dostępu przez bezpieczny kanał zarządzania."})
  }
  if normalized=="ip telnet server"||normalized=="telnet server enable" {
   findings=append(findings,securityAuditFinding{"critical","telnet-management-enabled","W konfiguracji jawnie włączono Telnet","Wykryto wpis: "+strings.TrimSpace(line),"Preferuj SSH. Wyłącz Telnet dopiero po potwierdzeniu działającego dostępu SSH."})
  }
  if normalized=="no spanning-tree"||normalized=="spanning-tree disable" {
   findings=append(findings,securityAuditFinding{"critical","stp-disabled","Wykryto jawne wyłączenie Spanning Tree","W konfiguracji znaleziono: "+strings.TrimSpace(line),"Zweryfikuj topologię warstwy 2 i włącz właściwy tryb STP/RSTP po zaplanowaniu zmiany."})
  }
 }
 return findings
}

func parseAuditVLANSet(raw string) map[int]bool {
 out:=map[int]bool{}
 for _,part:=range strings.Split(strings.TrimSpace(raw),","){
  part=strings.TrimSpace(part);if part==""{continue}
  if loRaw,hiRaw,ok:=strings.Cut(part,"-");ok{
   lo,e1:=strconv.Atoi(strings.TrimSpace(loRaw));hi,e2:=strconv.Atoi(strings.TrimSpace(hiRaw))
   if e1==nil&&e2==nil&&lo>=1&&hi<=4094&&hi>=lo&&hi-lo<=4093{for n:=lo;n<=hi;n++{out[n]=true}}
  }else if n,err:=strconv.Atoi(part);err==nil&&n>=1&&n<=4094{out[n]=true}
 }
 return out
}
func analyzeVLANAndPortSecurity(config,switchport string) []securityAuditFinding {
 findings:=make([]securityAuditFinding,0)
 declared:=map[int]bool{}
 referenced:=map[int]bool{}
 type ifaceConfig struct{name,mode,native,allowed,access string}
 interfaces:=make([]ifaceConfig,0)
 var current *ifaceConfig
 for _,line:=range strings.Split(config,"\n"){
  trimmed:=strings.TrimSpace(line);lower:=strings.ToLower(trimmed)
  if strings.HasPrefix(lower,"interface "){
   interfaces=append(interfaces,ifaceConfig{name:strings.TrimSpace(trimmed[len("interface "):])})
   current=&interfaces[len(interfaces)-1];continue
  }
  if current!=nil {
   fields:=strings.Fields(lower)
   if len(fields)>=2&&fields[0]=="switchport"&&fields[1]=="mode"&&len(fields)>=3{current.mode=fields[2]}
   if len(fields)>=4&&fields[0]=="switchport"&&fields[1]=="access"&&fields[2]=="vlan"{current.access=fields[3]}
   if len(fields)>=5&&fields[0]=="switchport"&&fields[1]=="trunk"&&fields[2]=="native"&&fields[3]=="vlan"{current.native=fields[4]}
   if len(fields)>=5&&fields[0]=="switchport"&&fields[1]=="trunk"&&fields[2]=="allowed"&&fields[3]=="vlan"{
    value:=strings.Join(fields[4:],"")
    if value=="none"{current.allowed=""}else if strings.HasPrefix(value,"add"){current.allowed+=strings.TrimPrefix(value,"add")}else if strings.HasPrefix(value,"remove"){remove:=parseAuditVLANSet(strings.TrimPrefix(value,"remove"));keep:=make([]string,0);for _,v:=range strings.Split(current.allowed,","){n,e:=strconv.Atoi(v);if e!=nil||!remove[n]{keep=append(keep,v)}};current.allowed=strings.Join(keep,",")}else{current.allowed=value}
   }
  }
  if strings.HasPrefix(lower,"vlan "){
   fields:=strings.Fields(lower);if len(fields)==2{if n,e:=strconv.Atoi(fields[1]);e==nil&&n>=1&&n<=4094{declared[n]=true}}
  }
 }
 for _,p:=range interfaces{
  if n,e:=strconv.Atoi(p.access);e==nil&&n>=1&&n<=4094{referenced[n]=true}
  for n:=range parseAuditVLANSet(p.allowed){referenced[n]=true}
  if p.mode=="trunk"&&p.native!=""&&p.allowed!=""{
   native,e:=strconv.Atoi(p.native);allowed:=parseAuditVLANSet(p.allowed)
   if e==nil&&!allowed[native]{findings=append(findings,securityAuditFinding{"warning","trunk-native-not-allowed","Native VLAN nie znajduje się na jawnej liście allowed",p.name+": native VLAN "+p.native+"; allowed "+p.allowed,"Zweryfikuj zamierzony ruch tagowany i nietagowany. Dodaj native VLAN do allowed lub popraw native VLAN po sprawdzeniu konfiguracji sąsiada."})}
  }
 }
 if len(declared)>0{
  for vlan:=range declared{if !referenced[vlan]&&vlan!=1{findings=append(findings,securityAuditFinding{"info","vlan-no-explicit-port-reference","VLAN bez jawnego przypisania w running-config",fmt.Sprintf("VLAN %d istnieje, ale parser nie znalazł jawnego przypisania access ani allowed trunk.",vlan),"Sprawdź członkostwo VLAN i tablicę MAC. To kandydat do weryfikacji, a nie dowód, że VLAN jest nieużywany."})}}
  for vlan:=range referenced{if !declared[vlan]&&vlan!=1{findings=append(findings,securityAuditFinding{"warning","port-references-undeclared-vlan","Port odwołuje się do VLAN nieznajdującego się w deklaracjach",fmt.Sprintf("VLAN %d jest użyty w konfiguracji interfejsu, ale nie znaleziono deklaracji vlan %d.",vlan,vlan),"Sprawdź pełny running-config i stan bazy VLAN. Nie twórz ani nie usuwaj VLAN automatycznie."})}}
 }
 blocks:=regexp.MustCompile(`(?im)^\s*Interface name\s*:\s*(\S+)\s*$`).FindAllStringSubmatchIndex(switchport,-1)
 for i,loc:=range blocks{
  name:=strings.TrimSpace(switchport[loc[2]:loc[3]]);end:=len(switchport);if i+1<len(blocks){end=blocks[i+1][0]}
  block:=switchport[loc[0]:end]
  val:=func(label string)string{re:=regexp.MustCompile(`(?im)^\s*`+regexp.QuoteMeta(label)+`\s*:\s*(.*?)\s*$`);m:=re.FindStringSubmatch(block);if len(m)>1{return strings.TrimSpace(m[1])};return ""}
  mode:=strings.ToLower(val("Switchport mode"));nativeRaw:=val("Default Vlan")
  configured:=val("Configured Vlans")
  if mode=="trunk"&&nativeRaw!=""&&configured!=""{
   native,e:=strconv.Atoi(nativeRaw);allowed:=map[int]bool{}
   for _,token:=range strings.Fields(configured){if n,e:=strconv.Atoi(strings.Trim(token,","));e==nil&&n>=1&&n<=4094{allowed[n]=true}}
   if e==nil&&!allowed[native]{findings=append(findings,securityAuditFinding{"warning","trunk-native-not-in-configured-vlans","Native VLAN nie występuje w odczycie VLAN skonfigurowanych na trunku",name+": native VLAN "+nativeRaw+"; configured VLANs "+configured,"Zweryfikuj listę VLAN na obu końcach łącza przed zmianą. Odczyt switchport i running-config mogą mieć różne formaty lub zakresy."})}
  }
 }
 return findings
}
func securityAuditHandler(read func(string)(string,error)) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodGet{http.Error(w,"method not allowed",http.StatusMethodNotAllowed);return}
  config,err:=read("show running-config")
  if err!=nil{http.Error(w,"nie udało się odczytać running-config; audyt bezpieczeństwa nie został wykonany",http.StatusBadGateway);return}
  findings:=analyzeSecurityConfiguration(config)
  checks:=map[string]bool{"runningConfig":true}
  switchport,switchportErr:=read("show interface switchport")
  if switchportErr==nil&&strings.Contains(switchport,"Interface name"){findings=append(findings,analyzeVLANAndPortSecurity(config,switchport)...);checks["switchport"]=true}else{checks["switchport"]=false}
  if switchportErr!=nil{findings=append(findings,securityAuditFinding{"info","switchport-data-unavailable","Brak odczytu szczegółów switchport","Nie udało się pobrać lub rozpoznać show interface switchport.","Ponów audyt; analiza spójności trunków jest niekompletna."})}
  w.Header().Set("Content-Type","application/json; charset=utf-8")
  w.Header().Set("Cache-Control","no-store")
  _=json.NewEncoder(w).Encode(map[string]any{"available":true,"readOnly":true,"findings":findings,"checks":checks,"note":"VLAN bez jawnego przypisania są kandydatami do weryfikacji, nie dowodem nieużywania. Wnioski trunk wymagają rozpoznanych pól switchport. Brak wykrycia nie jest dowodem bezpieczeństwa."})
 })
}
