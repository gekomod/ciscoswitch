package settings

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"gs948mx-manager/internal/device"
)

// Connection to dane dostępu do switcha. Zapisuje je instalator pierwszego
// uruchomienia (lub administrator w panelu) w data/connection.json z prawami
// 0600. Gdy pliku nie ma, wartości pochodzą z .env (zgodność ze starszymi
// instalacjami).
type Connection struct {
	Host               string `json:"host"`
	SSHPort            string `json:"sshPort"`
	SSHUser            string `json:"sshUser"`
	SSHPassword        string `json:"sshPassword"`
	SSHHostKey         string `json:"sshHostKey"`
	SNMPVersion        string `json:"snmpVersion"`
	SNMPPort           string `json:"snmpPort"`
	SNMPCommunity      string `json:"snmpCommunity"`
	SNMPV3User         string `json:"snmpV3User"`
	SNMPV3AuthPassword string `json:"snmpV3AuthPassword"`
	SNMPV3PrivPassword string `json:"snmpV3PrivPassword"`
}

var conn struct {
	mu     sync.RWMutex
	path   string
	fromFS bool
	value  Connection
}

// LoadConnection wczytuje data/connection.json (brak pliku nie jest błędem).
func LoadConnection(path string) error {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	conn.path, conn.fromFS, conn.value = path, false, Connection{}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var c Connection
	if err := json.Unmarshal(b, &c); err != nil {
		return fmt.Errorf("plik połączenia %s jest uszkodzony: %w", path, err)
	}
	conn.fromFS, conn.value = true, c.withDefaults()
	return nil
}

func envConnection() Connection {
	g := func(k string) string { return strings.TrimSpace(os.Getenv(k)) }
	return Connection{
		Host: g("SWITCH_HOST"), SSHPort: g("SWITCH_SSH_PORT"), SSHUser: g("SWITCH_SSH_USER"),
		SSHPassword: os.Getenv("SWITCH_SSH_PASSWORD"), SSHHostKey: g("SWITCH_SSH_HOST_KEY"),
		SNMPVersion: g("SWITCH_SNMP_VERSION"), SNMPPort: g("SWITCH_SNMP_PORT"), SNMPCommunity: os.Getenv("SWITCH_SNMP_COMMUNITY"),
		SNMPV3User: g("SWITCH_SNMP_V3_USER"), SNMPV3AuthPassword: os.Getenv("SWITCH_SNMP_V3_AUTH_PASSWORD"),
		SNMPV3PrivPassword: os.Getenv("SWITCH_SNMP_V3_PRIV_PASSWORD"),
	}.withDefaults()
}

func (c Connection) withDefaults() Connection {
	if c.SSHPort == "" {
		c.SSHPort = "22"
	}
	if c.SNMPPort == "" {
		c.SNMPPort = "161"
	}
	if c.SNMPVersion == "" || c.SNMPVersion == "2" || c.SNMPVersion == "v2c" {
		c.SNMPVersion = "2c"
	}
	if c.SNMPVersion == "v3" {
		c.SNMPVersion = "3"
	}
	return c
}

// CurrentConnection zwraca dane z pliku, a gdy go nie ma — z .env.
func CurrentConnection() Connection {
	conn.mu.RLock()
	defer conn.mu.RUnlock()
	if conn.fromFS {
		return conn.value
	}
	return envConnection()
}

// ConnectionSource: "panel" (data/connection.json), "env" albo "none".
func ConnectionSource() string {
	conn.mu.RLock()
	fromFS := conn.fromFS
	conn.mu.RUnlock()
	if fromFS {
		return "panel"
	}
	if envConnection().Host != "" {
		return "env"
	}
	return "none"
}

// Validate sprawdza kompletność danych połączenia (z przypiętym kluczem SSH).
func (c Connection) Validate() error {
	c = c.withDefaults()
	if ip := net.ParseIP(c.Host); ip == nil && !validHostname(c.Host) {
		return fmt.Errorf("adres switcha: podaj adres IP lub nazwę hosta")
	}
	for name, p := range map[string]string{"port SSH": c.SSHPort, "port SNMP": c.SNMPPort} {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("%s: liczba 1–65535", name)
		}
	}
	if c.SSHUser == "" || strings.ContainsAny(c.SSHUser, " \t\r\n") {
		return fmt.Errorf("login SSH jest wymagany (bez spacji)")
	}
	if c.SSHPassword == "" {
		return fmt.Errorf("hasło SSH jest wymagane")
	}
	if !strings.HasPrefix(c.SSHHostKey, "SHA256:") {
		return fmt.Errorf("odcisk klucza SSH switcha jest wymagany (pobierz go przyciskiem i porównaj)")
	}
	switch c.SNMPVersion {
	case "2c":
		if c.SNMPCommunity == "" {
			return fmt.Errorf("community SNMP jest wymagane")
		}
	case "3":
		if c.SNMPV3User == "" || len(c.SNMPV3AuthPassword) < 8 || len(c.SNMPV3PrivPassword) < 8 {
			return fmt.Errorf("SNMPv3: użytkownik oraz hasła auth i priv (min. 8 znaków) są wymagane")
		}
	default:
		return fmt.Errorf("wersja SNMP: 2c albo 3")
	}
	return nil
}

func validHostname(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, part := range strings.Split(h, ".") {
		if part == "" || len(part) > 63 || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return false
		}
		for _, r := range part {
			if !(r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
				return false
			}
		}
	}
	return true
}

// SaveConnection zapisuje dane połączenia (0600) i od tej chwili ma ono
// pierwszeństwo przed .env.
func SaveConnection(c Connection) error {
	c = c.withDefaults()
	if err := c.Validate(); err != nil {
		return err
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.path == "" {
		return fmt.Errorf("plik połączenia nie został wczytany")
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(conn.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".connection-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), conn.path); err != nil {
		return err
	}
	conn.fromFS, conn.value = true, c
	return nil
}

// Apply przenosi dane połączenia do konfiguracji urządzenia.
func (c Connection) Apply(cfg *device.Config) {
	c = c.withDefaults()
	cfg.Host, cfg.SSHPort, cfg.SSHUser, cfg.SSHPassword = c.Host, c.SSHPort, c.SSHUser, c.SSHPassword
	cfg.SSHHostKeyFingerprint = c.SSHHostKey
	cfg.SNMPVersion, cfg.SNMPPort, cfg.SNMPCommunity = c.SNMPVersion, c.SNMPPort, c.SNMPCommunity
	cfg.SNMPV3User, cfg.SNMPV3AuthPassword, cfg.SNMPV3PrivacyPassword = c.SNMPV3User, c.SNMPV3AuthPassword, c.SNMPV3PrivPassword
	cfg.SNMPV3PrivacyProtocol = Get("SWITCH_SNMP_V3_PRIV_PROTOCOL")
}
