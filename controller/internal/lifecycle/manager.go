package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Intent struct {
	Version   int    `json:"version"`
	Mode      string `json:"mode"`
	ManualOff bool   `json:"manualOff"`
	Requested bool   `json:"requested"`
}

type Status struct {
	Intent     Intent `json:"intent"`
	Phase      string `json:"phase"`
	Reason     string `json:"reason,omitempty"`
	Generation uint64 `json:"generation"`
	Party      bool   `json:"party"`
	Suspended  bool   `json:"suspended"`
}

// Session is one running worker. Done delivers its exit error once, then closes.
type Session interface {
	Stop(context.Context) error
	Done() <-chan error
}
type Backend interface {
	Start(context.Context) (Session, error)
}

// Sweeper is a Backend that can close dedicated-browser windows Windows
// restored at sign-in. The controller starts none of them, so the manager
// calls it only while it owns no session: right before each Start, where
// start retries windows that could not be ended before, and from
// CloseRestored. No other state change sweeps.
type Sweeper interface {
	CloseRestored(ctx context.Context, start bool) error
}

// ErrRestoredOpen refuses Start while such a window is open. A later sweep
// that leaves none open returns a Start by hand to Off; a follow mode starts again.
var ErrRestoredOpen = errors.New("A browser window restored by Windows at sign-in is open; close it before Start.")

// Follows reports whether mode follows a source: Follow Party or Follow
// Lightshow. A follow mode keeps its Start across a new owner.
func Follows(mode string) bool { return mode == "follow-party" || mode == "follow-lightshow" }

func supported(v Intent) bool { return v.Version == 1 && (v.Mode == "manual" || Follows(v.Mode)) }

type Store interface {
	Load() (Intent, error)
	Save(Intent) error
}

type Manager struct {
	mu      sync.Mutex
	run     sync.Mutex
	backend Backend
	store   Store
	state   Status
	saved   Intent // what the store holds; differs after a Stop whose Off could not be saved
	partyBy string // the follow mode whose source set Party; empty when set by hand
	cancel  context.CancelFunc
	session Session
	changed func(Status)
	seq     uint64     // counts the states handed to notify; guarded by mu
	tell    sync.Mutex // orders calls of changed
	told    uint64     // seq of the newest state changed received; guarded by tell
	closed  bool
	wg      sync.WaitGroup
	life    context.Context // ends at Close, with any sweep in progress
	end     context.CancelFunc
	blocked uint64 // generation whose Start ErrRestoredOpen refused; guarded by run
}

func New(store Store, backend Backend, changed func(Status)) (*Manager, error) {
	intent, err := store.Load()
	if err != nil {
		return nil, err
	}
	if !supported(intent) {
		return nil, errors.New("unsupported controller intent; preserve the file and repair it before starting")
	}
	if intent.Requested && !Follows(intent.Mode) {
		intent.Requested, intent.ManualOff = false, true
		if err := store.Save(intent); err != nil {
			return nil, err
		}
	}
	life, end := context.WithCancel(context.Background())
	return &Manager{store: store, backend: backend, state: Status{Intent: intent, Phase: "off"}, saved: intent, changed: changed, life: life, end: end}, nil
}

func (m *Manager) Snapshot() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.state }

// notify hands changed the state numbered seq, in order: a state that a newer
// one overtook on its way here is dropped, so changed ends on the newest state.
// changed must not wait for a caller of Apply or Event: they wait for it.
func (m *Manager) notify(s Status, seq uint64) {
	if m.changed == nil {
		return
	}
	m.tell.Lock()
	defer m.tell.Unlock()
	if seq > m.told {
		m.told = seq
		m.changed(s)
	}
}

func (m *Manager) Apply(command string) error {
	return m.apply(command, "", 0, "")
}

// Quit is Stop for a controller about to end. An Off that could not be saved
// refuses it only in a follow mode, where the next controller acts on the Start
// still stored; in Manual mode no controller starts visuals on its own.
func (m *Manager) Quit() error {
	e := m.Apply("stop")
	if e != nil && !Follows(m.Snapshot().Intent.Mode) {
		return nil
	}
	return e
}

