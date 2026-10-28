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
	return p
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
