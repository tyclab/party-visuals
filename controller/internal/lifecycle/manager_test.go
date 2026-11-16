package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSession struct {
	done    chan error
	once    sync.Once
	stopped chan struct{}
	stopErr error
}

func (s *fakeSession) Done() <-chan error { return s.done }
func (s *fakeSession) Stop(context.Context) error {
	s.once.Do(func() { close(s.stopped) })
	return s.stopErr
}
func (s *fakeSession) end(err error) { s.done <- err; close(s.done) }

type fakeBackend struct {
	mu       sync.Mutex
	sessions []*fakeSession
	gate     chan struct{}
	starting chan struct{}
	stopErr  error
	deaf     bool // Start waits for gate only: it finishes after its context ended
	refuse   error
	calls    int
}

func (b *fakeBackend) Start(ctx context.Context) (Session, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	if b.refuse != nil {
		return nil, b.refuse
	}
	if b.starting != nil {
		select {
		case b.starting <- struct{}{}:
		default:
		}
	}
	if b.gate != nil && b.deaf {
		<-b.gate
	} else if b.gate != nil {
		select {
		case <-b.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	b.mu.Lock()
	s := &fakeSession{done: make(chan error, 1), stopped: make(chan struct{}), stopErr: b.stopErr}
	b.sessions = append(b.sessions, s)
	b.mu.Unlock()
	return s, nil
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
func newManager(t *testing.T, b Backend) (*Manager, FileStore) {
	t.Helper()
	s := FileStore{filepath.Join(t.TempDir(), "intent.json")}
	m, e := New(s, b, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Close() })
	return m, s
}
func TestOffSurvivesTriggersAndNewOwner(t *testing.T) {
	b := &fakeBackend{}
	m, s := newManager(t, b)
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	m.Apply("stop")
	for _, c := range []string{"suspend", "resume", "party-on", "follow-party", "follow-lightshow", "restore"} {
		if e := m.Apply(c); e != nil {
			t.Fatal(e)
		}
	}
	eventually(t, func() bool { return m.Snapshot().Phase == "off" })
	next, e := New(s, b, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	next.Apply("restore")
	eventually(t, func() bool { return next.Snapshot().Phase == "off" })
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.sessions) != 1 {
		t.Fatalf("unexpected relaunch: %d", len(b.sessions))
	}
}
func TestStopCancelsPendingStart(t *testing.T) {
	b := &fakeBackend{gate: make(chan struct{}), starting: make(chan struct{}, 1)}
	m, _ := newManager(t, b)
	m.Apply("start")
	<-b.starting
	m.Apply("stop")
	close(b.gate)
	eventually(t, func() bool { return m.Snapshot().Phase == "off" })
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.sessions {
		select {
		case <-s.stopped:
		default:
			t.Fatal("late start survived Stop")
		}
	}
}
func TestAQuitWhileStartingLeavesNoSessionAndSavesOff(t *testing.T) {
	b := &fakeBackend{gate: make(chan struct{}), starting: make(chan struct{}, 1), deaf: true}
	store := FileStore{filepath.Join(t.TempDir(), "intent.json")}
	m, e := New(store, b, nil)
	if e != nil {
		t.Fatal(e)
	}
	m.Apply("start")
	<-b.starting
	if e = m.Quit(); e != nil {
		t.Fatal(e)
	}
	close(b.gate)
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	if b.count() != 1 {
		t.Fatalf("the start made %d sessions, not the one that finishes late", b.count())
	}
	for _, s := range b.sessions {
		select {
		case <-s.stopped:
		default:
			t.Fatal("a start that finished late survived the quit")
		}
	}
	if v, e := store.Load(); e != nil || !v.ManualOff || v.Requested {
		t.Fatalf("the store holds %+v, %v", v, e)
	}
}

func TestDuplicateStartDoesNotReplaceSession(t *testing.T) {
	b := &fakeBackend{}
	m, _ := newManager(t, b)
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	for i := 0; i < 20; i++ {
		m.Apply("start")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.sessions) != 1 {
		t.Fatal("duplicate workers")
	}
}
func TestUnknownBrowserExitPersistsOff(t *testing.T) {
	b := &fakeBackend{}
	m, s := newManager(t, b)
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	b.mu.Lock()
	session := b.sessions[0]
	b.mu.Unlock()
	session.done <- errors.New("browser ended")
	eventually(t, func() bool { return m.Snapshot().Intent.ManualOff })
	v, e := s.Load()
	if e != nil || !v.ManualOff || v.Requested {
		t.Fatalf("Off not durable: %+v %v", v, e)
	}
}

var followModes = []string{"follow-party", "follow-lightshow"}

func TestFollowModesRequireOptInAndExplicitStart(t *testing.T) {
	for _, mode := range followModes {
		t.Run(mode, func(t *testing.T) {
			b := &fakeBackend{}
			m, _ := newManager(t, b)
			m.Apply(mode)
			m.Apply("party-on")
			eventually(t, func() bool { return m.Snapshot().Phase == "off" })
			m.Apply("start")
			eventually(t, func() bool { return m.Snapshot().Phase == "running" })
			m.Apply("party-off")
			eventually(t, func() bool { return m.Snapshot().Phase == "waiting-for-party" })
			m.Apply("party-on")
			eventually(t, func() bool { return m.Snapshot().Phase == "running" })
		})
	}
}
func TestCorruptStateFailsClosed(t *testing.T) {
	s := FileStore{filepath.Join(t.TempDir(), "intent.json")}
	AtomicWrite(s.Path, []byte("not json"))
	if _, e := New(s, &fakeBackend{}, nil); e == nil {
		t.Fatal("corrupt state accepted")
	}
}

func TestLeftoverOnAfterOwnerLossRequiresStart(t *testing.T) {
	store := FileStore{Path: filepath.Join(t.TempDir(), "intent.json")}
	if e := store.Save(Intent{Version: 1, Mode: "manual", Requested: true}); e != nil {
		t.Fatal(e)
	}
	b := &fakeBackend{}
	m, e := New(store, b, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.Apply("restore")
	eventually(t, func() bool { return m.Snapshot().Phase == "off" })
	if !m.Snapshot().Intent.ManualOff {
		t.Fatal("ambiguous previous owner silently resumed")
	}
}
func TestAFollowModesStartSurvivesANewOwner(t *testing.T) {
	for _, mode := range followModes {
		t.Run(mode, func(t *testing.T) {
			store := FileStore{Path: filepath.Join(t.TempDir(), "intent.json")}
			if e := store.Save(Intent{Version: 1, Mode: mode, Requested: true}); e != nil {
				t.Fatal(e)
			}
			b := &fakeBackend{}
			m, e := New(store, b, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			m.Apply("restore")
			eventually(t, func() bool { return m.Snapshot().Phase == "waiting-for-party" })
			if v, e := store.Load(); e != nil || !v.Requested || v.ManualOff || v.Mode != mode {
				t.Fatalf("a new owner dropped the Start that %s had: %+v, %v", mode, v, e)
			}
			if b.count() != 0 {
				t.Fatal("visuals started before the source asked")
			}
			m.Apply("party-on")
			eventually(t, func() bool { return m.Snapshot().Phase == "running" })
		})
	}
}
func TestCloseEndsTheSessionAndKeepsTheIntent(t *testing.T) {
	for _, mode := range followModes {
		t.Run(mode, func(t *testing.T) {
			b := &fakeBackend{}
			m, store := newManager(t, b)
			m.Apply(mode)
			m.Apply("start")
			m.Apply("party-on")
			eventually(t, func() bool { return m.Snapshot().Phase == "running" })
			if e := m.Close(); e != nil {
				t.Fatal(e)
			}
			b.mu.Lock()
			s := b.sessions[0]
			b.mu.Unlock()
			select {
			case <-s.stopped:
			default:
				t.Fatal("Close left the worker running")
			}
			if v, e := store.Load(); e != nil || !v.Requested || v.ManualOff || v.Mode != mode {
				t.Fatalf("Close recorded an Off nobody asked for: %+v, %v", v, e)
			}
		})
	}
}
func TestAnOffThatCouldNotBeSavedIsSavedLater(t *testing.T) {
	for name, later := range map[string]func(*Manager){
		"by the next command": func(m *Manager) { m.Apply("party-off") },
		"by Close":            func(m *Manager) { m.Close() },
	} {
		t.Run(name, func(t *testing.T) {
			store := &failingStore{FileStore: FileStore{Path: filepath.Join(t.TempDir(), "intent.json")}}
			m, e := New(store, &fakeBackend{}, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			m.Apply("follow-party")
			m.Apply("start")
			m.Apply("party-on")
			eventually(t, func() bool { return m.Snapshot().Phase == "running" })
			store.fail = true
			if e = m.Apply("stop"); e == nil {
				t.Fatal("failed save reported success")
			}
			if e = m.Apply("party-on"); e != nil {
				t.Fatalf("a party event was refused over the Off still owed: %v", e)
			}
			store.fail = false
			later(m)
			if v, e := store.Load(); e != nil || !v.ManualOff || v.Requested {
				t.Fatalf("the Off stayed unsaved, so the next owner would start visuals: %+v, %v", v, e)
			}
		})
	}
}
func TestAFollowModeStartsOnceTheRestoredWindowThatRefusedItIsClosed(t *testing.T) {
	for _, mode := range followModes {
		t.Run(mode, func(t *testing.T) {
			b := &restoringBackend{lateRestore: true}
			m, store := newManager(t, b)
			m.Apply(mode)
			m.Apply("start")
			m.Apply("party-on")
			eventually(t, func() bool { return m.Snapshot().Reason == ErrRestoredOpen.Error() })
			m.CloseRestored(context.Background())
			eventually(t, func() bool { return m.Snapshot().Phase == "running" && b.open() == 0 })
			if v, e := store.Load(); e != nil || !v.Requested || v.ManualOff {
				t.Fatalf("the window Windows restored cost %s its Start: %+v, %v", mode, v, e)
			}
		})
	}
}
func TestPartyEventsDoNotRestartManualSession(t *testing.T) {
	b := &fakeBackend{}
	m, _ := newManager(t, b)
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	for _, c := range []string{"party-on", "party-off", "party-on"} {
		m.Apply(c)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.sessions) != 1 {
		t.Fatal("irrelevant party event replaced manual session")
	}
}

type failingStore struct {
	FileStore
	fail bool
}

func (s *failingStore) Save(i Intent) error {
	if s.fail {
		return errors.New("disk refused")
	}
	return s.FileStore.Save(i)
}
func TestStopStillEndsSessionWhenPersistenceFails(t *testing.T) {
	store := &failingStore{FileStore: FileStore{Path: filepath.Join(t.TempDir(), "intent.json")}}
	b := &fakeBackend{}
	m, e := New(store, b, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	store.fail = true
	if e = m.Apply("stop"); e == nil {
		t.Fatal("failed save reported success")
	}
	eventually(t, func() bool { return m.Snapshot().Phase == "attention" })
	b.mu.Lock()
	s := b.sessions[0]
	b.mu.Unlock()
	select {
	case <-s.stopped:
	default:
		t.Fatal("Stop left worker running after disk failure")
	}
	if !m.Snapshot().Intent.ManualOff {
		t.Fatal("runtime Off lost")
	}
}

// owedOff runs a Follow Party session and stops it while the store refuses.
func owedOff(t *testing.T) (*Manager, *failingStore) {
	t.Helper()
	store := &failingStore{FileStore: FileStore{Path: filepath.Join(t.TempDir(), "intent.json")}}
	m, e := New(store, &fakeBackend{}, nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Close() })
	m.Apply("follow-party")
	m.Apply("start")
	m.Apply("party-on")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	store.fail = true
	if e = m.Apply("stop"); e == nil {
		t.Fatal("failed save reported success")
	}
	return m, store
}

func settled(t *testing.T, m *Manager) Status {
	t.Helper()
	eventually(t, func() bool { p := m.Snapshot().Phase; return p != "stopping" && p != "starting" })
	return m.Snapshot()
}

func TestAnOffStillOwedIsSaidUntilItIsSaved(t *testing.T) {
	m, store := owedOff(t)
	owed := func(after string) {
		t.Helper()
		if s := settled(t, m); s.Phase != "attention" || !strings.HasPrefix(s.Reason, "Could not persist Off") {
			t.Fatalf("after %s the Off still owed is shown as %q %q", after, s.Phase, s.Reason)
		}
	}
	owed("the Stop")
	if e := m.Apply("stop"); e == nil {
		t.Fatal("a second Stop whose Off was not saved either reported success")
	}
	owed("a second Stop")
	if e := m.Apply("manual"); e == nil || m.Snapshot().Intent.Mode != "follow-party" {
		t.Fatalf("a mode that could not be saved was taken: %v, %+v", e, m.Snapshot().Intent)
	}
	if e := m.Apply("party-off"); e != nil {
		t.Fatalf("a party event was refused over the Off still owed: %v", e)
	}
	owed("a party event")
	store.fail = false
	if e := m.Apply("party-on"); e != nil {
		t.Fatal(e)
	}
	if s := settled(t, m); s.Phase != "off" || s.Reason != "" {
		t.Fatalf("the Off, now saved, is shown as %q %q", s.Phase, s.Reason)
	}
	if v, e := store.Load(); e != nil || !v.ManualOff || v.Requested || v.Mode != "follow-party" {
		t.Fatalf("the store holds %+v, %v", v, e)
	}
}

func TestCloseSaysThatTheOffItOwesWasNotSaved(t *testing.T) {
	m, _ := owedOff(t)
	if e := m.Close(); e == nil || !strings.Contains(e.Error(), "Off was not saved") || !strings.Contains(e.Error(), "disk refused") {
		t.Fatalf("Close of a controller that owes an Off returned %v", e)
	}
	if e := m.Close(); e != nil {
		t.Fatalf("a second Close returned %v", e)
	}
}

func TestCloseNamesAnUnsavedOffAndAWorkerThatDidNotStopOnOneLine(t *testing.T) {
	store := &failingStore{FileStore: FileStore{Path: filepath.Join(t.TempDir(), "intent.json")}}
	m, e := New(store, &fakeBackend{stopErr: errors.New("owned worker did not exit")}, nil)
	if e != nil {
		t.Fatal(e)
	}
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	store.fail = true
	m.Apply("stop")
	e = m.Close()
	if e == nil || !strings.Contains(e.Error(), "Off was not saved") || !strings.Contains(e.Error(), "owned worker did not exit") || strings.Contains(e.Error(), "\n") {
		t.Fatalf("Close returned %q", e)
	}
}

func TestAQuitIsRefusedOverAnUnsavedOffOnlyInAFollowMode(t *testing.T) {
	for mode, refused := range map[string]bool{"manual": false, "follow-party": true, "follow-lightshow": true} {
		t.Run(mode, func(t *testing.T) {
			store := &failingStore{FileStore: FileStore{Path: filepath.Join(t.TempDir(), "intent.json")}}
			b := &fakeBackend{}
			m, e := New(store, b, nil)
			if e != nil {
				t.Fatal(e)
			}
			m.Apply(mode)
			m.Apply("start")
			m.Apply("party-on")
			eventually(t, func() bool { return m.Snapshot().Phase == "running" })
			store.fail = true
			for _, attempt := range []string{"first", "second"} {
				if e = m.Quit(); (e != nil) != refused {
					t.Fatalf("the %s quit answered %v", attempt, e)
				}
			}
			m.Close()
			select {
			case <-b.sessions[0].stopped:
			default:
				t.Fatal("the quit left the visuals running")
			}
			// Why the two differ: what the next controller does with the Start still stored.
			store.fail = false
			next, e := New(store, &fakeBackend{}, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer next.Close()
			if i := next.Snapshot().Intent; i.Requested != refused {
				t.Fatalf("the next controller holds %+v", i)
			}
		})
	}
}

func TestStaleSessionCompletionCannotStopNewStart(t *testing.T) {
	b := &fakeBackend{}
	m, store := newManager(t, b)
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	oldGeneration := m.Snapshot().Generation
	if !m.current(oldGeneration) {
		t.Fatal("old completion must initially observe itself current")
	}
	m.Apply("stop")
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" && m.Snapshot().Generation != oldGeneration })
	before := m.Snapshot()
	if e := m.apply("stop", "late old completion", oldGeneration, ""); e != nil {
		t.Fatal(e)
	}
	after := m.Snapshot()
	persisted, e := store.Load()
	if e != nil {
		t.Fatal(e)
	}
	if after != before || persisted.ManualOff || !persisted.Requested {
		t.Fatalf("stale completion changed newer intent: before=%+v after=%+v persisted=%+v", before, after, persisted)
	}
	b.mu.Lock()
	current := b.sessions[len(b.sessions)-1]
	b.mu.Unlock()
	select {
	case <-current.stopped:
		t.Fatal("stale completion stopped newer worker")
	default:
	}
}

func (b *fakeBackend) count() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.sessions) }

func TestFailedStopBlocksANewWorkerOnlyUntilTheOldOneEnds(t *testing.T) {
	b := &fakeBackend{stopErr: errors.New("owned worker did not exit")}
	m, _ := newManager(t, b)
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	m.Apply("suspend")
	eventually(t, func() bool { return strings.Contains(m.Snapshot().Reason, "did not stop") })
	for _, c := range []string{"resume", "start"} {
		m.Apply(c)
		eventually(t, func() bool { return m.Snapshot().Phase == "attention" })
		if b.count() != 1 {
			t.Fatalf("%s started a second worker beside one that may still run", c)
		}
	}
	b.mu.Lock()
	first := b.sessions[0]
	b.mu.Unlock()
	first.end(nil)
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	if b.count() != 2 {
		t.Fatalf("expected one fresh worker, have %d sessions", b.count())
	}
}

func TestResumeStartsFreshAfterASuspendStopFailedAndTheWorkerEnded(t *testing.T) {
	b := &fakeBackend{stopErr: errors.New("owned worker did not exit")}
	m, _ := newManager(t, b)
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	m.Apply("suspend")
	eventually(t, func() bool { return m.Snapshot().Phase == "attention" })
	b.mu.Lock()
	b.sessions[0].end(nil)
	b.mu.Unlock()
	m.Apply("resume")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" && b.count() == 2 })
}

func TestStopOfAnEndedSessionIsNotAnError(t *testing.T) {
	b := &fakeBackend{stopErr: errors.New("process already gone")}
	m, _ := newManager(t, b)
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	b.mu.Lock()
	b.sessions[0].end(nil)
	b.mu.Unlock()
	eventually(t, func() bool { return m.Snapshot().Intent.ManualOff && m.Snapshot().Phase == "attention" })
	if r := m.Snapshot().Reason; strings.Contains(r, "did not stop") {
		t.Fatalf("ended session reported as still running: %s", r)
	}
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" && b.count() == 2 })
}

// restoringBackend is a backend on a PC where Windows can restore the kiosk
// at sign-in: the worker refuses Start while such a window is open, as
// Run-PartyVisualsSession.ps1 does.
type restoringBackend struct {
	fakeBackend
	sweep       sync.Mutex
	restored    int
	stuck       bool
	sweeps      int
	starts      []bool
	owned       int
	startErr    error
	lateRestore bool
}

func (b *restoringBackend) Start(ctx context.Context) (Session, error) {
	b.sweep.Lock()
	late, err := b.lateRestore, b.startErr
	if late {
		// A window Windows restored between the sweep and the worker's own check.
		b.restored, b.lateRestore = b.restored+1, false
	}
	b.sweep.Unlock()
	if late {
		return nil, ErrRestoredOpen
	}
	if err != nil {
		return nil, err
	}
	if b.open() > 0 {
		return nil, errors.New("The dedicated browser is already open outside this session; close it before Start.")
	}
	return b.fakeBackend.Start(ctx)
}
func (b *restoringBackend) CloseRestored(_ context.Context, start bool) error {
	b.mu.Lock()
	for _, s := range b.sessions {
		select {
		case <-s.stopped:
		default:
			b.owned++
		}
	}
	b.mu.Unlock()
	b.sweep.Lock()
	defer b.sweep.Unlock()
	b.sweeps++
	b.starts = append(b.starts, start)
	if b.restored > 0 && b.stuck {
		return ErrRestoredOpen
	}
	b.restored = 0
	return nil
}
func (b *restoringBackend) restore()   { b.sweep.Lock(); b.restored++; b.sweep.Unlock() }
func (b *restoringBackend) swept() int { b.sweep.Lock(); defer b.sweep.Unlock(); return b.sweeps }
func (b *restoringBackend) open() int  { b.sweep.Lock(); defer b.sweep.Unlock(); return b.restored }
func (b *restoringBackend) sweptWhileOwned() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.owned
}

func TestWindowRestoredAtSignInIsClosedByTheSweepAndDoesNotBlockStart(t *testing.T) {
	b := &restoringBackend{restored: 1}
	m, store := newManager(t, b)
	m.Apply("restore")
	eventually(t, func() bool { return m.Snapshot().Phase == "off" })
	if b.swept() != 0 {
		t.Fatalf("restoring the intent swept %d times", b.swept())
	}
	// The app's sweep in its first minutes after sign-in.
	m.CloseRestored(context.Background())
	if v, e := store.Load(); e != nil || !v.ManualOff || v.Requested || b.count() != 0 || b.open() != 0 {
		t.Fatalf("closing the restored window changed Manual Off: %+v %v, %d sessions, %d open", v, e, b.count(), b.open())
	}
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	if b.count() != 1 || b.sweptWhileOwned() != 0 {
		t.Fatalf("%d sessions, %d sweeps beside an owned session", b.count(), b.sweptWhileOwned())
	}
	b.sweep.Lock()
	defer b.sweep.Unlock()
	if last := len(b.starts) - 1; last < 1 || !b.starts[last] || b.starts[last-1] {
		t.Fatalf("only the sweep before Start may retry windows that could not be ended: %v", b.starts)
	}
}

func TestWindowRestoredAfterTheControllerStartedIsClosedWhileOff(t *testing.T) {
	// The show PC: Windows reopened the kiosk 7 s after the controller started.
	b := &restoringBackend{}
	m, store := newManager(t, b)
	m.Apply("restore")
	eventually(t, func() bool { return m.Snapshot().Phase == "off" })
	b.restore()
	m.CloseRestored(context.Background())
	if b.open() != 0 {
		t.Fatal("restored window left open while Manual Off")
	}
	if v, _ := store.Load(); !v.ManualOff || b.count() != 0 || m.Snapshot().Phase != "off" {
		t.Fatalf("sweep changed the state: %+v, phase %s, %d sessions", v, m.Snapshot().Phase, b.count())
	}
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
}

func TestNoStateChangeButStartSweeps(t *testing.T) {
	b := &restoringBackend{}
	m, _ := newManager(t, b)
	settled := func() bool { p := m.Snapshot().Phase; return p != "starting" && p != "stopping" }
	m.Apply("restore")
	eventually(t, settled)
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	m.Apply("stop")
	eventually(t, settled)
	b.restore()
	for _, c := range []string{"stop", "suspend", "resume", "follow-party", "party-on", "party-off", "manual", "stop"} {
		if e := m.Apply(c); e != nil {
			t.Fatalf("%s: %v", c, e)
		}
		eventually(t, settled)
	}
	if b.swept() != 1 || b.open() != 1 {
		t.Fatalf("%d sweeps, %d windows open; only the Start may have swept", b.swept(), b.open())
	}
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" && b.open() == 0 })
	b.sweep.Lock()
	starts := append([]bool{}, b.starts...)
	b.sweep.Unlock()
	if len(starts) != 2 || !starts[0] || !starts[1] || b.sweptWhileOwned() != 0 {
		t.Fatalf("sweeps %v, %d beside an owned session", starts, b.sweptWhileOwned())
	}
}

func TestNoSweepWhileASessionIsOwned(t *testing.T) {
	b := &restoringBackend{}
	m, _ := newManager(t, b)
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	before := b.swept()
	b.restore()
	m.CloseRestored(context.Background())
	if after := b.swept(); after != before || b.open() != 1 {
		t.Fatalf("swept beside an owned session: sweeps %d -> %d", before, after)
	}
}

func TestARestoredWindowThatStaysOpenIsNamedWhenStartIsRefused(t *testing.T) {
	b := &restoringBackend{restored: 1, stuck: true}
	m, _ := newManager(t, b)
	m.Apply("restore")
	eventually(t, func() bool { return m.Snapshot().Phase == "off" })
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "attention" })
	if r := m.Snapshot().Reason; r != ErrRestoredOpen.Error() || b.count() != 0 {
		t.Fatalf("reason %q, %d sessions", r, b.count())
	}
}

type phaseLog struct {
	mu     sync.Mutex
	phases []string
}

func (l *phaseLog) record(s Status) { l.mu.Lock(); l.phases = append(l.phases, s.Phase); l.mu.Unlock() }
func (l *phaseLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.phases...)
}

