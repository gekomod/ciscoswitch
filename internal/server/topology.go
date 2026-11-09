package server

import (
	"encoding/json"
	"fmt"
	"gs948mx-manager/internal/device"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type topologyPort struct {
	Port        string `json:"port"`
	Mode        string `json:"mode,omitempty"`
	VLANs       []int  `json:"vlans,omitempty"`
	NativeVLAN  int    `json:"nativeVlan,omitempty"`
	LAG         string `json:"lag,omitempty"`
	EdgePort    bool   `json:"edgePort,omitempty"`
	Description string `json:"description,omitempty"`
	Role        string `json:"role,omitempty"`
	Protected   bool   `json:"protected"`
}
type topologyLink struct {
	LocalPort string `json:"localPort"`
	Neighbor  string `json:"neighbor"`
	Evidence  string `json:"evidence"`
}
type topologyAggregation struct {
	Name           string   `json:"name"`
	Members        []string `json:"members"`
	LiveMembers    []string `json:"liveMembers,omitempty"`
	Live           bool     `json:"live"`
	MemberMismatch bool     `json:"memberMismatch"`
	Partner        string   `json:"partner,omitempty"`
	Ready          string   `json:"ready,omitempty"`
}
type topologyRisk struct {
	Severity       string `json:"severity"`
	Code           string `json:"code"`
	Title          string `json:"title"`
	Evidence       string `json:"evidence"`
	Recommendation string `json:"recommendation"`
}

type topologyImpact struct {
	Code                  string   `json:"code"`
	Subject               string   `json:"subject"`
	ConfiguredPorts       []string `json:"configuredPorts,omitempty"`
	LivePorts             []string `json:"livePorts,omitempty"`
	VLANs                 []int    `json:"vlans,omitempty"`
	Neighbors             []string `json:"neighbors,omitempty"`
	VLANEvidenceAvailable bool     `json:"vlanEvidenceAvailable"`
	Note                  string   `json:"note"`
}

func buildTopologyImpacts(ports []topologyPort, links []topologyLink, aggregations []topologyAggregation, risks []topologyRisk) []topologyImpact {
	portByName := make(map[string]topologyPort, len(ports))
	for _, p := range ports {
		portByName[strings.ToLower(p.Port)] = p
	}
	neighborsByPort := make(map[string][]string)
	for _, link := range links {
		key := strings.ToLower(link.LocalPort)
		neighborsByPort[key] = append(neighborsByPort[key], link.Neighbor)
	}
	impacts := make([]topologyImpact, 0)
	add := func(code, subject string, configured, live []string) {
		vlanSet := map[int]bool{}
		vlanEvidence := false
		neighborSet := map[string]bool{}
		for _, name := range append(append([]string(nil), configured...), live...) {
			p, ok := portByName[strings.ToLower(name)]
			if !ok {
				continue
			}
			if len(p.VLANs) > 0 || p.NativeVLAN > 0 {
				vlanEvidence = true
			}
			for _, v := range p.VLANs {
				if v > 0 {
					vlanSet[v] = true
				}
			}
			if p.NativeVLAN > 0 {
				vlanSet[p.NativeVLAN] = true
			}
			for _, n := range neighborsByPort[strings.ToLower(name)] {
				neighborSet[n] = true
			}
		}
		vlans := make([]int, 0, len(vlanSet))
		for v := range vlanSet {
			vlans = append(vlans, v)
		}
		sort.Ints(vlans)
		neighborList := make([]string, 0, len(neighborSet))
		for n := range neighborSet {
			neighborList = append(neighborList, n)
		}
		sort.Strings(neighborList)
		impacts = append(impacts, topologyImpact{Code: code, Subject: subject, ConfiguredPorts: append([]string(nil), configured...), LivePorts: append([]string(nil), live...), VLANs: vlans, Neighbors: neighborList, VLANEvidenceAvailable: vlanEvidence, Note: "To potencjalny zakres wpływu wynikający z rozpoznanej konfiguracji i odczytów. Nie potwierdza przerwania ruchu ani pełnej ścieżki end-to-end."})
	}
	for _, risk := range risks {
		switch risk.Code {
		case "lag-single-member", "lag-member-mismatch", "lag-not-ready":
			for _, lag := range aggregations {
				if strings.HasPrefix(strings.ToLower(risk.Evidence), strings.ToLower(lag.Name)) {
					add(risk.Code, lag.Name, lag.Members, lag.LiveMembers)
					break
				}
			}
		case "single-explicit-uplink":
			for _, p := range ports {
				if p.Role == "Uplink / switch" {
					add(risk.Code, p.Port, []string{p.Port}, nil)
					break
				}
			}
		}
	}
	return impacts
}

func analyzeTopologyRisks(ports []topologyPort, links []topologyLink, aggregations []topologyAggregation, lldpAvailable bool) []topologyRisk {
	risks := make([]topologyRisk, 0)
	explicitUplinks := make([]topologyPort, 0)
	neighbors := map[string]bool{}
	for _, link := range links {
		neighbors[strings.ToLower(link.LocalPort)] = true
	}
	for _, port := range ports {
		if port.Role == "Uplink / switch" {
			explicitUplinks = append(explicitUplinks, port)
		}
		if port.Protected {
			risks = append(risks, topologyRisk{"info", "protected-port", "Port objęty ochroną zmian", port.Port + " znajduje się na liście PROTECTED_PORTS.", "Nie zmieniaj tego portu bez zapewnienia alternatywnego dostępu i świadomej procedury serwisowej."})
		}
		if port.Role == "Uplink / switch" && lldpAvailable && !neighbors[strings.ToLower(port.Port)] {
			risks = append(risks, topologyRisk{"info", "uplink-no-lldp", "Brak potwierdzenia LLDP dla uplinku", port.Port + " jest opisany jako uplink/switch, ale nie ma rozpoznanego wpisu LLDP.", "Zweryfikuj sąsiedztwo inną metodą. Brak LLDP nie oznacza braku fizycznego łącza."})
		}
	}
	if len(explicitUplinks) == 1 {
		p := explicitUplinks[0]
		risks = append(risks, topologyRisk{"warning", "single-explicit-uplink", "Jeden jawnie opisany uplink — kandydat na pojedynczy punkt awarii", p.Port + " jest jedynym portem rozpoznanym po opisie jako uplink/switch.", "Sprawdź topologię po drugiej stronie i ścieżki zapasowe. Sam opis portu nie potwierdza całej redundancji sieci."})
	}
	for _, lag := range aggregations {
		if len(lag.Members) == 1 {
			risks = append(risks, topologyRisk{"warning", "lag-single-member", "Agregacja ma jednego skonfigurowanego członka", lag.Name + " zawiera tylko " + lag.Members[0] + ".", "Zweryfikuj, czy drugi tor powinien należeć do agregacji. Jeden członek nie zapewnia redundancji łącza."})
		}
		if lag.Live && lag.MemberMismatch {
			risks = append(risks, topologyRisk{"warning", "lag-member-mismatch", "Niespójność członków LACP", lag.Name + ": konfiguracja [" + strings.Join(lag.Members, ", ") + "] różni się od rozpoznanych aktywnych członków [" + strings.Join(lag.LiveMembers, ", ") + "].", "Sprawdź stan LACP i konfigurację obu końców agregacji przed zmianami."})
		}
		if lag.Live && lag.Ready == "Niegotowy" {
			risks = append(risks, topologyRisk{"warning", "lag-not-ready", "Agregacja LACP nie jest gotowa", lag.Name + " zgłasza stan Ready=0.", "Sprawdź zgodność trybu LACP, partnera, członków i fizyczne linki."})
		}
	}
	if len(explicitUplinks) == 0 {
		risks = append(risks, topologyRisk{"info", "uplink-role-unknown", "Nie rozpoznano jawnie opisanych uplinków", "Żaden port nie zawiera w opisie jednoznacznej etykiety uplink/switch.", "Uzupełnij opisy portów lub zweryfikuj topologię ręcznie. Nie oznacza to, że switch nie ma uplinków."})
	}
	return risks
}
func parseTopologyConfig(config, switchport string) ([]topologyPort, []topologyAggregation, []int) {
	config = device.NormalizeInterfaceRanges(config)
	ports := map[string]*topologyPort{}
	lags := map[string]*topologyAggregation{}
	vlans := map[int]bool{}
	var current *topologyPort
	for _, line := range strings.Split(config, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "interface ") {
			name := strings.TrimSpace(line[len("interface "):])
			if !regexp.MustCompile(`(?i)^port[0-9.]+$`).MatchString(name) {
				current = nil
				continue
			}
			current = &topologyPort{Port: name, VLANs: []int{}}
			ports[strings.ToLower(name)] = current
			continue
		}
		if strings.HasPrefix(lower, "vlan ") {
			f := strings.Fields(lower)
			if len(f) == 2 {
				if n, e := strconv.Atoi(f[1]); e == nil && n >= 1 && n <= 4094 {
					vlans[n] = true
				}
			}
		}
		if current == nil {
			continue
		}
		f := strings.Fields(lower)
		if len(f) >= 3 && f[0] == "switchport" && f[1] == "mode" {
			current.Mode = f[2]
		}
		if len(f) >= 4 && f[0] == "switchport" && f[1] == "access" && f[2] == "vlan" {
			if n, e := strconv.Atoi(f[3]); e == nil && n >= 1 && n <= 4094 {
				current.VLANs = append(current.VLANs, n)
			}
		}
		if len(f) >= 5 && f[0] == "switchport" && f[1] == "trunk" && f[2] == "native" && f[3] == "vlan" {
			current.NativeVLAN, _ = strconv.Atoi(f[4])
		}
		if len(f) >= 2 && f[0] == "spanning-tree" && f[1] == "edgeport" {
			current.EdgePort = true
		}
		if len(f) >= 2 && f[0] == "description" {
			current.Description = strings.TrimSpace(line[len(f[0]):])
		}
		if (len(f) >= 3 && f[0] == "channel-group") || (len(f) >= 2 && f[0] == "static-channel-group") {
			name := "Po" + f[1]
			if f[0] == "static-channel-group" {
				name = "sa" + f[1]
			}
			current.LAG = name
			key := strings.ToLower(name)
			if lags[key] == nil {
				lags[key] = &topologyAggregation{Name: name, Members: []string{}}
			}
			lags[key].Members = append(lags[key].Members, current.Port)
		}
	}
	// Prefer the switch's parsed switchport view for operationally visible VLAN membership.
	blocks := regexp.MustCompile(`(?im)^\s*Interface name\s*:\s*(\S+)\s*$`).FindAllStringSubmatchIndex(switchport, -1)
	for i, loc := range blocks {
		name := strings.TrimSpace(switchport[loc[2]:loc[3]])
		p := ports[strings.ToLower(name)]
		if p == nil {
			p = &topologyPort{Port: name, VLANs: []int{}}
			ports[strings.ToLower(name)] = p
		}
		end := len(switchport)
		if i+1 < len(blocks) {
			end = blocks[i+1][0]
		}
		block := switchport[loc[0]:end]
		read := func(label string) string {
			re := regexp.MustCompile(`(?im)^\s*` + regexp.QuoteMeta(label) + `\s*:\s*(.*?)\s*$`)
			m := re.FindStringSubmatch(block)
			if len(m) > 1 {
				return strings.TrimSpace(m[1])
			}
			return ""
		}
		if mode := read("Switchport mode"); mode != "" {
			p.Mode = strings.ToLower(mode)
		}
		if n, e := strconv.Atoi(read("Default Vlan")); e == nil && n > 0 {
			p.NativeVLAN = n
		}
		if raw := device.SwitchportVLANField(block, "Configured Vlans"); raw != "" {
			set := parseAuditVLANSet(strings.ReplaceAll(raw, " ", ","))
			vals := make([]int, 0, len(set))
			for n := range set {
				vals = append(vals, n)
			}
			sort.Ints(vals)
			if len(vals) > 0 {
				p.VLANs = vals
			}
		}
	}
	out := make([]topologyPort, 0, len(ports))
	for _, p := range ports {
		desc := strings.ToLower(p.Description)
		switch {
		case strings.Contains(desc, "uplink") || strings.Contains(desc, "switch"):
			p.Role = "Uplink / switch"
		case strings.Contains(desc, "access point") || strings.Contains(desc, " ap"):
			p.Role = "Access Point"
		case strings.Contains(desc, "cctv") || strings.Contains(desc, "kamera"):
			p.Role = "CCTV"
		case strings.Contains(desc, "voip") || strings.Contains(desc, "telefon"):
			p.Role = "VoIP"
		case strings.Contains(desc, "server") || strings.Contains(desc, "storage"):
			p.Role = "Server"
		}
		sort.Ints(p.VLANs)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	agg := make([]topologyAggregation, 0, len(lags))
	for _, l := range lags {
		sort.Strings(l.Members)
		agg = append(agg, *l)
	}
	sort.Slice(agg, func(i, j int) bool { return agg[i].Name < agg[j].Name })
	vlanList := make([]int, 0, len(vlans))
	for n := range vlans {
		vlanList = append(vlanList, n)
	}
	sort.Ints(vlanList)
	return out, agg, vlanList
}
func parseTopologyLLDP(raw string) []topologyLink {
	links := make([]topologyLink, 0)
	seen := map[string]bool{}
	portRE := regexp.MustCompile(`(?i)^port[0-9.]+$`)
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || !portRE.MatchString(fields[0]) {
			continue
		}
		joined := strings.Join(fields[1:], " ")
		lower := strings.ToLower(joined)
		if strings.Contains(lower, "local interface") || strings.Contains(lower, "neighbor") || strings.Contains(lower, "port id") || strings.Contains(lower, "system name") {
			continue
		}
		key := strings.ToLower(fields[0] + "|" + joined)
		if seen[key] {
			continue
		}
		seen[key] = true
		links = append(links, topologyLink{fields[0], joined, "LLDP"})
	}
	return links
}
func parseTopologyLACP(raw string) []topologyAggregation {
	groups := make([]topologyAggregation, 0)
	var current *topologyAggregation
	for _, line := range strings.Split(raw, "\n") {
		if m := regexp.MustCompile(`(?i)^\s*Aggregator\s+(po[0-9]+)\b`).FindStringSubmatch(line); len(m) > 1 {
			groups = append(groups, topologyAggregation{Name: m[1], Members: []string{}})
			current = &groups[len(groups)-1]
			continue
		}
		if current == nil {
			continue
		}
		if m := regexp.MustCompile(`(?i)^\s*Partner LAG:\s*(.+)$`).FindStringSubmatch(line); len(m) > 1 {
			current.Partner = strings.TrimSpace(m[1])
		}
		if m := regexp.MustCompile(`(?i)^\s*Individual:\s*\d+\s*-\s*Ready:\s*(\d+)`).FindStringSubmatch(line); len(m) > 1 {
			if m[1] == "1" {
				current.Ready = "Gotowy"
			} else {
				current.Ready = "Niegotowy"
			}
		}
		if m := regexp.MustCompile(`(?i)^\s*Link:\s*(port[0-9.]+)`).FindStringSubmatch(line); len(m) > 1 {
			current.Members = append(current.Members, m[1])
		}
	}
	return groups
}
func topologyChangeRisksFromSnapshot(config, switchport, lldpRaw string, configErr, lldpErr error, port string) []topologyRisk {
	risks := make([]topologyRisk, 0)
	if configErr != nil {
		return []topologyRisk{{"info", "topology-check-unavailable", "Niepełna analiza wpływu zmiany", "Nie udało się odczytać running-config; rola portu i członkostwo LAG nie zostały potwierdzone.", "Przed zmianą sprawdź topologię ręcznie. Podgląd nie może potwierdzić, że port jest bezpieczny."}}
	}
	ports, lags, _ := parseTopologyConfig(config, switchport)
	links := parseTopologyLLDP(lldpRaw)
	target := topologyPort{}
	found := false
	for _, item := range ports {
		if strings.EqualFold(item.Port, port) {
			target = item
			found = true
			break
		}
	}
	if !found {
		risks = append(risks, topologyRisk{"info", "port-role-unknown", "Nie udało się ustalić roli portu", port + " nie został jednoznacznie rozpoznany w running-config.", "Zweryfikuj przeznaczenie portu i połączenia przed zatwierdzeniem zmiany."})
	}
	if target.Role == "Uplink / switch" {
		risks = append(risks, topologyRisk{"warning", "change-uplink-risk", "Zmiana dotyczy portu uplink/switch", port + " ma jawny opis wskazujący na uplink lub połączenie switch-switch.", "Zmiana VLAN lub trybu może odciąć dalszą część sieci. Zapewnij alternatywny dostęp i sprawdź konfigurację urządzenia po drugiej stronie."})
	}
	for _, link := range links {
		if strings.EqualFold(link.LocalPort, port) {
			risks = append(risks, topologyRisk{"warning", "change-lldp-neighbor", "Port ma rozpoznanego sąsiada LLDP", port + " prowadzi do: " + link.Neighbor + ".", "Przed zmianą oceń wpływ na urządzenie po drugiej stronie i dostęp do zarządzania."})
			break
		}
	}
	if target.LAG != "" {
		members := []string{}
		for _, lag := range lags {
			if strings.EqualFold(lag.Name, target.LAG) {
				members = lag.Members
				break
			}
		}
		risks = append(risks, topologyRisk{"warning", "change-lacp-member", "Zmiana dotyczy członka agregacji LACP", port + " należy do " + target.LAG + "; członkowie skonfigurowani: " + strings.Join(members, ", ") + ".", "Zmiana VLAN lub trybu może naruszyć spójność agregacji albo przerwać ruch. Sprawdź konfigurację wszystkich członków i partnera."})
	}
	if isProtectedPort(port) {
		risks = append(risks, topologyRisk{"critical", "change-protected-port", "Port jest objęty ochroną", port + " znajduje się na liście PROTECTED_PORTS.", "Operacja powinna zostać zablokowana; użyj alternatywnego portu zarządzającego."})
	}
	if lldpErr != nil || strings.TrimSpace(lldpRaw) == "" {
		risks = append(risks, topologyRisk{"info", "change-lldp-unavailable", "Sąsiedztwo LLDP niedostępne", "Nie udało się uzyskać wiarygodnego odczytu LLDP.", "Nie traktuj braku LLDP jako dowodu, że port nie prowadzi do innego urządzenia."})
	}
	return risks
}
func requireTopologyRiskAcknowledgement(read func(string) (string, error), ports []string, acknowledged bool) error {
	config, configErr := read("show running-config")
	sw, _ := read("show interface switchport")
	lldp, lldpErr := read("show lldp neighbors")
	if configErr != nil {
		if !acknowledged {
			return fmt.Errorf("nie można potwierdzić topologii; ponów podgląd i jawnie potwierdź ryzyko")
		}
		return nil
	}
	for _, port := range ports {
		risks := topologyChangeRisksFromSnapshot(config, sw, lldp, configErr, lldpErr, port)
		if len(risks) > 0 && !acknowledged {
			return fmt.Errorf("port %s ma ostrzeżenia topologii; wymagany jest podgląd i jawne potwierdzenie ryzyka", port)
		}
	}
	return nil
}

