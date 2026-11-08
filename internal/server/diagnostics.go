package server

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// diagnosticsSample stores only normalized topology metadata; never CLI output or credentials.
type diagnosticsSample struct {
	CapturedAt   time.Time             `json:"capturedAt"`
	Checks       map[string]bool       `json:"checks"`
	Aggregations []topologyAggregation `json:"aggregations"`
	Uplinks      []string              `json:"uplinks"`
	Neighbors    []topologyLink        `json:"neighbors"`
}
type diagnosticsStore struct {
	mu        sync.Mutex
	path      string
	retention int
}

var diagnosticsRegistryMu sync.Mutex
var diagnosticsRegistry = map[string]*diagnosticsStore{}

func sharedDiagnosticsStore(path string, retention int) *diagnosticsStore {
	diagnosticsRegistryMu.Lock()
	defer diagnosticsRegistryMu.Unlock()
	if store := diagnosticsRegistry[path]; store != nil {
		return store
	}
	store := newDiagnosticsStore(path, retention)
	diagnosticsRegistry[path] = store
	return store
}
func StartDiagnosticsCollector(ctx context.Context, read func(string) (string, error), path string, interval time.Duration) {
	if interval <= 0 {
		return
	}
	store := sharedDiagnosticsStore(path, 12000)
	go func() {
		collect := func() {
			sample := collectDiagnosticsSample(read)
			if err := store.append(sample); err != nil {
				log.Printf("diagnostics history write failed: %v", err)
			}
		}
		collect()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				collect()
			}
		}
	}()
}
func newDiagnosticsStore(path string, retention int) *diagnosticsStore {
	if retention < 2 || retention > 50000 {
		retention = 12000
	}
	return &diagnosticsStore{path: path, retention: retention}
}
func (s *diagnosticsStore) readLocked() ([]diagnosticsSample, error) {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return []diagnosticsSample{}, nil
	}
	if err != nil {
		return nil, err
	}
	var items []diagnosticsSample
	if len(raw) == 0 {
		return []diagnosticsSample{}, nil
	}
	if err = json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("history file is invalid: %w", err)
	}
	return items, nil
}
func (s *diagnosticsStore) list() ([]diagnosticsSample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked()
}
func (s *diagnosticsStore) append(sample diagnosticsSample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.readLocked()
	if err != nil {
		return err
	}
	items = append(items, sample)
	if len(items) > s.retention {
		items = append([]diagnosticsSample(nil), items[len(items)-s.retention:]...)
	}
	raw, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".diagnostics-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(0600); err != nil {
		return err
	}
	if _, err = tmp.Write(raw); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpName, s.path); err != nil {
		return err
	}
	if err = os.Chmod(s.path, 0600); err != nil {
		return err
	}
	ok = true
	return nil
}
func collectDiagnosticsSample(read func(string) (string, error)) diagnosticsSample {
	sample := diagnosticsSample{CapturedAt: time.Now().UTC(), Checks: map[string]bool{}, Aggregations: []topologyAggregation{}, Uplinks: []string{}, Neighbors: []topologyLink{}}
	config, configErr := read("show running-config")
	sample.Checks["runningConfig"] = configErr == nil
	sw, swErr := read("show interface switchport")
	sample.Checks["switchport"] = swErr == nil && strings.Contains(sw, "Interface name")
	lldp, lldpErr := read("show lldp neighbors")
	sample.Checks["lldp"] = lldpErr == nil && len(parseTopologyLLDP(lldp)) > 0
	lacp, lacpErr := read("show etherchannel detail")
	liveParsed := []topologyAggregation{}
	if lacpErr == nil {
		liveParsed = parseTopologyLACP(lacp)
	}
	sample.Checks["lacp"] = lacpErr == nil && len(liveParsed) > 0
	ports, groups, _ := parseTopologyConfig(config, sw)
	for _, p := range ports {
		if p.Role == "Uplink / switch" {
			sample.Uplinks = append(sample.Uplinks, p.Port)
		}
	}
	sort.Strings(sample.Uplinks)
	if sample.Checks["lldp"] {
		sample.Neighbors = parseTopologyLLDP(lldp)
	}
	if sample.Checks["runningConfig"] {
		liveGroups := liveParsed
		for i := range groups {
			for _, live := range liveGroups {
				if !strings.EqualFold(groups[i].Name, live.Name) {
					continue
				}
				groups[i].Live = true
				groups[i].Ready = live.Ready
				groups[i].Partner = live.Partner
				groups[i].LiveMembers = append([]string(nil), live.Members...)
				configured := map[string]bool{}
				for _, p := range groups[i].Members {
					configured[strings.ToLower(p)] = true
				}
				active := map[string]bool{}
				for _, p := range live.Members {
					active[strings.ToLower(p)] = true
				}
				if len(active) > 0 {
					if len(active) != len(configured) {
						groups[i].MemberMismatch = true
					} else {
						for p := range configured {
							if !active[p] {
								groups[i].MemberMismatch = true
								break
							}
						}
					}
				}
			}
		}
		sample.Aggregations = groups
	}
	if sample.Checks["lacp"] {
		known := map[string]bool{}
		for _, g := range sample.Aggregations {
			known[strings.ToLower(g.Name)] = true
		}
		for _, live := range liveParsed {
			if !known[strings.ToLower(live.Name)] {
				live.Live = true
				sample.Aggregations = append(sample.Aggregations, live)
			}
		}
	}
	return sample
}

