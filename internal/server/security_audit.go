package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

type securityAuditFinding struct {
	Severity       string `json:"severity"`
	Code           string `json:"code"`
	Title          string `json:"title"`
	Evidence       string `json:"evidence"`
	Recommendation string `json:"recommendation"`
}

func analyzeSecurityConfiguration(config string) []securityAuditFinding {
	findings := make([]securityAuditFinding, 0)
	for _, line := range strings.Split(config, "\n") {
		normalized := strings.ToLower(strings.TrimSpace(line))
		fields := strings.Fields(normalized)
		if len(fields) >= 3 && fields[0] == "snmp-server" && fields[1] == "community" && (fields[2] == "public" || fields[2] == "private") {
			findings = append(findings, securityAuditFinding{"critical", "snmp-default-community", "Wykryto domyślną nazwę community SNMP", "Konfiguracja zawiera community o nazwie " + fields[2] + ".", "Zastąp community unikalną wartością lub przejdź na SNMPv3; sprawdź urządzenia monitorujące przed zmianą."})
		}
		if normalized == "ip http server" || normalized == "http server enable" {
			findings = append(findings, securityAuditFinding{"warning", "http-management-enabled", "W konfiguracji jawnie włączono serwer HTTP", "Wykryto wpis: " + strings.TrimSpace(line), "Zweryfikuj, czy panel HTTP jest potrzebny. Jeśli nie, wyłącz go po zapewnieniu dostępu przez bezpieczny kanał zarządzania."})
		}
		if normalized == "ip telnet server" || normalized == "telnet server enable" {
			findings = append(findings, securityAuditFinding{"critical", "telnet-management-enabled", "W konfiguracji jawnie włączono Telnet", "Wykryto wpis: " + strings.TrimSpace(line), "Preferuj SSH. Wyłącz Telnet dopiero po potwierdzeniu działającego dostępu SSH."})
		}
		if normalized == "no spanning-tree" || normalized == "spanning-tree disable" {
			findings = append(findings, securityAuditFinding{"critical", "stp-disabled", "Wykryto jawne wyłączenie Spanning Tree", "W konfiguracji znaleziono: " + strings.TrimSpace(line), "Zweryfikuj topologię warstwy 2 i włącz właściwy tryb STP/RSTP po zaplanowaniu zmiany."})
		}
	}
	return findings
}

