package worker

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

type Config struct {
	Version        int    `json:"version"`
	WorkerScript   string `json:"workerScript"`
	NodeExe        string `json:"nodeExe"`
	ChromeExe      string `json:"chromeExe"`
	NodecgRoot     string `json:"nodecgRoot"`
	BrowserProfile string `json:"browserProfile"`
	HelperPath     string `json:"helperPath"`
	VisualizerURL  string `json:"visualizerUrl"`
	ControlsURL    string `json:"controlsUrl"`
	NodecgConfig   string `json:"nodecgConfig"`
	TelemetryFile  string `json:"telemetryFile"`
	StatusFile     string `json:"statusFile"`
	LogFile        string `json:"logFile"`
	Curtain        bool   `json:"curtain"`
	Room           bool   `json:"room"`
	Panels         string `json:"panels"`
	Fixture        int    `json:"fixture"`
	FPS            int    `json:"fps"`
}

func Load(path string) (Config, error) {
	f, e := os.Open(path)
	if e != nil {
		return Config{}, e
	}
	defer f.Close()
	var c Config
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, e
	}
	if c.Version != 1 {
		return c, errors.New("unsupported controller configuration")
	}
	for name, p := range map[string]string{"workerScript": c.WorkerScript, "nodeExe": c.NodeExe, "chromeExe": c.ChromeExe, "helperPath": c.HelperPath} {
		if !filepath.IsAbs(p) {
			return c, fmt.Errorf("%s must be absolute", name)
		}
		if s, e := os.Stat(p); e != nil || s.IsDir() {
			return c, fmt.Errorf("%s is missing", name)
		}
	}
	for _, raw := range []string{c.VisualizerURL, c.ControlsURL} {
		u, e := url.Parse(raw)
		if e != nil || u.User != nil || u.RawQuery != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return c, errors.New("use HTTP URLs without credentials")
		}
	}
	u, _ := url.Parse(c.ControlsURL)
	if u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
		return c, errors.New("NodeCG controls must use loopback HTTP")
	}
	if c.BrowserProfile == "" || c.NodecgRoot == "" || c.LogFile == "" {
		return c, errors.New("profile, NodeCG root and log path are required")
	}
	return c, nil
}
