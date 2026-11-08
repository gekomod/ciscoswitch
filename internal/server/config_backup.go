package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// configExportHandler keeps the export endpoint independently testable without a live switch.
func configExportHandler(run func(string) (string, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		source := r.URL.Query().Get("source")
		command, filename := "show running-config", "running-config.txt"
		switch source {
		case "", "running":
		case "startup":
			command, filename = "show startup-config", "startup-config.txt"
		default:
			http.Error(w, "invalid source", http.StatusBadRequest)
			return
		}
		output, err := run(command)
		if err != nil {
			http.Error(w, "switch configuration export failed", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write([]byte(output))
	})
}

type configCompareResult struct {
	Added        int `json:"added"`
	Removed      int `json:"removed"`
	RunningLines int `json:"runningLines"`
	StartupLines int `json:"startupLines"`
}

func configCompareHandler(run func(string) (string, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		running, err := run("show running-config")
		if err != nil {
			http.Error(w, "running configuration read failed", http.StatusBadGateway)
			return
		}
		startup, err := run("show startup-config")
		if err != nil {
			http.Error(w, "startup configuration read failed", http.StatusBadGateway)
			return
		}
		result := compareConfigLines(running, startup)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(result)
	})
}

// compareConfigLines compares line multiplicity, so duplicate directives are handled
// correctly. Added means present in running-config but absent from startup-config.
func compareConfigLines(running, startup string) configCompareResult {
	rLines := nonEmptyConfigLines(running)
	sLines := nonEmptyConfigLines(startup)
	counts := make(map[string]int, len(sLines))
	for _, line := range sLines {
		counts[line]++
	}
	result := configCompareResult{Added: 0, Removed: 0, RunningLines: len(rLines), StartupLines: len(sLines)}
	for _, line := range rLines {
		if counts[line] > 0 {
			counts[line]--
		} else {
			result.Added++
		}
	}
	counts = make(map[string]int, len(rLines))
	for _, line := range rLines {
		counts[line]++
	}
	for _, line := range sLines {
		if counts[line] > 0 {
			counts[line]--
		} else {
			result.Removed++
		}
	}
	return result
}

func nonEmptyConfigLines(raw string) []string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

type configBackup struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	Verified  bool      `json:"verified"`
}

func configBackupsHandler(run func(string) (string, error), dir string, retention int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			entries, err := listConfigBackups(dir)
			if err != nil {
				http.Error(w, "backup listing failed", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(entries)
		case http.MethodPost:
			output, err := run("show running-config")
			if err != nil {
				http.Error(w, "running configuration backup failed", http.StatusBadGateway)
				return
			}
			if strings.TrimSpace(output) == "" {
				http.Error(w, "switch returned an empty configuration; backup not created", http.StatusBadGateway)
				return
			}
			entry, err := writeConfigBackup(dir, output, time.Now().UTC())
			if err != nil {
				http.Error(w, "configuration backup could not be stored", http.StatusInternalServerError)
				return
			}
			if err := pruneConfigBackups(dir, retention); err != nil {
				http.Error(w, "backup saved but retention cleanup failed", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(entry)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func writeConfigBackup(dir, content string, now time.Time) (configBackup, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return configBackup{}, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return configBackup{}, err
	}
	id := now.UTC().Format("20060102T150405.000000000Z")
	path := filepath.Join(dir, id+".cfg")
	tmp, err := os.CreateTemp(dir, ".backup-*.tmp")
	if err != nil {
		return configBackup{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return configBackup{}, err
	}
	n, err := tmp.WriteString(content)
	if err != nil {
		_ = tmp.Close()
		return configBackup{}, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return configBackup{}, err
	}
	if err := tmp.Close(); err != nil {
		return configBackup{}, err
	}
	if _, err := os.Stat(path); err == nil {
		return configBackup{}, fmt.Errorf("backup id already exists")
	} else if !os.IsNotExist(err) {
		return configBackup{}, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return configBackup{}, err
	}
	sum := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(sum[:])
	if err := writeBackupDigest(dir, id, digest); err != nil {
		_ = os.Remove(path)
		return configBackup{}, err
	}
	return configBackup{ID: id, CreatedAt: now.UTC(), Size: int64(n), SHA256: digest, Verified: true}, nil
}

// writeBackupDigest stores the expected SHA-256 separately from the configuration
// file so later reads can detect accidental or unauthorized modification.
func writeBackupDigest(dir, id, digest string) error {
	path := filepath.Join(dir, id+".sha256")
	tmp, err := os.CreateTemp(dir, ".digest-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(digest + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func listConfigBackups(dir string) ([]configBackup, error) {
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []configBackup{}, nil
	}
	if err != nil {
		return nil, err
	}
	entries := make([]configBackup, 0, len(files))
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".cfg") {
			continue
		}
		id := strings.TrimSuffix(file.Name(), ".cfg")
		if !validBackupID(id) {
			continue
		}
		info, err := file.Info()
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, file.Name()))
		if err != nil {
			continue
		}
		sum := sha256.Sum256(data)
		actual := hex.EncodeToString(sum[:])
		expected, digestErr := os.ReadFile(filepath.Join(dir, id+".sha256"))
		verified := digestErr == nil && strings.TrimSpace(string(expected)) == actual
		entries = append(entries, configBackup{ID: id, CreatedAt: info.ModTime().UTC(), Size: info.Size(), SHA256: actual, Verified: verified})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].CreatedAt.After(entries[j].CreatedAt) })
	return entries, nil
}