func TestStopWhileOffPublishesAndSweepsNothing(t *testing.T) {
	b, l := &restoringBackend{}, &phaseLog{}
	m, e := New(FileStore{filepath.Join(t.TempDir(), "intent.json")}, b, l.record)
	if e != nil {
		t.Fatal(e)
	}
	m.Apply("restore")
	// Restore publishes stopping, then off; an off that overtook stopping drops it.
	eventually(t, func() bool { p := l.get(); return len(p) > 0 && p[len(p)-1] == "off" && m.Snapshot().Phase == "off" })
	before, generation := l.get(), m.Snapshot().Generation
	b.restore()
	for i := 0; i < 3; i++ {
		if e = m.Apply("stop"); e != nil {
			t.Fatal(e)
		}
	}
	if after := l.get(); len(after) != len(before) || m.Snapshot().Generation != generation || b.swept() != 0 || b.open() != 1 {
		t.Fatalf("Stops while Off: phases %v -> %v, generation %d -> %d, %d sweeps", before, after, generation, m.Snapshot().Generation, b.swept())
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	if after := l.get(); len(after) != len(before) || m.Snapshot().Phase != "off" {
		t.Fatalf("Close while Off published %v", after[len(before):])
	}
}

func TestARefusedStartReturnsToOffOnceNoRestoredWindowIsLeft(t *testing.T) {
	t.Run("the window that refused the close is gone", func(t *testing.T) {
		b := &restoringBackend{restored: 1, stuck: true}
		m, store := newManager(t, b)
		m.Apply("start")
		eventually(t, func() bool { return m.Snapshot().Phase == "attention" })
		m.CloseRestored(context.Background())
		if s := m.Snapshot(); s.Phase != "attention" || s.Reason != ErrRestoredOpen.Error() {
			t.Fatalf("cleared while the window is still open: %+v", s)
		}
		b.sweep.Lock()
		b.restored = 0
		b.sweep.Unlock()
		m.CloseRestored(context.Background())
		eventually(t, func() bool { s := m.Snapshot(); return s.Phase == "off" && s.Reason == "" })
		if v, _ := store.Load(); !v.ManualOff || v.Requested || b.count() != 0 {
			t.Fatalf("not back to Manual Off: %+v, %d sessions", v, b.count())
		}
	})
	t.Run("the worker refused Start and the sweep closed the window", func(t *testing.T) {
		b := &restoringBackend{lateRestore: true}
		m, _ := newManager(t, b)
		m.Apply("start")
		eventually(t, func() bool { return m.Snapshot().Reason == ErrRestoredOpen.Error() })
		m.CloseRestored(context.Background())
		eventually(t, func() bool { s := m.Snapshot(); return s.Phase == "off" && s.Reason == "" && b.open() == 0 })
	})
	t.Run("another refusal stays", func(t *testing.T) {
		b := &restoringBackend{startErr: errors.New("NodeCG port is already in use outside this controller; no second instance was started.")}
		m, _ := newManager(t, b)
		m.Apply("start")
		eventually(t, func() bool { return m.Snapshot().Phase == "attention" })
		m.CloseRestored(context.Background())
		if s := m.Snapshot(); s.Phase != "attention" || !strings.Contains(s.Reason, "NodeCG port") {
			t.Fatalf("an unrelated refusal was cleared: %+v", s)
		}
	})
}

// slowSweep is a backend whose close waits like a restored Chrome that
// ignores WM_CLOSE: until its context ends.
type slowSweep struct {
	fakeBackend
	started chan struct{}
	calls   chan struct{}
}

func (b *slowSweep) CloseRestored(ctx context.Context, _ bool) error {
	b.calls <- struct{}{}
	close(b.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestCloseEndsASweepInProgress(t *testing.T) {
	b := &slowSweep{started: make(chan struct{}), calls: make(chan struct{}, 2)}
	m, e := New(FileStore{filepath.Join(t.TempDir(), "intent.json")}, b, nil)
	if e != nil {
		t.Fatal(e)
	}
	swept := make(chan struct{})
	go func() { m.CloseRestored(context.Background()); close(swept) }()
	<-b.started
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()
	select {
	case e = <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close waited for a sweep whose caller's context was still live")
	}
	<-swept
	if e != nil || len(b.calls) != 1 {
		t.Fatalf("Close: %v, %d sweeps", e, len(b.calls))
	}
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	if m.CloseRestored(ended); len(b.calls) != 1 {
		t.Fatal("a sweep ran after Close")
	}
}

func TestASweepWhoseContextEndedLooksForNothing(t *testing.T) {
	b := &restoringBackend{restored: 1}
	m, _ := newManager(t, b)
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	m.CloseRestored(ended)
	if b.swept() != 0 || b.open() != 1 {
		t.Fatalf("%d sweeps, %d windows open", b.swept(), b.open())
	}
}

// following runs mode with a Start and the source's party-on until visuals run.
func following(t *testing.T, b Backend, mode string) (*Manager, FileStore) {
	t.Helper()
	m, store := newManager(t, b)
	m.Apply(mode)
	m.Apply("start")
	m.Apply("party-on")
	return m, store
}

func TestASwitchBetweenTheFollowModesLeavesARunningSessionToTheNewSource(t *testing.T) {
	for i, from := range followModes {
		to := followModes[1-i]
		t.Run(from, func(t *testing.T) {
			b := &fakeBackend{}
			m, store := following(t, b, from)
			eventually(t, func() bool { return m.Snapshot().Phase == "running" })
			if e := m.Apply(to); e != nil {
				t.Fatal(e)
			}
			if s := settled(t, m); s.Phase != "running" || s.Intent.Mode != to || b.count() != 1 {
				t.Fatalf("the switch to %s left %+v with %d sessions", to, s, b.count())
			}
			select {
			case <-b.sessions[0].stopped:
				t.Fatal("the switch stopped visuals no source asked to stop")
			default:
			}
			if v, e := store.Load(); e != nil || v.Mode != to || !v.Requested {
				t.Fatalf("the store holds %+v, %v", v, e)
			}
			if e := m.Event(to, "party-off"); e != nil {
				t.Fatal(e)
			}
			if s := settled(t, m); s.Phase != "waiting-for-party" {
				t.Fatalf("the new source's party-off left %q", s.Phase)
			}
		})
	}
}

func TestASwitchBetweenTheFollowModesDoesNotRestartAStartInProgress(t *testing.T) {
	b := &fakeBackend{gate: make(chan struct{}), starting: make(chan struct{}, 1)}
	m, _ := following(t, b, "follow-party")
	<-b.starting
	m.Apply("follow-lightshow")
	close(b.gate)
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	if calls := func() int { b.mu.Lock(); defer b.mu.Unlock(); return b.calls }(); calls != 1 || b.count() != 1 {
		t.Fatalf("the switch restarted the start: %d starts, %d sessions", calls, b.count())
	}
}

func TestAfterASwitchNothingStartsBeforeTheNewSourceSaysSo(t *testing.T) {
	for name, before := range map[string]func(*Manager){
		"Off, then Start":       func(m *Manager) { m.Apply("stop") },
		"suspended, then wake":  func(m *Manager) { m.Apply("suspend") },
		"a refused Start stays": func(*Manager) {},
	} {
		t.Run(name, func(t *testing.T) {
			b := &fakeBackend{refuse: errors.New("NodeCG port is already in use outside this controller")}
			m, _ := following(t, b, "follow-party")
			eventually(t, func() bool { return m.Snapshot().Phase == "attention" })
			before(m)
			settled(t, m)
			b.mu.Lock()
			b.refuse, b.calls = nil, 0
			b.mu.Unlock()
			m.Apply("follow-lightshow")
			m.Apply("start")
			m.Apply("resume")
			m.CloseRestored(context.Background())
			if s := settled(t, m); s.Phase == "running" || b.count() != 0 {
				t.Fatalf("visuals started on Follow Party's reading after the switch: %+v, %d sessions", s, b.count())
			}
			m.Event("follow-lightshow", "party-on")
			eventually(t, func() bool { return m.Snapshot().Phase == "running" })
		})
	}
}

func TestASourceEventCountsOnlyWhileItsModeIsSelected(t *testing.T) {
	b := &fakeBackend{}
	m, _ := newManager(t, b)
	m.Apply("follow-lightshow")
	m.Apply("start")
	settled(t, m)
	for _, mode := range []string{"follow-party", "manual"} {
		if e := m.Event(mode, "party-on"); e == nil {
			t.Fatalf("a reading for %s was taken in Follow Lightshow", mode)
		}
	}
	if s := settled(t, m); s.Phase != "waiting-for-party" || s.Party || b.count() != 0 {
		t.Fatalf("a late reading of the other source changed %+v", s)
	}
	for _, c := range []string{"start", "stop", "follow-party"} {
		if e := m.Event("follow-lightshow", c); e == nil {
			t.Fatalf("a source sent %q", c)
		}
	}
	if e := m.Event("follow-lightshow", "party-on"); e != nil {
		t.Fatal(e)
	}
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
}

func TestTheStoreTakesTheThreeModesOnly(t *testing.T) {
	s := FileStore{filepath.Join(t.TempDir(), "intent.json")}
	for _, mode := range []string{"manual", "follow-party", "follow-lightshow"} {
		if e := s.Save(Intent{Version: 1, Mode: mode, ManualOff: true}); e != nil {
			t.Fatal(e)
		}
		if v, e := s.Load(); e != nil || v.Mode != mode {
			t.Fatalf("%s: %+v, %v", mode, v, e)
		}
	}
	AtomicWrite(s.Path, []byte(`{"version":1,"mode":"follow-something","requested":true}`))
	if _, e := New(s, &fakeBackend{}, nil); e == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

// v015Starts is how release v0.1.5 reads intent.json (lifecycle.FileStore.Load
// and New at tag v0.1.5): whether it would run the stored Start.
func v015Starts(b []byte) (starts bool, refused bool) {
	var v struct {
		Version   int    `json:"version"`
		Mode      string `json:"mode"`
		ManualOff bool   `json:"manualOff"`
		Requested bool   `json:"requested"`
	}
	if json.Unmarshal(b, &v) != nil || v.Version != 1 || (v.Mode != "manual" && v.Mode != "follow-party") {
		return false, true
	}
	return v.Requested && v.Mode == "follow-party", false
}

func TestAnIntentInFollowLightshowIsRefusedByAnEarlierRelease(t *testing.T) {
	store := FileStore{filepath.Join(t.TempDir(), "intent.json")}
	m, e := New(store, &fakeBackend{}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	for _, c := range []string{"follow-lightshow", "start", "party-on", "stop", "start"} {
		m.Apply(c)
		b, e := os.ReadFile(store.Path)
		if e != nil {
			t.Fatal(e)
		}
		if starts, refused := v015Starts(b); starts || !refused {
			t.Fatalf("after %q v0.1.5 would read %s as starts=%v refused=%v", c, b, starts, refused)
		}
	}
}

func TestAfterASwitchTheSignInSweepDoesNotRetryTheOtherSourcesStart(t *testing.T) {
	b := &restoringBackend{lateRestore: true}
	m, _ := following(t, b, "follow-party")
	eventually(t, func() bool { return m.Snapshot().Reason == ErrRestoredOpen.Error() })
	m.Apply("follow-lightshow")
	settled(t, m)
	m.CloseRestored(context.Background())
	if s := settled(t, m); s.Phase != "waiting-for-party" || b.count() != 0 {
		t.Fatalf("the sweep retried Follow Party's Start in Follow Lightshow: %+v, %d sessions", s, b.count())
	}
	m.Event("follow-lightshow", "party-on")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
}

func TestAReadingDoesNotCarryThroughManualIntoTheOtherFollowMode(t *testing.T) {
	for name, before := range map[string]func(*Manager){
		"Off, then Start":      func(m *Manager) { m.Apply("stop") },
		"suspended, then wake": func(m *Manager) { m.Apply("suspend") },
	} {
		t.Run(name, func(t *testing.T) {
			b := &fakeBackend{}
			m, _ := newManager(t, b)
			m.Apply("follow-party")
			m.Apply("start")
			if e := m.Event("follow-party", "party-on"); e != nil {
				t.Fatal(e)
			}
			eventually(t, func() bool { return m.Snapshot().Phase == "running" })
			before(m)
			settled(t, m)
			m.Apply("manual")
			m.Apply("follow-lightshow")
			m.Apply("start")
			m.Apply("resume")
			if s := settled(t, m); s.Phase == "running" || s.Party || b.count() != 1 {
				t.Fatalf("visuals started on Follow Party's reading after Manual: %+v, %d sessions", s, b.count())
			}
			m.Event("follow-lightshow", "party-on")
			eventually(t, func() bool { return m.Snapshot().Phase == "running" })
		})
	}
}

func TestTheLastStateHandedOutIsTheNewest(t *testing.T) {
	var mu sync.Mutex
	var modes []string
	release := make(chan struct{})
	m, e := New(FileStore{filepath.Join(t.TempDir(), "intent.json")}, &fakeBackend{}, func(s Status) {
		if s.Phase == "running" && s.Intent.Mode == "follow-party" {
			<-release
		}
		mu.Lock()
		modes = append(modes, s.Intent.Mode)
		mu.Unlock()
	})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Close() })
	m.Apply("follow-party")
	m.Apply("start")
	m.Event("follow-party", "party-on")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	switched := make(chan struct{})
	go func() { m.Apply("follow-lightshow"); close(switched) }()
	eventually(t, func() bool { return m.Snapshot().Intent.Mode == "follow-lightshow" })
	time.Sleep(20 * time.Millisecond)
	close(release)
	<-switched
	m.mu.Lock()
	m.seq++
	stale, seq := m.state, m.seq
	m.mu.Unlock()
	stale.Intent.Mode = "follow-party"
	m.Apply("follow-party")
	m.Apply("follow-lightshow")
	m.notify(stale, seq)
	mu.Lock()
	defer mu.Unlock()
	if last := modes[len(modes)-1]; last != "follow-lightshow" {
		t.Fatalf("the last state handed out was in %s, the manager is in follow-lightshow: %v", last, modes)
	}
	if !slices.Equal(modes[len(modes)-4:], []string{"follow-party", "follow-lightshow", "follow-party", "follow-lightshow"}) {
		t.Fatalf("states out of order: %v", modes)
	}
}

func TestAReadingKeptForARunningSessionStartsNothingOnceThatSessionEnded(t *testing.T) {
	for name, after := range map[string]func(*Manager, *fakeBackend){
		"Stop, then Start": func(m *Manager, _ *fakeBackend) { m.Apply("stop"); settled(t, m); m.Apply("start") },
		"sleep, then wake": func(m *Manager, _ *fakeBackend) { m.Apply("suspend"); settled(t, m); m.Apply("resume") },
		"window closed, Start": func(m *Manager, b *fakeBackend) {
			b.sessions[0].end(nil)
			eventually(t, func() bool { return m.Snapshot().Phase != "running" })
			settled(t, m)
			m.Apply("start")
		},
		"Manual, Stop, then Start": func(m *Manager, _ *fakeBackend) {
			m.Apply("manual")
			m.Apply("follow-lightshow")
			m.Apply("stop")
			settled(t, m)
			m.Apply("start")
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := &fakeBackend{}
			m, _ := newManager(t, b)
			m.Apply("follow-party")
			m.Apply("start")
			m.Event("follow-party", "party-on")
			eventually(t, func() bool { return m.Snapshot().Phase == "running" })
			m.Apply("follow-lightshow")
			if s := settled(t, m); s.Phase != "running" || b.count() != 1 {
				t.Fatalf("the switch did not keep the running session: %+v", s)
			}
			after(m, b)
			if s := settled(t, m); s.Phase == "running" || b.count() != 1 {
				t.Fatalf("visuals started again on Follow Party's reading: %+v, %d sessions", s, b.count())
			}
			m.Event("follow-lightshow", "party-on")
			eventually(t, func() bool { return m.Snapshot().Phase == "running" })
		})
	}
}

func TestTheSignInSweepDoesNotRetryAStartTheOtherSourceBeganBeforeTheSwitch(t *testing.T) {
	b := &restoringBackend{lateRestore: true}
	m, _ := newManager(t, b)
	m.Apply("follow-party")
	m.Apply("start")
	b.sweep.Lock()
	m.Event("follow-party", "party-on")
	if p := m.Snapshot().Phase; p != "starting" {
		b.sweep.Unlock()
		t.Fatalf("phase %s", p)
	}
	m.Apply("follow-lightshow")
	b.sweep.Unlock()
	eventually(t, func() bool { return m.Snapshot().Reason == ErrRestoredOpen.Error() })
	m.CloseRestored(context.Background())
	if s := settled(t, m); s.Phase == "running" || b.count() != 0 {
		t.Fatalf("the sweep retried Follow Party's Start in Follow Lightshow: %+v, %d sessions", s, b.count())
	}
	m.Event("follow-lightshow", "party-on")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
}
