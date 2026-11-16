package partysource

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	wait  bool // answer only once the rig's release closes
}

type rig struct {
	t        *testing.T
	source   *Source
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	replies  []reply
	auth     []string
	paths    []string
	commands []string
	modes    []string
	waits    []time.Duration
	logs     []string
	hold     bool
	release  chan struct{}
	freed    sync.Once
	refuse   int // commands the controller refuses before it takes one
}

var (
	party     = Target{Mode: "follow-party", Name: "Follow Party", Entity: "sensor.house_mode_code", Value: "3"}
	lightshow = Target{Mode: "follow-lightshow", Name: "Follow Lightshow", Entity: "binary_sensor.engine_visualizer", Value: "on"}
)

func newRig(t *testing.T, token string, replies ...reply) *rig {
	r := &rig{t: t, replies: replies}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		n := len(r.auth)
		r.auth = append(r.auth, req.Header.Get("Authorization"))
		r.paths = append(r.paths, req.URL.Path)
		r.mu.Unlock()
		entity := strings.TrimPrefix(req.URL.Path, "/api/states/")
		if (entity != party.Entity && entity != lightshow.Entity) || n >= len(replies) {
			http.NotFound(w, req)
			return
		}
		if replies[n].wait {
			<-r.release
		}
		switch v := replies[n]; {
		case v.state == "hang":
			<-req.Context().Done()
		case v.code != 0:
			http.Error(w, "scripted failure", v.code)
		default:
			fmt.Fprintf(w, `{"entity_id":%q,"state":%q,"attributes":{}}`, entity, v.state)
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(r.free)
	file := filepath.Join(t.TempDir(), "ha.token")
	if e := os.WriteFile(file, []byte(token), 0600); e != nil {
		t.Fatal(e)
	}
	fakeACL(t, grants(testUser), nil)
	r.ctx, r.cancel = context.WithCancel(context.Background())
	t.Cleanup(r.cancel)
	r.source = New(Config{URL: server.URL, TokenFile: file, Poll: 5 * time.Second, Targets: []Target{party, lightshow}}, r.command, r.logf)
	r.source.client = server.Client()
	r.source.client.Timeout = 200 * time.Millisecond
	r.source.after = r.after
	return r
}
func (r *rig) command(mode, c string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands = append(r.commands, c)
	r.modes = append(r.modes, mode)
	if r.refuse > 0 {
		r.refuse--
		return fmt.Errorf("%s is no longer the selected mode", mode)
	}
	return nil
}

// free lets held answers go; cleanup calls it too, so a failed test cannot hold the server.
func (r *rig) free() {
	r.freed.Do(func() {
		if r.release != nil {
			close(r.release)
		}
	})
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
	return r.follow(party.Mode)
}
func (r *rig) follow(mode string) error {
	r.source.Follow(mode)
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
	if s := r.source.Status(party.Mode); s.State != "polling" || s.Value != "3" || !strings.Contains(s.Summary, "sensor.house_mode_code is 3") {
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
	for _, target := range []Target{party, lightshow} {
		t.Run(target.Mode, func(t *testing.T) {
			r := newRig(t, "example-token", reply{state: target.Value}, reply{code: 500}, reply{code: 500}, reply{state: "hang"}, reply{code: 502}, reply{state: "unavailable"}, reply{state: target.Value}, reply{state: "1"})
			if e := r.follow(target.Mode); e != nil {
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
				if strings.HasPrefix(l, target.Name+": reading "+target.Entity+" failed") {
					failures++
				}
			}
			if failures != 5 {
				t.Fatalf("%d failures logged: %q", failures, r.logs)
			}
		})
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
		if s := r.source.Status(party.Mode); s.State != "refused" {
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
	if s := r.source.Status(party.Mode); s.State != "refused" {
		t.Fatalf("status %+v", s)
	}
}

func TestManualModeDoesNotPoll(t *testing.T) {
	r := newRig(t, "example-token", reply{state: "3"}, reply{state: "3"})
	r.hold = true
	done := make(chan error, 1)
	go func() { done <- r.source.Run(r.ctx) }()
	eventually(t, func() bool { return r.source.Status(party.Mode).State == "idle" })
	time.Sleep(20 * time.Millisecond)
	if _, _, requests := r.snapshot(); requests != 0 {
		t.Fatalf("manual mode polled %d times", requests)
	}
	r.source.Follow(party.Mode)
	eventually(t, func() bool { _, w, _ := r.snapshot(); return len(w) == 1 })
	r.source.Follow("manual")
	eventually(t, func() bool { return r.source.Status(party.Mode).State == "idle" })
	r.source.Follow(party.Mode)
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
	s.Follow(party.Mode)
	for _, mode := range []string{party.Mode, lightshow.Mode} {
		if st := s.Status(mode); st.State != "unconfigured" || st.Summary != "no Home Assistant source configured" {
			t.Fatalf("status %+v", st)
		}
	}
}

func TestASwitchBetweenTargetsPollsOnlyTheSelectedEntity(t *testing.T) {
	r := newRig(t, "example-token", reply{state: "3"}, reply{state: "on"})
	r.hold = true
	done := make(chan error, 1)
	go func() { done <- r.source.Run(r.ctx) }()
	r.source.Follow(party.Mode)
	eventually(t, func() bool { _, w, _ := r.snapshot(); return len(w) == 1 })
	r.source.Follow(lightshow.Mode)
	eventually(t, func() bool { _, w, _ := r.snapshot(); return len(w) == 2 })
	if s := r.source.Status(party.Mode); s.State != "idle" || s.Summary != "not selected; Home Assistant not polled" {
		t.Fatalf("Follow Party after the switch: %+v", s)
	}
	if s := r.source.Status(lightshow.Mode); s.State != "polling" || s.Value != "on" || !strings.Contains(s.Summary, lightshow.Entity+" is on (party)") {
		t.Fatalf("Follow Lightshow after the switch: %+v", s)
	}
	r.cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if want := []string{"/api/states/" + party.Entity, "/api/states/" + lightshow.Entity}; !reflect.DeepEqual(r.paths, want) {
		t.Fatalf("requests %v, want %v", r.paths, want)
	}
	// The first reading after the switch is applied even though the party state is unchanged.
	if !reflect.DeepEqual(r.commands, []string{"party-on", "party-on"}) || !reflect.DeepEqual(r.modes, []string{party.Mode, lightshow.Mode}) {
		t.Fatalf("commands %v for %v", r.commands, r.modes)
	}
	for _, want := range []string{"Follow Party: polling Home Assistant sensor.house_mode_code every 5s", "Follow Party: not selected; stopped polling sensor.house_mode_code", "Follow Lightshow: polling Home Assistant binary_sensor.engine_visualizer every 5s", `Follow Lightshow: binary_sensor.engine_visualizer is "on"; sent party-on`} {
		if !slices.Contains(r.logs, want) {
			t.Errorf("no log line %q in %q", want, r.logs)
		}
	}
}

func TestARejectedTokenStopsPollingForEveryTarget(t *testing.T) {
	for i, target := range []Target{party, lightshow} {
		other := []Target{party, lightshow}[1-i]
		t.Run(target.Mode, func(t *testing.T) {
			r := newRig(t, "example-token", reply{code: 403}, reply{state: "on"})
			if e := r.follow(target.Mode); e == nil {
				t.Fatal("a rejected token kept polling")
			}
			r.source.Follow(other.Mode)
			time.Sleep(20 * time.Millisecond)
			if _, _, requests := r.snapshot(); requests != 1 {
				t.Fatalf("%d requests: %s sent the rejected token again", requests, other.Name)
			}
			for _, mode := range []string{party.Mode, lightshow.Mode} {
				if s := r.source.Status(mode); s.State != "refused" || !strings.HasPrefix(s.Summary, "not polling: Home Assistant rejected the token") {
					t.Fatalf("%s: %+v", mode, s)
				}
			}
			if !slices.Contains(r.logs, "Follow Party and Follow Lightshow: not polling Home Assistant: Home Assistant rejected the token (403 Forbidden); fix the token file and restart the controller") {
				t.Fatalf("logs %q", r.logs)
			}
		})
	}
}

func TestAReadingInFlightAtASwitchCountsForTheModeItWasReadFor(t *testing.T) {
	r := newRig(t, "example-token", reply{state: "3", wait: true}, reply{state: "off"})
	r.release = make(chan struct{})
	r.hold = true
	done := make(chan error, 1)
	go func() { done <- r.source.Run(r.ctx) }()
	r.source.Follow(party.Mode)
	eventually(t, func() bool { _, _, n := r.snapshot(); return n == 1 })
	r.source.Follow(lightshow.Mode)
	r.free()
	eventually(t, func() bool { c, _, _ := r.snapshot(); return len(c) == 2 })
	r.cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !reflect.DeepEqual(r.commands, []string{"party-on", "party-off"}) || !reflect.DeepEqual(r.modes, []string{party.Mode, lightshow.Mode}) {
		t.Fatalf("commands %v for %v: a reading must carry the mode it was read for", r.commands, r.modes)
	}
}

func TestAReadingTheControllerRefusedIsSentAgain(t *testing.T) {
	r := newRig(t, "example-token", reply{state: "3"}, reply{state: "3"}, reply{state: "3"})
	r.refuse = 1
	if e := r.run(); e != nil {
		t.Fatal(e)
	}
	if commands, _, _ := r.snapshot(); !reflect.DeepEqual(commands, []string{"party-on", "party-on"}) {
		t.Fatalf("commands %v: a refused party-on must be sent again on the next reading, and only until it is taken", commands)
	}
	if !slices.Contains(r.logs, `Follow Party: sensor.house_mode_code is "3" but the controller refused party-on: follow-party is no longer the selected mode`) {
		t.Fatalf("logs %q", r.logs)
	}
}

func TestAModeWithoutATargetPollsNothing(t *testing.T) {
	r := newRig(t, "example-token", reply{state: "on"})
	r.source.config.Targets = []Target{party}
	r.hold = true
	done := make(chan error, 1)
	go func() { done <- r.source.Run(r.ctx) }()
	r.source.Follow(lightshow.Mode)
	time.Sleep(20 * time.Millisecond)
	if _, _, requests := r.snapshot(); requests != 0 {
		t.Fatalf("Follow Lightshow without an entity polled %d times", requests)
	}
	if s := r.source.Status(lightshow.Mode); s.State != "unconfigured" || s.Summary != Unconfigured {
		t.Fatalf("status %+v", s)
	}
	r.cancel()
	<-done
}
