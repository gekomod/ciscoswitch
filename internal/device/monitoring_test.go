package device

import (
	"reflect"
	"testing"
)

func TestParseLog(t *testing.T) {
	raw := `<date> <time> <facility>.<severity> <program[<pid>]>: <message>
--------------------------------------------------------------------------------
2026 Nov  8 10:12:33 user.notice awplus IMISH[1234]: manager logged in
2026 Nov  8 10:14:02 kern.warning awplus kernel: port1.0.23 link flap
2026 Nov  8 10:15:00 daemon.err awplus lldpd[88]: neighbour table full
coś w nieznanym formacie
`
	e := ParseLog(raw)
	if len(e) != 4 {
		t.Fatalf("got %d entries: %+v", len(e), e)
	}
	if e[0].Time != "2026 Nov 8 10:12:33" || e[0].Severity != "notice" || e[0].Level != 5 || e[0].Program != "IMISH" || e[0].Message != "manager logged in" {
		t.Fatalf("%+v", e[0])
	}
	if e[1].Program != "kernel" || e[1].Level != 4 || e[2].Level != 3 {
		t.Fatalf("%+v %+v", e[1], e[2])
	}
	if e[3].Level != -1 || e[3].Message != "coś w nieznanym formacie" {
		t.Fatalf("fallback %+v", e[3])
	}
}

const runningSample = `!
clock timezone CET plus 1
ntp server 192.168.1.1
ntp server pool.ntp.org
!
interface port1.0.5
 switchport
!
interface port1.0.24
 mirror interface port1.0.1 direction both
 mirror interface port1.0.2,port1.0.3 direction receive
!
`

func TestParseTimeAndMirrors(t *testing.T) {
	s, tz, _ := ParseTimeConfig(runningSample)
	if !reflect.DeepEqual(s, []string{"192.168.1.1", "pool.ntp.org"}) || tz != "CET plus 1" {
		t.Fatalf("%v %q", s, tz)
	}
	m := ParseMirrors(runningSample)
	want := []MirrorSession{{"port1.0.24", "port1.0.1", "both"}, {"port1.0.24", "port1.0.2", "receive"}, {"port1.0.24", "port1.0.3", "receive"}}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("%+v", m)
	}
}

func TestNTPAndTimezoneCommands(t *testing.T) {
	ex := []string{"192.168.1.1"}
	if c, err := BuildNTPCommands("pl.pool.ntp.org", false, ex); err != nil || c[1] != "ntp server pl.pool.ntp.org" {
		t.Fatalf("%v %v", c, err)
	}
	if c, err := BuildNTPCommands("192.168.1.1", true, ex); err != nil || c[1] != "no ntp server 192.168.1.1" {
		t.Fatalf("%v %v", c, err)
	}
	for _, bad := range []string{"", "a b", "x;reload", "192.168.1.1", "::1", "-bad.host"} {
		if _, err := BuildNTPCommands(bad, false, ex); err == nil {
			t.Errorf("NTP %q must be rejected", bad)
		}
	}
	if _, err := BuildNTPCommands("10.0.0.9", true, ex); err == nil {
		t.Error("removing unknown server must be rejected")
	}
	if c, _ := BuildTimezoneCommands("CET", 1); c[1] != "clock timezone CET plus 1" {
		t.Fatal(c)
	}
	if c, _ := BuildTimezoneCommands("EST", -5); c[1] != "clock timezone EST minus 5" {
		t.Fatal(c)
	}
	for _, bad := range []struct {
		n string
		o int
	}{{"C", 1}, {"CET;x", 1}, {"CET", 15}, {"CET", -13}} {
		if _, err := BuildTimezoneCommands(bad.n, bad.o); err == nil {
			t.Errorf("timezone %v must be rejected", bad)
		}
	}
}

func TestMirrorCommands(t *testing.T) {
	ex := ParseMirrors(runningSample)
	c, err := BuildMirrorCommands("port1.0.48", "port1.0.7", "transmit", false, ex)
	if err != nil || !reflect.DeepEqual(c, []string{"configure terminal", "interface port1.0.48", "mirror interface port1.0.7 direction transmit", "end"}) {
		t.Fatalf("%v %v", c, err)
	}
	c, err = BuildMirrorCommands("port1.0.24", "port1.0.1", "", true, ex)
	if err != nil || c[2] != "no mirror interface port1.0.1" {
		t.Fatalf("%v %v", c, err)
	}
	bad := []struct {
		d, s, dir string
		rm        bool
	}{
		{"port1.0.5", "port1.0.5", "both", false},  // ten sam port
		{"port1.0.53", "port1.0.1", "both", false}, // poza zakresem
		{"vlan1", "port1.0.1", "both", false},      // nie port
		{"port1.0.5", "port1.0.1", "all", false},   // zły kierunek
		{"port1.0.24", "port1.0.1", "both", false}, // już istnieje
		{"port1.0.5", "port1.0.24", "both", false}, // źródło jest już celem
		{"port1.0.1", "port1.0.9", "both", false},  // cel jest już źródłem
		{"port1.0.5", "port1.0.9", "", true},       // usuwanie nieistniejącego
	}
	for _, b := range bad {
		if _, err := BuildMirrorCommands(b.d, b.s, b.dir, b.rm, ex); err == nil {
			t.Errorf("%+v must be rejected", b)
		}
	}
}
