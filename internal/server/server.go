package server

import (
 "encoding/json"
 "encoding/csv"
 "context"
 "log"
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
 "regexp"
 "time"
 "net/http"
 "strings"
 "gs948mx-manager/internal/device"
)

func Register(m *http.ServeMux,svc *device.Service){
 apiMux:=http.NewServeMux()
 userPath:=os.Getenv("PANEL_USERS_PATH");if userPath==""{userPath=filepath.Join("data","panel-users.json")}
 panelUsers:=newPanelUserStore(userPath)
 apiMux.Handle("/api/users",panelUsers.usersHandler())
 apiMux.Handle("/api/users/",panelUsers.usersHandler())
 apiMux.Handle("/",http.FileServer(http.Dir("web")))
 apiMux.Handle("/api/config/export", configExportHandler(svc.RunReadOnly))
 apiMux.Handle("/api/config/compare", configCompareHandler(svc.RunReadOnly))
 backupDir := os.Getenv("CONFIG_BACKUP_DIR")
 if backupDir == "" { backupDir = filepath.Join("data", "config-backups") }
 retention := 100
 if raw := os.Getenv("CONFIG_BACKUP_RETENTION"); raw != "" { if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 10000 { retention = n } }
 apiMux.Handle("/api/config/backups", configBackupsHandler(svc.RunReadOnly, backupDir, retention))
 apiMux.Handle("/api/config/backups/download", configBackupDownloadHandler(backupDir))
 apiMux.Handle("/api/config/backups/compare", configBackupCompareHandler(svc.RunReadOnly, backupDir))
 apiMux.Handle("/api/config/backups/restore-preview", configBackupRestorePreviewHandler(backupDir))
 apiMux.Handle("/api/audit", auditHistoryHandler(os.Getenv("AUDIT_LOG_PATH")))
 apiMux.Handle("/api/security-audit", securityAuditHandler(svc.RunReadOnly))
 apiMux.Handle("/api/topology/map", topologyMapHandler(svc.RunReadOnly))
 diagnosticsPath:=os.Getenv("DIAGNOSTICS_HISTORY_PATH");if diagnosticsPath==""{diagnosticsPath=filepath.Join("data","diagnostics-history.json")}
 diagnosticsStore:=sharedDiagnosticsStore(diagnosticsPath,12000)
 apiMux.Handle("/api/diagnostics/collect",diagnosticsCollectHandler(svc.RunReadOnly,diagnosticsStore))
 apiMux.Handle("/api/diagnostics/history",diagnosticsHistoryHandler(diagnosticsStore))
 apiMux.Handle("/api/events",diagnosticsEventsHandler(diagnosticsStore))
 apiMux.Handle("/api/diagnostics/availability",diagnosticsAvailabilityHandler(diagnosticsStore))
 apiMux.Handle("/api/diagnostics/availability.csv",diagnosticsAvailabilityCSVHandler(diagnosticsStore))
 apiMux.Handle("/api/diagnostics/acknowledge",diagnosticsAcknowledgeHandler(diagnosticsStore,diagnosticsPath+".acks.json"))
 apiMux.Handle("/api/monitor/history", monitorHistoryHandler(filepath.Join("data", "monitor-history.jsonl")))
 apiMux.Handle("/api/monitor/stream", monitorStreamHandler())
 apiMux.HandleFunc("/api/status",func(w http.ResponseWriter,r *http.Request){if r.Method!="GET"{http.Error(w,"method not allowed",405);return};w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(svc.Status())})
 apiMux.Handle("/api/ports/protection", protectedPortsHandler())
 profilePath:=os.Getenv("PORT_PROFILES_PATH");if profilePath==""{profilePath=filepath.Join("data","port-profiles.json")}
 apiMux.Handle("/api/port-profiles",portProfilesHandler(profilePath))
 apiMux.Handle("/api/ports/preview/bulk", bulkPortPreviewHandler(svc.RunReadOnly))
 apiMux.Handle("/api/ports/switchport/bulk", bulkSwitchportDetailsHandler(svc.RunReadOnly))
 apiMux.HandleFunc("/api/port/preview",func(w http.ResponseWriter,r *http.Request){
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
 risks:=topologyChangeRisks(svc.RunReadOnly,q.Port)
 for _,risk:=range risks{warnings=append(warnings,risk.Title+": "+risk.Evidence+" Zalecenie: "+risk.Recommendation)}
 if strings.ToLower(current.Mode)!=q.Mode{warnings=append(warnings,"Zmiana trybu portu z "+current.Mode+" na "+q.Mode+" może przerwać łączność urządzeń.")}
 if q.Mode=="trunk"{warnings=append(warnings,"Zmiana listy allowed VLAN może przerwać ruch urządzeń korzystających z tego portu.")}
 w.Header().Set("Content-Type","application/json")
 w.Header().Set("Cache-Control","no-store")
 _=json.NewEncoder(w).Encode(map[string]any{
  "commands":cmds,"warnings":warnings,"risks":risks,"applies":false,
  "current":map[string]any{"port":current.Port,"mode":current.Mode,"defaultVlan":current.DefaultVLAN,"configuredVlans":current.ConfiguredVLANs},
 })
})
apiMux.HandleFunc("/api/vlan/preview",func(w http.ResponseWriter,r *http.Request){
 if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
 r.Body=http.MaxBytesReader(w,r.Body,2048)
 var q struct{ID int `json:"id"`;Name string `json:"name"`;Delete bool `json:"delete"`}
 if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
 cmds,err:=device.BuildVLANCommands(q.ID,q.Name,q.Delete);if err!=nil{http.Error(w,err.Error(),400);return}
 warnings:=[]string{"Podgląd nie zmienia konfiguracji przełącznika."}
 if q.Delete{warnings=append(warnings,"Usunięcie VLAN może przerwać łączność wszystkich przypisanych do niego urządzeń.")}
 w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(map[string]any{"commands":cmds,"warnings":warnings,"applies":false})
})
apiMux.HandleFunc("/api/port",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Port string `json:"port"`;Action string `json:"action"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};if isProtectedPort(q.Port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",q.Port));return};if q.Action=="disable"&&!portShutdownEnabled(){write(w,"",fmt.Errorf("wyłączanie portów jest domyślnie zablokowane; ustaw ALLOW_PORT_SHUTDOWN=true po sprawdzeniu ryzyka utraty dostępu"));return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigurePort(q.Port,q.Action)});write(w,v,e)})
 apiMux.HandleFunc("/api/management-dhcp",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,1024);var q struct{Enabled bool `json:"enabled"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};if !managementDHCPChangeEnabled(){write(w,"",fmt.Errorf("zmiana DHCP interfejsu zarządzającego jest domyślnie zablokowana; ustaw ALLOW_MANAGEMENT_DHCP_CHANGE=true po zapewnieniu alternatywnego dostępu"));return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.SetManagementDHCP(q.Enabled)});write(w,v,e)})
 apiMux.HandleFunc("/api/ssh/user",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Username string `json:"username"`;Password string `json:"password"`;Privilege int `json:"privilege"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureSSHUser(q.Username,q.Password,q.Privilege,q.Delete)});write(w,v,e)})
 apiMux.HandleFunc("/api/snmp/trap-host",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Address string `json:"address"`;Community string `json:"community"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureSNMPTrapHost(q.Address,q.Community,q.Delete)});write(w,v,e)})
 apiMux.HandleFunc("/api/snmp/v3-user",func(w http.ResponseWriter,r *http.Request){
   if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
   r.Body=http.MaxBytesReader(w,r.Body,4096)
   var q struct{Username string `json:"username"`;Group string `json:"group"`;AuthPassword string `json:"authPassword"`;PrivacyPassword string `json:"privacyPassword"`;Delete bool `json:"delete"`}
   if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
   v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureSNMPv3User(q.Username,q.Group,q.AuthPassword,q.PrivacyPassword,q.Delete)})
   write(w,v,e)
  })
  apiMux.HandleFunc("/api/snmp/v3-trap-host",func(w http.ResponseWriter,r *http.Request){
   if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
   r.Body=http.MaxBytesReader(w,r.Body,2048)
   var q struct{Address string `json:"address"`;Username string `json:"username"`;Delete bool `json:"delete"`}
   if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
   v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureSNMPv3TrapHost(q.Address,q.Username,q.Delete)})
   write(w,v,e)
  })
  apiMux.HandleFunc("/api/network/probe",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Kind string `json:"kind"`;Target string `json:"target"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=svc.ProbeNetwork(q.Kind,q.Target);write(w,v,e)})
 apiMux.HandleFunc("/api/route/static",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Prefix string `json:"prefix"`;Gateway string `json:"gateway"`;Distance int `json:"distance"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureStaticRoute(q.Prefix,q.Gateway,q.Distance,q.Delete)});write(w,v,e)})
 apiMux.HandleFunc("/api/dhcp/pool",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,4096);var q struct{Name string `json:"name"`;Network string `json:"network"`;Mask string `json:"mask"`;Low string `json:"low"`;High string `json:"high"`;Router string `json:"router"`;DNS string `json:"dns"`;Enable bool `json:"enable"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureDHCPPool(q.Name,q.Network,q.Mask,q.Low,q.High,q.Router,q.DNS,q.Enable)});write(w,v,e)})
 apiMux.HandleFunc("/api/vlan",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{ID int `json:"id"`;Name string `json:"name"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureVLAN(q.ID,q.Name,q.Delete)});write(w,v,e)})

 apiMux.HandleFunc("/api/vlan/ports/preview",func(w http.ResponseWriter,r *http.Request){
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
 apiMux.HandleFunc("/api/vlan/ports/apply",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  r.Body=http.MaxBytesReader(w,r.Body,4096)
  var q struct{Ports []string `json:"ports"`;VLAN int `json:"vlan"`;Membership string `json:"membership"`}
  if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
  for _,port:=range q.Ports{if isProtectedPort(port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",port));return}}
  v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureVLANPortMembership(q.Ports,q.VLAN,q.Membership)})
  write(w,v,e)
 })
 apiMux.HandleFunc("/api/port/settings",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,4096);var q struct{Port string `json:"port"`;Mode string `json:"mode"`;AccessVLAN int `json:"accessVlan"`;Allowed string `json:"allowed"`;Native int `json:"native"`;Speed string `json:"speed"`;Duplex string `json:"duplex"`;Description string `json:"description"`;AcknowledgeConnectivityRisk bool `json:"acknowledgeConnectivityRisk"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};if isProtectedPort(q.Port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",q.Port));return};if err:=requireTopologyRiskAcknowledgement(svc.RunReadOnly,[]string{q.Port},q.AcknowledgeConnectivityRisk);err!=nil{write(w,"",err);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){if err:=requireTopologyRiskAcknowledgement(svc.RunReadOnly,[]string{q.Port},q.AcknowledgeConnectivityRisk);err!=nil{return "",err};return svc.ConfigurePortSettings(q.Port,q.Mode,q.AccessVLAN,q.Allowed,q.Native,q.Speed,q.Duplex,q.Description)});write(w,v,e)})
 apiMux.HandleFunc("/api/ports/repair/preview",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  r.Body=http.MaxBytesReader(w,r.Body,4096)
  var q device.PortRepairRequest
  if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
  if isProtectedPort(q.Port){http.Error(w,"port "+q.Port+" jest chroniony przez PROTECTED_PORTS",400);return}
  commands,current,err:=validatePortRepair(svc.RunReadOnly,q)
  if err!=nil{http.Error(w,err.Error(),400);return}
  risks:=topologyChangeRisks(svc.RunReadOnly,q.Port)
  warnings:=make([]string,0,len(risks));for _,risk:=range risks{warnings=append(warnings,risk.Title+": "+risk.Evidence+" Zalecenie: "+risk.Recommendation)}
  w.Header().Set("Content-Type","application/json");w.Header().Set("Cache-Control","no-store")
  _=json.NewEncoder(w).Encode(map[string]any{"port":q.Port,"current":current,"commands":commands,"risks":risks,"warnings":warnings,"applies":false})
 })
 apiMux.HandleFunc("/api/ports/repair/apply",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  r.Body=http.MaxBytesReader(w,r.Body,4096)
  var request struct { device.PortRepairRequest; AcknowledgeConnectivityRisk bool `json:"acknowledgeConnectivityRisk"` }
  if json.NewDecoder(r.Body).Decode(&request)!=nil{http.Error(w,"invalid JSON",400);return}
  q:=request.PortRepairRequest
  if isProtectedPort(q.Port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",q.Port));return}
  if _,_,err:=validatePortRepair(svc.RunReadOnly,q);err!=nil{write(w,"",err);return}
  if err:=requireTopologyRiskAcknowledgement(svc.RunReadOnly,[]string{q.Port},request.AcknowledgeConnectivityRisk);err!=nil{write(w,"",err);return}
  output,err:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){if _,_,checkErr:=validatePortRepair(svc.RunReadOnly,q);checkErr!=nil{return "",checkErr};if riskErr:=requireTopologyRiskAcknowledgement(svc.RunReadOnly,[]string{q.Port},request.AcknowledgeConnectivityRisk);riskErr!=nil{return "",riskErr};return svc.ConfigurePortRepair(q)})
  write(w,output,err)
 })
 apiMux.HandleFunc("/api/ports/settings/bulk",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  r.Body=http.MaxBytesReader(w,r.Body,8192)
  var q struct{Ports []string `json:"ports"`;Mode string `json:"mode"`;AccessVLAN int `json:"accessVlan"`;Allowed string `json:"allowed"`;Native int `json:"native"`;Speed string `json:"speed"`;Duplex string `json:"duplex"`;Description string `json:"description"`;AcknowledgeConnectivityRisk bool `json:"acknowledgeConnectivityRisk"`}
  if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
  for _,port:=range q.Ports{if isProtectedPort(port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",port));return}}
  if _,err:=device.BuildBulkPortSettingsCommands(q.Ports,q.Mode,q.AccessVLAN,q.Allowed,q.Native,q.Speed,q.Duplex,q.Description);err!=nil{write(w,"",err);return}
  if err:=requireTopologyRiskAcknowledgement(svc.RunReadOnly,q.Ports,q.AcknowledgeConnectivityRisk);err!=nil{write(w,"",err);return}
  v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){if err:=requireTopologyRiskAcknowledgement(svc.RunReadOnly,q.Ports,q.AcknowledgeConnectivityRisk);err!=nil{return "",err};return svc.ConfigureBulkPortSettings(q.Ports,q.Mode,q.AccessVLAN,q.Allowed,q.Native,q.Speed,q.Duplex,q.Description)})
  write(w,v,e)
 })
 apiMux.HandleFunc("/api/ports/action/bulk",func(w http.ResponseWriter,r *http.Request){
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
 apiMux.HandleFunc("/api/lacp",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Group int `json:"group"`;Ports string `json:"ports"`;Mode string `json:"mode"`;Delete bool `json:"delete"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};if protectedPortNumberList(q.Ports){write(w,"",fmt.Errorf("LACP obejmuje port chroniony przez PROTECTED_PORTS"));return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureLACP(q.Group,q.Ports,q.Mode,q.Delete)});write(w,v,e)})
 apiMux.HandleFunc("/api/stp",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,1024);var q struct{Mode string `json:"mode"`;Priority int `json:"priority"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureSTP(q.Mode,q.Priority)});write(w,v,e)})
 apiMux.HandleFunc("/api/ports/edgeport/bulk",func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  r.Body=http.MaxBytesReader(w,r.Body,4096)
  var q struct{Ports []string `json:"ports"`;Enabled bool `json:"enabled"`}
  if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
  for _,port:=range q.Ports{if isProtectedPort(port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",port));return}}
  if _,err:=device.BuildBulkPortEdgePortCommands(q.Ports,q.Enabled);err!=nil{write(w,"",err);return}
  v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.ConfigureBulkPortEdgePort(q.Ports,q.Enabled)})
  write(w,v,e)
 })
 apiMux.HandleFunc("/api/edgeport",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,1024);var q struct{Port string `json:"port"`;Enabled bool `json:"enabled"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};if isProtectedPort(q.Port){write(w,"",fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS",q.Port));return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.SetPortEdgePort(q.Port,q.Enabled)});write(w,v,e)})
 apiMux.HandleFunc("/api/save",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};v,e:=runWithPreChangeBackup(backupDir,svc.RunReadOnly,func()(string,error){return svc.SaveConfig()});write(w,v,e)})
 apiMux.HandleFunc("/api/system",func(w http.ResponseWriter,r *http.Request){if r.Method!="GET"{http.Error(w,"method not allowed",405);return};w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(svc.SystemInfo())})
 apiMux.Handle("/api/port/switchport", switchportDetailsHandler(svc.RunReadOnly))
 apiMux.HandleFunc("/api/command",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,4096);var q struct{Command string `json:"command"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=svc.RunReadOnly(strings.TrimSpace(q.Command));write(w,v,e)})
 m.Handle("/api/",panelUsers.middleware(apiMux))
 m.Handle("/",panelUsers.middleware(apiMux))
 m.HandleFunc("/login",panelLoginPageHandler)
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
  topologyConfig,topologyConfigErr:=read("show running-config")
  topologyLLDP,topologyLLDPErr:=read("show lldp neighbors")
  previews := make([]bulkPortPreviewItem,0,len(ports))
  for _,port := range ports {
   current,parseErr := device.ParseSwitchportDetails(raw,port)
   if parseErr != nil {
    http.Error(w,"nie udało się jednoznacznie rozpoznać bieżącej konfiguracji portu "+port+"; podgląd i zapis zostały zablokowane",http.StatusConflict); return
   }
   warnings := []string{"Bieżąca konfiguracja została odczytana bez wprowadzania zmian."}
   if strings.ToLower(current.Mode) != q.Mode { warnings=append(warnings,"Zmiana trybu portu z "+current.Mode+" na "+q.Mode+" może przerwać łączność urządzeń.") }
   if q.Mode == "trunk" { warnings=append(warnings,"Zmiana listy allowed VLAN może przerwać ruch urządzeń korzystających z tego portu.") }
   risks:=topologyChangeRisksFromSnapshot(topologyConfig,raw,topologyLLDP,topologyConfigErr,topologyLLDPErr,port)
   for _,risk:=range risks{warnings=append(warnings,risk.Title+": "+risk.Evidence+" Zalecenie: "+risk.Recommendation)}
   previews=append(previews,bulkPortPreviewItem{
    Port:port,
    Current:map[string]any{"port":current.Port,"mode":current.Mode,"defaultVlan":current.DefaultVLAN,"configuredVlans":current.ConfiguredVLANs},
    Warnings:warnings,
   })
  }
  w.Header().Set("Content-Type","application/json")
  w.Header().Set("Cache-Control","no-store")
  _ = json.NewEncoder(w).Encode(map[string]any{"previews":previews,"applies":false,"readOperations":3,"switchportReadOperations":1,"topologyReadOperations":2})
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
 type ifaceConfig struct{name,mode,native,allowed,access string;edgeport,lacp bool}
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
   if len(fields)>=2&&fields[0]=="spanning-tree"&&fields[1]=="edgeport"{current.edgeport=true}
   if len(fields)>=2&&(fields[0]=="channel-group"||fields[0]=="static-channel-group"){current.lacp=true}
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
  if p.edgeport&&p.mode=="trunk"{findings=append(findings,securityAuditFinding{"warning","edgeport-on-trunk","EdgePort jest skonfigurowany na porcie trunk",p.name+": spanning-tree edgeport oraz switchport mode trunk.","Zweryfikuj rolę portu i topologię. EdgePort jest przeznaczony dla urządzeń końcowych; nie włączaj go na łączach między przełącznikami bez potwierdzenia projektu STP."})}
  if p.edgeport&&p.lacp{findings=append(findings,securityAuditFinding{"warning","edgeport-on-lacp-member","EdgePort jest skonfigurowany na członku agregacji",p.name+": spanning-tree edgeport oraz channel-group.","Zweryfikuj agregację i sąsiedni switch. Nie traktuj członka LACP jako portu końcowego."})}
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

type topologyPort struct { Port string `json:"port"`; Mode string `json:"mode,omitempty"`; VLANs []int `json:"vlans,omitempty"`; NativeVLAN int `json:"nativeVlan,omitempty"`; LAG string `json:"lag,omitempty"`; EdgePort bool `json:"edgePort,omitempty"`; Description string `json:"description,omitempty"`; Role string `json:"role,omitempty"`; Protected bool `json:"protected"` }
type topologyLink struct { LocalPort string `json:"localPort"`; Neighbor string `json:"neighbor"`; Evidence string `json:"evidence"` }
type topologyAggregation struct { Name string `json:"name"`; Members []string `json:"members"`; LiveMembers []string `json:"liveMembers,omitempty"`; Live bool `json:"live"`; MemberMismatch bool `json:"memberMismatch"`; Partner string `json:"partner,omitempty"`; Ready string `json:"ready,omitempty"` }
type topologyRisk struct { Severity string `json:"severity"`; Code string `json:"code"`; Title string `json:"title"`; Evidence string `json:"evidence"`; Recommendation string `json:"recommendation"` }

type topologyImpact struct {
 Code string `json:"code"`
 Subject string `json:"subject"`
 ConfiguredPorts []string `json:"configuredPorts,omitempty"`
 LivePorts []string `json:"livePorts,omitempty"`
 VLANs []int `json:"vlans,omitempty"`
 Neighbors []string `json:"neighbors,omitempty"`
 VLANEvidenceAvailable bool `json:"vlanEvidenceAvailable"`
 Note string `json:"note"`
}

func buildTopologyImpacts(ports []topologyPort, links []topologyLink, aggregations []topologyAggregation, risks []topologyRisk) []topologyImpact {
 portByName:=make(map[string]topologyPort,len(ports))
 for _,p:=range ports { portByName[strings.ToLower(p.Port)]=p }
 neighborsByPort:=make(map[string][]string)
 for _,link:=range links { key:=strings.ToLower(link.LocalPort);neighborsByPort[key]=append(neighborsByPort[key],link.Neighbor) }
 impacts:=make([]topologyImpact,0)
 add:=func(code,subject string,configured,live []string){
  vlanSet:=map[int]bool{};vlanEvidence:=false;neighborSet:=map[string]bool{}
  for _,name:=range append(append([]string(nil),configured...),live...){
   p,ok:=portByName[strings.ToLower(name)];if !ok{continue}
   if len(p.VLANs)>0||p.NativeVLAN>0{vlanEvidence=true}
   for _,v:=range p.VLANs{if v>0{vlanSet[v]=true}}
   if p.NativeVLAN>0{vlanSet[p.NativeVLAN]=true}
   for _,n:=range neighborsByPort[strings.ToLower(name)]{neighborSet[n]=true}
  }
  vlans:=make([]int,0,len(vlanSet));for v:=range vlanSet{vlans=append(vlans,v)};sort.Ints(vlans)
  neighborList:=make([]string,0,len(neighborSet));for n:=range neighborSet{neighborList=append(neighborList,n)};sort.Strings(neighborList)
  impacts=append(impacts,topologyImpact{Code:code,Subject:subject,ConfiguredPorts:append([]string(nil),configured...),LivePorts:append([]string(nil),live...),VLANs:vlans,Neighbors:neighborList,VLANEvidenceAvailable:vlanEvidence,Note:"To potencjalny zakres wpływu wynikający z rozpoznanej konfiguracji i odczytów. Nie potwierdza przerwania ruchu ani pełnej ścieżki end-to-end."})
 }
 for _,risk:=range risks{
  switch risk.Code{
  case "lag-single-member","lag-member-mismatch","lag-not-ready":
   for _,lag:=range aggregations{
    if strings.HasPrefix(strings.ToLower(risk.Evidence),strings.ToLower(lag.Name)){
     add(risk.Code,lag.Name,lag.Members,lag.LiveMembers);break
    }
   }
  case "single-explicit-uplink":
   for _,p:=range ports{if p.Role=="Uplink / switch"{add(risk.Code,p.Port,[]string{p.Port},nil);break}}
  }
 }
 return impacts
}

func analyzeTopologyRisks(ports []topologyPort, links []topologyLink, aggregations []topologyAggregation, lldpAvailable bool) []topologyRisk {
 risks:=make([]topologyRisk,0)
 explicitUplinks:=make([]topologyPort,0)
 neighbors:=map[string]bool{}
 for _,link:=range links { neighbors[strings.ToLower(link.LocalPort)]=true }
 for _,port:=range ports {
  if port.Role=="Uplink / switch" { explicitUplinks=append(explicitUplinks,port) }
  if port.Protected { risks=append(risks,topologyRisk{"info","protected-port","Port objęty ochroną zmian",port.Port+" znajduje się na liście PROTECTED_PORTS.","Nie zmieniaj tego portu bez zapewnienia alternatywnego dostępu i świadomej procedury serwisowej."}) }
  if port.Role=="Uplink / switch" && lldpAvailable && !neighbors[strings.ToLower(port.Port)] {
   risks=append(risks,topologyRisk{"info","uplink-no-lldp","Brak potwierdzenia LLDP dla uplinku",port.Port+" jest opisany jako uplink/switch, ale nie ma rozpoznanego wpisu LLDP.","Zweryfikuj sąsiedztwo inną metodą. Brak LLDP nie oznacza braku fizycznego łącza."})
  }
 }
 if len(explicitUplinks)==1 {
  p:=explicitUplinks[0]
  risks=append(risks,topologyRisk{"warning","single-explicit-uplink","Jeden jawnie opisany uplink — kandydat na pojedynczy punkt awarii",p.Port+" jest jedynym portem rozpoznanym po opisie jako uplink/switch.","Sprawdź topologię po drugiej stronie i ścieżki zapasowe. Sam opis portu nie potwierdza całej redundancji sieci."})
 }
 for _,lag:=range aggregations {
  if len(lag.Members)==1 {
   risks=append(risks,topologyRisk{"warning","lag-single-member","Agregacja ma jednego skonfigurowanego członka",lag.Name+" zawiera tylko "+lag.Members[0]+".","Zweryfikuj, czy drugi tor powinien należeć do agregacji. Jeden członek nie zapewnia redundancji łącza."})
  }
  if lag.Live && lag.MemberMismatch {
   risks=append(risks,topologyRisk{"warning","lag-member-mismatch","Niespójność członków LACP",lag.Name+": konfiguracja ["+strings.Join(lag.Members,", ")+"] różni się od rozpoznanych aktywnych członków ["+strings.Join(lag.LiveMembers,", ")+ "].","Sprawdź stan LACP i konfigurację obu końców agregacji przed zmianami."})
  }
  if lag.Live && lag.Ready=="Niegotowy" {
   risks=append(risks,topologyRisk{"warning","lag-not-ready","Agregacja LACP nie jest gotowa",lag.Name+" zgłasza stan Ready=0.","Sprawdź zgodność trybu LACP, partnera, członków i fizyczne linki."})
  }
 }
 if len(explicitUplinks)==0 {
  risks=append(risks,topologyRisk{"info","uplink-role-unknown","Nie rozpoznano jawnie opisanych uplinków","Żaden port nie zawiera w opisie jednoznacznej etykiety uplink/switch.","Uzupełnij opisy portów lub zweryfikuj topologię ręcznie. Nie oznacza to, że switch nie ma uplinków."})
 }
 return risks
}
func parseTopologyConfig(config,switchport string)([]topologyPort,[]topologyAggregation,[]int){
 ports:=map[string]*topologyPort{};lags:=map[string]*topologyAggregation{};vlans:=map[int]bool{}
 var current *topologyPort
 for _,line:=range strings.Split(config,"\n"){
  line=strings.TrimSpace(line);lower:=strings.ToLower(line)
  if strings.HasPrefix(lower,"interface "){
   name:=strings.TrimSpace(line[len("interface "):]);if !regexp.MustCompile(`(?i)^port[0-9.]+$`).MatchString(name){current=nil;continue}
   current=&topologyPort{Port:name,VLANs:[]int{}};ports[strings.ToLower(name)]=current;continue
  }
  if strings.HasPrefix(lower,"vlan "){f:=strings.Fields(lower);if len(f)==2{if n,e:=strconv.Atoi(f[1]);e==nil&&n>=1&&n<=4094{vlans[n]=true}}}
  if current==nil{continue};f:=strings.Fields(lower)
  if len(f)>=3&&f[0]=="switchport"&&f[1]=="mode"{current.Mode=f[2]}
  if len(f)>=4&&f[0]=="switchport"&&f[1]=="access"&&f[2]=="vlan"{if n,e:=strconv.Atoi(f[3]);e==nil&&n>=1&&n<=4094{current.VLANs=append(current.VLANs,n)}}
  if len(f)>=5&&f[0]=="switchport"&&f[1]=="trunk"&&f[2]=="native"&&f[3]=="vlan"{current.NativeVLAN,_=strconv.Atoi(f[4])}
  if len(f)>=2&&f[0]=="spanning-tree"&&f[1]=="edgeport"{current.EdgePort=true}
  if len(f)>=2&&f[0]=="description"{current.Description=strings.TrimSpace(line[len(f[0]):])}
  if len(f)>=3&&(f[0]=="channel-group"||f[0]=="static-channel-group"){
   name:="Po"+f[1];current.LAG=name;key:=strings.ToLower(name);if lags[key]==nil{lags[key]=&topologyAggregation{Name:name,Members:[]string{}}};lags[key].Members=append(lags[key].Members,current.Port)
  }
 }
 // Prefer the switch's parsed switchport view for operationally visible VLAN membership.
 blocks:=regexp.MustCompile(`(?im)^\s*Interface name\s*:\s*(\S+)\s*$`).FindAllStringSubmatchIndex(switchport,-1)
 for i,loc:=range blocks{
  name:=strings.TrimSpace(switchport[loc[2]:loc[3]]);p:=ports[strings.ToLower(name)];if p==nil{p=&topologyPort{Port:name,VLANs:[]int{}};ports[strings.ToLower(name)]=p}
  end:=len(switchport);if i+1<len(blocks){end=blocks[i+1][0]};block:=switchport[loc[0]:end]
  read:=func(label string)string{re:=regexp.MustCompile(`(?im)^\s*`+regexp.QuoteMeta(label)+`\s*:\s*(.*?)\s*$`);m:=re.FindStringSubmatch(block);if len(m)>1{return strings.TrimSpace(m[1])};return ""}
  if mode:=read("Switchport mode");mode!=""{p.Mode=strings.ToLower(mode)}
  if n,e:=strconv.Atoi(read("Default Vlan"));e==nil&&n>0{p.NativeVLAN=n}
  if raw:=read("Configured Vlans");raw!=""{set:=parseAuditVLANSet(strings.ReplaceAll(raw," ",","));vals:=make([]int,0,len(set));for n:=range set{vals=append(vals,n)};sort.Ints(vals);if len(vals)>0{p.VLANs=vals}}
 }
 out:=make([]topologyPort,0,len(ports));for _,p:=range ports{
  desc:=strings.ToLower(p.Description)
  switch{case strings.Contains(desc,"uplink")||strings.Contains(desc,"switch"):p.Role="Uplink / switch";case strings.Contains(desc,"access point")||strings.Contains(desc," ap"):p.Role="Access Point";case strings.Contains(desc,"cctv")||strings.Contains(desc,"kamera"):p.Role="CCTV";case strings.Contains(desc,"voip")||strings.Contains(desc,"telefon"):p.Role="VoIP";case strings.Contains(desc,"server")||strings.Contains(desc,"storage"):p.Role="Server"}
  sort.Ints(p.VLANs);out=append(out,*p)
 }
 sort.Slice(out,func(i,j int)bool{return out[i].Port<out[j].Port})
 agg:=make([]topologyAggregation,0,len(lags));for _,l:=range lags{sort.Strings(l.Members);agg=append(agg,*l)}
 sort.Slice(agg,func(i,j int)bool{return agg[i].Name<agg[j].Name})
 vlanList:=make([]int,0,len(vlans));for n:=range vlans{vlanList=append(vlanList,n)};sort.Ints(vlanList)
 return out,agg,vlanList
}
func parseTopologyLLDP(raw string)[]topologyLink{
 links:=make([]topologyLink,0);seen:=map[string]bool{}
 portRE:=regexp.MustCompile(`(?i)^port[0-9.]+$`)
 for _,line:=range strings.Split(raw,"\n"){
  fields:=strings.Fields(strings.TrimSpace(line));if len(fields)<2||!portRE.MatchString(fields[0]){continue}
  joined:=strings.Join(fields[1:]," ");lower:=strings.ToLower(joined)
  if strings.Contains(lower,"local interface")||strings.Contains(lower,"neighbor")||strings.Contains(lower,"port id")||strings.Contains(lower,"system name"){continue}
  key:=strings.ToLower(fields[0]+"|"+joined);if seen[key]{continue};seen[key]=true
  links=append(links,topologyLink{fields[0],joined,"LLDP"})
 }
 return links
}
func parseTopologyLACP(raw string)[]topologyAggregation{
 groups:=make([]topologyAggregation,0);var current *topologyAggregation
 for _,line:=range strings.Split(raw,"\n"){
  if m:=regexp.MustCompile(`(?i)^\s*Aggregator\s+(po[0-9]+)\b`).FindStringSubmatch(line);len(m)>1{groups=append(groups,topologyAggregation{Name:m[1],Members:[]string{}});current=&groups[len(groups)-1];continue}
  if current==nil{continue}
  if m:=regexp.MustCompile(`(?i)^\s*Partner LAG:\s*(.+)$`).FindStringSubmatch(line);len(m)>1{current.Partner=strings.TrimSpace(m[1])}
  if m:=regexp.MustCompile(`(?i)^\s*Individual:\s*\d+\s*-\s*Ready:\s*(\d+)`).FindStringSubmatch(line);len(m)>1{if m[1]=="1"{current.Ready="Gotowy"}else{current.Ready="Niegotowy"}}
  if m:=regexp.MustCompile(`(?i)^\s*Link:\s*(port[0-9.]+)`).FindStringSubmatch(line);len(m)>1{current.Members=append(current.Members,m[1])}
 }
 return groups
}
func topologyChangeRisksFromSnapshot(config, switchport, lldpRaw string, configErr, lldpErr error, port string) []topologyRisk {
 risks:=make([]topologyRisk,0)
 if configErr!=nil { return []topologyRisk{{"info","topology-check-unavailable","Niepełna analiza wpływu zmiany","Nie udało się odczytać running-config; rola portu i członkostwo LAG nie zostały potwierdzone.","Przed zmianą sprawdź topologię ręcznie. Podgląd nie może potwierdzić, że port jest bezpieczny."}} }
 ports,lags,_:=parseTopologyConfig(config,switchport)
 links:=parseTopologyLLDP(lldpRaw)
 target:=topologyPort{};found:=false
 for _,item:=range ports { if strings.EqualFold(item.Port,port){target=item;found=true;break} }
 if !found { risks=append(risks,topologyRisk{"info","port-role-unknown","Nie udało się ustalić roli portu",port+" nie został jednoznacznie rozpoznany w running-config.","Zweryfikuj przeznaczenie portu i połączenia przed zatwierdzeniem zmiany."}) }
 if target.Role=="Uplink / switch" { risks=append(risks,topologyRisk{"warning","change-uplink-risk","Zmiana dotyczy portu uplink/switch",port+" ma jawny opis wskazujący na uplink lub połączenie switch-switch.","Zmiana VLAN lub trybu może odciąć dalszą część sieci. Zapewnij alternatywny dostęp i sprawdź konfigurację urządzenia po drugiej stronie."}) }
 for _,link:=range links { if strings.EqualFold(link.LocalPort,port) { risks=append(risks,topologyRisk{"warning","change-lldp-neighbor","Port ma rozpoznanego sąsiada LLDP",port+" prowadzi do: "+link.Neighbor+".","Przed zmianą oceń wpływ na urządzenie po drugiej stronie i dostęp do zarządzania."});break } }
 if target.LAG!="" { members:=[]string{};for _,lag:=range lags{if strings.EqualFold(lag.Name,target.LAG){members=lag.Members;break}};risks=append(risks,topologyRisk{"warning","change-lacp-member","Zmiana dotyczy członka agregacji LACP",port+" należy do "+target.LAG+"; członkowie skonfigurowani: "+strings.Join(members,", ")+".","Zmiana VLAN lub trybu może naruszyć spójność agregacji albo przerwać ruch. Sprawdź konfigurację wszystkich członków i partnera."}) }
 if isProtectedPort(port) { risks=append(risks,topologyRisk{"critical","change-protected-port","Port jest objęty ochroną",port+" znajduje się na liście PROTECTED_PORTS.","Operacja powinna zostać zablokowana; użyj alternatywnego portu zarządzającego."}) }
 if lldpErr!=nil || strings.TrimSpace(lldpRaw)=="" { risks=append(risks,topologyRisk{"info","change-lldp-unavailable","Sąsiedztwo LLDP niedostępne","Nie udało się uzyskać wiarygodnego odczytu LLDP.","Nie traktuj braku LLDP jako dowodu, że port nie prowadzi do innego urządzenia."}) }
 return risks
}
func requireTopologyRiskAcknowledgement(read func(string)(string,error), ports []string, acknowledged bool) error {
 config,configErr:=read("show running-config")
 sw,_:=read("show interface switchport")
 lldp,lldpErr:=read("show lldp neighbors")
 if configErr!=nil { if !acknowledged{return fmt.Errorf("nie można potwierdzić topologii; ponów podgląd i jawnie potwierdź ryzyko")};return nil }
 for _,port:=range ports {
  risks:=topologyChangeRisksFromSnapshot(config,sw,lldp,configErr,lldpErr,port)
  if len(risks)>0&&!acknowledged{return fmt.Errorf("port %s ma ostrzeżenia topologii; wymagany jest podgląd i jawne potwierdzenie ryzyka",port)}
 }
 return nil
}

func topologyChangeRisks(read func(string)(string,error), port string) []topologyRisk {
 config,configErr:=read("show running-config")
 sw,_:=read("show interface switchport")
 lldp,lldpErr:=read("show lldp neighbors")
 return topologyChangeRisksFromSnapshot(config,sw,lldp,configErr,lldpErr,port)
}

func topologyMapHandler(read func(string)(string,error))http.Handler{
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodGet{http.Error(w,"method not allowed",405);return}
  config,err:=read("show running-config");if err!=nil{http.Error(w,"brak running-config; mapa topologii nie została wygenerowana",http.StatusBadGateway);return}
  sw,swErr:=read("show interface switchport");ports,lags,vlans:=parseTopologyConfig(config,sw)
  lldp,lldpErr:=read("show lldp neighbors");links:=parseTopologyLLDP(lldp)
  lacpRaw,lacpErr:=read("show etherchannel detail");liveLags:=parseTopologyLACP(lacpRaw)
  for i:=range lags {
   for _,live:=range liveLags {
    if !strings.EqualFold(lags[i].Name,live.Name) { continue }
    lags[i].Live=true;lags[i].Partner=live.Partner;lags[i].Ready=live.Ready
    lags[i].LiveMembers=append([]string(nil),live.Members...)
    configured:=map[string]bool{};for _,member:=range lags[i].Members{configured[strings.ToLower(member)]=true}
    active:=map[string]bool{};for _,member:=range live.Members{active[strings.ToLower(member)]=true}
    if len(active)>0 {
     if len(configured)!=len(active){lags[i].MemberMismatch=true}else{for member:=range configured{if !active[member]{lags[i].MemberMismatch=true;break}}}
    }
   }
  }
  for i:=range ports { ports[i].Protected=isProtectedPort(ports[i].Port) }
  lldpLower:=strings.ToLower(lldp)
  lldpSupported:=lldpErr==nil&&strings.TrimSpace(lldp)!=""&&!strings.Contains(lldpLower,"invalid command")&&!strings.Contains(lldpLower,"invalid input")&&!strings.Contains(lldpLower,"unknown command")&&!strings.Contains(lldpLower,"unrecognized")&&!strings.Contains(lldpLower,"not supported")
  checks:=map[string]bool{"runningConfig":true,"switchport":swErr==nil&&strings.Contains(sw,"Interface name"),"lldp":lldpSupported&&len(links)>0,"lacp":lacpErr==nil&&len(liveLags)>0}
  risks:=analyzeTopologyRisks(ports,links,lags,lldpSupported)
  configuredGroups:=map[string]bool{};for _,lag:=range lags{configuredGroups[strings.ToLower(lag.Name)]=true}
  for _,live:=range liveLags{if !configuredGroups[strings.ToLower(live.Name)]{risks=append(risks,topologyRisk{"info","lacp-live-unconfigured","Agregacja LACP bez rozpoznanej deklaracji konfiguracji",live.Name+" jest widoczna w odczycie operacyjnym, ale nie została rozpoznana w running-config.","Zweryfikuj format konfiguracji i stan agregacji. Parser może nie rozpoznawać wszystkich wariantów składni firmware."})}}
  w.Header().Set("Content-Type","application/json; charset=utf-8");w.Header().Set("Cache-Control","no-store")
  impacts:=buildTopologyImpacts(ports,links,lags,risks)
  _=json.NewEncoder(w).Encode(map[string]any{"available":true,"readOnly":true,"ports":ports,"links":links,"aggregations":lags,"vlans":vlans,"risks":risks,"impacts":impacts,"checks":checks,"note":"Ryzyka są wskazówkami opartymi wyłącznie na rozpoznanych opisach i danych CLI. Potencjalny wpływ na porty i VLAN-y jest wyliczany tylko z rozpoznanych członków LACP, konfiguracji switchport i LLDP; nie potwierdza przerwania ruchu ani pełnej ścieżki end-to-end. Brak LLDP nie oznacza braku fizycznego łącza."})
 })
}


// diagnosticsSample stores only normalized topology metadata; never CLI output or credentials.
type diagnosticsSample struct {
 CapturedAt time.Time `json:"capturedAt"`
 Checks map[string]bool `json:"checks"`
 Aggregations []topologyAggregation `json:"aggregations"`
 Uplinks []string `json:"uplinks"`
 Neighbors []topologyLink `json:"neighbors"`
}
type diagnosticsStore struct { mu sync.Mutex; path string; retention int }
var diagnosticsRegistryMu sync.Mutex
var diagnosticsRegistry = map[string]*diagnosticsStore{}
func sharedDiagnosticsStore(path string, retention int)*diagnosticsStore{
 diagnosticsRegistryMu.Lock();defer diagnosticsRegistryMu.Unlock()
 if store:=diagnosticsRegistry[path];store!=nil{return store}
 store:=newDiagnosticsStore(path,retention);diagnosticsRegistry[path]=store;return store
}
func StartDiagnosticsCollector(ctx context.Context, read func(string)(string,error), path string, interval time.Duration){
 if interval<=0{return}
 store:=sharedDiagnosticsStore(path,12000)
 go func(){
  collect:=func(){
   sample:=collectDiagnosticsSample(read)
   if err:=store.append(sample);err!=nil{log.Printf("diagnostics history write failed: %v",err)}
  }
  collect()
  ticker:=time.NewTicker(interval);defer ticker.Stop()
  for{select{case <-ctx.Done():return;case <-ticker.C:collect()}}
 }()
}
func newDiagnosticsStore(path string, retention int)*diagnosticsStore{if retention<2||retention>50000{retention=12000};return &diagnosticsStore{path:path,retention:retention}}
func (s *diagnosticsStore) readLocked()([]diagnosticsSample,error){
 raw,err:=os.ReadFile(s.path);if os.IsNotExist(err){return []diagnosticsSample{},nil};if err!=nil{return nil,err}
 var items []diagnosticsSample;if len(raw)==0{return []diagnosticsSample{},nil}
 if err=json.Unmarshal(raw,&items);err!=nil{return nil,fmt.Errorf("history file is invalid: %w",err)}
 return items,nil
}
func (s *diagnosticsStore) list()([]diagnosticsSample,error){s.mu.Lock();defer s.mu.Unlock();return s.readLocked()}
func (s *diagnosticsStore) append(sample diagnosticsSample)error{
 s.mu.Lock();defer s.mu.Unlock()
 items,err:=s.readLocked();if err!=nil{return err}
 items=append(items,sample);if len(items)>s.retention{items=append([]diagnosticsSample(nil),items[len(items)-s.retention:]...)}
 raw,err:=json.MarshalIndent(items,"","  ");if err!=nil{return err}
 dir:=filepath.Dir(s.path);if err=os.MkdirAll(dir,0700);err!=nil{return err};if err=os.Chmod(dir,0700);err!=nil{return err}
 tmp,err:=os.CreateTemp(dir,".diagnostics-*.tmp");if err!=nil{return err}
 tmpName:=tmp.Name();ok:=false
 defer func(){_ = tmp.Close();if !ok{_ = os.Remove(tmpName)}}()
 if err=tmp.Chmod(0600);err!=nil{return err}
 if _,err=tmp.Write(raw);err!=nil{return err}
 if err=tmp.Sync();err!=nil{return err}
 if err=tmp.Close();err!=nil{return err}
 if err=os.Rename(tmpName,s.path);err!=nil{return err}
 if err=os.Chmod(s.path,0600);err!=nil{return err}
 ok=true
 return nil
}
func collectDiagnosticsSample(read func(string)(string,error)) diagnosticsSample {
 sample:=diagnosticsSample{CapturedAt:time.Now().UTC(),Checks:map[string]bool{},Aggregations:[]topologyAggregation{},Uplinks:[]string{},Neighbors:[]topologyLink{}}
 config,configErr:=read("show running-config");sample.Checks["runningConfig"]=configErr==nil
 sw,swErr:=read("show interface switchport");sample.Checks["switchport"]=swErr==nil&&strings.Contains(sw,"Interface name")
 lldp,lldpErr:=read("show lldp neighbors");sample.Checks["lldp"]=lldpErr==nil&&len(parseTopologyLLDP(lldp))>0
 lacp,lacpErr:=read("show etherchannel detail");liveParsed:=[]topologyAggregation{};if lacpErr==nil{liveParsed=parseTopologyLACP(lacp)};sample.Checks["lacp"]=lacpErr==nil&&len(liveParsed)>0
 ports,groups,_:=parseTopologyConfig(config,sw)
 for _,p:=range ports{if p.Role=="Uplink / switch"{sample.Uplinks=append(sample.Uplinks,p.Port)}}
 sort.Strings(sample.Uplinks)
 if sample.Checks["lldp"]{sample.Neighbors=parseTopologyLLDP(lldp)}
 if sample.Checks["runningConfig"]{
  liveGroups:=liveParsed
  for i:=range groups{
   for _,live:=range liveGroups{if !strings.EqualFold(groups[i].Name,live.Name){continue}
    groups[i].Live=true;groups[i].Ready=live.Ready;groups[i].Partner=live.Partner;groups[i].LiveMembers=append([]string(nil),live.Members...)
    configured:=map[string]bool{};for _,p:=range groups[i].Members{configured[strings.ToLower(p)]=true}
    active:=map[string]bool{};for _,p:=range live.Members{active[strings.ToLower(p)]=true}
    if len(active)>0{if len(active)!=len(configured){groups[i].MemberMismatch=true}else{for p:=range configured{if !active[p]{groups[i].MemberMismatch=true;break}}}}
   }}
  sample.Aggregations=groups
 }
 if sample.Checks["lacp"]{
  known:=map[string]bool{};for _,g:=range sample.Aggregations{known[strings.ToLower(g.Name)]=true}
  for _,live:=range liveParsed{if !known[strings.ToLower(live.Name)]{live.Live=true;sample.Aggregations=append(sample.Aggregations,live)}}
 }
 return sample
}
type diagnosticsAlert struct { Severity string `json:"severity"`; Code string `json:"code"`; Title string `json:"title"`; Evidence string `json:"evidence"`; At time.Time `json:"at"` }
type diagnosticsTransition struct { Type string `json:"type"`; Subject string `json:"subject"`; From string `json:"from"`; To string `json:"to"`; At time.Time `json:"at"`; Severity string `json:"severity"` }
func diagnosticsCurrentAlerts(s diagnosticsSample)[]diagnosticsAlert{
 out:=[]diagnosticsAlert{}
 if s.Checks["lacp"]{
  for _,g:=range s.Aggregations{
   if g.Ready=="Niegotowy"{out=append(out,diagnosticsAlert{"critical","lacp-not-ready","Agregacja LACP nie jest gotowa",g.Name+" zgłasza Ready=0.",s.CapturedAt})}
   if g.MemberMismatch{out=append(out,diagnosticsAlert{"warning","lacp-member-mismatch","Niespójność członków LACP",g.Name+": skonfigurowane ["+strings.Join(g.Members,", ")+"] · aktywne ["+strings.Join(g.LiveMembers,", ")+"].",s.CapturedAt})}
   if !g.Live&&len(g.Members)>0{out=append(out,diagnosticsAlert{"warning","lacp-no-operational-data","Brak operacyjnego odczytu agregacji",g.Name+" jest skonfigurowana, ale nie ma rozpoznanego stanu operacyjnego.",s.CapturedAt})}
  }
 }
 if s.Checks["runningConfig"]&&len(s.Uplinks)==1{out=append(out,diagnosticsAlert{"info","single-explicit-uplink","Jeden jawnie opisany uplink",s.Uplinks[0]+" jest jedynym portem rozpoznanym po opisie jako uplink/switch. To ryzyko topologii, nie alarm awarii.",s.CapturedAt})}
 return out
}
func diagnosticsTransitions(samples []diagnosticsSample)[]diagnosticsTransition{
 out:=[]diagnosticsTransition{}
 for i:=1;i<len(samples);i++{
  prev,cur:=samples[i-1],samples[i]
  if prev.Checks["lacp"]&&cur.Checks["lacp"]{
   old:=map[string]topologyAggregation{};for _,g:=range prev.Aggregations{old[strings.ToLower(g.Name)]=g}
   for _,g:=range cur.Aggregations{p,ok:=old[strings.ToLower(g.Name)];if !ok{continue}
    if p.Ready!=g.Ready&&p.Ready!=""&&g.Ready!=""{sev:="warning";if g.Ready=="Niegotowy"{sev="critical"};out=append(out,diagnosticsTransition{"lacp-ready",g.Name,p.Ready,g.Ready,cur.CapturedAt,sev})}
    if p.Live&&g.Live{
     before:=map[string]bool{};after:=map[string]bool{};for _,v:=range p.LiveMembers{before[strings.ToLower(v)]=true};for _,v:=range g.LiveMembers{after[strings.ToLower(v)]=true}
     for v:=range before{if !after[v]{out=append(out,diagnosticsTransition{"lacp-member-lost",g.Name+" / "+v,"aktywny","brak w odczycie",cur.CapturedAt,"critical"})}}
     for v:=range after{if !before[v]{out=append(out,diagnosticsTransition{"lacp-member-restored",g.Name+" / "+v,"brak w odczycie","aktywny",cur.CapturedAt,"info"})}}
    }
   }
  }
  if prev.Checks["lldp"]&&cur.Checks["lldp"]{
   old:=map[string]string{};now:=map[string]string{}
   for _,n:=range prev.Neighbors{old[strings.ToLower(n.LocalPort)]=n.Neighbor};for _,n:=range cur.Neighbors{now[strings.ToLower(n.LocalPort)]=n.Neighbor}
   for port,neighbor:=range old{
    next,ok:=now[port]
    if !ok{out=append(out,diagnosticsTransition{"lldp-neighbor-removed",port,neighbor,"brak sąsiada w odczycie",cur.CapturedAt,"warning"})}else if next!=neighbor{out=append(out,diagnosticsTransition{"lldp-neighbor-change",port,neighbor,next,cur.CapturedAt,"info"})}
   }
   for port,neighbor:=range now{if _,ok:=old[port];!ok{out=append(out,diagnosticsTransition{"lldp-neighbor-added",port,"brak sąsiada w odczycie",neighbor,cur.CapturedAt,"info"})}}
  }
  // Wynik kontroli CLI opisuje dostępność odczytu zarządzającego, nie status ruchu.
  for _,check:=range []string{"runningConfig","switchport","lldp","lacp"}{
   if prev.Checks[check]&&!cur.Checks[check]{out=append(out,diagnosticsTransition{"control-read-failed",check,"odczyt poprawny","odczyt nieudany",cur.CapturedAt,"warning"})}
   if !prev.Checks[check]&&cur.Checks[check]{out=append(out,diagnosticsTransition{"control-read-restored",check,"odczyt nieudany","odczyt poprawny",cur.CapturedAt,"info"})}
  }
 }
 return out
}

func diagnosticsCollectHandler(read func(string)(string,error),store *diagnosticsStore)http.Handler{
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  sample:=collectDiagnosticsSample(read)
  if err:=store.append(sample);err!=nil{http.Error(w,"nie udało się zapisać historii diagnostycznej",http.StatusInternalServerError);return}
  if !sample.Checks["runningConfig"]&&!sample.Checks["lacp"]&&!sample.Checks["lldp"]{http.Error(w,"próbka zapisana, ale wszystkie kontrole zakończyły się niepowodzeniem",http.StatusBadGateway);return}
  w.Header().Set("Content-Type","application/json; charset=utf-8");w.Header().Set("Cache-Control","no-store")
  _=json.NewEncoder(w).Encode(map[string]any{"sample":sample,"alerts":diagnosticsCurrentAlerts(sample),"saved":true,"retention":store.retention})
 })
}
func diagnosticsSamplingSettings()(string,string){
 raw:=strings.TrimSpace(os.Getenv("DIAGNOSTICS_INTERVAL"))
 if raw=="0"{return "manual","wyłączone"}
 if raw==""{return "scheduled","60s"}
 interval,err:=time.ParseDuration(raw)
 if err!=nil||interval<10*time.Second{return "scheduled","60s"}
 return "scheduled",interval.String()
}
type diagnosticsAcknowledgement struct {
 ID string `json:"id"`
 Code string `json:"code"`
 Evidence string `json:"evidence"`
 At time.Time `json:"at"`
 AcknowledgedAt time.Time `json:"acknowledgedAt"`
 Username string `json:"username"`
 Comment string `json:"comment"`
}
type diagnosticsAckStore struct { mu sync.Mutex; path string }
var diagnosticsAckRegistryMu sync.Mutex
var diagnosticsAckRegistry = map[string]*diagnosticsAckStore{}
func sharedDiagnosticsAckStore(path string)*diagnosticsAckStore{
 diagnosticsAckRegistryMu.Lock();defer diagnosticsAckRegistryMu.Unlock()
 if store:=diagnosticsAckRegistry[path];store!=nil{return store}
 store:=&diagnosticsAckStore{path:path};diagnosticsAckRegistry[path]=store;return store
}
func (a *diagnosticsAckStore) list()([]diagnosticsAcknowledgement,error){
 a.mu.Lock();defer a.mu.Unlock()
 raw,err:=os.ReadFile(a.path);if os.IsNotExist(err){return []diagnosticsAcknowledgement{},nil};if err!=nil{return nil,err}
 var rows []diagnosticsAcknowledgement;if len(raw)==0{return rows,nil};if err=json.Unmarshal(raw,&rows);err!=nil{return nil,err};return rows,nil
}
func (a *diagnosticsAckStore) save(row diagnosticsAcknowledgement)error{
 a.mu.Lock();defer a.mu.Unlock()
 rows:=[]diagnosticsAcknowledgement{}
 raw,err:=os.ReadFile(a.path);if err==nil&&len(raw)>0{if err=json.Unmarshal(raw,&rows);err!=nil{return err}}else if err!=nil&&!os.IsNotExist(err){return err}
 for i:=range rows{if rows[i].ID==row.ID{rows[i]=row;return a.write(rows)}}
 rows=append(rows,row);if len(rows)>1000{rows=rows[len(rows)-1000:]}
 return a.write(rows)
}
func (a *diagnosticsAckStore) write(rows []diagnosticsAcknowledgement)error{
 raw,err:=json.MarshalIndent(rows,"","  ");if err!=nil{return err}
 dir:=filepath.Dir(a.path);if err=os.MkdirAll(dir,0700);err!=nil{return err};if err=os.Chmod(dir,0700);err!=nil{return err}
 tmp,err:=os.CreateTemp(dir,".diagnostics-acks-*.tmp");if err!=nil{return err};name:=tmp.Name();ok:=false
 defer func(){_ = tmp.Close();if !ok{_ = os.Remove(name)}}()
 if err=tmp.Chmod(0600);err!=nil{return err};if _,err=tmp.Write(raw);err!=nil{return err};if err=tmp.Sync();err!=nil{return err};if err=tmp.Close();err!=nil{return err};if err=os.Rename(name,a.path);err!=nil{return err};ok=true;return os.Chmod(a.path,0600)
}
func diagnosticsAlertID(code,evidence string,at time.Time)string{
 sum:=sha256.Sum256([]byte(code+"\x00"+evidence+"\x00"+at.UTC().Format(time.RFC3339Nano)))
 return hex.EncodeToString(sum[:])
}
func diagnosticsAcknowledgeHandler(store *diagnosticsStore,path string)http.Handler{
 acks:=sharedDiagnosticsAckStore(path)
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
  r.Body=http.MaxBytesReader(w,r.Body,2048)
  var q struct{ID string `json:"id"`;Comment string `json:"comment"`}
  if json.NewDecoder(r.Body).Decode(&q)!=nil||len(q.ID)!=64||len(q.Comment)>500{http.Error(w,"nieprawidłowe dane potwierdzenia alarmu",400);return}
  samples,err:=store.list();if err!=nil{http.Error(w,"nie można odczytać historii diagnostycznej",500);return}
  if len(samples)==0{http.Error(w,"brak aktywnych alarmów",409);return}
  found:=false;var row diagnosticsAcknowledgement
  for _,alert:=range diagnosticsCurrentAlerts(samples[len(samples)-1]){if diagnosticsAlertID(alert.Code,alert.Evidence,alert.At)==q.ID{found=true;row=diagnosticsAcknowledgement{ID:q.ID,Code:alert.Code,Evidence:alert.Evidence,At:alert.At};break}}
  if !found{http.Error(w,"alarm nie jest już aktywny lub jego stan się zmienił; odśwież listę",409);return}
  username:="unknown";if identity,ok:=r.Context().Value(identityContextKey{}).(panelIdentity);ok&&strings.TrimSpace(identity.Username)!=""{username=identity.Username}else if authenticated,_,ok:=r.BasicAuth();ok&&strings.TrimSpace(authenticated)!=""{username=authenticated}
  row.AcknowledgedAt=time.Now().UTC();row.Username=username;row.Comment=strings.TrimSpace(q.Comment)
  if err=acks.save(row);err!=nil{http.Error(w,"nie udało się zapisać potwierdzenia alarmu",500);return}
  w.Header().Set("Content-Type","application/json");w.Header().Set("Cache-Control","no-store");_=json.NewEncoder(w).Encode(map[string]any{"saved":true,"acknowledgement":row})
 })
}
type diagnosticsEvent struct {
 ID string `json:"id"`
 Type string `json:"type"`
 Severity string `json:"severity"`
 Title string `json:"title"`
 Subject string `json:"subject"`
 Description string `json:"description"`
 At time.Time `json:"at"`
 Acknowledged bool `json:"acknowledged"`
 Acknowledgement *diagnosticsAcknowledgement `json:"acknowledgement,omitempty"`
}
func diagnosticsEventID(kind,subject string,at time.Time)string{
 sum:=sha256.Sum256([]byte(kind+"\x00"+subject+"\x00"+at.UTC().Format(time.RFC3339Nano)))
 return hex.EncodeToString(sum[:])
}
type diagnosticsCorrelation struct {
 ID string `json:"id"`
 Severity string `json:"severity"`
 Title string `json:"title"`
 Summary string `json:"summary"`
 Start time.Time `json:"start"`
 End time.Time `json:"end"`
 EventCount int `json:"eventCount"`
 Categories []string `json:"categories"`
 EventIDs []string `json:"eventIds"`
}
func buildDiagnosticsCorrelations(events []diagnosticsEvent,window time.Duration)[]diagnosticsCorrelation{
 if len(events)<2{return []diagnosticsCorrelation{}}
 ordered:=append([]diagnosticsEvent(nil),events...)
 sort.Slice(ordered,func(i,j int)bool{if ordered[i].At.Equal(ordered[j].At){return ordered[i].ID<ordered[j].ID};return ordered[i].At.Before(ordered[j].At)})
 out:=make([]diagnosticsCorrelation,0)
 for i:=0;i<len(ordered);{
  start:=ordered[i].At
  j:=i+1
  for j<len(ordered)&&ordered[j].At.Sub(start)<=window{j++}
  group:=ordered[i:j]
  categories:=map[string]bool{};ids:=make([]string,0,len(group));severity:="info";hasSignal:=false
  for _,event:=range group{
   categories[event.Type]=true;ids=append(ids,event.ID)
   if event.Severity=="critical"{severity="critical";hasSignal=true}else if event.Severity=="warning"{if severity!="critical"{severity="warning"};hasSignal=true}
  }
  if len(categories)>=2&&hasSignal{
   categoryList:=make([]string,0,len(categories));for category:=range categories{categoryList=append(categoryList,category)};sort.Strings(categoryList)
   end:=group[len(group)-1].At
   id:=diagnosticsEventID("correlation",strings.Join(ids,","),start)
   title:="Zdarzenia z różnych warstw wystąpiły w zbliżonym czasie"
   if categories["lacp"]&&categories["lldp"]{title="Zmiany LACP i sąsiedztwa LLDP"}
   if categories["availability"]&&(categories["lacp"]||categories["lldp"]){title="Problem odczytu CLI zbiegł się ze zmianą topologii"}
   summary:="Wykryto "+strconv.Itoa(len(group))+" zdarzenia z kategorii: "+strings.Join(categoryList,", ")+". To korelacja czasowa, nie dowód wspólnej przyczyny. Sprawdź kolejność zdarzeń, porty i urządzenie po drugiej stronie łącza."
   out=append(out,diagnosticsCorrelation{ID:id,Severity:severity,Title:title,Summary:summary,Start:start,End:end,EventCount:len(group),Categories:categoryList,EventIDs:ids})
  }
  i=j
 }
 return out
}

func diagnosticsEventsHandler(store *diagnosticsStore)http.Handler{
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodGet{http.Error(w,"method not allowed",405);return}
  window:=strings.TrimSpace(r.URL.Query().Get("window"));if window==""{window="24h"}
  durations:=map[string]time.Duration{"24h":24*time.Hour,"7d":7*24*time.Hour,"30d":30*24*time.Hour}
  span,ok:=durations[window];if !ok{http.Error(w,"window musi mieć wartość 24h, 7d lub 30d",400);return}
  kind:=strings.TrimSpace(r.URL.Query().Get("type"));if kind==""{kind="all"}
  allowed:=map[string]bool{"all":true,"alarm":true,"lacp":true,"lldp":true,"availability":true}
  if !allowed[kind]{http.Error(w,"nieobsługiwana kategoria zdarzeń",400);return}
  limit:=100;if raw:=r.URL.Query().Get("limit");raw!=""{n,err:=strconv.Atoi(raw);if err!=nil||n<1||n>500{http.Error(w,"limit musi mieścić się w zakresie 1–500",400);return};limit=n}
  samples,err:=store.list();if err!=nil{http.Error(w,"nie udało się odczytać historii diagnostycznej",500);return}
  cutoff:=time.Now().UTC().Add(-span)
  ackRows,err:=sharedDiagnosticsAckStore(store.path+".acks.json").list();if err!=nil{http.Error(w,"nie udało się odczytać potwierdzeń alarmów",500);return}
  ackByID:=map[string]diagnosticsAcknowledgement{};for i:=range ackRows{ackByID[ackRows[i].ID]=ackRows[i]}
  events:=make([]diagnosticsEvent,0)
  add:=func(eventType,severity,title,subject,description string,at time.Time){
   if at.IsZero()||at.Before(cutoff)||(kind!="all"&&kind!=eventType){return}
   id:=diagnosticsEventID(eventType,subject,at)
   events=append(events,diagnosticsEvent{ID:id,Type:eventType,Severity:severity,Title:title,Subject:subject,Description:description,At:at})
  }
  for _,tr:=range diagnosticsTransitions(samples){
   switch tr.Type{
   case "lacp-ready":add("lacp",tr.Severity,"Zmiana gotowości LACP",tr.Subject,tr.From+" → "+tr.To,tr.At)
   case "lacp-member-lost":add("lacp","critical","Utrata aktywnego członka LACP",tr.Subject,tr.From+" → "+tr.To,tr.At)
   case "lacp-member-restored":add("lacp","info","Powrót członka LACP",tr.Subject,tr.From+" → "+tr.To,tr.At)
   case "lldp-neighbor-added":add("lldp","info","Wykryto sąsiada LLDP",tr.Subject,tr.To,tr.At)
   case "lldp-neighbor-removed":add("lldp","warning","Zniknął sąsiad LLDP",tr.Subject,tr.From,tr.At)
   case "lldp-neighbor-change":add("lldp","info","Zmiana sąsiada LLDP",tr.Subject,tr.From+" → "+tr.To,tr.At)
   case "control-read-failed":add("availability","warning","Nieudany odczyt zarządzający",tr.Subject,"Kontrola CLI nie powiodła się. To brak potwierdzenia odczytu, a nie dowód, że switch lub ruch użytkowników jest offline.",tr.At)
   case "control-read-restored":add("availability","info","Odczyt zarządzający przywrócony",tr.Subject,"Kontrola CLI ponownie zakończyła się poprawnie.",tr.At)
   }
  }
  if len(samples)>0&&(kind=="all"||kind=="alarm"){
   for _,alert:=range diagnosticsCurrentAlerts(samples[len(samples)-1]){
    id:=diagnosticsAlertID(alert.Code,alert.Evidence,alert.At)
    if alert.At.Before(cutoff){continue}
    event:=diagnosticsEvent{ID:id,Type:"alarm",Severity:alert.Severity,Title:alert.Title,Subject:alert.Code,Description:alert.Evidence,At:alert.At}
    if ack,exists:=ackByID[id];exists{event.Acknowledged=true;event.Acknowledgement=&ack}
    events=append(events,event)
   }
  }
  sort.Slice(events,func(i,j int)bool{if events[i].At.Equal(events[j].At){return events[i].ID<events[j].ID};return events[i].At.After(events[j].At)})
  total:=len(events);if len(events)>limit{events=events[:limit]}
  w.Header().Set("Content-Type","application/json; charset=utf-8");w.Header().Set("Cache-Control","no-store")
  correlations:=[]diagnosticsCorrelation{}
  if kind=="all"{correlations=buildDiagnosticsCorrelations(events,5*time.Minute)}
  _=json.NewEncoder(w).Encode(map[string]any{"events":events,"total":total,"window":window,"type":kind,"limit":limit,"correlations":correlations,"correlationWindow":"5m","note":"Brak odczytu oznacza nieudaną kontrolę zarządzającą, a nie potwierdzoną awarię switcha lub ruchu. Korelacja czasowa nie dowodzi wspólnej przyczyny."})
 })
}

func diagnosticsHistoryHandler(store *diagnosticsStore)http.Handler{
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodGet{http.Error(w,"method not allowed",405);return}
  limit:=100;if raw:=r.URL.Query().Get("limit");raw!=""{n,err:=strconv.Atoi(raw);if err!=nil||n<1||n>500{http.Error(w,"limit musi mieścić się w zakresie 1–500",400);return};limit=n}
  windowMinutes:=60;if raw:=r.URL.Query().Get("windowMinutes");raw!=""{n,err:=strconv.Atoi(raw);if err!=nil||n<5||n>1440{http.Error(w,"windowMinutes musi mieścić się w zakresie 5–1440",400);return};windowMinutes=n}
  all,err:=store.list();if err!=nil{http.Error(w,"nie udało się odczytać historii diagnostycznej",http.StatusInternalServerError);return}
  transitions:=diagnosticsTransitions(all)
  cutoff:=time.Now().UTC().Add(-time.Duration(windowMinutes)*time.Minute)
  windowTransitions:=make([]diagnosticsTransition,0)
  counts:=map[string]int{}
  for _,tr:=range transitions{if !tr.At.Before(cutoff){windowTransitions=append(windowTransitions,tr);if tr.Type=="lacp-ready"||tr.Type=="lacp-member-lost"||tr.Type=="lacp-member-restored"{counts[tr.Type+"|"+tr.Subject]++}}}
  alerts:=[]diagnosticsAlert{};if len(all)>0{alerts=diagnosticsCurrentAlerts(all[len(all)-1])}
  for key,count:=range counts{if count>=3{parts:=strings.SplitN(key,"|",2);alerts=append(alerts,diagnosticsAlert{"warning","lacp-flapping","Powtarzające się zmiany stanu LACP",parts[1]+": "+strconv.Itoa(count)+" zmian w ostatnich "+strconv.Itoa(windowMinutes)+" minutach. Sprawdź okablowanie, porty i partnera LACP.",all[len(all)-1].CapturedAt})}}
  ackStore:=sharedDiagnosticsAckStore(store.path+".acks.json");ackRows,_:=ackStore.list();ackByID:=map[string]diagnosticsAcknowledgement{};for _,ack:=range ackRows{ackByID[ack.ID]=ack}
  alertRows:=make([]map[string]any,0,len(alerts));for _,alert:=range alerts{id:=diagnosticsAlertID(alert.Code,alert.Evidence,alert.At);row:=map[string]any{"severity":alert.Severity,"code":alert.Code,"title":alert.Title,"evidence":alert.Evidence,"at":alert.At,"id":id,"acknowledged":false};if ack,ok:=ackByID[id];ok{row["acknowledged"]=true;row["acknowledgement"]=ack};alertRows=append(alertRows,row)}
  items:=all;if len(items)>limit{items=items[len(items)-limit:]}
  w.Header().Set("Content-Type","application/json; charset=utf-8");w.Header().Set("Cache-Control","no-store")
  _=json.NewEncoder(w).Encode(map[string]any{"samples":items,"alerts":alertRows,"transitions":windowTransitions,"sampleCount":len(all),"hasEnoughHistory":len(all)>=2,"windowMinutes":windowMinutes,"flappingThreshold":3,"collectionMode":func()string{mode,_:=diagnosticsSamplingSettings();return mode}(),"collectionInterval":func()string{_,cadence:=diagnosticsSamplingSettings();return cadence}(),"note":func()string{if len(all)<2{return "Zebrano mniej niż dwie próbki. Nie można jeszcze ocenić zmian ani flappingu."};return "Zmiany są porównywane między kolejnymi próbkami z dostępnym odczytem. Brak danych nie jest traktowany jako powrót do normy. Flapping wymaga co najmniej trzech zmian w wybranym oknie."}()})
 })
}


type availabilityOutage struct {
 Start time.Time `json:"start"`
 End time.Time `json:"end,omitempty"`
 DurationSeconds int64 `json:"durationSeconds"`
 Open bool `json:"open"`
}
type availabilityMetric struct {
 Name string `json:"name"`
 AvailabilityPercent *float64 `json:"availabilityPercent"`
 ObservedSeconds int64 `json:"observedSeconds"`
 UnknownSeconds int64 `json:"unknownSeconds"`
 OutageCount int `json:"outageCount"`
 OutageSeconds int64 `json:"outageSeconds"`
 Outages []availabilityOutage `json:"outages"`
}
func diagnosticsAvailabilityMetric(name string, samples []diagnosticsSample, start,end time.Time, maxGap time.Duration, state func(diagnosticsSample)(bool,bool)) availabilityMetric {
 m:=availabilityMetric{Name:name,Outages:[]availabilityOutage{}}
 observed:=int64(0);up:=int64(0);outageOpen:=-1
 for i:=1;i<len(samples);i++ {
  prev,cur:=samples[i-1],samples[i]
  if cur.CapturedAt.Before(start)||!prev.CapturedAt.Before(end)||!cur.CapturedAt.After(prev.CapturedAt){continue}
  span:=cur.CapturedAt.Sub(prev.CapturedAt);if span>maxGap{if outageOpen>=0{row:=&m.Outages[outageOpen];row.End=prev.CapturedAt;row.DurationSeconds=int64(row.End.Sub(row.Start).Seconds());row.Open=false;outageOpen=-1};continue}
  known,online:=state(prev);if !known{if outageOpen>=0{row:=&m.Outages[outageOpen];row.End=prev.CapturedAt;row.DurationSeconds=int64(row.End.Sub(row.Start).Seconds());row.Open=false;outageOpen=-1};continue}
  left:=prev.CapturedAt;if left.Before(start){left=start};right:=cur.CapturedAt;if right.After(end){right=end}
  seconds:=int64(right.Sub(left).Seconds());if seconds<=0{continue}
  observed+=seconds;if online{up+=seconds}
  if i>0 {
   prevKnown,prevOnline:=state(prev);curKnown,curOnline:=state(cur)
   if prevKnown&&curKnown {
    if prevOnline&&!curOnline { outageOpen=len(m.Outages);m.Outages=append(m.Outages,availabilityOutage{Start:cur.CapturedAt,Open:true}) }
    if !prevOnline&&!curOnline&&outageOpen<0 {outageOpen=len(m.Outages);m.Outages=append(m.Outages,availabilityOutage{Start:prev.CapturedAt,Open:true})}
    if !prevOnline&&curOnline {if outageOpen<0{outageOpen=len(m.Outages);m.Outages=append(m.Outages,availabilityOutage{Start:prev.CapturedAt,Open:true})};row:=&m.Outages[outageOpen];row.End=cur.CapturedAt;row.DurationSeconds=int64(cur.CapturedAt.Sub(row.Start).Seconds());row.Open=false;outageOpen=-1}
   }
  }
 }
 total:=int64(end.Sub(start).Seconds());if total<0{total=0}
 m.ObservedSeconds=observed;m.UnknownSeconds=total-observed;if m.UnknownSeconds<0{m.UnknownSeconds=0}
 if observed>0 {v:=float64(up)*100/float64(observed);m.AvailabilityPercent=&v}
 for i:=range m.Outages { if m.Outages[i].Open {m.Outages[i].DurationSeconds=int64(end.Sub(m.Outages[i].Start).Seconds())} }
 m.OutageCount=len(m.Outages)
 for _,o:=range m.Outages {m.OutageSeconds+=o.DurationSeconds}
 return m
}

type availabilityTimelinePoint struct {
 At time.Time `json:"at"`
 AvailabilityPercent *float64 `json:"availabilityPercent"`
 ObservedSeconds int64 `json:"observedSeconds"`
 UnknownSeconds int64 `json:"unknownSeconds"`
}
type diagnosticsAvailabilityReport struct {
 Window string `json:"window"`
 From time.Time `json:"from"`
 To time.Time `json:"to"`
 MaxSampleGapSeconds int64 `json:"maxSampleGapSeconds"`
 Device availabilityMetric `json:"device"`
 Aggregations []availabilityMetric `json:"aggregations"`
 Timeline []availabilityTimelinePoint `json:"timeline"`
 Method string `json:"method"`
}
func buildDiagnosticsAvailabilityReport(store *diagnosticsStore,window string)(diagnosticsAvailabilityReport,error){
 var report diagnosticsAvailabilityReport
 var duration time.Duration
 switch window {case "24h":duration=24*time.Hour;case "7d":duration=7*24*time.Hour;default:return report,fmt.Errorf("window musi mieć wartość 24h lub 7d")}
 all,err:=store.list();if err!=nil{return report,err}
 sort.Slice(all,func(i,j int)bool{return all[i].CapturedAt.Before(all[j].CapturedAt)})
 end:=time.Now().UTC();start:=end.Add(-duration)
 _,cadence:=diagnosticsSamplingSettings();interval,err:=time.ParseDuration(cadence);if err!=nil||interval<=0{interval=time.Minute}
 maxGap:=2*interval;if maxGap<2*time.Minute{maxGap=2*time.Minute}
 deviceState:=func(s diagnosticsSample)(bool,bool){return s.Checks!=nil,s.Checks["runningConfig"]}
 report=diagnosticsAvailabilityReport{Window:window,From:start,To:end,MaxSampleGapSeconds:int64(maxGap.Seconds()),Device:diagnosticsAvailabilityMetric("Dostępność CLI",all,start,end,maxGap,deviceState),Aggregations:[]availabilityMetric{},Timeline:[]availabilityTimelinePoint{},Method:"Uptime jest liczony jako udany odczyt CLI (show running-config), a agregacje jako rozpoznany stan LACP. Procent odnosi się wyłącznie do przedziałów objętych poprawnymi, dostatecznie częstymi próbkami. Luki pomiarowe są wyłączone z mianownika i pokazane jako brak danych; to nie jest gwarantowane SLA ani pomiar dostępności ruchu użytkowników."}
 names:=map[string]bool{}
 for _,sample:=range all {if sample.Checks["lacp"]{for _,g:=range sample.Aggregations{names[g.Name]=true}}}
 sortedNames:=make([]string,0,len(names));for name:=range names{sortedNames=append(sortedNames,name)};sort.Strings(sortedNames)
 for _,name:=range sortedNames {
  state:=func(s diagnosticsSample)(bool,bool){
   if !s.Checks["lacp"]{return false,false}
   for _,g:=range s.Aggregations{if strings.EqualFold(g.Name,name){return true,g.Live&&g.Ready!="Niegotowy"}}
   return true,false
  }
  report.Aggregations=append(report.Aggregations,diagnosticsAvailabilityMetric(name,all,start,end,maxGap,state))
 }
 buckets:=24;step:=time.Hour;if window=="7d"{buckets=7;step=24*time.Hour}
 for i:=0;i<buckets;i++{
  bucketStart:=start.Add(time.Duration(i)*step);bucketEnd:=bucketStart.Add(step);if bucketEnd.After(end){bucketEnd=end}
  if !bucketEnd.After(bucketStart){continue}
  metric:=diagnosticsAvailabilityMetric("CLI",all,bucketStart,bucketEnd,maxGap,deviceState)
  report.Timeline=append(report.Timeline,availabilityTimelinePoint{At:bucketStart,AvailabilityPercent:metric.AvailabilityPercent,ObservedSeconds:metric.ObservedSeconds,UnknownSeconds:metric.UnknownSeconds})
 }
 return report,nil
}
func diagnosticsAvailabilityHandler(store *diagnosticsStore)http.Handler{
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodGet{http.Error(w,"method not allowed",405);return}
  window:=r.URL.Query().Get("window");if window==""{window="24h"}
  report,err:=buildDiagnosticsAvailabilityReport(store,window)
  if err!=nil{if strings.Contains(err.Error(),"window"){http.Error(w,err.Error(),400)}else{http.Error(w,"nie udało się odczytać historii diagnostycznej",500)};return}
  w.Header().Set("Content-Type","application/json; charset=utf-8");w.Header().Set("Cache-Control","no-store")
  _=json.NewEncoder(w).Encode(report)
 })
}
func diagnosticsAvailabilityCSVHandler(store *diagnosticsStore)http.Handler{
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.Method!=http.MethodGet{http.Error(w,"method not allowed",405);return}
  window:=r.URL.Query().Get("window");if window==""{window="24h"}
  report,err:=buildDiagnosticsAvailabilityReport(store,window)
  if err!=nil{if strings.Contains(err.Error(),"window"){http.Error(w,err.Error(),400)}else{http.Error(w,"nie udało się odczytać historii diagnostycznej",500)};return}
  w.Header().Set("Content-Type","text/csv; charset=utf-8")
  w.Header().Set("Content-Disposition",`attachment; filename="switch-availability-`+window+`.csv"`)
  w.Header().Set("Cache-Control","no-store")
  _,_=w.Write([]byte{0xEF,0xBB,0xBF})
  writer:=csv.NewWriter(w);writer.Comma=';'
  _=writer.Write([]string{"Typ","Nazwa","Dostępność (%)","Czas pomiaru (s)","Brak danych (s)","Liczba przerw","Czas przerw (s)","Początek przerwy","Koniec przerwy","Przerwa trwa"})
  writeMetric:=func(kind string,m availabilityMetric){
   percent:="";if m.AvailabilityPercent!=nil{percent=strconv.FormatFloat(*m.AvailabilityPercent,'f',4,64)}
   _=writer.Write([]string{kind,m.Name,percent,strconv.FormatInt(m.ObservedSeconds,10),strconv.FormatInt(m.UnknownSeconds,10),strconv.Itoa(m.OutageCount),strconv.FormatInt(m.OutageSeconds,10),"","",""})
   for _,out:=range m.Outages{
    finish:="";if !out.End.IsZero(){finish=out.End.Format(time.RFC3339)}
    _=writer.Write([]string{kind+" — przerwa",m.Name,"","","","","",out.Start.Format(time.RFC3339),finish,strconv.FormatBool(out.Open)})
   }
  }
  writeMetric("Urządzenie / CLI",report.Device)
  for _,metric:=range report.Aggregations{writeMetric("Agregacja LACP",metric)}
  _=writer.Write([]string{"Metoda",report.Method})
  _=writer.Write([]string{"Okres od",report.From.Format(time.RFC3339)})
  _=writer.Write([]string{"Okres do",report.To.Format(time.RFC3339)})
  _=writer.Write([]string{"Maksymalna luka próbki (s)",strconv.FormatInt(report.MaxSampleGapSeconds,10)})
  writer.Flush()
 })
}

