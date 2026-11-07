package main

import (
 "bufio"
 "encoding/json"
 "path/filepath"
 "sync"
 "crypto/subtle"
 "crypto/rand"
 "encoding/hex"
 "context"
 "log"
 "net/http"
 "net/url"
 "os"
 "strings"
 "time"

 "gs948mx-manager/internal/device"
 "gs948mx-manager/internal/server"
)

func main() {
 if err := loadEnv(".env"); err != nil && !os.IsNotExist(err) { log.Fatalf("Nie można odczytać .env: %v", err) }
 cfg := device.Config{
  Host: os.Getenv("SWITCH_HOST"), SSHPort: env("SWITCH_SSH_PORT","22"),
  SSHUser: os.Getenv("SWITCH_SSH_USER"), SSHPassword: os.Getenv("SWITCH_SSH_PASSWORD"),
  SSHHostKeyFingerprint: os.Getenv("SWITCH_SSH_HOST_KEY"),
  SNMPCommunity: os.Getenv("SWITCH_SNMP_COMMUNITY"), SNMPPort: env("SWITCH_SNMP_PORT","161"),
  SNMPVersion: env("SWITCH_SNMP_VERSION","2c"),
  SNMPV3User: os.Getenv("SWITCH_SNMP_V3_USER"),
  SNMPV3AuthPassword: os.Getenv("SWITCH_SNMP_V3_AUTH_PASSWORD"),
  SNMPV3PrivacyPassword: os.Getenv("SWITCH_SNMP_V3_PRIV_PASSWORD"),
  Timeout: 4*time.Second,
 }
 svc := device.New(cfg)
 addr := env("LISTEN_ADDR","127.0.0.1:8090")
 mux := http.NewServeMux()
 server.Register(mux,svc)
 panelUser, panelPassword := os.Getenv("PANEL_USER"), os.Getenv("PANEL_PASSWORD")
 if panelUser == "" || len(panelPassword) < 8 {
  log.Fatal("Wymagane PANEL_USER oraz PANEL_PASSWORD (minimum 8 znaków); panel nie zostanie uruchomiony bez uwierzytelnienia")
 }
 panelRole := env("PANEL_ROLE", "admin")
 if !validPanelRole(panelRole) { log.Fatalf("Nieprawidłowy PANEL_ROLE: dozwolone wartości to viewer, operator, admin") }
 handler := securityHeaders(sessionAuth(auditLog(mux, env("AUDIT_LOG_PATH", filepath.Join("data", "audit.jsonl"))), panelUser, panelPassword))
 log.Printf("CiscoSwitch Manager listening on http://%s (switch=%s, snmp-version=%s, community=%s)",addr,cfg.Host,snmpMode(cfg),masked(cfg.SNMPCommunity))
 httpServer := &http.Server{
  Addr: addr,
  Handler: handler,
  ReadHeaderTimeout: 5 * time.Second,
  ReadTimeout: 15 * time.Second,
  WriteTimeout: 60 * time.Second,
  IdleTimeout: 60 * time.Second,
  MaxHeaderBytes: 1 << 20,
 }
 log.Fatal(httpServer.ListenAndServe())
}
func loadEnv(path string) error {
 f,err:=os.Open(path); if err!=nil{return err}; defer f.Close()
 sc:=bufio.NewScanner(f)
 for sc.Scan(){ line:=strings.TrimSpace(sc.Text()); if line==""||strings.HasPrefix(line,"#"){continue}; line=strings.TrimPrefix(line,"export "); k,v,ok:=strings.Cut(line,"="); if !ok{continue}; k=strings.TrimSpace(k); v=strings.TrimSpace(v); if len(v)>=2&&((v[0]=='"'&&v[len(v)-1]=='"')||(v[0]=='\''&&v[len(v)-1]=='\'')){v=v[1:len(v)-1]}; if k!=""&&os.Getenv(k)==""{if err:=os.Setenv(k,v);err!=nil{return err}} }
 return sc.Err()
}
func env(k,f string)string{if v:=os.Getenv(k);v!=""{return v};return f}
func snmpMode(c device.Config)string{v:=strings.TrimSpace(c.SNMPVersion);if v==""{return "2c"};return v}
func masked(v string)string{if v==""{return "nieustawione"};return "ustawione"}
func securityHeaders(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Content-Type-Options","nosniff");w.Header().Set("X-Frame-Options","DENY");w.Header().Set("Referrer-Policy","no-referrer");w.Header().Set("Content-Security-Policy","default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:");next.ServeHTTP(w,r)})}


const panelSessionCookie = "switch_panel_session"
const panelSessionTTL = 12 * time.Hour

type panelSession struct { expires time.Time; username string; role string }
type panelSessions struct {
 mu sync.Mutex
 values map[string]panelSession
}

func (s *panelSessions) create(username, role string) (string,error) {
 raw:=make([]byte,32)
 if _,err:=rand.Read(raw);err!=nil{return "",err}
 token:=hex.EncodeToString(raw)
 s.mu.Lock()
 if s.values==nil{s.values=make(map[string]panelSession)}
 now:=time.Now()
 for key,value:=range s.values{if now.After(value.expires){delete(s.values,key)}}
 s.values[token]=panelSession{expires:now.Add(panelSessionTTL), username:username, role:role}
 s.mu.Unlock()
 return token,nil
}
func (s *panelSessions) get(token string) (panelSession, bool) {
 if len(token)!=64{return panelSession{},false}
 s.mu.Lock();defer s.mu.Unlock()
 value,ok:=s.values[token]
 if !ok{return panelSession{},false}
 if time.Now().After(value.expires){delete(s.values,token);return panelSession{},false}
 return value,true
}
func (s *panelSessions) revoke(token string) {
 s.mu.Lock();delete(s.values,token);s.mu.Unlock()
}

type identityContextKey struct{}
type panelIdentity struct { Username string; Role string }

func validPanelRole(role string) bool { return role=="viewer" || role=="operator" || role=="admin" }
func roleAllows(role, method, path string) bool {
 if role=="admin" { return true }
 if method==http.MethodGet || method==http.MethodHead || method==http.MethodOptions { return true }
 if role=="viewer" && (path=="/api/config/export" || path=="/api/config/backups/download" || path=="/api/audit") { return false }
 safePost := path=="/api/network/probe" || strings.HasSuffix(path,"/preview")
 if safePost { return true }
 return role=="operator" && !strings.HasPrefix(path,"/api/users")
}
func sessionAuth(next http.Handler, username, password string) http.Handler {
 return sessionAuthWithRole(next,username,password,env("PANEL_ROLE","admin"))
}
func sessionAuthWithRole(next http.Handler, username, password, role string) http.Handler {
 sessions:=&panelSessions{values:make(map[string]panelSession)}
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  path:=r.URL.Path
  if path=="/login" || path=="/login/" {
   if r.Method!=http.MethodGet && r.Method!=http.MethodHead {http.Error(w,"method not allowed",405);return}
   r.URL.Path="/login.html"
   next.ServeHTTP(w,r);return
  }
  if path=="/login.css" || path=="/login.js" {
   next.ServeHTTP(w,r);return
  }
  if path=="/api/login" {
   if r.Method!=http.MethodPost {http.Error(w,"method not allowed",405);return}
   if !sameOrigin(r) {http.Error(w,"forbidden origin",http.StatusForbidden);return}
   r.Body=http.MaxBytesReader(w,r.Body,2048)
   var credentials struct { Username string `json:"username"`; Password string `json:"password"` }
   if json.NewDecoder(r.Body).Decode(&credentials)!=nil {http.Error(w,"invalid credentials",http.StatusUnauthorized);return}
   userOK:=subtle.ConstantTimeCompare([]byte(credentials.Username),[]byte(username))==1
   passOK:=subtle.ConstantTimeCompare([]byte(credentials.Password),[]byte(password))==1
   if !userOK || !passOK {w.Header().Set("Cache-Control","no-store");http.Error(w,"Nieprawidłowy login lub hasło.",http.StatusUnauthorized);return}
   token,err:=sessions.create(credentials.Username,role)
   if err!=nil {http.Error(w,"Nie można utworzyć sesji.",http.StatusInternalServerError);return}
   http.SetCookie(w,&http.Cookie{Name:panelSessionCookie,Value:token,Path:"/",HttpOnly:true,Secure:r.TLS!=nil||strings.EqualFold(r.Header.Get("X-Forwarded-Proto"),"https"),SameSite:http.SameSiteStrictMode,MaxAge:int(panelSessionTTL.Seconds())})
   w.Header().Set("Content-Type","application/json")
   w.Header().Set("Cache-Control","no-store")
   _=json.NewEncoder(w).Encode(map[string]any{"ok":true,"expiresIn":int(panelSessionTTL.Seconds()),"username":credentials.Username,"role":role})
   return
  }
  if path=="/api/logout" {
   if r.Method!=http.MethodPost {http.Error(w,"method not allowed",405);return}
   if !sameOrigin(r) {http.Error(w,"forbidden origin",http.StatusForbidden);return}
   cookie,err:=r.Cookie(panelSessionCookie)
   if err==nil {sessions.revoke(cookie.Value)}
   clearPanelCookie(w,r)
   w.Header().Set("Content-Type","application/json")
   _=json.NewEncoder(w).Encode(map[string]bool{"ok":true})
   return
  }
  cookie,err:=r.Cookie(panelSessionCookie)
  session,valid:=panelSession{},false
  if err==nil { session,valid=sessions.get(cookie.Value) }
  if !valid {
   w.Header().Set("Cache-Control","no-store")
   if strings.HasPrefix(path,"/api/") {http.Error(w,"session expired",http.StatusUnauthorized);return}
   http.Redirect(w,r,"/login",http.StatusSeeOther);return
  }
  if !sameOrigin(r) {http.Error(w,"forbidden origin",http.StatusForbidden);return}
  if path=="/api/session" {
   if r.Method!=http.MethodGet {http.Error(w,"method not allowed",http.StatusMethodNotAllowed);return}
   w.Header().Set("Content-Type","application/json")
   w.Header().Set("Cache-Control","no-store")
   _=json.NewEncoder(w).Encode(map[string]string{"username":session.username,"role":session.role})
   return
  }
  if strings.HasPrefix(path,"/api/") && !roleAllows(session.role,r.Method,path) {
   w.Header().Set("Content-Type","application/json")
   w.Header().Set("Cache-Control","no-store")
   w.WriteHeader(http.StatusForbidden)
   _=json.NewEncoder(w).Encode(map[string]string{"error":"Brak uprawnień do wykonania tej operacji."})
   return
  }
  ctx:=context.WithValue(r.Context(),identityContextKey{},panelIdentity{Username:session.username,Role:session.role})
  next.ServeHTTP(w,r.WithContext(ctx))
 })
}
func sameOrigin(r *http.Request) bool {
 if r.Method==http.MethodGet || r.Method==http.MethodHead || r.Method==http.MethodOptions {return true}
 origin:=r.Header.Get("Origin")
 if origin=="" {return true}
 u,err:=url.Parse(origin)
 return err==nil && strings.EqualFold(u.Host,r.Host) && (u.Scheme=="http"||u.Scheme=="https")
}
func clearPanelCookie(w http.ResponseWriter,r *http.Request) {
 http.SetCookie(w,&http.Cookie{Name:panelSessionCookie,Value:"",Path:"/",HttpOnly:true,Secure:r.TLS!=nil||strings.EqualFold(r.Header.Get("X-Forwarded-Proto"),"https"),SameSite:http.SameSiteStrictMode,MaxAge:-1})
}



