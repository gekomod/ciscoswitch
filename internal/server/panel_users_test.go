package server

import (
 "net/http"
 "net/http/httptest"
 "path/filepath"
 "strings"
 "testing"
)

func TestPanelMiddlewareRequiresLoginAndBlocksViewerWrites(t *testing.T) {
 store:=newPanelUserStore(filepath.Join(t.TempDir(),"users.json"))
 next:=http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusNoContent)})
 handler:=store.middleware(next)
 unauth:=httptest.NewRecorder()
 handler.ServeHTTP(unauth,httptest.NewRequest(http.MethodGet,"/api/status",nil))
 if unauth.Code!=http.StatusUnauthorized { t.Fatalf("unauthenticated request status=%d",unauth.Code) }

 store.mu.Lock()
 store.sessions["viewer-token"]=panelIdentity{UserID:"viewer-id",Username:"viewer",Role:"viewer"}
 store.users["viewer-id"]=panelUser{ID:"viewer-id",Username:"viewer",Role:"viewer",Active:true}
 store.mu.Unlock()
 write:=httptest.NewRecorder()
 req:=httptest.NewRequest(http.MethodPost,"/api/port/settings",strings.NewReader("{}"))
 req.AddCookie(&http.Cookie{Name:"panel_session",Value:"viewer-token"})
 handler.ServeHTTP(write,req)
 if write.Code!=http.StatusForbidden { t.Fatalf("viewer write status=%d body=%s",write.Code,write.Body.String()) }

 read:=httptest.NewRecorder()
 req=httptest.NewRequest(http.MethodGet,"/api/status",nil)
 req.AddCookie(&http.Cookie{Name:"panel_session",Value:"viewer-token"})
 handler.ServeHTTP(read,req)
 if read.Code!=http.StatusNoContent { t.Fatalf("viewer read status=%d",read.Code) }
}

func TestPanelUsersHandlerRejectsWeakPasswordAndDuplicateLogin(t *testing.T) {
 store:=newPanelUserStore(filepath.Join(t.TempDir(),"users.json"))
 handler:=store.usersHandler()
 weak:=httptest.NewRecorder()
 handler.ServeHTTP(weak,httptest.NewRequest(http.MethodPost,"/api/users",strings.NewReader(`{"username":"operator1","password":"short","role":"operator"}`)))
 if weak.Code!=http.StatusBadRequest { t.Fatalf("weak password status=%d",weak.Code) }

 store.mu.Lock()
 store.users["existing"]=panelUser{ID:"existing",Username:"operator1",Role:"operator",Active:true}
 store.mu.Unlock()
 dup:=httptest.NewRecorder()
 handler.ServeHTTP(dup,httptest.NewRequest(http.MethodPost,"/api/users",strings.NewReader(`{"username":"OPERATOR1","password":"long-enough-password","role":"operator"}`)))
 if dup.Code!=http.StatusConflict { t.Fatalf("duplicate username status=%d",dup.Code) }
}
