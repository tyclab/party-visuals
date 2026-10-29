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
	stopErr  error
}

func (b Backend) Start(ctx context.Context) (lifecycle.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command(powerShell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", b.Config.WorkerScript, "-ConfigPath", b.ConfigPath)
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
			var v struct {
				Event  string `json:"event"`
				Reason string `json:"reason"`
			}
			if json.Unmarshal(scan.Bytes(), &v) != nil {
				continue
			}
			switch v.Event {
			case "ready":
				ready <- nil
			case "error":
				select {
				case ready <- errors.New(v.Reason):
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
func (s *session) Done() <-chan error { return s.done }
func (s *session) finish(err error) {
	s.done <- err
	close(s.exited)
}
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
		if err := s.job.Close(); err != nil {
			s.stopErr = err
		}
		select {
		case <-s.exited:
		case <-time.After(s.exitWait):
			s.stopErr = errors.New("owned worker did not exit")
		}
	})
	return s.stopErr
}

type job interface {
	Assign(*os.Process) error
	Close() error
}
