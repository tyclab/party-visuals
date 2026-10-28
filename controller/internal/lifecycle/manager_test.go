package lifecycle

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeSession struct {
	done    chan error
	once    sync.Once
	stopped chan struct{}
}

func (s *fakeSession) Done() <-chan error         { return s.done }
func (s *fakeSession) Stop(context.Context) error { s.once.Do(func() { close(s.stopped) }); return nil }

type fakeBackend struct {
	mu       sync.Mutex
	sessions []*fakeSession
	gate     chan struct{}
	starting chan struct{}
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
	s := &fakeSession{done: make(chan error, 1), stopped: make(chan struct{})}
	b.mu.Lock()
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
func newManager(t *testing.T, b *fakeBackend) (*Manager, FileStore) {
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
