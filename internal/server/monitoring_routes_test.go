package server

import "testing"

func TestMirrorDestinationCannotBeProtected(t *testing.T) {
	t.Setenv("PROTECTED_PORTS", "port1.0.1,port1.0.48")
	if err := mirrorAllowed("port1.0.48", false); err == nil {
		t.Fatal("protected port must not become mirror destination")
	}
	if err := mirrorAllowed("port1.0.48", true); err != nil {
		t.Fatal("removing an existing session must stay possible")
	}
	if err := mirrorAllowed("port1.0.20", false); err != nil {
		t.Fatal(err)
	}
}
