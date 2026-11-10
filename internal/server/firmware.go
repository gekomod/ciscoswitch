package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"gs948mx-manager/internal/settings"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gs948mx-manager/internal/device"
)

const (
	firmwareMaxUpload   = 128 << 20
	firmwareCopyTimeout = 15 * time.Minute
)

type firmwareFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	Uploaded string `json:"uploaded"`
}

type firmwareJob struct {
	Name     string `json:"name"`
	State    string `json:"state"` // running | done | error
	Message  string `json:"message"`
	Warning  string `json:"warning,omitempty"`
	Started  string `json:"started"`
	Finished string `json:"finished,omitempty"`
}

type firmwareManager struct {
	dir string
	svc *device.Service
	mu  sync.Mutex
	job *firmwareJob
}

// firmwareAddrValid wymaga konkretnego adresu IP (switch musi wiedzieć, dokąd się połączyć).
func firmwareAddrValid(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return fmt.Errorf("FIRMWARE_HTTP_ADDR ma postać IP:port, np. 192.168.31.10:8091")
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsLoopback() {
		return fmt.Errorf("FIRMWARE_HTTP_ADDR musi zawierać adres IPv4 komputera z panelem widoczny dla switcha (nie 0.0.0.0 ani 127.0.0.1)")
	}
	return nil
}

func (m *firmwareManager) list() []firmwareFile {
	out := []firmwareFile{}
	entries, _ := os.ReadDir(m.dir)
	for _, e := range entries {
		if e.IsDir() || !device.ValidFirmwareName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		sum, _ := os.ReadFile(filepath.Join(m.dir, e.Name()+".sha256"))
		out = append(out, firmwareFile{Name: e.Name(), Size: info.Size(), SHA256: strings.TrimSpace(string(sum)), Uploaded: info.ModTime().Format("2006-01-02 15:04")})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *firmwareManager) upload(w http.ResponseWriter, r *http.Request) (firmwareFile, error) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(10 * time.Minute))
	_ = rc.SetWriteDeadline(time.Now().Add(11 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, firmwareMaxUpload)
	file, header, err := r.FormFile("file")
	if err != nil {
		return firmwareFile{}, fmt.Errorf("nie udało się odebrać pliku (maks. 128 MB): %w", err)
	}
	defer file.Close()
	name := filepath.Base(header.Filename)
	if !device.ValidFirmwareName(name) {
		return firmwareFile{}, fmt.Errorf("plik musi mieć rozszerzenie .rel i nazwę z liter, cyfr, kropek, myślników lub podkreśleń")
	}
	if err := os.MkdirAll(m.dir, 0o750); err != nil {
		return firmwareFile{}, err
	}
	final := filepath.Join(m.dir, name)
	if _, err := os.Stat(final); err == nil {
		return firmwareFile{}, fmt.Errorf("plik %s jest już w panelu", name)
	}
	tmp, err := os.CreateTemp(m.dir, ".upload-*")
	if err != nil {
		return firmwareFile{}, err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), file)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return firmwareFile{}, fmt.Errorf("zapis pliku nie powiódł się: %w", err)
	}
	if size < 1<<20 {
		return firmwareFile{}, fmt.Errorf("plik ma tylko %d B; to nie wygląda na firmware", size)
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return firmwareFile{}, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	_ = os.WriteFile(final+".sha256", []byte(sum+"\n"), 0o640)
	return firmwareFile{Name: name, Size: size, SHA256: sum, Uploaded: time.Now().Format("2006-01-02 15:04")}, nil
}

// serveOnce uruchamia tymczasowy serwer HTTP, który udostępnia jeden plik pod
// losowym adresem. Zwraca URL i funkcję zamykającą serwer.
func serveOnce(addr, name, path string, lifetime time.Duration) (string, func(), error) {
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", nil, err
	}
	token := hex.EncodeToString(tokenBytes)
	// Nasłuch na wszystkich interfejsach na porcie z adresu; switch dostaje adres
	// z addr. Dzięki temu działa też w kontenerze z przekierowanym portem, gdzie
	// adres hosta nie istnieje wewnątrz kontenera.
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", nil, err
	}
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		return "", nil, fmt.Errorf("nie udało się nasłuchiwać na porcie %s: %w", port, err)
	}
	want := "/fw/" + token + "/" + name
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != want || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			http.NotFound(w, r)
			return
		}
		f, err := os.Open(path)
		if err != nil {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		defer f.Close()
		info, _ := f.Stat()
		log.Printf("firmware: switch %s pobiera %s", r.RemoteAddr, name)
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, name, info.ModTime(), f)
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	timer := time.AfterFunc(lifetime, func() { _ = srv.Close() })
	var once sync.Once
	stop := func() {
		once.Do(func() {
			timer.Stop()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		})
	}
	return "http://" + addr + want, stop, nil
}

