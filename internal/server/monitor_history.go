package server

import (
 "bufio"
 "encoding/json"
 "fmt"
 "math"
 "net/http"
 "os"
 "path/filepath"
 "strings"
 "sync"
 "time"
 "log"
 "gs948mx-manager/internal/device"
)

const monitorHistoryMaxBytes = 2 << 20

type monitorRecord struct {
 Kind string `json:"kind"`
 Timestamp time.Time `json:"timestamp"`
 Message string `json:"message,omitempty"`
 Type string `json:"type,omitempty"`
 Port string `json:"port,omitempty"`
 State string `json:"state,omitempty"`
 RX float64 `json:"rx,omitempty"`
 TX float64 `json:"tx,omitempty"`
 Ports map[string]monitorPortRate `json:"ports,omitempty"`
 ErrorRXDelta float64 `json:"errorRxDelta,omitempty"`
 ErrorTXDelta float64 `json:"errorTxDelta,omitempty"`
 DiscardRXDelta float64 `json:"discardRxDelta,omitempty"`
 DiscardTXDelta float64 `json:"discardTxDelta,omitempty"`
 ErrorRXTotal float64 `json:"errorRxTotal,omitempty"`
 ErrorTXTotal float64 `json:"errorTxTotal,omitempty"`
 DiscardRXTotal float64 `json:"discardRxTotal,omitempty"`
 DiscardTXTotal float64 `json:"discardTxTotal,omitempty"`
}
type monitorPortRate struct { RX float64 `json:"rx"`; TX float64 `json:"tx"` }
type monitorHistoryResponse struct { Events []monitorRecord `json:"events"`; Samples []monitorRecord `json:"samples"`; PortEvents []monitorRecord `json:"portEvents"`; CounterEvents []monitorRecord `json:"counterEvents"`; PortSamples []monitorRecord `json:"portSamples"` }
type monitorHistoryStore struct { path string; mu sync.Mutex }

type monitorEventHub struct {
	mu sync.Mutex
	nextID uint64
	subscribers map[uint64]chan monitorRecord
}

var sharedMonitorEventHub = &monitorEventHub{subscribers: make(map[uint64]chan monitorRecord)}

func (h *monitorEventHub) subscribe() (uint64, <-chan monitorRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subscribers == nil { h.subscribers = make(map[uint64]chan monitorRecord) }
	h.nextID++
	ch := make(chan monitorRecord, 32)
	h.subscribers[h.nextID] = ch
	return h.nextID, ch
}

func (h *monitorEventHub) unsubscribe(id uint64) {
	h.mu.Lock()
	if ch, ok := h.subscribers[id]; ok { delete(h.subscribers, id); close(ch) }
	h.mu.Unlock()
}

func (h *monitorEventHub) publish(record monitorRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subscribers {
		select { case ch <- record: default:
			// A slow client must never block monitoring for other users.
			select { case <-ch: default: }
			select { case ch <- record: default: }
		}
	}
}

func monitorStreamHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet { w.Header().Set("Allow", "GET"); http.Error(w, "method not allowed", http.StatusMethodNotAllowed); return }
		flusher, ok := w.(http.Flusher)
		if !ok { http.Error(w, "streaming is not supported", http.StatusInternalServerError); return }
		id, events := sharedMonitorEventHub.subscribe()
		defer sharedMonitorEventHub.unsubscribe(id)
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		_, _ = fmt.Fprint(w, "event: ready\ndata: {}\n\n")
		flusher.Flush()
		heartbeat := time.NewTicker(20 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-r.Context().Done(): return
			case record, open := <-events:
				if !open { return }
				payload, err := json.Marshal(record)
				if err != nil { continue }
				if _, err := fmt.Fprintf(w, "event: monitor\ndata: %s\n\n", payload); err != nil { return }
				flusher.Flush()
			case <-heartbeat.C:
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil { return }
				flusher.Flush()
			}
		}
	})
}

