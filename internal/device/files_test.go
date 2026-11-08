package device

import (
	"reflect"
	"testing"
)

// Format wzorowany na AlliedWare Plus; do potwierdzenia na prawdziwym GS948MX.
const sampleDir = `      588 -rw- Nov  8 2026 10:12:33  flash:/default.cfg
 18874368 -rw- Oct  2 2025 08:01:10  flash:/GS900-5.4.6-2.1.rel
 18712001 -rw- Mar 11 2024 17:45:02  flash:/GS900-5.4.5-1.3.rel
     4096 drwx Jan  1 2020 00:00:00  flash:/.configs/
      210 -rw- Nov  8 2026 10:15:00  flash:/exception.log

1.9M of 63.0M bytes free
`

const sampleBoot = `Boot configuration
----------------------------------------------------------------
Current software   : GS900-5.4.6-2.1.rel
Current boot image : flash:/GS900-5.4.6-2.1.rel
Backup boot image  : Not set
Default boot config: flash:/default.cfg
Current boot config: flash:/default.cfg (file exists)
`

func TestParseDir(t *testing.T) {
	files := ParseDir(sampleDir)
	if len(files) != 4 {
		t.Fatalf("expected 4 files (directory skipped), got %d: %+v", len(files), files)
	}
	if files[1].Base != "GS900-5.4.6-2.1.rel" || files[1].Kind != "firmware" || files[1].Size != 18874368 || files[1].Date != "Oct 2 2025 08:01:10" {
		t.Fatalf("unexpected %+v", files[1])
	}
	if files[0].Kind != "config" || files[3].Kind != "other" {
		t.Fatalf("kinds %+v", files)
	}
}

func TestParseBoot(t *testing.T) {
	b := ParseBoot(sampleBoot)
	if len(b) != 5 || b[1].Key != "Current boot image" || b[1].Value != "flash:/GS900-5.4.6-2.1.rel" || b[3].Key != "Default boot config" {
		t.Fatalf("unexpected %+v", b)
	}
}

func TestFileCommands(t *testing.T) {
	files := ParseDir(sampleDir)
	cases := []struct {
		action, name string
		want         []string
	}{
		{FileActionBootImage, "GS900-5.4.5-1.3.rel", []string{"configure terminal", "boot system flash:/GS900-5.4.5-1.3.rel", "end"}},
		{FileActionBackupBoot, "flash:/GS900-5.4.5-1.3.rel", []string{"configure terminal", "boot system backup flash:/GS900-5.4.5-1.3.rel", "end"}},
		{FileActionBootConfig, "default.cfg", []string{"configure terminal", "boot config-file flash:/default.cfg", "end"}},
		{FileActionSaveAs, "przed-zmianami", []string{"copy running-config flash:/przed-zmianami.cfg"}},
	}
	for _, c := range cases {
		got, err := BuildFileCommands(c.action, c.name, files)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %s: got %v %v", c.action, c.name, got, err)
		}
	}
	bad := []struct{ action, name string }{
		{FileActionBootImage, "default.cfg"},          // nie firmware
		{FileActionBootImage, "nieistniejacy.rel"},    // brak pliku
		{FileActionBootConfig, "GS900-5.4.6-2.1.rel"}, // nie konfiguracja
		{FileActionSaveAs, "default"},                 // już istnieje
		{FileActionSaveAs, "../etc/x"},                // niedozwolone znaki
		{FileActionSaveAs, "a b; reload"},             // wstrzyknięcie polecenia
		{"delete", "default.cfg"},                     // nieobsługiwana akcja
	}
	for _, b := range bad {
		if _, err := BuildFileCommands(b.action, b.name, files); err == nil {
			t.Errorf("%s %q must be rejected", b.action, b.name)
		}
	}
}
