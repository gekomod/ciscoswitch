package device

import (
	"reflect"
	"testing"
)

func TestParseQoS(t *testing.T) {
	running := "mls qos enable\n!\ninterface port1.0.3-1.0.4\n mls qos cos 5\n!\ninterface port1.0.10\n mls qos cos 1\n!\ninterface vlan1\n ip address 10.0.0.1/24\n!\n"
	i := ParseQoS(running)
	want := []QoSPort{{"port1.0.3", 5}, {"port1.0.4", 5}, {"port1.0.10", 1}}
	if !i.Enabled || !reflect.DeepEqual(i.Ports, want) {
		t.Fatalf("%+v", i)
	}
	if ParseQoS("hostname x\n").Enabled {
		t.Fatal("disabled by default")
	}
}

func TestQoSCommands(t *testing.T) {
	c, err := BuildQoSPortCommands([]string{"port1.0.3"}, 5, false)
	if err != nil || !reflect.DeepEqual(c, []string{"configure terminal", "interface port1.0.3", "mls qos cos 5", "end"}) {
		t.Fatalf("%v %v", c, err)
	}
	if c, _ := BuildQoSPortCommands([]string{"port1.0.3"}, 0, true); c[2] != "no mls qos cos" {
		t.Fatal(c)
	}
	for _, bad := range []int{-1, 8} {
		if _, err := BuildQoSPortCommands([]string{"port1.0.3"}, bad, false); err == nil {
			t.Errorf("cos %d must be rejected", bad)
		}
	}
	if !reflect.DeepEqual(BuildQoSGlobalCommands(false), []string{"configure terminal", "no mls qos enable", "end"}) {
		t.Fatal("global off")
	}
}

func TestPortSettingsFiberPorts(t *testing.T) {
	c, err := BuildPortSettingsCommands("port1.0.51", "trunk", 1, "1,10", 1, "10000", "auto", "uplink")
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, x := range c {
		joined += x + "|"
	}
	if !contains(c, "speed 10000") || containsPrefix(c, "duplex") {
		t.Fatalf("SFP+: speed 10000, no duplex: %s", joined)
	}
	if c, _ := BuildPortSettingsCommands("port1.0.49", "access", 1, "", 1, "1000", "full", ""); !contains(c, "duplex full") {
		t.Fatal("explicit full duplex on SFP must be sent")
	}
	bad := []struct{ port, speed, duplex string }{
		{"port1.0.5", "10000", "auto"}, // miedź 10G
		{"port1.0.50", "100", "auto"},  // SFP 100M
		{"port1.0.52", "1000", "half"}, // SFP półdupleks
		{"port1.0.52", "2500", "auto"}, // nieobsługiwana
	}
	for _, b := range bad {
		if _, err := BuildPortSettingsCommands(b.port, "access", 1, "", 1, b.speed, b.duplex, ""); err == nil {
			t.Errorf("%+v must be rejected", b)
		}
	}
	if c, _ := BuildPortSettingsCommands("port1.0.5", "access", 1, "", 1, "auto", "auto", ""); !contains(c, "duplex auto") {
		t.Fatal("copper keeps duplex auto")
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func containsPrefix(xs []string, p string) bool {
	for _, x := range xs {
		if len(x) >= len(p) && x[:len(p)] == p {
			return true
		}
	}
	return false
}
