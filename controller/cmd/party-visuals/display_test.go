package main

import (
	"github.com/tyclab/party-visuals/controller/internal/partysource"
	"github.com/tyclab/party-visuals/controller/internal/worker"
	"reflect"
	"testing"
)

func TestDiagnosticsNameWhereTheVisualsOpenedAndTheDisplayPower(t *testing.T) {
	lines := diagnosticLines(worker.Diagnostics{Screen: "ready", Display: `\\.\DISPLAY3 (Big TV, 3840x2160 at 1920,-2160): the preferred display is connected and on`, DisplayPower: "media_player.big_tv is on (on)"}, partysource.Status{Summary: "x"})
	want := []string{"Screen: ready", "Audio: ", "OpenRGB: ", "Follow Party: x", `Display: \\.\DISPLAY3 (Big TV, 3840x2160 at 1920,-2160): the preferred display is connected and on`, "Display power: media_player.big_tv is on (on)"}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("%q", lines)
	}
	if lines := diagnosticLines(worker.Diagnostics{}, partysource.Status{}); len(lines) != 4 {
		t.Fatalf("without a preferred display: %q", lines)
	}
}
