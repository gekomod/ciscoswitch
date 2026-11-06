package server
import("encoding/json";"net/http";"net/http/httptest";"path/filepath";"strings";"testing")
func TestMonitorHistoryPersistsEventsAndSamples(t *testing.T){
 h:=monitorHistoryHandler(filepath.Join(t.TempDir(),"data","monitor.jsonl"))
 for _,v:=range []monitorRecord{{Kind:"event",Message:"port1.0.1: link DOWN",Type:"danger"},{Kind:"sample",RX:1200,TX:800},{Kind:"port",Port:"port1.0.1",State:"down"},{Kind:"counter",Port:"port1.0.1",ErrorDelta:3,DiscardDelta:2,ErrorTotal:10,DiscardTotal:5}}{
  b,_:=json.Marshal(v);w:=httptest.NewRecorder();h.ServeHTTP(w,httptest.NewRequest(http.MethodPost,"/api/monitor/history",strings.NewReader(string(b))))
  if w.Code!=http.StatusNoContent{t.Fatalf("POST status=%d body=%s",w.Code,w.Body.String())}
 }
 w:=httptest.NewRecorder();h.ServeHTTP(w,httptest.NewRequest(http.MethodGet,"/api/monitor/history",nil));var got monitorHistoryResponse
 if w.Code!=http.StatusOK{t.Fatalf("GET status=%d",w.Code)};if err:=json.Unmarshal(w.Body.Bytes(),&got);err!=nil{t.Fatal(err)}
 if len(got.Events)!=1||got.Events[0].Message!="port1.0.1: link DOWN"{t.Fatalf("events=%+v",got.Events)}
 if len(got.Samples)!=1||got.Samples[0].RX!=1200||got.Samples[0].TX!=800{t.Fatalf("samples=%+v",got.Samples)}
 if len(got.PortEvents)!=1||got.PortEvents[0].Port!="port1.0.1"||got.PortEvents[0].State!="down"{t.Fatalf("port events=%+v",got.PortEvents)}
 if len(got.CounterEvents)!=1||got.CounterEvents[0].ErrorDelta!=3||got.CounterEvents[0].DiscardDelta!=2{t.Fatalf("counter events=%+v",got.CounterEvents)}
}
func TestMonitorHistoryRejectsInvalidRecords(t *testing.T){
 h:=monitorHistoryHandler(filepath.Join(t.TempDir(),"monitor.jsonl"))
 for _,p:=range []string{`{"kind":"event","message":"","type":"danger"}`,`{"kind":"sample","rx":-1,"tx":2}`,`{"kind":"unknown"}`,`{"kind":"port","port":"bad","state":"down"}`,`{"kind":"port","port":"port1.0.1","state":"broken"}`,`{"kind":"counter","port":"port1.0.1","errorDelta":0,"discardDelta":0}`}{
  w:=httptest.NewRecorder();h.ServeHTTP(w,httptest.NewRequest(http.MethodPost,"/api/monitor/history",strings.NewReader(p)));if w.Code!=400{t.Fatalf("payload=%s status=%d",p,w.Code)}
 }
}
func TestMonitorHistoryMethodNotAllowed(t *testing.T){
 h:=monitorHistoryHandler(filepath.Join(t.TempDir(),"monitor.jsonl"));w:=httptest.NewRecorder();h.ServeHTTP(w,httptest.NewRequest(http.MethodPut,"/api/monitor/history",nil));if w.Code!=405{t.Fatalf("status=%d",w.Code)}
}
