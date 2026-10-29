package main

import (
	"encoding/json"
	"github.com/tyclab/party-visuals/controller/internal/lifecycle"
	"github.com/tyclab/party-visuals/controller/internal/owner"
	"github.com/tyclab/party-visuals/controller/internal/partysource"
	"github.com/tyclab/party-visuals/controller/internal/worker"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRoot(t *testing.T, extra map[string]any) string {
	t.Helper()
	root := t.TempDir()
	v := map[string]any{"version": 1, "nodecgRoot": root, "browserProfile": filepath.Join(root, "profile"), "logFile": filepath.Join(root, "nodecg.log"), "visualizerUrl": "http://music.example.invalid:8095/#/now-playing?player=test", "controlsUrl": "http://127.0.0.1:9090/bundles/party-visuals/api/state"}
	for _, name := range []string{"workerScript", "nodeExe", "chromeExe", "helperPath"} {
		p := filepath.Join(root, name)
		os.WriteFile(p, nil, 0600)
		v[name] = p
	}
	for k, x := range extra {
		v[k] = x
	}
	b, _ := json.Marshal(v)
	os.WriteFile(filepath.Join(root, "config.json"), b, 0600)
	return root
}
func stdout(t *testing.T, f func() error) (string, error) {
	t.Helper()
	r, w, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	saved := os.Stdout
	os.Stdout = w
	e = f()
	os.Stdout = saved
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b), e
}

func TestCheckValidatesTheHomeAssistantTokenFile(t *testing.T) {
	out, e := stdout(t, func() error { return run([]string{"--root", writeRoot(t, nil), "check"}) })
	if e != nil || !strings.Contains(out, "Follow Party: no Home Assistant source configured") {
		t.Fatalf("unconfigured check: %q, %v", out, e)
	}
	token := filepath.Join(t.TempDir(), "ha.token")
	root := writeRoot(t, map[string]any{"homeAssistantUrl": "https://ha.example.invalid", "homeAssistantTokenFile": token})
	for _, content := range []string{"", "\n"} {
		os.WriteFile(token, []byte(content), 0600)
		if _, e = stdout(t, func() error { return run([]string{"--root", root, "check"}) }); e == nil {
			t.Fatalf("check accepted token file %q", content)
		}
	}
	os.WriteFile(token, []byte("example-token\n"), 0600)
	out, e = stdout(t, func() error { return run([]string{"--root", root, "check"}) })
	if e != nil || !strings.Contains(out, "sensor.house_mode_code") || strings.Contains(out, "example-token") {
		t.Fatalf("configured check: %q, %v", out, e)
	}
}

func TestDiagnosticsNameTheFollowPartySource(t *testing.T) {
	root := t.TempDir()
	var source *partysource.Source
	server, e := owner.Listen(root, func() any {
		return statusView{Version: "test", State: lifecycle.Status{Phase: "waiting-for-party"}, Diagnostics: worker.Diagnostics{Screen: "ready", Audio: "unknown", OpenRGB: "unavailable"}, FollowParty: source.Status()}
	}, func(string) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	out, e := stdout(t, func() error { return run([]string{"--root", root, "diagnostics"}) })
	if e != nil || !strings.Contains(out, "Follow Party: no Home Assistant source configured\n") || !strings.Contains(out, "waiting-for-party") {
		t.Fatalf("diagnostics: %q, %v", out, e)
	}
	out, e = stdout(t, func() error { return run([]string{"--root", root, "status", "--json"}) })
	var s statusView
	if e != nil || json.Unmarshal([]byte(out), &s) != nil || s.FollowParty.State != "unconfigured" {
		t.Fatalf("status --json: %q, %v", out, e)
	}
}

func TestPartyLinesLandInControllerLog(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "controller.log")
	for i := 0; i < 2; i++ {
		f, e := openLog(root)
		if e != nil {
			t.Fatal(e)
		}
		log.New(f, "", log.LstdFlags).Printf("Follow Party: sensor.house_mode_code is %q; sent party-on", "3")
		f.Close()
	}
	b, _ := os.ReadFile(path)
	if strings.Count(string(b), "sent party-on") != 2 {
		t.Fatalf("controller.log holds %q", b)
	}
	os.WriteFile(path, make([]byte, 1024*1024+1), 0600)
	f, e := openLog(root)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
	if old, e := os.Stat(path + ".1"); e != nil || old.Size() != 1024*1024+1 {
		t.Fatalf("full log not rotated: %v", e)
	}
	if fresh, e := os.Stat(path); e != nil || fresh.Size() != 0 {
		t.Fatalf("fresh log: %v", e)
	}
}
