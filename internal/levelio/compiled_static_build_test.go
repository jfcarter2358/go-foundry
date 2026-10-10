package levelio

// compiled_static_build_test.go — building the compiled surfaces on every core
// gives exactly the surfaces building them in order does.

import (
	"os"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/compile"
	"foundry/internal/world"
)

// manyFaces is the fixture's surfaces repeated until there are enough to be
// worth spreading over cores, each copy moved so no two are the same.
func manyFaces(t testing.TB) *CompiledLevel {
	t.Helper()
	c, err := CompileLevel(sealedLevel(), compile.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	base := c.Faces
	var faces []compile.Face
	for k := 0; len(faces) < 20*staticBuildBatch; k++ {
		for _, f := range base {
			g := f
			g.Points = make([]mgl32.Vec3, len(f.Points))
			for i, p := range f.Points {
				g.Points[i] = p.Add(mgl32.Vec3{float32(k) * 9.5, 0, 0})
			}
			// A third smooth, so the welded-normal path is exercised too.
			g.Smooth = k%3 == 0
			g.DoubleSided = k%5 == 0
			faces = append(faces, g)
		}
	}
	c.Faces = faces
	return c
}

func buildWith(t testing.TB, c *CompiledLevel, parallel bool) *world.Level {
	t.Helper()
	prev := parallelStaticBuild
	parallelStaticBuild = parallel
	defer func() { parallelStaticBuild = prev }()
	var lvl world.Level
	if err := buildStaticAll(c, &lvl, nil); err != nil {
		t.Fatal(err)
	}
	return &lvl
}

func TestTheParallelStaticBuildIsTheSerialOne(t *testing.T) {
	defer withHeadlessGFX()()
	c := manyFaces(t)
	serial := buildWith(t, c, false)
	parallel := buildWith(t, c, true)
	if d := firstDiff(reflect.ValueOf(serial.Static), reflect.ValueOf(parallel.Static), "static"); d != "" {
		t.Fatalf("the parallel build differs: %s", d)
	}
	built := 0
	for i := range parallel.Static.Surfaces {
		if len(parallel.Static.Surfaces[i].Geometry.Vertices) > 0 {
			built++
		}
	}
	if built < staticBuildBatch {
		t.Fatalf("only %d surfaces built", built)
	}
}

// BenchmarkBuildStaticGeometry builds every surface of the level in
// FOUNDRY_FBSP_BENCH (or the repeated fixture), in order and on every core.
func BenchmarkBuildStaticGeometry(b *testing.B) {
	defer withHeadlessGFX()()
	var c *CompiledLevel
	if p := os.Getenv("FOUNDRY_FBSP_BENCH"); p != "" {
		var err error
		if c, err = ReadCompiledLevel(p); err != nil {
			b.Fatal(err)
		}
	} else {
		c = manyFaces(b)
	}
	for _, mode := range []struct {
		name     string
		parallel bool
	}{{"serial", false}, {"parallel", true}} {
		b.Run(mode.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				buildWith(b, c, mode.parallel)
			}
		})
	}
}
