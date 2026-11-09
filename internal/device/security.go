package device

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type PortSecuritySetting struct {
	Port      string `json:"port"`
	Maximum   string `json:"maximum"`
	Violation string `json:"violation"`
}

type ACL struct {
	ID    string   `json:"id"`
	Rules []string `json:"rules"`
	Ports []string `json:"ports"`
}

type SecurityInfo struct {
	PortSecurity    []PortSecuritySetting `json:"portSecurity"`
	DHCPSnooping    bool                  `json:"dhcpSnooping"`
	SnoopingVLANs   []string              `json:"snoopingVlans"`
	TrustedPorts    []string              `json:"trustedPorts"`
	VLANInterfaces  []string              `json:"vlanInterfaces"`
	ACLs            []ACL                 `json:"acls"`
	UnknownACLPorts []string              `json:"unknownAclPorts"`
}

var aclIDRE = regexp.MustCompile(`^(3[0-6]\d\d|[A-Za-z][A-Za-z0-9_-]{0,30})$`)

// ParseSecurity odczytuje ustawienia bezpieczeństwa z running-config.
func ParseSecurity(running string) SecurityInfo {
	info := SecurityInfo{PortSecurity: []PortSecuritySetting{}, SnoopingVLANs: []string{}, TrustedPorts: []string{}, VLANInterfaces: []string{}, ACLs: []ACL{}, UnknownACLPorts: []string{}}
	acls := map[string]*ACL{}
	var order []string
	getACL := func(id string) *ACL {
		if a, ok := acls[id]; ok {
			return a
		}
		a := &ACL{ID: id, Rules: []string{}, Ports: []string{}}
		acls[id] = a
		order = append(order, id)
		return a
	}
	var named *ACL
	for _, line := range strings.Split(running, "\n") {
		l := strings.TrimRight(line, "\r")
		f := strings.Fields(l)
		if l == "service dhcp-snooping" {
			info.DHCPSnooping = true
		}
		if strings.HasPrefix(l, "access-list ") && len(f) >= 3 {
			if f[1] == "hardware" {
				named = getACL(f[2])
				continue
			}
			getACL(f[1]).Rules = append(getACL(f[1]).Rules, strings.Join(f[2:], " "))
			named = nil
			continue
		}
		if named != nil && strings.HasPrefix(l, " ") && strings.TrimSpace(l) != "" {
			named.Rules = append(named.Rules, strings.TrimSpace(l))
			continue
		}
		named = nil
	}

	blocks := InterfaceBlocks(running)
	names := make([]string, 0, len(blocks))
	for n := range blocks {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return naturalLess(names[i], names[j]) })
	for _, name := range names {
		cmds := blocks[name]
		if m := vlanIfRE.FindStringSubmatch(name); m != nil {
			info.VLANInterfaces = append(info.VLANInterfaces, m[1])
			for _, c := range cmds {
				if c == "ip dhcp snooping" {
					info.SnoopingVLANs = append(info.SnoopingVLANs, m[1])
				}
			}
			continue
		}
		ps := PortSecuritySetting{Port: name}
		hasPS := false
		for _, c := range cmds {
			f := strings.Fields(c)
			switch {
			case c == "switchport port-security":
				hasPS = true
			case len(f) == 4 && f[0] == "switchport" && f[1] == "port-security" && f[2] == "maximum":
				ps.Maximum = f[3]
			case len(f) == 4 && f[0] == "switchport" && f[1] == "port-security" && f[2] == "violation":
				ps.Violation = f[3]
			case c == "ip dhcp snooping trust":
				info.TrustedPorts = append(info.TrustedPorts, name)
			case len(f) == 2 && f[0] == "access-group":
				if a, ok := acls[f[1]]; ok {
					a.Ports = append(a.Ports, name)
				} else {
					info.UnknownACLPorts = append(info.UnknownACLPorts, name+": "+f[1])
				}
			}
		}
		if hasPS {
			info.PortSecurity = append(info.PortSecurity, ps)
		}
	}
	for _, id := range order {
		info.ACLs = append(info.ACLs, *acls[id])
	}
	return info
}

func (s *Service) Security() (SecurityInfo, error) {
	running, err := s.RunReadOnly("show running-config")
	if err != nil {
		return SecurityInfo{}, err
	}
	return ParseSecurity(running), nil
}

