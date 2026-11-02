package main

import (
	"encoding/json"
	"errors"
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

func readLog(t *testing.T, root string) string {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(root, "controller.log"))
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestRestoredWindowNoticesLandInControllerLogAndDiagnostics(t *testing.T) {
	root := t.TempDir()
	f, e := openLog(root)
	if e != nil {
		t.Fatal(e)
	}
	l := &controllerLog{Logger: log.New(f, "", log.LstdFlags)}
	if lines := diagnosticLines(worker.Diagnostics{Browser: l.browserNote()}, partysource.Status{}); strings.HasPrefix(lines[len(lines)-1], "Browser:") {
		t.Fatal("a Browser line without a restored window")
	}
	open := worker.RestoredOpen + " (process 41: access denied)"
	if !l.restored(open) || l.restored(open) {
		t.Fatal("a repeated notice was not recognised")
	}
	if !l.restored(worker.RestoredClosed + " (process 42)") {
		t.Fatal("a new notice was taken for a repeat")
	}
	f.Close()
	b := readLog(t, root)
	for _, want := range []string{open + "\n", "A browser window restored by Windows at sign-in was closed (process 42)\n"} {
		if strings.Count(b, want) != 1 {
			t.Errorf("controller.log lacks one %q:\n%s", want, b)
		}
	}
	lines := diagnosticLines(worker.Diagnostics{Browser: l.browserNote()}, partysource.Status{})
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "Browser: A browser window restored by Windows at sign-in was closed (process 42) at ") {
		t.Fatalf("diagnostics end with %q", last)
	}
}

func TestCommandsAndStatesLandInControllerLog(t *testing.T) {
	root := t.TempDir()
	f, e := openLog(root)
	if e != nil {
		t.Fatal(e)
	}
	l := &controllerLog{Logger: log.New(f, "", log.LstdFlags)}
	apply := func(c string) error {
		if c == "party-on" {
			return errors.New("controller is shutting down")
		}
		return nil
	}
	server, e := owner.Listen(root, func() any { return statusView{} }, l.command("cli", apply))
	if e != nil {
		t.Fatal(e)
	}
	defer server.Close()
	if e = l.command("tray", apply)("start"); e != nil {
		t.Fatal(e)
	}
	for _, args := range [][]string{{"stop"}, {"mode", "follow-party"}, {"quit"}} {
		if _, e = stdout(t, func() error { return run(append([]string{"--root", root}, args...)) }); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = stdout(t, func() error { return run([]string{"--root", root, "party", "on"}) }); e == nil {
		t.Fatal("refused command reported success")
	}
	off := lifecycle.Status{Phase: "off", Intent: lifecycle.Intent{Version: 1, Mode: "manual", ManualOff: true}}
	l.state(off)
	l.state(off)
	refused := off
	refused.Phase, refused.Reason = "attention", worker.RestoredOpen
	l.state(refused)
	f.Close()
	b := readLog(t, root)
	for _, want := range []string{
		"command start from tray: accepted\n",
		"command stop from cli: accepted\n",
		"command follow-party from cli: accepted\n",
		"command quit from cli: accepted\n",
		"command party-on from cli: refused: controller is shutting down\n",
		"phase off (mode manual, Manual Off)\n",
		"phase attention (mode manual, Manual Off): A browser window restored by Windows at sign-in is open; close it before Start\n",
	} {
		if strings.Count(b, want) != 1 {
			t.Errorf("controller.log lacks one %q:\n%s", want, b)
		}
	}
}
