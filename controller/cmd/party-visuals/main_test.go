package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/tyclab/party-visuals/controller/internal/lifecycle"
	"github.com/tyclab/party-visuals/controller/internal/owner"
	"github.com/tyclab/party-visuals/controller/internal/partysource"
	"github.com/tyclab/party-visuals/controller/internal/worker"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeRoot(t *testing.T, extra map[string]any) string {
	t.Helper()
	root := t.TempDir()
	v := map[string]any{"version": 1, "nodecgRoot": root, "browserProfile": filepath.Join(root, "profile"), "logFile": filepath.Join(root, "nodecg.log"), "visualizerUrl": "http://music.example.invalid:8095/#/now-playing?player=test", "controlsUrl": "http://127.0.0.1:9090/bundles/party-visuals/api/state"}
	for _, name := range []string{"nodeExe", "chromeExe", "helperPath"} {
		p := filepath.Join(root, name)
		os.WriteFile(p, nil, 0600)
		v[name] = p
	}
	v["workerScript"] = besideTestBinary(t, "Run-PartyVisualsSession.ps1")
	for k, x := range extra {
		v[k] = x
	}
	b, _ := json.Marshal(v)
	os.WriteFile(filepath.Join(root, "config.json"), b, 0600)
	return root
}

// besideTestBinary writes an empty file next to the running test binary,
// the only folder whose worker script Load accepts.
func besideTestBinary(t *testing.T, name string) string {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(filepath.Dir(exe), name)
	if e = os.WriteFile(p, nil, 0600); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.Remove(p) })
	return p
}
func stdout(t *testing.T, f func() error) (string, error) {
	t.Helper()
	r, w, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	saved := os.Stdout
	os.Stdout = w
	e = f()
	os.Stdout = saved
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b), e
}

func TestCheckValidatesTheHomeAssistantTokenFile(t *testing.T) {
	out, e := stdout(t, func() error { return run([]string{"--root", writeRoot(t, nil), "check"}) })
	if e != nil || !strings.Contains(out, "Follow Party: no Home Assistant source configured") {
		t.Fatalf("unconfigured check: %q, %v", out, e)
	}
	token := filepath.Join(t.TempDir(), "ha.token")
	os.WriteFile(token, nil, 0600)
	ownerOnly(t, token)
	root := writeRoot(t, map[string]any{"homeAssistantUrl": "https://ha.example.invalid", "homeAssistantTokenFile": token})
	for _, content := range []string{"", "\n"} {
		os.WriteFile(token, []byte(content), 0600)
		if _, e = stdout(t, func() error { return run([]string{"--root", root, "check"}) }); e == nil {
			t.Fatalf("check accepted token file %q", content)
		}
	}
	os.WriteFile(token, []byte("example-token\n"), 0600)
	out, e = stdout(t, func() error { return run([]string{"--root", root, "check"}) })
	if e != nil || !strings.Contains(out, "sensor.house_mode_code") || strings.Contains(out, "example-token") {
		t.Fatalf("configured check: %q, %v", out, e)
	}
}

func TestCheckRefusesAWorkerScriptOutsideTheReleaseFolder(t *testing.T) {
	elsewhere := filepath.Join(t.TempDir(), "Run-PartyVisualsSession.ps1")
	os.WriteFile(elsewhere, nil, 0600)
	root := writeRoot(t, map[string]any{"workerScript": elsewhere})
	_, e := stdout(t, func() error { return run([]string{"--root", root, "check"}) })
	if e == nil || !strings.Contains(e.Error(), "workerScript must be in the folder that holds this executable") {
		t.Fatalf("check: %v", e)
	}
}

