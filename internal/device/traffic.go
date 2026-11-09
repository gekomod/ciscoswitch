package device

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---------- Wspólne: bloki interfejsów z running-config ----------

var ifaceRangeRE = regexp.MustCompile(`^port(\d+)\.(\d+)\.(\d+)(?:-(?:port)?(\d+)\.(\d+)\.(\d+))?$`)

// ExpandInterfaces rozwija nazwę z nagłówka "interface ...", np.
// "port1.0.1-1.0.4,port1.0.7" -> port1.0.1 ... port1.0.4, port1.0.7.
// Nazwy inne niż porty (vlan10, sa1, po1) zwraca bez zmian.
func ExpandInterfaces(spec string) []string {
	var out []string
	for _, part := range strings.Split(strings.TrimSpace(spec), ",") {
		part = strings.TrimSpace(part)
		m := ifaceRangeRE.FindStringSubmatch(part)
		if m == nil {
			if part != "" {
				out = append(out, part)
			}
			continue
		}
		if m[4] == "" {
			out = append(out, part)
			continue
		}
		from, _ := strconv.Atoi(m[3])
		to, _ := strconv.Atoi(m[6])
		if m[1] != m[4] || m[2] != m[5] || to < from || to-from > 64 {
			out = append(out, part)
			continue
		}
		for n := from; n <= to; n++ {
			out = append(out, fmt.Sprintf("port%s.%s.%d", m[1], m[2], n))
		}
	}
	return out
}

// NormalizeInterfaceRanges przepisuje running-config tak, by każdy blok
// "interface" dotyczył jednego interfejsu: "interface port1.0.1-1.0.3" z treścią
// staje się trzema blokami. AW+ zwija porty o identycznej konfiguracji w zakresy,
// a starsze parsery panelu oczekują pojedynczych nazw.
func NormalizeInterfaceRanges(running string) string {
	lines := strings.Split(running, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		if !strings.HasPrefix(l, "interface ") {
			out = append(out, l)
			continue
		}
		names := ExpandInterfaces(strings.TrimPrefix(l, "interface "))
		j := i + 1
		for j < len(lines) && strings.HasPrefix(lines[j], " ") {
			j++
		}
		body := lines[i+1 : j]
		for k, n := range names {
			if k > 0 {
				out = append(out, "!")
			}
			out = append(out, "interface "+n)
			for _, b := range body {
				out = append(out, strings.TrimRight(b, "\r"))
			}
		}
		i = j - 1
	}
	return strings.Join(out, "\n")
}

// InterfaceBlocks zwraca polecenia z bloków "interface X" running-config,
// przypisane do każdego interfejsu po rozwinięciu zakresów.
func InterfaceBlocks(running string) map[string][]string {
	out := map[string][]string{}
	var current []string
	for _, line := range strings.Split(running, "\n") {
		l := strings.TrimRight(line, "\r")
		if strings.HasPrefix(l, "interface ") {
			current = ExpandInterfaces(strings.TrimPrefix(l, "interface "))
			for _, n := range current {
				if _, ok := out[n]; !ok {
					out[n] = nil
				}
			}
			continue
		}
		t := strings.TrimSpace(l)
		if t == "!" || (l != "" && !strings.HasPrefix(l, " ")) {
			current = nil
			continue
		}
		if t == "" {
			continue
		}
		for _, n := range current {
			out[n] = append(out[n], t)
		}
	}
	return out
}

// ParsePortRanges czyta listę portów od użytkownika: "1-8,12,49".
func ParsePortRanges(raw string) ([]string, error) {
	seen := map[int]bool{}
	var nums []int
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi := part, part
		if i := strings.Index(part, "-"); i > 0 {
			lo, hi = strings.TrimSpace(part[:i]), strings.TrimSpace(part[i+1:])
		}
		a, e1 := strconv.Atoi(lo)
		b, e2 := strconv.Atoi(hi)
		if e1 != nil || e2 != nil || a < 1 || b > 52 || a > b {
			return nil, fmt.Errorf("porty podaj jako numery 1–52, np. 1-8,12,49")
		}
		for n := a; n <= b; n++ {
			if !seen[n] {
				seen[n] = true
				nums = append(nums, n)
			}
		}
	}
	if len(nums) == 0 {
		return nil, fmt.Errorf("podaj co najmniej jeden port")
	}
	sort.Ints(nums)
	out := make([]string, len(nums))
	for i, n := range nums {
		out[i] = "port1.0." + strconv.Itoa(n)
	}
	return out, nil
}

// ---------- Stan ochrony ruchu ----------

type StormSetting struct {
	Port  string `json:"port"`
	Type  string `json:"type"` // broadcast | multicast | dlf
	Level string `json:"level"`
}

type LoopPort struct {
	Port   string `json:"port"`
	Action string `json:"action"`
}

type TrafficInfo struct {
	Storm             []StormSetting `json:"storm"`
	LoopDetect        bool           `json:"loopDetect"`
	LoopDetectLine    string         `json:"loopDetectLine"`
	LoopPorts         []LoopPort     `json:"loopPorts"`
	VLANs             []VLANSnooping `json:"vlans"`
	IGMPGlobalDisable bool           `json:"igmpGlobalDisabled"`
}

type VLANSnooping struct {
	VLAN     string `json:"vlan"`
	Snooping string `json:"snooping"` // disabled | enabled | default
}

var vlanIfRE = regexp.MustCompile(`^vlan(\d{1,4})$`)