func monitorHistoryHandler(path string) http.Handler {
 if strings.TrimSpace(path)=="" { path=filepath.Join("data","monitor-history.jsonl") }
 store:=&monitorHistoryStore{path:path}
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  switch r.Method {
  case http.MethodGet:
   result,err:=store.read(); if err!=nil { http.Error(w,"monitor history unavailable",500); return }
   w.Header().Set("Content-Type","application/json"); w.Header().Set("Cache-Control","no-store"); _=json.NewEncoder(w).Encode(result)
  case http.MethodPost:
   r.Body=http.MaxBytesReader(w,r.Body,16384); var rec monitorRecord
   dec:=json.NewDecoder(r.Body); dec.DisallowUnknownFields()
   if dec.Decode(&rec)!=nil || validateMonitorRecord(rec)!=nil { http.Error(w,"invalid monitor record",400); return }
   rec.Timestamp=time.Now().UTC()
   if err:=store.append(rec); err!=nil { http.Error(w,"monitor history write failed",500); return }
   sharedMonitorEventHub.publish(rec)
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
 case "port":
  if len(r.Port)>64 || !strings.HasPrefix(r.Port,"port") || strings.ContainsAny(r.Port," <>\\\"'") { return http.ErrNotSupported }
  if r.State!="up" && r.State!="down" { return http.ErrNotSupported }
 case "counter":
  if len(r.Port)>64 || !strings.HasPrefix(r.Port,"port") || strings.ContainsAny(r.Port," <>\\\"'") { return http.ErrNotSupported }
  values:=[]float64{r.ErrorRXDelta,r.ErrorTXDelta,r.DiscardRXDelta,r.DiscardTXDelta,r.ErrorRXTotal,r.ErrorTXTotal,r.DiscardRXTotal,r.DiscardTXTotal}; for _,v:=range values { if math.IsNaN(v)||math.IsInf(v,0)||v<0 { return http.ErrNotSupported } }; if r.ErrorRXDelta==0 && r.ErrorTXDelta==0 && r.DiscardRXDelta==0 && r.DiscardTXDelta==0 { return http.ErrNotSupported }
 case "sample":
  if math.IsNaN(r.RX)||math.IsNaN(r.TX)||math.IsInf(r.RX,0)||math.IsInf(r.TX,0)||r.RX<0||r.TX<0 { return http.ErrNotSupported }
 case "port_sample":
  if len(r.Ports)==0||len(r.Ports)>64 { return http.ErrNotSupported }
  for name,rate:=range r.Ports { if len(name)>64||!strings.HasPrefix(name,"port")||strings.ContainsAny(name, " <>\\\"'")||math.IsNaN(rate.RX)||math.IsNaN(rate.TX)||math.IsInf(rate.RX,0)||math.IsInf(rate.TX,0)||rate.RX<0||rate.TX<0 { return http.ErrNotSupported } }
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
 out:=monitorHistoryResponse{Events:[]monitorRecord{},Samples:[]monitorRecord{},PortEvents:[]monitorRecord{},CounterEvents:[]monitorRecord{},PortSamples:[]monitorRecord{}}
 f,err:=os.Open(s.path); if os.IsNotExist(err){return out,nil}; if err!=nil{return out,err}; defer f.Close()
 info,err:=f.Stat(); if err!=nil{return out,err}; if info.Size()>monitorHistoryMaxBytes{return out,os.ErrInvalid}
 sc:=bufio.NewScanner(f); sc.Buffer(make([]byte,4096),16384)
 for sc.Scan(){var r monitorRecord;if json.Unmarshal(sc.Bytes(),&r)!=nil{continue};switch r.Kind{case "event":out.Events=append(out.Events,r);case "sample":out.Samples=append(out.Samples,r);case "port":out.PortEvents=append(out.PortEvents,r);case "counter":out.CounterEvents=append(out.CounterEvents,r);case "port_sample":out.PortSamples=append(out.PortSamples,r)}}
 if err:=sc.Err();err!=nil{return out,err}
 if len(out.Events)>200{out.Events=out.Events[len(out.Events)-200:]}
 if len(out.Samples)>300{out.Samples=out.Samples[len(out.Samples)-300:]}
 if len(out.PortEvents)>2000{out.PortEvents=out.PortEvents[len(out.PortEvents)-2000:]}
 if len(out.CounterEvents)>2000{out.CounterEvents=out.CounterEvents[len(out.CounterEvents)-2000:]}
 if len(out.PortSamples)>60{out.PortSamples=out.PortSamples[len(out.PortSamples)-60:]}
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


// StartMonitorCollector records device availability, link transitions and counter
// increases independently of browser sessions. Traffic-rate sampling remains in the
// UI because it needs closely spaced counter snapshots and is already persisted there.
func StartMonitorCollector(ctx context.Context, svc *device.Service, path string, interval time.Duration) {
 if svc==nil || interval<=0 { return }
 store:=&monitorHistoryStore{path:path}
 go func(){
  var previousReachable *bool
  previousPorts:=map[string]device.Port{}
  collect:=func(){
   status:=svc.Status()
   now:=time.Now().UTC()
   if previousReachable!=nil && *previousReachable!=status.Reachable {
    message:="Utracono połączenie SNMP";kind:="danger"
    if status.Reachable { message="Połączenie SNMP zostało przywrócone";kind="success" }
    rec:=monitorRecord{Kind:"event",Timestamp:now,Message:message,Type:kind}
    if err:=store.append(rec);err!=nil{log.Printf("monitor history write failed: %v",err)}else{sharedMonitorEventHub.publish(rec)}
   }
   current:=make(map[string]device.Port,len(status.Ports))
   if status.Reachable {
    for _,p:=range status.Ports {
     current[p.Name]=p
     if old,ok:=previousPorts[p.Name];ok {
      if (old.Oper=="up"||old.Oper=="down")&&(p.Oper=="up"||p.Oper=="down")&&old.Oper!=p.Oper {
       rec:=monitorRecord{Kind:"port",Timestamp:now,Port:p.Name,State:p.Oper}
       if err:=store.append(rec);err!=nil{log.Printf("monitor history write failed: %v",err)}else{sharedMonitorEventHub.publish(rec)}
       event:=monitorRecord{Kind:"event",Timestamp:now,Message:fmt.Sprintf("%s: link %s → %s",p.Name,strings.ToUpper(old.Oper),strings.ToUpper(p.Oper)),Type:map[bool]string{true:"success",false:"danger"}[p.Oper=="up"]}
       if err:=store.append(event);err!=nil{log.Printf("monitor history write failed: %v",err)}else{sharedMonitorEventHub.publish(event)}
      }
      inErr:=counterDelta(p.InErrors,old.InErrors);outErr:=counterDelta(p.OutErrors,old.OutErrors)
      inDrop:=counterDelta(p.InDiscards,old.InDiscards);outDrop:=counterDelta(p.OutDiscards,old.OutDiscards)
      if inErr+outErr+inDrop+outDrop>0 {
       rec:=monitorRecord{Kind:"counter",Timestamp:now,Port:p.Name,ErrorRXDelta:float64(inErr),ErrorTXDelta:float64(outErr),DiscardRXDelta:float64(inDrop),DiscardTXDelta:float64(outDrop),ErrorRXTotal:float64(p.InErrors),ErrorTXTotal:float64(p.OutErrors),DiscardRXTotal:float64(p.InDiscards),DiscardTXTotal:float64(p.OutDiscards)}
       if err:=store.append(rec);err!=nil{log.Printf("monitor history write failed: %v",err)}else{sharedMonitorEventHub.publish(rec)}
      }
     }
    }
    previousPorts=current
   }
   reachable:=status.Reachable
   previousReachable=&reachable
  }
  collect()
  ticker:=time.NewTicker(interval);defer ticker.Stop()
  for { select { case <-ctx.Done():return; case <-ticker.C:collect() } }
 }()
}

func counterDelta(current, previous uint64) uint64 {
 if current>=previous { return current-previous }
 // Counter wrap or device reboot: do not report a huge artificial increase.
 return 0
}
