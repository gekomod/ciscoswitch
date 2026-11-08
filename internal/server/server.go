package server

import (
	"encoding/json"
	"fmt"
	"gs948mx-manager/internal/device"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func Register(m *http.ServeMux, svc *device.Service) {
	apiMux := http.NewServeMux()
	userPath := os.Getenv("PANEL_USERS_PATH")
	if userPath == "" {
		userPath = filepath.Join("data", "panel-users.json")
	}
	panelUsers := newPanelUserStore(userPath)
	apiMux.Handle("/api/users", panelUsers.usersHandler())
	apiMux.Handle("/api/users/", panelUsers.usersHandler())
	apiMux.Handle("/", http.FileServer(http.Dir("web")))
	apiMux.Handle("/api/config/export", configExportHandler(svc.RunReadOnly))
	apiMux.Handle("/api/config/compare", configCompareHandler(svc.RunReadOnly))
	backupDir := os.Getenv("CONFIG_BACKUP_DIR")
	if backupDir == "" {
		backupDir = filepath.Join("data", "config-backups")
	}
	retention := 100
	if raw := os.Getenv("CONFIG_BACKUP_RETENTION"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 && n <= 10000 {
			retention = n
		}
	}
	apiMux.Handle("/api/config/backups", configBackupsHandler(svc.RunReadOnly, backupDir, retention))
	apiMux.Handle("/api/config/backups/download", configBackupDownloadHandler(backupDir))
	apiMux.Handle("/api/config/backups/compare", configBackupCompareHandler(svc.RunReadOnly, backupDir))
	apiMux.Handle("/api/config/backups/restore-preview", configBackupRestorePreviewHandler(backupDir))
	apiMux.Handle("/api/audit", auditHistoryHandler(os.Getenv("AUDIT_LOG_PATH")))
	apiMux.Handle("/api/security-audit", securityAuditHandler(svc.RunReadOnly))
	apiMux.Handle("/api/topology/map", topologyMapHandler(svc.RunReadOnly))
	diagnosticsPath := os.Getenv("DIAGNOSTICS_HISTORY_PATH")
	if diagnosticsPath == "" {
		diagnosticsPath = filepath.Join("data", "diagnostics-history.json")
	}
	diagnosticsStore := sharedDiagnosticsStore(diagnosticsPath, 12000)
	apiMux.Handle("/api/diagnostics/collect", diagnosticsCollectHandler(svc.RunReadOnly, diagnosticsStore))
	apiMux.Handle("/api/diagnostics/history", diagnosticsHistoryHandler(diagnosticsStore))
	apiMux.Handle("/api/events", diagnosticsEventsHandler(diagnosticsStore))
	apiMux.Handle("/api/diagnostics/availability", diagnosticsAvailabilityHandler(diagnosticsStore))
	apiMux.Handle("/api/diagnostics/availability.csv", diagnosticsAvailabilityCSVHandler(diagnosticsStore))
	apiMux.Handle("/api/diagnostics/acknowledge", diagnosticsAcknowledgeHandler(diagnosticsStore, diagnosticsPath+".acks.json"))
	monitorPath := os.Getenv("MONITOR_HISTORY_PATH")
	if monitorPath == "" {
		monitorPath = filepath.Join("data", "monitor-history.jsonl")
	}
	apiMux.Handle("/api/monitor/history", monitorHistoryHandler(monitorPath))
	apiMux.Handle("/api/monitor/stream", monitorStreamHandler())
	apiMux.HandleFunc("/api/device/identity", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		identity, err := svc.DeviceIdentity()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(identity)
	})
	apiMux.HandleFunc("/api/device/capabilities", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		identity, identityErr := svc.DeviceIdentity()
		status := svc.Status()
		speeds := map[string]int{}
		for _, p := range status.Ports {
			if p.Speed != "" {
				speeds[p.Speed]++
			}
		}
		payload := map[string]any{"identity": identity, "identityAvailable": identityErr == nil, "interfacesObserved": len(status.Ports), "interfaceSpeeds": speeds, "snmpReachable": status.Reachable, "snmpVersion": status.SNMPVersion}
		if identityErr != nil {
			payload["identityError"] = identityErr.Error()
		}
		if status.Warning != "" {
			payload["statusWarning"] = status.Warning
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(payload)
	})
	apiMux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "method not allowed", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(svc.Status())
	})
	apiMux.Handle("/api/ports/protection", protectedPortsHandler())
	profilePath := os.Getenv("PORT_PROFILES_PATH")
	if profilePath == "" {
		profilePath = filepath.Join("data", "port-profiles.json")
	}
	apiMux.Handle("/api/port-profiles", portProfilesHandler(profilePath))
	apiMux.Handle("/api/ports/preview/bulk", bulkPortPreviewHandler(svc.RunReadOnly))
	apiMux.Handle("/api/ports/switchport/bulk", bulkSwitchportDetailsHandler(svc.RunReadOnly))
	apiMux.HandleFunc("/api/port/preview", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var q struct {
			Port        string `json:"port"`
			Mode        string `json:"mode"`
			AccessVLAN  int    `json:"accessVlan"`
			Allowed     string `json:"allowed"`
			Native      int    `json:"native"`
			Speed       string `json:"speed"`
			Duplex      string `json:"duplex"`
			Description string `json:"description"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if isProtectedPort(q.Port) {
			http.Error(w, "port chroniony przez PROTECTED_PORTS", 400)
			return
		}
		cmds, err := device.BuildPortSettingsCommands(q.Port, q.Mode, q.AccessVLAN, q.Allowed, q.Native, q.Speed, q.Duplex, q.Description)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		raw, err := svc.RunReadOnly("show interface switchport")
		if err != nil {
			http.Error(w, "nie udało się odczytać bieżącej konfiguracji portu; podgląd i zapis zostały zablokowane", http.StatusBadGateway)
			return
		}
		current, err := device.ParseSwitchportDetails(raw, q.Port)
		if err != nil {
			http.Error(w, "nie udało się jednoznacznie rozpoznać bieżącej konfiguracji portu; podgląd i zapis zostały zablokowane", http.StatusConflict)
			return
		}
		warnings := []string{"Bieżąca konfiguracja została odczytana bez wprowadzania zmian."}
		risks := topologyChangeRisks(svc.RunReadOnly, q.Port)
		for _, risk := range risks {
			warnings = append(warnings, risk.Title+": "+risk.Evidence+" Zalecenie: "+risk.Recommendation)
		}
		if strings.ToLower(current.Mode) != q.Mode {
			warnings = append(warnings, "Zmiana trybu portu z "+current.Mode+" na "+q.Mode+" może przerwać łączność urządzeń.")
		}
		if q.Mode == "trunk" {
			warnings = append(warnings, "Zmiana listy allowed VLAN może przerwać ruch urządzeń korzystających z tego portu.")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"commands": cmds, "warnings": warnings, "risks": risks, "applies": false,
			"current": map[string]any{"port": current.Port, "mode": current.Mode, "defaultVlan": current.DefaultVLAN, "configuredVlans": current.ConfiguredVLANs},
		})
	})
	apiMux.HandleFunc("/api/vlan/preview", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		var q struct {
			ID     int    `json:"id"`
			Name   string `json:"name"`
			Delete bool   `json:"delete"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		cmds, err := device.BuildVLANCommands(q.ID, q.Name, q.Delete)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		warnings := []string{"Podgląd nie zmienia konfiguracji przełącznika."}
		if q.Delete {
			warnings = append(warnings, "Usunięcie VLAN może przerwać łączność wszystkich przypisanych do niego urządzeń.")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"commands": cmds, "warnings": warnings, "applies": false})
	})
	apiMux.HandleFunc("/api/port", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		var q struct {
			Port   string `json:"port"`
			Action string `json:"action"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if isProtectedPort(q.Port) {
			write(w, "", fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS", q.Port))
			return
		}
		if q.Action == "disable" && !portShutdownEnabled() {
			write(w, "", fmt.Errorf("wyłączanie portów jest domyślnie zablokowane; ustaw ALLOW_PORT_SHUTDOWN=true po sprawdzeniu ryzyka utraty dostępu"))
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) { return svc.ConfigurePort(q.Port, q.Action) })
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/management-dhcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		var q struct {
			Enabled bool `json:"enabled"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if !managementDHCPChangeEnabled() {
			write(w, "", fmt.Errorf("zmiana DHCP interfejsu zarządzającego jest domyślnie zablokowana; ustaw ALLOW_MANAGEMENT_DHCP_CHANGE=true po zapewnieniu alternatywnego dostępu"))
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) { return svc.SetManagementDHCP(q.Enabled) })
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/ssh/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		var q struct {
			Username  string `json:"username"`
			Password  string `json:"password"`
			Privilege int    `json:"privilege"`
			Delete    bool   `json:"delete"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) { return svc.ConfigureSSHUser(q.Username, q.Password, q.Privilege, q.Delete) })
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/snmp/trap-host", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		var q struct {
			Address   string `json:"address"`
			Community string `json:"community"`
			Delete    bool   `json:"delete"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) { return svc.ConfigureSNMPTrapHost(q.Address, q.Community, q.Delete) })
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/snmp/v3-user", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var q struct {
			Username        string `json:"username"`
			Group           string `json:"group"`
			AuthPassword    string `json:"authPassword"`
			PrivacyPassword string `json:"privacyPassword"`
			PrivacyProtocol string `json:"privacyProtocol"`
			Delete          bool   `json:"delete"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) {
			return svc.ConfigureSNMPv3User(q.Username, q.Group, q.AuthPassword, q.PrivacyPassword, q.PrivacyProtocol, q.Delete)
		})
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/snmp/v3-trap-host", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		var q struct {
			Address  string `json:"address"`
			Username string `json:"username"`
			Delete   bool   `json:"delete"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) { return svc.ConfigureSNMPv3TrapHost(q.Address, q.Username, q.Delete) })
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/network/probe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		var q struct {
			Kind   string `json:"kind"`
			Target string `json:"target"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		v, e := svc.ProbeNetwork(q.Kind, q.Target)
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/route/static", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		var q struct {
			Prefix   string `json:"prefix"`
			Gateway  string `json:"gateway"`
			Distance int    `json:"distance"`
			Delete   bool   `json:"delete"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) { return svc.ConfigureStaticRoute(q.Prefix, q.Gateway, q.Distance, q.Delete) })
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/dhcp/pool", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var q struct {
			Name    string `json:"name"`
			Network string `json:"network"`
			Mask    string `json:"mask"`
			Low     string `json:"low"`
			High    string `json:"high"`
			Router  string `json:"router"`
			DNS     string `json:"dns"`
			Enable  bool   `json:"enable"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) {
			return svc.ConfigureDHCPPool(q.Name, q.Network, q.Mask, q.Low, q.High, q.Router, q.DNS, q.Enable)
		})
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/vlan", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		var q struct {
			ID     int    `json:"id"`
			Name   string `json:"name"`
			Delete bool   `json:"delete"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) { return svc.ConfigureVLAN(q.ID, q.Name, q.Delete) })
		write(w, v, e)
	})

	apiMux.HandleFunc("/api/vlan/ports/preview", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var q struct {
			Ports      []string `json:"ports"`
			VLAN       int      `json:"vlan"`
			Membership string   `json:"membership"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		for _, port := range q.Ports {
			if isProtectedPort(port) {
				write(w, "", fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS", port))
				return
			}
		}
		running, err := svc.RunReadOnly("show running-config")
		if err != nil {
			write(w, "", fmt.Errorf("nie udało się odczytać running-config: %w", err))
			return
		}
		cmds, err := device.BuildVLANPortMembershipCommands(running, q.Ports, q.VLAN, q.Membership)
		if err != nil {
			write(w, "", err)
			return
		}
		warning := "Operacja zmienia wyłącznie członkostwo VLAN na wybranych portach; nie zmienia automatycznie trybu access/trunk."
		if q.Membership == "untagged" {
			warning += " Porty muszą być w trybie access."
		} else {
			warning += " Porty muszą już być w trybie trunk; istniejąca lista VLAN i native VLAN pozostają zachowane."
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"commands": cmds, "warnings": []string{warning}, "applies": false})
	})
	apiMux.HandleFunc("/api/vlan/ports/apply", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var q struct {
			Ports      []string `json:"ports"`
			VLAN       int      `json:"vlan"`
			Membership string   `json:"membership"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		for _, port := range q.Ports {
			if isProtectedPort(port) {
				write(w, "", fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS", port))
				return
			}
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) { return svc.ConfigureVLANPortMembership(q.Ports, q.VLAN, q.Membership) })
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/port/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var q struct {
			Port                        string `json:"port"`
			Mode                        string `json:"mode"`
			AccessVLAN                  int    `json:"accessVlan"`
			Allowed                     string `json:"allowed"`
			Native                      int    `json:"native"`
			Speed                       string `json:"speed"`
			Duplex                      string `json:"duplex"`
			Description                 string `json:"description"`
			AcknowledgeConnectivityRisk bool   `json:"acknowledgeConnectivityRisk"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if isProtectedPort(q.Port) {
			write(w, "", fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS", q.Port))
			return
		}
		if err := requireTopologyRiskAcknowledgement(svc.RunReadOnly, []string{q.Port}, q.AcknowledgeConnectivityRisk); err != nil {
			write(w, "", err)
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) {
			if err := requireTopologyRiskAcknowledgement(svc.RunReadOnly, []string{q.Port}, q.AcknowledgeConnectivityRisk); err != nil {
				return "", err
			}
			return svc.ConfigurePortSettings(q.Port, q.Mode, q.AccessVLAN, q.Allowed, q.Native, q.Speed, q.Duplex, q.Description)
		})
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/ports/repair/preview", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var q device.PortRepairRequest
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if isProtectedPort(q.Port) {
			http.Error(w, "port "+q.Port+" jest chroniony przez PROTECTED_PORTS", 400)
			return
		}
		commands, current, err := validatePortRepair(svc.RunReadOnly, q)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		risks := topologyChangeRisks(svc.RunReadOnly, q.Port)
		warnings := make([]string, 0, len(risks))
		for _, risk := range risks {
			warnings = append(warnings, risk.Title+": "+risk.Evidence+" Zalecenie: "+risk.Recommendation)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"port": q.Port, "current": current, "commands": commands, "risks": risks, "warnings": warnings, "applies": false})
	})
	apiMux.HandleFunc("/api/ports/repair/apply", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var request struct {
			device.PortRepairRequest
			AcknowledgeConnectivityRisk bool `json:"acknowledgeConnectivityRisk"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		q := request.PortRepairRequest
		if isProtectedPort(q.Port) {
			write(w, "", fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS", q.Port))
			return
		}
		if _, _, err := validatePortRepair(svc.RunReadOnly, q); err != nil {
			write(w, "", err)
			return
		}
		if err := requireTopologyRiskAcknowledgement(svc.RunReadOnly, []string{q.Port}, request.AcknowledgeConnectivityRisk); err != nil {
			write(w, "", err)
			return
		}
		output, err := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) {
			if _, _, checkErr := validatePortRepair(svc.RunReadOnly, q); checkErr != nil {
				return "", checkErr
			}
			if riskErr := requireTopologyRiskAcknowledgement(svc.RunReadOnly, []string{q.Port}, request.AcknowledgeConnectivityRisk); riskErr != nil {
				return "", riskErr
			}
			return svc.ConfigurePortRepair(q)
		})
		write(w, output, err)
	})
	apiMux.HandleFunc("/api/ports/settings/bulk", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		var q struct {
			Ports                       []string `json:"ports"`
			Mode                        string   `json:"mode"`
			AccessVLAN                  int      `json:"accessVlan"`
			Allowed                     string   `json:"allowed"`
			Native                      int      `json:"native"`
			Speed                       string   `json:"speed"`
			Duplex                      string   `json:"duplex"`
			Description                 string   `json:"description"`
			AcknowledgeConnectivityRisk bool     `json:"acknowledgeConnectivityRisk"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		for _, port := range q.Ports {
			if isProtectedPort(port) {
				write(w, "", fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS", port))
				return
			}
		}
		if _, err := device.BuildBulkPortSettingsCommands(q.Ports, q.Mode, q.AccessVLAN, q.Allowed, q.Native, q.Speed, q.Duplex, q.Description); err != nil {
			write(w, "", err)
			return
		}
		if err := requireTopologyRiskAcknowledgement(svc.RunReadOnly, q.Ports, q.AcknowledgeConnectivityRisk); err != nil {
			write(w, "", err)
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) {
			if err := requireTopologyRiskAcknowledgement(svc.RunReadOnly, q.Ports, q.AcknowledgeConnectivityRisk); err != nil {
				return "", err
			}
			return svc.ConfigureBulkPortSettings(q.Ports, q.Mode, q.AccessVLAN, q.Allowed, q.Native, q.Speed, q.Duplex, q.Description)
		})
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/ports/action/bulk", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var q struct {
			Ports  []string `json:"ports"`
			Action string   `json:"action"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		for _, port := range q.Ports {
			if isProtectedPort(port) {
				write(w, "", fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS", port))
				return
			}
		}
		if q.Action == "disable" && !portShutdownEnabled() {
			write(w, "", fmt.Errorf("wyłączanie portów jest domyślnie zablokowane; ustaw ALLOW_PORT_SHUTDOWN=true po sprawdzeniu ryzyka utraty dostępu"))
			return
		}
		if _, err := device.BuildBulkPortActionCommands(q.Ports, q.Action); err != nil {
			write(w, "", err)
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) { return svc.ConfigureBulkPortAction(q.Ports, q.Action) })
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/lacp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		var q struct {
			Group  int    `json:"group"`
			Ports  string `json:"ports"`
			Mode   string `json:"mode"`
			Delete bool   `json:"delete"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if protectedPortNumberList(q.Ports) {
			write(w, "", fmt.Errorf("LACP obejmuje port chroniony przez PROTECTED_PORTS"))
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) { return svc.ConfigureLACP(q.Group, q.Ports, q.Mode, q.Delete) })
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/stp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		var q struct {
			Mode     string `json:"mode"`
			Priority int    `json:"priority"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) { return svc.ConfigureSTP(q.Mode, q.Priority) })
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/ports/edgeport/bulk", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var q struct {
			Ports   []string `json:"ports"`
			Enabled bool     `json:"enabled"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		for _, port := range q.Ports {
			if isProtectedPort(port) {
				write(w, "", fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS", port))
				return
			}
		}
		if _, err := device.BuildBulkPortEdgePortCommands(q.Ports, q.Enabled); err != nil {
			write(w, "", err)
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) { return svc.ConfigureBulkPortEdgePort(q.Ports, q.Enabled) })
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/edgeport", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		var q struct {
			Port    string `json:"port"`
			Enabled bool   `json:"enabled"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		if isProtectedPort(q.Port) {
			write(w, "", fmt.Errorf("port %s jest chroniony przez PROTECTED_PORTS", q.Port))
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) { return svc.SetPortEdgePort(q.Port, q.Enabled) })
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/save", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) { return svc.SaveConfig() })
		write(w, v, e)
	})
	apiMux.HandleFunc("/api/system", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "method not allowed", 405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(svc.SystemInfo())
	})
	apiMux.Handle("/api/port/switchport", switchportDetailsHandler(svc.RunReadOnly))
	apiMux.HandleFunc("/api/command", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		var q struct {
			Command string `json:"command"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		v, e := svc.RunReadOnlyFresh(strings.TrimSpace(q.Command))
		write(w, v, e)
	})
	m.Handle("/api/", panelUsers.middleware(apiMux))
	m.Handle("/", panelUsers.middleware(apiMux))
	m.HandleFunc("/login", panelLoginPageHandler)
}
func write(w http.ResponseWriter, v string, e error) {
	w.Header().Set("Content-Type", "application/json")
	if e != nil {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": e.Error(), "output": v})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"output": v, "ok": "true"})
}
