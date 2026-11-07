package main

import (
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestSessionAuthLoginAndProtectedRoutes(t *testing.T) {
 called:=false
 mux:=http.NewServeMux()
 mux.HandleFunc("/",func(w http.ResponseWriter,r *http.Request){called=true;w.WriteHeader(http.StatusNoContent)})
 h:=sessionAuth(mux,"admin","password8")
 req:=httptest.NewRequest(http.MethodGet,"/",nil)
 rec:=httptest.NewRecorder();h.ServeHTTP(rec,req)
 if rec.Code!=http.StatusSeeOther || rec.Header().Get("Location")!="/login" || called {t.Fatalf("unauthenticated request: status=%d location=%q called=%v",rec.Code,rec.Header().Get("Location"),called)}

 req=httptest.NewRequest(http.MethodPost,"/api/login",strings.NewReader(`{"username":"admin","password":"wrong"}`))
 req.Header.Set("Origin","http://panel.local");req.Host="panel.local"
 rec=httptest.NewRecorder();h.ServeHTTP(rec,req)
 if rec.Code!=http.StatusUnauthorized {t.Fatalf("wrong password status=%d",rec.Code)}

 req=httptest.NewRequest(http.MethodPost,"/api/login",strings.NewReader(`{"username":"admin","password":"password8"}`))
 req.Header.Set("Origin","http://panel.local");req.Host="panel.local"
 rec=httptest.NewRecorder();h.ServeHTTP(rec,req)
 if rec.Code!=http.StatusOK {t.Fatalf("login status=%d body=%s",rec.Code,rec.Body.String())}
 var payload map[string]any
 if err:=json.Unmarshal(rec.Body.Bytes(),&payload);err!=nil||payload["ok"]!=true {t.Fatalf("invalid login response: %s",rec.Body.String())}
 cookies:=rec.Result().Cookies()
 if len(cookies)!=1 || cookies[0].Name!=panelSessionCookie || !cookies[0].HttpOnly || cookies[0].SameSite!=http.SameSiteStrictMode {t.Fatalf("unsafe session cookie: %#v",cookies)}
 req=httptest.NewRequest(http.MethodGet,"/",nil);req.AddCookie(cookies[0])
 rec=httptest.NewRecorder();h.ServeHTTP(rec,req)
 if rec.Code!=http.StatusNoContent || !called {t.Fatalf("authenticated request: status=%d called=%v",rec.Code,called)}

 req=httptest.NewRequest(http.MethodPost,"/api/logout",nil);req.AddCookie(cookies[0]);req.Host="panel.local";req.Header.Set("Origin","http://panel.local")
 rec=httptest.NewRecorder();h.ServeHTTP(rec,req)
 if rec.Code!=http.StatusOK {t.Fatalf("logout status=%d",rec.Code)}
 req=httptest.NewRequest(http.MethodGet,"/",nil);req.AddCookie(cookies[0])
 rec=httptest.NewRecorder();h.ServeHTTP(rec,req)
 if rec.Code!=http.StatusSeeOther {t.Fatalf("revoked session still accepted: %d",rec.Code)}
}

func TestSessionRolesEnforceReadOnlyAndOperatorPermissions(t *testing.T) {
 cases:=[]struct{name,role,method,path string; want int}{
  {"viewer blocks config change","viewer",http.MethodPost,"/api/port/settings",http.StatusForbidden},
  {"viewer blocks raw CLI","viewer",http.MethodPost,"/api/command",http.StatusForbidden},
  {"viewer blocks config export","viewer",http.MethodGet,"/api/config/export",http.StatusForbidden},
  {"viewer blocks backup download","viewer",http.MethodGet,"/api/config/backups/download",http.StatusForbidden},
  {"viewer allows preview","viewer",http.MethodPost,"/api/vlan/preview",http.StatusNoContent},
  {"operator allows config change","operator",http.MethodPost,"/api/port/settings",http.StatusNoContent},
  {"operator blocks user administration","operator",http.MethodPost,"/api/users",http.StatusForbidden},
  {"admin allows user administration","admin",http.MethodPost,"/api/users",http.StatusNoContent},
 }
 for _,tc:=range cases { t.Run(tc.name,func(t *testing.T){
  mux:=http.NewServeMux()
  for _,path:=range []string{"/api/command","/api/vlan/preview","/api/port/settings","/api/users"} { mux.HandleFunc(path,func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusNoContent)}) }
  h:=sessionAuthWithRole(mux,"admin","password8",tc.role)
  login:=httptest.NewRequest(http.MethodPost,"/api/login",strings.NewReader("{\"username\":\"admin\",\"password\":\"password8\"}"))
  login.Host="panel.local";login.Header.Set("Origin","http://panel.local")
  loginRec:=httptest.NewRecorder();h.ServeHTTP(loginRec,login)
  if loginRec.Code!=http.StatusOK {t.Fatalf("login status=%d body=%s",loginRec.Code,loginRec.Body.String())}
  cookie:=loginRec.Result().Cookies()[0]
  req:=httptest.NewRequest(tc.method,tc.path,nil);req.AddCookie(cookie);req.Host="panel.local"
  if tc.method!=http.MethodGet {req.Header.Set("Origin","http://panel.local")}
  rec:=httptest.NewRecorder();h.ServeHTTP(rec,req)
  if rec.Code!=tc.want {t.Fatalf("status=%d want=%d body=%s",rec.Code,tc.want,rec.Body.String())}
 }) }
}

