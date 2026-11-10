package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"gs948mx-manager/internal/device"
	"gs948mx-manager/internal/settings"
)

// registerSettingsRoutes: ustawienia panelu (tylko administrator, patrz middleware).
func registerSettingsRoutes(apiMux *http.ServeMux, svc *device.Service) {
	apiMux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			detected := ""
			if ip, err := svc.LocalIP(); err == nil {
				detected = fmt.Sprintf("%s:%d", ip, settings.Int("FIRMWARE_HTTP_PORT"))
			}
			writeJSON(w, map[string]any{"settings": settings.All(), "detectedFirmwareAddress": detected}, nil)
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			var q struct {
				Values map[string]string `json:"values"`
			}
			if json.NewDecoder(r.Body).Decode(&q) != nil || len(q.Values) == 0 {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if err := settings.Save(q.Values); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, map[string]any{"settings": settings.All()}, nil)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}
