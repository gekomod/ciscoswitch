package device

import "time"

// LiveRaw to lekki odczyt jednego interfejsu do widoku na żywo (diody portów).
type LiveRaw struct {
	Name      string
	Admin     string
	Oper      string
	SpeedMbps uint64
	InOctets  uint64
	OutOctets uint64
	Errors    uint64
}

// LiveSample odczytuje przez SNMP tylko kolumny potrzebne do diod: stan,
// prędkość, liczniki bajtów i błędów. Nazwy interfejsów pochodzą z ostatniego
// pełnego odczytu (Status), żeby nie pobierać ich co kilka sekund.
func (s *Service) LiveSample() (time.Time, map[int]LiveRaw, error) {
	names := s.interfaceNames()
	g, err := newSNMPClient(s.snmpConfig())
	if err != nil {
		return time.Time{}, nil, err
	}
	if err := g.Connect(); err != nil {
		return time.Time{}, nil, err
	}
	defer g.Conn.Close()
	at := time.Now()
	admin, err := walkNum(g, ".1.3.6.1.2.1.2.2.1.7")
	if err != nil {
		return at, nil, err
	}
	oper, _ := walkNum(g, ".1.3.6.1.2.1.2.2.1.8")
	speed, _ := walkCounter(g, ".1.3.6.1.2.1.31.1.1.1.15")
	in, _ := walkCounter(g, ".1.3.6.1.2.1.31.1.1.1.6")
	out, _ := walkCounter(g, ".1.3.6.1.2.1.31.1.1.1.10")
	inErr, _ := walkCounter(g, ".1.3.6.1.2.1.2.2.1.14")
	outErr, _ := walkCounter(g, ".1.3.6.1.2.1.2.2.1.20")
	res := make(map[int]LiveRaw, len(admin))
	for idx, a := range admin {
		name := names[idx]
		if name == "" {
			continue
		}
		res[idx] = LiveRaw{Name: name, Admin: state(a), Oper: state(oper[idx]), SpeedMbps: speed[idx],
			InOctets: in[idx], OutOctets: out[idx], Errors: inErr[idx] + outErr[idx]}
	}
	return at, res, nil
}

// interfaceNames zwraca indeks → nazwa z ostatniego pełnego odczytu, odświeżając
// go co najwyżej raz na minutę.
func (s *Service) interfaceNames() map[int]string {
	s.statusMu.Lock()
	fresh := !s.statusAt.IsZero() && time.Since(s.statusAt) < time.Minute
	st := s.status
	s.statusMu.Unlock()
	if !fresh || len(st.Ports) == 0 {
		st = s.Status()
	}
	out := make(map[int]string, len(st.Ports))
	for _, p := range st.Ports {
		out[p.Index] = p.Name
	}
	return out
}
