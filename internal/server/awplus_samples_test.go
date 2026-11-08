package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTopologyStaticLAGAndWrappedVLANs(t *testing.T) {
	sw, err := os.ReadFile(filepath.Join("..", "device", "testdata", "awplus", "allied_telesis_awplus_show_interface_switchport.raw"))
	if err != nil {
		t.Fatal(err)
	}
	config := "interface port1.0.23\n static-channel-group 1\n!\ninterface port1.0.24\n static-channel-group 1\n!\ninterface port1.0.7\n channel-group 7 mode active\n!\n"
	ports, lags, _ := parseTopologyConfig(config, string(sw))
	names := map[string][]string{}
	for _, l := range lags {
		names[l.Name] = l.Members
	}
	if len(names["sa1"]) != 2 {
		t.Fatalf("static LAG sa1 not detected: %v", names)
	}
	if len(names["Po7"]) != 1 {
		t.Fatalf("LACP Po7 not detected: %v", names)
	}
	for _, p := range ports {
		if p.Port == "port1.0.1" && len(p.VLANs) != 10 {
			t.Fatalf("port1.0.1 VLANs %v; wrapped list lost", p.VLANs)
		}
	}
}

func TestTopologyLACPFromRealOutput(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "device", "testdata", "awplus", "allied_telesis_awplus_show_etherchannel_summary.raw"))
	if err != nil {
		t.Fatal(err)
	}
	groups := parseTopologyLACP(string(raw))
	if len(groups) != 2 || groups[0].Name != "po7" || len(groups[0].Members) != 2 || len(groups[1].Members) != 2 {
		t.Fatalf("unexpected %+v", groups)
	}
}
