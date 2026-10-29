package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"github.com/tyclab/party-visuals/controller/internal/lifecycle"
	"github.com/tyclab/party-visuals/controller/internal/owner"
	"github.com/tyclab/party-visuals/controller/internal/worker"
	"github.com/tyclab/tycswap/desktop/tray"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var version = "dev"

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
		fmt.Printf("Configuration valid; worker %s; no processes started\n", filepath.Base(c.WorkerScript))
		return nil
	case "status", "diagnostics":
		b, e := owner.Call(*root, "")
		if e != nil {
			return e
		}
		fmt.Println(string(b))
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
	if e = releaseAppConsole(root); e != nil {
		return e
	}
	var m *lifecycle.Manager
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
		return struct {
			Version     string             `json:"version"`
			State       lifecycle.Status   `json:"state"`
			Diagnostics worker.Diagnostics `json:"diagnostics"`
		}{version, m.Snapshot(), d}
	}
	refresh := func(s lifecycle.Status) {
		if t == nil {
			return
		}
		diagMu.Lock()
		d := diag
		diagMu.Unlock()
		t.SetTooltip("Party Visuals · " + s.Phase)
		t.SetMenu([]tray.Item{{Title: "Party Visuals · " + s.Phase, Disabled: true}, {Title: s.Reason, Disabled: true}, {ID: "start", Title: "Start visuals", Disabled: s.Phase == "starting" || s.Phase == "running"}, {ID: "stop", Title: "Stop visuals"}, {Separator: true}, {Title: "Screen: " + d.Screen, Disabled: true}, {Title: "Audio: " + d.Audio, Disabled: true}, {Title: "OpenRGB: " + d.OpenRGB, Disabled: true}, {ID: "show", Title: "Open Music Assistant controls"}, {ID: "diagnostics", Title: "Diagnostics / interactive menu"}, {Separator: true}, {ID: "manual", Title: "Manual", Checked: s.Intent.Mode == "manual"}, {ID: "follow-party", Title: "Follow Party", Checked: s.Intent.Mode == "follow-party"}, {ID: "quit", Title: "Stop visuals and quit"}})
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
	m, e = lifecycle.New(lifecycle.FileStore{Path: filepath.Join(root, "intent.json")}, worker.Backend{Config: c, ConfigPath: filepath.Join(root, "config.json")}, refresh)
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
