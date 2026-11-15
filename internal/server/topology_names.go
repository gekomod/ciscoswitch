package server

import (
	"context"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gs948mx-manager/internal/device"
	"gs948mx-manager/internal/netnames"
)

// Nazwy urządzeń dla mapy topologii. Switch zna tylko MAC (FDB) i część
// adresów IP (ARP — tylko urządzenia, które rozmawiały z jego adresem
// zarządzającym), więc panel uzupełnia IP z tablicy ARP własnego komputera,
// a nazwy ustala przez DNS (PTR), mDNS i NetBIOS. Nazwy sąsiadów LLDP
// pochodzą z `show lldp neighbors detail`.

type topologyDevice struct {
	MAC    string   `json:"mac"`
	Port   string   `json:"port,omitempty"`
	VLAN   string   `json:"vlan,omitempty"`
	IPs    []string `json:"ips,omitempty"`
	Name   string   `json:"name,omitempty"`
	Source string   `json:"source,omitempty"`
}

var fdbPortRE = regexp.MustCompile(`(?i)^(port\d+\.\d+\.\d+|sa\d+|po\d+)$`)

// parseFDBEntries: wiersze `show mac address-table` (VLAN, port, MAC).
func parseFDBEntries(raw string) []topologyDevice {
	var out []topologyDevice
	for _, line := range strings.Split(raw, "\n") {
		var d topologyDevice
		for i, f := range strings.Fields(line) {
			if mac := netnames.NormalizeMAC(f); mac != "" && d.MAC == "" {
				d.MAC = mac
			} else if fdbPortRE.MatchString(f) && d.Port == "" {
				d.Port = f
			} else if i == 0 && isVLANNumber(f) {
				d.VLAN = f
			}
		}
		if d.MAC != "" && d.Port != "" {
			out = append(out, d)
		}
	}
	return out
}

func isVLANNumber(s string) bool {
	if len(s) == 0 || len(s) > 4 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

type namesState struct {
	mu        sync.Mutex
	lastSweep time.Time
	resolver  *netnames.Resolver
	// Podmieniane w testach.
	hostARP func() map[string][]string
	sweep   func(*net.IPNet) int
	subnet  func() (*net.IPNet, error)
}

func topologyNamesHandler(read func(string) (string, error), localIP func() (net.IP, error)) http.Handler {
	st := &namesState{
		resolver: netnames.NewResolver(),
		hostARP:  netnames.HostARP,
		sweep:    netnames.Sweep,
		subnet: func() (*net.IPNet, error) {
			ip, err := localIP()
			if err != nil {
				return nil, err
			}
			return netnames.LocalSubnet(ip)
		},
	}
	return st.handler(read)
}

func (st *namesState) handler(read func(string) (string, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		fdbRaw, fdbErr := read("show mac address-table")
		arpRaw, _ := read("show arp")
		lldpRaw, lldpErr := read("show lldp neighbors detail")
		if fdbErr != nil && lldpErr != nil {
			writeJSON(w, nil, fdbErr)
			return
		}
		devices := parseFDBEntries(fdbRaw)
		var lldp []device.LLDPNeighbor
		if lldpErr == nil {
			lldp = device.ParseLLDPDetail(lldpRaw)
		}
		switchARP := netnames.ParseARPTable(arpRaw)
		hostARP := st.hostARP()
		fill := func() int {
			missing := 0
			for i := range devices {
				devices[i].IPs = mergeIPs(switchARP[devices[i].MAC], hostARP[devices[i].MAC])
				if len(devices[i].IPs) == 0 {
					missing++
				}
			}
			return missing
		}
		sweepInfo := map[string]any{"done": false}
		// Przeszukanie sieci (jedno zapytanie NetBIOS na adres) najwyżej raz na 10 minut
		// i tylko gdy części urządzeń brakuje adresu IP.
		if fill() > 0 && r.URL.Query().Get("sweep") != "0" {
			st.mu.Lock()
			due := time.Since(st.lastSweep) > 10*time.Minute
			if due {
				st.lastSweep = time.Now()
			}
			st.mu.Unlock()
			if due {
				if subnet, err := st.subnet(); err == nil {
					sent := st.sweep(subnet)
					time.Sleep(1500 * time.Millisecond)
					hostARP = st.hostARP()
					fill()
					sweepInfo = map[string]any{"done": true, "subnet": subnet.String(), "probes": sent}
				} else {
					sweepInfo["error"] = err.Error()
				}
			}
		}
		var ips []string
		for _, d := range devices {
			ips = append(ips, d.IPs...)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		names := st.resolver.Lookup(ctx, ips)
		named := 0
		for i := range devices {
			for _, ip := range devices[i].IPs {
				if n, ok := names[ip]; ok {
					devices[i].Name, devices[i].Source = n.Name, n.Source
					named++
					break
				}
			}
		}
		// Sąsiad LLDP z nazwą systemu nazywa też swój MAC (Chassis ID).
		for _, n := range lldp {
			mac := netnames.NormalizeMAC(n.ChassisID)
			if mac == "" || n.SystemName == "" {
				continue
			}
			for i := range devices {
				if devices[i].MAC == mac && devices[i].Name == "" {
					devices[i].Name, devices[i].Source = n.SystemName, "lldp"
					named++
				}
			}
		}
		sort.SliceStable(devices, func(i, j int) bool { return devices[i].Port < devices[j].Port })
		writeJSON(w, map[string]any{
			"devices": devices, "lldp": lldp, "named": named, "sweep": sweepInfo,
			"lldpAvailable": lldpErr == nil, "hostArpEntries": len(hostARP),
		}, nil)
	})
}

func mergeIPs(lists ...[]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range lists {
		for _, ip := range l {
			if !seen[ip] {
				seen[ip] = true
				out = append(out, ip)
			}
		}
	}
	return out
}
