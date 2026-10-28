package main

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"github.com/tyclab/tycswap/desktop/tray"
)

//go:generate go run icon/generate.go

// icons holds the rainbow Krake mark rendered from icon/krake-rainbow.svg.
//
//go:embed icon/krake-rainbow-*.png
var icons embed.FS

var iconSizes = []int{16, 24, 32, 48, 64}

// trayIconSize matches tycswap's own tray: Windows scales this PNG to the notification area.
const trayIconSize = 32

func iconPNG(size int) ([]byte, error) {
	b, e := icons.ReadFile(fmt.Sprintf("icon/krake-rainbow-%d.png", size))
	if e != nil {
		return nil, e
	}
	im, e := png.Decode(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	if r := im.Bounds(); r.Dx() != size || r.Dy() != size {
		return nil, fmt.Errorf("icon %d px is %dx%d", size, r.Dx(), r.Dy())
	}
	return b, nil
}

func icon() tray.Icon {
	if b, e := iconPNG(trayIconSize); e == nil {
		return tray.Icon{PNG: b}
	}
	return tray.Icon{PNG: discPNG()}
}

func discPNG() []byte {
	im := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			c := color.RGBA{22, 20, 38, 255}
			if (x-16)*(x-16)+(y-16)*(y-16) < 150 {
				c = color.RGBA{142, 102, 255, 255}
			}
			im.SetRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, im)
	return b.Bytes()
}
