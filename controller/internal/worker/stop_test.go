package worker

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeJob struct {
	mu     sync.Mutex
	closes int
}

func (j *fakeJob) Assign(*os.Process) error { return nil }
func (j *fakeJob) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.closes++
	return nil
}
func (j *fakeJob) closed() int { j.mu.Lock(); defer j.mu.Unlock(); return j.closes }

type fakeStdin struct {
	mu     sync.Mutex
	text   strings.Builder
	closed bool
}

func (f *fakeStdin) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return 0, io.ErrClosedPipe
	}
	return f.text.Write(b)
}
func (f *fakeStdin) Close() error { f.mu.Lock(); defer f.mu.Unlock(); f.closed = true; return nil }

func testSession(grace, wait time.Duration) (*session, *fakeJob, *fakeStdin) {
	j, in := &fakeJob{}, &fakeStdin{}
	return &session{in: in, job: j, done: make(chan error, 1), exited: make(chan struct{}), grace: grace, exitWait: wait}, j, in
}

func TestStopSucceedsWhenWorkerExitsAfterItsWaitsRanOut(t *testing.T) {
	// As across sleep: the grace period is over at wake, and the worker outlives the wait after its job is closed.
	s, j, in := testSession(time.Millisecond, 10*time.Millisecond)
	if e := s.Stop(context.Background()); e == nil {
		t.Fatal("stop reported success while the worker was still running")
	}
	if in.text.String() != "stop\n" || !in.closed || j.closed() != 1 {
		t.Fatalf("graceful stop then job close expected: stdin %q closed=%v, job closes %d", in.text.String(), in.closed, j.closed())
	}
	s.exitWait = 10 * time.Second
	result := make(chan error, 1)
	go func() { result <- s.Stop(context.Background()) }()
	s.finish(errors.New("exit status 1"))
	select {
	case e := <-result:
		if e != nil {
			t.Fatalf("worker exited but stop still fails: %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not observe the worker's exit")
	}
	if j.closed() != 1 {
		t.Fatalf("job closed %d times", j.closed())
	}
}

func TestStopOfAWorkerThatIsAlreadyGoneIsNotAnError(t *testing.T) {
	t.Run("after an earlier stop failed", func(t *testing.T) {
		s, j, _ := testSession(time.Millisecond, time.Millisecond)
		if e := s.Stop(context.Background()); e == nil {
			t.Fatal("stop reported success while the worker was still running")
		}
		s.finish(nil)
		for i := 0; i < 3; i++ {
			if e := s.Stop(context.Background()); e != nil {
				t.Fatalf("stop %d after the worker exited: %v", i+2, e)
			}
		}
		if j.closed() != 1 {
			t.Fatalf("job closed %d times", j.closed())
		}
	})
	t.Run("before any stop, without waiting out the grace period", func(t *testing.T) {
		s, j, _ := testSession(time.Hour, time.Hour)
		s.finish(errors.New("browser ended"))
		start := time.Now()
		if e := s.Stop(context.Background()); e != nil {
			t.Fatal(e)
		}
		if time.Since(start) > 5*time.Second || j.closed() != 1 {
			t.Fatalf("took %v, job closed %d times", time.Since(start), j.closed())
		}
	})
	t.Run("when its waits ran out too", func(t *testing.T) {
		// A sleep can leave the exit and an expired wait ready together; the exit decides.
		for i := 0; i < 64; i++ {
			s, _, _ := testSession(0, 0)
			s.finish(nil)
			if e := s.Stop(context.Background()); e != nil {
				t.Fatalf("run %d: %v", i, e)
			}
		}
	})
}