// Event applies a source's party-on or party-off only while the mode it reads
// for is selected, so a reading that lands after a mode switch changes nothing.
func (m *Manager) Event(mode, command string) error {
	if command != "party-on" && command != "party-off" {
		return errors.New("a source sends party-on or party-off only")
	}
	return m.apply(command, "", 0, mode)
}
func (m *Manager) apply(command, reason string, expectedGeneration uint64, forMode string) error {
	m.mu.Lock()
	if expectedGeneration != 0 && m.state.Generation != expectedGeneration {
		m.mu.Unlock()
		return nil
	}
	if forMode != "" && m.state.Intent.Mode != forMode {
		m.mu.Unlock()
		return fmt.Errorf("%s is no longer the selected mode", forMode)
	}
	if m.closed {
		m.mu.Unlock()
		return errors.New("controller is shutting down")
	}
	next, by := m.state, m.partyBy
	previousWant := m.wants(next)
	var persistenceError error
	switch command {
	case "start":
		next.Intent.Requested, next.Intent.ManualOff = true, false
	case "stop":
		next.Intent.Requested, next.Intent.ManualOff = false, true
	case "manual", "follow-party", "follow-lightshow":
		// The party state came from the other source, also by way of Manual:
		// only a session that runs or starts keeps it until the new one reads.
		from := next.Intent.Mode
		if !Follows(from) {
			from = m.partyBy
		}
		if Follows(command) && Follows(from) && command != from && next.Phase != "running" && next.Phase != "starting" {
			next.Party = false
		}
		next.Intent.Mode = command
	case "party-on", "party-off":
		next.Party = command == "party-on"
		m.partyBy = forMode
	case "suspend":
		next.Suspended = true
	case "resume":
		next.Suspended = false
	case "restore":
	default:
		m.mu.Unlock()
		return errors.New("unknown controller command")
	}
	if next.Intent != m.saved {
		err := m.store.Save(next.Intent)
		switch {
		case err == nil:
			m.saved = next.Intent
		case command == "stop":
			persistenceError = err
			reason = "Could not persist Off; visuals stopped: " + err.Error()
		case next.Intent == m.state.Intent:
			// The Off stays owed. This command does not fail over it; the status keeps saying so.
			if reason == "" {
				reason = "Could not persist Off; visuals stopped: " + err.Error()
			}
		default:
			m.mu.Unlock()
			return err
		}
	}
	next.Reason = reason
	if next == m.state && (m.partyBy == by || m.wants(next) == previousWant) && command != "restore" && !(command == "start" && m.state.Phase == "attention") {
		m.mu.Unlock()
		return persistenceError
	}
	m.state = next
	want := m.wants(next)
	if want && previousWant && (m.state.Phase == "running" || m.state.Phase == "starting") {
		m.seq++
		s, seq := m.state, m.seq
		m.mu.Unlock()
		m.notify(s, seq)
		return persistenceError
	}
	m.state.Generation++
	gen := m.state.Generation
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.state.Phase = "stopping"
	if want {
		m.state.Phase = "starting"
	}
	m.seq++
	s, seq := m.state, m.seq
	m.wg.Add(1)
	go func() { defer m.wg.Done(); m.reconcile(ctx, gen, want) }()
	m.mu.Unlock()
	m.notify(s, seq)
	return persistenceError
}

// wants reports whether s calls for visuals. A party state the other follow
// mode's source set only keeps a session that runs or starts, until this
// mode's first reading; it never starts one. Callers hold mu.
func (m *Manager) wants(s Status) bool {
	if !s.Intent.Requested || s.Intent.ManualOff || s.Suspended {
		return false
	}
	own := m.partyBy == "" || m.partyBy == s.Intent.Mode
	return s.Intent.Mode == "manual" || s.Party && (own || s.Phase == "running" || s.Phase == "starting")
}