// ---------- Polecenia ----------

func portCmds(ports []string, lines func(p string) []string) ([]string, error) {
	cmds := []string{"configure terminal"}
	for _, p := range ports {
		if !portRE.MatchString(p) {
			return nil, fmt.Errorf("nieprawidłowy port %q", p)
		}
		cmds = append(cmds, "interface "+p)
		cmds = append(cmds, lines(p)...)
	}
	return append(cmds, "end"), nil
}

// BuildPortSecurityCommands włącza ograniczenie liczby adresów MAC na portach albo je usuwa.
func BuildPortSecurityCommands(ports []string, maximum int, violation string, remove bool) ([]string, error) {
	if remove {
		return portCmds(ports, func(string) []string { return []string{"no switchport port-security"} })
	}
	if maximum < 1 || maximum > 256 {
		return nil, fmt.Errorf("liczba adresów MAC musi być z zakresu 1–256")
	}
	switch violation {
	case "protect", "restrict", "shutdown":
	default:
		return nil, fmt.Errorf("reakcja: protect, restrict albo shutdown")
	}
	return portCmds(ports, func(string) []string {
		return []string{"switchport port-security", "switchport port-security maximum " + strconv.Itoa(maximum), "switchport port-security violation " + violation}
	})
}

func BuildDHCPSnoopingGlobalCommands(enable bool) []string {
	if enable {
		return []string{"configure terminal", "service dhcp-snooping", "end"}
	}
	return []string{"configure terminal", "no service dhcp-snooping", "end"}
}

// BuildDHCPSnoopingVLANCommands włącza snooping w VLAN-ie. Włączenie bez żadnego
// zaufanego portu odcięłoby wszystkie urządzenia od serwera DHCP, więc jest blokowane.
func BuildDHCPSnoopingVLANCommands(vlan int, enable bool, info SecurityInfo) ([]string, error) {
	v := strconv.Itoa(vlan)
	exists := false
	for _, x := range info.VLANInterfaces {
		if x == v {
			exists = true
		}
	}
	if !exists {
		return nil, fmt.Errorf("VLAN %d nie ma interfejsu w konfiguracji switcha", vlan)
	}
	if enable && len(info.TrustedPorts) == 0 {
		return nil, fmt.Errorf("najpierw oznacz jako zaufany port, za którym jest serwer DHCP (zwykle uplink do routera); inaczej urządzenia w VLAN %d nie dostaną adresów", vlan)
	}
	cmd := "ip dhcp snooping"
	if !enable {
		cmd = "no " + cmd
	}
	return []string{"configure terminal", "interface vlan" + v, cmd, "end"}, nil
}

func BuildDHCPTrustCommands(ports []string, trust bool) ([]string, error) {
	cmd := "ip dhcp snooping trust"
	if !trust {
		cmd = "no " + cmd
	}
	return portCmds(ports, func(string) []string { return []string{cmd} })
}

func findACL(info SecurityInfo, id string) (ACL, bool) {
	for _, a := range info.ACLs {
		if a.ID == id {
			return a, true
		}
	}
	return ACL{}, false
}

// BuildACLApplyCommands przypisuje istniejący ACL do portów albo go z nich zdejmuje.
func BuildACLApplyCommands(ports []string, id string, remove bool, info SecurityInfo) ([]string, error) {
	if !aclIDRE.MatchString(id) {
		return nil, fmt.Errorf("nieprawidłowy identyfikator ACL")
	}
	if _, ok := findACL(info, id); !ok && !remove {
		return nil, fmt.Errorf("ACL %s nie istnieje", id)
	}
	cmd := "access-group " + id
	if remove {
		cmd = "no " + cmd
	}
	return portCmds(ports, func(string) []string { return []string{cmd} })
}

// aclAddress akceptuje "any", "A.B.C.D" (jeden host) albo "A.B.C.D/M".
func aclAddress(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "any" || raw == "" {
		return "any", nil
	}
	if ip := net.ParseIP(raw); ip != nil && ip.To4() != nil {
		return raw + "/32", nil
	}
	ip, n, err := net.ParseCIDR(raw)
	if err != nil || ip.To4() == nil {
		return "", fmt.Errorf("adres podaj jako any, A.B.C.D albo A.B.C.D/M")
	}
	return n.String(), nil
}

