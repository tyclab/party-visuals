package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, extra map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	v := map[string]any{"version": 1, "nodecgRoot": dir, "browserProfile": filepath.Join(dir, "profile"), "logFile": filepath.Join(dir, "nodecg.log"), "visualizerUrl": "http://music.example.invalid:8095/#/now-playing?player=test", "controlsUrl": "http://127.0.0.1:9090/bundles/party-visuals/api/state"}
	for _, name := range []string{"workerScript", "nodeExe", "chromeExe", "helperPath"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, nil, 0600)
		v[name] = p
	}
	for k, x := range extra {
		v[k] = x
	}
	b, _ := json.Marshal(v)
	p := filepath.Join(dir, "config.json")
	os.WriteFile(p, b, 0600)
	runFrom(t, dir)
	return p
}

// runFrom makes dir the release folder, as if party-visuals.exe ran from it.
func runFrom(t *testing.T, dir string) {
	saved := executable
	executable = func() (string, error) { return filepath.Join(dir, "party-visuals.exe"), nil }
	t.Cleanup(func() { executable = saved })
}

func TestWorkerScriptMustBeInTheExecutablesFolder(t *testing.T) {
	root := t.TempDir()
	release := filepath.Join(root, "release")
	os.MkdirAll(filepath.Join(release, "scripts"), 0700)
	const name = "Run-PartyVisualsSession.ps1"
	for _, dir := range []string{root, release, filepath.Join(release, "scripts")} {
		os.WriteFile(filepath.Join(dir, name), nil, 0600)
	}
	load := func(script string) error {
		p := writeConfig(t, map[string]any{"workerScript": script})
		runFrom(t, release)
		_, e := Load(p)
		return e
	}
	sep := string(filepath.Separator)
	for what, script := range map[string]string{
		"the folder above":       filepath.Join(root, name),
		"a path that climbs out": release + sep + ".." + sep + name,
		"a subfolder":            filepath.Join(release, "scripts", name),
	} {
		if e := load(script); e == nil || !strings.Contains(e.Error(), "workerScript must be in the folder that holds this executable") {
			t.Errorf("%s: %v", what, e)
		}
	}
	if e := load(filepath.Join(release, name)); e != nil {
		t.Errorf("the script beside the executable: %v", e)
	}
	if e := load(name); e == nil || e.Error() != "workerScript must be absolute" {
		t.Errorf("a relative workerScript: %v", e)
	}
}

func TestConfigWithoutHomeAssistantLeavesFollowPartyUnconfigured(t *testing.T) {
	c, e := Load(writeConfig(t, nil))
	if e != nil {
		t.Fatal(e)
	}
	if c.HomeAssistantURL != "" || c.PartyEntity != "" || c.PartyValue != "" || c.PartyPollMs != 0 {
		t.Fatalf("defaults applied without a source: %+v", c)
	}
}

func TestHomeAssistantSourceDefaults(t *testing.T) {
	token := filepath.Join(t.TempDir(), "ha.token")
	c, e := Load(writeConfig(t, map[string]any{"homeAssistantUrl": "https://ha.example.invalid", "homeAssistantTokenFile": token}))
	if e != nil {
		t.Fatal(e)
	}
	if c.PartyEntity != "sensor.house_mode_code" || c.PartyValue != "3" || c.PartyPollMs != 5000 {
		t.Fatalf("defaults %+v", c)
	}
}

func TestHomeAssistantSourceIsValidated(t *testing.T) {
	token := filepath.Join(t.TempDir(), "ha.token")
	good := map[string]any{"homeAssistantUrl": "https://ha.example.invalid:8123", "homeAssistantTokenFile": token}
	for name, change := range map[string]map[string]any{
		"plain http":            {"homeAssistantUrl": "http://ha.example.invalid:8123"},
		"credentials in url":    {"homeAssistantUrl": "https://user:secret@ha.example.invalid"},
		"query in url":          {"homeAssistantUrl": "https://ha.example.invalid/?token=x"},
		"no url":                {"homeAssistantUrl": ""},
		"token in config":       {"homeAssistantToken": "secret"},
		"relative token file":   {"homeAssistantTokenFile": "ha.token"},
		"no token file":         {"homeAssistantTokenFile": ""},
		"entity with path":      {"partyEntity": "sensor.x/../../config"},
		"entity without domain": {"partyEntity": "house_mode_code"},
		"poll too fast":         {"partyPollMs": 200},
		"poll too slow":         {"partyPollMs": 120000},
		"party keys alone":      {"homeAssistantUrl": "", "homeAssistantTokenFile": "", "partyValue": "3"},
	} {
		v := map[string]any{}
		for k, x := range good {
			v[k] = x
		}
		for k, x := range change {
			v[k] = x
		}
		if _, e := Load(writeConfig(t, v)); e == nil {
			t.Errorf("%s accepted", name)
		} else if strings.Contains(e.Error(), "secret") {
			t.Errorf("%s: error repeats a credential: %v", name, e)
		}
	}
}

func TestBrowserProfileMustNotBeABrowsersOwnProfileFolder(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	sep := string(filepath.Separator)
	refused := func(profile string) bool {
		_, e := Load(writeConfig(t, map[string]any{"browserProfile": profile}))
		return e != nil && strings.Contains(e.Error(), "not a browser's own profile folder")
	}
	chrome := filepath.Join(local, "Google", "Chrome", "User Data")
	for _, profile := range []string{
		chrome,
		strings.ToUpper(chrome) + sep + sep,
		filepath.Join(local, "Google", "Chrome Beta", "User Data"),
		filepath.Join(local, "Chromium", "User Data"),
		filepath.Join(local, "Microsoft", "Edge", "User Data"),
	} {
		if !refused(profile) {
			t.Errorf("browserProfile %q accepted", profile)
		}
	}
	for _, profile := range []string{
		filepath.Join(local, "PartyVisuals", "browser-profile"),
		filepath.Join(chrome, "Default"),
		filepath.Join(local, "Google", "Chrome"),
		filepath.Join(t.TempDir(), "User Data"),
	} {
		if refused(profile) {
			t.Errorf("browserProfile %q refused", profile)
		}
	}
	os.MkdirAll(chrome, 0700)
	link := filepath.Join(t.TempDir(), "local")
	if e := os.Symlink(local, link); e != nil {
		t.Skipf("no symbolic links here: %v", e)
	}
	if !refused(filepath.Join(link, "Google", "Chrome", "User Data")) {
		t.Error("the default root spelt through a link to LOCALAPPDATA was accepted")
	}
	t.Setenv("LOCALAPPDATA", link)
	if !refused(chrome) {
		t.Error("the default root was accepted while LOCALAPPDATA names it through a link")
	}
}
