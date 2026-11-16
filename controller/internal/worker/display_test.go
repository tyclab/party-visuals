package worker

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPreferredDisplayAloneNeedsNoHomeAssistant(t *testing.T) {
	c, e := Load(writeConfig(t, map[string]any{"preferredDisplay": "Big TV"}))
	if e != nil {
		t.Fatal(e)
	}
	if c.PreferredDisplay != "Big TV" || c.DisplayPowerEntity != "" || c.HomeAssistantURL != "" || c.PartyEntity != "" {
		t.Fatalf("%+v", c)
	}
}

func TestPreferredDisplayIsOneNameAsWindowsShowsIt(t *testing.T) {
	for _, name := range []string{" ", " Big TV", "Big TV ", "Big\nTV", "Big\x00TV", strings.Repeat("x", 65)} {
		if _, e := Load(writeConfig(t, map[string]any{"preferredDisplay": name})); e == nil || !strings.Contains(e.Error(), "preferredDisplay must be") {
			t.Errorf("%q: %v", name, e)
		}
	}
	if _, e := Load(writeConfig(t, map[string]any{"preferredDisplay": strings.Repeat("x", 64)})); e != nil {
		t.Errorf("64 characters: %v", e)
	}
}

func TestDisplayPowerEntityNeedsAPreferredDisplayAndHomeAssistant(t *testing.T) {
	token := filepath.Join(t.TempDir(), "ha.token")
	source := map[string]any{"homeAssistantUrl": "https://ha.example.invalid:8123", "homeAssistantTokenFile": token}
	with := func(extra map[string]any) map[string]any {
		v := map[string]any{}
		for k, x := range source {
			v[k] = x
		}
		for k, x := range extra {
			v[k] = x
		}
		return v
	}
	for what, c := range map[string]struct {
		config map[string]any
		want   string
	}{
		"no preferred display": {with(map[string]any{"displayPowerEntity": "media_player.big_tv"}), "displayPowerEntity needs preferredDisplay"},
		"no Home Assistant":    {map[string]any{"displayPowerEntity": "media_player.big_tv", "preferredDisplay": "Big TV"}, "homeAssistantUrl must be an https URL"},
		"a malformed entity":   {with(map[string]any{"displayPowerEntity": "Big TV", "preferredDisplay": "Big TV"}), "displayPowerEntity must be a Home Assistant entity id"},
	} {
		if _, e := Load(writeConfig(t, c.config)); e == nil || !strings.Contains(e.Error(), c.want) {
			t.Errorf("%s: %v", what, e)
		}
	}
	c, e := Load(writeConfig(t, with(map[string]any{"displayPowerEntity": "media_player.big_tv", "preferredDisplay": "Big TV"})))
	if e != nil {
		t.Fatal(e)
	}
	if c.DisplayPowerEntity != "media_player.big_tv" || c.PartyPollMs != 5000 {
		t.Fatalf("%+v", c)
	}
}

func TestWorkerCommandCarriesThePowerReading(t *testing.T) {
	b := &Backend{Config: Config{WorkerScript: `C:\release\Run-PartyVisualsSession.ps1`}, ConfigPath: `C:\controller\config.json`}
	want := []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", `C:\release\Run-PartyVisualsSession.ps1`, "-ConfigPath", `C:\controller\config.json`, "-DisplayPower", "none"}
	if got := b.command(b.power()); !reflect.DeepEqual(got, want) {
		t.Fatalf("without a power source: %q", got)
	}
	b.Power = func() string { return "off" }
	if got := b.command(b.power()); got[len(got)-1] != "off" {
		t.Fatalf("with a power source: %q", got)
	}
}

func TestPlacementIsStaleOnlyWhenOnOrOffChangesTheScreen(t *testing.T) {
	for _, c := range []struct {
		listed      bool
		opened, now string
		stale       bool
	}{
		{true, "on", "off", true},
		{true, "off", "on", true},
		{true, "unknown", "off", true},
		{true, "off", "unknown", true},
		{true, "unknown", "on", false},
		{true, "on", "unknown", false},
		{true, "on", "on", false},
		{true, "none", "none", false},
		{false, "on", "off", false},
		{false, "off", "on", false},
	} {
		p := Placement{Text: "x", PreferredListed: c.listed, Power: c.opened}
		if got := p.Stale(c.now); got != c.stale {
			t.Errorf("listed %v, opened %s, now %s: stale %v", c.listed, c.opened, c.now, got)
		}
	}
	if (Placement{}).Stale("off") {
		t.Error("a session that reported no placement is stale")
	}
}

func TestDisplayEventRecordsThePlacementWithItsPowerReading(t *testing.T) {
	var logged []Placement
	b := &Backend{Placed: func(p Placement) { logged = append(logged, p) }}
	b.place(Placement{Power: "on"})
	b.event([]byte(`{"preferredListed":true,"power":"off","event":"display","text":"\\\\.\\DISPLAY2 (Desk Monitor, 3840x1600 at 3840,0): the preferred display is off; first screen other than the primary"}`))
	want := Placement{Text: `\\.\DISPLAY2 (Desk Monitor, 3840x1600 at 3840,0): the preferred display is off; first screen other than the primary`, PreferredListed: true, Power: "off"}
	if got := b.Placement(); got != want {
		t.Fatalf("placement %+v", got)
	}
	if !reflect.DeepEqual(logged, []Placement{want}) {
		t.Fatalf("logged %+v", logged)
	}
	b.place(Placement{Power: "on"})
	if got := b.Placement(); got != (Placement{Power: "on"}) {
		t.Fatalf("a new session keeps the last one's placement: %+v", got)
	}
}
