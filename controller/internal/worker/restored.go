package worker

import (
	"context"
	"path/filepath"
	"strings"
)

// Windows' "restart apps after sign-in" relaunches a Chrome that was open at
// shutdown with --restore-last-session added. The notices name that window.
const (
	RestoredClosed = "A browser window restored by Windows at sign-in was closed"
	RestoredOpen   = "A browser window restored by Windows at sign-in is open; close it before Start"
)

type process struct {
	Session uint32
	Args    []string
}

// foreignRestored reports whether p is a Chrome browser process (no --type) in
// session whose only --user-data-dir is profile and which carries
// --restore-last-session, which nothing the controller starts carries.
func foreignRestored(p process, profile string, session uint32) bool {
	const dir = "--user-data-dir="
	dirs, same, restored := 0, false, false
	for _, a := range p.Args {
		switch {
		case len(a) >= 7 && strings.EqualFold(a[:7], "--type="):
			return false
		case len(a) >= len(dir) && strings.EqualFold(a[:len(dir)], dir):
			dirs++
			same = profile != "" && sameDir(a[len(dir):], profile)
		case strings.EqualFold(a, "--restore-last-session"):
			restored = true
		}
	}
	return p.Session == session && dirs == 1 && same && restored
}

// sameDir compares directories as Windows does: full paths, any case,
// without trailing separators.
func sameDir(a, b string) bool {
	full := func(s string) string {
		if abs, e := filepath.Abs(s); e == nil {
			s = abs
		}
		return strings.TrimRight(s, `\/`)
	}
	return strings.EqualFold(full(a), full(b))
}

// CloseRestored closes each dedicated-profile Chrome Windows restored at
// sign-in, through its windows first as Close-VisualsBrowser does, reports
// each to Notice, and returns RestoredOpen while one stays open.
func (b Backend) CloseRestored(ctx context.Context) error {
	notice := b.Notice
	if notice == nil {
		notice = func(string) {}
	}
	return closeRestored(ctx, b.Config.BrowserProfile, notice)
}
