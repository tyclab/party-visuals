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

// reply is one scripted Home Assistant answer: a status code, or "hang" to
// outlast the client timeout.
type reply struct {
	code  int
	state string
}

type rig struct {
	t        *testing.T
	source   *Source
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	replies  []reply
	auth     []string
	commands []string
	waits    []time.Duration
	logs     []string
	hold     bool
}

func newRig(t *testing.T, token string, replies ...reply) *rig {
	r := &rig{t: t, replies: replies}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		n := len(r.auth)
		r.auth = append(r.auth, req.Header.Get("Authorization"))
		r.mu.Unlock()
		if req.URL.Path != "/api/states/sensor.house_mode_code" || n >= len(replies) {
			http.NotFound(w, req)
			return
		}
		switch v := replies[n]; {
		case v.state == "hang":
			<-req.Context().Done()
		case v.code != 0:
			http.Error(w, "scripted failure", v.code)
		default:
			fmt.Fprintf(w, `{"entity_id":"sensor.house_mode_code","state":%q,"attributes":{}}`, v.state)
		}
	}))
	t.Cleanup(server.Close)
	file := filepath.Join(t.TempDir(), "ha.token")
	if e := os.WriteFile(file, []byte(token), 0600); e != nil {
		t.Fatal(e)
	}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	t.Cleanup(r.cancel)
	r.source = New(Config{URL: server.URL, TokenFile: file, Entity: "sensor.house_mode_code", Value: "3", Poll: 5 * time.Second}, r.command, r.logf)
	r.source.client = server.Client()
	r.source.client.Timeout = 200 * time.Millisecond
	r.source.after = r.after
	return r
}
func (r *rig) command(c string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, c)
	return nil
}
func (r *rig) logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

// after stands in for the poll timer: it records each delay and fires at
// once until the script is spent, then ends the run (or parks while held).
func (r *rig) after(d time.Duration) <-chan time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.waits = append(r.waits, d)
	if r.hold {
		return nil
	}
	if len(r.auth) >= len(r.replies) {
		r.cancel()
		return nil
	}
	c := make(chan time.Time, 1)
	c <- time.Time{}
	return c
}
func (r *rig) run() error {
	r.source.SetActive(true)
	done := make(chan error, 1)
	go func() { done <- r.source.Run(r.ctx) }()
	select {
	case e := <-done:
		return e
	case <-time.After(5 * time.Second):
		r.t.Fatal("source did not finish its script")
		return nil
	}
}
func (r *rig) snapshot() (commands []string, waits []time.Duration, requests int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.commands...), append([]time.Duration(nil), r.waits...), len(r.auth)
}
func eventually(t *testing.T, f func() bool) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for !f() {
		if time.Now().After(end) {
			t.Fatal("condition did not settle")
		}
		time.Sleep(time.Millisecond)
	}
}
func seconds(v ...int) []time.Duration {
	d := make([]time.Duration, len(v))
	for i, s := range v {
		d[i] = time.Duration(s) * time.Second
	}
	return d
}

func TestPartyTransitionsSendEachCommandOnce(t *testing.T) {
	r := newRig(t, "example-token\n", reply{state: "3"}, reply{state: "3"}, reply{state: "1"}, reply{state: "1"}, reply{state: "3"}, reply{state: "3"})
	if e := r.run(); e != nil {
		t.Fatal(e)
	}
	commands, waits, _ := r.snapshot()
	if want := []string{"party-on", "party-off", "party-on"}; !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands %v, want %v", commands, want)
	}
	if want := seconds(5, 5, 5, 5, 5, 5); !reflect.DeepEqual(waits, want) {
		t.Fatalf("waits %v, want %v", waits, want)
	}
	for _, a := range r.auth {
		if a != "Bearer example-token" {
			t.Fatalf("authorization %q", a)
		}
	}
	transitions := 0
	for _, l := range r.logs {
		if strings.Contains(l, "party-on") || strings.Contains(l, "party-off") {
			transitions++
		}
	}
	if transitions != 3 {
		t.Fatalf("%d transitions logged: %q", transitions, r.logs)
	}
	if s := r.source.Status(); s.State != "polling" || s.Value != "3" || !strings.Contains(s.Summary, "sensor.house_mode_code is 3") {
		t.Fatalf("status %+v", s)
	}
}

