// Package partysource follows a Home Assistant entity for each follow mode,
// one poller for all of them, so only the selected mode's entity is read and a
// rejected token stops every mode. The controller pulls, because its owner
// endpoint accepts loopback calls only.
package partysource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Target is the entity a follow mode reads: party-on while its state equals Value.
type Target struct {
	Mode   string
	Name   string
	Entity string
	Value  string
}

type Config struct {
	URL       string
	TokenFile string
	Poll      time.Duration
	Targets   []Target
}

type Status struct {
	State    string    `json:"state"`
	Entity   string    `json:"entity,omitempty"`
	Value    string    `json:"value,omitempty"`
	Failures int       `json:"failures,omitempty"`
	Error    string    `json:"error,omitempty"`
	Checked  time.Time `json:"checked,omitzero"`
	Summary  string    `json:"summary"`
}

const (
	Unconfigured = "no Home Assistant source configured"
	maxBackoff   = 60 * time.Second
)

var errRejected = errors.New("Home Assistant rejected the token")

type Source struct {
	config  Config
	command func(mode, command string) error
	logf    func(string, ...any)
	client  *http.Client
	after   func(time.Duration) <-chan time.Time
	wake    chan struct{}
	mu      sync.Mutex
	mode    string
	epoch   uint64
	status  Status
	of      string // the mode status describes
	refusal string
}

// New returns a source that sends party-on or party-off with the mode it read
// for through command and reports to logf. It reads nothing before Run.
func New(c Config, command func(mode, command string) error, logf func(string, ...any)) *Source {
	if c.Poll <= 0 {
		c.Poll = 5 * time.Second
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Home Assistant redirect refused") }}
	return &Source{config: c, command: command, logf: logf, client: client, after: time.After, wake: make(chan struct{}, 1)}
}

// ReadToken returns the one token in path. Home Assistant counts an empty or
// malformed bearer as a failed login and may ban the address, so those are
// errors here and never become a request. A file other accounts can open is
// refused before it is read.
func ReadToken(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", fmt.Errorf("Home Assistant token file unreadable: %w", e)
	}
	defer f.Close()
	if e = checkTokenACL(path); e != nil {
		return "", e
	}
	b, e := io.ReadAll(io.LimitReader(f, 4097))
	if e != nil {
		return "", e
	}
	token := strings.TrimSpace(strings.TrimPrefix(string(b), "\xef\xbb\xbf"))
	if len(b) > 4096 || token == "" || strings.ContainsFunc(token, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return "", errors.New("Home Assistant token file must hold exactly one token")
	}
	return token, nil
}

