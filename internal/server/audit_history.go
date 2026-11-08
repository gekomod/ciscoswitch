package server

import (
	"bufio"
	"encoding/json"
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
		if info.Size() > 10<<20 {
			http.Error(w, "audit history exceeds 10 MiB safety limit", http.StatusRequestEntityTooLarge)
			return
		}
		file, err := os.Open(path)
		if err != nil {
			http.Error(w, "audit history unavailable", http.StatusInternalServerError)
			return
		}
		defer file.Close()
		records := make([]auditHistoryRecord, 0, 200)
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 64*1024)
		for scanner.Scan() {
			var item auditHistoryRecord
			if json.Unmarshal(scanner.Bytes(), &item) != nil {
				continue
			}
			records = append(records, item)
			if len(records) > 200 {
				records = records[1:]
			}
		}
		if err := scanner.Err(); err != nil {
			http.Error(w, "audit history read failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(records)
	})
}
