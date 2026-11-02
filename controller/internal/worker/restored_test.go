package worker

import (
	"context"
	"errors"
	"github.com/tyclab/party-visuals/controller/internal/lifecycle"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// kioskArgs is the command line Get-BrowserArguments gives the dedicated browser.
func kioskArgs(profile, visualizer string) []string {
	return []string{`C:\Program Files\Google\Chrome\Application\chrome.exe`, "--user-data-dir=" + profile, "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", "--disable-backgrounding-occluded-windows", "--disable-renderer-backgrounding", "--disable-background-timer-throttling", "--no-first-run", "--no-default-browser-check", "--start-fullscreen", "--app=" + visualizer}
}

const testVisualizer = "http://music.example.invalid:8095/#/now-playing?player=test&frameless=1"

func TestOnlyTheDedicatedProfileRestoredAtSignInIsForeignRestored(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "PartyVisuals", "browser-profile")
	other := filepath.Join(t.TempDir(), "other-profile")
	restored := append(kioskArgs(profile, testVisualizer), "--restore-last-session", "--restart")
	with := func(args []string, extra ...string) []string { return append(append([]string{}, args...), extra...) }
	for _, c := range []struct {
		name    string
		session uint32
		args    []string
		want    bool
	}{
		{"the kiosk Windows restored at sign-in", 1, restored, true},
		{"the profile spelt in another case, with a trailing separator", 1, with(kioskArgs(strings.ToUpper(profile)+string(filepath.Separator), testVisualizer), "--restore-last-session"), true},
		{"the kiosk without --restore-last-session, as the controller opens it", 1, kioskArgs(profile, testVisualizer), false},
		{"normal Chrome restored at sign-in", 1, []string{"chrome.exe", "--restore-last-session", "--restart"}, false},
		{"another profile restored at sign-in", 1, with(kioskArgs(other, testVisualizer), "--restore-last-session"), false},
		{"a subprocess of the restored kiosk", 1, with(restored, "--type=renderer"), false},
		{"the restored kiosk of another session", 2, restored, false},
		{"a second user data directory", 1, with(restored, "--user-data-dir="+other), false},
	} {
		if got := foreignRestored(process{Session: c.session, Args: c.args}, profile, 1); got != c.want {
			t.Errorf("%s: foreignRestored = %v, want %v", c.name, got, c.want)
		}
	}
	if foreignRestored(process{Session: 1, Args: restored}, "", 1) {
		t.Error("an empty profile matched")
	}
}

func TestASweepSkipsABrowserThatCouldNotBeEndedUntilStartOrUntilItIsGone(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "browser-profile")
	restored := process{Session: 1, Args: append(kioskArgs(profile, testVisualizer), "--restore-last-session")}
	stuck := browser{browserID{PID: 41, Started: 100}, restored}
	var notices []string
	b := &Backend{Config: Config{BrowserProfile: profile}, Notice: func(s string) { notices = append(notices, s) }}
	tries := map[uint32]int{}
	end := func(_ context.Context, f browser) error {
		tries[f.PID]++
		if f.PID == 41 {
			return errors.New("access denied")
		}
		return nil
	}
	sweep := func(start bool, found ...browser) error { return b.sweep(context.Background(), found, 1, start, end) }
	for i := 0; i < 3; i++ {
		if e := sweep(false, stuck); !errors.Is(e, lifecycle.ErrRestoredOpen) {
			t.Fatalf("sweep %d: %v", i, e)
		}
	}
	if tries[41] != 1 || len(notices) != 1 || !strings.HasSuffix(notices[0], "(process 41: access denied)") {
		t.Fatalf("a browser that could not be ended was tried %d times; notices %q", tries[41], notices)
	}
	if e := sweep(false, stuck, browser{browserID{PID: 42, Started: 100}, restored}); !errors.Is(e, lifecycle.ErrRestoredOpen) || tries[42] != 1 {
		t.Fatalf("another restored browser beside it: %v, tried %d times", e, tries[42])
	}
	if e := sweep(true, stuck); !errors.Is(e, lifecycle.ErrRestoredOpen) || tries[41] != 2 {
		t.Fatalf("the sweep before Start must try again: %v, tried %d times", e, tries[41])
	}
	if e := sweep(false); e != nil {
		t.Fatalf("with the browser gone: %v", e)
	}
	if e := sweep(false, stuck); !errors.Is(e, lifecycle.ErrRestoredOpen) || tries[41] != 3 {
		t.Fatalf("a browser that is gone is forgotten, so its next appearance is tried: %v, tried %d times", e, tries[41])
	}
	reused := browser{browserID{PID: 41, Started: 200}, restored}
	if sweep(false, reused); tries[41] != 4 {
		t.Fatalf("a reused PID is a new browser: tried %d times", tries[41])
	}
}

func TestTheWorkerRefusesWithTheSameTextAsTheController(t *testing.T) {
	// Start maps the worker's refusal to lifecycle.ErrRestoredOpen by its text.
	b, e := os.ReadFile(filepath.Join("..", "..", "..", "install", "Run-PartyVisualsSession.ps1"))
	if e != nil || !strings.Contains(string(b), "throw '"+lifecycle.ErrRestoredOpen.Error()+"'") {
		t.Fatalf("Run-PartyVisualsSession.ps1 does not throw %q: %v", lifecycle.ErrRestoredOpen, e)
	}
}

func TestEndFailureNamesWhatFailed(t *testing.T) {
	for _, e := range []error{endFailure(nil, 5*time.Second), endFailure(errors.New("Access is denied."), 5*time.Second)} {
		if strings.Contains(e.Error(), "<nil>") {
			t.Fatalf("%q", e)
		}
	}
	if e := endFailure(nil, 5*time.Second).Error(); e != "still running 5s after it was ended" {
		t.Fatalf("%q", e)
	}
}