func TestSessionEndpointReturnsIdentityAndRole(t *testing.T) {
 h:=sessionAuthWithRole(http.NewServeMux(),"operator","password8","operator")
 login:=httptest.NewRequest(http.MethodPost,"/api/login",strings.NewReader("{\"username\":\"operator\",\"password\":\"password8\"}"))
 login.Host="panel.local";login.Header.Set("Origin","http://panel.local")
 loginRec:=httptest.NewRecorder();h.ServeHTTP(loginRec,login)
 cookie:=loginRec.Result().Cookies()[0]
 req:=httptest.NewRequest(http.MethodGet,"/api/session",nil);req.AddCookie(cookie)
 rec:=httptest.NewRecorder();h.ServeHTTP(rec,req)
 if rec.Code!=http.StatusOK || !strings.Contains(rec.Body.String(),"\"username\":\"operator\"") || !strings.Contains(rec.Body.String(),"\"role\":\"operator\"") {t.Fatalf("identity response status=%d body=%s",rec.Code,rec.Body.String())}
}
func TestSessionAuthRejectsForeignOrigin(t *testing.T) {
 h:=sessionAuth(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(http.StatusNoContent)}),"admin","password8")
 req:=httptest.NewRequest(http.MethodPost,"/api/login",strings.NewReader(`{"username":"admin","password":"password8"}`))
 req.Host="panel.local";req.Header.Set("Origin","http://evil.example")
 rec:=httptest.NewRecorder();h.ServeHTTP(rec,req)
 if rec.Code!=http.StatusForbidden {t.Fatalf("foreign origin status=%d",rec.Code)}
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

func TestPanelUserStoreBootstrapsSecurelyAndAuthenticates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "users.json")
	store, err := openPanelUserStore(path, "rootadmin", "bootstrap-password", "admin")
	if err != nil { t.Fatal(err) }
	users := store.list()
	if len(users) != 1 || users[0].Username != "rootadmin" || users[0].Role != "admin" || !users[0].Active { t.Fatalf("unexpected bootstrap users: %#v", users) }
	if _, ok := store.authenticate("ROOTADMIN", "bootstrap-password"); !ok { t.Fatal("bootstrap account did not authenticate") }
	data, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	if strings.Contains(string(data), "bootstrap-password") || !strings.Contains(string(data), "$2") { t.Fatal("password is not stored as a bcrypt hash") }
	info, err := os.Stat(path)
	if err != nil { t.Fatal(err) }
	if info.Mode().Perm() != 0600 { t.Fatalf("users file mode=%o", info.Mode().Perm()) }
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil { t.Fatal(err) }
	if dirInfo.Mode().Perm() != 0700 { t.Fatalf("users directory mode=%o", dirInfo.Mode().Perm()) }
}

func TestPanelUserStoreCreatesUpdatesAndProtectsLastAdmin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	store, err := openPanelUserStore(path, "rootadmin", "bootstrap-password", "admin")
	if err != nil { t.Fatal(err) }
	created, err := store.create("network.operator", "a-long-new-password", "operator")
	if err != nil { t.Fatal(err) }
	if created.Role != "operator" || !created.Active { t.Fatalf("unexpected created user: %#v", created) }
	if _, ok := store.authenticate("network.operator", "a-long-new-password"); !ok { t.Fatal("new user did not authenticate") }
	disabled := false
	if _, err := store.update(created.ID, nil, &disabled, nil); err != nil { t.Fatal(err) }
	if _, ok := store.authenticate("network.operator", "a-long-new-password"); ok { t.Fatal("disabled user authenticated") }
	demote := "viewer"
	var admin panelUserPublic
	for _, candidate := range store.list() {
		if candidate.Username == "rootadmin" { admin = candidate }
	}
	if admin.ID == "" { t.Fatal("bootstrap administrator missing from list") }
	if _, err := store.update(admin.ID, &demote, nil, nil); err == nil || !strings.Contains(err.Error(), "ostatniego aktywnego administratora") { t.Fatalf("last admin demotion was not blocked: %v", err) }
	if _, err := store.create("bad user", "a-long-new-password", "viewer"); err == nil { t.Fatal("invalid username accepted") }
	if _, err := store.create("second.user", "short", "viewer"); err == nil { t.Fatal("short password accepted") }
}

func TestPanelUserStoreExistingRegistryDoesNotNeedBootstrapEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	if _, err := openPanelUserStore(path, "rootadmin", "bootstrap-password", "admin"); err != nil { t.Fatal(err) }
	store, err := openPanelUserStore(path, "", "", "")
	if err != nil { t.Fatalf("existing registry should load without bootstrap env: %v", err) }
	if len(store.list()) != 1 { t.Fatalf("existing registry lost accounts: %#v", store.list()) }
}

