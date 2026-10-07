package levelio

// compiled_clusters_bench_test.go — the last stage of a compile, cutting a
// real compiled level's surfaces into clusters (CutCompiledClusters), timed
// and dumped. Both need a real level, which the repo does not ship, so both
// read FOUNDRY_BENCH_FBSP (a .fbsp path) and skip without it.
//
// FOUNDRY_CLUSTER_DUMP names a file TestDumpCompiledClusters writes the cut
// to, gob-encoded: run it on two builds and compare the files byte for byte
// to show a change to the cut's speed left its output alone.

import (
	"bytes"
	"encoding/gob"
	"os"
	"testing"
)

func benchCompiledLevel(tb testing.TB) *CompiledLevel {
	tb.Helper()
	path := os.Getenv("FOUNDRY_BENCH_FBSP")
	if path == "" {
		tb.Skip("FOUNDRY_BENCH_FBSP is not set")
	}
	c, err := ReadCompiledLevel(path)
	if err != nil {
		tb.Fatal(err)
	}
	return c
}

func BenchmarkCutCompiledClusters(b *testing.B) {
	c := benchCompiledLevel(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := CutCompiledClusters(c); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDumpCompiledClusters(t *testing.T) {
	out := os.Getenv("FOUNDRY_CLUSTER_DUMP")
	if out == "" {
		t.Skip("FOUNDRY_CLUSTER_DUMP is not set")
	}
	c := benchCompiledLevel(t)
	clusters, err := CutCompiledClusters(c)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(clusters); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
