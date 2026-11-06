package server

import (
 "bufio"
 "encoding/json"
 "math"
 "net/http"
 "os"
 "path/filepath"
 "strings"
 "sync"
 "time"
)

const monitorHistoryMaxBytes = 2 << 20

type monitorRecord struct {
 Kind string `json:"kind"`
 Timestamp time.Time `json:"timestamp"`
 Message string `json:"message,omitempty"`
 Type string `json:"type,omitempty"`
 RX float64 `json:"rx,omitempty"`
 TX float64 `json:"tx,omitempty"`
}
type monitorHistoryResponse struct { Events []monitorRecord `json:"events"`; Samples []monitorRecord `json:"samples"` }
type monitorHistoryStore struct { path string; mu sync.Mutex }

func monitorHistoryHandler(path string) http.Handler {
 if strings.TrimSpace(path)=="" { path=filepath.Join("data","monitor-history.jsonl") }
 store:=&monitorHistoryStore{path:path}
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  switch r.Method {
  case http.MethodGet:
   result,err:=store.read(); if err!=nil { http.Error(w,"monitor history unavailable",500); return }
   w.Header().Set("Content-Type","application/json"); w.Header().Set("Cache-Control","no-store"); _=json.NewEncoder(w).Encode(result)
  case http.MethodPost:
   r.Body=http.MaxBytesReader(w,r.Body,2048); var rec monitorRecord
   dec:=json.NewDecoder(r.Body); dec.DisallowUnknownFields()
   if dec.Decode(&rec)!=nil || validateMonitorRecord(rec)!=nil { http.Error(w,"invalid monitor record",400); return }
   rec.Timestamp=time.Now().UTC()
   if err:=store.append(rec); err!=nil { http.Error(w,"monitor history write failed",500); return }
   w.WriteHeader(http.StatusNoContent)
  default:
   w.Header().Set("Allow","GET, POST"); http.Error(w,"method not allowed",405)
  }
 })
}
func validateMonitorRecord(r monitorRecord) error {
 switch r.Kind {
 case "event":
  if strings.TrimSpace(r.Message)=="" || len(r.Message)>300 { return http.ErrNotSupported }
  switch r.Type { case "info","success","warning","danger": default: return http.ErrNotSupported }
 case "sample":
  if math.IsNaN(r.RX)||math.IsNaN(r.TX)||math.IsInf(r.RX,0)||math.IsInf(r.TX,0)||r.RX<0||r.TX<0 { return http.ErrNotSupported }
 default: return http.ErrNotSupported
 }
 return nil
}
func (s *monitorHistoryStore) append(r monitorRecord) error {
 s.mu.Lock(); defer s.mu.Unlock()
 if err:=os.MkdirAll(filepath.Dir(s.path),0700); err!=nil{return err}
 if err:=os.Chmod(filepath.Dir(s.path),0700); err!=nil{return err}
 f,err:=os.OpenFile(s.path,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600); if err!=nil{return err}
 if err=f.Chmod(0600); err!=nil{_ = f.Close();return err}
 e1:=json.NewEncoder(f).Encode(r); e2:=f.Close(); if e1!=nil{return e1}; if e2!=nil{return e2}
 return s.prune()
}
func (s *monitorHistoryStore) read() (monitorHistoryResponse,error) {
 s.mu.Lock(); defer s.mu.Unlock()
 out:=monitorHistoryResponse{Events:[]monitorRecord{},Samples:[]monitorRecord{}}
 f,err:=os.Open(s.path); if os.IsNotExist(err){return out,nil}; if err!=nil{return out,err}; defer f.Close()
 info,err:=f.Stat(); if err!=nil{return out,err}; if info.Size()>monitorHistoryMaxBytes{return out,os.ErrInvalid}
 sc:=bufio.NewScanner(f); sc.Buffer(make([]byte,4096),4096)
 for sc.Scan(){var r monitorRecord;if json.Unmarshal(sc.Bytes(),&r)!=nil{continue};switch r.Kind{case "event":out.Events=append(out.Events,r);case "sample":out.Samples=append(out.Samples,r)}}
 if err:=sc.Err();err!=nil{return out,err}
 if len(out.Events)>200{out.Events=out.Events[len(out.Events)-200:]}
 if len(out.Samples)>300{out.Samples=out.Samples[len(out.Samples)-300:]}
 return out,nil
}
func (s *monitorHistoryStore) prune() error {
 info,err:=os.Stat(s.path);if err!=nil{return err};if info.Size()<=monitorHistoryMaxBytes{return nil}
 data,err:=os.ReadFile(s.path);if err!=nil{return err}
 lines:=strings.Split(strings.TrimSpace(string(data)),"\n"); kept:=make([]string,0,len(lines)); total:=0
 for i:=len(lines)-1;i>=0&&len(kept)<2000;i--{line:=lines[i];if line==""||total+len(line)+1>monitorHistoryMaxBytes/2{continue};kept=append(kept,line);total+=len(line)+1}
 tmp:=s.path+".tmp";f,err:=os.OpenFile(tmp,os.O_CREATE|os.O_TRUNC|os.O_WRONLY,0600);if err!=nil{return err}
 if err=f.Chmod(0600);err!=nil{_ = f.Close();_ = os.Remove(tmp);return err}
 for i:=len(kept)-1;i>=0;i--{if _,err=f.WriteString(kept[i]+"\n");err!=nil{_ = f.Close();_ = os.Remove(tmp);return err}}
 if err=f.Close();err!=nil{_ = os.Remove(tmp);return err};return os.Rename(tmp,s.path)
}
