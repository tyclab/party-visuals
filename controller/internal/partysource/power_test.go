package partysource

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// powerRig scripts Home Assistant's answers for the display's power entity
// and a clock that moves one poll interval per wait.
type powerRig struct {
	t       *testing.T
	power   *Power
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	replies []reply
	auth    int
	waits   []time.Duration
	seen    []string
	logs    []string
	clock   time.Time
}

func newPowerRig(t *testing.T, token string, replies ...reply) *powerRig {
	r := &powerRig{t: t, replies: replies, clock: time.Date(2026, 11, 15, 21, 0, 0, 0, time.UTC)}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		n := r.auth
		r.auth++
		r.mu.Unlock()
		if req.URL.Path != "/api/states/media_player.big_tv" || req.Header.Get("Authorization") != "Bearer example-token" || n >= len(replies) {
			http.NotFound(w, req)
			return
		}
		switch v := replies[n]; {
		case v.state == "hang":
			<-req.Context().Done()
		case v.code != 0:
			http.Error(w, "scripted failure", v.code)
		default:
			fmt.Fprintf(w, `{"entity_id":"media_player.big_tv","state":%q,"attributes":{}}`, v.state)
		}
	}))
	t.Cleanup(server.Close)
	file := filepath.Join(t.TempDir(), "ha.token")
	if e := os.WriteFile(file, []byte(token), 0600); e != nil {
		t.Fatal(e)
	}
	fakeACL(t, grants(testUser), nil)
	r.ctx, r.cancel = context.WithCancel(context.Background())
	t.Cleanup(r.cancel)
	r.power = NewPower(New(Config{URL: server.URL, TokenFile: file, Poll: 5 * time.Second}, nil, r.logf), "media_player.big_tv", r.logf, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.seen = append(r.seen, r.power.Reading())
	})
	r.power.src.client = server.Client()
	r.power.src.client.Timeout = 200 * time.Millisecond
	r.power.after = r.after
	r.power.now = func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.clock }
	return r
}
func (r *powerRig) logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}
func (r *powerRig) after(d time.Duration) <-chan time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.waits = append(r.waits, d)
	r.clock = r.clock.Add(d)
	if r.auth >= len(r.replies) {
		r.cancel()
		return nil
	}
	c := make(chan time.Time, 1)
	c <- time.Time{}
	return c
}
func (r *powerRig) run() error {
	done := make(chan error, 1)
	go func() { done <- r.power.Run(r.ctx) }()
	select {
	case e := <-done:
		return e
	case <-time.After(5 * time.Second):
		r.t.Fatal("power reader did not finish its script")
		return nil
	}
}
func (r *powerRig) snapshot() (seen []string, waits []time.Duration, requests int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...), append([]time.Duration(nil), r.waits...), r.auth
}
func states(v ...string) []reply {
	r := make([]reply, len(v))
	for i, s := range v {
		r[i].state = s
	}
	return r
}

func TestPowerTakesTheFirstReadingAtOnceAndAChangeOnceItHeldThirtySeconds(t *testing.T) {
	r := newPowerRig(t, "example-token", states("on", "off", "off", "off", "off", "off", "off", "off", "standby")...)
	if e := r.run(); e != nil {
		t.Fatal(e)
	}
	seen, waits, _ := r.snapshot()
	if want := []string{"on", "on", "on", "on", "on", "on", "on", "off", "off"}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("readings %v, want %v", seen, want)
	}
	if want := seconds(5, 5, 5, 5, 5, 5, 5, 5, 5); !reflect.DeepEqual(waits, want) {
		t.Fatalf("waits %v, want %v", waits, want)
	}
	changes := 0
	for _, l := range r.logs {
		if strings.Contains(l, "media_player.big_tv reads ") {
			changes++
		}
	}
	if changes != 2 {
		t.Fatalf("%d changes logged: %q", changes, r.logs)
	}
	if s := r.power.Status(); s.State != "polling" || s.Reading != "off" || s.Summary != "media_player.big_tv is standby (off)" {
		t.Fatalf("status %+v", s)
	}
}

func TestPowerIgnoresAChangeShorterThanThirtySeconds(t *testing.T) {
	r := newPowerRig(t, "example-token", states("playing", "off", "off", "off", "off", "off", "on", "off")...)
	if e := r.run(); e != nil {
		t.Fatal(e)
	}
	seen, _, _ := r.snapshot()
	if want := []string{"on", "on", "on", "on", "on", "on", "on", "on"}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("readings %v, want %v", seen, want)
	}
}

func TestPowerKeepsItsReadingThroughFailuresAndBacksOff(t *testing.T) {
	r := newPowerRig(t, "example-token", reply{state: "off"}, reply{code: 500}, reply{state: "unavailable"}, reply{state: "hang"}, reply{state: "unknown"}, reply{code: 502}, reply{state: "off"})
	if e := r.run(); e != nil {
		t.Fatal(e)
	}
	seen, waits, _ := r.snapshot()
	if want := []string{"off", "off", "off", "off", "off", "off", "off"}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("readings %v, want %v", seen, want)
	}
	if want := seconds(5, 10, 20, 40, 60, 60, 5); !reflect.DeepEqual(waits, want) {
		t.Fatalf("waits %v, want %v", waits, want)
	}
}

func TestPowerIsUnknownUntilItsFirstReading(t *testing.T) {
	r := newPowerRig(t, "example-token", reply{code: 500}, reply{state: "on"})
	if got := r.power.Reading(); got != "unknown" {
		t.Fatalf("before Run: %s", got)
	}
	if e := r.run(); e != nil {
		t.Fatal(e)
	}
	if seen, _, _ := r.snapshot(); !reflect.DeepEqual(seen, []string{"unknown", "on"}) {
		t.Fatalf("readings %v", seen)
	}
}

func TestPowerGoesByWindowsListAloneAfterARejectedToken(t *testing.T) {
	for _, before := range []string{"on", "off"} {
		r := newPowerRig(t, "example-token", reply{state: before}, reply{code: 403}, reply{state: "on"})
		if e := r.run(); e == nil {
			t.Fatal("rejected token kept polling")
		}
		_, _, requests := r.snapshot()
		if requests != 2 || r.power.Reading() != "unknown" {
			t.Fatalf("after %s: %d requests, reading %s", before, requests, r.power.Reading())
		}
		if s := r.power.Status(); s.State != "refused" || !strings.Contains(s.Summary, "not polling: Home Assistant rejected the token") {
			t.Fatalf("status %+v", s)
		}
	}
}

func TestPowerRefusesAnEmptyTokenWithoutRequest(t *testing.T) {
	r := newPowerRig(t, " \n", reply{state: "on"})
	if e := r.run(); e == nil {
		t.Fatal("empty token accepted")
	}
	if _, _, requests := r.snapshot(); requests != 0 || r.power.Reading() != "unknown" || r.power.Status().State != "refused" {
		t.Fatalf("%d requests, reading %s, status %+v", requests, r.power.Reading(), r.power.Status())
	}
}

func TestPowerStatesReadAsOnOrOff(t *testing.T) {
	for state, want := range map[string]string{"off": "off", "standby": "off", "on": "on", "playing": "on", "idle": "on", "paused": "on", "buffering": "on"} {
		if got := powerOf(state); got != want {
			t.Errorf("%s: %s, want %s", state, got, want)
		}
	}
}

func TestUnconfiguredPowerReadsNone(t *testing.T) {
	var p *Power
	if p.Reading() != "none" || p.Status().State != "unconfigured" {
		t.Fatalf("%s %+v", p.Reading(), p.Status())
	}
}