// BuildACLRuleCommands dodaje lub usuwa regułę IP w numerowanym ACL 3000–3699.
func BuildACLRuleCommands(id int, action, src, dst string, remove bool, info SecurityInfo) ([]string, error) {
	if id < 3000 || id > 3699 {
		return nil, fmt.Errorf("numer ACL dla reguł IP: 3000–3699")
	}
	if action != "permit" && action != "deny" {
		return nil, fmt.Errorf("akcja: permit albo deny")
	}
	s, err := aclAddress(src)
	if err != nil {
		return nil, fmt.Errorf("źródło: %w", err)
	}
	d, err := aclAddress(dst)
	if err != nil {
		return nil, fmt.Errorf("cel: %w", err)
	}
	rule := action + " ip " + s + " " + d
	acl, exists := findACL(info, strconv.Itoa(id))
	present := false
	for _, r := range acl.Rules {
		if r == rule {
			present = true
		}
	}
	if remove && !present {
		return nil, fmt.Errorf("w ACL %d nie ma reguły %q", id, rule)
	}
	if !remove && present {
		return nil, fmt.Errorf("reguła już istnieje")
	}
	if remove && exists && len(acl.Rules) == 1 && len(acl.Ports) > 0 {
		return nil, fmt.Errorf("to ostatnia reguła ACL %d przypisanego do portów %s; najpierw zdejmij ACL z portów", id, strings.Join(acl.Ports, ", "))
	}
	line := "access-list " + strconv.Itoa(id) + " " + rule
	if remove {
		line = "no " + line
	}
	return []string{"configure terminal", line, "end"}, nil
}

// ---------- Wykonanie (zawsze na świeżym running-config) ----------

func (s *Service) freshSecurity() (SecurityInfo, error) {
	running, err := s.RunReadOnlyFresh("show running-config")
	if err != nil {
		return SecurityInfo{}, err
	}
	return ParseSecurity(running), nil
}

func (s *Service) ConfigurePortSecurity(rawPorts string, maximum int, violation string, remove bool) (string, error) {
	ports, err := ParsePortRanges(rawPorts)
	if err != nil {
		return "", err
	}
	cmds, err := BuildPortSecurityCommands(ports, maximum, violation, remove)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

func (s *Service) ConfigureDHCPSnoopingGlobal(enable bool) (string, error) {
	return s.runConfig(BuildDHCPSnoopingGlobalCommands(enable))
}

func (s *Service) ConfigureDHCPSnoopingVLAN(vlan int, enable bool) (string, error) {
	info, err := s.freshSecurity()
	if err != nil {
		return "", err
	}
	cmds, err := BuildDHCPSnoopingVLANCommands(vlan, enable, info)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

func (s *Service) ConfigureDHCPTrust(rawPorts string, trust bool) (string, error) {
	ports, err := ParsePortRanges(rawPorts)
	if err != nil {
		return "", err
	}
	if !trust {
		info, err := s.freshSecurity()
		if err != nil {
			return "", err
		}
		remaining := 0
		for _, t := range info.TrustedPorts {
			keep := true
			for _, p := range ports {
				if p == t {
					keep = false
				}
			}
			if keep {
				remaining++
			}
		}
		if remaining == 0 && len(info.SnoopingVLANs) > 0 {
			return "", fmt.Errorf("to ostatnie zaufane porty, a DHCP snooping działa w VLAN %s; najpierw wyłącz tam snooping", strings.Join(info.SnoopingVLANs, ", "))
		}
	}
	cmds, err := BuildDHCPTrustCommands(ports, trust)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

func (s *Service) ConfigureACLApply(rawPorts, id string, remove bool) (string, error) {
	ports, err := ParsePortRanges(rawPorts)
	if err != nil {
		return "", err
	}
	info, err := s.freshSecurity()
	if err != nil {
		return "", err
	}
	cmds, err := BuildACLApplyCommands(ports, id, remove, info)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

func (s *Service) ConfigureACLRule(id int, action, src, dst string, remove bool) (string, error) {
	info, err := s.freshSecurity()
	if err != nil {
		return "", err
	}
	cmds, err := BuildACLRuleCommands(id, action, src, dst, remove, info)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}