func TestDiagnosticsNameTheFollowPartySource(t *testing.T) {
	root := t.TempDir()
	var source *partysource.Source
	server, e := owner.Listen(root, func() any {
		return statusView{Version: "test", State: lifecycle.Status{Phase: "waiting-for-party"}, Diagnostics: worker.Diagnostics{Screen: "ready", Audio: "unknown", OpenRGB: "unavailable"}, FollowParty: source.Status()}
	}, func(string) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	out, e := stdout(t, func() error { return run([]string{"--root", root, "diagnostics"}) })
	if e != nil || !strings.Contains(out, "Follow Party: no Home Assistant source configured\n") || !strings.Contains(out, "waiting-for-party") {
		t.Fatalf("diagnostics: %q, %v", out, e)
	}
	out, e = stdout(t, func() error { return run([]string{"--root", root, "status", "--json"}) })
	var s statusView
	if e != nil || json.Unmarshal([]byte(out), &s) != nil || s.FollowParty.State != "unconfigured" {
		t.Fatalf("status --json: %q, %v", out, e)
	}
}

func TestPartyLinesLandInControllerLog(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "controller.log")
	for i := 0; i < 2; i++ {
		f, e := openLog(root)
		if e != nil {
			t.Fatal(e)
		}
		log.New(f, "", log.LstdFlags).Printf("Follow Party: sensor.house_mode_code is %q; sent party-on", "3")
		f.Close()
	}
	b, _ := os.ReadFile(path)
	if strings.Count(string(b), "sent party-on") != 2 {
		t.Fatalf("controller.log holds %q", b)
	}
	os.WriteFile(path, make([]byte, 1024*1024+1), 0600)
	f, e := openLog(root)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	if old, e := os.Stat(path + ".1"); e != nil || old.Size() != 1024*1024+1 {
		t.Fatalf("full log not rotated: %v", e)
	}
	if fresh, e := os.Stat(path); e != nil || fresh.Size() != 0 {
		t.Fatalf("fresh log: %v", e)
	}
}

func readLog(t *testing.T, root string) string {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(root, "controller.log"))
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestRestoredWindowNoticesLandInControllerLogAndDiagnostics(t *testing.T) {
	root := t.TempDir()
	f, e := openLog(root)
	if e != nil {
		t.Fatal(e)
	}
	l := &controllerLog{Logger: log.New(f, "", log.LstdFlags)}
	if lines := diagnosticLines(worker.Diagnostics{Browser: l.browserNote()}, partysource.Status{}); strings.HasPrefix(lines[len(lines)-1], "Browser:") {
		t.Fatal("a Browser line without a restored window")
	}
	open := strings.TrimSuffix(lifecycle.ErrRestoredOpen.Error(), ".") + " (process 41: access denied)"
	if !l.restored(open) || l.restored(open) {
		t.Fatal("a repeated notice was not recognised")
	}
	if !l.restored(worker.RestoredClosed + " (process 42)") {
		t.Fatal("a new notice was taken for a repeat")
	}
	f.Close()
	b := readLog(t, root)
	for _, want := range []string{open + "\n", "A browser window restored by Windows at sign-in was closed (process 42)\n"} {
		if strings.Count(b, want) != 1 {
			t.Errorf("controller.log lacks one %q:\n%s", want, b)
		}
	}
	lines := diagnosticLines(worker.Diagnostics{Browser: l.browserNote()}, partysource.Status{})
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "Browser: A browser window restored by Windows at sign-in was closed (process 42) at ") {
		t.Fatalf("diagnostics end with %q", last)
	}
}

func TestCommandsAndStatesLandInControllerLog(t *testing.T) {
	root := t.TempDir()
	f, e := openLog(root)
	if e != nil {
		t.Fatal(e)
	}
	l := &controllerLog{Logger: log.New(f, "", log.LstdFlags)}
	apply := func(c string) error {
		switch c {
		case "party-on":
			return errors.New("controller is shutting down")
		case "start", "stop", "follow-party", "quit":
			return nil
		}
		return errors.New("unknown controller command")
	}
	server, e := owner.Listen(root, func() any { return statusView{} }, l.command("cli", apply))
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	if e = l.command("tray", apply)("start"); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"stop"}, {"mode", "follow-party"}, {"quit"}} {
		if _, e = stdout(t, func() error { return run(append([]string{"--root", root}, args...)) }); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = stdout(t, func() error { return run([]string{"--root", root, "party", "on"}) }); e == nil {
		t.Fatal("refused command reported success")
	}
	if _, e = owner.Call(root, "start\nphase running (mode manual, Start requested)"); e == nil {
		t.Fatal("a command with a line break was accepted")
	}
	off := lifecycle.Status{Phase: "off", Intent: lifecycle.Intent{Version: 1, Mode: "manual", ManualOff: true}}
	l.state(off)
	l.state(off)
	refused := off
	refused.Phase, refused.Reason = "attention", lifecycle.ErrRestoredOpen.Error()
	l.state(refused)
	f.Close()
	b := readLog(t, root)
	for _, want := range []string{
		`command "start" from tray: accepted` + "\n",
		`command "stop" from cli: accepted` + "\n",
		`command "follow-party" from cli: accepted` + "\n",
		`command "quit" from cli: accepted` + "\n",
		`command "party-on" from cli: refused: controller is shutting down` + "\n",
		`command "start\nphase running (mode manual, Start requested)" from cli: refused: unknown controller command` + "\n",
		"phase off (mode manual, Manual Off)\n",
		"phase attention (mode manual, Manual Off): A browser window restored by Windows at sign-in is open; close it before Start.\n",
	} {
		if strings.Count(b, want) != 1 {
			t.Errorf("controller.log lacks one %q:\n%s", want, b)
		}
	}
	if strings.Contains(b, "\nphase running") {
		t.Fatalf("a command forged a log line:\n%s", b)
	}
}

