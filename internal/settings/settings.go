// Package settings przechowuje ustawienia zmieniane z poziomu panelu.
// Kolejność: wartość zapisana w panelu (data/settings.json), potem zmienna
// środowiskowa z .env, potem wartość domyślna. Hasła i dane dostępowe
// zostają wyłącznie w .env i nie są tu obsługiwane.
package settings

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"gs948mx-manager/internal/device"
)

type Def struct {
	Key     string   `json:"key"`
	Group   string   `json:"group"`
	Label   string   `json:"label"`
	Help    string   `json:"help"`
	Kind    string   `json:"kind"` // bool | int | seconds | choice | ports | text
	Default string   `json:"default"`
	Options []string `json:"options,omitempty"`
	Min     int      `json:"min,omitempty"`
	Max     int      `json:"max,omitempty"`
}

var Defs = []Def{
	{Key: "PROTECTED_PORTS", Group: "Bezpieczeństwo zmian", Label: "Porty chronione", Kind: "ports",
		Help: "Porty, których panel nie zmienia (np. uplink do routera i port, przez który działa panel). Numery, np. 1,49."},
	{Key: "ALLOW_PORT_SHUTDOWN", Group: "Bezpieczeństwo zmian", Label: "Zezwól na wyłączanie portów", Kind: "bool", Default: "false",
		Help: "Bez tego przyciski „Wyłącz port” są zablokowane."},
	{Key: "ALLOW_MANAGEMENT_DHCP_CHANGE", Group: "Bezpieczeństwo zmian", Label: "Zezwól na zmianę DHCP adresu zarządzającego", Kind: "bool", Default: "false",
		Help: "Zmiana może zmienić adres switcha i odciąć panel."},
	{Key: "CONFIG_BACKUP_RETENTION", Group: "Bezpieczeństwo zmian", Label: "Liczba przechowywanych kopii konfiguracji", Kind: "int", Default: "100", Min: 1, Max: 10000,
		Help: "Starsze kopie są usuwane automatycznie."},
	{Key: "SWITCH_SNMP_V3_PRIV_PROTOCOL", Group: "Połączenie ze switchem", Label: "Szyfrowanie SNMPv3", Kind: "choice", Default: "des", Options: []string{"des", "aes"},
		Help: "Musi zgadzać się z użytkownikiem SNMPv3 na switchu. Dotyczy tylko SNMPv3."},
	{Key: "SSH_COMMAND_TIMEOUT", Group: "Połączenie ze switchem", Label: "Limit czasu polecenia SSH (s)", Kind: "seconds", Default: "30", Min: 5, Max: 300,
		Help: "Po tym czasie panel przerywa czekanie na odpowiedź i łączy się od nowa."},
	{Key: "STATUS_CACHE_TTL", Group: "Wydajność", Label: "Pamięć stanu portów (s)", Kind: "seconds", Default: "5", Min: 0, Max: 300,
		Help: "Jak długo panel używa ostatniego odczytu SNMP. 0 wyłącza."},
	{Key: "READ_CACHE_TTL", Group: "Wydajność", Label: "Pamięć wyników poleceń show (s)", Kind: "seconds", Default: "5", Min: 0, Max: 300,
		Help: "Jak długo panel używa ostatniego wyniku polecenia. 0 wyłącza. Po każdej zmianie pamięć jest czyszczona."},
	{Key: "LIVE_INTERVAL", Group: "Wydajność", Label: "Odświeżanie portów na żywo (s)", Kind: "int", Default: "2", Min: 1, Max: 30,
		Help: "Jak często front switcha odczytuje stan portów i ruch, gdy panel jest otwarty. Mniej = szybciej, ale większe obciążenie switcha."},
	{Key: "PERF_LOG", Group: "Wydajność", Label: "Loguj czas każdego zapytania", Kind: "bool", Default: "false",
		Help: "Wpisy [perf] w logu panelu; bez tego logowane są tylko zapytania dłuższe niż 1,5 s."},
	{Key: "FIRMWARE_HTTP_ADDR", Group: "Firmware", Label: "Adres panelu dla switcha (IP:port)", Kind: "text",
		Help: "Puste: wykrywany automatycznie na podstawie połączenia ze switchem."},
	{Key: "FIRMWARE_HTTP_PORT", Group: "Firmware", Label: "Port serwera firmware", Kind: "int", Default: "8091", Min: 1024, Max: 65535,
		Help: "Używany przy automatycznym wykrywaniu adresu. Zapora musi przepuszczać połączenia ze switcha."},
}

func def(key string) (Def, bool) {
	for _, d := range Defs {
		if d.Key == key {
			return d, true
		}
	}
	return Def{}, false
}

type Store struct {
	path      string
	mu        sync.RWMutex
	vals      map[string]string
	listeners []func()
}

var current = &Store{vals: map[string]string{}}

