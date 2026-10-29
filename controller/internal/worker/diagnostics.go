package worker

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

type Diagnostics struct {
	Screen     string    `json:"screen"`
	Capture    string    `json:"capture"`
	Audio      string    `json:"audio"`
	NodeCG     string    `json:"nodecg"`
	OpenRGB    string    `json:"openrgb"`
	RGBDevices uint32    `json:"rgbDevices,omitempty"`
	Outputs    []Output  `json:"outputs,omitempty"`
	Time       time.Time `json:"time"`
}
type Output struct {
	Name      string `json:"name"`
	Armed     bool   `json:"armed"`
	Connected bool   `json:"connected"`
	Leased    bool   `json:"leased"`
	Reason    string `json:"reason"`
}
type helperStatus struct {
	Version int       `json:"version"`
	Time    time.Time `json:"time"`
	Stopped bool      `json:"stopped"`
	Display struct {
		Live    bool   `json:"live"`
		Visible bool   `json:"visible"`
		Reason  string `json:"reason"`
	} `json:"display"`
	Browser struct {
		Connected bool `json:"connected"`
		Ready     bool `json:"ready"`
	} `json:"browser"`
	Outputs []Output `json:"outputs"`
}

func Diagnose(ctx context.Context, c Config) Diagnostics {
	d := Diagnostics{Time: time.Now().UTC(), Screen: "unknown", Capture: "unknown", Audio: "unknown", NodeCG: "unavailable", OpenRGB: "unavailable"}
	if f, e := os.Open(c.StatusFile); e == nil {
		var s helperStatus
		e = json.NewDecoder(io.LimitReader(f, 65536)).Decode(&s)
		f.Close()
		if e == nil && s.Version == 1 && time.Since(s.Time) >= 0 && time.Since(s.Time) < 5*time.Second {
			d.Outputs = s.Outputs
			if s.Stopped {
				d.Screen = "stopped"
				d.Capture = "stopped"
			} else if s.Display.Live {
				d.Screen = "ready"
				d.Capture = "canvas available"
			} else {
				d.Screen = s.Display.Reason
				d.Capture = "no live canvas"
			}
		} else {
			d.Screen = "status stale"
			d.Capture = "unknown"
		}
	}
	r, e := http.NewRequestWithContext(ctx, "GET", c.ControlsURL, nil)
	if e == nil {
		client := http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		if res, e := client.Do(r); e == nil {
			defer res.Body.Close()
			if res.StatusCode == 200 {
				var state struct {
					Connection struct {
						Status string `json:"status"`
					} `json:"connection"`
					Audio *struct {
						At int64 `json:"at"`
					} `json:"audio"`
				}
				if json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&state) == nil {
					d.NodeCG = state.Connection.Status
					if state.Audio != nil && time.Now().UnixMilli()-state.Audio.At < 5000 && time.Now().UnixMilli() >= state.Audio.At {
						d.Audio = "analysis frames arriving"
					} else {
						d.Audio = "no recent analysis frame; check playback source"
					}
				}
			}
		}
	}
	if count, e := rgbCount(ctx); e == nil {
		d.OpenRGB = "SDK reachable (shared; never stopped)"
		d.RGBDevices = count
	}
	return d
}
func rgbCount(ctx context.Context) (uint32, error) {
	d := net.Dialer{Timeout: time.Second}
	c, e := d.DialContext(ctx, "tcp4", "127.0.0.1:6742")
	if e != nil {
		return 0, e
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	p := make([]byte, 16)
	copy(p, "ORGB")
	if _, e = c.Write(p); e != nil {
		return 0, e
	}
	for i := 0; i < 4; i++ {
		if _, e = io.ReadFull(c, p); e != nil {
			return 0, e
		}
		n := binary.LittleEndian.Uint32(p[12:])
		if string(p[:4]) != "ORGB" || n > 65536 {
			return 0, io.ErrUnexpectedEOF
		}
		b := make([]byte, n)
		if _, e = io.ReadFull(c, b); e != nil {
			return 0, e
		}
		if binary.LittleEndian.Uint32(p[8:]) == 0 && len(b) == 4 {
			return binary.LittleEndian.Uint32(b), nil
		}
	}
	return 0, io.ErrUnexpectedEOF
}
