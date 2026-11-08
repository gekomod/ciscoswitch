package server

import (
	"encoding/json"
	"net/http"

	"gs948mx-manager/internal/device"
)

// registerFileRoutes: zawartość flash, ustawienia startu i bezpieczne akcje na plikach.
func registerFileRoutes(apiMux *http.ServeMux, svc *device.Service, backupDir string) {
	apiMux.HandleFunc("/api/files", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			info, err := svc.Files()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(info)
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 1024)
			var q struct {
				Action string `json:"action"`
				Name   string `json:"name"`
			}
			if json.NewDecoder(r.Body).Decode(&q) != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) {
				return svc.FileAction(q.Action, q.Name)
			})
			write(w, v, e)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}
