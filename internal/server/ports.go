package server

import (
	"encoding/json"
	"fmt"
	"gs948mx-manager/internal/device"
	"net/http"
	"strings"
)

type bulkPortPreviewRequest struct {
	Ports       []string `json:"ports"`
	Mode        string   `json:"mode"`
	AccessVLAN  int      `json:"accessVlan"`
	Allowed     string   `json:"allowed"`
	Native      int      `json:"native"`
	Speed       string   `json:"speed"`
	Duplex      string   `json:"duplex"`
	Description string   `json:"description"`
}

type bulkPortPreviewItem struct {
	Port     string         `json:"port"`
	Current  map[string]any `json:"current"`
	Warnings []string       `json:"warnings"`
}

func bulkSwitchportDetailsHandler(read func(string) (string, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var q struct {
			Ports []string `json:"ports"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if _, err := device.BuildBulkPortActionCommands(q.Ports, "enable"); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ports := make([]string, 0, len(q.Ports))
		seen := make(map[string]bool, len(q.Ports))
		for _, raw := range q.Ports {
			port := strings.TrimSpace(raw)
			key := strings.ToLower(port)
			if !seen[key] {
				seen[key] = true
				ports = append(ports, port)
			}
		}
		output, err := read("show interface switchport")
		if err != nil {
			http.Error(w, "nie udało się odczytać konfiguracji portów po zmianie", http.StatusBadGateway)
			return
		}
		details := make([]map[string]any, 0, len(ports))
		for _, port := range ports {
			current, parseErr := device.ParseSwitchportDetails(output, port)
			if parseErr != nil {
				http.Error(w, "nie udało się potwierdzić konfiguracji portu "+port, http.StatusConflict)
				return
			}
			details = append(details, map[string]any{"port": port, "mode": current.Mode, "defaultVlan": current.DefaultVLAN, "configuredVlans": current.ConfiguredVLANs})
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"ports": details, "readOperations": 1})
	})
}

func validatePortRepair(read func(string) (string, error), q device.PortRepairRequest) ([]string, device.SwitchportDetails, error) {
	q.Port = strings.TrimSpace(q.Port)
	if isProtectedPort(q.Port) {
		return nil, device.SwitchportDetails{}, fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS", q.Port)
	}
	raw, err := read("show interface switchport")
	if err != nil {
		return nil, device.SwitchportDetails{}, fmt.Errorf("nie udało się odczytać bieżącej konfiguracji switchport")
	}
	current, err := device.ParseSwitchportDetails(raw, q.Port)
	if err != nil {
		return nil, device.SwitchportDetails{}, fmt.Errorf("nie udało się jednoznacznie odczytać portu %s", q.Port)
	}
	if !strings.EqualFold(strings.TrimSpace(current.Mode), q.Mode) {
		return nil, device.SwitchportDetails{}, fmt.Errorf("tryb portu zmienił się od audytu lub nie zgadza się z profilem; wymagany jest osobny audyt i pełna zmiana trybu")
	}
	commands, err := device.BuildPortRepairCommands(q)
	if err != nil {
		return nil, device.SwitchportDetails{}, err
	}
	return commands, current, nil
}

func bulkPortPreviewHandler(read func(string) (string, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		var q bulkPortPreviewRequest
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if _, err := device.BuildBulkPortSettingsCommands(q.Ports, q.Mode, q.AccessVLAN, q.Allowed, q.Native, q.Speed, q.Duplex, q.Description); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ports := make([]string, 0, len(q.Ports))
		seen := make(map[string]bool, len(q.Ports))
		for _, raw := range q.Ports {
			port := strings.TrimSpace(raw)
			if isProtectedPort(port) {
				http.Error(w, "port "+port+" jest chroniony przez PROTECTED_PORTS", http.StatusBadRequest)
				return
			}
			key := strings.ToLower(port)
			if !seen[key] {
				seen[key] = true
				ports = append(ports, port)
			}
		}
		raw, err := read("show interface switchport")
		if err != nil {
			http.Error(w, "nie udało się odczytać bieżącej konfiguracji portów; podgląd i zapis zostały zablokowane", http.StatusBadGateway)
			return
		}
		topologyConfig, topologyConfigErr := read("show running-config")
		topologyLLDP, topologyLLDPErr := read("show lldp neighbors")
		previews := make([]bulkPortPreviewItem, 0, len(ports))
		for _, port := range ports {
			current, parseErr := device.ParseSwitchportDetails(raw, port)
			if parseErr != nil {
				http.Error(w, "nie udało się jednoznacznie rozpoznać bieżącej konfiguracji portu "+port+"; podgląd i zapis zostały zablokowane", http.StatusConflict)
				return
			}
			warnings := []string{"Bieżąca konfiguracja została odczytana bez wprowadzania zmian."}
			if strings.ToLower(current.Mode) != q.Mode {
				warnings = append(warnings, "Zmiana trybu portu z "+current.Mode+" na "+q.Mode+" może przerwać łączność urządzeń.")
			}
			if q.Mode == "trunk" {
				warnings = append(warnings, "Zmiana listy allowed VLAN może przerwać ruch urządzeń korzystających z tego portu.")
			}
			risks := topologyChangeRisksFromSnapshot(topologyConfig, raw, topologyLLDP, topologyConfigErr, topologyLLDPErr, port)
			for _, risk := range risks {
				warnings = append(warnings, risk.Title+": "+risk.Evidence+" Zalecenie: "+risk.Recommendation)
			}
			previews = append(previews, bulkPortPreviewItem{
				Port:     port,
				Current:  map[string]any{"port": current.Port, "mode": current.Mode, "defaultVlan": current.DefaultVLAN, "configuredVlans": current.ConfiguredVLANs},
				Warnings: warnings,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"previews": previews, "applies": false, "readOperations": 3, "switchportReadOperations": 1, "topologyReadOperations": 2})
	})
}

func switchportDetailsHandler(read func(string) (string, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		port := strings.TrimSpace(r.URL.Query().Get("port"))
		raw, err := read("show interface switchport")
		if err != nil {
			http.Error(w, "nie udało się odczytać switchport", http.StatusBadGateway)
			return
		}
		details, err := device.ParseSwitchportDetails(raw, port)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error(), "raw": raw})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(details)
	})
}