func validBackupID(id string) bool {
	if len(id) != len("20060102T150405.000000000Z") {
		return false
	}
	_, err := time.Parse("20060102T150405.000000000Z", id)
	return err == nil
}

func configBackupDownloadHandler(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := r.URL.Query().Get("id")
		if !validBackupID(id) {
			http.Error(w, "invalid backup id", http.StatusBadRequest)
			return
		}
		path := filepath.Join(dir, id+".cfg")
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "backup read failed", http.StatusInternalServerError)
			return
		}
		expected, digestErr := os.ReadFile(filepath.Join(dir, id+".sha256"))
		if digestErr == nil {
			sum := sha256.Sum256(data)
			if strings.TrimSpace(string(expected)) != hex.EncodeToString(sum[:]) {
				http.Error(w, "backup integrity check failed; download blocked", http.StatusConflict)
				return
			}
			w.Header().Set("X-Backup-Integrity", "verified")
		} else if os.IsNotExist(digestErr) {
			w.Header().Set("X-Backup-Integrity", "unverified-legacy")
		} else {
			http.Error(w, "backup integrity metadata read failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="running-config-`+id+`.txt"`)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(data)
	})
}

type configBackupCompareResult struct {
	BackupID           string    `json:"backupId"`
	SHA256             string    `json:"sha256"`
	BackupLines        int       `json:"backupLines"`
	CurrentLines       int       `json:"currentLines"`
	AddedInCurrent     int       `json:"addedInCurrent"`
	RemovedFromCurrent int       `json:"removedFromCurrent"`
	Identical          bool      `json:"identical"`
	ComparedAt         time.Time `json:"comparedAt"`
}

// configBackupCompareHandler compares a verified local snapshot with the current
// running-config. It deliberately returns counts only: configuration lines can
// contain passwords, communities, and other management secrets.
func configBackupCompareHandler(run func(string) (string, error), dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := r.URL.Query().Get("id")
		if !validBackupID(id) {
			http.Error(w, "invalid backup id", http.StatusBadRequest)
			return
		}
		data, err := os.ReadFile(filepath.Join(dir, id+".cfg"))
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "backup read failed", http.StatusInternalServerError)
			return
		}
		expected, err := os.ReadFile(filepath.Join(dir, id+".sha256"))
		if os.IsNotExist(err) {
			http.Error(w, "backup is not verified; comparison blocked", http.StatusConflict)
			return
		}
		if err != nil {
			http.Error(w, "backup integrity metadata read failed", http.StatusInternalServerError)
			return
		}
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		if strings.TrimSpace(string(expected)) != digest {
			http.Error(w, "backup integrity check failed; comparison blocked", http.StatusConflict)
			return
		}

		current, err := run("show running-config")
		if err != nil {
			http.Error(w, "current running configuration read failed", http.StatusBadGateway)
			return
		}
		if strings.TrimSpace(current) == "" {
			http.Error(w, "switch returned an empty running configuration", http.StatusBadGateway)
			return
		}
		diff := compareConfigLines(current, string(data))
		result := configBackupCompareResult{
			BackupID:           id,
			SHA256:             digest,
			BackupLines:        len(nonEmptyConfigLines(string(data))),
			CurrentLines:       len(nonEmptyConfigLines(current)),
			AddedInCurrent:     diff.Added,
			RemovedFromCurrent: diff.Removed,
			Identical:          diff.Added == 0 && diff.Removed == 0,
			ComparedAt:         time.Now().UTC(),
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(result)
	})
}

