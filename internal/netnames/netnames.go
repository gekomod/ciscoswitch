// Package netnames ustala nazwy urządzeń w sieci lokalnej na podstawie ich
// adresów IP: odwrotny DNS (PTR — zwykle nazwy z DHCP routera), mDNS (Apple,
// Linux z Avahi, Android, drukarki, NAS) i NetBIOS (Windows, Samba).
// Wszystkie zapytania są tylko do odczytu i mają krótkie limity czasu.
package netnames

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Result to nazwa urządzenia i jej źródło: "dns", "mdns" albo "netbios".
type Result struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// Timeout pojedynczego zapytania (zmieniany w testach).
var Timeout = 1200 * time.Millisecond

// Resolver z pamięcią wyników; równoległe skanowania nie odpytują sieci ponownie.
type Resolver struct {
	TTL   time.Duration
	mu    sync.Mutex
	cache map[string]cached
	// Zapytania podmieniane w testach.
	dns, mdns, netbios func(ctx context.Context, ip string) string
}
type cached struct {
	r  Result
	at time.Time
}

func NewResolver() *Resolver {
	return &Resolver{TTL: 10 * time.Minute, cache: map[string]cached{}, dns: ReverseDNS, mdns: MDNSName, netbios: NetBIOSName}
}

// Lookup ustala nazwy dla podanych adresów IPv4 (równolegle, najwyżej 32 naraz).
// Adresy bez nazwy nie występują w wyniku.
func (r *Resolver) Lookup(ctx context.Context, ips []string) map[string]Result {
	out := map[string]Result{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	seen := map[string]bool{}
	for _, ip := range ips {
		if seen[ip] || net.ParseIP(ip).To4() == nil {
			continue
		}
		seen[ip] = true
		r.mu.Lock()
		c, ok := r.cache[ip]
		r.mu.Unlock()
		if ok && time.Since(c.at) < r.TTL {
			if c.r.Name != "" {
				out[ip] = c.r
			}
			continue
		}
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			res := r.resolve(ctx, ip)
			r.mu.Lock()
			r.cache[ip] = cached{res, time.Now()}
			r.mu.Unlock()
			if res.Name != "" {
				mu.Lock()
				out[ip] = res
				mu.Unlock()
			}
		}(ip)
	}
	wg.Wait()
	return out
}

// resolve odpytuje wszystkie źródła równolegle; pierwszeństwo: DNS, mDNS, NetBIOS.
func (r *Resolver) resolve(ctx context.Context, ip string) Result {
	ctx, cancel := context.WithTimeout(ctx, Timeout+300*time.Millisecond)
	defer cancel()
	var names [3]string
	var wg sync.WaitGroup
	for i, f := range []func(context.Context, string) string{r.dns, r.mdns, r.netbios} {
		wg.Add(1)
		go func(i int, f func(context.Context, string) string) {
			defer wg.Done()
			names[i] = cleanName(f(ctx, ip), ip)
		}(i, f)
	}
	wg.Wait()
	for i, src := range []string{"dns", "mdns", "netbios"} {
		if names[i] != "" {
			return Result{Name: names[i], Source: src}
		}
	}
	return Result{}
}

// cleanName usuwa końcową kropkę i sufiks .local, odrzuca nazwy będące samym
// adresem IP (np. 192-168-1-5.dynamic.isp) i znaki spoza nazwy hosta.
func cleanName(name, ip string) string {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	name = strings.TrimSuffix(name, ".local")
	if name == "" || name == ip {
		return ""
	}
	if strings.Contains(name, strings.ReplaceAll(ip, ".", "-")) || strings.HasSuffix(name, ".in-addr.arpa") {
		return ""
	}
	if len(name) > 80 {
		name = name[:80]
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7f {
			return ""
		}
	}
	return name
}

// ReverseDNS pyta systemowy resolver o rekord PTR.
func ReverseDNS(ctx context.Context, ip string) string {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	return names[0]
}

