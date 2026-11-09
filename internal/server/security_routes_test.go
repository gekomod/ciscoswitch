package server

import "testing"

func TestSecurityProtectedPorts(t *testing.T) {
	t.Setenv("PROTECTED_PORTS", "port1.0.1")
	cases := []struct {
		q    securityRequest
		deny bool
	}{
		{securityRequest{Action: "port-security", Ports: "1-4"}, true},
		{securityRequest{Action: "port-security", Ports: "1-4", Remove: true}, true},
		{securityRequest{Action: "acl-apply", Ports: "1", ACL: "3001"}, true},
		{securityRequest{Action: "acl-apply", Ports: "1", ACL: "3001", Remove: true}, false},
		{securityRequest{Action: "dhcp-trust", Ports: "1", Enable: true}, false},
		{securityRequest{Action: "dhcp-trust", Ports: "1", Enable: false}, true},
		{securityRequest{Action: "port-security", Ports: "5-8"}, false},
		{securityRequest{Action: "dhcp-global", Enable: true}, false},
	}
	for _, c := range cases {
		err := securityPortsAllowed(c.q)
		if (err != nil) != c.deny {
			t.Errorf("%+v: deny=%v got %v", c.q, c.deny, err)
		}
	}
}
