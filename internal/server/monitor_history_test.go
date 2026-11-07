package server
import("bufio";"context";"encoding/json";"net/http";"net/http/httptest";"path/filepath";"strings";"testing";"time")
func TestMonitorHistoryPersistsEventsAndSamples(t *testing.T){
 h:=monitorHistoryHandler(filepath.Join(t.TempDir(),"data","monitor.jsonl"))
 for _,v:=range []monitorRecord{{Kind:"event",Message:"port1.0.1: link DOWN",Type:"danger"},{Kind:"sample",RX:1200,TX:800},{Kind:"port_sample",Ports:map[string]monitorPortRate{"port1.0.1":{RX:1250,TX:640}}},{Kind:"port",Port:"port1.0.1",State:"down"},{Kind:"counter",Port:"port1.0.1",ErrorRXDelta:3,ErrorTXDelta:1,DiscardRXDelta:2,DiscardTXDelta:1,ErrorRXTotal:10,ErrorTXTotal:4,DiscardRXTotal:5,DiscardTXTotal:2}}{
  b,_:=json.Marshal(v);w:=httptest.NewRecorder();h.ServeHTTP(w,httptest.NewRequest(http.MethodPost,"/api/monitor/history",strings.NewReader(string(b))))
  if w.Code!=http.StatusNoContent{t.Fatalf("POST status=%d body=%s",w.Code,w.Body.String())}
 }
 w:=httptest.NewRecorder();h.ServeHTTP(w,httptest.NewRequest(http.MethodGet,"/api/monitor/history",nil));var got monitorHistoryResponse
 if w.Code!=http.StatusOK{t.Fatalf("GET status=%d",w.Code)};if err:=json.Unmarshal(w.Body.Bytes(),&got);err!=nil{t.Fatal(err)}
 if len(got.Events)!=1||got.Events[0].Message!="port1.0.1: link DOWN"{t.Fatalf("events=%+v",got.Events)}
 if len(got.Samples)!=1||got.Samples[0].RX!=1200||got.Samples[0].TX!=800{t.Fatalf("samples=%+v",got.Samples)}
 if len(got.PortEvents)!=1||got.PortEvents[0].Port!="port1.0.1"||got.PortEvents[0].State!="down"{t.Fatalf("port events=%+v",got.PortEvents)}
 if len(got.CounterEvents)!=1||got.CounterEvents[0].ErrorRXDelta!=3||got.CounterEvents[0].ErrorTXDelta!=1||got.CounterEvents[0].DiscardRXDelta!=2||got.CounterEvents[0].DiscardTXDelta!=1{t.Fatalf("counter events=%+v",got.CounterEvents)}
 if len(got.PortSamples)!=1||got.PortSamples[0].Ports["port1.0.1"].RX!=1250||got.PortSamples[0].Ports["port1.0.1"].TX!=640{t.Fatalf("port samples=%+v",got.PortSamples)}
}
func TestMonitorHistoryRejectsInvalidRecords(t *testing.T){
 h:=monitorHistoryHandler(filepath.Join(t.TempDir(),"monitor.jsonl"))
 for _,p:=range []string{`{"kind":"event","message":"","type":"danger"}`,`{"kind":"sample","rx":-1,"tx":2}`,`{"kind":"unknown"}`,`{"kind":"port","port":"bad","state":"down"}`,`{"kind":"port","port":"port1.0.1","state":"broken"}`,`{"kind":"counter","port":"port1.0.1","errorRxDelta":0,"errorTxDelta":0,"discardRxDelta":0,"discardTxDelta":0}`,`{"kind":"port_sample","ports":{}}`,`{"kind":"port_sample","ports":{"invalid":{"rx":1,"tx":2}}}`,`{"kind":"port_sample","ports":{"port1.0.1":{"rx":-1,"tx":2}}}`}{
  w:=httptest.NewRecorder();h.ServeHTTP(w,httptest.NewRequest(http.MethodPost,"/api/monitor/history",strings.NewReader(p)));if w.Code!=400{t.Fatalf("payload=%s status=%d",p,w.Code)}
 }
}
func TestMonitorHistoryMethodNotAllowed(t *testing.T){
 h:=monitorHistoryHandler(filepath.Join(t.TempDir(),"monitor.jsonl"));w:=httptest.NewRecorder();h.ServeHTTP(w,httptest.NewRequest(http.MethodPut,"/api/monitor/history",nil));if w.Code!=405{t.Fatalf("status=%d",w.Code)}
}

func TestMonitorEventHubPublishesRecords(t *testing.T) {
	hub := &monitorEventHub{subscribers: make(map[uint64]chan monitorRecord)}
	id, events := hub.subscribe()
	defer hub.unsubscribe(id)
	want := monitorRecord{Kind:"event",Message:"Port 1 link down",Type:"danger"}
	hub.publish(want)
	select {
	case got := <-events:
		if got.Kind != want.Kind || got.Message != want.Message || got.Type != want.Type { t.Fatalf("record=%+v want=%+v", got, want) }
	case <-time.After(time.Second):
		t.Fatal("monitor event was not delivered")
	}
}

func TestMonitorStreamDeliversLiveEvents(t *testing.T) {
	server := httptest.NewServer(monitorStreamHandler())
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil { t.Fatal(err) }
	resp, err := http.DefaultClient.Do(req)
	if err != nil { t.Fatal(err) }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") { t.Fatalf("status=%d content-type=%q", resp.StatusCode, resp.Header.Get("Content-Type")) }
	reader := bufio.NewReader(resp.Body)
	ready := false
	for i:=0;i<3;i++ { line,err:=reader.ReadString('\n'); if err!=nil { t.Fatal(err) }; if strings.Contains(line,"event: ready") { ready=true } }
	if !ready { t.Fatal("SSE ready event was not sent") }
	sharedMonitorEventHub.publish(monitorRecord{Kind:"event",Message:"Live link restored",Type:"success",Timestamp:time.Now().UTC()})
	received := false
	deadline := time.After(2*time.Second)
	for !received {
		select {
		case <-deadline: t.Fatal("live SSE event was not delivered")
		default:
			line,err:=reader.ReadString('\n')
			if err!=nil { t.Fatal(err) }
			if strings.Contains(line,"Live link restored") { received=true }
		}
	}
}