func ParseTraffic(running string) TrafficInfo {
	info := TrafficInfo{Storm: []StormSetting{}, LoopPorts: []LoopPort{}, VLANs: []VLANSnooping{}}
	for _, line := range strings.Split(running, "\n") {
		l := strings.TrimRight(line, "\r")
		if strings.HasPrefix(l, "loop-protection loop-detect") {
			info.LoopDetect, info.LoopDetectLine = true, l
		}
		if l == "no ip igmp snooping" {
			info.IGMPGlobalDisable = true
		}
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
			state := "default"
			for _, c := range cmds {
				if c == "no ip igmp snooping" {
					state = "disabled"
				} else if c == "ip igmp snooping" {
					state = "enabled"
				}
			}
			info.VLANs = append(info.VLANs, VLANSnooping{VLAN: m[1], Snooping: state})
			continue
		}
		for _, c := range cmds {
			f := strings.Fields(c)
			if len(f) >= 4 && f[0] == "storm-control" && f[2] == "level" {
				info.Storm = append(info.Storm, StormSetting{Port: name, Type: f[1], Level: f[3]})
			}
			if len(f) >= 3 && f[0] == "loop-protection" && f[1] == "action" {
				info.LoopPorts = append(info.LoopPorts, LoopPort{Port: name, Action: f[2]})
			}
		}
	}
	return info
}

var naturalNumRE = regexp.MustCompile(`\d+|\D+`)

func naturalLess(a, b string) bool {
	pa, pb := naturalNumRE.FindAllString(a, -1), naturalNumRE.FindAllString(b, -1)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] == pb[i] {
			continue
		}
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		if ea == nil && eb == nil {
			return na < nb
		}
		return pa[i] < pb[i]
	}
	return len(pa) < len(pb)
}

func (s *Service) Traffic() (TrafficInfo, error) {
	running, err := s.RunReadOnly("show running-config")
	if err != nil {
		return TrafficInfo{}, err
	}
	return ParseTraffic(running), nil
}

// ---------- Polecenia ----------

var stormTypes = map[string]bool{"broadcast": true, "multicast": true, "dlf": true}
var loopActions = map[string]bool{"port-disable": true, "link-down": true, "vlan-disable": true, "learn-disable": true, "none": true}

// BuildStormCommands ustawia lub usuwa limit ruchu danego typu na portach (procent pasma 1–100).
func BuildStormCommands(ports []string, typ string, level int, remove bool) ([]string, error) {
	if !stormTypes[typ] {
		return nil, fmt.Errorf("rodzaj ruchu: broadcast, multicast albo dlf (nieznany unicast)")
	}
	if !remove && (level < 1 || level > 100) {
		return nil, fmt.Errorf("limit podaj w procentach pasma, od 1 do 100")
	}
	cmds := []string{"configure terminal"}
	for _, p := range ports {
		if !portRE.MatchString(p) {
			return nil, fmt.Errorf("nieprawidłowy port %q", p)
		}
		cmds = append(cmds, "interface "+p)
		if remove {
			cmds = append(cmds, "no storm-control "+typ+" level")
		} else {
			cmds = append(cmds, "storm-control "+typ+" level "+strconv.Itoa(level))
		}
	}
	return append(cmds, "end"), nil
}

// BuildLoopDetectCommands włącza lub wyłącza wykrywanie pętli na całym switchu.
func BuildLoopDetectCommands(enable bool) []string {
	if enable {
		return []string{"configure terminal", "loop-protection loop-detect", "end"}
	}
	return []string{"configure terminal", "no loop-protection loop-detect", "end"}
}

// BuildLoopActionCommands ustawia, co switch robi z portem, na którym wykrył pętlę.
func BuildLoopActionCommands(ports []string, action string, remove bool) ([]string, error) {
	if !remove && !loopActions[action] {
		return nil, fmt.Errorf("nieznana akcja %q", action)
	}
	cmds := []string{"configure terminal"}
	for _, p := range ports {
		if !portRE.MatchString(p) {
			return nil, fmt.Errorf("nieprawidłowy port %q", p)
		}
		cmds = append(cmds, "interface "+p)
		if remove {
			cmds = append(cmds, "no loop-protection action")
		} else {
			cmds = append(cmds, "loop-protection action "+action)
		}
	}
	return append(cmds, "end"), nil
}

// BuildIGMPCommands włącza lub wyłącza IGMP snooping w VLAN-ie.
func BuildIGMPCommands(vlan int, enable bool) ([]string, error) {
	if vlan < 1 || vlan > 4094 {
		return nil, fmt.Errorf("VLAN musi być z zakresu 1–4094")
	}
	cmd := "ip igmp snooping"
	if !enable {
		cmd = "no " + cmd
	}
	return []string{"configure terminal", "interface vlan" + strconv.Itoa(vlan), cmd, "end"}, nil
}

func (s *Service) ConfigureStorm(rawPorts, typ string, level int, remove bool) (string, error) {
	ports, err := ParsePortRanges(rawPorts)
	if err != nil {
		return "", err
	}
	cmds, err := BuildStormCommands(ports, typ, level, remove)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

func (s *Service) ConfigureLoopDetect(enable bool) (string, error) {
	return s.runConfig(BuildLoopDetectCommands(enable))
}

func (s *Service) ConfigureLoopAction(rawPorts, action string, remove bool) (string, error) {
	ports, err := ParsePortRanges(rawPorts)
	if err != nil {
		return "", err
	}
	cmds, err := BuildLoopActionCommands(ports, action, remove)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

func (s *Service) ConfigureIGMP(vlan int, enable bool) (string, error) {
	running, err := s.RunReadOnlyFresh("show running-config")
	if err != nil {
		return "", err
	}
	exists := false
	for _, v := range ParseTraffic(running).VLANs {
		if v.VLAN == strconv.Itoa(vlan) {
			exists = true
		}
	}
	if !exists {
		return "", fmt.Errorf("VLAN %d nie ma interfejsu w konfiguracji switcha", vlan)
	}
	cmds, err := BuildIGMPCommands(vlan, enable)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}
