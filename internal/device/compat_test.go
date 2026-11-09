package device

import (
	"strings"
	"testing"
	"time"
)

func TestAnonymize(t *testing.T) {
	got := anonymize("gw 192.168.1.1 mac 0050.56a6.9a73 and 00:11:22:33:44:55 port1.0.1")
	if strings.Contains(got, "192.168") || strings.Contains(got, "0050") || strings.Contains(got, "00:11") || !strings.Contains(got, "port1.0.1") {
		t.Fatalf("%q", got)
	}
}

// Raport na atrapie switcha: brak SNMP, ale polecenia SSH działają.
func TestCompatibilityReportOnFakeSwitch(t *testing.T) {
	f := startFakeSwitch(t)
	s := fakeService(f)
	s.cfg.Timeout = 300 * time.Millisecond
	defer s.Close()
	r := s.Compatibility()
	if len(r.Checks) < 10 {
		t.Fatalf("too few checks: %d", len(r.Checks))
	}
	if r.Checks[0].Status != "error" {
		t.Fatalf("SNMP is not configured in the fake; expected error, got %+v", r.Checks[0])
	}
	if !strings.Contains(r.Text, "[OK  ] Konfiguracja: show running-config") {
		t.Fatalf("report text:\n%s", r.Text)
	}
	if strings.Contains(r.Text, "output of show running-config") {
		t.Fatal("running-config content must never be included")
	}
}
