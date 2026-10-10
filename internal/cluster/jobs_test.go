package cluster

// jobs_test.go — DAGOptions.Serial and BuildJobs: building on one goroutine,
// and several DAGs side by side, build exactly what the default does.

import (
	"bytes"
	"runtime"
	"sync"
	"testing"
)

func TestSerialDAGMatchesTheParallelOne(t *testing.T) {
	m := lumpySphere(4, true)
	opt := DAGOptions{AttrWeights: []float32{0.2, 0.2}}
	par := BuildDAG(m, opt)
	opt.Serial = true
	ser := BuildDAG(m, opt)
	if par.Levels < 3 {
		t.Fatalf("only %d levels: the test mesh should simplify further", par.Levels)
	}
	if !bytes.Equal(encodeDAG(t, par), encodeDAG(t, ser)) {
		t.Fatal("a DAG built on one goroutine differs from one built over the cores")
	}
}

func TestBuildJobsRunsEveryJobOnceAndBuildsTheSame(t *testing.T) {
	meshes := []Mesh{lumpySphere(4, true), lumpySphere(2, false), lumpySphere(3, true), lumpySphere(1, false), lumpySphere(3, false)}
	sizes := make([]int, len(meshes))
	for i, m := range meshes {
		sizes[i] = m.TriangleCount()
	}
	build := func(parallel bool) ([][]byte, []int, []bool) {
		out := make([]DAG, len(meshes))
		runs := make([]int, len(meshes))
		inners := make([]bool, len(meshes))
		var mu sync.Mutex
		BuildJobs(sizes, parallel, func(i int, inner bool) {
			out[i] = BuildDAG(meshes[i], DAGOptions{AttrWeights: []float32{0.2, 0.2}, Serial: !inner})
			mu.Lock()
			runs[i]++
			inners[i] = inner
			mu.Unlock()
		})
		enc := make([][]byte, len(out))
		for i := range out {
			enc[i] = encodeDAG(t, out[i])
		}
		return enc, runs, inners
	}
	serial, runs, inners := build(false)
	for i := range runs {
		if runs[i] != 1 || !inners[i] {
			t.Fatalf("serially, job %d ran %d times (inner %v); want once, free to fan out", i, runs[i], inners[i])
		}
	}
	side, runs, inners := build(true)
	for i := range runs {
		if runs[i] != 1 {
			t.Fatalf("side by side, job %d ran %d times", i, runs[i])
		}
	}
	if runtime.GOMAXPROCS(0) > 1 && !inners[0] {
		t.Fatal("the job bigger than a worker's share should have been free to fan out")
	}
	for i := range serial {
		if !bytes.Equal(serial[i], side[i]) {
			t.Fatalf("DAG %d built side by side differs from the one built alone", i)
		}
	}
}
