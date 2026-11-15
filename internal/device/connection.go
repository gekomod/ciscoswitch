package device

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

var errNoSSH = errors.New("brak danych SSH switcha: uruchom instalator lub ustaw połączenie w Ustawieniach panelu")

func (s *Service) sshConfigured() bool {
	c := s.snmpConfig()
	return c.Host != "" && c.SSHUser != "" && c.SSHPassword != ""
}

func sshPort(c Config) string {
	if strings.TrimSpace(c.SSHPort) == "" {
		return "22"
	}
	return c.SSHPort
}

func dialTimeout(c Config) time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 4 * time.Second
}

// SetConnection podmienia dane dostępu do switcha w działającym panelu
// (instalator, zmiana w ustawieniach). Ustawienia wydajności zostają bez zmian,
// współdzielone połączenie SSH jest zamykane, a cache czyszczony.
func (s *Service) SetConnection(c Config) {
	s.tuneMu.Lock()
	s.cfg.Host, s.cfg.SSHPort, s.cfg.SSHUser, s.cfg.SSHPassword = c.Host, c.SSHPort, c.SSHUser, c.SSHPassword
	s.cfg.SSHHostKeyFingerprint = c.SSHHostKeyFingerprint
	s.cfg.SNMPCommunity, s.cfg.SNMPPort, s.cfg.SNMPVersion = c.SNMPCommunity, c.SNMPPort, c.SNMPVersion
	s.cfg.SNMPV3User, s.cfg.SNMPV3AuthPassword, s.cfg.SNMPV3PrivacyPassword = c.SNMPV3User, c.SNMPV3AuthPassword, c.SNMPV3PrivacyPassword
	if c.SNMPV3PrivacyProtocol != "" {
		s.cfg.SNMPV3PrivacyProtocol = c.SNMPV3PrivacyProtocol
	}
	s.tuneMu.Unlock()
	s.Close()
	s.InvalidateCache()
}

// Configured mówi, czy panel ma adres switcha.
func (s *Service) Configured() bool { return strings.TrimSpace(s.snmpConfig().Host) != "" }

var errKeyCaptured = errors.New("klucz odczytany")

// FetchHostKeyFingerprint łączy się z serwerem SSH switcha tylko po to, by
// odczytać jego klucz (SHA256). Nie loguje się; użytkownik musi porównać
// odcisk z wyświetlanym na switchu (show crypto key hostkey) przed zapisem.
func FetchHostKeyFingerprint(host, port string, timeout time.Duration) (string, error) {
	if strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("podaj adres switcha")
	}
	if port == "" {
		port = "22"
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	var fp string
	_, err := ssh.Dial("tcp", net.JoinHostPort(host, port), &ssh.ClientConfig{
		User: "fingerprint-probe",
		Auth: []ssh.AuthMethod{ssh.Password("")},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fp = ssh.FingerprintSHA256(key)
			return errKeyCaptured
		},
		Timeout: timeout,
	})
	if fp != "" {
		return fp, nil
	}
	if err == nil {
		return "", fmt.Errorf("switch nie przedstawił klucza SSH")
	}
	return "", fmt.Errorf("nie udało się połączyć z %s:%s: %v", host, port, err)
}

// ConnectionTest to wynik sprawdzenia połączenia przez instalator.
type ConnectionTest struct {
	SSHOK     bool   `json:"sshOk"`
	SSHError  string `json:"sshError,omitempty"`
	SNMPOK    bool   `json:"snmpOk"`
	SNMPError string `json:"snmpError,omitempty"`
	Name      string `json:"name,omitempty"`
	Model     string `json:"model,omitempty"`
}

// TestConnection sprawdza SSH (logowanie i polecenie show) oraz SNMP dla
// podanych danych, bez zmiany konfiguracji działającego panelu.
func TestConnection(c Config) ConnectionTest {
	var out ConnectionTest
	if c.Timeout <= 0 {
		c.Timeout = 5 * time.Second
	}
	tmp := New(c)
	defer tmp.Close()
	if !tmp.sshConfigured() {
		out.SSHError = "podaj adres, login i hasło SSH"
	} else if _, err := tmp.ssh.output(tmp.dialSSH, "show system", 20*time.Second); err != nil {
		out.SSHError = err.Error()
	} else {
		out.SSHOK = true
	}
	id, err := tmp.DeviceIdentity()
	if err != nil {
		out.SNMPError = err.Error()
	} else {
		out.SNMPOK, out.Name, out.Model = true, id.Name, id.Description
	}
	return out
}
