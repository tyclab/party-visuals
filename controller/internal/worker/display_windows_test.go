//go:build windows

package worker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWorkerReportsWhereItOpenedTheVisualsWithItsPowerReading(t *testing.T) {
	c, configPath := sessionFixture(t)
	c.PreferredDisplay = "No Such Display"
	data, _ := json.Marshal(c)
	if e := os.WriteFile(configPath, data, 0600); e != nil {
		t.Fatal(e)
	}
	var placed []Placement
	b := &Backend{Config: c, ConfigPath: configPath, Power: func() string { return "off" }, Placed: func(p Placement) { placed = append(placed, p) }}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	s, e := b.Start(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Stop(ctx)
	if len(placed) != 1 || placed[0] != b.Placement() {
		t.Fatalf("placements %+v, current %+v", placed, b.Placement())
	}
	if p := placed[0]; p.Power != "off" || p.PreferredListed || !strings.HasPrefix(p.Text, `\\.\DISPLAY`) || !strings.Contains(p.Text, "): the preferred display is not connected; ") {
		t.Fatalf("placement %+v", p)
	}
}
