package server

import (
 "context"
 "crypto/rand"
 "encoding/hex"
 "encoding/json"
 "net/http"
 "os"
 "path/filepath"
 "strings"
 "sync"
 "time"

 "golang.org/x/crypto/bcrypt"
)

type panelIdentity struct { UserID string; Username string; Role string }
type identityContextKey struct{}
type panelUser struct {
 ID string `json:"id"`
 Username string `json:"username"`
 PasswordHash string `json:"passwordHash"`
 Role string `json:"role"`
 Active bool `json:"active"`
 CreatedAt time.Time `json:"createdAt"`
 UpdatedAt time.Time `json:"updatedAt"`
}
type panelUserView struct { ID string `json:"id"`; Username string `json:"username"`; Role string `json:"role"`; Active bool `json:"active"`; CreatedAt time.Time `json:"createdAt"`; UpdatedAt time.Time `json:"updatedAt"` }
type panelUserStore struct {
 mu sync.Mutex
 path string
 users map[string]panelUser
 sessions map[string]panelIdentity
}
func newPanelUserStore(path string) *panelUserStore {
 s:=&panelUserStore{path:path,users:map[string]panelUser{},sessions:map[string]panelIdentity{}}
 _=s.load()
 return s
}
func (s *panelUserStore) load() error {
 s.mu.Lock(); defer s.mu.Unlock()
 raw,err:=os.ReadFile(s.path)
 if os.IsNotExist(err) {
  username,password:=strings.TrimSpace(os.Getenv("PANEL_ADMIN_USER")),os.Getenv("PANEL_ADMIN_PASSWORD")
  if username!=""&&len(password)>=12 {
   hash,e:=bcrypt.GenerateFromPassword([]byte(password),bcrypt.DefaultCost);if e!=nil{return e}
   now:=time.Now().UTC();id:=randomPanelToken()
   s.users[id]=panelUser{ID:id,Username:username,PasswordHash:string(hash),Role:"admin",Active:true,CreatedAt:now,UpdatedAt:now}
   return s.writeLocked()
  }
  return nil
 }
 if err!=nil{return err}
 var rows []panelUser
 if err=json.Unmarshal(raw,&rows);err!=nil{return err}
 for _,u:=range rows{s.users[u.ID]=u}
 return nil
}
func (s *panelUserStore) writeLocked() error {
 rows:=make([]panelUser,0,len(s.users));for _,u:=range s.users{rows=append(rows,u)}
 raw,err:=json.MarshalIndent(rows,"","  ");if err!=nil{return err}
 dir:=filepath.Dir(s.path);if err=os.MkdirAll(dir,0700);err!=nil{return err};if err=os.Chmod(dir,0700);err!=nil{return err}
 tmp,err:=os.CreateTemp(dir,".panel-users-*.tmp");if err!=nil{return err};name:=tmp.Name();ok:=false
 defer func(){_ = tmp.Close();if !ok{_ = os.Remove(name)}}()
 if err=tmp.Chmod(0600);err!=nil{return err};if _,err=tmp.Write(raw);err!=nil{return err};if err=tmp.Sync();err!=nil{return err};if err=tmp.Close();err!=nil{return err};if err=os.Rename(name,s.path);err!=nil{return err};ok=true;return os.Chmod(s.path,0600)
}
func randomPanelToken() string { var b [32]byte;if _,err:=rand.Read(b[:]);err!=nil{return ""};return hex.EncodeToString(b[:]) }
func validPanelRole(role string) bool { return role=="viewer"||role=="operator"||role=="admin" }
func (s *panelUserStore) login(w http.ResponseWriter,r *http.Request) {
 if r.Method!=http.MethodPost{http.Error(w,"method not allowed",405);return}
 r.Body=http.MaxBytesReader(w,r.Body,2048)
 var q struct{Username string `json:"username"`;Password string `json:"password"`}
 if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
 s.mu.Lock();var found panelUser;for _,u:=range s.users{if strings.EqualFold(u.Username,strings.TrimSpace(q.Username)){found=u;break}};s.mu.Unlock()
 if found.ID==""||!found.Active||bcrypt.CompareHashAndPassword([]byte(found.PasswordHash),[]byte(q.Password))!=nil{http.Error(w,"Nieprawidłowy login lub hasło",401);return}
 token:=randomPanelToken();if token==""{http.Error(w,"session creation failed",500);return}
 s.mu.Lock();s.sessions[token]=panelIdentity{UserID:found.ID,Username:found.Username,Role:found.Role};s.mu.Unlock()
 http.SetCookie(w,&http.Cookie{Name:"panel_session",Value:token,Path:"/",HttpOnly:true,SameSite:http.SameSiteStrictMode,Secure:r.TLS!=nil,MaxAge:8*60*60})
 w.Header().Set("Content-Type","application/json");_=json.NewEncoder(w).Encode(map[string]any{"ok":true})
}
func (s *panelUserStore) identity(r *http.Request)(panelIdentity,bool) {
 c,err:=r.Cookie("panel_session");if err!=nil{return panelIdentity{},false}
 s.mu.Lock();defer s.mu.Unlock();id,ok:=s.sessions[c.Value];if !ok{return panelIdentity{},false}
 u,exists:=s.users[id.UserID];if !exists||!u.Active{return panelIdentity{},false};id.Role=u.Role;id.Username=u.Username;return id,true
}
func (s *panelUserStore) middleware(next http.Handler) http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  switch r.URL.Path {
  case "/api/login":s.login(w,r);return
  case "/api/logout":
   if r.Method!="POST"{http.Error(w,"method not allowed",405);return};if c,e:=r.Cookie("panel_session");e==nil{s.mu.Lock();delete(s.sessions,c.Value);s.mu.Unlock()};http.SetCookie(w,&http.Cookie{Name:"panel_session",Value:"",Path:"/",HttpOnly:true,SameSite:http.SameSiteStrictMode,MaxAge:-1});w.WriteHeader(204);return
  case "/api/session":
   if r.Method!="GET"{http.Error(w,"method not allowed",405);return};id,ok:=s.identity(r);if !ok{http.Error(w,"unauthorized",401);return};w.Header().Set("Content-Type","application/json");_=json.NewEncoder(w).Encode(map[string]string{"id":id.UserID,"username":id.Username,"role":id.Role});return
  }
  id,ok:=s.identity(r);if !ok{if strings.HasPrefix(r.URL.Path,"/api/"){http.Error(w,"unauthorized",401);return};http.Redirect(w,r,"/login",http.StatusSeeOther);return}
  if id.Role=="viewer"&&r.Method!=http.MethodGet&&r.Method!=http.MethodHead{http.Error(w,"forbidden: rola Podgląd nie może zmieniać konfiguracji",403);return}
  if strings.HasPrefix(r.URL.Path,"/api/users")&&id.Role!="admin"{http.Error(w,"forbidden",403);return}
  next.ServeHTTP(w,r.WithContext(context.WithValue(r.Context(),identityContextKey{},id)))
 })
}
func (s *panelUserStore) usersHandler() http.Handler {
 return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  w.Header().Set("Content-Type","application/json");w.Header().Set("Cache-Control","no-store")
  if r.URL.Path=="/api/users" {
   if r.Method=="GET"{s.mu.Lock();rows:=make([]panelUserView,0,len(s.users));for _,u:=range s.users{rows=append(rows,panelUserView{ID:u.ID,Username:u.Username,Role:u.Role,Active:u.Active,CreatedAt:u.CreatedAt,UpdatedAt:u.UpdatedAt})};s.mu.Unlock();_=json.NewEncoder(w).Encode(map[string]any{"users":rows});return}
   if r.Method!="POST"{http.Error(w,"method not allowed",405);return}
   r.Body=http.MaxBytesReader(w,r.Body,4096);var q struct{Username string `json:"username"`;Password string `json:"password"`;Role string `json:"role"`}
   if json.NewDecoder(r.Body).Decode(&q)!=nil||len(q.Username)<3||len(q.Username)>32||len(q.Password)<12||!validPanelRole(q.Role){http.Error(w,"login 3–32 znaki, hasło min. 12 znaków i poprawna rola są wymagane",400);return}
   hash,err:=bcrypt.GenerateFromPassword([]byte(q.Password),bcrypt.DefaultCost);if err!=nil{http.Error(w,"password hashing failed",500);return}
   s.mu.Lock();defer s.mu.Unlock();for _,u:=range s.users{if strings.EqualFold(u.Username,q.Username){http.Error(w,"login już istnieje",409);return}}
   now:=time.Now().UTC();id:=randomPanelToken();s.users[id]=panelUser{ID:id,Username:q.Username,PasswordHash:string(hash),Role:q.Role,Active:true,CreatedAt:now,UpdatedAt:now}
   if err=s.writeLocked();err!=nil{delete(s.users,id);http.Error(w,"nie można zapisać konta",500);return};w.WriteHeader(201);_=json.NewEncoder(w).Encode(map[string]any{"created":true,"id":id});return
  }
  if !strings.HasPrefix(r.URL.Path,"/api/users/"){http.NotFound(w,r);return}
  id:=strings.TrimPrefix(r.URL.Path,"/api/users/");if id==""||strings.Contains(id,"/"){http.NotFound(w,r);return}
  if r.Method!="PATCH"{http.Error(w,"method not allowed",405);return}
  r.Body=http.MaxBytesReader(w,r.Body,4096);var q struct{Role string `json:"role"`;Active *bool `json:"active"`;Password string `json:"password"`}
  if json.NewDecoder(r.Body).Decode(&q)!=nil{http.Error(w,"invalid JSON",400);return}
  s.mu.Lock();defer s.mu.Unlock();u,ok:=s.users[id];if !ok{http.NotFound(w,r);return}
  if q.Role!=""&&!validPanelRole(q.Role){http.Error(w,"invalid role",400);return}
  if q.Password!=""&&len(q.Password)<12{http.Error(w,"hasło musi mieć co najmniej 12 znaków",400);return}
  if (q.Role=="viewer"||q.Active!=nil&&!*q.Active)&&u.Role=="admin"&&u.Active {admins:=0;for _,x:=range s.users{if x.Role=="admin"&&x.Active{admins++}};if admins<=1{http.Error(w,"nie można zablokować ani zdegradować ostatniego aktywnego administratora",409);return}}
  if q.Role!=""{u.Role=q.Role};if q.Active!=nil{u.Active=*q.Active};if q.Password!=""{hash,err:=bcrypt.GenerateFromPassword([]byte(q.Password),bcrypt.DefaultCost);if err!=nil{http.Error(w,"password hashing failed",500);return};u.PasswordHash=string(hash)}
  u.UpdatedAt=time.Now().UTC();s.users[id]=u
  if err:=s.writeLocked();err!=nil{http.Error(w,"nie można zapisać zmian",500);return}
  if !u.Active{for token,identity:=range s.sessions{if identity.UserID==id{delete(s.sessions,token)}}}
  _=json.NewEncoder(w).Encode(map[string]any{"saved":true})
 })
}
func panelLoginPageHandler(w http.ResponseWriter,r *http.Request) {
 w.Header().Set("Content-Type","text/html; charset=utf-8");w.Header().Set("Cache-Control","no-store")
 _,_=w.Write([]byte(`<!doctype html><html lang="pl"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Logowanie · Allied Network</title><style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0b1018;color:#eaf1f9;font:15px system-ui}.box{width:min(380px,calc(100% - 40px));padding:30px;background:#111a26;border:1px solid #263548;border-radius:18px}label{display:block;margin:15px 0;color:#9aaabd;font-size:12px}input{display:block;box-sizing:border-box;width:100%;margin-top:6px;padding:12px;border-radius:9px;border:1px solid #30445c;background:#0b1420;color:white}button{width:100%;padding:12px;border:0;border-radius:9px;background:#3988ed;color:white;font-weight:700}p{color:#9aaabd;font-size:12px}#error{color:#ff8997}</style><main class="box"><h2>Allied Network</h2><p>Zaloguj się do lokalnego panelu zarządzania przełącznikiem.</p><form id="f"><label>Login<input name="username" autocomplete="username" required></label><label>Hasło<input name="password" type="password" autocomplete="current-password" required></label><button>Zaloguj</button><p id="error" role="alert"></p></form></main><script>document.getElementById('f').addEventListener('submit',async e=>{e.preventDefault();const f=e.currentTarget;const r=await fetch('/api/login',{method:'POST',headers:{'Content-Type':'application/json'},credentials:'same-origin',body:JSON.stringify({username:f.username.value,password:f.password.value})});if(r.ok)location.href='/';else document.getElementById('error').textContent=await r.text();});</script></html>`))
}
