package device

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// FlashFile to pozycja z polecenia "dir" (pamięć flash switcha).
type FlashFile struct {
	Name string `json:"name"` // pełna ścieżka, np. flash:/default.cfg
	Base string `json:"base"` // sama nazwa pliku
	Size int64  `json:"size"`
	Date string `json:"date"`
	Kind string `json:"kind"` // firmware | config | other
}

// BootField to para "nazwa : wartość" z "show boot".
type BootField struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type FilesInfo struct {
	Files   []FlashFile `json:"files"`
	Boot    []BootField `json:"boot"`
	RawDir  string      `json:"rawDir"`
	RawBoot string      `json:"rawBoot"`
}

var (
	dirLineRE   = regexp.MustCompile(`^\s*(\d+)\s+[-dlrwx]{3,}\s+(.+?)\s+((?:flash|nvs|card|usb)[^\s]*:\S*)\s*$`)
	bootFieldRE = regexp.MustCompile(`^\s*([A-Za-z][A-Za-z ()/-]*?)\s*:\s*(.*?)\s*$`)
	safeFileRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

func fileKind(name string) string {
	l := strings.ToLower(name)
	switch {
	case strings.HasSuffix(l, ".rel"):
		return "firmware"
	case strings.HasSuffix(l, ".cfg"):
		return "config"
	}
	return "other"
}

// ParseDir czyta wynik "dir". Rozpoznaje linie z rozmiarem, uprawnieniami, datą
// i ścieżką z prefiksem systemu plików (flash:/...). Inne linie są pomijane.
func ParseDir(raw string) []FlashFile {
	var out []FlashFile
	for _, line := range strings.Split(raw, "\n") {
		m := dirLineRE.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		size, _ := strconv.ParseInt(m[1], 10, 64)
		name := m[3]
		base := name[strings.LastIndexAny(name, ":/")+1:]
		if base == "" {
			continue
		}
		out = append(out, FlashFile{Name: name, Base: base, Size: size, Date: strings.Join(strings.Fields(m[2]), " "), Kind: fileKind(base)})
	}
	return out
}

// ParseBoot czyta "show boot" jako listę par "nazwa : wartość", bez założeń
// co do ich kolejności czy dokładnych nazw.
func ParseBoot(raw string) []BootField {
	var out []BootField
	for _, line := range strings.Split(raw, "\n") {
		l := strings.TrimRight(line, "\r")
		if strings.Trim(l, "- ") == "" {
			continue
		}
		m := bootFieldRE.FindStringSubmatch(l)
		if m == nil || m[2] == "" {
			continue
		}
		out = append(out, BootField{Key: m[1], Value: m[2]})
	}
	return out
}

// Files odczytuje zawartość flash i ustawienia startu.
func (s *Service) Files() (FilesInfo, error) {
	dir, err := s.RunReadOnly("dir")
	if err != nil {
		return FilesInfo{}, fmt.Errorf("nie udało się odczytać listy plików: %w", err)
	}
	boot, err := s.RunReadOnly("show boot")
	if err != nil {
		return FilesInfo{}, fmt.Errorf("nie udało się odczytać ustawień startu: %w", err)
	}
	return FilesInfo{Files: ParseDir(dir), Boot: ParseBoot(boot), RawDir: dir, RawBoot: boot}, nil
}

// Akcje na plikach. Każda działa wyłącznie na pliku, który istnieje we flash
// (sprawdzane po stronie serwera na świeżym odczycie "dir"), więc do polecenia
// nie trafi dowolny tekst od użytkownika.
const (
	FileActionBootImage  = "boot-image"  // firmware przy następnym starcie
	FileActionBackupBoot = "backup-boot" // zapasowy firmware
	FileActionBootConfig = "boot-config" // plik konfiguracji przy starcie
	FileActionSaveAs     = "save-as"     // zapis running-config do nowego pliku
)

// BuildFileCommands zwraca polecenia dla akcji na pliku.
func BuildFileCommands(action, name string, existing []FlashFile) ([]string, error) {
	find := func(n string) (FlashFile, bool) {
		for _, f := range existing {
			if f.Name == n || f.Base == n {
				return f, true
			}
		}
		return FlashFile{}, false
	}
	switch action {
	case FileActionBootImage, FileActionBackupBoot:
		f, ok := find(name)
		if !ok {
			return nil, fmt.Errorf("pliku %q nie ma we flash", name)
		}
		if f.Kind != "firmware" {
			return nil, fmt.Errorf("%s nie jest plikiem firmware (.rel)", f.Base)
		}
		cmd := "boot system " + f.Name
		if action == FileActionBackupBoot {
			cmd = "boot system backup " + f.Name
		}
		return []string{"configure terminal", cmd, "end"}, nil
	case FileActionBootConfig:
		f, ok := find(name)
		if !ok {
			return nil, fmt.Errorf("pliku %q nie ma we flash", name)
		}
		if f.Kind != "config" {
			return nil, fmt.Errorf("%s nie jest plikiem konfiguracji (.cfg)", f.Base)
		}
		return []string{"configure terminal", "boot config-file " + f.Name, "end"}, nil
	case FileActionSaveAs:
		base := strings.TrimSpace(name)
		if !strings.HasSuffix(strings.ToLower(base), ".cfg") {
			base += ".cfg"
		}
		if !safeFileRE.MatchString(base) {
			return nil, fmt.Errorf("nazwa pliku może zawierać tylko litery, cyfry, kropkę, myślnik i podkreślenie (do 64 znaków)")
		}
		if _, exists := find(base); exists {
			return nil, fmt.Errorf("plik %s już istnieje; wybierz inną nazwę", base)
		}
		return []string{"copy running-config flash:/" + base}, nil
	}
	return nil, fmt.Errorf("nieznana akcja %q", action)
}

// FileAction wykonuje akcję na pliku po świeżym odczycie zawartości flash.
func (s *Service) FileAction(action, name string) (string, error) {
	dir, err := s.RunReadOnlyFresh("dir")
	if err != nil {
		return "", fmt.Errorf("nie udało się sprawdzić zawartości flash: %w", err)
	}
	cmds, err := BuildFileCommands(action, name, ParseDir(dir))
	if err != nil {
		return "", err
	}
	return s.runConfig(cmds)
}

// ---------- Wgrywanie firmware ----------

var dirFreeRE = regexp.MustCompile(`(?i)([\d.]+)\s*([KMG]?)B?\s+of\s+[\d.]+\s*[KMG]?B?\s+bytes\s+free`)

// ParseDirFree odczytuje wolne miejsce z końca "dir" ("1.9M of 63.0M bytes free").
// Zwraca -1, gdy formatu nie da się rozpoznać.
func ParseDirFree(raw string) int64 {
	m := dirFreeRE.FindStringSubmatch(raw)
	if m == nil {
		return -1
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return -1
	}
	mult := map[string]float64{"": 1, "K": 1 << 10, "M": 1 << 20, "G": 1 << 30}[strings.ToUpper(m[2])]
	return int64(v * mult)
}

var firmwareNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}\.rel$`)

// ValidFirmwareName sprawdza nazwę pliku firmware (tylko bezpieczne znaki, rozszerzenie .rel).
func ValidFirmwareName(name string) bool { return firmwareNameRE.MatchString(name) }

// FirmwarePrecheck sprawdza przed kopiowaniem: nazwa, brak pliku o tej nazwie,
// wolne miejsce (z 1 MB zapasu). Zwraca ostrzeżenie, gdy miejsca nie da się ustalić.
func FirmwarePrecheck(name string, size int64, dirRaw string) (warning string, err error) {
	if !ValidFirmwareName(name) {
		return "", fmt.Errorf("nazwa firmware może zawierać tylko litery, cyfry, kropkę, myślnik i podkreślenie i musi kończyć się na .rel")
	}
	for _, f := range ParseDir(dirRaw) {
		if strings.EqualFold(f.Base, name) {
			return "", fmt.Errorf("plik %s już jest we flash", name)
		}
	}
	free := ParseDirFree(dirRaw)
	if free < 0 {
		return "nie udało się odczytać wolnego miejsca we flash; kopiowanie może się nie udać, jeśli miejsca zabraknie", nil
	}
	if free < size+(1<<20) {
		return "", fmt.Errorf("za mało miejsca we flash: wolne %.1f MB, potrzeba %.1f MB; usuń stary firmware z poziomu CLI switcha", float64(free)/(1<<20), float64(size+(1<<20))/(1<<20))
	}
	return "", nil
}

// CopyFirmwareFromURL każe switchowi pobrać plik z adresu HTTP do flash,
// a potem sprawdza, czy plik ma oczekiwany rozmiar.
func (s *Service) CopyFirmwareFromURL(url, name string, size int64, timeout time.Duration) (string, error) {
	if !ValidFirmwareName(name) || !strings.HasPrefix(url, "http://") || strings.ContainsAny(url, " \t\r\n;") {
		return "", fmt.Errorf("nieprawidłowe dane kopiowania")
	}
	out, err := s.runConfigTimeout([]string{"copy " + url + " flash:/" + name}, timeout)
	if err != nil {
		return out, err
	}
	dir, derr := s.RunReadOnlyFresh("dir")
	if derr != nil {
		return out, fmt.Errorf("kopiowanie zakończone, ale nie udało się sprawdzić flash: %w", derr)
	}
	for _, f := range ParseDir(dir) {
		if strings.EqualFold(f.Base, name) {
			if f.Size != size {
				return out, fmt.Errorf("plik %s we flash ma %d B zamiast %d B; nie ustawiaj go jako firmware startowego", name, f.Size, size)
			}
			return out, nil
		}
	}
	return out, fmt.Errorf("po kopiowaniu nie ma pliku %s we flash; odpowiedź switcha: %s", name, strings.TrimSpace(out))
}
