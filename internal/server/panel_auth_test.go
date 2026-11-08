package server

import (
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "path/filepath"
 "strings"
 "testing"
)

func TestPanelLoginCreatesSessionAndReportsRole(t *testing.T) {
 t.Setenv("PANEL_ADMIN_USER","rootadmin")
 t.Setenv("PANEL_ADMIN_PASSWORD","a-strong-local-panel-password")
 store:=newPanelUserStore(filepath.Join(t.TempDir(),"users.json"))
 handler:=store.middleware(http.NotFoundHandler())
 login:=httptest.NewRecorder()
 req:=httptest.NewRequest(http.MethodPost,"/api/login",strings.NewReader(`{"username":"rootadmin","password":"a-strong-local-panel-password"}`))
 handler.ServeHTTP(login,req)
 if login.Code!=http.StatusOK { t.Fatalf("login status=%d body=%s",login.Code,login.Body.String()) }
 cookies:=login.Result().Cookies()
 if len(cookies)==0||cookies[0].Name!="panel_session" { t.Fatal("session cookie was not issued") }
 session:=httptest.NewRecorder()
 req=httptest.NewRequest(http.MethodGet,"/api/session",nil);req.AddCookie(cookies[0])
 handler.ServeHTTP(session,req)
 if session.Code!=http.StatusOK { t.Fatalf("session status=%d body=%s",session.Code,session.Body.String()) }
 var identity map[string]string
 if err:=json.Unmarshal(session.Body.Bytes(),&identity);err!=nil{t.Fatal(err)}
 if identity["username"]!="rootadmin"||identity["role"]!="admin" { t.Fatalf("unexpected identity: %+v",identity) }
}

func TestPanelUserPasswordHashPersistsButIsNotExposed(t *testing.T) {
 t.Setenv("PANEL_ADMIN_USER","persistadmin")
 t.Setenv("PANEL_ADMIN_PASSWORD","another-strong-panel-password")
 path:=filepath.Join(t.TempDir(),"users.json")
 first:=newPanelUserStore(path)
 if len(first.users)!=1 { t.Fatalf("bootstrap users=%d",len(first.users)) }
 second:=newPanelUserStore(path)
 var user panelUser
 for _,u:=range second.users{user=u}
 if user.PasswordHash=="" { t.Fatal("password hash was not persisted") }
 handler:=second.usersHandler()
 rec:=httptest.NewRecorder()
 handler.ServeHTTP(rec,httptest.NewRequest(http.MethodGet,"/api/users",nil))
 if strings.Contains(rec.Body.String(),"passwordHash")||strings.Contains(rec.Body.String(),user.PasswordHash){t.Fatal("password hash leaked in users response")}
}