type diagnosticsAlert struct {
	Severity string    `json:"severity"`
	Code     string    `json:"code"`
	Title    string    `json:"title"`
	Evidence string    `json:"evidence"`
	At       time.Time `json:"at"`
}
type diagnosticsTransition struct {
	Type     string    `json:"type"`
	Subject  string    `json:"subject"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	At       time.Time `json:"at"`
	Severity string    `json:"severity"`
}

func diagnosticsCurrentAlerts(s diagnosticsSample) []diagnosticsAlert {
	out := []diagnosticsAlert{}
	if s.Checks["lacp"] {
		for _, g := range s.Aggregations {
			if g.Ready == "Niegotowy" {
				out = append(out, diagnosticsAlert{"critical", "lacp-not-ready", "Agregacja LACP nie jest gotowa", g.Name + " zgłasza Ready=0.", s.CapturedAt})
			}
			if g.MemberMismatch {
				out = append(out, diagnosticsAlert{"warning", "lacp-member-mismatch", "Niespójność członków LACP", g.Name + ": skonfigurowane [" + strings.Join(g.Members, ", ") + "] · aktywne [" + strings.Join(g.LiveMembers, ", ") + "].", s.CapturedAt})
			}
			if !g.Live && len(g.Members) > 0 {
				out = append(out, diagnosticsAlert{"warning", "lacp-no-operational-data", "Brak operacyjnego odczytu agregacji", g.Name + " jest skonfigurowana, ale nie ma rozpoznanego stanu operacyjnego.", s.CapturedAt})
			}
		}
	}
	if s.Checks["runningConfig"] && len(s.Uplinks) == 1 {
		out = append(out, diagnosticsAlert{"info", "single-explicit-uplink", "Jeden jawnie opisany uplink", s.Uplinks[0] + " jest jedynym portem rozpoznanym po opisie jako uplink/switch. To ryzyko topologii, nie alarm awarii.", s.CapturedAt})
	}
	return out
}
func diagnosticsTransitions(samples []diagnosticsSample) []diagnosticsTransition {
	out := []diagnosticsTransition{}
	for i := 1; i < len(samples); i++ {
		prev, cur := samples[i-1], samples[i]
		if prev.Checks["lacp"] && cur.Checks["lacp"] {
			old := map[string]topologyAggregation{}
			for _, g := range prev.Aggregations {
				old[strings.ToLower(g.Name)] = g
			}
			for _, g := range cur.Aggregations {
				p, ok := old[strings.ToLower(g.Name)]
				if !ok {
					continue
				}
				if p.Ready != g.Ready && p.Ready != "" && g.Ready != "" {
					sev := "warning"
					if g.Ready == "Niegotowy" {
						sev = "critical"
					}
					out = append(out, diagnosticsTransition{"lacp-ready", g.Name, p.Ready, g.Ready, cur.CapturedAt, sev})
				}
				if p.Live && g.Live {
					before := map[string]bool{}
					after := map[string]bool{}
					for _, v := range p.LiveMembers {
						before[strings.ToLower(v)] = true
					}
					for _, v := range g.LiveMembers {
						after[strings.ToLower(v)] = true
					}
					for v := range before {
						if !after[v] {
							out = append(out, diagnosticsTransition{"lacp-member-lost", g.Name + " / " + v, "aktywny", "brak w odczycie", cur.CapturedAt, "critical"})
						}
					}
					for v := range after {
						if !before[v] {
							out = append(out, diagnosticsTransition{"lacp-member-restored", g.Name + " / " + v, "brak w odczycie", "aktywny", cur.CapturedAt, "info"})
						}
					}
				}
			}
		}
		if prev.Checks["lldp"] && cur.Checks["lldp"] {
			old := map[string]string{}
			now := map[string]string{}
			for _, n := range prev.Neighbors {
				old[strings.ToLower(n.LocalPort)] = n.Neighbor
			}
			for _, n := range cur.Neighbors {
				now[strings.ToLower(n.LocalPort)] = n.Neighbor
			}
			for port, neighbor := range old {
				next, ok := now[port]
				if !ok {
					out = append(out, diagnosticsTransition{"lldp-neighbor-removed", port, neighbor, "brak sąsiada w odczycie", cur.CapturedAt, "warning"})
				} else if next != neighbor {
					out = append(out, diagnosticsTransition{"lldp-neighbor-change", port, neighbor, next, cur.CapturedAt, "info"})
				}
			}
			for port, neighbor := range now {
				if _, ok := old[port]; !ok {
					out = append(out, diagnosticsTransition{"lldp-neighbor-added", port, "brak sąsiada w odczycie", neighbor, cur.CapturedAt, "info"})
				}
			}
		}
	}
	return out
}

func diagnosticsCollectHandler(read func(string) (string, error), store *diagnosticsStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		sample := collectDiagnosticsSample(read)
		if err := store.append(sample); err != nil {
			http.Error(w, "nie udało się zapisać historii diagnostycznej", http.StatusInternalServerError)
			return
		}
		if !sample.Checks["runningConfig"] && !sample.Checks["lacp"] && !sample.Checks["lldp"] {
			http.Error(w, "próbka zapisana, ale wszystkie kontrole zakończyły się niepowodzeniem", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"sample": sample, "alerts": diagnosticsCurrentAlerts(sample), "saved": true, "retention": store.retention})
	})
}
func diagnosticsSamplingSettings() (string, string) {
	raw := strings.TrimSpace(os.Getenv("DIAGNOSTICS_INTERVAL"))
	if raw == "0" {
		return "manual", "wyłączone"
	}
	if raw == "" {
		return "scheduled", "60s"
	}
	interval, err := time.ParseDuration(raw)
	if err != nil || interval < 10*time.Second {
		return "scheduled", "60s"
	}
	return "scheduled", interval.String()
}

type diagnosticsAcknowledgement struct {
	ID             string    `json:"id"`
	Code           string    `json:"code"`
	Evidence       string    `json:"evidence"`
	At             time.Time `json:"at"`
	AcknowledgedAt time.Time `json:"acknowledgedAt"`
	Username       string    `json:"username"`
	Comment        string    `json:"comment"`
}
type diagnosticsAckStore struct {
	mu   sync.Mutex
	path string
}

var diagnosticsAckRegistryMu sync.Mutex
var diagnosticsAckRegistry = map[string]*diagnosticsAckStore{}

func sharedDiagnosticsAckStore(path string) *diagnosticsAckStore {
	diagnosticsAckRegistryMu.Lock()
	defer diagnosticsAckRegistryMu.Unlock()
	if store := diagnosticsAckRegistry[path]; store != nil {
		return store
	}
	store := &diagnosticsAckStore{path: path}
	diagnosticsAckRegistry[path] = store
	return store
}
func (a *diagnosticsAckStore) list() ([]diagnosticsAcknowledgement, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	raw, err := os.ReadFile(a.path)
	if os.IsNotExist(err) {
		return []diagnosticsAcknowledgement{}, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []diagnosticsAcknowledgement
	if len(raw) == 0 {
		return rows, nil
	}
	if err = json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
func (a *diagnosticsAckStore) save(row diagnosticsAcknowledgement) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	rows := []diagnosticsAcknowledgement{}
	raw, err := os.ReadFile(a.path)
	if err == nil && len(raw) > 0 {
		if err = json.Unmarshal(raw, &rows); err != nil {
			return err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := range rows {
		if rows[i].ID == row.ID {
			rows[i] = row
			return a.write(rows)
		}
	}
	rows = append(rows, row)
	if len(rows) > 1000 {
		rows = rows[len(rows)-1000:]
	}
	return a.write(rows)
}
func (a *diagnosticsAckStore) write(rows []diagnosticsAcknowledgement) error {
	raw, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(a.path)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".diagnostics-acks-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err = tmp.Chmod(0600); err != nil {
		return err
	}
	if _, err = tmp.Write(raw); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, a.path); err != nil {
		return err
	}
	ok = true
	return os.Chmod(a.path, 0600)
}
func diagnosticsAlertID(code, evidence string, at time.Time) string {
	sum := sha256.Sum256([]byte(code + "\x00" + evidence + "\x00" + at.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])
}
func diagnosticsAcknowledgeHandler(store *diagnosticsStore, path string) http.Handler {
	acks := sharedDiagnosticsAckStore(path)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		var q struct {
			ID      string `json:"id"`
			Comment string `json:"comment"`
		}
		if json.NewDecoder(r.Body).Decode(&q) != nil || len(q.ID) != 64 || len(q.Comment) > 500 {
			http.Error(w, "nieprawidłowe dane potwierdzenia alarmu", 400)
			return
		}
		samples, err := store.list()
		if err != nil {
			http.Error(w, "nie można odczytać historii diagnostycznej", 500)
			return
		}
		if len(samples) == 0 {
			http.Error(w, "brak aktywnych alarmów", 409)
			return
		}
		found := false
		var row diagnosticsAcknowledgement
		for _, alert := range diagnosticsCurrentAlerts(samples[len(samples)-1]) {
			if diagnosticsAlertID(alert.Code, alert.Evidence, alert.At) == q.ID {
				found = true
				row = diagnosticsAcknowledgement{ID: q.ID, Code: alert.Code, Evidence: alert.Evidence, At: alert.At}
				break
			}
		}
		if !found {
			http.Error(w, "alarm nie jest już aktywny lub jego stan się zmienił; odśwież listę", 409)
			return
		}
		username := "unknown"
		if identity, ok := r.Context().Value(identityContextKey{}).(panelIdentity); ok && strings.TrimSpace(identity.Username) != "" {
			username = identity.Username
		} else if authenticated, _, ok := r.BasicAuth(); ok && strings.TrimSpace(authenticated) != "" {
			username = authenticated
		}
		row.AcknowledgedAt = time.Now().UTC()
		row.Username = username
		row.Comment = strings.TrimSpace(q.Comment)
		if err = acks.save(row); err != nil {
			http.Error(w, "nie udało się zapisać potwierdzenia alarmu", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"saved": true, "acknowledgement": row})
	})
}

type diagnosticsEvent struct {
	ID              string                      `json:"id"`
	Type            string                      `json:"type"`
	Severity        string                      `json:"severity"`
	Title           string                      `json:"title"`
	Subject         string                      `json:"subject"`
	Description     string                      `json:"description"`
	At              time.Time                   `json:"at"`
	Acknowledged    bool                        `json:"acknowledged"`
	Acknowledgement *diagnosticsAcknowledgement `json:"acknowledgement,omitempty"`
}

func diagnosticsEventID(kind, subject string, at time.Time) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + subject + "\x00" + at.UTC().Format(time.RFC3339Nano)))
	return hex.EncodeToString(sum[:])
}

type diagnosticsCorrelation struct {
	ID         string    `json:"id"`
	Severity   string    `json:"severity"`
	Title      string    `json:"title"`
	Summary    string    `json:"summary"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	EventCount int       `json:"eventCount"`
	Categories []string  `json:"categories"`
	EventIDs   []string  `json:"eventIds"`
}

