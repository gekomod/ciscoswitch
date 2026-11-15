package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gs948mx-manager/internal/netnames"
)

func TestTopologyNamesMergesHostARPAndNames(t *testing.T) {
	lldpRaw, err := os.ReadFile(filepath.Join("..", "device", "testdata", "awplus", "allied_telesis_awplus_show_lldp_neighbors_detail.raw"))
	if err != nil {
		t.Fatal(err)
	}
	read := func(cmd string) (string, error) {
		switch cmd {
		case "show mac address-table":
			return "VLAN port             mac            fwd\n1    port1.0.5    a483.e701.0203   forward   dynamic\n1    port1.0.7    0011.2233.4455   forward   dynamic\n1    port1.0.23   001a.eb96.25f4   forward   dynamic\n", nil
		case "show arp":
			return "IP Address      MAC Address     Interface    Port        Type\n192.168.31.20   a483.e701.0203  vlan1        port1.0.5   dynamic\n", nil
		case "show lldp neighbors detail":
			return string(lldpRaw), nil
		}
		return "", nil
	}
	swept := 0
	r := netnames.NewResolver()
	st := &namesState{
		resolver: r,
		hostARP: func() map[string][]string {
			if swept == 0 {
				return map[string][]string{}
			}
			return map[string][]string{"00:11:22:33:44:55": {"192.168.31.40"}}
		},
		sweep:  func(*net.IPNet) int { swept++; return 254 },
		subnet: func() (*net.IPNet, error) { _, n, _ := net.ParseCIDR("192.168.31.0/24"); return n, nil },
	}
	setTestLookups(r, map[string]string{"192.168.31.20": "laptop-jarek", "192.168.31.40": "DESKTOP-PC"})
	rec := httptest.NewRecorder()
	st.handler(read).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/topology/names", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Devices []topologyDevice `json:"devices"`
		Named   int              `json:"named"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	byMAC := map[string]topologyDevice{}
	for _, d := range out.Devices {
		byMAC[d.MAC] = d
	}
	if d := byMAC["a4:83:e7:01:02:03"]; d.Name != "laptop-jarek" || d.Port != "port1.0.5" {
		t.Fatalf("switch ARP device: %+v", d)
	}
	if d := byMAC["00:11:22:33:44:55"]; d.Name != "DESKTOP-PC" || len(d.IPs) != 1 || swept != 1 {
		t.Fatalf("host ARP after sweep: %+v swept=%d", d, swept)
	}
	if d := byMAC["00:1a:eb:96:25:f4"]; d.Name != "6F-SH210-1" || d.Source != "lldp" {
		t.Fatalf("LLDP name: %+v", d)
	}
	// Drugie skanowanie w ciągu 10 minut nie przeszukuje sieci ponownie.
	st.handler(read).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/topology/names", nil))
	if swept != 1 {
		t.Fatalf("sweep repeated: %d", swept)
	}
}

func setTestLookups(r *netnames.Resolver, names map[string]string) {
	netnames.SetLookupsForTest(r, func(_ context.Context, ip string) string { return names[ip] })
}
