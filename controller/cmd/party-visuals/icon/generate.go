//go:build ignore

// Generate renders krake-rainbow.svg to the embedded tray PNGs with headless
// Chrome or Chromium: CHROME=/path/to/chrome go generate ./cmd/party-visuals
package main

import (
	"fmt"
	"image/png"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Small sizes get wider strokes, and 16 px drops the face, so arms stay solid colour.
var sizes = []struct {
	px  int
	css string
}{
	{16, "--arm:24px;--face:none"},
	{24, "--arm:19px;--smile:14px"},
	{32, "--arm:17px;--smile:12px"},
	{48, "--arm:14px;--smile:10px"},
	{64, ""},
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "generate:", err)
		os.Exit(1)
	}
}

func run() error {
	chrome := os.Getenv("CHROME")
	if chrome == "" {
		return fmt.Errorf("set CHROME to a Chrome or Chromium executable")
	}
	svg, err := os.ReadFile(filepath.Join("icon", "krake-rainbow.svg"))
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "krake-icon-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, s := range sizes {
		page := filepath.Join(tmp, fmt.Sprintf("%d.html", s.px))
		shot := filepath.Join(tmp, fmt.Sprintf("%d.png", s.px))
		html := fmt.Sprintf("<!doctype html><style>html,body{margin:0;background:transparent;overflow:hidden}svg{display:block;width:%dpx;height:%dpx;%s}</style>%s", s.px, s.px, s.css, svg)
		if err := os.WriteFile(page, []byte(html), 0o644); err != nil {
			return err
		}
		path := filepath.ToSlash(page)
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		cmd := exec.Command(chrome, "--headless", "--disable-gpu", "--hide-scrollbars", "--force-device-scale-factor=1",
			"--default-background-color=00000000", fmt.Sprintf("--window-size=%d,%d", s.px, s.px), "--screenshot="+shot,
			(&url.URL{Scheme: "file", Path: path}).String())
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%d px: %v\n%s", s.px, err, out)
		}
		if err := reencode(shot, filepath.Join("icon", fmt.Sprintf("krake-rainbow-%d.png", s.px)), s.px); err != nil {
			return err
		}
	}
	return nil
}

// reencode checks the screenshot size and rewrites it without Chrome's metadata.
func reencode(from, to string, px int) error {
	f, err := os.Open(from)
	if err != nil {
		return err
	}
	defer f.Close()
	im, err := png.Decode(f)
	if err != nil {
		return err
	}
	if b := im.Bounds(); b.Dx() != px || b.Dy() != px {
		return fmt.Errorf("%s is %dx%d, want %d", from, b.Dx(), b.Dy(), px)
	}
	out, err := os.Create(to)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(out, im); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
