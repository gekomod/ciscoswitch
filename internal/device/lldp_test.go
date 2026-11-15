package device

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseLLDPDetailAWPlusSample(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "awplus", "allied_telesis_awplus_show_lldp_neighbors_detail.raw"))
	if err != nil {
		t.Fatal(err)
	}
	got := ParseLLDPDetail(string(raw))
	if len(got) < 2 {
		t.Fatalf("neighbors=%d", len(got))
	}
	first := got[0]
	if first.LocalPort != "port1.0.1" || first.ChassisID != "0050.56a6.9a73" || first.SystemName != "" {
		t.Fatalf("first=%+v", first)
	}
	var sw LLDPNeighbor
	for _, n := range got {
		if n.LocalPort == "port1.0.23" {
			sw = n
		}
	}
	if sw.SystemName != "6F-SH210-1" || sw.PortID != "port1.0.15" || sw.ManagementAddress != "192.168.254.201" {
		t.Fatalf("switch neighbor=%+v", sw)
	}
	if sw.SystemDescription != "Allied Telesis router/switch, AW+ v5.4.5-1.3" {
		t.Fatalf("description=%q", sw.SystemDescription)
	}
	if sw.Capabilities != "Bridge" {
		t.Fatalf("capabilities=%q", sw.Capabilities)
	}
}