func buildDiagnosticsCorrelations(events []diagnosticsEvent, window time.Duration) []diagnosticsCorrelation {
	if len(events) < 2 {
		return []diagnosticsCorrelation{}
	}
	ordered := append([]diagnosticsEvent(nil), events...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].At.Equal(ordered[j].At) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].At.Before(ordered[j].At)
	})
	out := make([]diagnosticsCorrelation, 0)
	for i := 0; i < len(ordered); {
		start := ordered[i].At
		j := i + 1
		for j < len(ordered) && ordered[j].At.Sub(start) <= window {
			j++
		}
		group := ordered[i:j]
		categories := map[string]bool{}
		ids := make([]string, 0, len(group))
		severity := "info"
		hasSignal := false
		for _, event := range group {
			categories[event.Type] = true
			ids = append(ids, event.ID)
			if event.Severity == "critical" {
				severity = "critical"
				hasSignal = true
			} else if event.Severity == "warning" {
				if severity != "critical" {
					severity = "warning"
				}
				hasSignal = true
			}
		}
		if len(categories) >= 2 && hasSignal {
			categoryList := make([]string, 0, len(categories))
			for category := range categories {
				categoryList = append(categoryList, category)
			}
			sort.Strings(categoryList)
			end := group[len(group)-1].At
			id := diagnosticsEventID("correlation", strings.Join(ids, ","), start)
			title := "Zdarzenia z różnych warstw wystąpiły w zbliżonym czasie"
			if categories["lacp"] && categories["lldp"] {
				title = "Zmiany LACP i sąsiedztwa LLDP"
			}
			if categories["availability"] && (categories["lacp"] || categories["lldp"]) {
				title = "Problem odczytu CLI zbiegł się ze zmianą topologii"
			}
			summary := "Wykryto " + strconv.Itoa(len(group)) + " zdarzenia z kategorii: " + strings.Join(categoryList, ", ") + ". To korelacja czasowa, nie dowód wspólnej przyczyny. Sprawdź kolejność zdarzeń, porty i urządzenie po drugiej stronie łącza."
			out = append(out, diagnosticsCorrelation{ID: id, Severity: severity, Title: title, Summary: summary, Start: start, End: end, EventCount: len(group), Categories: categoryList, EventIDs: ids})
		}
		i = j
	}
	return out
}

