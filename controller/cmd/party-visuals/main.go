package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/tyclab/party-visuals/controller/internal/lifecycle"
	"github.com/tyclab/party-visuals/controller/internal/owner"
	"github.com/tyclab/party-visuals/controller/internal/partysource"
	"github.com/tyclab/party-visuals/controller/internal/worker"
	"github.com/tyclab/tycswap/desktop/tray"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var version = "dev"

type statusView struct {
	Version     string             `json:"version"`
	State       lifecycle.Status   `json:"state"`
	Diagnostics worker.Diagnostics `json:"diagnostics"`
	FollowParty partysource.Status `json:"followParty"`
}

func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run(args []string) error {
	f := flag.NewFlagSet("party-visuals", flag.ContinueOnError)
	root := f.String("root", filepath.Join(os.Getenv("LOCALAPPDATA"), "PartyVisuals", "controller"), "controller state directory")
	if e := f.Parse(args); e != nil {
		return e
	}
	a := f.Args()
	if len(a) == 0 {
		return menu(*root)
	}
	switch a[0] {
	case "version", "--version":
		fmt.Println(version)
		return nil
	case "app":
		return app(*root)
	case "menu":
		return menu(*root)
	case "check":
		c, e := worker.Load(filepath.Join(*root, "config.json"))
		if e != nil {
			return e
		}
		_, e = (lifecycle.FileStore{Path: filepath.Join(*root, "intent.json")}).Load()
		if e != nil {
			return e
		}
		follow := partysource.Unconfigured
		if c.HomeAssistantURL != "" {
			if _, e = partysource.ReadToken(c.HomeAssistantTokenFile); e != nil {
				return e
			}
			follow = "polls Home Assistant " + c.PartyEntity
		}
		fmt.Printf("Configuration valid; worker %s; Follow Party: %s; no processes started\n", filepath.Base(c.WorkerScript), follow)
		return nil
	case "status":
		return invoke(*root, "")
	case "diagnostics":
		b, e := owner.Call(*root, "")
		if e != nil {
			return e
		}
		var s statusView
		if e = json.Unmarshal(b, &s); e != nil {
			return e
		}
		fmt.Println("Party Visuals · " + s.State.Phase)
		if s.State.Reason != "" {
			fmt.Println(s.State.Reason)
		}
		for _, line := range diagnosticLines(s.Diagnostics, s.FollowParty) {
			fmt.Println(line)
		}
		return nil
	case "start", "stop", "show", "quit":
		return invoke(*root, a[0])
	case "mode":
		if len(a) != 2 || (a[1] != "manual" && a[1] != "follow-party") {
			return fmt.Errorf("mode requires manual or follow-party")
		}
		return invoke(*root, a[1])
	case "party":
		if len(a) != 2 || (a[1] != "on" && a[1] != "off") {
			return fmt.Errorf("party requires on or off")
		}
		return invoke(*root, "party-"+a[1])
	default:
		return fmt.Errorf("commands: app, menu, start, stop, status --json, diagnostics, show, mode manual|follow-party, party on|off, quit, check, version")
	}
}
func invoke(root, command string) error {
	b, e := owner.Call(root, command)
	if e != nil {
		return e
	}
	fmt.Println(string(b))
	return nil
}
func menu(root string) error {
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Fprintln(os.Stderr, "\nParty Visuals\n1 Start visuals\n2 Stop visuals\n3 Status / diagnostics\n4 Open Music Assistant controls\n5 Manual mode\n6 Follow Party (Start enables following)\n0 Close menu\nClosing this menu leaves visuals running.")
		if !in.Scan() {
			return in.Err()
		}
		command := ""
		switch strings.TrimSpace(in.Text()) {
		case "0":
			return nil
		case "1":
			command = "start"
		case "2":
			command = "stop"
		case "3":
		case "4":
			command = "show"
		case "5":
			command = "manual"
		case "6":
			command = "follow-party"
		default:
			continue
		}
		if e := invoke(root, command); e != nil {
			fmt.Fprintln(os.Stderr, e)
		}
	}
}
func app(root string) error {
	c, e := worker.Load(filepath.Join(root, "config.json"))
	if e != nil {
		return e
	}
	l, e := owner.Acquire(root)
	if e != nil {
		return e
	}
	defer l.Close()
	logFile, e := openLog(root)
	if e != nil {
		return e
	}
	defer logFile.Close()
	if e = releaseAppConsole(logFile); e != nil {
		return e
	}
	logger := log.New(logFile, "", log.LstdFlags)
	logger.Printf("controller %s started", version)
	var m *lifecycle.Manager
	var source *partysource.Source
	if c.HomeAssistantURL != "" {
		source = partysource.New(partysource.Config{URL: c.HomeAssistantURL, TokenFile: c.HomeAssistantTokenFile, Entity: c.PartyEntity, Value: c.PartyValue, Poll: time.Duration(c.PartyPollMs) * time.Millisecond}, func(command string) error { return m.Apply(command) }, logger.Printf)
	}
	var eventMu sync.Mutex
	var eventGeneration uint64
	var t tray.Tray
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var diagMu sync.Mutex
	diag := worker.Diagnostics{Screen: "unknown", Capture: "unknown", Audio: "unknown", OpenRGB: "unknown"}
	status := func() any {
		diagMu.Lock()
		d := diag
		diagMu.Unlock()
		return statusView{version, m.Snapshot(), d, source.Status()}
	}
	refresh := func(s lifecycle.Status) {
		if t == nil {
			return
		}
		diagMu.Lock()
		d := diag
		diagMu.Unlock()
		t.SetTooltip("Party Visuals · " + s.Phase)
		items := []tray.Item{{Title: "Party Visuals · " + s.Phase, Disabled: true}, {Title: s.Reason, Disabled: true}, {ID: "start", Title: "Start visuals", Disabled: s.Phase == "starting" || s.Phase == "running"}, {ID: "stop", Title: "Stop visuals"}, {Separator: true}}
		for _, line := range diagnosticLines(d, source.Status()) {
			items = append(items, tray.Item{Title: line, Disabled: true})
		}
		t.SetMenu(append(items, tray.Item{ID: "show", Title: "Open Music Assistant controls"}, tray.Item{ID: "diagnostics", Title: "Diagnostics / interactive menu"}, tray.Item{Separator: true}, tray.Item{ID: "manual", Title: "Manual", Checked: s.Intent.Mode == "manual"}, tray.Item{ID: "follow-party", Title: "Follow Party", Checked: s.Intent.Mode == "follow-party"}, tray.Item{ID: "quit", Title: "Stop visuals and quit"}))
	}
	command := func(command string) error {
		switch command {
		case "quit":
			if e := m.Apply("stop"); e != nil {
				return e
			}
			go func() {
				time.Sleep(100 * time.Millisecond)
				cancel()
				if t != nil {
					t.Quit()
				}
			}()
			return nil
		case "show":
			return openURL(c.VisualizerURL)
		case "diagnostics":
			return openMenu(root)
		default:
			return m.Apply(command)
		}
	}
	t, e = tray.New(icon(), tray.Options{Name: "Party Visuals", Identifier: "io.tyclab.partyvisuals.controller", Tooltip: "Party Visuals", OnClick: func(c string) {
		go func() {
			if e := command(c); e != nil {
				t.Notify("Party Visuals", e.Error())
			}
		}()
	}, OnSystemEvent: func(event string) {
		if event == "display-change" && m.Snapshot().Suspended {
			return
		}
		eventMu.Lock()
		eventGeneration++
		generation := eventGeneration
		eventMu.Unlock()
		switch event {
		case "suspend", "resume-automatic":
			m.Apply("suspend")
		case "resume", "display-change":
			go func() {
				m.Apply("suspend")
				select {
				case <-ctx.Done():
				case <-time.After(2 * time.Second):
					eventMu.Lock()
					if eventGeneration == generation {
						m.Apply("resume")
					}
					eventMu.Unlock()
				}
			}()
		}
	}})
	if e != nil {
		return e
	}
	m, e = lifecycle.New(lifecycle.FileStore{Path: filepath.Join(root, "intent.json")}, worker.Backend{Config: c, ConfigPath: filepath.Join(root, "config.json")}, func(s lifecycle.Status) {
		source.SetActive(s.Intent.Mode == "follow-party")
		refresh(s)
	})
	if e != nil {
		return e
	}
	defer m.Close()
	server, e := owner.Listen(root, status, command)
	if e != nil {
		return e
	}
	defer server.Close()
	refresh(m.Snapshot())
	if source != nil {
		source.SetActive(m.Snapshot().Intent.Mode == "follow-party")
		go source.Run(ctx)
	}
	go func() {
		tick := time.NewTicker(3 * time.Second)
		defer tick.Stop()
		for {
			d := worker.Diagnose(ctx, c)
			diagMu.Lock()
			diag = d
			diagMu.Unlock()
			refresh(m.Snapshot())
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)
	go func() {
		select {
		case <-signals:
			cancel()
			t.Quit()
		case <-ctx.Done():
		}
	}()
	if e = m.Apply("restore"); e != nil {
		return e
	}
	return t.Run()
}

func diagnosticLines(d worker.Diagnostics, p partysource.Status) []string {
	return []string{"Screen: " + d.Screen, "Audio: " + d.Audio, "OpenRGB: " + d.OpenRGB, "Follow Party: " + p.Summary}
}

// openLog opens controller.log in root for appending, first moving a log
// over 1 MiB to controller.log.1.
func openLog(root string) (*os.File, error) {
	path := filepath.Join(root, "controller.log")
	if info, e := os.Stat(path); e == nil && info.Size() > 1024*1024 {
		os.Remove(path + ".1")
		if e = os.Rename(path, path+".1"); e != nil {
			return nil, e
		}
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
}
func openURL(raw string) error {
	u := strings.Split(raw, "#")[0]
	return desktopOpen(u)
}
func openMenu(root string) error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	cmd := exec.Command(exe, "--root", root, "menu")
	newConsole(cmd)
	return cmd.Start()
}