// restoredWindows is a backend on which Windows restores the kiosk at any time.
type restoredWindows struct {
	mu     sync.Mutex
	open   int
	sweeps []bool
}

func (r *restoredWindows) Start(context.Context) (lifecycle.Session, error) {
	return nil, errors.New("no worker in this test")
}
func (r *restoredWindows) CloseRestored(_ context.Context, start bool) error {
	r.mu.Lock()
	r.open = 0
	r.sweeps = append(r.sweeps, start)
	r.mu.Unlock()
	return nil
}
func (r *restoredWindows) windows() int { r.mu.Lock(); defer r.mu.Unlock(); return r.open }
func (r *restoredWindows) swept() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool{}, r.sweeps...)
}
func (r *restoredWindows) restore() { r.mu.Lock(); r.open = 1; r.mu.Unlock() }

func TestAWindowRestoredAfterStartupIsClosedWithoutACommand(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &restoredWindows{}
	m, _, e := startManager(ctx, t.TempDir(), b, nil, 10*time.Millisecond, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.Apply("restore")
	for end := time.Now().Add(3 * time.Second); m.Snapshot().Phase != "off"; time.Sleep(time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("restore did not settle")
		}
	}
	b.restore() // as on the show PC, 7 s after the controller started
	for end := time.Now().Add(3 * time.Second); b.windows() != 0; time.Sleep(time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("no sweep closed the window restored after startup")
		}
	}
	if s := m.Snapshot(); s.Phase != "off" || !s.Intent.ManualOff {
		t.Fatalf("the sweep changed the state: %+v", s)
	}
}

func TestAfterTheSignInWindowOnlyStartSweeps(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &restoredWindows{}
	m, done, e := startManager(ctx, t.TempDir(), b, nil, 10*time.Millisecond, 100*time.Millisecond)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the sign-in sweeps did not end with their 100 ms window")
	}
	before := len(b.swept())
	if before == 0 {
		t.Fatal("no sweep in the sign-in window")
	}
	settled := func() bool { p := m.Snapshot().Phase; return p != "starting" && p != "stopping" }
	b.restore()
	for _, c := range []string{"restore", "stop", "follow-party", "manual"} {
		m.Apply(c)
		eventually(t, settled)
	}
	if b.windows() != 1 || len(b.swept()) != before {
		t.Fatalf("after the sign-in window: %d windows open, sweeps %d -> %d", b.windows(), before, len(b.swept()))
	}
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "attention" })
	if s := b.swept(); b.windows() != 0 || len(s) != before+1 || !s[before] {
		t.Fatalf("%d windows open, sweeps %v; want one more, before Start", b.windows(), s[before:])
	}
}

// slowClose is a backend whose close waits, as for a restored Chrome that
// ignores WM_CLOSE, until its context ends.
type slowClose struct{ started, ended chan struct{} }

func (s *slowClose) Start(context.Context) (lifecycle.Session, error) {
	return nil, errors.New("no worker in this test")
}
func (s *slowClose) CloseRestored(ctx context.Context, _ bool) error {
	close(s.started)
	<-ctx.Done()
	close(s.ended)
	return ctx.Err()
}

func TestACloseInProgressEndsWithTheSignInWindow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &slowClose{started: make(chan struct{}), ended: make(chan struct{})}
	m, done, e := startManager(ctx, t.TempDir(), b, nil, time.Hour, 50*time.Millisecond)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	<-b.started
	select {
	case <-b.ended:
	case <-time.After(5 * time.Second):
		t.Fatal("a close in progress outlived the 50 ms sign-in window")
	}
	<-done
}

func eventually(t *testing.T, f func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !f(); time.Sleep(time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("condition did not settle")
		}
	}
}

