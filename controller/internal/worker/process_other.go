//go:build !windows

package worker

import (
	"errors"
	"os/exec"
)

func powerShell() string { return "powershell" }
func hide(*exec.Cmd)     {}
func newJob() (job, error) {
	return nil, errors.New("visuals workers require Windows; diagnostics and state tests are platform independent")
}
