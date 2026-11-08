package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const panelUserPasswordMin = 12

type panelUserRecord struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"passwordHash"`
	Role         string    `json:"role"`
	Active       bool      `json:"active"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type panelUserPublic struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type panelUserStore struct {
	mu    sync.RWMutex
	path  string
	users map[string]panelUserRecord
}

var panelUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,31}$`)

func openPanelUserStore(path, bootstrapUsername, bootstrapPassword, bootstrapRole string) (*panelUserStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("PANEL_USERS_PATH nie może być pusty")
	}
	store := &panelUserStore{path: path, users: make(map[string]panelUserRecord)}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &store.users); err != nil {
			return nil, fmt.Errorf("nie można odczytać rejestru użytkowników: %w", err)
		}
		if len(store.users) == 0 {
			return nil, errors.New("rejestr użytkowników jest pusty; odmowa automatycznego nadpisania")
		}
		activeAdmins := 0
		seenUsernames := make(map[string]bool, len(store.users))
		for id, user := range store.users {
			if id == "" || user.ID != id || !panelUsernamePattern.MatchString(user.Username) || !validPanelRole(user.Role) || user.PasswordHash == "" {
				return nil, fmt.Errorf("nieprawidłowy wpis użytkownika %q", id)
			}
			key := strings.ToLower(user.Username)
			if seenUsernames[key] {
				return nil, fmt.Errorf("powtórzony login użytkownika %q", user.Username)
			}
			seenUsernames[key] = true
			if _, err := bcrypt.Cost([]byte(user.PasswordHash)); err != nil {
				return nil, fmt.Errorf("nieprawidłowy hash hasła użytkownika %q", user.Username)
			}
			if user.Active && user.Role == "admin" {
				activeAdmins++
			}
		}
		if activeAdmins == 0 {
			return nil, errors.New("rejestr nie zawiera aktywnego administratora; start zablokowany")
		}
		if err := os.Chmod(path, 0600); err != nil {
			return nil, fmt.Errorf("nie można ustawić uprawnień rejestru użytkowników: %w", err)
		}
		return store, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("nie można otworzyć rejestru użytkowników: %w", err)
	}
	if !validPanelRole(bootstrapRole) {
		return nil, errors.New("nieprawidłowa rola konta początkowego")
	}
	if !panelUsernamePattern.MatchString(bootstrapUsername) || len(bootstrapPassword) < 8 {
		return nil, errors.New("konto początkowe wymaga poprawnego PANEL_USER i PANEL_PASSWORD (minimum 8 znaków)")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(bootstrapPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("nie można zahashować hasła konta początkowego: %w", err)
	}
	now := time.Now().UTC()
	id, err := newPanelUserID()
	if err != nil {
		return nil, err
	}
	store.users[id] = panelUserRecord{ID: id, Username: bootstrapUsername, PasswordHash: string(hash), Role: bootstrapRole, Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.saveLocked(store.users); err != nil {
		return nil, err
	}
	return store, nil
}

func newPanelUserID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (s *panelUserStore) saveLocked(users map[string]panelUserRecord) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("nie można utworzyć katalogu rejestru użytkowników: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("nie można zabezpieczyć katalogu rejestru użytkowników: %w", err)
	}
	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".users-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	return os.Chmod(s.path, 0600)
}

func (s *panelUserStore) sessionValid(session panelSession) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, user := range s.users {
		if strings.EqualFold(user.Username, session.username) {
			return user.Active && user.Role == session.role && !user.UpdatedAt.After(session.issuedAt)
		}
	}
	return false
}

func (s *panelUserStore) authenticate(username, password string) (panelUserRecord, bool) {
	s.mu.RLock()
	var found panelUserRecord
	for _, user := range s.users {
		if strings.EqualFold(user.Username, username) {
			found = user
			break
		}
	}
	s.mu.RUnlock()
	if found.ID == "" {
		// Keep the missing-user path close to the bcrypt cost of a real login.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"), []byte(password))
		return panelUserRecord{}, false
	}
	passwordErr := bcrypt.CompareHashAndPassword([]byte(found.PasswordHash), []byte(password))
	if !found.Active || passwordErr != nil {
		return panelUserRecord{}, false
	}
	return found, true
}

func publicPanelUser(user panelUserRecord) panelUserPublic {
	return panelUserPublic{ID: user.ID, Username: user.Username, Role: user.Role, Active: user.Active, CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt}
}

func (s *panelUserStore) list() []panelUserPublic {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]panelUserPublic, 0, len(s.users))
	for _, user := range s.users {
		result = append(result, publicPanelUser(user))
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Username) < strings.ToLower(result[j].Username) })
	return result
}

func (s *panelUserStore) create(username, password, role string) (panelUserPublic, error) {
	username = strings.TrimSpace(username)
	if !panelUsernamePattern.MatchString(username) {
		return panelUserPublic{}, errors.New("login musi mieć 3–32 znaki: litery, cyfry, kropka, myślnik lub podkreślenie")
	}
	if len(password) < panelUserPasswordMin {
		return panelUserPublic{}, fmt.Errorf("hasło musi mieć co najmniej %d znaków", panelUserPasswordMin)
	}
	if !validPanelRole(role) {
		return panelUserPublic{}, errors.New("nieprawidłowa rola użytkownika")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return panelUserPublic{}, err
	}
	id, err := newPanelUserID()
	if err != nil {
		return panelUserPublic{}, err
	}
	now := time.Now().UTC()
	user := panelUserRecord{ID: id, Username: username, PasswordHash: string(hash), Role: role, Active: true, CreatedAt: now, UpdatedAt: now}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.users {
		if strings.EqualFold(existing.Username, username) {
			return panelUserPublic{}, errors.New("użytkownik o takim loginie już istnieje")
		}
	}
	next := make(map[string]panelUserRecord, len(s.users)+1)
	for key, value := range s.users {
		next[key] = value
	}
	next[id] = user
	if err := s.saveLocked(next); err != nil {
		return panelUserPublic{}, err
	}
	s.users = next
	return publicPanelUser(user), nil
}

func (s *panelUserStore) update(id string, role *string, active *bool, password *string) (panelUserPublic, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.users[id]
	if !ok {
		return panelUserPublic{}, errors.New("nie znaleziono użytkownika")
	}
	if role != nil {
		if !validPanelRole(*role) {
			return panelUserPublic{}, errors.New("nieprawidłowa rola użytkownika")
		}
		user.Role = *role
	}
	if active != nil {
		user.Active = *active
	}
	if password != nil {
		if len(*password) < panelUserPasswordMin {
			return panelUserPublic{}, fmt.Errorf("hasło musi mieć co najmniej %d znaków", panelUserPasswordMin)
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
		if err != nil {
			return panelUserPublic{}, err
		}
		user.PasswordHash = string(hash)
	}
	if !user.Active || user.Role != "admin" {
		activeAdmins := 0
		for key, existing := range s.users {
			if key == id {
				continue
			}
			if existing.Active && existing.Role == "admin" {
				activeAdmins++
			}
		}
		if user.Active && user.Role == "admin" {
			activeAdmins++
		}
		if activeAdmins == 0 {
			return panelUserPublic{}, errors.New("nie można zablokować ani zdegradować ostatniego aktywnego administratora")
		}
	}
	updatedAt := time.Now().UTC()
	if !updatedAt.After(user.UpdatedAt) {
		updatedAt = user.UpdatedAt.Add(time.Nanosecond)
	}
	user.UpdatedAt = updatedAt
	next := make(map[string]panelUserRecord, len(s.users))
	for key, value := range s.users {
		next[key] = value
	}
	next[id] = user
	if err := s.saveLocked(next); err != nil {
		return panelUserPublic{}, err
	}
	s.users = next
	return publicPanelUser(user), nil
}

func registerPanelUserRoutes(mux *http.ServeMux, store *panelUserStore) {
	mux.HandleFunc("/api/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"users": store.list()})
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			var input struct {
				Username string `json:"username"`
				Password string `json:"password"`
				Role     string `json:"role"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				http.Error(w, "Nieprawidłowe dane użytkownika.", http.StatusBadRequest)
				return
			}
			user, err := store.create(input.Username, input.Password, input.Role)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(user)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/users/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPatch {
			w.Header().Set("Allow", "PATCH")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/users/")
		if id == "" || strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var input struct {
			Role     *string `json:"role"`
			Active   *bool   `json:"active"`
			Password *string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "Nieprawidłowe dane użytkownika.", http.StatusBadRequest)
			return
		}
		if input.Role == nil && input.Active == nil && input.Password == nil {
			http.Error(w, "Nie podano zmian.", http.StatusBadRequest)
			return
		}
		if identity, ok := r.Context().Value(identityContextKey{}).(panelIdentity); ok {
			store.mu.RLock()
			current := store.users[id]
			store.mu.RUnlock()
			if current.Username == identity.Username && ((input.Active != nil && !*input.Active) || (input.Role != nil && *input.Role != "admin")) {
				http.Error(w, "Nie można zablokować ani zdegradować własnego konta administratora.", http.StatusConflict)
				return
			}
		}
		user, err := store.update(id, input.Role, input.Active, input.Password)
		if err != nil {
			status := http.StatusBadRequest
			if err.Error() == "nie znaleziono użytkownika" {
				status = http.StatusNotFound
			} else if strings.Contains(err.Error(), "ostatniego aktywnego administratora") {
				status = http.StatusConflict
			}
			http.Error(w, err.Error(), status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(user)
	})
}
