//go:build windows

package worker

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestChromeRestartCommandLinesParseAsForeignRestored(t *testing.T) {
	const profile = `C:\Users\Show User\AppData\Local\PartyVisuals\browser-profile`
	for _, line := range []string{
		// Chrome registers its own switches for the restart, quoting only the value.
		`"C:\Program Files\Google\Chrome\Application\chrome.exe" --user-data-dir="C:\Users\Show User\AppData\Local\PartyVisuals\browser-profile" --remote-debugging-port=0 --app=http://music.example.invalid:8095/#/now-playing?player=test&frameless=1 --restore-last-session --restart`,
		// The launcher quotes each whole argument.
		`"C:\Program Files\Google\Chrome\Application\chrome.exe" "--user-data-dir=C:\Users\Show User\AppData\Local\PartyVisuals\browser-profile\\" "--start-fullscreen" --restore-last-session`,
	} {
		args, e := windows.DecomposeCommandLine(line)
		if e != nil || !foreignRestored(process{Session: 3, Args: args}, profile, 3) {
			t.Errorf("not foreign-restored: %q -> %q, %v", line, args, e)
		}
	}
}

type fixtureProcess struct {
	done chan struct{}
	code int
}

func startFixture(t *testing.T, exe string, args ...string) *fixtureProcess {
	t.Helper()
	c := exec.Command(exe, args...)
	in, _ := c.StdinPipe()
	out, _ := c.StdoutPipe()
	if e := c.Start(); e != nil {
		t.Fatal(e)
	}
	p := &fixtureProcess{done: make(chan struct{})}
	t.Cleanup(func() { c.Process.Kill(); in.Close(); <-p.done })
	ready := make(chan struct{})
	go func() {
		s := bufio.NewScanner(out)
		for s.Scan() {
			if s.Text() == "ready" {
				close(ready)
			}
		}
		c.Wait()
		p.code = c.ProcessState.ExitCode()
		close(p.done)
	}()
	for _, a := range args {
		if a == "--test-window" {
			select {
			case <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("fixture window did not open")
			}
		}
	}
	return p
}

// ended reports p's exit code once it ends within wait, or ok=false.
func (p *fixtureProcess) ended(wait time.Duration) (code int, ok bool) {
	select {
	case <-p.done:
		return p.code, true
	case <-time.After(wait):
		return 0, false
	}
}

func fixtureChrome(t *testing.T) string {
	t.Helper()
	child := os.Getenv("PARTY_TEST_CHILD")
	if child == "" {
		t.Skip("the native fixture binary must be supplied by Windows CI")
	}
	raw, e := os.ReadFile(child)
	if e != nil {
		t.Fatal(e)
	}
	exe := filepath.Join(t.TempDir(), "chrome.exe")
	if e = os.WriteFile(exe, raw, 0700); e != nil {
		t.Fatal(e)
	}
	return exe
}

func TestCloseRestoredEndsOnlyTheDedicatedKioskWindowsRestored(t *testing.T) {
	chrome := fixtureChrome(t)
	profile, other := filepath.Join(t.TempDir(), "browser-profile"), filepath.Join(t.TempDir(), "Default")
	saved := restoredCloseWait
	restoredCloseWait = 2 * time.Second
	defer func() { restoredCloseWait = saved }()
	withWindow := startFixture(t, chrome, "--test-window", "--user-data-dir="+profile, "--restore-last-session", "--restart")
	windowless := startFixture(t, chrome, "--user-data-dir="+profile, "--restore-last-session")
	owned := startFixture(t, chrome, "--user-data-dir="+profile, "--app=http://music.example.invalid/")
	otherProfile := startFixture(t, chrome, "--test-window", "--user-data-dir="+other, "--restore-last-session", "--restart")
	normal := startFixture(t, chrome, "--restore-last-session", "--restart")
	var mu sync.Mutex
	var notices []string
	b := Backend{Config: Config{BrowserProfile: profile}, Notice: func(s string) { mu.Lock(); notices = append(notices, s); mu.Unlock() }}
	if e := b.CloseRestored(context.Background()); e != nil {
		t.Fatal(e)
	}
	if code, ok := withWindow.ended(5 * time.Second); !ok || code != 0 {
		t.Fatalf("restored kiosk with a window: ended=%v code=%d, want closed through its window (0)", ok, code)
	}
	if code, ok := windowless.ended(5 * time.Second); !ok || code != 1 {
		t.Fatalf("restored kiosk without a window: ended=%v code=%d, want ended after the wait (1)", ok, code)
	}
	for name, p := range map[string]*fixtureProcess{"kiosk without the restore flag": owned, "another profile": otherProfile, "normal Chrome": normal} {
		if _, ok := p.ended(100 * time.Millisecond); ok {
			t.Errorf("%s was ended", name)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(notices) != 2 || !strings.HasPrefix(notices[0], RestoredClosed) || !strings.HasPrefix(notices[1], RestoredClosed) {
		t.Fatalf("notices %q", notices)
	}
	if e := b.CloseRestored(context.Background()); e != nil || len(notices) != 2 {
		t.Fatalf("a second sweep: %v, notices %q", e, notices)
	}
}
