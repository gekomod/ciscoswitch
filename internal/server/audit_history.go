package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type auditHistoryRecord struct {
	Timestamp  time.Time `json:"timestamp"`
	Username   string    `json:"username"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMS int64     `json:"durationMs"`
}

func auditHistoryHandler(path string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if path == "" {
			path = filepath.Join("data", "audit.jsonl")
		}
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]auditHistoryRecord{})
			return
		}
		if err != nil {
			http.Error(w, "audit history unavailable", http.StatusInternalServerError)
			return
		}
		file, err := os.Open(path)
		if err != nil {
			http.Error(w, "audit history unavailable", http.StatusInternalServerError)
			return
		}
		defer file.Close()
		// Potrzebne jest tylko ostatnie 200 wpisów: czytamy najwyżej ostatni 1 MiB
		// (wcześniej plik >10 MiB blokował dziennik na stałe).
		partial := false
		if info.Size() > 1<<20 {
			if _, err := file.Seek(info.Size()-(1<<20), io.SeekStart); err != nil {
				http.Error(w, "audit history unavailable", http.StatusInternalServerError)
				return
			}
			partial = true
		}
		tail, err := io.ReadAll(io.LimitReader(file, 1<<20))
		if err != nil {
			http.Error(w, "audit history read failed", http.StatusInternalServerError)
			return
		}
		lines := bytes.Split(tail, []byte("\n"))
		if partial && len(lines) > 0 {
			lines = lines[1:] // pierwsza linia jest ucięta
		}
		records := make([]auditHistoryRecord, 0, 200)
		for _, line := range lines {
			var item auditHistoryRecord
			if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &item) != nil {
				continue
			}
			records = append(records, item)
			if len(records) > 200 {
				records = records[1:]
			}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(records)
	})
}
