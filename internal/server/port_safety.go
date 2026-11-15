package server

import (
	"encoding/json"
	"fmt"
	"gs948mx-manager/internal/settings"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Port shutdown is opt-in because the panel cannot reliably infer which physical
// interface carries its own management session. Protected ports remain immutable.
func portShutdownEnabled() bool {
	return settings.Bool("ALLOW_PORT_SHUTDOWN")
}
func managementDHCPChangeEnabled() bool {
	return settings.Bool("ALLOW_MANAGEMENT_DHCP_CHANGE")
}
func protectedPortNames() []string {
	seen := map[string]bool{}
	ports := []string{}
	for _, item := range strings.Split(settings.Get("PROTECTED_PORTS"), ",") {
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
	name = canonicalPort(name)
	for _, item := range strings.Split(settings.Get("PROTECTED_PORTS"), ",") {
		if canonicalPort(item) == name {
			return true
		}
	}
	return false
}

// canonicalPort sprowadza "port1.0.05", " Port1.0.5" i "5" do "port1.0.5",
// aby inna pisownia nie omijała listy portów chronionych.
func canonicalPort(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	num := strings.TrimPrefix(s, "port1.0.")
	if n, err := strconv.Atoi(num); err == nil && n >= 0 && !strings.HasPrefix(num, "+") {
		return "port1.0." + strconv.Itoa(n)
	}
	return s
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
