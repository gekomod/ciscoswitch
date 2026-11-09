package server

import "testing"

func TestTrafficProtectedPorts(t *testing.T) {
	t.Setenv("PROTECTED_PORTS", "port1.0.1")
	if err := trafficPortsAllowed("storm", "1-4"); err == nil {
		t.Fatal("range containing protected port must be refused")
	}
	if err := trafficPortsAllowed("loop-action", "5-8"); err != nil {
		t.Fatal(err)
	}
	if err := trafficPortsAllowed("igmp", ""); err != nil {
		t.Fatal("igmp has no ports")
	}
}
