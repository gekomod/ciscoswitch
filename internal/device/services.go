package device

import (
	"fmt"
	"regexp"
	"strings"
)

// ServiceDef opisuje usługę AlliedWare Plus, którą panel umie włączyć i wyłączyć.
// Stan jest ustalany najpierw z running-config (jawne "service x" / "no service x"),
// a gdy wpisu brak, z odpowiedzi polecenia show. Jeśli żadne źródło nie daje
// jednoznacznej odpowiedzi, stan zostaje "unknown" — panel niczego nie zgaduje.
type ServiceDef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	EnableCmd   string `json:"enableCommand"`
	DisableCmd  string `json:"disableCommand"`
	ShowCmd     string `json:"showCommand"`
	// Locked: panel nie pozwala wyłączyć usługi, bo sam z niej korzysta.
	Locked bool `json:"locked"`
	// Warning: ostrzeżenie pokazywane przed wyłączeniem.
	Warning string `json:"warning,omitempty"`
}

type ServiceState struct {
	ServiceDef
	State  string `json:"state"`  // enabled | disabled | unknown
	Source string `json:"source"` // skąd pochodzi stan
}

var ServiceDefs = []ServiceDef{
	{
		ID: "ssh", Name: "Serwer SSH",
		Description: "Zdalny dostęp do wiersza poleceń. Panel łączy się ze switchem właśnie przez SSH.",
		EnableCmd:   "service ssh", DisableCmd: "no service ssh", ShowCmd: "show ssh server",
		Locked: true,
	},
	{
		ID: "http", Name: "Serwer WWW (HTTP)",
		Description: "Wbudowany interfejs WWW switcha. Bez pliku GUI producenta strona i tak nie działa, więc można go bezpiecznie wyłączyć.",
		EnableCmd:   "service http", DisableCmd: "no service http", ShowCmd: "show http",
	},
	{
		ID: "telnet", Name: "Serwer Telnet",
		Description: "Nieszyfrowany dostęp do wiersza poleceń. Hasła idą przez sieć jawnym tekstem; zalecane wyłączenie, gdy działa SSH.",
		EnableCmd:   "service telnet", DisableCmd: "no service telnet", ShowCmd: "show telnet",
	},
	{
		ID: "snmp", Name: "Agent SNMP",
		Description: "Udostępnia liczniki i stan portów. Panel odczytuje przez SNMP dashboard i monitoring.",
		EnableCmd:   "snmp-server", DisableCmd: "no snmp-server", ShowCmd: "show snmp-server",
		Warning: "Po wyłączeniu SNMP dashboard, lista portów i monitoring ruchu przestaną działać.",
	},
	{
		ID: "lldp", Name: "LLDP",
		Description: "Wykrywanie sąsiednich urządzeń. Zasila mapę topologii informacjami o podłączonych switchach, routerach i punktach dostępowych.",
		EnableCmd:   "lldp run", DisableCmd: "no lldp run", ShowCmd: "show lldp",
	},
	{
		ID: "dhcp-server", Name: "Serwer DHCP",
		Description: "Przydziela adresy IP z pul skonfigurowanych na switchu. W domowej sieci zwykle robi to router; dwa serwery DHCP w jednej sieci powodują konflikty.",
		EnableCmd:   "service dhcp-server", DisableCmd: "no service dhcp-server", ShowCmd: "show ip dhcp server summary",
		Warning: "Urządzenia, które dostają adresy z tego switcha, nie odnowią dzierżawy.",
	},
}

func serviceDef(id string) (ServiceDef, bool) {
	for _, d := range ServiceDefs {
		if d.ID == id {
			return d, true
		}
	}
	return ServiceDef{}, false
}

var showStateRE = regexp.MustCompile(`(?i)(?:server|status|state|service|agent)[^\n:]*[:.]+\s*(enabled|disabled)\b|\bis\s+(enabled|disabled)\b`)

// ParseServiceState ustala stan usługi na podstawie running-config i wyniku show.
func ParseServiceState(def ServiceDef, running, show string) ServiceState {
	st := ServiceState{ServiceDef: def, State: "unknown", Source: "brak jednoznacznych danych"}
	enabled, disabled := false, false
	for _, line := range strings.Split(running, "\n") {
		l := strings.TrimSpace(line)
		if l == def.EnableCmd {
			enabled = true
		}
		if l == def.DisableCmd {
			disabled = true
		}
	}
	switch {
	case disabled && !enabled:
		st.State, st.Source = "disabled", "running-config: "+def.DisableCmd
		return st
	case enabled && !disabled:
		st.State, st.Source = "enabled", "running-config: "+def.EnableCmd
		return st
	}
	if m := showStateRE.FindStringSubmatch(show); m != nil {
		word := strings.ToLower(m[1] + m[2])
		if word == "enabled" || word == "disabled" {
			st.State, st.Source = word, def.ShowCmd
		}
	}
	return st
}

// BuildServiceCommands zwraca polecenia zmiany stanu usługi albo błąd,
// gdy zmiana jest zablokowana.
func BuildServiceCommands(id string, enable bool) ([]string, error) {
	def, ok := serviceDef(id)
	if !ok {
		return nil, fmt.Errorf("nieznana usługa %q", id)
	}
	if def.Locked && !enable {
		return nil, fmt.Errorf("%s nie może zostać wyłączony z panelu, bo panel straciłby połączenie ze switchem", def.Name)
	}
	cmd := def.DisableCmd
	if enable {
		cmd = def.EnableCmd
	}
	return []string{"configure terminal", cmd, "end"}, nil
}

// ServiceStates odczytuje stan wszystkich usług.
func (s *Service) ServiceStates() ([]ServiceState, error) {
	running, err := s.RunReadOnly("show running-config")
	if err != nil {
		return nil, fmt.Errorf("nie udało się odczytać running-config: %w", err)
	}
	out := make([]ServiceState, 0, len(ServiceDefs))
	for _, def := range ServiceDefs {
		show, _ := s.RunReadOnly(def.ShowCmd)
		out = append(out, ParseServiceState(def, running, show))
	}
	return out, nil
}

// ConfigureService włącza lub wyłącza usługę.
func (s *Service) ConfigureService(id string, enable bool) (string, error) {
	cmds, err := BuildServiceCommands(id, enable)
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}
