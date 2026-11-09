package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFirmwareAddrValid(t *testing.T) {
	for _, ok := range []string{"192.168.31.10:8091", "10.0.0.5:80"} {
		if err := firmwareAddrValid(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "0.0.0.0:8091", "127.0.0.1:8091", "192.168.1.1", "host:80", "[::1]:80"} {
		if err := firmwareAddrValid(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func freeAddr(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := ln.Addr().String()
	ln.Close()
	return a
}

func TestServeOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fw.rel")
	os.WriteFile(path, []byte("firmware-bytes"), 0o600)
	url, stop, err := serveOnce(freeAddr(t), "fw.rel", path, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "firmware-bytes" {
		t.Fatalf("got %q", b)
	}
	bad := url[:strings.LastIndex(url, "/fw/")] + "/fw/zgadniety-token/fw.rel"
	if r, _ := http.Get(bad); r == nil || r.StatusCode != 404 {
		t.Fatal("wrong token must give 404")
	}
	stop()
	if _, err := http.Get(url); err == nil {
		t.Fatal("server must be closed after stop")
	}
}

func uploadReq(t *testing.T, name string, size int) *http.Request {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write(bytes.Repeat([]byte("x"), size))
	mw.Close()
	r := httptest.NewRequest(http.MethodPost, "/api/firmware/upload", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r
}

func TestFirmwareUpload(t *testing.T) {
	m := &firmwareManager{dir: t.TempDir()}
	w := httptest.NewRecorder()
	f, err := m.upload(w, uploadReq(t, "GS900-5.4.7.rel", 2<<20))
	if err != nil || f.Size != 2<<20 || len(f.SHA256) != 64 {
		t.Fatalf("%+v %v", f, err)
	}
	if list := m.list(); len(list) != 1 || list[0].SHA256 != f.SHA256 {
		t.Fatalf("%+v", list)
	}
	for _, c := range []struct {
		name string
		size int
		why  string
	}{
		{"GS900-5.4.7.rel", 2 << 20, "już"},
		{"evil.sh", 2 << 20, ".rel"},
		{"small.rel", 1000, "firmware"},
	} {
		if _, err := m.upload(httptest.NewRecorder(), uploadReq(t, c.name, c.size)); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	// nazwa ze ścieżką jest sprowadzana do samej nazwy pliku
	if f, err := m.upload(httptest.NewRecorder(), uploadReq(t, "../../etc/x.rel", 2<<20)); err != nil || f.Name != "x.rel" {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := os.Stat(filepath.Join(m.dir, "x.rel")); err != nil {
		t.Fatal("file must stay inside firmware dir")
	}
	j, _ := json.Marshal(m.list())
	if strings.Contains(string(j), "upload-") {
		t.Fatal("temp files must not be listed")
	}
}
