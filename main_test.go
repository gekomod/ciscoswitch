package main

import (
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestBasicAuthRequiresCredentials(t *testing.T) {
 called := false
 h := basicAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusNoContent) }), "admin", "a-very-long-test-password")
 for _, tc := range []struct{name,user,pass string; want int}{
  {"missing","","",http.StatusUnauthorized},
  {"wrong","admin","wrong-password",http.StatusUnauthorized},
  {"valid","admin","a-very-long-test-password",http.StatusNoContent},
 } {
  t.Run(tc.name,func(t *testing.T){
   called=false
   req:=httptest.NewRequest(http.MethodGet,"/",nil)
   if tc.user!="" { req.SetBasicAuth(tc.user,tc.pass) }
   rec:=httptest.NewRecorder();h.ServeHTTP(rec,req)
   if rec.Code!=tc.want {t.Fatalf("got %d want %d",rec.Code,tc.want)}
   if called!=(tc.want==http.StatusNoContent) {t.Fatalf("handler called=%v",called)}
  })
 }
}
func TestBasicAuthRejectsForeignOriginOnWrites(t *testing.T) {
 h:=basicAuth(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusNoContent)}),"admin","a-very-long-test-password")
 req:=httptest.NewRequest(http.MethodPost,"http://panel.local/api/vlan",nil)
 req.Host="panel.local";req.Header.Set("Origin","http://evil.example");req.SetBasicAuth("admin","a-very-long-test-password")
 rec:=httptest.NewRecorder();h.ServeHTTP(rec,req)
 if rec.Code!=http.StatusForbidden {t.Fatalf("got %d want %d",rec.Code,http.StatusForbidden)}
}


func TestAuditLogStoresMetadataWithoutRequestBody(t *testing.T) {
 dir:=t.TempDir()
 path:=filepath.Join(dir,"audit.jsonl")
 t.Setenv("PANEL_USER","operator")
 h:=auditLog(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusCreated)}),path)
 req:=httptest.NewRequest(http.MethodPost,"/api/snmp/trap-host",strings.NewReader(`{"community":"TOP-SECRET"}`))
 rec:=httptest.NewRecorder()
 h.ServeHTTP(rec,req)
 data,err:=os.ReadFile(path);if err!=nil{t.Fatal(err)}
 line:=string(data)
 if !strings.Contains(line,`"username":"operator"`) || !strings.Contains(line,`"status":201`) || !strings.Contains(line,`"/api/snmp/trap-host"`){t.Fatalf("missing audit metadata: %s",line)}
 if strings.Contains(line,"TOP-SECRET"){t.Fatal("audit log contains request secret")}
 info,err:=os.Stat(path);if err!=nil{t.Fatal(err)}
 if info.Mode().Perm()!=0600{t.Fatalf("audit file mode=%o",info.Mode().Perm())}
}

func TestAuditLogIgnoresReadOnlyAndNonAPIRequests(t *testing.T) {
 path:=filepath.Join(t.TempDir(),"audit.jsonl")
 h:=auditLog(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusOK)}),path)
 for _,target:=range []string{"/api/status","/"}{
  h.ServeHTTP(httptest.NewRecorder(),httptest.NewRequest(http.MethodGet,target,nil))
 }
 if _,err:=os.Stat(path);!os.IsNotExist(err){t.Fatalf("unexpected audit file, err=%v",err)}
}
