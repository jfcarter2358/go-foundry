package terrain

// dump_test.go — FOUNDRY_TERRAIN_DUMP=<dir> writes a generation's pictures
// (shaded relief, heights, every mask, albedo) for a person to look at:
//
//	FOUNDRY_TERRAIN_DUMP=/tmp/t go test ./internal/procgen/terrain -run TestDump -v
//
// FOUNDRY_TERRAIN_DUMP_CELL sets the resolution (metres, default 2) and
// FOUNDRY_TERRAIN_DUMP_SIZE the side (default 1024). Skipped without the
// variable: it is a looking aid, not a check.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDump(t *testing.T) {
	dir := os.Getenv("FOUNDRY_TERRAIN_DUMP")
	if dir == "" {
		t.Skip("set FOUNDRY_TERRAIN_DUMP to a folder to write the pictures")
	}
	s := Defaults()
	s.Seed = 1234
	s.Cell = 2
	if v, err := strconv.ParseFloat(os.Getenv("FOUNDRY_TERRAIN_DUMP_CELL"), 64); err == nil {
		s.Cell = v
	}
	if v, err := strconv.ParseFloat(os.Getenv("FOUNDRY_TERRAIN_DUMP_SIZE"), 64); err == nil {
		s.Size = v
	}
	s.Canyons = []Canyon{demoCanyon()}
	for _, off := range strings.Split(os.Getenv("FOUNDRY_TERRAIN_DUMP_OFF"), ",") {
		switch off {
		case "drainage":
			s.Drainage.Strength = 0
		case "droplets":
			s.Droplets.Density = 0
		case "thermal":
			s.Thermal.Iterations = 0
		case "terrace":
			s.Strata.Terrace = 0
		case "canyons":
			s.Canyons = nil
		}
	}
	if js := os.Getenv("FOUNDRY_TERRAIN_DUMP_JSON"); js != "" {
		if err := json.Unmarshal([]byte(js), &s); err != nil {
			t.Fatal(err)
		}
	}
	t0 := time.Now()
	res, err := Generate(&s, NewPool(context.Background(), 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("generated %d² in %v, %d droplets", res.Grid.S(), time.Since(t0), res.Droplets)
	for p, d := range res.Timings {
		t.Logf("  %-9s %v", p, d)
	}
	meta := NewMeta(&s, res)
	files, err := Encode(res, meta, NewPool(context.Background(), 0))
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(dir, 0o755)
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("heights %.1f .. %.1f m -> %s", meta.Height.Lo, meta.Height.Hi, dir)
}

// demoCanyon is a canyon across the map, for the pictures and the tests.
func demoCanyon() Canyon {
	return Canyon{
		Path: []PathPoint{
			{X: -560, Z: -260, Width: 90, Depth: 80},
			{X: -200, Z: -120, Width: 110, Depth: 95},
			{X: 120, Z: 60, Width: 130, Depth: 110},
			{X: 560, Z: 240, Width: 150, Depth: 120},
		},
		Profile: ProfileStepped, Steps: 3, Floor: 0.15,
		Meander: 40, MeanderLength: 260, Tributaries: 5, TributaryLength: 220, Seed: 99,
	}
}