func TestAControllerThatEndsOnAnErrorSaysWhyInControllerLog(t *testing.T) {
	saved := detach
	detach = func(*os.File) error { return nil }
	defer func() { detach = saved }()
	root := writeRoot(t, nil)
	e := owned(root, func(string, worker.Config, *controllerLog) error { return errors.New("tray: taskbar not there yet") })
	if e == nil || e.Error() != "tray: taskbar not there yet" {
		t.Fatalf("owned returned %v", e)
	}
	b, _ := os.ReadFile(filepath.Join(root, "controller.log"))
	if !strings.Contains(string(b), "controller dev started\n") || !strings.Contains(string(b), "controller stopped: tray: taskbar not there yet\n") {
		t.Fatalf("controller.log holds %q", b)
	}
	if e = owned(root, func(string, worker.Config, *controllerLog) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if b, _ = os.ReadFile(filepath.Join(root, "controller.log")); strings.Count(string(b), "controller stopped") != 1 {
		t.Fatalf("a controller asked to quit logged a stop reason: %q", b)
	}
}

func TestAControllerThatCannotStartSaysWhyInControllerLog(t *testing.T) {
	saved := detach
	defer func() { detach = saved }()
	detach = func(*os.File) error { return nil }
	root := t.TempDir()
	ran := false
	if e := owned(root, func(string, worker.Config, *controllerLog) error { ran = true; return nil }); e == nil || ran {
		t.Fatalf("a folder without a configuration started a controller: %v", e)
	}
	b, _ := os.ReadFile(filepath.Join(root, "controller.log"))
	if !strings.Contains(string(b), "controller did not start: ") || strings.Contains(string(b), "started") {
		t.Fatalf("controller.log holds %q", b)
	}
	detach = func(*os.File) error { return errors.New("no standard error handle") }
	root = writeRoot(t, nil)
	if e := owned(root, func(string, worker.Config, *controllerLog) error { ran = true; return nil }); e != nil || !ran {
		t.Fatalf("a controller without a place for its crash report did not run: %v", e)
	}
	b, _ = os.ReadFile(filepath.Join(root, "controller.log"))
	if !strings.Contains(string(b), "no standard error handle") || !strings.Contains(string(b), "controller dev started\n") {
		t.Fatalf("controller.log holds %q", b)
	}
}

func TestEveryRefusedStartIsNamedInControllerLog(t *testing.T) {
	refused := func(root, why string) {
		t.Helper()
		ran := false
		if e := owned(root, func(string, worker.Config, *controllerLog) error { ran = true; return nil }); e == nil || ran {
			t.Fatalf("%s started a controller: %v", why, e)
		}
		b, _ := os.ReadFile(filepath.Join(root, "controller.log"))
		if !strings.HasSuffix(string(b), "\n") || !strings.Contains(string(b[max(0, len(b)-400):]), "controller did not start: ") {
			t.Fatalf("%s: controller.log ends with %q", why, b[max(0, len(b)-400):])
		}
	}
	root := writeRoot(t, nil)
	l, e := owner.Acquire(root)
	if e != nil {
		t.Fatal(e)
	}
	refused(root, "a folder another controller owns")
	l.Close()
	// A log over 1 MiB that cannot be moved aside: controller.log.1 is a folder with a file in it.
	root = writeRoot(t, nil)
	os.WriteFile(filepath.Join(root, "controller.log"), bytes.Repeat([]byte("earlier line\n"), 100000), 0600)
	os.MkdirAll(filepath.Join(root, "controller.log.1", "kept"), 0700)
	refused(root, "a log that cannot be opened")
}

type refusingStore struct {
	lifecycle.FileStore
	refuse bool
}

func (s *refusingStore) Save(i lifecycle.Intent) error {
	if s.refuse {
		return errors.New("disk refused")
	}
	return s.FileStore.Save(i)
}

func TestAnOffThatCloseCouldNotSaveIsNamedInControllerLog(t *testing.T) {
	store := &refusingStore{FileStore: lifecycle.FileStore{Path: filepath.Join(t.TempDir(), "intent.json")}}
	m, e := lifecycle.New(store, &restoredWindows{}, nil)
	if e != nil {
		t.Fatal(e)
	}
	m.Apply("follow-party")
	m.Apply("start")
	store.refuse = true
	if e = m.Apply("stop"); e == nil {
		t.Fatal("failed save reported success")
	}
	var out bytes.Buffer
	closing(m, &controllerLog{Logger: log.New(&out, "", 0)})
	if !strings.Contains(out.String(), "controller closed: Off was not saved") || !strings.Contains(out.String(), "disk refused") {
		t.Fatalf("controller.log holds %q", out.String())
	}
	out.Reset()
	closing(m, &controllerLog{Logger: log.New(&out, "", 0)})
	if out.Len() != 0 {
		t.Fatalf("a Close with nothing left to do logged %q", out.String())
	}
}
