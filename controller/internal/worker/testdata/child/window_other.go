//go:build !windows

package main

import "os"

func window() { os.Exit(3) }
