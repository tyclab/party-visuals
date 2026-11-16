package main

import (
	"fmt"
	"github.com/tyclab/party-visuals/controller/internal/partysource"
	"github.com/tyclab/party-visuals/controller/internal/worker"
	"reflect"
	"testing"
)

func TestDiagnosticsNameWhereTheVisualsOpenedAndTheDisplayPower(t *testing.T) {
	lines := diagnosticLines(worker.Diagnostics{Screen: "ready", Display: `\\.\DISPLAY3 (Big TV, 3840x2160 at 1920,-2160): the preferred display is connected and on`, DisplayPower: "media_player.big_tv is on (on)"}, partysource.Status{Summary: "x"}, partysource.Status{Summary: "y"})
	want := []string{"Screen: ready", "Audio: ", "OpenRGB: ", "Follow Party: x", "Follow Lightshow: y", `Display: \\.\DISPLAY3 (Big TV, 3840x2160 at 1920,-2160): the preferred display is connected and on`, "Display power: media_player.big_tv is on (on)"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("%q", lines)
	}
	if lines := diagnosticLines(worker.Diagnostics{}, partysource.Status{}, partysource.Status{}); len(lines) != 5 {
		t.Fatalf("without a preferred display: %q", lines)
	}
}

func TestRunningVisualsReopenOnlyWhenTheSettledPowerMovesThemToAnotherScreen(t *testing.T) {
	onTV := worker.Placement{Text: "TV", PreferredListed: true, Power: "on"}
	for _, c := range []struct {
		name, phase, reading string
		placed               worker.Placement
		reopen               bool
	}{
		{"TV went off", "running", "off", onTV, true},
		{"TV came on", "running", "on", worker.Placement{PreferredListed: true, Power: "off"}, true},
		{"unchanged", "running", "on", onTV, false},
		{"not running", "waiting-for-party", "off", onTV, false},
		{"starting", "starting", "off", onTV, false},
		{"preferred display not listed", "running", "on", worker.Placement{Power: "off"}, false},
	} {
		var events, logs []string
		reopenOnPower("media_player.big_tv", func() string { return c.phase }, func() worker.Placement { return c.placed }, func() string { return c.reading }, func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }, func(e string) { events = append(events, e) })()
		if want := c.reopen; (len(events) == 1 && events[0] == "display-change") != want || len(events) > 1 {
			t.Fatalf("%s: events %q", c.name, events)
		}
		if c.reopen && (len(logs) != 1 || logs[0] != "Display power: media_player.big_tv settled at "+c.reading+"; reopening the visuals") {
			t.Fatalf("%s: logs %q", c.name, logs)
		}
	}
}