func diagnosticsEventsHandler(store *diagnosticsStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		window := strings.TrimSpace(r.URL.Query().Get("window"))
		if window == "" {
			window = "24h"
		}
		durations := map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}
		span, ok := durations[window]
		if !ok {
			http.Error(w, "window musi mieć wartość 24h, 7d lub 30d", 400)
			return
		}
		kind := strings.TrimSpace(r.URL.Query().Get("type"))
		if kind == "" {
			kind = "all"
		}
		allowed := map[string]bool{"all": true, "alarm": true, "lacp": true, "lldp": true, "availability": true}
		if !allowed[kind] {
			http.Error(w, "nieobsługiwana kategoria zdarzeń", 400)
			return
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 500 {
				http.Error(w, "limit musi mieścić się w zakresie 1–500", 400)
				return
			}
			limit = n
		}
		samples, err := store.list()
		if err != nil {
			http.Error(w, "nie udało się odczytać historii diagnostycznej", 500)
			return
		}
		cutoff := time.Now().UTC().Add(-span)
		ackRows, err := sharedDiagnosticsAckStore(store.path + ".acks.json").list()
		if err != nil {
			http.Error(w, "nie udało się odczytać potwierdzeń alarmów", 500)
			return
		}
		ackByID := map[string]diagnosticsAcknowledgement{}
		for i := range ackRows {
			ackByID[ackRows[i].ID] = ackRows[i]
		}
		events := make([]diagnosticsEvent, 0)
		add := func(eventType, severity, title, subject, description string, at time.Time) {
			if at.IsZero() || at.Before(cutoff) || (kind != "all" && kind != eventType) {
				return
			}
			id := diagnosticsEventID(eventType, subject, at)
			events = append(events, diagnosticsEvent{ID: id, Type: eventType, Severity: severity, Title: title, Subject: subject, Description: description, At: at})
		}
		for _, tr := range diagnosticsTransitions(samples) {
			switch tr.Type {
			case "lacp-ready":
				add("lacp", tr.Severity, "Zmiana gotowości LACP", tr.Subject, tr.From+" → "+tr.To, tr.At)
			case "lacp-member-lost":
				add("lacp", "critical", "Utrata aktywnego członka LACP", tr.Subject, tr.From+" → "+tr.To, tr.At)
			case "lacp-member-restored":
				add("lacp", "info", "Powrót członka LACP", tr.Subject, tr.From+" → "+tr.To, tr.At)
			case "lldp-neighbor-added":
				add("lldp", "info", "Wykryto sąsiada LLDP", tr.Subject, tr.To, tr.At)
			case "lldp-neighbor-removed":
				add("lldp", "warning", "Zniknął sąsiad LLDP", tr.Subject, tr.From, tr.At)
			case "lldp-neighbor-change":
				add("lldp", "info", "Zmiana sąsiada LLDP", tr.Subject, tr.From+" → "+tr.To, tr.At)
			case "control-read-failed":
				add("availability", "warning", "Nieudany odczyt zarządzający", tr.Subject, "Kontrola CLI nie powiodła się. To brak potwierdzenia odczytu, a nie dowód, że switch lub ruch użytkowników jest offline.", tr.At)
			case "control-read-restored":
				add("availability", "info", "Odczyt zarządzający przywrócony", tr.Subject, "Kontrola CLI ponownie zakończyła się poprawnie.", tr.At)
			}
		}
		if len(samples) > 0 && (kind == "all" || kind == "alarm") {
			for _, alert := range diagnosticsCurrentAlerts(samples[len(samples)-1]) {
				id := diagnosticsAlertID(alert.Code, alert.Evidence, alert.At)
				if alert.At.Before(cutoff) {
					continue
				}
				event := diagnosticsEvent{ID: id, Type: "alarm", Severity: alert.Severity, Title: alert.Title, Subject: alert.Code, Description: alert.Evidence, At: alert.At}
				if ack, exists := ackByID[id]; exists {
					event.Acknowledged = true
					event.Acknowledgement = &ack
				}
				events = append(events, event)
			}
		}
		sort.Slice(events, func(i, j int) bool {
			if events[i].At.Equal(events[j].At) {
				return events[i].ID < events[j].ID
			}
			return events[i].At.After(events[j].At)
		})
		total := len(events)
		if len(events) > limit {
			events = events[:limit]
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		correlations := []diagnosticsCorrelation{}
		if kind == "all" {
			correlations = buildDiagnosticsCorrelations(events, 5*time.Minute)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"events": events, "total": total, "window": window, "type": kind, "limit": limit, "correlations": correlations, "correlationWindow": "5m", "note": "Brak odczytu oznacza nieudaną kontrolę zarządzającą, a nie potwierdzoną awarię switcha lub ruchu. Korelacja czasowa nie dowodzi wspólnej przyczyny."})
	})
}

