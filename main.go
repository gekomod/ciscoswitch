package main

import (
 "bufio"
 "encoding/json"
 "path/filepath"
 "sync"
 "crypto/subtle"
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
  Timeout: 4*time.Second,
 }
 svc := device.New(cfg)
 addr := env("LISTEN_ADDR","127.0.0.1:8090")
 mux := http.NewServeMux()
 server.Register(mux,svc)
 panelUser, panelPassword := os.Getenv("PANEL_USER"), os.Getenv("PANEL_PASSWORD")
 if panelUser == "" || len(panelPassword) < 16 {
  log.Fatal("Wymagane PANEL_USER oraz PANEL_PASSWORD (minimum 16 znaków); panel nie zostanie uruchomiony bez uwierzytelnienia")
 }
 handler := securityHeaders(auditLog(basicAuth(mux, panelUser, panelPassword), env("AUDIT_LOG_PATH", filepath.Join("data", "audit.jsonl"))))
 log.Printf("CiscoSwitch Manager listening on http://%s (switch=%s, snmp=%s)",addr,cfg.Host,masked(cfg.SNMPCommunity))
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
func masked(v string)string{if v==""{return "nieustawione"};return "ustawione"}
func securityHeaders(next http.Handler)http.Handler{return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.Header().Set("X-Content-Type-Options","nosniff");w.Header().Set("X-Frame-Options","DENY");w.Header().Set("Referrer-Policy","no-referrer");w.Header().Set("Content-Security-Policy","default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:");next.ServeHTTP(w,r)})}

func basicAuth(next http.Handler, username, password string) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
   origin := r.Header.Get("Origin")
   if origin != "" {
    u, err := url.Parse(origin)
    if err != nil || !strings.EqualFold(u.Host, r.Host) {
     http.Error(w, "forbidden origin", http.StatusForbidden)
     return
    }
   }
  }
  user, pass, ok := r.BasicAuth()
  userOK := subtle.ConstantTimeCompare([]byte(user), []byte(username)) == 1
  passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(password)) == 1
  if !ok || !userOK || !passOK {
   w.Header().Set("WWW-Authenticate", `Basic realm="Switch Manager", charset="UTF-8"`)
   w.Header().Set("Cache-Control", "no-store")
   http.Error(w, "authentication required", http.StatusUnauthorized)
   return
  }
  next.ServeHTTP(w, r)
 })
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
  rec:=auditRecord{Timestamp:time.Now().UTC(),Username:env("PANEL_USER","unknown"),Method:r.Method,Path:r.URL.Path,Status:status,DurationMS:time.Since(started).Milliseconds()}
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
