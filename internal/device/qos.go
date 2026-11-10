package device

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// QoS w wersji podstawowej: włączenie globalne i domyślny priorytet (CoS 0–7)
// dla ruchu bez znacznika wchodzącego na port.

type QoSPort struct {
	Port string `json:"port"`
	CoS  int    `json:"cos"`
}

type QoSInfo struct {
	Enabled bool      `json:"enabled"`
	Ports   []QoSPort `json:"ports"`
	Raw     string    `json:"raw"`
}

func ParseQoS(running string) QoSInfo {
	info := QoSInfo{Ports: []QoSPort{}}
	for _, line := range strings.Split(running, "\n") {
		if strings.TrimRight(line, "\r") == "mls qos enable" {
			info.Enabled = true
		}
	}
	blocks := InterfaceBlocks(running)
	names := make([]string, 0, len(blocks))
	for n := range blocks {
		if portRE.MatchString(n) {
			names = append(names, n)
		}
	}
	sort.Slice(names, func(i, j int) bool { return naturalLess(names[i], names[j]) })
	for _, n := range names {
		for _, c := range blocks[n] {
			f := strings.Fields(c)
			if len(f) == 4 && f[0] == "mls" && f[1] == "qos" && f[2] == "cos" {
				if v, err := strconv.Atoi(f[3]); err == nil {
					info.Ports = append(info.Ports, QoSPort{Port: n, CoS: v})
				}
			}
		}
	}
	return info
}

func (s *Service) QoS() (QoSInfo, error) {
	running, err := s.RunReadOnly("show running-config")
	if err != nil {
		return QoSInfo{}, err
	}
	info := ParseQoS(running)
	info.Raw, _ = s.RunReadOnly("show mls qos")
	return info, nil
}

func BuildQoSGlobalCommands(enable bool) []string {
	if enable {
		return []string{"configure terminal", "mls qos enable", "end"}
	}
	return []string{"configure terminal", "no mls qos enable", "end"}
}

// BuildQoSPortCommands ustawia domyślny priorytet ruchu bez znacznika na portach
// albo przywraca ustawienie domyślne switcha.
func BuildQoSPortCommands(ports []string, cos int, remove bool) ([]string, error) {
	if !remove && (cos < 0 || cos > 7) {
		return nil, fmt.Errorf("priorytet CoS musi być z zakresu 0–7")
	}
	return portCmds(ports, func(string) []string {
		if remove {
			return []string{"no mls qos cos"}
		}
		return []string{"mls qos cos " + strconv.Itoa(cos)}
	})
}

func (s *Service) ConfigureQoSGlobal(enable bool) (string, error) {
	return s.runConfig(BuildQoSGlobalCommands(enable))
}

func (s *Service) ConfigureQoSPorts(rawPorts string, cos int, remove bool) (string, error) {
	ports, err := ParsePortRanges(rawPorts)
	if err != nil {
		return "", err
	}
	cmds, err := BuildQoSPortCommands(ports, cos, remove)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}