func topologyChangeRisks(read func(string) (string, error), port string) []topologyRisk {
	config, configErr := read("show running-config")
	sw, _ := read("show interface switchport")
	lldp, lldpErr := read("show lldp neighbors")
	return topologyChangeRisksFromSnapshot(config, sw, lldp, configErr, lldpErr, port)
}

func topologyMapHandler(read func(string) (string, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		config, err := read("show running-config")
		if err != nil {
			http.Error(w, "brak running-config; mapa topologii nie została wygenerowana", http.StatusBadGateway)
			return
		}
		sw, swErr := read("show interface switchport")
		ports, lags, vlans := parseTopologyConfig(config, sw)
		lldp, lldpErr := read("show lldp neighbors")
		links := parseTopologyLLDP(lldp)
		lacpRaw, lacpErr := read("show etherchannel detail")
		liveLags := parseTopologyLACP(lacpRaw)
		for i := range lags {
			for _, live := range liveLags {
				if !strings.EqualFold(lags[i].Name, live.Name) {
					continue
				}
				lags[i].Live = true
				lags[i].Partner = live.Partner
				lags[i].Ready = live.Ready
				lags[i].LiveMembers = append([]string(nil), live.Members...)
				configured := map[string]bool{}
				for _, member := range lags[i].Members {
					configured[strings.ToLower(member)] = true
				}
				active := map[string]bool{}
				for _, member := range live.Members {
					active[strings.ToLower(member)] = true
				}
				if len(active) > 0 {
					if len(configured) != len(active) {
						lags[i].MemberMismatch = true
					} else {
						for member := range configured {
							if !active[member] {
								lags[i].MemberMismatch = true
								break
							}
						}
					}
				}
			}
		}
		for i := range ports {
			ports[i].Protected = isProtectedPort(ports[i].Port)
		}
		lldpLower := strings.ToLower(lldp)
		lldpSupported := lldpErr == nil && strings.TrimSpace(lldp) != "" && !strings.Contains(lldpLower, "invalid command") && !strings.Contains(lldpLower, "invalid input") && !strings.Contains(lldpLower, "unknown command") && !strings.Contains(lldpLower, "unrecognized") && !strings.Contains(lldpLower, "not supported")
		checks := map[string]bool{"runningConfig": true, "switchport": swErr == nil && strings.Contains(sw, "Interface name"), "lldp": lldpSupported && len(links) > 0, "lacp": lacpErr == nil && len(liveLags) > 0}
		risks := analyzeTopologyRisks(ports, links, lags, lldpSupported)
		configuredGroups := map[string]bool{}
		for _, lag := range lags {
			configuredGroups[strings.ToLower(lag.Name)] = true
		}
		for _, live := range liveLags {
			if !configuredGroups[strings.ToLower(live.Name)] {
				risks = append(risks, topologyRisk{"info", "lacp-live-unconfigured", "Agregacja LACP bez rozpoznanej deklaracji konfiguracji", live.Name + " jest widoczna w odczycie operacyjnym, ale nie została rozpoznana w running-config.", "Zweryfikuj format konfiguracji i stan agregacji. Parser może nie rozpoznawać wszystkich wariantów składni firmware."})
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		impacts := buildTopologyImpacts(ports, links, lags, risks)
		_ = json.NewEncoder(w).Encode(map[string]any{"available": true, "readOnly": true, "ports": ports, "links": links, "aggregations": lags, "vlans": vlans, "risks": risks, "impacts": impacts, "checks": checks, "note": "Ryzyka są wskazówkami opartymi wyłącznie na rozpoznanych opisach i danych CLI. Potencjalny wpływ na porty i VLAN-y jest wyliczany tylko z rozpoznanych członków LACP, konfiguracji switchport i LLDP; nie potwierdza przerwania ruchu ani pełnej ścieżki end-to-end. Brak LLDP nie oznacza braku fizycznego łącza."})
	})
}
