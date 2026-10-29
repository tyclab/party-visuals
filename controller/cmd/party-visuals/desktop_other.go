//go:build !windows

package main

import (
	"errors"
	"os/exec"
)

func newConsole(*exec.Cmd)           {}
func releaseAppConsole(string) error { return nil }
func desktopOpen(string) error       { return errors.New("desktop controls require Windows") }
func smallIconSize() int             { return 0 }
