package device

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func sample(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "awplus", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// AW+ zawija długą listę "Configured Vlans" do kolejnych linii.
func TestSwitchportWrappedVLANsFromRealOutput(t *testing.T) {
	out := sample(t, "allied_telesis_awplus_show_interface_switchport.raw")
	d, err := ParseSwitchportDetails(out, "port1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	want := []int{767, 793, 794, 811, 900, 964, 973, 989, 1111, 2222}
	if !reflect.DeepEqual(d.ConfiguredVLANs, want) {
		t.Fatalf("got %v want %v", d.ConfiguredVLANs, want)
	}
	if d.Mode != "trunk" {
		t.Fatalf("mode %q", d.Mode)
	}
	d, err = ParseSwitchportDetails(out, "port1.0.3")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.ConfiguredVLANs, []int{795}) {
		t.Fatalf("port1.0.3 got %v; continuation must stop at next field", d.ConfiguredVLANs)
	}
}

// Linie informacyjne AW+ zaczynające się od "% " nie są błędami.
func TestCLIErrorIgnoresInformationalPercentLines(t *testing.T) {
	for _, name := range []string{
		"allied_telesis_awplus_show_static-channel-group.raw",
		"allied_telesis_awplus_show_static-channel-group2.raw",
	} {
		if msg := cliError(sample(t, name)); msg != "" {
			t.Fatalf("%s: informational line treated as error: %q", name, msg)
		}
	}
	for _, line := range []string{
		"% Invalid input detected at '^' marker.",
		"% Incomplete command.",
		"% Unrecognized command",
		"% Ambiguous command",
		"% VLAN 4000 does not exist",
		"% Failed to add port to channel group",
	} {
		if cliError("awplus(config)#\n"+line+"\n") == "" {
			t.Fatalf("error not detected: %q", line)
		}
	}
}
