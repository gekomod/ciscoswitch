package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"gs948mx-manager/internal/device"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type portProfile struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	PortDescription string    `json:"portDescription"`
	Mode            string    `json:"mode"`
	AccessVLAN      int       `json:"accessVlan"`
	Allowed         string    `json:"allowed"`
	Native          int       `json:"native"`
	Speed           string    `json:"speed"`
	Duplex          string    `json:"duplex"`
	UpdatedAt       time.Time `json:"updatedAt"`
}
type portProfileStore struct {
	mu   sync.Mutex
	path string
}

func portProfilesHandler(path string) http.Handler {
	store := &portProfileStore{path: path}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		switch r.Method {
		case http.MethodGet:
			store.mu.Lock()
			defer store.mu.Unlock()
			profiles, err := store.load()
			if err != nil {
				http.Error(w, "nie udało się odczytać profili portów", 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"profiles": profiles})
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			var q portProfile
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			if err := dec.Decode(&q); err != nil {
				http.Error(w, "nieprawidłowe dane profilu", 400)
				return
			}
			q.Name = strings.TrimSpace(q.Name)
			q.Description = strings.TrimSpace(q.Description)
			if q.Name == "" || len([]rune(q.Name)) > 40 || len([]rune(q.Description)) > 120 {
				http.Error(w, "nazwa jest wymagana (maks. 40 znaków), opis maks. 120 znaków", 400)
				return
			}
			if q.Speed == "" {
				q.Speed = "auto"
			}
			if q.Duplex == "" {
				q.Duplex = "auto"
			}
			access := q.AccessVLAN
			if access == 0 {
				access = 1
			}
			if _, err := device.BuildPortSettingsCommands("port1.0.1", q.Mode, access, q.Allowed, q.Native, q.Speed, q.Duplex, q.PortDescription); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			profiles, err := store.load()
			if err != nil {
				http.Error(w, "nie udało się odczytać rejestru profili", 500)
				return
			}
			if q.ID != "" && !validPortProfileID(q.ID) {
				http.Error(w, "nieprawidłowy identyfikator profilu", 400)
				return
			}
			found := false
			if q.ID != "" {
				for i := range profiles {
					if profiles[i].ID == q.ID {
						found = true
						break
					}
				}
				if !found {
					http.Error(w, "profil nie istnieje", 404)
					return
				}
			}
			for _, existing := range profiles {
				if strings.EqualFold(existing.Name, q.Name) && existing.ID != q.ID {
					http.Error(w, "profil o takiej nazwie już istnieje", http.StatusConflict)
					return
				}
			}
			if q.ID == "" {
				if len(profiles) >= 100 {
					http.Error(w, "osiągnięto limit 100 profili", 409)
					return
				}
				var id [6]byte
				if _, err := rand.Read(id[:]); err != nil {
					http.Error(w, "nie udało się utworzyć identyfikatora", 500)
					return
				}
				q.ID = "profile-" + hex.EncodeToString(id[:])
			}
			q.UpdatedAt = time.Now().UTC()
			if found {
				for i := range profiles {
					if profiles[i].ID == q.ID {
						profiles[i] = q
						break
					}
				}
			} else {
				profiles = append(profiles, q)
			}
			if err := store.save(profiles); err != nil {
				http.Error(w, "nie udało się zapisać profilu", 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"profile": q, "profiles": profiles})
		case http.MethodDelete:
			id := r.URL.Query().Get("id")
			if !validPortProfileID(id) {
				http.Error(w, "nieprawidłowy identyfikator profilu", 400)
				return
			}
			store.mu.Lock()
			defer store.mu.Unlock()
			profiles, err := store.load()
			if err != nil {
				http.Error(w, "nie udało się odczytać rejestru profili", 500)
				return
			}
			next := make([]portProfile, 0, len(profiles))
			found := false
			for _, p := range profiles {
				if p.ID == id {
					found = true
					continue
				}
				next = append(next, p)
			}
			if !found {
				http.Error(w, "profil nie istnieje", 404)
				return
			}
			if err := store.save(next); err != nil {
				http.Error(w, "nie udało się zapisać rejestru profili", 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"profiles": next})
		default:
			w.Header().Set("Allow", "GET, POST, DELETE")
			http.Error(w, "method not allowed", 405)
		}
	})
}
func validPortProfileID(id string) bool {
	if !strings.HasPrefix(id, "profile-") || len(id) != 20 {
		return false
	}
	for _, r := range id[8:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func (s *portProfileStore) load() ([]portProfile, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return []portProfile{}, nil
	}
	if err != nil {
		return nil, err
	}
	var profiles []portProfile
	if err := json.Unmarshal(data, &profiles); err != nil {
		return nil, err
	}
	if profiles == nil {
		profiles = []portProfile{}
	}
	return profiles, nil
}
func (s *portProfileStore) save(profiles []portProfile) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