// address zwraca adres serwera firmware: z ustawień panelu albo .env, a gdy brak,
// adres tego komputera widziany przez switch z portem FIRMWARE_HTTP_PORT.
func (m *firmwareManager) address() (addr string, auto bool, err error) {
	if a := strings.TrimSpace(settings.Get("FIRMWARE_HTTP_ADDR")); a != "" {
		return a, false, firmwareAddrValid(a)
	}
	ip, err := m.svc.LocalIP()
	if err != nil {
		return "", true, fmt.Errorf("nie udało się wykryć adresu panelu: %w; ustaw go w Ustawieniach", err)
	}
	port := settings.Int("FIRMWARE_HTTP_PORT")
	if port == 0 {
		port = 8091
	}
	a := net.JoinHostPort(ip.String(), fmt.Sprint(port))
	return a, true, firmwareAddrValid(a)
}

func (m *firmwareManager) install(name string) error {
	addr, _, err := m.address()
	if err != nil {
		return err
	}
	if !device.ValidFirmwareName(name) {
		return fmt.Errorf("nieprawidłowa nazwa pliku")
	}
	path := filepath.Join(m.dir, name)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("pliku %s nie ma w panelu", name)
	}
	m.mu.Lock()
	if m.job != nil && m.job.State == "running" {
		m.mu.Unlock()
		return fmt.Errorf("trwa już kopiowanie %s", m.job.Name)
	}
	job := &firmwareJob{Name: name, State: "running", Message: "Sprawdzam miejsce we flash…", Started: time.Now().Format("15:04:05")}
	m.job = job
	m.mu.Unlock()

	set := func(f func(j *firmwareJob)) {
		m.mu.Lock()
		f(job)
		m.mu.Unlock()
	}
	go func() {
		finish := func(err error) {
			set(func(j *firmwareJob) {
				j.Finished = time.Now().Format("15:04:05")
				if err != nil {
					j.State, j.Message = "error", err.Error()
				} else {
					j.State, j.Message = "done", "Firmware skopiowany do flash i sprawdzony (rozmiar się zgadza). Aby go użyć, ustaw go jako firmware startowy i zrestartuj switch."
				}
			})
		}
		dir, err := m.svc.RunReadOnlyFresh("dir")
		if err != nil {
			finish(fmt.Errorf("nie udało się odczytać flash: %w", err))
			return
		}
		warning, err := device.FirmwarePrecheck(name, info.Size(), dir)
		if err != nil {
			finish(err)
			return
		}
		url, stop, err := serveOnce(addr, name, path, firmwareCopyTimeout+time.Minute)
		if err != nil {
			finish(err)
			return
		}
		defer stop()
		set(func(j *firmwareJob) {
			j.Warning = warning
			j.Message = fmt.Sprintf("Switch pobiera plik (%.1f MB). To może potrwać kilka minut; nie wyłączaj switcha ani panelu.", float64(info.Size())/(1<<20))
		})
		_, err = m.svc.CopyFirmwareFromURL(url, name, info.Size(), firmwareCopyTimeout)
		finish(err)
	}()
	return nil
}

func (m *firmwareManager) currentJob() *firmwareJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.job == nil {
		return nil
	}
	j := *m.job
	return &j
}

// registerFirmwareRoutes: pliki firmware w panelu i kopiowanie do switcha.
func registerFirmwareRoutes(apiMux *http.ServeMux, svc *device.Service, backupDir string) {
	m := &firmwareManager{
		dir: envOr("FIRMWARE_DIR", filepath.Join(filepath.Dir(backupDir), "firmware")),
		svc: svc,
	}
	apiMux.HandleFunc("/api/firmware", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cfgErr := ""
		addr, auto, err := m.address()
		if err != nil {
			cfgErr = err.Error()
		}
		writeJSON(w, map[string]any{"files": m.list(), "job": m.currentJob(), "address": addr, "autoAddress": auto, "configError": cfgErr}, nil)
	})
	apiMux.HandleFunc("/api/firmware/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		f, err := m.upload(w, r)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, f, nil)
	})
	apiMux.HandleFunc("/api/firmware/install", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var q struct {
			Name string `json:"name"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if json.NewDecoder(r.Body).Decode(&q) != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		write(w, "Kopiowanie rozpoczęte", m.install(q.Name))
	})
	apiMux.HandleFunc("/api/firmware/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var q struct {
			Name string `json:"name"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if json.NewDecoder(r.Body).Decode(&q) != nil || !device.ValidFirmwareName(q.Name) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if j := m.currentJob(); j != nil && j.State == "running" && j.Name == q.Name {
			write(w, "", fmt.Errorf("plik jest właśnie kopiowany"))
			return
		}
		err := os.Remove(filepath.Join(m.dir, q.Name))
		_ = os.Remove(filepath.Join(m.dir, q.Name+".sha256"))
		write(w, "Usunięto z panelu", err)
	})
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
