package lifecycle

import (
	"context"
	"errors"
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

type Session interface {
	Stop(context.Context) error
	Done() <-chan error
}
type Backend interface {
	Start(context.Context) (Session, error)
}
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
	cancel  context.CancelFunc
	session Session
	changed func(Status)
	closed  bool
	wg      sync.WaitGroup
}

func New(store Store, backend Backend, changed func(Status)) (*Manager, error) {
	intent, err := store.Load()
	if err != nil {
		return nil, err
	}
	if intent.Version != 1 || (intent.Mode != "manual" && intent.Mode != "follow-party") {
		return nil, errors.New("unsupported controller intent; preserve the file and repair it before starting")
	}
	if intent.Requested {
		intent.Requested, intent.ManualOff = false, true
		if err := store.Save(intent); err != nil {
			return nil, err
		}
	}
	return &Manager{store: store, backend: backend, state: Status{Intent: intent, Phase: "off"}, changed: changed}, nil
}

func (m *Manager) Snapshot() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.state }
func (m *Manager) notify(s Status) {
	if m.changed != nil {
		m.changed(s)
	}
}

func (m *Manager) Apply(command string) error {
	return m.apply(command, "")
}
func (m *Manager) apply(command, reason string) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("controller is shutting down")
	}
	next := m.state
	previousWant := next.Intent.Requested && !next.Intent.ManualOff && !next.Suspended && (next.Intent.Mode == "manual" || next.Party)
	var persistenceError error
	switch command {
	case "start":
		next.Intent.Requested, next.Intent.ManualOff = true, false
	case "stop":
		next.Intent.Requested, next.Intent.ManualOff = false, true
	case "manual":
		next.Intent.Mode = "manual"
	case "follow-party":
		next.Intent.Mode = "follow-party"
	case "party-on":
		next.Party = true
	case "party-off":
		next.Party = false
	case "suspend":
		next.Suspended = true
	case "resume":
		next.Suspended = false
	case "restore":
	default:
		m.mu.Unlock()
		return errors.New("unknown controller command")
	}
	if next.Intent != m.state.Intent {
		if err := m.store.Save(next.Intent); err != nil {
			if command != "stop" {
				m.mu.Unlock()
				return err
			}
			persistenceError = err
			reason = "Could not persist Off; visuals stopped: " + err.Error()
		}
	}
	next.Reason = reason
	if next == m.state && command != "restore" && !(command == "start" && m.state.Phase == "attention") {
		m.mu.Unlock()
		return nil
	}
	m.state = next
	want := next.Intent.Requested && !next.Intent.ManualOff && !next.Suspended && (next.Intent.Mode == "manual" || next.Party)
	if want && previousWant && m.state.Phase == "running" {
		s := m.state
		m.mu.Unlock()
		m.notify(s)
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
	s := m.state
	m.wg.Add(1)
	go func() { defer m.wg.Done(); m.reconcile(ctx, gen, want) }()
	m.mu.Unlock()
	m.notify(s)
	return persistenceError
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
	s := m.state
	m.mu.Unlock()
	m.notify(s)
}

func stop(session Session) error {
	if session == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	return session.Stop(ctx)
}

func (m *Manager) reconcile(ctx context.Context, gen uint64, want bool) {
	m.run.Lock()
	defer m.run.Unlock()
	if !m.current(gen) {
		return
	}
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
	session, err := m.backend.Start(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.publish(gen, "attention", err.Error())
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
			if !m.current(gen) {
				return
			}
			reason := "Visualizer window closed; Start to resume"
			if err != nil {
				reason = "Visuals stopped unexpectedly; inspect diagnostics, then Start: " + err.Error()
			}
			_ = m.apply("stop", reason)
		}
	}()
}

func (m *Manager) Close() error {
	persistErr := m.Apply("stop")
	m.mu.Lock()
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.run.Lock()
	err := stop(m.session)
	m.session = nil
	m.run.Unlock()
	m.wg.Wait()
	return errors.Join(persistErr, err)
}