func TestUnchangedStateSendsNoFurtherCommand(t *testing.T) {
	r := newRig(t, "example-token", reply{state: "1"}, reply{state: "2"}, reply{state: "1"}, reply{state: "1"})
	if e := r.run(); e != nil {
		t.Fatal(e)
	}
	commands, _, requests := r.snapshot()
	if requests != 4 || !reflect.DeepEqual(commands, []string{"party-off"}) {
		t.Fatalf("%d requests sent %v; only the first reading may sync party-off", requests, commands)
	}
}

func TestFailuresBackOffToSixtySecondsAndKeepPartyState(t *testing.T) {
	r := newRig(t, "example-token", reply{state: "3"}, reply{code: 500}, reply{code: 500}, reply{state: "hang"}, reply{code: 502}, reply{state: "unavailable"}, reply{state: "3"}, reply{state: "1"})
	if e := r.run(); e != nil {
		t.Fatal(e)
	}
	commands, waits, _ := r.snapshot()
	if want := seconds(5, 10, 20, 40, 60, 60, 5, 5); !reflect.DeepEqual(waits, want) {
		t.Fatalf("waits %v, want %v", waits, want)
	}
	if want := []string{"party-on", "party-off"}; !reflect.DeepEqual(commands, want) {
		t.Fatalf("failures changed the party state: %v", commands)
	}
	failures := 0
	for _, l := range r.logs {
		if strings.Contains(l, "failed") {
			failures++
		}
	}
	if failures != 5 {
		t.Fatalf("%d failures logged: %q", failures, r.logs)
	}
}

func TestEmptyTokenRefusesWithoutRequest(t *testing.T) {
	for _, token := range []string{"", " \r\n", "\uFEFF\n"} {
		r := newRig(t, token, reply{state: "3"})
		if e := r.run(); e == nil {
			t.Fatalf("token %q accepted", token)
		}
		commands, _, requests := r.snapshot()
		if requests != 0 || len(commands) != 0 {
			t.Fatalf("token %q: %d requests, commands %v", token, requests, commands)
		}
		if s := r.source.Status(); s.State != "refused" {
			t.Fatalf("status %+v", s)
		}
	}
}

func TestRejectedTokenStopsPolling(t *testing.T) {
	r := newRig(t, "example-token", reply{code: 401}, reply{state: "3"})
	if e := r.run(); e == nil {
		t.Fatal("rejected token kept polling")
	}
	commands, waits, requests := r.snapshot()
	if requests != 1 || len(commands) != 0 || len(waits) != 0 {
		t.Fatalf("%d requests, commands %v, waits %v after 401", requests, commands, waits)
	}
	if s := r.source.Status(); s.State != "refused" {
		t.Fatalf("status %+v", s)
	}
}

func TestManualModeDoesNotPoll(t *testing.T) {
	r := newRig(t, "example-token", reply{state: "3"}, reply{state: "3"})
	r.hold = true
	done := make(chan error, 1)
	go func() { done <- r.source.Run(r.ctx) }()
	eventually(t, func() bool { return r.source.Status().State == "idle" })
	time.Sleep(20 * time.Millisecond)
	if _, _, requests := r.snapshot(); requests != 0 {
		t.Fatalf("manual mode polled %d times", requests)
	}
	r.source.SetActive(true)
	eventually(t, func() bool { _, w, _ := r.snapshot(); return len(w) == 1 })
	r.source.SetActive(false)
	eventually(t, func() bool { return r.source.Status().State == "idle" })
	r.source.SetActive(true)
	eventually(t, func() bool { _, w, _ := r.snapshot(); return len(w) == 2 })
	r.cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	commands, _, requests := r.snapshot()
	// Re-entering Follow Party applies the reading again, even unchanged.
	if requests != 2 || !reflect.DeepEqual(commands, []string{"party-on", "party-on"}) {
		t.Fatalf("%d requests, commands %v", requests, commands)
	}
}

func TestUnconfiguredSourceReportsItself(t *testing.T) {
	var s *Source
	s.SetActive(true)
	if st := s.Status(); st.State != "unconfigured" || st.Summary != "no Home Assistant source configured" {
		t.Fatalf("status %+v", st)
	}
}