// Follow tells the source the selected mode; it polls only a mode with a
// target. Each change applies the first reading even when unchanged, since
// the party state came from another source or was set by hand meanwhile.
func (s *Source) Follow(mode string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	changed := s.mode != mode
	if changed {
		s.mode = mode
		s.epoch++
	}
	s.mu.Unlock()
	if changed {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// Status is what the source does for mode.
func (s *Source) Status(mode string) Status {
	t, ok := s.target(mode)
	if !ok {
		return Status{State: "unconfigured", Summary: Unconfigured}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.refusal != "":
		return Status{State: "refused", Entity: t.Entity, Error: s.refusal, Summary: "not polling: " + s.refusal}
	case s.of == mode:
		return s.status
	}
	return Status{State: "idle", Entity: t.Entity, Summary: "not selected; Home Assistant not polled"}
}
func (s *Source) target(mode string) (Target, bool) {
	if s != nil {
		for _, t := range s.config.Targets {
			if t.Mode == mode {
				return t, true
			}
		}
	}
	return Target{}, false
}
func (s *Source) set(mode string, st Status) {
	s.mu.Lock()
	s.status, s.of = st, mode
	s.mu.Unlock()
}

// Run polls until ctx ends. It returns early with an error only when it
// refuses the token: unreadable or empty, or rejected by Home Assistant.
// Failed readings keep the last party state and back off up to 60 s.
func (s *Source) Run(ctx context.Context) error {
	token, e := ReadToken(s.config.TokenFile)
	if e != nil {
		return s.refuse(e)
	}
	var polled *Target
	var epoch uint64
	var known, party bool
	var failures int
	var delay time.Duration
	for {
		select {
		case <-s.wake:
		default:
		}
		s.mu.Lock()
		mode, current := s.mode, s.epoch
		s.mu.Unlock()
		t, ok := s.target(mode)
		if polled != nil && (!ok || polled.Mode != t.Mode) {
			s.logf("%s: not selected; stopped polling %s", polled.Name, polled.Entity)
			polled = nil
		}
		if !ok {
			s.set("", Status{})
			select {
			case <-ctx.Done():
				return nil
			case <-s.wake:
			}
			continue
		}
		entity := t.Entity
		if current != epoch {
			epoch, polled, known, failures, delay = current, &t, false, 0, 0
			s.logf("%s: polling Home Assistant %s every %s", t.Name, entity, s.config.Poll)
			s.set(t.Mode, Status{State: "polling", Entity: entity, Summary: "polling " + entity})
		}
		if delay > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-s.wake:
				continue
			case <-s.after(delay):
			}
		}
		value, e := s.read(ctx, token, entity)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(e, errRejected) {
			return s.refuse(e)
		}
		if e != nil {
			failures++
			delay = backoff(s.config.Poll, failures)
			s.logf("%s: reading %s failed (%d in a row), retry in %s: %v", t.Name, entity, failures, delay, e)
			s.set(t.Mode, Status{State: "failing", Entity: entity, Failures: failures, Error: e.Error(), Checked: time.Now().UTC(), Summary: fmt.Sprintf("%s unreadable %d times, retry in %s", entity, failures, delay)})
			continue
		}
		if failures > 0 {
			s.logf("%s: %s readable again after %d failures", t.Name, entity, failures)
		}
		failures, delay = 0, s.config.Poll
		on := value == t.Value
		if !known || on != party {
			command := "party-off"
			if on {
				command = "party-on"
			}
			if e = s.command(t.Mode, command); e != nil {
				s.logf("%s: %s is %q but the controller refused %s: %v", t.Name, entity, value, command, e)
			} else {
				s.logf("%s: %s is %q; sent %s", t.Name, entity, value, command)
				known, party = true, on
			}
		}
		word := "no party"
		if on {
			word = "party"
		}
		s.set(t.Mode, Status{State: "polling", Entity: entity, Value: value, Checked: time.Now().UTC(), Summary: fmt.Sprintf("%s is %s (%s)", entity, value, word)})
	}
}
func (s *Source) refuse(e error) error {
	var names []string
	for _, t := range s.config.Targets {
		names = append(names, t.Name)
	}
	s.logf("%s: not polling Home Assistant: %v", strings.Join(names, " and "), e)
	s.mu.Lock()
	s.refusal = e.Error()
	s.mu.Unlock()
	return e
}
func (s *Source) read(ctx context.Context, token, entity string) (string, error) {
	r, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(s.config.URL, "/")+"/api/states/"+url.PathEscape(entity), nil)
	if e != nil {
		return "", e
	}
	r.Header.Set("Authorization", "Bearer "+token)
	res, e := s.client.Do(r)
	if e != nil {
		return "", e
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("%w (%s); fix the token file and restart the controller", errRejected, res.Status)
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Home Assistant answered %s", res.Status)
	}
	var v struct {
		EntityID string `json:"entity_id"`
		State    string `json:"state"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&v); e != nil {
		return "", fmt.Errorf("unreadable Home Assistant state: %w", e)
	}
	if v.EntityID != entity {
		return "", fmt.Errorf("Home Assistant answered for %q", v.EntityID)
	}
	if v.State == "" || v.State == "unavailable" || v.State == "unknown" {
		return "", fmt.Errorf("%s is %q", entity, v.State)
	}
	return v.State, nil
}
func backoff(poll time.Duration, failures int) time.Duration {
	d := poll
	for i := 0; i < failures && d < maxBackoff; i++ {
		d *= 2
	}
	return min(d, maxBackoff)
}