func diagnosticsHistoryHandler(store *diagnosticsStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 500 {
				http.Error(w, "limit musi mieścić się w zakresie 1–500", 400)
				return
			}
			limit = n
		}
		windowMinutes := 60
		if raw := r.URL.Query().Get("windowMinutes"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 5 || n > 1440 {
				http.Error(w, "windowMinutes musi mieścić się w zakresie 5–1440", 400)
				return
			}
			windowMinutes = n
		}
		all, err := store.list()
		if err != nil {
			http.Error(w, "nie udało się odczytać historii diagnostycznej", http.StatusInternalServerError)
			return
		}
		transitions := diagnosticsTransitions(all)
		cutoff := time.Now().UTC().Add(-time.Duration(windowMinutes) * time.Minute)
		windowTransitions := make([]diagnosticsTransition, 0)
		counts := map[string]int{}
		for _, tr := range transitions {
			if !tr.At.Before(cutoff) {
				windowTransitions = append(windowTransitions, tr)
				if tr.Type == "lacp-ready" || tr.Type == "lacp-member-lost" || tr.Type == "lacp-member-restored" {
					counts[tr.Type+"|"+tr.Subject]++
				}
			}
		}
		alerts := []diagnosticsAlert{}
		if len(all) > 0 {
			alerts = diagnosticsCurrentAlerts(all[len(all)-1])
		}
		for key, count := range counts {
			if count >= 3 {
				parts := strings.SplitN(key, "|", 2)
				alerts = append(alerts, diagnosticsAlert{"warning", "lacp-flapping", "Powtarzające się zmiany stanu LACP", parts[1] + ": " + strconv.Itoa(count) + " zmian w ostatnich " + strconv.Itoa(windowMinutes) + " minutach. Sprawdź okablowanie, porty i partnera LACP.", all[len(all)-1].CapturedAt})
			}
		}
		ackStore := sharedDiagnosticsAckStore(store.path + ".acks.json")
		ackRows, _ := ackStore.list()
		ackByID := map[string]diagnosticsAcknowledgement{}
		for _, ack := range ackRows {
			ackByID[ack.ID] = ack
		}
		alertRows := make([]map[string]any, 0, len(alerts))
		for _, alert := range alerts {
			id := diagnosticsAlertID(alert.Code, alert.Evidence, alert.At)
			row := map[string]any{"severity": alert.Severity, "code": alert.Code, "title": alert.Title, "evidence": alert.Evidence, "at": alert.At, "id": id, "acknowledged": false}
			if ack, ok := ackByID[id]; ok {
				row["acknowledged"] = true
				row["acknowledgement"] = ack
			}
			alertRows = append(alertRows, row)
		}
		items := all
		if len(items) > limit {
			items = items[len(items)-limit:]
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"samples": items, "alerts": alertRows, "transitions": windowTransitions, "sampleCount": len(all), "hasEnoughHistory": len(all) >= 2, "windowMinutes": windowMinutes, "flappingThreshold": 3, "collectionMode": func() string { mode, _ := diagnosticsSamplingSettings(); return mode }(), "collectionInterval": func() string { _, cadence := diagnosticsSamplingSettings(); return cadence }(), "note": func() string {
			if len(all) < 2 {
				return "Zebrano mniej niż dwie próbki. Nie można jeszcze ocenić zmian ani flappingu."
			}
			return "Zmiany są porównywane między kolejnymi próbkami z dostępnym odczytem. Brak danych nie jest traktowany jako powrót do normy. Flapping wymaga co najmniej trzech zmian w wybranym oknie."
		}()})
	})
}