// CloseRestored closes windows Windows restored at sign-in while no session is
// owned. The app calls it only in the first minutes after it started, when
// Windows restores them; later only the sweep before each Start runs. The
// sweep ends with ctx or Close, also while it waits for a window to close.
func (m *Manager) CloseRestored(ctx context.Context) {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	defer context.AfterFunc(m.life, stop)()
	m.run.Lock()
	defer m.run.Unlock()
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed || m.session != nil || ctx.Err() != nil {
		return
	}
	if err := m.closeRestored(ctx, false); err == nil && m.blocked != 0 {
		gen := m.blocked
		m.blocked = 0
		// A Start by hand returns to Off. A follow mode asked for its Start
		// itself, at sign-in when Windows restores windows, and tries it again.
		command := "stop"
		if Follows(m.Snapshot().Intent.Mode) {
			command = "restore"
		}
		_ = m.apply(command, "", gen, "")
	}
}
func (m *Manager) closeRestored(ctx context.Context, start bool) error {
	if s, ok := m.backend.(Sweeper); ok && m.session == nil {
		return s.CloseRestored(ctx, start)
	}
	return nil
}

// refuse publishes a refused Start and remembers one a restored window caused.
func (m *Manager) refuse(gen uint64, err error) {
	if errors.Is(err, ErrRestoredOpen) {
		m.blocked = gen
	}
	m.publish(gen, "attention", err.Error())
}

func (m *Manager) current(gen uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.Generation == gen && !m.closed
}
func (m *Manager) publish(gen uint64, phase, reason string) {
	m.mu.Lock()
	if m.state.Generation != gen || m.closed {
		m.mu.Unlock()
		return
	}
	m.state.Phase, m.state.Reason = phase, reason
	m.seq++
	s, seq := m.state, m.seq
	m.mu.Unlock()
	m.notify(s, seq)
}

// stop ends session; a failure counts only while the session has not ended,
// so a session that ended after an earlier failed stop is dropped, not kept.
func stop(session Session) error {
	if session == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	if err := session.Stop(ctx); err != nil {
		select {
		case <-session.Done():
		default:
			return err
		}
	}
	return nil
}

func (m *Manager) reconcile(ctx context.Context, gen uint64, want bool) {
	m.run.Lock()
	defer m.run.Unlock()
	if !m.current(gen) {
		return
	}
	m.blocked = 0
	if m.session != nil {
		if err := stop(m.session); err != nil {
			m.publish(gen, "attention", "Owned processes did not stop: "+err.Error())
			return
		}
		m.session = nil
	}
	if !m.current(gen) || ctx.Err() != nil {
		return
	}
	if !want {
		s := m.Snapshot()
		phase := "off"
		if s.Suspended {
			phase = "suspended"
		} else if s.Intent.Requested && !s.Intent.ManualOff {
			phase = "waiting-for-party"
		}
		if s.Reason != "" {
			phase = "attention"
		}
		m.publish(gen, phase, s.Reason)
		return
	}
	err := m.closeRestored(ctx, true)
	var session Session
	if err == nil {
		session, err = m.backend.Start(ctx)
	}
	if err != nil {
		if ctx.Err() == nil {
			m.refuse(gen, err)
		}
		return
	}
	if ctx.Err() != nil || !m.current(gen) {
		if err := stop(session); err != nil {
			m.session = session
		}
		return
	}
	m.session = session
	m.publish(gen, "running", "Check diagnostics for screen, audio and RGB readiness")
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		select {
		case <-ctx.Done():
			return
		case err := <-session.Done():
			reason := "Visualizer window closed; Start to resume"
			if err != nil {
				reason = "Visuals stopped unexpectedly; inspect diagnostics, then Start: " + err.Error()
			}
			_ = m.apply("stop", reason, gen, "")
		}
	}()
}

// Close ends the session and leaves the intent as it is: only Stop records
// Off, so an owner that ends on its own does not turn a follow mode off. An
// Off that could not be saved before is saved now.
func (m *Manager) Close() error {
	m.mu.Lock()
	var owed error
	if !m.closed && m.state.Intent != m.saved {
		if owed = m.store.Save(m.state.Intent); owed == nil {
			m.saved = m.state.Intent
		} else {
			owed = fmt.Errorf("Off was not saved, the stored intent still holds the Start: %w", owed)
		}
	}
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.end()
	m.mu.Unlock()
	m.run.Lock()
	err := stop(m.session)
	m.session = nil
	m.run.Unlock()
	m.wg.Wait()
	if owed != nil && err != nil {
		return fmt.Errorf("%w; %w", owed, err)
	}
	return errors.Join(owed, err)
}
