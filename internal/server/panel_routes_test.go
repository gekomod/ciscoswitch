package server

import (
 "net/http"
 "net/http/httptest"
 "testing"

)

func TestRegisterMountsLoginAndAuthenticatedPanelRoutes(t *testing.T) {
 mux:=http.NewServeMux()
 Register(mux,nil)
 login:=httptest.NewRecorder()
 mux.ServeHTTP(login,httptest.NewRequest(http.MethodGet,"/login",nil))
 if login.Code!=http.StatusOK { t.Fatalf("login page status=%d",login.Code) }
 root:=httptest.NewRecorder()
 mux.ServeHTTP(root,httptest.NewRequest(http.MethodGet,"/",nil))
 if root.Code!=http.StatusSeeOther { t.Fatalf("unauthenticated root status=%d",root.Code) }
 if got:=root.Header().Get("Location");got!="/login" { t.Fatalf("root redirect=%q",got) }
 api:=httptest.NewRecorder()
 mux.ServeHTTP(api,httptest.NewRequest(http.MethodGet,"/api/status",nil))
 if api.Code!=http.StatusUnauthorized { t.Fatalf("unauthenticated API status=%d",api.Code) }
}
