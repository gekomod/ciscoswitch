package main

import (
 "bufio"
 "log"
 "net/http"
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
 log.Printf("CiscoSwitch Manager listening on http://%s (switch=%s, snmp=%s)",addr,cfg.Host,masked(cfg.SNMPCommunity))
 log.Fatal(http.ListenAndServe(addr,securityHeaders(mux)))
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