type availabilityOutage struct {
	Start           time.Time `json:"start"`
	End             time.Time `json:"end,omitempty"`
	DurationSeconds int64     `json:"durationSeconds"`
	Open            bool      `json:"open"`
}
type availabilityMetric struct {
	Name                string               `json:"name"`
	AvailabilityPercent *float64             `json:"availabilityPercent"`
	ObservedSeconds     int64                `json:"observedSeconds"`
	UnknownSeconds      int64                `json:"unknownSeconds"`
	OutageCount         int                  `json:"outageCount"`
	OutageSeconds       int64                `json:"outageSeconds"`
	Outages             []availabilityOutage `json:"outages"`
}

func diagnosticsAvailabilityMetric(name string, samples []diagnosticsSample, start, end time.Time, maxGap time.Duration, state func(diagnosticsSample) (bool, bool)) availabilityMetric {
	m := availabilityMetric{Name: name, Outages: []availabilityOutage{}}
	observed := int64(0)
	up := int64(0)
	outageOpen := -1
	for i := 1; i < len(samples); i++ {
		prev, cur := samples[i-1], samples[i]
		if cur.CapturedAt.Before(start) || !prev.CapturedAt.Before(end) || !cur.CapturedAt.After(prev.CapturedAt) {
			continue
		}
		span := cur.CapturedAt.Sub(prev.CapturedAt)
		if span >= maxGap {
			if outageOpen >= 0 {
				row := &m.Outages[outageOpen]
				row.End = prev.CapturedAt
				row.DurationSeconds = int64(row.End.Sub(row.Start).Seconds())
				row.Open = false
				outageOpen = -1
			}
			continue
		}
		known, online := state(prev)
		if !known {
			if outageOpen >= 0 {
				row := &m.Outages[outageOpen]
				row.End = prev.CapturedAt
				row.DurationSeconds = int64(row.End.Sub(row.Start).Seconds())
				row.Open = false
				outageOpen = -1
			}
			continue
		}
		left := prev.CapturedAt
		if left.Before(start) {
			left = start
		}
		right := cur.CapturedAt
		if right.After(end) {
			right = end
		}
		seconds := int64(right.Sub(left).Seconds())
		if seconds <= 0 {
			continue
		}
		observed += seconds
		if online {
			up += seconds
		}
		if i > 0 {
			prevKnown, prevOnline := state(prev)
			curKnown, curOnline := state(cur)
			if prevKnown && curKnown {
				if prevOnline && !curOnline {
					outageOpen = len(m.Outages)
					m.Outages = append(m.Outages, availabilityOutage{Start: cur.CapturedAt, Open: true})
				}
				if !prevOnline && !curOnline && outageOpen < 0 {
					outageOpen = len(m.Outages)
					m.Outages = append(m.Outages, availabilityOutage{Start: prev.CapturedAt, Open: true})
				}
				if !prevOnline && curOnline {
					if outageOpen < 0 {
						outageOpen = len(m.Outages)
						m.Outages = append(m.Outages, availabilityOutage{Start: prev.CapturedAt, Open: true})
					}
					row := &m.Outages[outageOpen]
					row.End = cur.CapturedAt
					row.DurationSeconds = int64(cur.CapturedAt.Sub(row.Start).Seconds())
					row.Open = false
					outageOpen = -1
				}
			}
		}
	}
	total := int64(end.Sub(start).Seconds())
	if total < 0 {
		total = 0
	}
	m.ObservedSeconds = observed
	m.UnknownSeconds = total - observed
	if m.UnknownSeconds < 0 {
		m.UnknownSeconds = 0
	}
	if observed > 0 {
		v := float64(up) * 100 / float64(observed)
		m.AvailabilityPercent = &v
	}
	for i := range m.Outages {
		if m.Outages[i].Open {
			m.Outages[i].DurationSeconds = int64(end.Sub(m.Outages[i].Start).Seconds())
		}
	}
	m.OutageCount = len(m.Outages)
	for _, o := range m.Outages {
		m.OutageSeconds += o.DurationSeconds
	}
	return m
}

