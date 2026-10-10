package nav_test

// signature_real_test.go — Signature on a level the game ships, since the
// synthetic bigLevel has neither the brush count nor the chunk layout of one.
//
// Opt-in: FOUNDRY_NAV_BENCH_ROOT is the game project (the directory holding
// resources/), FOUNDRY_NAV_BENCH_LEVEL the level (level3 by default). It sits
// in an external test package because it opens the level through the harness,
// which imports nav.
//
//	FOUNDRY_NAV_BENCH_ROOT=/mnt/c/Users/jcart/OneDrive/Documents/Foundry \
//	go test ./internal/nav -run '^$' -bench SignatureRealLevel

import (
	"os"
	"testing"

	"foundry/internal/harness"
	"foundry/internal/nav"
)

func BenchmarkSignatureRealLevel(b *testing.B) {
	root := os.Getenv("FOUNDRY_NAV_BENCH_ROOT")
	if root == "" {
		b.Skip("FOUNDRY_NAV_BENCH_ROOT not set")
	}
	level := os.Getenv("FOUNDRY_NAV_BENCH_LEVEL")
	if level == "" {
		level = "level3"
	}
	s, err := harness.Open(harness.Options{Project: root, Level: level, Compiled: true, ScriptsOnly: true})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	brushes, verts, faces := 0, 0, 0
	for ci := range s.Level.Chunks {
		for bi := range s.Level.Chunks[ci].Brushes {
			br := &s.Level.Chunks[ci].Brushes[bi]
			brushes++
			verts += len(br.Verts)
			faces += len(br.Faces)
		}
	}
	nav.Signature(s.Level)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nav.Signature(s.Level)
	}
	// After the loop: ResetTimer drops metrics reported before it.
	b.ReportMetric(float64(brushes), "brushes")
	b.ReportMetric(float64(verts), "verts")
	b.ReportMetric(float64(faces), "faces")
}
