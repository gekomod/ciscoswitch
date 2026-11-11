package server

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gs948mx-manager/internal/device"
)

func TestLiveDiff(t *testing.T) {
	t0 := time.Now()
	prev := map[int]device.LiveRaw{1: {Name: "port1.0.1", InOctets: 1000, OutOctets: 0, Errors: 5}}
	cur := map[int]device.LiveRaw{1: {Name: "port1.0.1", Admin: "up", Oper: "up", SpeedMbps: 1000, InOctets: 1000 + 250000, OutOctets: 125000, Errors: 7}}
	got := liveDiff(t0, prev, t0.Add(2*time.Second), cur)
	if len(got) != 1 || got[0].RX != 1000000 || got[0].TX != 500000 || got[0].ErrNew != 2 || got[0].Speed != 1000 {
		t.Fatalf("%+v", got)
	}
	// licznik się wyzerował (restart switcha): bez ujemnych ani ogromnych wartości
	got = liveDiff(t0, cur, t0.Add(2*time.Second), prev)
	if got[0].RX != 0 || got[0].TX != 0 || got[0].ErrNew != 0 {
		t.Fatalf("counter reset: %+v", got)
	}
}

func TestLiveHubPollsOnlyWhileWatched(t *testing.T) {
	var calls int64
	var octets uint64
	sampler := func() (time.Time, map[int]device.LiveRaw, error) {
		atomic.AddInt64(&calls, 1)
		octets += 125000
		return time.Now(), map[int]device.LiveRaw{1: {Name: "port1.0.1", Admin: "up", Oper: "up", InOctets: octets}}, nil
	}
	// odstęp powyżej 0,2 s: krótszych panel celowo nie używa do liczenia szybkości
	hub := newLiveHub(sampler, func() time.Duration { return 250 * time.Millisecond })
	srv := httptest.NewServer(liveStreamHandler(hub))
	defer srv.Close()
	if atomic.LoadInt64(&calls) != 0 {
		t.Fatal("no polling before anyone watches")
	}
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			resp.Body.Close()
		}
	}()
	sc := bufio.NewScanner(resp.Body)
	frames := 0
	var last liveFrame
	deadline := time.After(5 * time.Second)
	for frames < 3 {
		select {
		case <-deadline:
			t.Fatal("no frames")
		default:
		}
		if !sc.Scan() {
			t.Fatal("stream closed")
		}
		if strings.HasPrefix(sc.Text(), "data: ") {
			frames++
			json.Unmarshal([]byte(strings.TrimPrefix(sc.Text(), "data: ")), &last)
		}
	}
	if len(last.Ports) != 1 || last.Ports[0].RX == 0 {
		t.Fatalf("expected computed rate, got %+v", last)
	}
	resp.Body.Close()
	closed = true
	time.Sleep(600 * time.Millisecond)
	before := atomic.LoadInt64(&calls)
	time.Sleep(600 * time.Millisecond)
	if after := atomic.LoadInt64(&calls); after != before {
		t.Fatalf("polling must stop when nobody watches (%d -> %d)", before, after)
	}
}