func parseAuditVLANSet(raw string) map[int]bool {
	out := map[int]bool{}
	for _, part := range strings.Split(strings.TrimSpace(raw), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if loRaw, hiRaw, ok := strings.Cut(part, "-"); ok {
			lo, e1 := strconv.Atoi(strings.TrimSpace(loRaw))
			hi, e2 := strconv.Atoi(strings.TrimSpace(hiRaw))
			if e1 == nil && e2 == nil && lo >= 1 && hi <= 4094 && hi >= lo && hi-lo <= 4093 {
				for n := lo; n <= hi; n++ {
					out[n] = true
				}
			}
		} else if n, err := strconv.Atoi(part); err == nil && n >= 1 && n <= 4094 {
			out[n] = true
		}
	}
	return out
}
func analyzeVLANAndPortSecurity(config, switchport string) []securityAuditFinding {
	findings := make([]securityAuditFinding, 0)
	declared := map[int]bool{}
	referenced := map[int]bool{}
	type ifaceConfig struct {
		name, mode, native, allowed, access string
		edgeport, lacp                      bool
	}
	interfaces := make([]ifaceConfig, 0)
	var current *ifaceConfig
	for _, line := range strings.Split(config, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "interface ") {
			interfaces = append(interfaces, ifaceConfig{name: strings.TrimSpace(trimmed[len("interface "):])})
			current = &interfaces[len(interfaces)-1]
			continue
		}
		if current != nil {
			fields := strings.Fields(lower)
			if len(fields) >= 2 && fields[0] == "switchport" && fields[1] == "mode" && len(fields) >= 3 {
				current.mode = fields[2]
			}
			if len(fields) >= 2 && fields[0] == "spanning-tree" && fields[1] == "edgeport" {
				current.edgeport = true
			}
			if len(fields) >= 2 && (fields[0] == "channel-group" || fields[0] == "static-channel-group") {
				current.lacp = true
			}
			if len(fields) >= 4 && fields[0] == "switchport" && fields[1] == "access" && fields[2] == "vlan" {
				current.access = fields[3]
			}
			if len(fields) >= 5 && fields[0] == "switchport" && fields[1] == "trunk" && fields[2] == "native" && fields[3] == "vlan" {
				current.native = fields[4]
			}
			if len(fields) >= 5 && fields[0] == "switchport" && fields[1] == "trunk" && fields[2] == "allowed" && fields[3] == "vlan" {
				value := strings.Join(fields[4:], "")
				if value == "none" {
					current.allowed = ""
				} else if strings.HasPrefix(value, "add") {
					current.allowed += strings.TrimPrefix(value, "add")
				} else if strings.HasPrefix(value, "remove") {
					remove := parseAuditVLANSet(strings.TrimPrefix(value, "remove"))
					keep := make([]string, 0)
					for _, v := range strings.Split(current.allowed, ",") {
						n, e := strconv.Atoi(v)
						if e != nil || !remove[n] {
							keep = append(keep, v)
						}
					}
					current.allowed = strings.Join(keep, ",")
				} else {
					current.allowed = value
				}
			}
		}
		if strings.HasPrefix(lower, "vlan ") {
			fields := strings.Fields(lower)
			if len(fields) == 2 {
				if n, e := strconv.Atoi(fields[1]); e == nil && n >= 1 && n <= 4094 {
					declared[n] = true
				}
			}
		}
	}
	for _, p := range interfaces {
		if p.edgeport && p.mode == "trunk" {
			findings = append(findings, securityAuditFinding{"warning", "edgeport-on-trunk", "EdgePort jest skonfigurowany na porcie trunk", p.name + ": spanning-tree edgeport oraz switchport mode trunk.", "Zweryfikuj rolę portu i topologię. EdgePort jest przeznaczony dla urządzeń końcowych; nie włączaj go na łączach między przełącznikami bez potwierdzenia projektu STP."})
		}
		if p.edgeport && p.lacp {
			findings = append(findings, securityAuditFinding{"warning", "edgeport-on-lacp-member", "EdgePort jest skonfigurowany na członku agregacji", p.name + ": spanning-tree edgeport oraz channel-group.", "Zweryfikuj agregację i sąsiedni switch. Nie traktuj członka LACP jako portu końcowego."})
		}
		if n, e := strconv.Atoi(p.access); e == nil && n >= 1 && n <= 4094 {
			referenced[n] = true
		}
		for n := range parseAuditVLANSet(p.allowed) {
			referenced[n] = true
		}
		if p.mode == "trunk" && p.native != "" && p.allowed != "" {
			native, e := strconv.Atoi(p.native)
			allowed := parseAuditVLANSet(p.allowed)
			if e == nil && !allowed[native] {
				findings = append(findings, securityAuditFinding{"warning", "trunk-native-not-allowed", "Native VLAN nie znajduje się na jawnej liście allowed", p.name + ": native VLAN " + p.native + "; allowed " + p.allowed, "Zweryfikuj zamierzony ruch tagowany i nietagowany. Dodaj native VLAN do allowed lub popraw native VLAN po sprawdzeniu konfiguracji sąsiada."})
			}
		}
	}
	if len(declared) > 0 {
		for vlan := range declared {
			if !referenced[vlan] && vlan != 1 {
				findings = append(findings, securityAuditFinding{"info", "vlan-no-explicit-port-reference", "VLAN bez jawnego przypisania w running-config", fmt.Sprintf("VLAN %d istnieje, ale parser nie znalazł jawnego przypisania access ani allowed trunk.", vlan), "Sprawdź członkostwo VLAN i tablicę MAC. To kandydat do weryfikacji, a nie dowód, że VLAN jest nieużywany."})
			}
		}
		for vlan := range referenced {
			if !declared[vlan] && vlan != 1 {
				findings = append(findings, securityAuditFinding{"warning", "port-references-undeclared-vlan", "Port odwołuje się do VLAN nieznajdującego się w deklaracjach", fmt.Sprintf("VLAN %d jest użyty w konfiguracji interfejsu, ale nie znaleziono deklaracji vlan %d.", vlan, vlan), "Sprawdź pełny running-config i stan bazy VLAN. Nie twórz ani nie usuwaj VLAN automatycznie."})
			}
		}
	}
	blocks := regexp.MustCompile(`(?im)^\s*Interface name\s*:\s*(\S+)\s*$`).FindAllStringSubmatchIndex(switchport, -1)
	for i, loc := range blocks {
		name := strings.TrimSpace(switchport[loc[2]:loc[3]])
		end := len(switchport)
		if i+1 < len(blocks) {
			end = blocks[i+1][0]
		}
		block := switchport[loc[0]:end]
		val := func(label string) string {
			re := regexp.MustCompile(`(?im)^\s*` + regexp.QuoteMeta(label) + `\s*:\s*(.*?)\s*$`)
			m := re.FindStringSubmatch(block)
			if len(m) > 1 {
				return strings.TrimSpace(m[1])
			}
			return ""
		}
		mode := strings.ToLower(val("Switchport mode"))
		nativeRaw := val("Default Vlan")
		configured := val("Configured Vlans")
		if mode == "trunk" && nativeRaw != "" && configured != "" {
			native, e := strconv.Atoi(nativeRaw)
			allowed := map[int]bool{}
			for _, token := range strings.Fields(configured) {
				if n, e := strconv.Atoi(strings.Trim(token, ",")); e == nil && n >= 1 && n <= 4094 {
					allowed[n] = true
				}
			}
			if e == nil && !allowed[native] {
				findings = append(findings, securityAuditFinding{"warning", "trunk-native-not-in-configured-vlans", "Native VLAN nie występuje w odczycie VLAN skonfigurowanych na trunku", name + ": native VLAN " + nativeRaw + "; configured VLANs " + configured, "Zweryfikuj listę VLAN na obu końcach łącza przed zmianą. Odczyt switchport i running-config mogą mieć różne formaty lub zakresy."})
			}
		}
	}
	return findings
}
func securityAuditHandler(read func(string) (string, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		config, err := read("show running-config")
		if err != nil {
			http.Error(w, "nie udało się odczytać running-config; audyt bezpieczeństwa nie został wykonany", http.StatusBadGateway)
			return
		}
		findings := analyzeSecurityConfiguration(config)
		checks := map[string]bool{"runningConfig": true}
		switchport, switchportErr := read("show interface switchport")
		if switchportErr == nil && strings.Contains(switchport, "Interface name") {
			findings = append(findings, analyzeVLANAndPortSecurity(config, switchport)...)
			checks["switchport"] = true
		} else {
			checks["switchport"] = false
		}
		if switchportErr != nil {
			findings = append(findings, securityAuditFinding{"info", "switchport-data-unavailable", "Brak odczytu szczegółów switchport", "Nie udało się pobrać lub rozpoznać show interface switchport.", "Ponów audyt; analiza spójności trunków jest niekompletna."})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"available": true, "readOnly": true, "findings": findings, "checks": checks, "note": "VLAN bez jawnego przypisania są kandydatami do weryfikacji, nie dowodem nieużywania. Wnioski trunk wymagają rozpoznanych pól switchport. Brak wykrycia nie jest dowodem bezpieczeństwa."})
	})
}
