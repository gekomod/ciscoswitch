package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"gs948mx-manager/internal/device"
	"gs948mx-manager/internal/settings"
)

// LivePort to stan jednego portu w strumieniu na żywo (diody frontu switcha).
type LivePort struct {
	Name   string `json:"name"`
	Admin  string `json:"admin"`
	Oper   string `json:"oper"`
	Speed  uint64 `json:"speed"` // Mb/s
	RX     uint64 `json:"rx"`    // b/s
	TX     uint64 `json:"tx"`    // b/s
	ErrNew uint64 `json:"errNew"`
}

type liveFrame struct {
	At    string     `json:"at"`
	Ports []LivePort `json:"ports"`
	Error string     `json:"error,omitempty"`
}

type liveSampler func() (time.Time, map[int]device.LiveRaw, error)

// liveHub odczytuje switch tylko, gdy ktoś ogląda panel, i rozsyła klatki
// do wszystkich podłączonych przeglądarek.
type liveHub struct {
	sample   liveSampler
	interval func() time.Duration
	mu       sync.Mutex
	subs     map[uint64]chan []byte
	nextID   uint64
	running  bool
	stop     chan struct{}
	last     []byte
}

func newLiveHub(sample liveSampler, interval func() time.Duration) *liveHub {
	return &liveHub{sample: sample, interval: interval, subs: map[uint64]chan []byte{}}
}

func liveInterval() time.Duration {
	n := settings.Int("LIVE_INTERVAL")
	if n <= 0 {
		n = 2
	}
	return time.Duration(n) * time.Second
}

func (h *liveHub) subscribe() (uint64, <-chan []byte, []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextID++
	ch := make(chan []byte, 4)
	h.subs[h.nextID] = ch
	if !h.running {
		h.running = true
		h.stop = make(chan struct{})
		go h.loop(h.stop)
	}
	return h.nextID, ch, h.last
}

func (h *liveHub) unsubscribe(id uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.subs[id]; ok {
		delete(h.subs, id)
		close(ch)
	}
	if len(h.subs) == 0 && h.running {
		close(h.stop)
		h.running = false
	}
}

func (h *liveHub) publish(b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.last = b
	for _, ch := range h.subs {
		select {
		case ch <- b:
		default: // wolny klient dostanie następną klatkę
		}
	}
}

func (h *liveHub) loop(stop chan struct{}) {
	var prevAt time.Time
	prev := map[int]device.LiveRaw{}
	failures := 0
	for {
		at, cur, err := h.sample()
		frame := liveFrame{At: at.Format(time.RFC3339Nano), Ports: []LivePort{}}
		if err != nil {
			failures++
			frame.Error = "Switch nie odpowiada przez SNMP"
			if failures == 1 {
				log.Printf("live: %v", err)
			}
		} else {
			failures = 0
			frame.Ports = liveDiff(prevAt, prev, at, cur)
			prevAt, prev = at, cur
		}
		if b, err := json.Marshal(frame); err == nil {
			h.publish(b)
		}
		wait := h.interval()
		if failures > 0 {
			wait *= time.Duration(min(failures, 5)) // po błędach rzadziej
		}
		select {
		case <-stop:
			return
		case <-time.After(wait):
		}
	}
}

// liveDiff wylicza szybkość ruchu i nowe błędy z różnicy dwóch odczytów.
func liveDiff(prevAt time.Time, prev map[int]device.LiveRaw, at time.Time, cur map[int]device.LiveRaw) []LivePort {
	secs := at.Sub(prevAt).Seconds()
	out := make([]LivePort, 0, len(cur))
	for idx, c := range cur {
		lp := LivePort{Name: c.Name, Admin: c.Admin, Oper: c.Oper, Speed: c.SpeedMbps}
		if p, ok := prev[idx]; ok && secs > 0.2 {
			if c.InOctets >= p.InOctets {
				lp.RX = uint64(float64(c.InOctets-p.InOctets) * 8 / secs)
			}
			if c.OutOctets >= p.OutOctets {
				lp.TX = uint64(float64(c.OutOctets-p.OutOctets) * 8 / secs)
			}
			if c.Errors > p.Errors {
				lp.ErrNew = c.Errors - p.Errors
			}
		}
		out = append(out, lp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func liveStreamHandler(h *liveHub) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming is not supported", http.StatusInternalServerError)
			return
		}
		// Strumień trwa dłużej niż WriteTimeout serwera; zdejmujemy limit dla tego połączenia.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		id, ch, last := h.subscribe()
		defer h.unsubscribe(id)
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		fmt.Fprintf(w, "retry: 3000\n\n")
		if last != nil {
			fmt.Fprintf(w, "event: ports\ndata: %s\n\n", last)
		}
		flusher.Flush()
		heartbeat := time.NewTicker(20 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case b, open := <-ch:
				if !open {
					return
				}
				if _, err := fmt.Fprintf(w, "event: ports\ndata: %s\n\n", b); err != nil {
					return
				}
				flusher.Flush()
			case <-heartbeat.C:
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})
}

func registerLiveRoutes(apiMux *http.ServeMux, svc *device.Service) {
	hub := newLiveHub(svc.LiveSample, liveInterval)
	apiMux.Handle("/api/ports/live", liveStreamHandler(hub))
}
