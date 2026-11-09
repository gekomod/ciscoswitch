package device

import (
	"reflect"
	"testing"
)

const trafficRunning = `!
loop-protection loop-detect ldf-interval 10
!
interface port1.0.1-1.0.4
 switchport
 storm-control broadcast level 20
 loop-protection action port-disable
!
interface port1.0.7,port1.0.9
 storm-control multicast level 5
!
interface port1.0.24
 mirror interface port1.0.1-1.0.2 direction receive
!
interface vlan1
 ip address 192.168.31.2/24
!
interface vlan30
 no ip igmp snooping
!
`

func TestExpandInterfaces(t *testing.T) {
	got := ExpandInterfaces("port1.0.1-1.0.3,port1.0.7,vlan10,port1.0.49-port1.0.50")
	want := []string{"port1.0.1", "port1.0.2", "port1.0.3", "port1.0.7", "vlan10", "port1.0.49", "port1.0.50"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestParseTraffic(t *testing.T) {
	i := ParseTraffic(trafficRunning)
	if !i.LoopDetect || len(i.Storm) != 6 || len(i.LoopPorts) != 4 {
		t.Fatalf("%+v", i)
	}
	if i.Storm[0] != (StormSetting{"port1.0.1", "broadcast", "20"}) || i.Storm[5] != (StormSetting{"port1.0.9", "multicast", "5"}) {
		t.Fatalf("%+v", i.Storm)
	}
	if !reflect.DeepEqual(i.VLANs, []VLANSnooping{{"1", "default"}, {"30", "disabled"}}) {
		t.Fatalf("%+v", i.VLANs)
	}
	// mirroring z zakresem źródeł
	m := ParseMirrors(trafficRunning)
	if len(m) != 2 || m[1] != (MirrorSession{"port1.0.24", "port1.0.2", "receive"}) {
		t.Fatalf("%+v", m)
	}
}

func TestParsePortRanges(t *testing.T) {
	p, err := ParsePortRanges(" 3, 1-2 ,49,2")
	if err != nil || !reflect.DeepEqual(p, []string{"port1.0.1", "port1.0.2", "port1.0.3", "port1.0.49"}) {
		t.Fatalf("%v %v", p, err)
	}
	for _, bad := range []string{"", "0", "53", "5-2", "a", "1;reload", "1-60"} {
		if _, err := ParsePortRanges(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestTrafficCommands(t *testing.T) {
	c, err := BuildStormCommands([]string{"port1.0.5", "port1.0.6"}, "broadcast", 10, false)
	if err != nil || !reflect.DeepEqual(c, []string{"configure terminal", "interface port1.0.5", "storm-control broadcast level 10", "interface port1.0.6", "storm-control broadcast level 10", "end"}) {
		t.Fatalf("%v %v", c, err)
	}
	if c, _ := BuildStormCommands([]string{"port1.0.5"}, "dlf", 0, true); c[2] != "no storm-control dlf level" {
		t.Fatal(c)
	}
	if _, err := BuildStormCommands([]string{"port1.0.5"}, "unicast", 10, false); err == nil {
		t.Error("bad type")
	}
	if _, err := BuildStormCommands([]string{"port1.0.5"}, "broadcast", 0, false); err == nil {
		t.Error("level 0")
	}
	if c, _ := BuildLoopActionCommands([]string{"port1.0.3"}, "port-disable", false); c[2] != "loop-protection action port-disable" {
		t.Fatal(c)
	}
	if _, err := BuildLoopActionCommands([]string{"port1.0.3"}, "reboot", false); err == nil {
		t.Error("bad action")
	}
	if c, _ := BuildIGMPCommands(30, true); !reflect.DeepEqual(c, []string{"configure terminal", "interface vlan30", "ip igmp snooping", "end"}) {
		t.Fatal(c)
	}
	if _, err := BuildIGMPCommands(4095, true); err == nil {
		t.Error("vlan range")
	}
}

func TestNormalizeInterfaceRanges(t *testing.T) {
	in := "hostname sw\n!\ninterface port1.0.1-1.0.3\n switchport mode access\n switchport access vlan 10\n!\ninterface vlan1\n ip address 192.168.31.2/24\n!\nend"
	got := NormalizeInterfaceRanges(in)
	want := "hostname sw\n!\ninterface port1.0.1\n switchport mode access\n switchport access vlan 10\n!\ninterface port1.0.2\n switchport mode access\n switchport access vlan 10\n!\ninterface port1.0.3\n switchport mode access\n switchport access vlan 10\n!\ninterface vlan1\n ip address 192.168.31.2/24\n!\nend"
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
	if NormalizeInterfaceRanges(want) != want {
		t.Fatal("already normalized config must stay unchanged")
	}
}

// Zmiana VLAN na porcie z zakresu musi znać jego obecną konfigurację.
func TestVLANMembershipSeesRangedPorts(t *testing.T) {
	running := "interface port1.0.1-1.0.4\n switchport\n switchport mode trunk\n switchport trunk allowed vlan add 10,20\n!\n"
	cmds, err := BuildVLANPortMembershipCommands(running, []string{"port1.0.3"}, 30, "tagged")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%v", cmds)
	for _, c := range cmds {
		if c == "switchport mode trunk" {
			t.Fatalf("port1.0.3 is already trunk; panel must not reconfigure mode: %v", cmds)
		}
	}
}
