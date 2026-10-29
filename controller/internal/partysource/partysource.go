// Package partysource follows a Home Assistant entity for Follow Party. The
// controller pulls, because its owner endpoint accepts loopback calls only.
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

type Config struct {
	URL       string
	TokenFile string
	Entity    string
	Value     string
	Poll      time.Duration
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
	command func(string) error
	logf    func(string, ...any)
	client  *http.Client
	after   func(time.Duration) <-chan time.Time
	wake    chan struct{}
	mu      sync.Mutex
	active  bool
	epoch   uint64
	status  Status
}

// New returns a source that sends party-on or party-off through command and
// reports to logf. It reads nothing and polls nothing before Run.
func New(c Config, command func(string) error, logf func(string, ...any)) *Source {
	if c.Poll <= 0 {
		c.Poll = 5 * time.Second
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Home Assistant redirect refused") }}
	return &Source{config: c, command: command, logf: logf, client: client, after: time.After, wake: make(chan struct{}, 1), status: idle(c.Entity)}
}

// ReadToken returns the one token in path. Home Assistant counts an empty or
// malformed bearer as a failed login and may ban the address, so those are
// errors here and never become a request.
func ReadToken(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", fmt.Errorf("Home Assistant token file unreadable: %w", e)
	}
	defer f.Close()
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

// SetActive tells the source whether Follow Party is the selected mode; it
// polls only then. Each activation applies its first reading even when
// unchanged, since the party state may have been set by hand meanwhile.
func (s *Source) SetActive(on bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	changed := s.active != on
	s.active = on
	if changed && on {
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

func (s *Source) Status() Status {
	if s == nil {
		return Status{State: "unconfigured", Summary: Unconfigured}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}
func (s *Source) set(st Status) {
	s.mu.Lock()
	s.status = st
	s.mu.Unlock()
}
func idle(entity string) Status {
	return Status{State: "idle", Entity: entity, Summary: "Manual mode; Home Assistant not polled"}
}

// Run polls until ctx ends. It returns early with an error only when it
// refuses the token: unreadable or empty, or rejected by Home Assistant.
// Failed readings keep the last party state and back off up to 60 s.
func (s *Source) Run(ctx context.Context) error {
	token, e := ReadToken(s.config.TokenFile)
	if e != nil {
		return s.refuse(e)
	}
	entity := s.config.Entity
	var epoch uint64
	var polling, known, party bool
	var failures int
	var delay time.Duration
	for {
		select {
		case <-s.wake:
		default:
		}
		s.mu.Lock()
		active, current := s.active, s.epoch
		s.mu.Unlock()
		if !active {
			if polling {
				s.logf("Follow Party: Manual mode; stopped polling %s", entity)
				polling = false
			}
			s.set(idle(entity))
			select {
			case <-ctx.Done():
				return nil
			case <-s.wake:
			}
			continue
		}
		if current != epoch {
			epoch, polling, known, failures, delay = current, true, false, 0, 0
			s.logf("Follow Party: polling Home Assistant %s every %s", entity, s.config.Poll)
			s.set(Status{State: "polling", Entity: entity, Summary: "polling " + entity})
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
		value, e := s.read(ctx, token)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(e, errRejected) {
			return s.refuse(e)
		}
		if e != nil {
			failures++
			delay = backoff(s.config.Poll, failures)
			s.logf("Follow Party: reading %s failed (%d in a row), retry in %s: %v", entity, failures, delay, e)
			s.set(Status{State: "failing", Entity: entity, Failures: failures, Error: e.Error(), Checked: time.Now().UTC(), Summary: fmt.Sprintf("%s unreadable %d times, retry in %s", entity, failures, delay)})
			continue
		}
		if failures > 0 {
			s.logf("Follow Party: %s readable again after %d failures", entity, failures)
		}
		failures, delay = 0, s.config.Poll
		on := value == s.config.Value
		if !known || on != party {
			command := "party-off"
			if on {
				command = "party-on"
			}
			if e = s.command(command); e != nil {
				s.logf("Follow Party: %s is %q but the controller refused %s: %v", entity, value, command, e)
			} else {
				s.logf("Follow Party: %s is %q; sent %s", entity, value, command)
				known, party = true, on
			}
		}
		word := "no party"
		if on {
			word = "party"
		}
		s.set(Status{State: "polling", Entity: entity, Value: value, Checked: time.Now().UTC(), Summary: fmt.Sprintf("%s is %s (%s)", entity, value, word)})
	}
}
func (s *Source) refuse(e error) error {
	s.logf("Follow Party: not polling Home Assistant: %v", e)
	s.set(Status{State: "refused", Entity: s.config.Entity, Error: e.Error(), Summary: "not polling: " + e.Error()})
	return e
}
func (s *Source) read(ctx context.Context, token string) (string, error) {
	r, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(s.config.URL, "/")+"/api/states/"+url.PathEscape(s.config.Entity), nil)
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
	if v.EntityID != s.config.Entity {
		return "", fmt.Errorf("Home Assistant answered for %q", v.EntityID)
	}
	if v.State == "" || v.State == "unavailable" || v.State == "unknown" {
		return "", fmt.Errorf("%s is %q", s.config.Entity, v.State)
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
