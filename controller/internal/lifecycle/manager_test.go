package lifecycle

import (
	"context"
	"errors"
	"path/filepath"
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
}

func (b *fakeBackend) Start(ctx context.Context) (Session, error) {
	if b.starting != nil {
		select {
		case b.starting <- struct{}{}:
		default:
		}
	}
	if b.gate != nil {
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
	for _, c := range []string{"suspend", "resume", "party-on", "follow-party", "restore"} {
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
func TestFollowPartyRequiresOptInAndExplicitStart(t *testing.T) {
	b := &fakeBackend{}
	m, _ := newManager(t, b)
	m.Apply("follow-party")
	m.Apply("party-on")
	eventually(t, func() bool { return m.Snapshot().Phase == "off" })
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	m.Apply("party-off")
	eventually(t, func() bool { return m.Snapshot().Phase == "waiting-for-party" })
	m.Apply("party-on")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
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
	if e := m.apply("stop", "late old completion", oldGeneration); e != nil {
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

func TestWindowRestoredAtSignInIsClosedAtStartupAndDoesNotBlockStart(t *testing.T) {
	b := &restoringBackend{restored: 1}
	m, store := newManager(t, b)
	m.Apply("restore")
	eventually(t, func() bool { return m.Snapshot().Phase == "off" && b.open() == 0 })
	if v, e := store.Load(); e != nil || !v.ManualOff || v.Requested || b.count() != 0 {
		t.Fatalf("closing the restored window changed Manual Off: %+v %v, %d sessions", v, e, b.count())
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

func TestStopClosesAWindowRestoredAtSignIn(t *testing.T) {
	b := &restoringBackend{}
	m, _ := newManager(t, b)
	m.Apply("start")
	eventually(t, func() bool { return m.Snapshot().Phase == "running" })
	m.Apply("stop")
	eventually(t, func() bool { return m.Snapshot().Phase == "off" })
	b.restore()
	m.Apply("stop")
	eventually(t, func() bool { return b.open() == 0 && m.Snapshot().Phase == "off" })
	if b.sweptWhileOwned() != 0 {
		t.Fatal("swept while a session was owned")
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

func TestStopWhileOffSweepsWithoutPublishing(t *testing.T) {
	b, l := &restoringBackend{}, &phaseLog{}
	m, e := New(FileStore{filepath.Join(t.TempDir(), "intent.json")}, b, l.record)
	if e != nil {
		t.Fatal(e)
	}
	m.Apply("restore")
	// Restore publishes stopping and off, in either order.
	eventually(t, func() bool { return len(l.get()) == 2 && m.Snapshot().Phase == "off" })
	before, generation, sweeps := l.get(), m.Snapshot().Generation, b.swept()
	for i := 0; i < 3; i++ {
		b.restore()
		if e = m.Apply("stop"); e != nil {
			t.Fatal(e)
		}
		eventually(t, func() bool { return b.open() == 0 })
	}
	if after := l.get(); len(after) != len(before) || m.Snapshot().Generation != generation || b.swept() != sweeps+3 {
		t.Fatalf("Stops while Off: phases %v -> %v, generation %d -> %d, sweeps %d -> %d", before, after, generation, m.Snapshot().Generation, sweeps, b.swept())
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
