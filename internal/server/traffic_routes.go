package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"gs948mx-manager/internal/device"
)

// registerTrafficRoutes: storm control, ochrona przed pętlami, IGMP snooping.
func registerTrafficRoutes(apiMux *http.ServeMux, svc *device.Service, backupDir string) {
	apiMux.HandleFunc("/api/qos", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		info, err := svc.QoS()
		writeJSON(w, info, err)
	})
	apiMux.HandleFunc("/api/traffic", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			info, err := svc.Traffic()
			writeJSON(w, info, err)
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 2048)
			var q struct {
				Action string `json:"action"`
				Ports  string `json:"ports"`
				Type   string `json:"type"`
				Level  int    `json:"level"`
				Value  string `json:"value"`
				VLAN   int    `json:"vlan"`
				Enable bool   `json:"enable"`
				Remove bool   `json:"remove"`
			}
			if json.NewDecoder(r.Body).Decode(&q) != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if err := trafficPortsAllowed(q.Action, q.Ports); err != nil {
				write(w, "", err)
				return
			}
			v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) {
				switch q.Action {
				case "storm":
					return svc.ConfigureStorm(q.Ports, q.Type, q.Level, q.Remove)
				case "loop-detect":
					return svc.ConfigureLoopDetect(q.Enable)
				case "loop-action":
					return svc.ConfigureLoopAction(q.Ports, q.Value, q.Remove)
				case "igmp":
					return svc.ConfigureIGMP(q.VLAN, q.Enable)
				case "qos-global":
					return svc.ConfigureQoSGlobal(q.Enable)
				case "qos-port":
					return svc.ConfigureQoSPorts(q.Ports, q.Level, q.Remove)
				}
				return "", fmt.Errorf("nieznana akcja %q", q.Action)
			})
			write(w, v, e)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// trafficPortsAllowed blokuje zmiany na portach chronionych (np. uplink do panelu):
// zbyt niski limit ruchu albo wyłączenie portu przez ochronę przed pętlami
// mogłyby odciąć panel od switcha.
func trafficPortsAllowed(action, raw string) error {
	if action != "storm" && action != "loop-action" && action != "qos-port" {
		return nil
	}
	ports, err := device.ParsePortRanges(raw)
	if err != nil {
		return err
	}
	for _, p := range ports {
		if isProtectedPort(p) {
			return fmt.Errorf("%s jest portem chronionym (PROTECTED_PORTS); usuń go z listy", p)
		}
	}
	return nil
}