func udpExchange(ctx context.Context, addr string, req []byte) ([]byte, error) {
	conn, err := net.Dial("udp4", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	if _, err := conn.Write(req); err != nil {
		return nil, err
	}
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// ---------- mDNS ----------

// reverseName zwraca d.c.b.a.in-addr.arpa dla adresu a.b.c.d.
func reverseName(ip string) string {
	p := strings.Split(ip, ".")
	return p[3] + "." + p[2] + "." + p[1] + "." + p[0] + ".in-addr.arpa"
}

// MDNSName wysyła do urządzenia jednostkowe zapytanie mDNS (port 5353) o PTR
// jego adresu; odpowiada m.in. Avahi, Bonjour i Android.
func MDNSName(ctx context.Context, ip string) string {
	resp, err := udpExchange(ctx, net.JoinHostPort(ip, "5353"), buildPTRQuery(0x4e4e, reverseName(ip)))
	if err != nil {
		return ""
	}
	name, _ := parsePTRAnswer(resp)
	return name
}

func buildPTRQuery(id uint16, name string) []byte {
	b := make([]byte, 12, 64)
	binary.BigEndian.PutUint16(b[0:], id)
	binary.BigEndian.PutUint16(b[4:], 1) // QDCOUNT
	for _, label := range strings.Split(name, ".") {
		b = append(b, byte(len(label)))
		b = append(b, label...)
	}
	b = append(b, 0)
	b = append(b, 0, 12)   // PTR
	b = append(b, 0x80, 1) // IN + bit QU (odpowiedź jednostkowa)
	return b
}

var errDNS = errors.New("nieprawidłowa odpowiedź DNS")

// readName dekoduje nazwę DNS (z kompresją) od pozycji off.
func readName(msg []byte, off int) (string, int, error) {
	var labels []string
	end := -1
	for jumps := 0; jumps < 20; {
		if off >= len(msg) {
			return "", 0, errDNS
		}
		l := int(msg[off])
		switch {
		case l == 0:
			if end < 0 {
				end = off + 1
			}
			return strings.Join(labels, "."), end, nil
		case l&0xc0 == 0xc0:
			if off+1 >= len(msg) {
				return "", 0, errDNS
			}
			if end < 0 {
				end = off + 2
			}
			off = int(binary.BigEndian.Uint16(msg[off:]) & 0x3fff)
			jumps++
		default:
			if off+1+l > len(msg) {
				return "", 0, errDNS
			}
			labels = append(labels, string(msg[off+1:off+1+l]))
			off += 1 + l
		}
	}
	return "", 0, errDNS
}

// parsePTRAnswer zwraca pierwszy rekord PTR z sekcji odpowiedzi.
func parsePTRAnswer(msg []byte) (string, error) {
	if len(msg) < 12 || msg[2]&0x80 == 0 {
		return "", errDNS
	}
	qd, an := int(binary.BigEndian.Uint16(msg[4:])), int(binary.BigEndian.Uint16(msg[6:]))
	off := 12
	for i := 0; i < qd; i++ {
		_, next, err := readName(msg, off)
		if err != nil {
			return "", err
		}
		off = next + 4
	}
	for i := 0; i < an; i++ {
		_, next, err := readName(msg, off)
		if err != nil || next+10 > len(msg) {
			return "", errDNS
		}
		typ := binary.BigEndian.Uint16(msg[next:])
		rdlen := int(binary.BigEndian.Uint16(msg[next+8:]))
		rd := next + 10
		if rd+rdlen > len(msg) {
			return "", errDNS
		}
		if typ == 12 {
			name, _, err := readName(msg, rd)
			return name, err
		}
		off = rd + rdlen
	}
	return "", errDNS
}

// ---------- NetBIOS ----------

// NetBIOSName wysyła zapytanie o stan węzła NetBIOS (UDP 137) i zwraca
// nazwę komputera (Windows, Samba).
func NetBIOSName(ctx context.Context, ip string) string {
	resp, err := udpExchange(ctx, net.JoinHostPort(ip, "137"), NetBIOSStatusRequest(0x4e42))
	if err != nil {
		return ""
	}
	name, _ := parseNetBIOSStatus(resp)
	return name
}

// NetBIOSStatusRequest buduje zapytanie NBSTAT o nazwę "*".
func NetBIOSStatusRequest(id uint16) []byte {
	b := make([]byte, 12, 50)
	binary.BigEndian.PutUint16(b[0:], id)
	binary.BigEndian.PutUint16(b[4:], 1)
	b = append(b, 32)
	raw := [16]byte{'*'}
	for _, c := range raw {
		b = append(b, 'A'+c>>4, 'A'+c&0x0f)
	}
	b = append(b, 0, 0, 0x21, 0, 1) // koniec nazwy, NBSTAT, IN
	return b
}

func parseNetBIOSStatus(msg []byte) (string, error) {
	if len(msg) < 12 || msg[2]&0x80 == 0 || binary.BigEndian.Uint16(msg[6:]) == 0 {
		return "", errDNS
	}
	off := 12
	switch {
	case off < len(msg) && msg[off] == 32:
		off += 34
	case off+1 < len(msg) && msg[off]&0xc0 == 0xc0:
		off += 2
	default:
		return "", errDNS
	}
	off += 10 // typ, klasa, TTL, długość danych
	if off >= len(msg) {
		return "", errDNS
	}
	count := int(msg[off])
	off++
	for i := 0; i < count && off+18 <= len(msg); i++ {
		name := strings.TrimRight(string(msg[off:off+15]), " \x00")
		suffix := msg[off+15]
		flags := binary.BigEndian.Uint16(msg[off+16:])
		off += 18
		if suffix == 0x00 && flags&0x8000 == 0 && name != "" {
			return name, nil
		}
	}
	return "", errDNS
}

// ---------- tablica ARP komputera z panelem ----------

// NormalizeMAC sprowadza 0011.2233.4455, 00-11-22-33-44-55 i 0:11:22:… do
// postaci 00:11:22:33:44:55. Zwraca "" dla błędnych i zerowych adresów.
func NormalizeMAC(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var parts []string
	switch {
	case strings.Count(v, ".") == 2:
		for _, g := range strings.Split(v, ".") {
			if len(g) != 4 {
				return ""
			}
			parts = append(parts, g[:2], g[2:])
		}
	case strings.Count(v, ":") == 5 || strings.Count(v, "-") == 5:
		for _, g := range strings.FieldsFunc(v, func(r rune) bool { return r == ':' || r == '-' }) {
			if len(g) == 1 {
				g = "0" + g
			}
			parts = append(parts, g)
		}
	default:
		return ""
	}
	if len(parts) != 6 {
		return ""
	}
	for _, p := range parts {
		if _, err := strconv.ParseUint(p, 16, 8); err != nil || len(p) != 2 {
			return ""
		}
	}
	mac := strings.Join(parts, ":")
	if mac == "00:00:00:00:00:00" || mac == "ff:ff:ff:ff:ff:ff" {
		return ""
	}
	return mac
}

// ParseARPTable rozpoznaje tablicę sąsiadów z /proc/net/arp (Linux),
// `arp -a` (Windows) i `arp -an` (macOS/BSD): MAC → adresy IPv4.
func ParseARPTable(raw string) map[string][]string {
	out := map[string][]string{}
	for _, line := range strings.Split(raw, "\n") {
		var ip, mac string
		for _, f := range strings.Fields(strings.NewReplacer("(", " ", ")", " ").Replace(line)) {
			if ip == "" {
				if p := net.ParseIP(f); p != nil && p.To4() != nil {
					ip = p.To4().String()
					continue
				}
			}
			if mac == "" {
				mac = NormalizeMAC(f)
			}
		}
		if ip == "" || mac == "" || strings.HasPrefix(mac, "01:00:5e") {
			continue
		}
		dup := false
		for _, x := range out[mac] {
			dup = dup || x == ip
		}
		if !dup {
			out[mac] = append(out[mac], ip)
		}
	}
	return out
}

// LocalSubnet zwraca sieć IPv4 interfejsu z adresem local (np. 192.168.1.0/24),
// ograniczoną do /24 wokół local, żeby przeszukanie nie objęło tysięcy adresów.
func LocalSubnet(local net.IP) (*net.IPNet, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, ifc := range ifaces {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || !n.IP.Equal(local) || n.IP.To4() == nil {
				continue
			}
			ones, _ := n.Mask.Size()
			if ones < 24 {
				ones = 24
			}
			mask := net.CIDRMask(ones, 32)
			return &net.IPNet{IP: local.To4().Mask(mask), Mask: mask}, nil
		}
	}
	return nil, fmt.Errorf("nie znaleziono interfejsu z adresem %s", local)
}

// Hosts zwraca adresy hostów w sieci (bez adresu sieci i rozgłoszeniowego).
func Hosts(n *net.IPNet) []string {
	ones, bits := n.Mask.Size()
	if bits != 32 || ones < 22 || ones > 30 {
		return nil
	}
	base := binary.BigEndian.Uint32(n.IP.To4())
	size := uint32(1) << uint(32-ones)
	out := make([]string, 0, size-2)
	for i := uint32(1); i < size-1; i++ {
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, base+i)
		out = append(out, ip.String())
	}
	return out
}

// Sweep wysyła do każdego hosta sieci jedno zapytanie NetBIOS (UDP 137).
// System operacyjny musi przy tym ustalić adres MAC odbiorcy, więc po chwili
// tablica ARP komputera z panelem zawiera wszystkie aktywne urządzenia.
func Sweep(n *net.IPNet) int {
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return 0
	}
	defer conn.Close()
	req := NetBIOSStatusRequest(0x5357)
	sent := 0
	for _, h := range Hosts(n) {
		if _, err := conn.WriteToUDP(req, &net.UDPAddr{IP: net.ParseIP(h), Port: 137}); err == nil {
			sent++
		}
		if sent%32 == 0 {
			time.Sleep(5 * time.Millisecond)
		}
	}
	return sent
}

// SetLookupsForTest podmienia wszystkie zapytania sieciowe (tylko testy innych pakietów).
func SetLookupsForTest(r *Resolver, f func(context.Context, string) string) {
	none := func(context.Context, string) string { return "" }
	r.dns, r.mdns, r.netbios = f, none, none
}
