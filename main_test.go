package main

import (
 "net/http"
 "net/http/httptest"
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