type auditRecord struct {
 Timestamp time.Time `json:"timestamp"`
 Username string `json:"username"`
 Method string `json:"method"`
 Path string `json:"path"`
 Status int `json:"status"`
 DurationMS int64 `json:"durationMs"`
}

type statusWriter struct { http.ResponseWriter; status int }
func (w *statusWriter) WriteHeader(code int) { if w.status==0 {w.status=code}; w.ResponseWriter.WriteHeader(code) }
func (w *statusWriter) Write(p []byte)(int,error){if w.status==0{w.status=http.StatusOK};return w.ResponseWriter.Write(p)}

var auditMu sync.Mutex

// auditLog records request metadata only; it never stores request bodies, CLI output,
// passwords, SNMP communities, query strings, or configuration contents.
func auditLog(next http.Handler, path string) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  if !strings.HasPrefix(r.URL.Path,"/api/") || (r.Method!="POST" && r.Method!="PUT" && r.Method!="PATCH" && r.Method!="DELETE") {
   next.ServeHTTP(w,r); return
  }
  started:=time.Now()
  sw:=&statusWriter{ResponseWriter:w}
  next.ServeHTTP(sw,r)
  status:=sw.status; if status==0 {status=http.StatusOK}
  username:=env("PANEL_USER","unknown")
  if identity,ok:=r.Context().Value(identityContextKey{}).(panelIdentity);ok && identity.Username!="" { username=identity.Username }
  rec:=auditRecord{Timestamp:time.Now().UTC(),Username:username,Method:r.Method,Path:r.URL.Path,Status:status,DurationMS:time.Since(started).Milliseconds()}
  auditMu.Lock()
  defer auditMu.Unlock()
  dir:=filepath.Dir(path)
  if err:=os.MkdirAll(dir,0700);err!=nil{log.Printf("audit log unavailable: %v",err);return}
  if err:=os.Chmod(dir,0700);err!=nil{log.Printf("audit directory permissions: %v",err);return}
  file,err:=os.OpenFile(path,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600)
  if err!=nil{log.Printf("audit log unavailable: %v",err);return}
  if err:=file.Chmod(0600);err!=nil{_ = file.Close();log.Printf("audit file permissions: %v",err);return}
  encErr:=json.NewEncoder(file).Encode(rec)
  closeErr:=file.Close()
  if encErr!=nil{log.Printf("audit write failed: %v",encErr)} else if closeErr!=nil{log.Printf("audit close failed: %v",closeErr)}
 })
}
