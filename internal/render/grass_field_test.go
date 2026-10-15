package render

// grass_field_test.go — the grass instancer's CPU side without GL: which
// cells, runs and tiers a frame draws (grassFieldRenderer.gather).

import (
	"fmt"
	"strings"
	"testing"

	"foundry/internal/grassfield"

	"github.com/go-gl/mathgl/mgl32"
)

// gatherTestField is a 64 m field of 16 m cells, two kinds (one casting, two
// LODs; one not, three LODs), 20 instances per kind per cell.
func gatherTestField(t *testing.T) *grassFieldGPU {
	t.Helper()
	var ins []grassfield.Instance
	for c := 0; c < 16; c++ {
		cx, cz := float32(c%4)*16, float32(c/4)*16
		for i := 0; i < 40; i++ {
			ins = append(ins, grassfield.Instance{Kind: i % 2, Scale: 1,
				Pos: [3]float32{cx + 0.37*float32(i) + 0.1, 0, cz + float32(i%7)*2 + 0.2}})
		}
	}
	f, err := grassfield.BuildField(grassfield.FieldSpec{Kit: "k", Min: [2]float32{0, 0}, Max: [2]float32{64, 64}, Cell: 16, Kinds: 2}, ins)
	if err != nil {
		t.Fatal(err)
	}
	k := &grassKitGPU{
		lods:   [][]grassMeshRef{{{0, 0, 30}, {10, 30, 12}}, {{20, 42, 60}, {30, 102, 24}, {40, 126, 6}}},
		group:  []int{0, 1},
		groups: []grassGroup{{texture: "a", casts: true, lods: 2}, {texture: "b", lods: 3}},
	}
	k.tallest = 1
	return &grassFieldGPU{field: f, kit: k, top: 2}
}

func TestGrassGatherCullsAndThins(t *testing.T) {
	gf := gatherTestField(t)
	r := &grassFieldRenderer{}
	fo := grassfield.DefaultFalloff

	// Standing in the middle: every cell is near, every run drawn whole
	// (inside the thinning start), tier 0 at least.
	inst, cells := r.gather(gf, mgl32.Vec3{32, 1.7, 32}, nil, fo, fo.Range, false)
	if cells != 16 || inst < 640 {
		t.Fatalf("in the middle: %d cells, %d instances", cells, inst)
	}
	// Every command's prefix is DrawCount of its run at the cell's distance
	// and lies inside its run.
	for _, c := range r.cmds {
		if c.instances == 0 || c.instances > 20 {
			t.Fatalf("command %+v", c)
		}
	}

	// Far away: nothing.
	r.cmds, r.batches = r.cmds[:0], r.batches[:0]
	if inst, cells := r.gather(gf, mgl32.Vec3{5000, 2, 5000}, nil, fo, fo.Range, false); inst != 0 || cells != 0 {
		t.Fatalf("5 km away: %d instances in %d cells", inst, cells)
	}

	// A frustum looking away from the field: nothing.
	r.cmds, r.batches = r.cmds[:0], r.batches[:0]
	cam := mgl32.Vec3{-10, 2, 32}
	view := mgl32.LookAtV(cam, cam.Add(mgl32.Vec3{-1, 0, 0}), mgl32.Vec3{0, 1, 0})
	fr := ExtractFrustum(mgl32.Perspective(1.2, 1.5, 0.1, 1000).Mul4(view))
	if inst, _ := r.gather(gf, cam, &fr, fo, fo.Range, false); inst != 0 {
		t.Fatalf("looking away: %d instances", inst)
	}
	// ... and toward it: all of it.
	r.cmds, r.batches = r.cmds[:0], r.batches[:0]
	view = mgl32.LookAtV(cam, cam.Add(mgl32.Vec3{1, 0, 0}), mgl32.Vec3{0, 1, 0})
	fr = ExtractFrustum(mgl32.Perspective(1.6, 1.5, 0.1, 1000).Mul4(view))
	if _, cells := r.gather(gf, cam, &fr, fo, fo.Range, false); cells != 16 {
		t.Fatalf("looking at it: %d cells", cells)
	}

	// Thinning: from 120 m off, fewer instances than up close, each run's
	// prefix the falloff's.
	r.cmds, r.batches = r.cmds[:0], r.batches[:0]
	farInst, _ := r.gather(gf, mgl32.Vec3{-90, 2, 32}, nil, fo, fo.Range, false)
	if farInst == 0 || farInst >= inst {
		t.Fatalf("from 90 m: %d instances, from inside %d", farInst, inst)
	}
	// Lower density, fewer still.
	r.cmds, r.batches = r.cmds[:0], r.batches[:0]
	half, _ := r.gather(gf, mgl32.Vec3{32, 1.7, 32}, nil, fo.Scaled(1, 0.25), fo.Range, false)
	if half*3 > inst {
		t.Fatalf("density 0.25 drew %d of %d", half, inst)
	}
}

func TestGrassGatherTiersAndShadows(t *testing.T) {
	gf := gatherTestField(t)
	r := &grassFieldRenderer{}
	fo := grassfield.DefaultFalloff
	// 30 m from the near edge: past LOD1, so the three-LOD kind draws its
	// mid tier (and its far one where the span reaches it), never only the
	// near.
	r.gather(gf, mgl32.Vec3{-30, 2, 32}, nil, fo, fo.Range, false)
	tiers := map[[2]int]bool{}
	for _, b := range r.batches {
		tiers[[2]int{b.group, b.tier}] = true
		want := fo.Tiers(gf.kit.groups[b.group].lods)[b.tier]
		if b.window != want {
			t.Fatalf("batch %d/%d window %+v, want %+v", b.group, b.tier, b.window, want)
		}
	}
	if !tiers[[2]int{1, 1}] || tiers[[2]int{0, 2}] {
		t.Fatalf("tiers drawn %v", tiers)
	}
	// Shadows: the casting group only, within reach, one tier (its mid
	// mesh), open window.
	r.cmds, r.batches = r.cmds[:0], r.batches[:0]
	inst, _ := r.gather(gf, mgl32.Vec3{8, 1.7, 8}, nil, fo, 18, true)
	if inst == 0 {
		t.Fatal("nothing cast near the camera")
	}
	for _, b := range r.batches {
		if b.group != 0 || b.tier != 0 || b.window != (grassfield.Tier{}) {
			t.Fatalf("shadow batch %+v", b)
		}
	}
	for _, c := range r.cmds {
		if c.count != 12 {
			t.Fatalf("a shadow draws the casting kind's coarsest mesh (12 indices), not %d", c.count)
		}
	}
}

// The vertex stage's copy of the falloff constants is grassfield's.
func TestGrassShaderConstantsMatchGo(t *testing.T) {
	withRepoResources(t)
	src := mustReadShader(biomeShaderPath("grassfield_common.glsl"))
	for name, want := range map[string]float64{"GRASS_RANK_BAND": grassfield.RankBand,
		"GRASS_MAX_COMP": grassfield.MaxComp, "GRASS_TIER_BAND": grassfield.TierBand} {
		var got float64
		i := strings.Index(src, "const float "+name+" = ")
		if i < 0 {
			t.Fatalf("%s missing from grassfield_common.glsl", name)
		}
		if _, err := fmt.Sscanf(src[i+len("const float "+name+" = "):], "%g", &got); err != nil || got != want {
			t.Errorf("%s is %g in the shader, %g in Go", name, got, want)
		}
	}
}