type availabilityTimelinePoint struct {
	At                  time.Time `json:"at"`
	AvailabilityPercent *float64  `json:"availabilityPercent"`
	ObservedSeconds     int64     `json:"observedSeconds"`
	UnknownSeconds      int64     `json:"unknownSeconds"`
}
type diagnosticsAvailabilityReport struct {
	Window              string                      `json:"window"`
	From                time.Time                   `json:"from"`
	To                  time.Time                   `json:"to"`
	MaxSampleGapSeconds int64                       `json:"maxSampleGapSeconds"`
	Device              availabilityMetric          `json:"device"`
	Aggregations        []availabilityMetric        `json:"aggregations"`
	Timeline            []availabilityTimelinePoint `json:"timeline"`
	Method              string                      `json:"method"`
}

func buildDiagnosticsAvailabilityReport(store *diagnosticsStore, window string) (diagnosticsAvailabilityReport, error) {
	var report diagnosticsAvailabilityReport
	var duration time.Duration
	switch window {
	case "24h":
		duration = 24 * time.Hour
	case "7d":
		duration = 7 * 24 * time.Hour
	default:
		return report, fmt.Errorf("window musi mieć wartość 24h lub 7d")
	}
	all, err := store.list()
	if err != nil {
		return report, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CapturedAt.Before(all[j].CapturedAt) })
	end := time.Now().UTC()
	start := end.Add(-duration)
	_, cadence := diagnosticsSamplingSettings()
	interval, err := time.ParseDuration(cadence)
	if err != nil || interval <= 0 {
		interval = time.Minute
	}
	maxGap := 2 * interval
	if maxGap < 2*time.Minute {
		maxGap = 2 * time.Minute
	}
	deviceState := func(s diagnosticsSample) (bool, bool) { return s.Checks != nil, s.Checks["runningConfig"] }
	report = diagnosticsAvailabilityReport{Window: window, From: start, To: end, MaxSampleGapSeconds: int64(maxGap.Seconds()), Device: diagnosticsAvailabilityMetric("Dostępność CLI", all, start, end, maxGap, deviceState), Aggregations: []availabilityMetric{}, Timeline: []availabilityTimelinePoint{}, Method: "Uptime jest liczony jako udany odczyt CLI (show running-config), a agregacje jako rozpoznany stan LACP. Procent odnosi się wyłącznie do przedziałów objętych poprawnymi, dostatecznie częstymi próbkami. Luki pomiarowe są wyłączone z mianownika i pokazane jako brak danych; to nie jest gwarantowane SLA ani pomiar dostępności ruchu użytkowników."}
	names := map[string]bool{}
	for _, sample := range all {
		if sample.Checks["lacp"] {
			for _, g := range sample.Aggregations {
				names[g.Name] = true
			}
		}
	}
	sortedNames := make([]string, 0, len(names))
	for name := range names {
		sortedNames = append(sortedNames, name)
	}
	sort.Strings(sortedNames)
	for _, name := range sortedNames {
		state := func(s diagnosticsSample) (bool, bool) {
			if !s.Checks["lacp"] {
				return false, false
			}
			for _, g := range s.Aggregations {
				if strings.EqualFold(g.Name, name) {
					return true, g.Live && g.Ready != "Niegotowy"
				}
			}
			return true, false
		}
		report.Aggregations = append(report.Aggregations, diagnosticsAvailabilityMetric(name, all, start, end, maxGap, state))
	}
	buckets := 24
	step := time.Hour
	if window == "7d" {
		buckets = 7
		step = 24 * time.Hour
	}
	for i := 0; i < buckets; i++ {
		bucketStart := start.Add(time.Duration(i) * step)
		bucketEnd := bucketStart.Add(step)
		if bucketEnd.After(end) {
			bucketEnd = end
		}
		if !bucketEnd.After(bucketStart) {
			continue
		}
		metric := diagnosticsAvailabilityMetric("CLI", all, bucketStart, bucketEnd, maxGap, deviceState)
		report.Timeline = append(report.Timeline, availabilityTimelinePoint{At: bucketStart, AvailabilityPercent: metric.AvailabilityPercent, ObservedSeconds: metric.ObservedSeconds, UnknownSeconds: metric.UnknownSeconds})
	}
	return report, nil
}
func diagnosticsAvailabilityHandler(store *diagnosticsStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		window := r.URL.Query().Get("window")
		if window == "" {
			window = "24h"
		}
		report, err := buildDiagnosticsAvailabilityReport(store, window)
		if err != nil {
			if strings.Contains(err.Error(), "window") {
				http.Error(w, err.Error(), 400)
			} else {
				http.Error(w, "nie udało się odczytać historii diagnostycznej", 500)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(report)
	})
}
func diagnosticsAvailabilityCSVHandler(store *diagnosticsStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", 405)
			return
		}
		window := r.URL.Query().Get("window")
		if window == "" {
			window = "24h"
		}
		report, err := buildDiagnosticsAvailabilityReport(store, window)
		if err != nil {
			if strings.Contains(err.Error(), "window") {
				http.Error(w, err.Error(), 400)
			} else {
				http.Error(w, "nie udało się odczytać historii diagnostycznej", 500)
			}
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="switch-availability-`+window+`.csv"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
		writer := csv.NewWriter(w)
		writer.Comma = ';'
		_ = writer.Write([]string{"Typ", "Nazwa", "Dostępność (%)", "Czas pomiaru (s)", "Brak danych (s)", "Liczba przerw", "Czas przerw (s)", "Początek przerwy", "Koniec przerwy", "Przerwa trwa"})
		writeMetric := func(kind string, m availabilityMetric) {
			percent := ""
			if m.AvailabilityPercent != nil {
				percent = strconv.FormatFloat(*m.AvailabilityPercent, 'f', 4, 64)
			}
			_ = writer.Write([]string{kind, m.Name, percent, strconv.FormatInt(m.ObservedSeconds, 10), strconv.FormatInt(m.UnknownSeconds, 10), strconv.Itoa(m.OutageCount), strconv.FormatInt(m.OutageSeconds, 10), "", "", ""})
			for _, out := range m.Outages {
				finish := ""
				if !out.End.IsZero() {
					finish = out.End.Format(time.RFC3339)
				}
				_ = writer.Write([]string{kind + " — przerwa", m.Name, "", "", "", "", "", out.Start.Format(time.RFC3339), finish, strconv.FormatBool(out.Open)})
			}
		}
		writeMetric("Urządzenie / CLI", report.Device)
		for _, metric := range report.Aggregations {
			writeMetric("Agregacja LACP", metric)
		}
		_ = writer.Write([]string{"Metoda", report.Method})
		_ = writer.Write([]string{"Okres od", report.From.Format(time.RFC3339)})
		_ = writer.Write([]string{"Okres do", report.To.Format(time.RFC3339)})
		_ = writer.Write([]string{"Maksymalna luka próbki (s)", strconv.FormatInt(report.MaxSampleGapSeconds, 10)})
		writer.Flush()
	})
}
