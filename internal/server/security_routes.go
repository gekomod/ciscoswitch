package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"gs948mx-manager/internal/device"
)

type securityRequest struct {
	Action    string `json:"action"`
	Ports     string `json:"ports"`
	Maximum   int    `json:"maximum"`
	Violation string `json:"violation"`
	Enable    bool   `json:"enable"`
	VLAN      int    `json:"vlan"`
	ACL       string `json:"acl"`
	ACLNumber int    `json:"aclNumber"`
	Rule      string `json:"rule"`
	Source    string `json:"source"`
	Dest      string `json:"destination"`
	Remove    bool   `json:"remove"`
}

// registerSecurityRoutes: port security, DHCP snooping, ACL.
func registerSecurityRoutes(apiMux *http.ServeMux, svc *device.Service, backupDir string) {
	apiMux.HandleFunc("/api/security", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			info, err := svc.Security()
			writeJSON(w, info, err)
		case http.MethodPost:
			r.Body = http.MaxBytesReader(w, r.Body, 2048)
			var q securityRequest
			if json.NewDecoder(r.Body).Decode(&q) != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if err := securityPortsAllowed(q); err != nil {
				write(w, "", err)
				return
			}
			v, e := runWithPreChangeBackup(backupDir, svc.RunReadOnly, func() (string, error) {
				switch q.Action {
				case "port-security":
					return svc.ConfigurePortSecurity(q.Ports, q.Maximum, q.Violation, q.Remove)
				case "dhcp-global":
					return svc.ConfigureDHCPSnoopingGlobal(q.Enable)
				case "dhcp-vlan":
					return svc.ConfigureDHCPSnoopingVLAN(q.VLAN, q.Enable)
				case "dhcp-trust":
					return svc.ConfigureDHCPTrust(q.Ports, q.Enable)
				case "acl-apply":
					return svc.ConfigureACLApply(q.Ports, q.ACL, q.Remove)
				case "acl-rule":
					return svc.ConfigureACLRule(q.ACLNumber, q.Rule, q.Source, q.Dest, q.Remove)
				}
				return "", fmt.Errorf("nieznana akcja %q", q.Action)
			})
			write(w, v, e)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// securityPortsAllowed chroni porty z PROTECTED_PORTS (np. uplink do panelu):
// nie można na nich włączyć port security ani przypisać ACL, ani zdjąć z nich
// zaufania DHCP. Zdjęcie ACL i dodanie zaufania DHCP są bezpieczne i dozwolone.
func securityPortsAllowed(q securityRequest) error {
	check := false
	switch q.Action {
	case "port-security":
		check = true
	case "acl-apply":
		check = !q.Remove
	case "dhcp-trust":
		check = !q.Enable
	}
	if !check {
		return nil
	}
	ports, err := device.ParsePortRanges(q.Ports)
	if err != nil {
		return err
	}
	for _, p := range ports {
		if isProtectedPort(p) {
			return fmt.Errorf("%s jest portem chronionym (PROTECTED_PORTS); ta zmiana mogłaby odciąć panel od switcha", p)
		}
	}
	return nil
}
