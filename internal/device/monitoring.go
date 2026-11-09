package device

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---------- Logi ----------

// LogEntry to jedna linia logu AlliedWare Plus, np.
// "2026 Nov  8 10:12:33 user.notice awplus IMISH[1234]: tekst".
// Linie w nieznanym formacie trafiają w całości do Message.
type LogEntry struct {
	Time     string `json:"time"`
	Facility string `json:"facility"`
	Severity string `json:"severity"`
	Level    int    `json:"level"` // 0 emerg ... 7 debug, -1 nieznany
	Host     string `json:"host"`
	Program  string `json:"program"`
	Message  string `json:"message"`
}

var (
	logLineRE  = regexp.MustCompile(`^(\d{4}\s+[A-Z][a-z]{2}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})\s+([a-z0-9]+)\.([a-z]+)\s+(\S+)\s+([^:\s\[]+)(?:\[\d+\])?:\s?(.*)$`)
	severities = map[string]int{"emerg": 0, "alert": 1, "crit": 2, "err": 3, "error": 3, "warning": 4, "warn": 4, "notice": 5, "info": 6, "debug": 7}
)

func ParseLog(raw string) []LogEntry {
	var out []LogEntry
	for _, line := range strings.Split(raw, "\n") {
		l := strings.TrimRight(line, "\r")
		if strings.TrimSpace(l) == "" || strings.Trim(l, "-= ") == "" {
			continue
		}
		if m := logLineRE.FindStringSubmatch(l); m != nil {
			lvl, ok := severities[m[3]]
			if !ok {
				lvl = -1
			}
			out = append(out, LogEntry{Time: strings.Join(strings.Fields(m[1]), " "), Facility: m[2], Severity: m[3], Level: lvl, Host: m[4], Program: m[5], Message: m[6]})
			continue
		}
		low := strings.ToLower(strings.TrimSpace(l))
		if strings.HasPrefix(low, "<date>") || strings.HasPrefix(low, "date ") {
			continue // nagłówek tabeli
		}
		out = append(out, LogEntry{Level: -1, Message: strings.TrimSpace(l)})
	}
	return out
}

func (s *Service) Logs() ([]LogEntry, string, error) {
	raw, err := s.RunReadOnly("show log")
	if err != nil {
		return nil, "", err
	}
	return ParseLog(raw), raw, nil
}

// ---------- Czas i NTP ----------

type TimeInfo struct {
	Clock      string   `json:"clock"`
	NTPServers []string `json:"ntpServers"`
	Timezone   string   `json:"timezone"`
	SummerTime string   `json:"summerTime"`
	NTPStatus  string   `json:"ntpStatus"`
}

// ParseTimeConfig wyciąga serwery NTP i strefę czasową z running-config.
func ParseTimeConfig(running string) (servers []string, timezone, summer string) {
	for _, line := range strings.Split(running, "\n") {
		l := strings.TrimSpace(line)
		f := strings.Fields(l)
		switch {
		case len(f) >= 3 && f[0] == "ntp" && (f[1] == "server" || f[1] == "peer"):
			servers = append(servers, f[2])
		case strings.HasPrefix(l, "clock timezone "):
			timezone = strings.TrimPrefix(l, "clock timezone ")
		case strings.HasPrefix(l, "clock summer-time "):
			summer = strings.TrimPrefix(l, "clock summer-time ")
		}
	}
	return
}

func (s *Service) TimeInfo() (TimeInfo, error) {
	running, err := s.RunReadOnly("show running-config")
	if err != nil {
		return TimeInfo{}, err
	}
	servers, tz, summer := ParseTimeConfig(running)
	clock, _ := s.RunReadOnly("show clock")
	status, _ := s.RunReadOnly("show ntp associations")
	if servers == nil {
		servers = []string{}
	}
	return TimeInfo{Clock: strings.TrimSpace(clock), NTPServers: servers, Timezone: tz, SummerTime: summer, NTPStatus: strings.TrimSpace(status)}, nil
}

var hostnameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,62})(\.[A-Za-z0-9]([A-Za-z0-9-]{0,62}))*$`)
var tzNameRE = regexp.MustCompile(`^[A-Za-z]{2,6}$`)

func validNTPHost(h string) bool {
	if ip := net.ParseIP(h); ip != nil {
		return ip.To4() != nil
	}
	return len(h) <= 253 && hostnameRE.MatchString(h)
}

// BuildNTPCommands dodaje lub usuwa serwer NTP. Usuwać można tylko serwer obecny w konfiguracji.
func BuildNTPCommands(host string, remove bool, existing []string) ([]string, error) {
	host = strings.TrimSpace(host)
	if !validNTPHost(host) {
		return nil, fmt.Errorf("podaj adres IPv4 albo nazwę hosta serwera NTP")
	}
	present := false
	for _, e := range existing {
		if e == host {
			present = true
		}
	}
	if remove && !present {
		return nil, fmt.Errorf("serwera %s nie ma w konfiguracji", host)
	}
	if !remove && present {
		return nil, fmt.Errorf("serwer %s jest już skonfigurowany", host)
	}
	cmd := "ntp server " + host
	if remove {
		cmd = "no " + cmd
	}
	return []string{"configure terminal", cmd, "end"}, nil
}

// BuildTimezoneCommands ustawia strefę jako przesunięcie w pełnych godzinach względem UTC.
func BuildTimezoneCommands(name string, offset int) ([]string, error) {
	name = strings.TrimSpace(name)
	if !tzNameRE.MatchString(name) {
		return nil, fmt.Errorf("skrót strefy to 2–6 liter, np. CET")
	}
	if offset < -12 || offset > 14 {
		return nil, fmt.Errorf("przesunięcie musi być w zakresie od -12 do +14 godzin")
	}
	dir := "plus"
	if offset < 0 {
		dir, offset = "minus", -offset
	}
	return []string{"configure terminal", "clock timezone " + name + " " + dir + " " + strconv.Itoa(offset), "end"}, nil
}

// BuildSummerTimeCommands ustawia czas letni według reguły UE (ostatnia niedziela
// marca 02:00 do ostatniej niedzieli października 03:00, +60 min) albo go usuwa.
func BuildSummerTimeCommands(name string, remove bool) ([]string, error) {
	if remove {
		return []string{"configure terminal", "no clock summer-time", "end"}, nil
	}
	name = strings.TrimSpace(name)
	if !tzNameRE.MatchString(name) {
		return nil, fmt.Errorf("skrót czasu letniego to 2–6 liter, np. CEST")
	}
	return []string{"configure terminal", "clock summer-time " + name + " recurring 5 Sun Mar 02:00 5 Sun Oct 03:00 60", "end"}, nil
}

func (s *Service) ConfigureSummerTime(name string, remove bool) (string, error) {
	cmds, err := BuildSummerTimeCommands(name, remove)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

// ---------- Mirroring ----------

type MirrorSession struct {
	Destination string `json:"destination"`
	Source      string `json:"source"`
	Direction   string `json:"direction"`
}

var portRE = regexp.MustCompile(`^port1\.0\.([1-9]|[1-4][0-9]|5[0-2])$`)

// ParseMirrors czyta sesje mirroringu z running-config:
// "interface portX" z wierszem "mirror interface portY direction Z".
func ParseMirrors(running string) []MirrorSession {
	out := []MirrorSession{}
	blocks := InterfaceBlocks(running)
	names := make([]string, 0, len(blocks))
	for n := range blocks {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return naturalLess(names[i], names[j]) })
	for _, dest := range names {
		for _, c := range blocks[dest] {
			f := strings.Fields(c)
			if len(f) < 3 || f[0] != "mirror" || f[1] != "interface" {
				continue
			}
			dir := "both"
			if len(f) >= 5 && f[3] == "direction" {
				dir = f[4]
			}
			for _, src := range ExpandInterfaces(f[2]) {
				out = append(out, MirrorSession{Destination: dest, Source: src, Direction: dir})
			}
		}
	}
	return out
}

func (s *Service) Mirrors() ([]MirrorSession, error) {
	running, err := s.RunReadOnly("show running-config")
	if err != nil {
		return nil, err
	}
	return ParseMirrors(running), nil
}

// BuildMirrorCommands tworzy lub usuwa sesję mirroringu ruchu z source na destination.
func BuildMirrorCommands(dest, source, direction string, remove bool, existing []MirrorSession) ([]string, error) {
	if !portRE.MatchString(dest) || !portRE.MatchString(source) {
		return nil, fmt.Errorf("wybierz porty port1.0.1–port1.0.52")
	}
	if dest == source {
		return nil, fmt.Errorf("port docelowy musi być inny niż źródłowy")
	}
	var found *MirrorSession
	for i := range existing {
		if existing[i].Destination == dest && existing[i].Source == source {
			found = &existing[i]
		}
	}
	if remove {
		if found == nil {
			return nil, fmt.Errorf("nie ma mirroringu z %s na %s", source, dest)
		}
		return []string{"configure terminal", "interface " + dest, "no mirror interface " + source, "end"}, nil
	}
	switch direction {
	case "both", "receive", "transmit":
	default:
		return nil, fmt.Errorf("kierunek: both, receive albo transmit")
	}
	if found != nil {
		return nil, fmt.Errorf("mirroring z %s na %s już istnieje", source, dest)
	}
	for _, m := range existing {
		if m.Destination == source {
			return nil, fmt.Errorf("%s jest już portem docelowym mirroringu; nie może być źródłem", source)
		}
		if m.Source == dest {
			return nil, fmt.Errorf("%s jest już źródłem mirroringu; nie może być portem docelowym", dest)
		}
	}
	return []string{"configure terminal", "interface " + dest, "mirror interface " + source + " direction " + direction, "end"}, nil
}

// ---------- Wykonanie zmian (zawsze na świeżym running-config) ----------

func (s *Service) freshRunning() (string, error) {
	return s.RunReadOnlyFresh("show running-config")
}

func (s *Service) ConfigureNTP(host string, remove bool) (string, error) {
	running, err := s.freshRunning()
	if err != nil {
		return "", err
	}
	servers, _, _ := ParseTimeConfig(running)
	cmds, err := BuildNTPCommands(host, remove, servers)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

func (s *Service) ConfigureTimezone(name string, offset int) (string, error) {
	cmds, err := BuildTimezoneCommands(name, offset)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

func (s *Service) ConfigureMirror(dest, source, direction string, remove bool) (string, error) {
	running, err := s.freshRunning()
	if err != nil {
		return "", err
	}
	cmds, err := BuildMirrorCommands(dest, source, direction, remove, ParseMirrors(running))
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}
