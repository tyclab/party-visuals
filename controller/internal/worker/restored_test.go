package worker

import (
	"path/filepath"
	"strings"
	"testing"
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
