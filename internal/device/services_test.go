package device

import (
	"reflect"
	"testing"
)

func TestServiceStateFromRunningConfig(t *testing.T) {
	http, _ := serviceDef("http")
	st := ParseServiceState(http, "!\nno service http\n!\n", "HTTP Server Configuration\n  HTTP server : Enabled\n")
	if st.State != "disabled" {
		t.Fatalf("running-config must win: %+v", st)
	}
	lldp, _ := serviceDef("lldp")
	if st := ParseServiceState(lldp, "hostname awplus\nlldp run\n", ""); st.State != "enabled" {
		t.Fatalf("lldp: %+v", st)
	}
}

func TestServiceStateFromShowAndUnknown(t *testing.T) {
	telnet, _ := serviceDef("telnet")
	cases := map[string]string{
		"Telnet Server Configuration\n  Telnet server : Disabled\n": "disabled",
		"Telnet server ............... Enabled":                     "enabled",
		"DHCP server is enabled":                                    "enabled",
		"":                                                          "unknown",
		"Port : 23":                                                 "unknown",
	}
	for show, want := range cases {
		if st := ParseServiceState(telnet, "hostname awplus\n", show); st.State != want {
			t.Errorf("%q: got %s want %s", show, st.State, want)
		}
	}
}

func TestServiceCommandsAndLocks(t *testing.T) {
	cmds, err := BuildServiceCommands("http", false)
	if err != nil || !reflect.DeepEqual(cmds, []string{"configure terminal", "no service http", "end"}) {
		t.Fatalf("%v %v", cmds, err)
	}
	if _, err := BuildServiceCommands("ssh", false); err == nil {
		t.Fatal("disabling SSH must be blocked")
	}
	if _, err := BuildServiceCommands("ssh", true); err != nil {
		t.Fatal("enabling SSH must be allowed")
	}
	if _, err := BuildServiceCommands("rm -rf", true); err == nil {
		t.Fatal("unknown service must be rejected")
	}
	for _, d := range ServiceDefs {
		ok := false
		for _, a := range allowed {
			if a == d.ShowCmd {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s: %q not in read-only allowlist", d.ID, d.ShowCmd)
		}
	}
}
