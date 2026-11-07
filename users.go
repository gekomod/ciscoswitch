package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	if !validPanelRole(bootstrapRole) {
		return nil, errors.New("nieprawidłowa rola konta początkowego")
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
		for id, user := range store.users {
			if id == "" || user.ID != id || !panelUsernamePattern.MatchString(user.Username) || !validPanelRole(user.Role) || user.PasswordHash == "" {
				return nil, fmt.Errorf("nieprawidłowy wpis użytkownika %q", id)
			}
		}
		if err := os.Chmod(path, 0600); err != nil {
			return nil, fmt.Errorf("nie można ustawić uprawnień rejestru użytkowników: %w", err)
		}
		return store, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("nie można otworzyć rejestru użytkowników: %w", err)
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
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$7EqJtq98hPqEX7fNZaFWoO5z6qK6n6X1q3u7J7n5vQ9g2P2mM6q6K"), []byte(password))
		return panelUserRecord{}, false
	}
	if !found.Active || bcrypt.CompareHashAndPassword([]byte(found.PasswordHash), []byte(password)) != nil {
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
	user.UpdatedAt = time.Now().UTC()
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
