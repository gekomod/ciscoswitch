package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Port shutdown is opt-in because the panel cannot reliably infer which physical
// interface carries its own management session. Protected ports remain immutable.
func portShutdownEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_PORT_SHUTDOWN")), "true")
}
func managementDHCPChangeEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_MANAGEMENT_DHCP_CHANGE")), "true")
}
func protectedPortNames() []string {
	seen := map[string]bool{}
	ports := []string{}
	for _, item := range strings.Split(os.Getenv("PROTECTED_PORTS"), ",") {
		name := strings.TrimSpace(item)
		if !strings.HasPrefix(strings.ToLower(name), "port1.0.") {
			continue
		}
		number, err := strconv.Atoi(name[len("port1.0."):])
		if err != nil || number < 1 || number > 52 || !strings.EqualFold(name, fmt.Sprintf("port1.0.%d", number)) {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		ports = append(ports, key)
	}
	sort.Strings(ports)
	return ports
}

func protectedPortsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ports := protectedPortNames()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"configured":         len(ports) > 0,
			"ports":              ports,
			"count":              len(ports),
			"automaticDetection": false,
		})
	})
}

func isProtectedPort(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, item := range strings.Split(os.Getenv("PROTECTED_PORTS"), ",") {
		if strings.EqualFold(strings.TrimSpace(item), name) {
			return true
		}
	}
	return false
}

func protectedPortNumberList(raw string) bool {
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 || n > 52 {
			continue
		}
		if isProtectedPort(fmt.Sprintf("port1.0.%d", n)) {
			return true
		}
	}
	return false
}