func TestPanelUserStoreDoesNotOverwriteCorruptOrEmptyRegistry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil { t.Fatal(err) }
	if _, err := openPanelUserStore(path, "rootadmin", "bootstrap-password", "admin"); err == nil { t.Fatal("empty registry was silently bootstrapped") }
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil { t.Fatal(err) }
	if _, err := openPanelUserStore(path, "rootadmin", "bootstrap-password", "admin"); err == nil { t.Fatal("corrupt registry was silently overwritten") }
}

func TestMultiUserLoginAndAdminOnlyUserAPI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	store, err := openPanelUserStore(path, "rootadmin", "bootstrap-password", "admin")
	if err != nil { t.Fatal(err) }
	if _, err := store.create("read.only", "read-only-password", "viewer"); err != nil { t.Fatal(err) }
	mux := http.NewServeMux()
	registerPanelUserRoutes(mux, store)
	mux.HandleFunc("/api/private", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := sessionAuthWithUsers(mux, store)
	login := func(username, password string) *http.Cookie {
		req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"`+username+`","password":"`+password+`"}`))
		req.Header.Set("Origin", "http://panel.local"); req.Host = "panel.local"
		rec := httptest.NewRecorder(); h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK { t.Fatalf("login for %s failed: %d %s", username, rec.Code, rec.Body.String()) }
		return rec.Result().Cookies()[0]
	}
	viewerCookie := login("read.only", "read-only-password")
	req := httptest.NewRequest(http.MethodGet, "/api/users", nil); req.AddCookie(viewerCookie)
	rec := httptest.NewRecorder(); h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden { t.Fatalf("viewer read users status=%d", rec.Code) }
	adminCookie := login("rootadmin", "bootstrap-password")
	req = httptest.NewRequest(http.MethodGet, "/api/users", nil); req.AddCookie(adminCookie)
	rec = httptest.NewRecorder(); h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "passwordHash") { t.Fatalf("admin list response leaked hash or failed: %d %s", rec.Code, rec.Body.String()) }
	req = httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(`{"username":"new.operator","password":"another-long-password","role":"operator"}`))
	req.AddCookie(adminCookie); req.Header.Set("Origin", "http://panel.local"); req.Host = "panel.local"
	rec = httptest.NewRecorder(); h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated { t.Fatalf("admin create user status=%d body=%s", rec.Code, rec.Body.String()) }
	if strings.Contains(rec.Body.String(), "passwordHash") { t.Fatal("create response leaked password hash") }
}

func TestMultiUserSessionIsRevokedAfterDisableOrRoleChange(t *testing.T) {
	store, err := openPanelUserStore(filepath.Join(t.TempDir(), "users.json"), "rootadmin", "bootstrap-password", "admin")
	if err != nil { t.Fatal(err) }
	created, err := store.create("ops.account", "operator-long-password", "operator")
	if err != nil { t.Fatal(err) }
	mux := http.NewServeMux()
	mux.HandleFunc("/api/private", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := sessionAuthWithUsers(mux, store)
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"ops.account","password":"operator-long-password"}`))
	req.Header.Set("Origin", "http://panel.local"); req.Host = "panel.local"
	rec := httptest.NewRecorder(); h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK { t.Fatalf("login status=%d", rec.Code) }
	cookie := rec.Result().Cookies()[0]
	req = httptest.NewRequest(http.MethodGet, "/api/private", nil); req.AddCookie(cookie)
	rec = httptest.NewRecorder(); h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent { t.Fatalf("initial session status=%d", rec.Code) }
	disabled := false
	if _, err := store.update(created.ID, nil, &disabled, nil); err != nil { t.Fatal(err) }
	req = httptest.NewRequest(http.MethodGet, "/api/private", nil); req.AddCookie(cookie)
	rec = httptest.NewRecorder(); h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized { t.Fatalf("disabled account session status=%d", rec.Code) }
}

func TestPanelUserPasswordResetRevokesExistingSession(t *testing.T) {
	store, err := openPanelUserStore(filepath.Join(t.TempDir(), "users.json"), "rootadmin", "bootstrap-password", "admin")
	if err != nil { t.Fatal(err) }
	created, err := store.create("ops.reset", "original-long-password", "operator")
	if err != nil { t.Fatal(err) }
	mux := http.NewServeMux()
	mux.HandleFunc("/api/private", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := sessionAuthWithUsers(mux, store)
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"ops.reset","password":"original-long-password"}`))
	req.Header.Set("Origin", "http://panel.local"); req.Host = "panel.local"
	rec := httptest.NewRecorder(); h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK { t.Fatalf("login status=%d", rec.Code) }
	cookie := rec.Result().Cookies()[0]
	newPassword := "replacement-long-password"
	if _, err := store.update(created.ID, nil, nil, &newPassword); err != nil { t.Fatal(err) }
	req = httptest.NewRequest(http.MethodGet, "/api/private", nil); req.AddCookie(cookie)
	rec = httptest.NewRecorder(); h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized { t.Fatalf("session survived password reset: status=%d", rec.Code) }
	if _, ok := store.authenticate("ops.reset", newPassword); !ok { t.Fatal("new password was not applied") }
}
