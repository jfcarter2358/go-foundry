package cluster

// determinism_test.go — the same mesh builds the same DAG, bit for bit, every
// time: a compiled level or a cooked mesh is only reproducible if it does.

import (
	"bytes"
	"encoding/gob"
	"math/rand"
	"testing"
)

func encodeDAG(t *testing.T, d DAG) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(d); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Map iteration differs build to build, so one build compared with one more
// can pass by luck; several give an order-dependent float sum walked in map
// order more chances to show. (The border planes' quadrics once were one: a
// real level's groups came out different about one build in a few hundred.
// These meshes did not catch that one — it took a level's worth of geometry
// — so this is a guard, not a proof.)
func TestDAGIsTheSameBitForBitEveryBuild(t *testing.T) {
	for _, m := range []Mesh{lumpySphere(4, true), bumpySoup(48)} {
		opt := DAGOptions{AttrWeights: []float32{0.2, 0.2}}
		want := encodeDAG(t, BuildDAG(m, opt))
		for i := 0; i < 6; i++ {
			if got := encodeDAG(t, BuildDAG(m, opt)); !bytes.Equal(got, want) {
				t.Fatalf("%d triangles: build %d differs from the first", m.TriangleCount(), i+2)
			}
		}
	}
}

// bumpySoup is an open n×n grid of quads, uneven heights, as a corner soup
// (x, y, z, u, v a corner) — what a compiled level's surfaces are: every
// edge a border or a seam, so every vertex sums several border planes.
func bumpySoup(n int) Mesh {
	rng := rand.New(rand.NewSource(7))
	h := make([]float32, (n+1)*(n+1))
	for i := range h {
		h[i] = rng.Float32() * 0.3
	}
	corner := func(i, j int) []float32 {
		return []float32{-120 + float32(i)*0.37, 4.5 + h[j*(n+1)+i], 152 + float32(j)*0.41, float32(i) / float32(n), float32(j) / float32(n)}
	}
	var pos []float32
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			for _, c := range [][2]int{{i, j}, {i, j + 1}, {i + 1, j + 1}, {i, j}, {i + 1, j + 1}, {i + 1, j}} {
				pos = append(pos, corner(c[0], c[1])...)
			}
		}
	}
	return Mesh{Positions: pos, Stride: 5}
}
