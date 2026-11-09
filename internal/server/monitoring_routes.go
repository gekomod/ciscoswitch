package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"gs948mx-manager/internal/device"
)

func writeJSON(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// registerMonitoringRoutes: logi switcha, czas i NTP, mirroring portów.
func registerMonitoringRoutes(apiMux *http.ServeMux, svc *device.Service, backupDir string) {
	apiMux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		entries, raw, err := svc.Logs()
		writeJSON(w, map[string]any{"entries": entries, "raw": raw}, err)
	})

	apiMux.HandleFunc("/api/time", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			info, err := svc.TimeInfo()
			writeJSON(w, info, err)
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 1024)
			var q struct {
				Action string `json:"action"`
				Host   string `json:"host"`
				Name   string `json:"name"`
				Offset int    `json:"offset"`
			}
			if json.NewDecoder(r.Body).Decode(&q) != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) {
				switch q.Action {
				case "ntp-add":
					return svc.ConfigureNTP(q.Host, false)
				case "ntp-remove":
					return svc.ConfigureNTP(q.Host, true)
				case "timezone":
					return svc.ConfigureTimezone(q.Name, q.Offset)
				case "summer-eu":
					return svc.ConfigureSummerTime(q.Name, false)
				case "summer-remove":
					return svc.ConfigureSummerTime("", true)
				}
				return "", fmt.Errorf("nieznana akcja %q", q.Action)
			})
			write(w, v, e)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	apiMux.HandleFunc("/api/compat", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, svc.Compatibility(), nil)
	})

	apiMux.HandleFunc("/api/mirror", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			m, err := svc.Mirrors()
			writeJSON(w, map[string]any{"sessions": m}, err)
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 1024)
			var q struct {
				Destination string `json:"destination"`
				Source      string `json:"source"`
				Direction   string `json:"direction"`
				Remove      bool   `json:"remove"`
			}
			if json.NewDecoder(r.Body).Decode(&q) != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if err := mirrorAllowed(q.Destination, q.Remove); err != nil {
				write(w, "", err)
				return
			}
			v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) {
				return svc.ConfigureMirror(q.Destination, q.Source, q.Direction, q.Remove)
			})
			write(w, v, e)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// mirrorAllowed: port docelowy mirroringu przestaje normalnie przenosić ruch,
// więc nie może nim być port chroniony (np. uplink do panelu).
func mirrorAllowed(dest string, remove bool) error {
	if !remove && isProtectedPort(dest) {
		return fmt.Errorf("%s jest portem chronionym (PROTECTED_PORTS) i nie może być portem docelowym mirroringu", dest)
	}
	return nil
}
