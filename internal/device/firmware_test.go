package device

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseDirFree(t *testing.T) {
	cases := map[string]int64{
		"x\n2.5M of 63.0M bytes free\n": 5 << 19,
		"512K of 63M bytes free":        512 << 10,
		"nothing here":                  -1,
	}
	for in, want := range cases {
		if got := ParseDirFree(in); got != want {
			t.Errorf("%q: got %d want %d", in, got, want)
		}
	}
}

func TestFirmwarePrecheck(t *testing.T) {
	dir := sampleDir // 1.9M wolne
	if _, err := FirmwarePrecheck("GS900-5.4.7-0.1.rel", 18<<20, dir); err == nil || !strings.Contains(err.Error(), "za mało miejsca") {
		t.Fatalf("expected space error, got %v", err)
	}
	if _, err := FirmwarePrecheck("GS900-5.4.6-2.1.rel", 1, dir); err == nil {
		t.Fatal("existing file must be refused")
	}
	if _, err := FirmwarePrecheck("../x.rel", 1, dir); err == nil {
		t.Fatal("bad name")
	}
	if w, err := FirmwarePrecheck("new.rel", 1, "no free info"); err != nil || w == "" {
		t.Fatalf("unknown free space must warn, not fail: %q %v", w, err)
	}
}

// Pełny przebieg na atrapie: switch pobiera plik z serwera HTTP, panel sprawdza rozmiar.
func TestCopyFirmwareEndToEnd(t *testing.T) {
	payload := bytes.Repeat([]byte("F"), 2<<20)
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fw/tok/new.rel" {
			http.NotFound(w, r)
			return
		}
		w.Write(payload)
	}))
	defer web.Close()
	f := startFakeSwitch(t)
	s := fakeService(f)
	defer s.Close()
	if _, err := s.CopyFirmwareFromURL(web.URL+"/fw/tok/new.rel", "new.rel", int64(len(payload)), 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if f.copied["new.rel"] != int64(len(payload)) {
		t.Fatalf("switch got %d bytes", f.copied["new.rel"])
	}
	// zły adres: switch zgłasza błąd, plik nie powstaje
	if _, err := s.CopyFirmwareFromURL(web.URL+"/fw/zly/other.rel", "other.rel", 10, 10*time.Second); err == nil {
		t.Fatal("failed download must be reported")
	}
	if _, err := s.CopyFirmwareFromURL("http://x/a.rel; reload", "a.rel", 1, time.Second); err == nil {
		t.Fatal("injection must be rejected")
	}
}
