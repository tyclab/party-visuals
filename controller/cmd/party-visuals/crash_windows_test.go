package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The Go runtime prints a crash to the process's standard error, which the
// login task gives no reader; app has to point it at controller.log.
func TestACrashReportLandsInControllerLog(t *testing.T) {
	if path := os.Getenv("PARTY_TEST_CRASH_LOG"); path != "" {
		f, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil || releaseAppConsole(f) != nil {
			os.Exit(9)
		}
		f.Close() // as owned does while a panic unwinds: the report has its own handle
		go func() { panic("party test crash") }()
		time.Sleep(time.Minute)
		os.Exit(8)
	}
	path := filepath.Join(t.TempDir(), "controller.log")
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(exe, "-test.run=^TestACrashReportLandsInControllerLog$")
	cmd.Env = append(os.Environ(), "PARTY_TEST_CRASH_LOG="+path)
	e = cmd.Run()
	var exit *exec.ExitError
	if !errors.As(e, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("the crashing controller ended with %v, not the runtime's exit code 2", e)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "panic: party test crash") || strings.Count(string(b), "\ngoroutine ") < 2 {
		t.Fatalf("controller.log holds no report with every goroutine: %q", b)
	}
}
