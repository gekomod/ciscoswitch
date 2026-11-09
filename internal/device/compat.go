package device

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// CompatCheck to wynik sprawdzenia jednego polecenia odczytu i jego parsera.
type CompatCheck struct {
	Area    string `json:"area"`
	Command string `json:"command"`
	Status  string `json:"status"` // ok | warning | error
	Detail  string `json:"detail"`
	Sample  string `json:"sample,omitempty"`
}

type CompatReport struct {
	Generated string        `json:"generated"`
	Checks    []CompatCheck `json:"checks"`
	Text      string        `json:"text"`
}

var (
	sampleIPv4RE = regexp.MustCompile(`\b(\d{1,3}\.){3}\d{1,3}\b`)
	sampleMACRE  = regexp.MustCompile(`(?i)\b([0-9a-f]{4}\.){2}[0-9a-f]{4}\b|\b([0-9a-f]{2}[:-]){5}[0-9a-f]{2}\b`)
)

// anonymize ukrywa adresy IP i MAC w próbkach wyjść dołączanych do raportu.
func anonymize(s string) string {
	s = sampleIPv4RE.ReplaceAllString(s, "x.x.x.x")
	return sampleMACRE.ReplaceAllString(s, "xxxx.xxxx.xxxx")
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\r\n "), "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("… (%d linii więcej)", len(lines)-n))
	}
	return anonymize(strings.Join(lines, "\n"))
}

