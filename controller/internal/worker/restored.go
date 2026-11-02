package worker

import (
	"context"
	"errors"
	"fmt"
	"github.com/tyclab/party-visuals/controller/internal/lifecycle"
	"path/filepath"
	"strings"
	"time"
)

// Windows' "restart apps after sign-in" relaunches a Chrome that was open at
// shutdown with --restore-last-session added. lifecycle.ErrRestoredOpen
// names one still open.
const RestoredClosed = "A browser window restored by Windows at sign-in was closed"

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

// browser is one chrome.exe; its start time tells a reused PID apart.
type browser struct {
	browserID
	Process process
}
type browserID struct {
	PID     uint32
	Started int64
}

// CloseRestored closes each dedicated-profile Chrome Windows restored at
// sign-in, through its windows first as Close-VisualsBrowser does, reports
// each to Notice, and returns lifecycle.ErrRestoredOpen while one stays open.
func (b *Backend) CloseRestored(ctx context.Context, start bool) error {
	return closeRestored(ctx, b, start)
}

// sweep closes the restored browsers among found with end. One that could not
// be ended is skipped, still open, until a sweep before Start or until it is gone.
func (b *Backend) sweep(ctx context.Context, found []browser, session uint32, start bool, end func(context.Context, browser) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	notice := b.Notice
	if notice == nil {
		notice = func(string) {}
	}
	present := map[browserID]bool{}
	var open error
	for _, f := range found {
		if !foreignRestored(f.Process, b.Config.BrowserProfile, session) {
			continue
		}
		id := f.browserID
		present[id] = true
		if b.stuck[id] && !start {
			open = lifecycle.ErrRestoredOpen
			continue
		}
		e := end(ctx, f)
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case e != nil:
			if b.stuck == nil {
				b.stuck = map[browserID]bool{}
			}
			b.stuck[id] = true
			notice(fmt.Sprintf("%s (process %d: %v)", strings.TrimSuffix(lifecycle.ErrRestoredOpen.Error(), "."), f.PID, e))
			open = lifecycle.ErrRestoredOpen
		default:
			delete(b.stuck, id)
			notice(fmt.Sprintf("%s (process %d)", RestoredClosed, f.PID))
		}
	}
	for id := range b.stuck {
		if !present[id] {
			delete(b.stuck, id)
		}
	}
	return open
}

// endFailure describes a browser that outlived TerminateProcess's result.
func endFailure(terminate error, wait time.Duration) error {
	if terminate != nil {
		return fmt.Errorf("could not be ended: %w", terminate)
	}
	return errors.New("still running " + wait.String() + " after it was ended")
}
