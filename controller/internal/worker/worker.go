package worker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tyclab/party-visuals/controller/internal/lifecycle"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

type Backend struct {
	Config     Config
	ConfigPath string
	// Notice receives each closed or still open window Windows restored.
	Notice func(string)
	// Power reads the preferred display's power for each session; Placed
	// receives where each session opened the visuals.
	Power  func() string
	Placed func(Placement)
	mu     sync.Mutex
	stuck  map[browserID]bool
	placed Placement
}

const (
	gracefulStop = 45 * time.Second
	exitWait     = 5 * time.Second
)

type session struct {
	cmd      *exec.Cmd
	in       io.WriteCloser
	job      job
	done     chan error
	exited   chan struct{}
	grace    time.Duration
	exitWait time.Duration
	mu       sync.Mutex
	stopOnce sync.Once
	jobErr   error
}

func (b *Backend) Start(ctx context.Context) (lifecycle.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	shell, e := powerShell()
	if e != nil {
		return nil, e
	}
	power := b.power()
	b.place(Placement{Power: power})
	cmd := exec.Command(shell, b.command(power)...)
	hide(cmd)
	in, e := cmd.StdinPipe()
	if e != nil {
		return nil, e
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		return nil, e
	}
	cmd.Stderr = io.Discard
	j, e := newJob()
	if e != nil {
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		j.Close()
		return nil, e
	}
	s := &session{cmd: cmd, in: in, job: j, done: make(chan error, 1), exited: make(chan struct{}), grace: gracefulStop, exitWait: exitWait}
	if e = j.Assign(cmd.Process); e != nil {
		cmd.Process.Kill()
		cmd.Wait()
		j.Close()
		return nil, e
	}
	ready := make(chan error, 1)
	go func() {
		scan := bufio.NewScanner(out)
		scan.Buffer(make([]byte, 4096), 65536)
		for scan.Scan() {
			switch event, reason := b.event(scan.Bytes(), power); event {
			case "ready":
				ready <- nil
			case "error":
				e := errors.New(reason)
				if reason == lifecycle.ErrRestoredOpen.Error() {
					e = lifecycle.ErrRestoredOpen
				}
				select {
				case ready <- e:
				default:
				}
			}
		}
		err := cmd.Wait()
		s.finish(err)
		select {
		case ready <- fmt.Errorf("visuals worker ended before ready: %v", err):
		default:
		}
	}()
	if _, e = io.WriteString(in, "start\n"); e != nil {
		s.Stop(context.Background())
		return nil, e
	}
	timer := time.NewTimer(40 * time.Second)
	defer timer.Stop()
	select {
	case e = <-ready:
	case <-ctx.Done():
		e = ctx.Err()
	case <-timer.C:
		e = errors.New("visuals startup timed out")
	}
	if e != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		s.Stop(stopCtx)
		return nil, e
	}
	return s, nil
}

// event reads one worker line and records a display placement it reports.
func (b *Backend) event(line []byte, power string) (event, reason string) {
	var v struct {
		Event           string `json:"event"`
		Reason          string `json:"reason"`
		Text            string `json:"text"`
		PreferredListed bool   `json:"preferredListed"`
	}
	if json.Unmarshal(line, &v) != nil {
		return "", ""
	}
	if v.Event == "display" {
		p := Placement{Text: v.Text, PreferredListed: v.PreferredListed, Power: power}
		b.place(p)
		if b.Placed != nil {
			b.Placed(p)
		}
	}
	return v.Event, v.Reason
}
func (s *session) Done() <-chan error { return s.done }
func (s *session) finish(err error) {
	s.done <- err
	close(s.done)
	close(s.exited)
}

// Stop asks once for a graceful stop, then closes the job, ending every owned
// process. Every call then reports only whether the worker has exited, so a
// stop whose waits ran out, as across sleep, succeeds once the worker is gone.
func (s *session) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		_, _ = io.WriteString(s.in, "stop\n")
		_ = s.in.Close()
		s.mu.Unlock()
		timer := time.NewTimer(s.grace)
		defer timer.Stop()
		select {
		case <-s.exited:
		case <-ctx.Done():
		case <-timer.C:
		}
		s.jobErr = s.job.Close()
	})
	timer := time.NewTimer(s.exitWait)
	defer timer.Stop()
	select {
	case <-s.exited:
		return nil
	case <-timer.C:
	}
	select {
	case <-s.exited:
		return nil
	default:
		return errors.Join(s.jobErr, errors.New("owned worker did not exit"))
	}
}

type job interface {
	Assign(*os.Process) error
	Close() error
}
