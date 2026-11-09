package device

import (
	"reflect"
	"strings"
	"testing"
)

const securityRunning = `!
service dhcp-snooping
access-list 3001 deny ip 10.0.0.0/8 any
access-list 3001 permit ip any any
access-list hardware goscie
 deny ip any 192.168.1.0/24
!
interface port1.0.1
 ip dhcp snooping trust
!
interface port1.0.5-1.0.6
 switchport port-security
 switchport port-security maximum 2
 switchport port-security violation restrict
 access-group 3001
!
interface port1.0.9
 access-group 3999
!
interface vlan1
 ip address 192.168.31.2/24
!
interface vlan20
 ip dhcp snooping
!
`

func TestParseSecurity(t *testing.T) {
	i := ParseSecurity(securityRunning)
	if !i.DHCPSnooping || !reflect.DeepEqual(i.TrustedPorts, []string{"port1.0.1"}) || !reflect.DeepEqual(i.SnoopingVLANs, []string{"20"}) {
		t.Fatalf("dhcp %+v", i)
	}
	if len(i.PortSecurity) != 2 || i.PortSecurity[1] != (PortSecuritySetting{"port1.0.6", "2", "restrict"}) {
		t.Fatalf("ps %+v", i.PortSecurity)
	}
	if len(i.ACLs) != 2 || i.ACLs[0].ID != "3001" || len(i.ACLs[0].Rules) != 2 || !reflect.DeepEqual(i.ACLs[0].Ports, []string{"port1.0.5", "port1.0.6"}) {
		t.Fatalf("acl %+v", i.ACLs)
	}
	if i.ACLs[1].ID != "goscie" || i.ACLs[1].Rules[0] != "deny ip any 192.168.1.0/24" {
		t.Fatalf("named acl %+v", i.ACLs[1])
	}
	if !reflect.DeepEqual(i.UnknownACLPorts, []string{"port1.0.9: 3999"}) {
		t.Fatalf("%v", i.UnknownACLPorts)
	}
}

func TestPortSecurityCommands(t *testing.T) {
	c, err := BuildPortSecurityCommands([]string{"port1.0.7"}, 3, "shutdown", false)
	if err != nil || !reflect.DeepEqual(c, []string{"configure terminal", "interface port1.0.7", "switchport port-security", "switchport port-security maximum 3", "switchport port-security violation shutdown", "end"}) {
		t.Fatalf("%v %v", c, err)
	}
	for _, b := range []struct {
		m int
		v string
	}{{0, "restrict"}, {300, "restrict"}, {2, "reboot"}} {
		if _, err := BuildPortSecurityCommands([]string{"port1.0.7"}, b.m, b.v, false); err == nil {
			t.Errorf("%+v must be rejected", b)
		}
	}
}

func TestDHCPSnoopingSafety(t *testing.T) {
	noTrust := ParseSecurity("interface vlan20\n ip address 10.0.0.1/24\n!\n")
	if _, err := BuildDHCPSnoopingVLANCommands(20, true, noTrust); err == nil || !strings.Contains(err.Error(), "zaufany") {
		t.Fatalf("enabling snooping without trusted port must be refused: %v", err)
	}
	if _, err := BuildDHCPSnoopingVLANCommands(20, false, noTrust); err != nil {
		t.Fatal("disabling is always allowed")
	}
	i := ParseSecurity(securityRunning)
	if c, err := BuildDHCPSnoopingVLANCommands(1, true, i); err != nil || c[2] != "ip dhcp snooping" {
		t.Fatalf("%v %v", c, err)
	}
	if _, err := BuildDHCPSnoopingVLANCommands(77, true, i); err == nil {
		t.Fatal("unknown VLAN interface")
	}
}

func TestACLCommands(t *testing.T) {
	i := ParseSecurity(securityRunning)
	c, err := BuildACLRuleCommands(3002, "deny", "192.168.50.7", "any", false, i)
	if err != nil || c[1] != "access-list 3002 deny ip 192.168.50.7/32 any" {
		t.Fatalf("%v %v", c, err)
	}
	c, err = BuildACLRuleCommands(3001, "deny", "10.1.2.3/8", "any", true, i)
	if err != nil || c[1] != "no access-list 3001 deny ip 10.0.0.0/8 any" {
		t.Fatalf("network normalisation / removal: %v %v", c, err)
	}
	bad := []struct {
		id         int
		a, s, d    string
		remove     bool
		reasonPart string
	}{
		{2999, "deny", "any", "any", false, "3000"},
		{3002, "drop", "any", "any", false, "akcja"},
		{3002, "deny", "10.0.0.0/33", "any", false, "źródło"},
		{3002, "deny", "any", "x; reload", false, "cel"},
		{3001, "deny", "10.0.0.0/8", "any", false, "istnieje"},
		{3002, "deny", "1.2.3.4", "any", true, "nie ma"},
	}
	for _, b := range bad {
		if _, err := BuildACLRuleCommands(b.id, b.a, b.s, b.d, b.remove, i); err == nil || !strings.Contains(err.Error(), b.reasonPart) {
			t.Errorf("%+v: got %v", b, err)
		}
	}
	// ostatnia reguła ACL przypisanego do portów
	one := ParseSecurity("access-list 3005 deny ip any any\n!\ninterface port1.0.3\n access-group 3005\n!\n")
	if _, err := BuildACLRuleCommands(3005, "deny", "any", "any", true, one); err == nil {
		t.Fatal("removing last rule of applied ACL must be refused")
	}
	if c, err := BuildACLApplyCommands([]string{"port1.0.8"}, "goscie", false, i); err != nil || c[2] != "access-group goscie" {
		t.Fatalf("%v %v", c, err)
	}
	if _, err := BuildACLApplyCommands([]string{"port1.0.8"}, "3500", false, i); err == nil {
		t.Fatal("applying non-existent ACL")
	}
	if _, err := BuildACLApplyCommands([]string{"port1.0.8"}, "x y", false, i); err == nil {
		t.Fatal("invalid id")
	}
}
