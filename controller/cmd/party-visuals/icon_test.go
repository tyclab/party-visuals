package main

import (
	"bytes"
	"image/png"
	"io/fs"
	"testing"
)

func TestEmbeddedIconsDecodeAtTheirSizes(t *testing.T) {
	files, e := fs.Glob(icons, "icon/*.png")
	if e != nil || len(files) != len(iconSizes) {
		t.Fatalf("embedded icons %v (%v), want %d", files, e, len(iconSizes))
	}
	for _, size := range iconSizes {
		b, e := iconPNG(size)
		if e != nil {
			t.Fatalf("%d px: %v", size, e)
		}
		c, e := png.DecodeConfig(bytes.NewReader(b))
		if e != nil || c.Width != size || c.Height != size {
			t.Fatalf("%d px decodes as %dx%d (%v)", size, c.Width, c.Height, e)
		}
	}
}

func TestTrayUsesTheRainbowMark(t *testing.T) {
	want, e := iconPNG(trayIconSize)
	if e != nil {
		t.Fatal(e)
	}
	if got := icon().PNG; !bytes.Equal(got, want) {
		t.Fatal("tray icon is not the embedded rainbow mark")
	}
	if _, e := iconPNG(20); e == nil {
		t.Fatal("a missing size must fail so the disc fallback applies")
	}
	im, e := png.Decode(bytes.NewReader(discPNG()))
	if e != nil || im.Bounds().Dx() != 32 {
		t.Fatalf("fallback disc does not decode: %v", e)
	}
}