// Load wczytuje plik ustawień (brak pliku nie jest błędem).
func Load(path string) error {
	s := &Store{path: path, vals: map[string]string{}}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &s.vals); err != nil {
			return fmt.Errorf("plik ustawień %s jest uszkodzony: %w", path, err)
		}
		for k := range s.vals {
			if _, ok := def(k); !ok {
				delete(s.vals, k)
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	current.mu.Lock()
	current.path, current.vals = s.path, s.vals
	current.mu.Unlock()
	return nil
}

// Get zwraca wartość: panel, potem .env, potem domyślna.
func Get(key string) string {
	current.mu.RLock()
	v, ok := current.vals[key]
	current.mu.RUnlock()
	if ok {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	if d, ok := def(key); ok {
		return d.Default
	}
	return ""
}

func Bool(key string) bool { return strings.EqualFold(Get(key), "true") }

func Int(key string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(Get(key), "s"))
	return n
}

// Source mówi, skąd pochodzi wartość: panel | env | default.
func Source(key string) string {
	current.mu.RLock()
	_, ok := current.vals[key]
	current.mu.RUnlock()
	if ok {
		return "panel"
	}
	if strings.TrimSpace(os.Getenv(key)) != "" {
		return "env"
	}
	return "default"
}

// OnChange rejestruje funkcję wywoływaną po zapisaniu ustawień.
func OnChange(f func()) {
	current.mu.Lock()
	current.listeners = append(current.listeners, f)
	current.mu.Unlock()
}

// Normalize sprawdza i ujednolica wartość podaną w panelu. Pusta wartość
// oznacza powrót do .env lub wartości domyślnej.
func Normalize(key, raw string) (string, error) {
	d, ok := def(key)
	if !ok {
		return "", fmt.Errorf("nieznane ustawienie %s", key)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	switch d.Kind {
	case "bool":
		switch strings.ToLower(raw) {
		case "true", "false":
			return strings.ToLower(raw), nil
		}
		return "", fmt.Errorf("%s: wartość true albo false", d.Label)
	case "int", "seconds":
		n, err := strconv.Atoi(strings.TrimSuffix(raw, "s"))
		if err != nil || n < d.Min || n > d.Max {
			return "", fmt.Errorf("%s: liczba od %d do %d", d.Label, d.Min, d.Max)
		}
		if d.Kind == "seconds" {
			return strconv.Itoa(n) + "s", nil
		}
		return strconv.Itoa(n), nil
	case "choice":
		for _, o := range d.Options {
			if strings.EqualFold(raw, o) {
				return o, nil
			}
		}
		return "", fmt.Errorf("%s: jedna z wartości %s", d.Label, strings.Join(d.Options, ", "))
	case "ports":
		// numery i zakresy (1,49 albo 1-2) albo nazwy port1.0.N
		raw = strings.NewReplacer("port1.0.", "", " ", "").Replace(raw)
		ports, err := device.ParsePortRanges(raw)
		if err != nil {
			return "", fmt.Errorf("%s: %w", d.Label, err)
		}
		return strings.Join(ports, ","), nil
	case "text":
		if key == "FIRMWARE_HTTP_ADDR" {
			host, port, err := net.SplitHostPort(raw)
			ip := net.ParseIP(host)
			if err != nil || port == "" || ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsLoopback() {
				return "", fmt.Errorf("%s: adres IPv4 z portem, np. 192.168.31.10:8091 (nie 0.0.0.0 ani 127.0.0.1)", d.Label)
			}
		}
		return raw, nil
	}
	return "", fmt.Errorf("nieobsługiwany rodzaj ustawienia")
}

// Save zapisuje zmiany z panelu. Klucz z pustą wartością wraca do .env lub domyślnej.
// Wszystkie wartości są sprawdzane przed zapisem; przy błędzie nic się nie zmienia.
func Save(changes map[string]string) error {
	normalized := map[string]string{}
	for k, v := range changes {
		n, err := Normalize(k, v)
		if err != nil {
			return err
		}
		normalized[k] = n
	}
	current.mu.Lock()
	next := map[string]string{}
	for k, v := range current.vals {
		next[k] = v
	}
	for k, v := range normalized {
		if v == "" {
			delete(next, k)
		} else {
			next[k] = v
		}
	}
	path := current.path
	if path == "" {
		current.mu.Unlock()
		return fmt.Errorf("plik ustawień nie został wczytany")
	}
	if err := writeAtomic(path, next); err != nil {
		current.mu.Unlock()
		return err
	}
	current.vals = next
	listeners := append([]func(){}, current.listeners...)
	current.mu.Unlock()
	for _, f := range listeners {
		f()
	}
	return nil
}

func writeAtomic(path string, vals map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]string, len(vals))
	for _, k := range keys {
		ordered[k] = vals[k]
	}
	b, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".settings-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// View to ustawienie z bieżącą wartością i jej źródłem, do wyświetlenia w panelu.
type View struct {
	Def
	Value  string `json:"value"`
	Source string `json:"source"`
}

func All() []View {
	out := make([]View, 0, len(Defs))
	for _, d := range Defs {
		out = append(out, View{Def: d, Value: Get(d.Key), Source: Source(d.Key)})
	}
	return out
}
