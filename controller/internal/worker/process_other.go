//go:build !windows

package worker

import (
	"context"
	"errors"
	"os/exec"
)

func powerShell() string { return "powershell" }
func hide(*exec.Cmd)     {}
func newJob() (job, error) {
	return nil, errors.New("visuals workers require Windows; diagnostics and state tests are platform independent")
}

// closeRestored has nothing to close: only Windows restores apps at sign-in.
func closeRestored(ctx context.Context, b *Backend, start bool) error {
	return b.sweep(ctx, nil, 0, start, nil)
}
