package partysource

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// PowerStatus is what the display power reader last saw. Reading is the
// settled on or off the worker opens with, unknown before the first reading.
type PowerStatus struct {
	State    string    `json:"state"`
	Entity   string    `json:"entity,omitempty"`
	Reading  string    `json:"reading"`
	Raw      string    `json:"raw,omitempty"`
	Failures int       `json:"failures,omitempty"`
	Error    string    `json:"error,omitempty"`
	Checked  time.Time `json:"checked,omitzero"`
	Summary  string    `json:"summary"`
}

// powerSettle is how long a new on or off must hold before it counts. The TV
// entity this was built for read off for 10 to 11 s at a time while the TV
// stayed on, four times on one evening.
const powerSettle = 30 * time.Second

// Power reads whether the preferred display is on from a Home Assistant
// entity, with Source's token file, ACL check and request, and its rules for
// rejected tokens and failed readings.
type Power struct {
	src     *Source
	logf    func(string, ...any)
	polled  func()
	after   func(time.Duration) <-chan time.Time
	now     func() time.Time
	mu      sync.Mutex
	reading string
	pending string
	since   time.Time
	status  PowerStatus
}

// NewPower returns a reader of c.Entity that calls polled after each reading
// attempt. It reads nothing before Run.
func NewPower(c Config, logf func(string, ...any), polled func()) *Power {
	p := &Power{src: New(c, nil, logf), logf: logf, polled: polled, after: time.After, now: time.Now, reading: "unknown"}
	p.status = PowerStatus{State: "idle", Entity: c.Entity, Reading: "unknown", Summary: "not read yet"}
	return p
}

// powerOf reads a state as on or off: off and standby are off, any other
// readable state is on.
func powerOf(state string) string {
	if state == "off" || state == "standby" {
		return "off"
	}
	return "on"
}

// Reading is the settled reading: on, off or unknown, and none without a reader.
func (p *Power) Reading() string {
	if p == nil {
		return "none"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reading
}
func (p *Power) Status() PowerStatus {
	if p == nil {
		return PowerStatus{State: "unconfigured", Reading: "none", Summary: "no display power entity configured"}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

// Run polls until ctx ends, or returns an error when it refuses the token.
// A failed reading keeps the reading and backs off up to 60 s.
func (p *Power) Run(ctx context.Context) error {
	entity := p.src.config.Entity
	token, e := ReadToken(p.src.config.TokenFile)
	if e != nil {
		return p.refuse(e)
	}
	p.logf("Display power: polling %s every %s", entity, p.src.config.Poll)
	var failures int
	for {
		state, e := p.src.read(ctx, token)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(e, errRejected) {
			return p.refuse(e)
		}
		delay := p.src.config.Poll
		if e != nil {
			failures++
			delay = backoff(p.src.config.Poll, failures)
			p.logf("Display power: reading %s failed (%d in a row), retry in %s: %v", entity, failures, delay, e)
			p.mu.Lock()
			p.status = PowerStatus{State: "failing", Entity: entity, Reading: p.reading, Failures: failures, Error: e.Error(), Checked: p.now().UTC(), Summary: fmt.Sprintf("%s unreadable %d times, keeping %s", entity, failures, p.reading)}
			p.mu.Unlock()
		} else {
			failures = 0
			p.observe(entity, state)
		}
		if p.polled != nil {
			p.polled()
		}
		select {
		case <-ctx.Done():
			return nil
		case <-p.after(delay):
		}
	}
}

// observe settles a reading: the first at once, a change once it held powerSettle.
func (p *Power) observe(entity, state string) {
	now := p.now()
	value := powerOf(state)
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.reading == "unknown" || value == p.reading:
		p.pending = ""
	case value != p.pending:
		p.pending, p.since = value, now
	case now.Sub(p.since) >= powerSettle:
		p.pending = ""
	}
	if p.pending == "" && value != p.reading {
		p.reading = value
		p.logf("Display power: %s reads %s (%s)", entity, value, state)
	}
	summary := fmt.Sprintf("%s is %s (%s)", entity, state, p.reading)
	if p.pending != "" {
		summary = fmt.Sprintf("%s is %s (%s until it holds %s)", entity, state, p.reading, powerSettle)
	}
	p.status = PowerStatus{State: "polling", Entity: entity, Reading: p.reading, Raw: state, Checked: now.UTC(), Summary: summary}
}
func (p *Power) refuse(e error) error {
	p.logf("Display power: not polling Home Assistant: %v", e)
	p.mu.Lock()
	p.status = PowerStatus{State: "refused", Entity: p.src.config.Entity, Reading: p.reading, Error: e.Error(), Summary: "not polling: " + e.Error()}
	p.mu.Unlock()
	return e
}
