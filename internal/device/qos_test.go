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
