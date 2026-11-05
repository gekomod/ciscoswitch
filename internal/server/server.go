package server

import (
 "encoding/json"
 "net/http"
 "strings"
 "gs948mx-manager/internal/device"
)

func Register(m *http.ServeMux,svc *device.Service){
 m.Handle("/",http.FileServer(http.Dir("web")))
 m.HandleFunc("/api/status",func(w http.ResponseWriter,r *http.Request){if r.Method!="GET"{http.Error(w,"method not allowed",405);return};w.Header().Set("Content-Type","application/json");_ = json.NewEncoder(w).Encode(svc.Status())})
 m.HandleFunc("/api/port",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,2048);var q struct{Port string `json:"port"`;Action string `json:"action"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=svc.ConfigurePort(q.Port,q.Action);write(w,v,e)})
 m.HandleFunc("/api/management-dhcp",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,1024);var q struct{Enabled bool `json:"enabled"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=svc.SetManagementDHCP(q.Enabled);write(w,v,e)})
 m.HandleFunc("/api/save",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};v,e:=svc.SaveConfig();write(w,v,e)})
 m.HandleFunc("/api/command",func(w http.ResponseWriter,r *http.Request){if r.Method!="POST"{http.Error(w,"method not allowed",405);return};r.Body=http.MaxBytesReader(w,r.Body,4096);var q struct{Command string `json:"command"`};if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return};v,e:=svc.RunReadOnly(strings.TrimSpace(q.Command));write(w,v,e)})
}
func write(w http.ResponseWriter,v string,e error){w.Header().Set("Content-Type","application/json");if e!=nil{w.WriteHeader(400);_ = json.NewEncoder(w).Encode(map[string]string{"error":e.Error(),"output":v});return};_ = json.NewEncoder(w).Encode(map[string]string{"output":v,"ok":"true"})}
