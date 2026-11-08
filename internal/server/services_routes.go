package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"gs948mx-manager/internal/device"
)

// registerServiceRoutes: lista usług switcha z ich stanem oraz włączanie i wyłączanie.
func registerServiceRoutes(apiMux *http.ServeMux, svc *device.Service, backupDir string) {
	apiMux.HandleFunc("/api/services", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			states, err := svc.ServiceStates()
			w.Header().Set("Content-Type", "application/json")
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"services": states})
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 1024)
			var q struct {
				ID                 string `json:"id"`
				Enabled            bool   `json:"enabled"`
				AcknowledgeWarning bool   `json:"acknowledgeWarning"`
			}
			if json.NewDecoder(r.Body).Decode(&q) != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if err := serviceChangeAllowed(q.ID, q.Enabled, q.AcknowledgeWarning); err != nil {
				write(w, "", err)
				return
			}
			v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) {
				return svc.ConfigureService(q.ID, q.Enabled)
			})
			write(w, v, e)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// serviceChangeAllowed wymaga potwierdzenia ryzyka przed wyłączeniem usługi z ostrzeżeniem.
func serviceChangeAllowed(id string, enable, acknowledged bool) error {
	if _, err := device.BuildServiceCommands(id, enable); err != nil {
		return err
	}
	if enable {
		return nil
	}
	for _, d := range device.ServiceDefs {
		if d.ID == id && d.Warning != "" && !acknowledged {
			return fmt.Errorf("wyłączenie wymaga potwierdzenia: %s", d.Warning)
		}
	}
	return nil
}
