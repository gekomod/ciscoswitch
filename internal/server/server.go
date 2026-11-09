package server

import (
	"encoding/json"
	"gs948mx-manager/internal/device"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	registerPreviewRoutes(apiMux, svc, backupDir)
	registerDeviceSettingsRoutes(apiMux, svc, backupDir)
	registerPortConfigRoutes(apiMux, svc, backupDir)
	registerToolRoutes(apiMux, svc, backupDir)
	registerServiceRoutes(apiMux, svc, backupDir)
	registerFileRoutes(apiMux, svc, backupDir)
	registerMonitoringRoutes(apiMux, svc, backupDir)
	registerTrafficRoutes(apiMux, svc, backupDir)
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
