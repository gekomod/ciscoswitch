package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := Load(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPrecedencePanelEnvDefault(t *testing.T) {
	setup(t)
	if Get("SSH_COMMAND_TIMEOUT") != "30" || Source("SSH_COMMAND_TIMEOUT") != "default" {
		t.Fatal("default")
	}
	t.Setenv("SSH_COMMAND_TIMEOUT", "45s")
	if Get("SSH_COMMAND_TIMEOUT") != "45s" || Source("SSH_COMMAND_TIMEOUT") != "env" {
		t.Fatal("env")
	}
	if err := Save(map[string]string{"SSH_COMMAND_TIMEOUT": "60"}); err != nil {
		t.Fatal(err)
	}
	if Get("SSH_COMMAND_TIMEOUT") != "60s" || Source("SSH_COMMAND_TIMEOUT") != "panel" || Int("SSH_COMMAND_TIMEOUT") != 60 {
		t.Fatalf("panel: %q %s", Get("SSH_COMMAND_TIMEOUT"), Source("SSH_COMMAND_TIMEOUT"))
	}
	// pusta wartość = powrót do .env
	if err := Save(map[string]string{"SSH_COMMAND_TIMEOUT": ""}); err != nil {
		t.Fatal(err)
	}
	if Get("SSH_COMMAND_TIMEOUT") != "45s" {
		t.Fatal("reset to env")
	}
}

func TestNormalizeAndValidation(t *testing.T) {
	setup(t)
	ok := map[string][2]string{
		"PROTECTED_PORTS":              {"1, 49 ,port1.0.2", "port1.0.1,port1.0.2,port1.0.49"},
		"ALLOW_PORT_SHUTDOWN":          {"TRUE", "true"},
		"SWITCH_SNMP_V3_PRIV_PROTOCOL": {"AES", "aes"},
		"STATUS_CACHE_TTL":             {"0", "0s"},
		"FIRMWARE_HTTP_ADDR":           {"192.168.31.10:8091", "192.168.31.10:8091"},
	}
	for k, c := range ok {
		got, err := Normalize(k, c[0])
		if err != nil || got != c[1] {
			t.Errorf("%s %q: got %q %v", k, c[0], got, err)
		}
	}
	bad := map[string]string{
		"PROTECTED_PORTS":         "1;reload",
		"ALLOW_PORT_SHUTDOWN":     "yes please",
		"SSH_COMMAND_TIMEOUT":     "2",
		"CONFIG_BACKUP_RETENTION": "0",
		"FIRMWARE_HTTP_ADDR":      "0.0.0.0:8091",
		"FIRMWARE_HTTP_PORT":      "80",
		"SWITCH_SSH_PASSWORD":     "secret", // hasła nie są ustawieniami panelu
	}
	for k, v := range bad {
		if _, err := Normalize(k, v); err == nil {
			t.Errorf("%s=%q must be rejected", k, v)
		}
	}
}

func TestSaveIsAllOrNothingAndNotifies(t *testing.T) {
	p := setup(t)
	called := 0
	OnChange(func() { called++ })
	if err := Save(map[string]string{"PERF_LOG": "true", "SSH_COMMAND_TIMEOUT": "1"}); err == nil {
		t.Fatal("invalid batch must fail")
	}
	if Source("PERF_LOG") != "default" || called != 0 {
		t.Fatal("nothing may change after a failed save")
	}
	if err := Save(map[string]string{"PERF_LOG": "true"}); err != nil {
		t.Fatal(err)
	}
	if called != 1 || !Bool("PERF_LOG") {
		t.Fatal("listener / value")
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"PERF_LOG": "true"`) {
		t.Fatalf("file: %s", b)
	}
	// ponowne wczytanie z pliku
	if err := Load(p); err != nil || !Bool("PERF_LOG") {
		t.Fatal("reload")
	}
}

func TestUnknownKeysInFileAreIgnored(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	os.WriteFile(p, []byte(`{"SWITCH_SSH_PASSWORD":"x","PERF_LOG":"true"}`), 0o600)
	if err := Load(p); err != nil {
		t.Fatal(err)
	}
	if Source("SWITCH_SSH_PASSWORD") == "panel" || !Bool("PERF_LOG") {
		t.Fatal("unknown keys must not be loaded")
	}
}
