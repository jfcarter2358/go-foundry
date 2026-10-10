package harness

import (
	"os"
	"strings"
	"testing"

	"foundry/internal/collision"
	"foundry/internal/flang"
	"foundry/internal/nav"
	"foundry/internal/world"
)

// BenchmarkSimStep is one whole frame of a real level (FOUNDRY_SIM_DIGEST_ROOT,
// _LEVEL and _WAKE as for TestSimDigest), after a warm-up so navigation is
// built and the enemies are about their business. FOUNDRY_SIM_DIGEST_SERIAL
// and _NOSLEEP switch the parallel and memoised paths off, for comparison.
func BenchmarkSimStep(b *testing.B) {
	root := os.Getenv("FOUNDRY_SIM_DIGEST_ROOT")
	if root == "" {
		b.Skip("FOUNDRY_SIM_DIGEST_ROOT not set")
	}
	level := os.Getenv("FOUNDRY_SIM_DIGEST_LEVEL")
	if level == "" {
		level = "level3"
	}
	if os.Getenv("FOUNDRY_SIM_DIGEST_SERIAL") != "" {
		defer func(e, m bool) { collision.ParallelEntityUpdate, world.ParallelEmitters = e, m }(collision.ParallelEntityUpdate, world.ParallelEmitters)
		collision.ParallelEntityUpdate, world.ParallelEmitters = false, false
	}
	if os.Getenv("FOUNDRY_SIM_DIGEST_NOSLEEP") != "" {
		defer func(s bool) { collision.SleepRestingBodies = s }(collision.SleepRestingBodies)
		collision.SleepRestingBodies = false
	}
	var seeds uint64
	flang.TestSeeds = func() uint64 { seeds++; return seeds * 0x9E3779B97F4A7C15 }
	defer func() { flang.TestSeeds = nil }()
	s, err := Open(Options{Project: root, Level: level, Compiled: true, God: true})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	for _, tag := range strings.Split(os.Getenv("FOUNDRY_SIM_DIGEST_WAKE"), ",") {
		if tag != "" {
			s.Wake(tag)
		}
	}
	for f := 0; f < 300; f++ {
		nav.Sync(s.Level)
		s.Step()
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		nav.Sync(s.Level)
		b.StartTimer()
		s.Step()
	}
}
