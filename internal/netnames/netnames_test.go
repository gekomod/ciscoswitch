package netnames

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestParseARPTableFormats(t *testing.T) {
	linux := `IP address       HW type     Flags       HW address            Mask     Device
192.168.31.1     0x1         0x2         50:64:2b:aa:bb:cc     *        eth0
192.168.31.9     0x1         0x0         00:00:00:00:00:00     *        eth0`
	windows := `
Interface: 192.168.31.10 --- 0xb
  Internet Address      Physical Address      Type
  192.168.31.1          50-64-2b-aa-bb-cc     dynamic
  192.168.31.20         a4-83-e7-01-02-03     dynamic
  224.0.0.22            01-00-5e-00-00-16     static
  192.168.31.255        ff-ff-ff-ff-ff-ff     static`
	mac := `? (192.168.31.30) at 0:1b:63:a:b:c on en0 ifscope [ethernet]
? (192.168.31.31) at (incomplete) on en0 ifscope [ethernet]`
	got := ParseARPTable(linux)
	if len(got) != 1 || got["50:64:2b:aa:bb:cc"][0] != "192.168.31.1" {
		t.Fatalf("linux: %v", got)
	}
	got = ParseARPTable(windows)
	if len(got) != 2 || got["a4:83:e7:01:02:03"][0] != "192.168.31.20" {
		t.Fatalf("windows: %v", got)
	}
	got = ParseARPTable(mac)
	if len(got) != 1 || got["00:1b:63:0a:0b:0c"][0] != "192.168.31.30" {
		t.Fatalf("mac: %v", got)
	}
}

func TestNormalizeMAC(t *testing.T) {
	for in, want := range map[string]string{"0050.56a6.9a73": "00:50:56:a6:9a:73", "00-50-56-A6-9A-73": "00:50:56:a6:9a:73", "0:50:56:a6:9a:73": "00:50:56:a6:9a:73", "0000.0000.0000": "", "zz:50:56:a6:9a:73": "", "port1.0.1": ""} {
		if got := NormalizeMAC(in); got != want {
			t.Errorf("%s → %q, want %q", in, got, want)
		}
	}
}

func TestPTRQueryAndAnswerRoundTrip(t *testing.T) {
	q := buildPTRQuery(7, reverseName("192.168.1.5"))
	name, next, err := readName(q, 12)
	if err != nil || name != "5.1.168.192.in-addr.arpa" || next+4 != len(q) {
		t.Fatalf("query name=%q err=%v", name, err)
	}
	// Odpowiedź: nagłówek, pytanie, odpowiedź z kompresją nazwy (wskaźnik na pytanie).
	resp := append([]byte{}, q...)
	resp[2] = 0x84
	binary.BigEndian.PutUint16(resp[6:], 1)
	rdata := []byte{7, 'l', 'a', 'p', 't', 'o', 'p', '1', 5, 'l', 'o', 'c', 'a', 'l', 0}
	resp = append(resp, 0xc0, 12, 0, 12, 0, 1, 0, 0, 0, 120, 0, byte(len(rdata)))
	resp = append(resp, rdata...)
	got, err := parsePTRAnswer(resp)
	if err != nil || got != "laptop1.local" || cleanName(got, "192.168.1.5") != "laptop1" {
		t.Fatalf("answer=%q err=%v", got, err)
	}
}

func TestParseNetBIOSStatus(t *testing.T) {
	req := NetBIOSStatusRequest(9)
	resp := append([]byte{}, req[:len(req)-4]...) // nagłówek + nazwa
	resp[2] = 0x84
	binary.BigEndian.PutUint16(resp[4:], 0)
	binary.BigEndian.PutUint16(resp[6:], 1)
	resp = append(resp, 0, 0x21, 0, 1, 0, 0, 0, 0, 0, 0) // typ, klasa, TTL, rdlength (pomijane)
	entry := func(name string, suffix byte, flags uint16) []byte {
		b := []byte(name)
		for len(b) < 15 {
			b = append(b, ' ')
		}
		b = append(b, suffix, byte(flags>>8), byte(flags))
		return b
	}
	resp = append(resp, 3)
	resp = append(resp, entry("WORKGROUP", 0x00, 0x8400)...) // grupa — pomijana
	resp = append(resp, entry("DESKTOP-JAREK", 0x00, 0x0400)...)
	resp = append(resp, entry("DESKTOP-JAREK", 0x20, 0x0400)...)
	got, err := parseNetBIOSStatus(resp)
	if err != nil || got != "DESKTOP-JAREK" {
		t.Fatalf("name=%q err=%v", got, err)
	}
}

func TestCleanNameRejectsAddressLikeNames(t *testing.T) {
	if cleanName("192-168-1-5.dynamic.example.net.", "192.168.1.5") != "" || cleanName("192.168.1.5", "192.168.1.5") != "" {
		t.Fatal("address-like names should be dropped")
	}
	if cleanName("nas.lan.", "192.168.1.5") != "nas.lan" {
		t.Fatal("trailing dot should be trimmed")
	}
}

func TestResolverPriorityAndCache(t *testing.T) {
	calls := 0
	r := NewResolver()
	r.dns = func(context.Context, string) string { calls++; return "" }
	r.mdns = func(context.Context, string) string { return "iphone.local." }
	r.netbios = func(context.Context, string) string { return "IPHONE" }
	got := r.Lookup(context.Background(), []string{"10.0.0.7", "10.0.0.7", "bad"})
	if got["10.0.0.7"] != (Result{"iphone", "mdns"}) || len(got) != 1 {
		t.Fatalf("got %v", got)
	}
	r.Lookup(context.Background(), []string{"10.0.0.7"})
	if calls != 1 {
		t.Fatalf("cache not used, calls=%d", calls)
	}
}

func TestHostsLimitsRange(t *testing.T) {
	_, n, _ := net.ParseCIDR("192.168.31.0/24")
	if h := Hosts(n); len(h) != 254 || h[0] != "192.168.31.1" || h[253] != "192.168.31.254" {
		t.Fatalf("hosts=%d", len(h))
	}
	_, big, _ := net.ParseCIDR("10.0.0.0/8")
	if Hosts(big) != nil {
		t.Fatal("huge networks must not be swept")
	}
}

func TestMDNSAgainstLocalResponder(t *testing.T) {
	// Lokalny „responder” na losowym porcie potwierdza wymianę UDP.
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 512)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		resp := append([]byte{}, buf[:n]...)
		resp[2] = 0x84
		binary.BigEndian.PutUint16(resp[6:], 1)
		rdata := []byte{3, 'n', 'a', 's', 0}
		resp = append(resp, 0xc0, 12, 0, 12, 0, 1, 0, 0, 0, 10, 0, byte(len(rdata)))
		_, _ = pc.WriteTo(append(resp, rdata...), addr)
	}()
	Timeout = time.Second
	resp, err := udpExchange(context.Background(), pc.LocalAddr().String(), buildPTRQuery(1, reverseName("127.0.0.1")))
	if err != nil {
		t.Fatal(err)
	}
	if name, err := parsePTRAnswer(resp); err != nil || name != "nas" {
		t.Fatalf("name=%q err=%v", name, err)
	}
}
