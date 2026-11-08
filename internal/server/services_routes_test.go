package server

import "testing"

func TestServiceChangeNeedsAcknowledgement(t *testing.T) {
	if err := serviceChangeAllowed("snmp", false, false); err == nil {
		t.Fatal("disabling SNMP without acknowledgement must be refused")
	}
	if err := serviceChangeAllowed("snmp", false, true); err != nil {
		t.Fatal(err)
	}
	if err := serviceChangeAllowed("telnet", false, false); err != nil {
		t.Fatal("telnet has no warning")
	}
	if err := serviceChangeAllowed("ssh", false, true); err == nil {
		t.Fatal("SSH stays locked even with acknowledgement")
	}
}