// configBackupRestorePreviewHandler validates a snapshot for inspection only.
// It deliberately does not send configuration to the switch: AlliedWare Plus restore
// semantics are not yet verified for the supported hardware/firmware combination.
func configBackupRestorePreviewHandler(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := r.URL.Query().Get("id")
		if !validBackupID(id) {
			http.Error(w, "invalid backup id", http.StatusBadRequest)
			return
		}
		data, err := os.ReadFile(filepath.Join(dir, id+".cfg"))
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "backup read failed", http.StatusInternalServerError)
			return
		}
		expected, err := os.ReadFile(filepath.Join(dir, id+".sha256"))
		if os.IsNotExist(err) {
			http.Error(w, "backup is not verified; restore preview blocked", http.StatusConflict)
			return
		}
		if err != nil {
			http.Error(w, "backup integrity metadata read failed", http.StatusInternalServerError)
			return
		}
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		if strings.TrimSpace(string(expected)) != digest {
			http.Error(w, "backup integrity check failed; restore preview blocked", http.StatusConflict)
			return
		}
		lines := nonEmptyConfigLines(string(data))
		if len(lines) == 0 {
			http.Error(w, "backup is empty; restore preview blocked", http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"backupId": id, "sha256": digest, "size": len(data), "lineCount": len(lines),
			"integrityVerified": true, "restoreAvailable": false,
			"message": "Kopia jest integralna i może być dalej analizowana. Przywracanie pozostaje zablokowane do czasu potwierdzenia bezpiecznej procedury dla AlliedWare Plus.",
		})
	})
}

// runWithPreChangeBackup is fail-closed: a config change is not sent unless the
// current running-config was read and stored successfully first.
func runWithPreChangeBackup(dir string, run func(string) (string, error), change func() (string, error)) (string, error) {
	content, err := run("show running-config")
	if err != nil {
		return "", fmt.Errorf("zmiana zablokowana: nie udało się wykonać kopii bezpieczeństwa running-config: %w", err)
	}
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("zmiana zablokowana: switch zwrócił pustą konfigurację; kopia nie została utworzona")
	}
	if _, err := writeConfigBackup(dir, content, time.Now().UTC()); err != nil {
		return "", fmt.Errorf("zmiana zablokowana: nie udało się zapisać kopii bezpieczeństwa: %w", err)
	}
	if err := pruneConfigBackups(dir, backupRetentionFromEnv()); err != nil {
		return "", fmt.Errorf("zmiana zablokowana: kopia została zapisana, ale porządkowanie historii nie powiodło się: %w", err)
	}
	return change()
}

func backupRetentionFromEnv() int {
	n, err := strconv.Atoi(os.Getenv("CONFIG_BACKUP_RETENTION"))
	if err != nil || n < 1 || n > 10000 {
		return 100
	}
	return n
}

func pruneConfigBackups(dir string, keep int) error {
	if keep < 1 {
		return fmt.Errorf("retention must be at least 1")
	}
	entries, err := listConfigBackups(dir)
	if err != nil {
		return err
	}
	if len(entries) <= keep {
		return nil
	}
	for _, entry := range entries[keep:] {
		if err := os.Remove(filepath.Join(dir, entry.ID+".cfg")); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Remove(filepath.Join(dir, entry.ID+".sha256")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
