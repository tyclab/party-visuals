package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStaleHelperCannotLookReady(t *testing.T) {
	p := filepath.Join(t.TempDir(), "status.json")
	s := helperStatus{Version: 1, Time: time.Now().Add(-time.Minute)}
	s.Display.Live = true
	b, _ := json.Marshal(s)
	os.WriteFile(p, b, 0600)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := Diagnose(ctx, Config{StatusFile: p, ControlsURL: "http://127.0.0.1:1"})
	if d.Screen != "status stale" || d.Capture != "unknown" {
		t.Fatalf("stale image reported ready: %+v", d)
	}
}
