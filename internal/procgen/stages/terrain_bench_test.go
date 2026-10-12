package stages

// terrain_bench_test.go — a full terrain run, timed, from a document:
//
//	FOUNDRY_TERRAIN_BENCH=<doc.fgen> FOUNDRY_TERRAIN_BENCH_OUT=<dir> \
//	    go test ./internal/procgen/stages -run TestTerrainBench -v -timeout 30m
//
// The same procgen.Run `foundry gen run` makes (no splice), into a folder of
// its own, so the numbers in docs/canyon/PHASE_1.md can be remade with the
// editor binary out of reach. Wrap it in /usr/bin/time -v for the memory
// peak. Skipped without the variables.

import (
	"context"
	"os"
	"testing"
	"time"

	"foundry/internal/procgen"
)

func TestTerrainBench(t *testing.T) {
	doc, out := os.Getenv("FOUNDRY_TERRAIN_BENCH"), os.Getenv("FOUNDRY_TERRAIN_BENCH_OUT")
	if doc == "" || out == "" {
		t.Skip("set FOUNDRY_TERRAIN_BENCH (a .fgen) and FOUNDRY_TERRAIN_BENCH_OUT (a folder)")
	}
	d, err := procgen.ReadDoc(doc)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	res, err := procgen.Run(context.Background(), d, procgen.Options{OutDir: out, Force: true,
		Log: func(l string) { t.Log(l) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d placements in %s", d.Name, len(res.Placements()), time.Since(t0).Round(time.Millisecond))
}
