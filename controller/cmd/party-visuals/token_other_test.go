//go:build !windows

package main

import "testing"

// ownerOnly has nothing to set: only Windows checks the token file's DACL.
func ownerOnly(*testing.T, string) {}
