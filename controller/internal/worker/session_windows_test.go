//go:build windows

package worker

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualPowerShellSessionUsesOnlyOwnedFixtureProcesses(t *testing.T) {
	script, child := os.Getenv("PARTY_TEST_SESSION_SCRIPT"), os.Getenv("PARTY_TEST_CHILD")
	if script == "" || child == "" {
		t.Skip("native fixture binaries and worker script must be supplied by Windows CI")
	}
	dir := t.TempDir()
	workerSource, e := os.ReadFile(script)
	if e != nil {
		t.Fatal(e)
	}
	const productionMutex = `Local\PartyVisuals-Browser-Launch`
	if strings.Count(string(workerSource), productionMutex) != 1 {
		t.Fatal("worker mutex boundary changed; update isolated fixture")
	}
	workerSource = []byte(strings.Replace(string(workerSource), productionMutex, `Local\PartyVisuals-Test-`+filepath.Base(dir), 1))
	for _, name := range []string{"Start-PartyVisualsNodecg.ps1", "Start-PartyVisualsBrowser.ps1"} {
		b, e := os.ReadFile(filepath.Join(filepath.Dir(script), name))
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, name), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	script = filepath.Join(dir, "Run-PartyVisualsSession.ps1")
	if e = os.WriteFile(script, workerSource, 0600); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(child)
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"node.exe", "chrome.exe"} {
		if e = os.WriteFile(filepath.Join(dir, name), raw, 0700); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"start.js", "helper.js"} {
		os.WriteFile(filepath.Join(dir, name), nil, 0600)
	}
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := l.Addr().String()
	l.Close()
	c := Config{Version: 1, WorkerScript: script, NodeExe: filepath.Join(dir, "node.exe"), ChromeExe: filepath.Join(dir, "chrome.exe"), NodecgRoot: dir, BrowserProfile: filepath.Join(dir, "profile"), HelperPath: filepath.Join(dir, "helper.js"), VisualizerURL: "http://example.test/#/now-playing?player=test&frameless=1", ControlsURL: "http://" + address + "/state", LogFile: filepath.Join(dir, "nodecg.log"), StatusFile: filepath.Join(dir, "helper-status.json"), Fixture: 1}
	configPath := filepath.Join(dir, "config.json")
	data, _ := json.Marshal(c)
	os.WriteFile(configPath, data, 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	s, e := (Backend{Config: c, ConfigPath: configPath}).Start(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Stop(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("worker exit not observed")
	}
}