// Compatibility wykonuje polecenia odczytu używane przez panel i sprawdza,
// czy parsery rozumieją odpowiedzi. Raport nie zawiera running-config, haseł
// ani community; adresy IP i MAC w próbkach są ukryte.
func (s *Service) Compatibility() CompatReport {
	rep := CompatReport{Generated: time.Now().Format("2006-01-02 15:04:05")}
	add := func(c CompatCheck) { rep.Checks = append(rep.Checks, c) }
	run := func(cmd string) (string, error) {
		out, err := s.RunReadOnlyFresh(cmd)
		if err == nil {
			if msg := cliError(out); msg != "" {
				err = fmt.Errorf("%s", msg)
			}
		}
		return out, err
	}
	fail := func(area, cmd string, err error) {
		add(CompatCheck{Area: area, Command: cmd, Status: "error", Detail: anonymize(err.Error())})
	}

	// SNMP i nazwy portów (front switcha)
	st := s.Status()
	switch {
	case !st.Reachable:
		add(CompatCheck{Area: "SNMP", Command: "IF-MIB", Status: "error", Detail: "switch nie odpowiada przez SNMP: " + anonymize(st.Warning)})
	default:
		named, other := 0, []string{}
		for _, p := range st.Ports {
			if portRE.MatchString(p.Name) {
				named++
			} else if len(other) < 6 {
				other = append(other, p.Name)
			}
		}
		status := "ok"
		if named < 52 {
			status = "warning"
		}
		add(CompatCheck{Area: "SNMP / front switcha", Command: "IF-MIB ifDescr", Status: status,
			Detail: fmt.Sprintf("interfejsów: %d, w tym portów port1.0.1–52: %d; inne nazwy: %s", len(st.Ports), named, strings.Join(other, ", "))})
	}

	// running-config: tryb uprawnień i zakresy portów (bez treści)
	if running, err := run("show running-config"); err != nil {
		fail("Konfiguracja", "show running-config", err)
	} else {
		blocks := InterfaceBlocks(running)
		ranges := 0
		for _, line := range strings.Split(running, "\n") {
			if strings.HasPrefix(line, "interface port") && (strings.Contains(line, "-") || strings.Contains(line, ",")) {
				ranges++
			}
		}
		add(CompatCheck{Area: "Konfiguracja", Command: "show running-config", Status: "ok",
			Detail: fmt.Sprintf("odczyt działa w kanale SSH exec; interfejsów: %d, nagłówków z zakresem portów: %d", len(blocks), ranges)})
	}

	// polecenia z parserami
	type parsed struct {
		area, cmd string
		check     func(string) (string, string)
		sample    bool
	}
	checks := []parsed{
		{"System", "show version", func(o string) (string, string) {
			if strings.Contains(strings.ToLower(o), "version") {
				return "ok", firstLines(o, 1)
			}
			return "warning", "nie znaleziono słowa version"
		}, true},
		{"Pliki", "dir", func(o string) (string, string) {
			n := len(ParseDir(o))
			if n == 0 {
				return "warning", "parser nie rozpoznał żadnego pliku"
			}
			return "ok", fmt.Sprintf("rozpoznane pliki: %d", n)
		}, true},
		{"Pliki", "show boot", func(o string) (string, string) {
			n := len(ParseBoot(o))
			if n == 0 {
				return "warning", "parser nie rozpoznał pól"
			}
			return "ok", fmt.Sprintf("rozpoznane pola: %d", n)
		}, true},
		{"Logi", "show log", func(o string) (string, string) {
			e := ParseLog(o)
			known := 0
			for _, x := range e {
				if x.Level >= 0 {
					known++
				}
			}
			if len(e) > 0 && known*2 < len(e) {
				return "warning", fmt.Sprintf("rozpoznane %d z %d wpisów", known, len(e))
			}
			return "ok", fmt.Sprintf("rozpoznane %d z %d wpisów", known, len(e))
		}, true},
		{"Czas", "show clock", func(o string) (string, string) { return "ok", firstLines(o, 2) }, false},
		{"Czas", "show ntp associations", func(o string) (string, string) { return "ok", "odczyt działa" }, true},
		{"Porty", "show interface switchport", func(o string) (string, string) {
			d, err := ParseSwitchportDetails(o, "port1.0.1")
			if err != nil {
				return "warning", "parser: " + err.Error()
			}
			return "ok", "port1.0.1: tryb " + d.Mode
		}, true},
		{"Usługi", "show http", func(o string) (string, string) { return serviceProbe("http", o) }, true},
		{"Usługi", "show telnet", func(o string) (string, string) { return serviceProbe("telnet", o) }, true},
		{"Usługi", "show lldp", func(o string) (string, string) { return serviceProbe("lldp", o) }, true},
		{"Topologia", "show lldp neighbors", func(o string) (string, string) { return "ok", "format niezweryfikowany, próbka poniżej" }, true},
		{"Topologia", "show spanning-tree", func(o string) (string, string) { return "ok", "format niezweryfikowany, próbka poniżej" }, true},
		{"Topologia", "show mac address-table", func(o string) (string, string) { return "ok", "odczyt działa" }, false},
	}
	for _, c := range checks {
		out, err := run(c.cmd)
		if err != nil {
			fail(c.area, c.cmd, err)
			continue
		}
		status, detail := c.check(out)
		cc := CompatCheck{Area: c.area, Command: c.cmd, Status: status, Detail: detail}
		if c.sample || status != "ok" {
			cc.Sample = firstLines(out, 12)
		}
		add(cc)
	}
	rep.Text = compatText(rep)
	return rep
}

func serviceProbe(id, show string) (string, string) {
	def, _ := serviceDef(id)
	st := ParseServiceState(def, "", show)
	if st.State == "unknown" {
		return "warning", "stanu usługi nie da się odczytać z tej odpowiedzi"
	}
	return "ok", "stan: " + st.State
}

func compatText(r CompatReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Raport zgodności panelu GS948MX (%s)\n", r.Generated)
	mark := map[string]string{"ok": "OK  ", "warning": "UWAGA", "error": "BŁĄD"}
	for _, c := range r.Checks {
		fmt.Fprintf(&b, "\n[%s] %s: %s\n  %s\n", mark[c.Status], c.Area, c.Command, strings.ReplaceAll(c.Detail, "\n", "\n  "))
		if c.Sample != "" {
			b.WriteString("  --- próbka ---\n  " + strings.ReplaceAll(c.Sample, "\n", "\n  ") + "\n")
		}
	}
	return b.String()
}
