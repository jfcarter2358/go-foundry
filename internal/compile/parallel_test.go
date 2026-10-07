package compile

// parallel_test.go — the compile's fast paths give the slow paths' answers, to
// the bit.
//
// Each speed-up in the compile (parallel loops, packed ray tests, replayed
// bounce rays) has a package switch back to the plain way. These tests run a
// level both ways and demand identical output: not close, identical, because
// a compile that differs run to run makes every diff of a compiled level
// unreadable.

import (
	"math"
	"math/rand"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/bsp"
	"foundry/internal/world"
)

// withSwitch sets *sw to v for the length of the test.
func withSwitch[T any](t testing.TB, sw *T, v T) {
	old := *sw
	*sw = v
	t.Cleanup(func() { *sw = old })
}

// bakedField compiles and bakes benchField(n) with bounces bounces.
func bakedField(t *testing.T, n, bounces int) *BSPTree {
	t.Helper()
	lvl := benchField(n)
	lvl.Lights.Global.Bounces = bounces
	faces, err := CSG(lvl)
	if err != nil {
		t.Fatal(err)
	}
	tr := BuildBSP(faces)
	BakeLight(tr, lvl, DefaultLightOptions())
	return tr
}

// sameBits fails unless every float of a and b is the same bit pattern.
func sameBits(t *testing.T, what string, a, b []mgl32.Vec3) {
	t.Helper()
	if len(a) != len(b) || (a == nil) != (b == nil) {
		t.Fatalf("%s: %d values against %d", what, len(a), len(b))
	}
	for i := range a {
		for c := 0; c < 3; c++ {
			if math.Float32bits(a[i][c]) != math.Float32bits(b[i][c]) {
				t.Fatalf("%s[%d]: %v against %v", what, i, a[i], b[i])
			}
		}
	}
}

// sameTrees fails unless two compiled trees are identical, light to the bit.
func sameTrees(t *testing.T, a, b *BSPTree) {
	t.Helper()
	if len(a.Faces) != len(b.Faces) {
		t.Fatalf("%d faces against %d", len(a.Faces), len(b.Faces))
	}
	for fi := range a.Faces {
		fa, fb := &a.Faces[fi], &b.Faces[fi]
		sameBits(t, "Light", fa.Light, fb.Light)
		sameBits(t, "BackLight", fa.BackLight, fb.BackLight)
		sameBits(t, "SmoothLight", fa.SmoothLight, fb.SmoothLight)
		sameBits(t, "SmoothBackLight", fa.SmoothBackLight, fb.SmoothBackLight)
		sameBits(t, "Indirect", fa.Indirect, fb.Indirect)
		sameBits(t, "BackIndirect", fa.BackIndirect, fb.BackIndirect)
		sameBits(t, "SmoothIndirect", fa.SmoothIndirect, fb.SmoothIndirect)
		sameBits(t, "SmoothBackIndirect", fa.SmoothBackIndirect, fb.SmoothBackIndirect)
	}
	if !reflect.DeepEqual(a.Faces, b.Faces) {
		t.Fatal("the faces differ")
	}
	if !reflect.DeepEqual(a.Tree, b.Tree) {
		t.Fatal("the trees differ")
	}
}

// The packed ray test is the Face test: same hit, same distance, to the bit,
// for both the bounce's and the shadow's slack.
func TestPackedFaceTestIsTheFaceTest(t *testing.T) {
	lvl := benchField(2)
	faces, err := CSG(lvl)
	if err != nil {
		t.Fatal(err)
	}
	tr := BuildBSP(faces)
	tr.SubdivideFaces(DefaultLightOptions().GridStep)
	bv := buildFaceBVH(tr, true)
	rng := rand.New(rand.NewSource(7))
	tested, hit := 0, 0
	for j := range bv.items {
		f := &bv.faces[bv.items[j]]
		for r := 0; r < 40; r++ {
			// Aimed near the face, a good share of them right along its edges.
			target := f.Points[rng.Intn(len(f.Points))]
			if r%2 == 0 {
				target = target.Add(mgl32.Vec3{rng.Float32() - 0.5, rng.Float32() - 0.5, rng.Float32() - 0.5}.Mul(0.05))
			}
			o := target.Add(mgl32.Vec3{rng.Float32()*8 - 4, rng.Float32()*8 - 4, rng.Float32()*8 - 4})
			d := target.Sub(o)
			if d.Len() < 1e-3 {
				continue
			}
			d = d.Normalize()
			for _, sl := range [][2]float32{{1e-4, rayMinT}, {shadowSlack, -shadowSlack}} {
				wt, wok := rayHitsFaceWithin(f, o, d, sl[0], sl[1])
				gt, gok := bv.hits(int32(j), o, d, sl[0], sl[1])
				if wok != gok || math.Float32bits(wt) != math.Float32bits(gt) {
					t.Fatalf("face %d: Face test %v,%v, packed %v,%v", bv.items[j], wt, wok, gt, gok)
				}
				tested++
				if wok {
					hit++
				}
			}
		}
	}
	if hit == 0 || hit == tested {
		t.Fatalf("%d of %d rays hit: the test is not exercising both answers", hit, tested)
	}
}

// Replaying the first bounce's rays gives what casting them again gives.
func TestReplayedBounceRaysAreCastRays(t *testing.T) {
	withBenchAlbedos(t)
	replayed := bakedField(t, 2, 3)
	withSwitch(t, &replayBounceRays, false)
	cast := bakedField(t, 2, 3)
	sameTrees(t, replayed, cast)
}

// A face whose log does not fit the budget is cast again, and the answer is
// still the same.
func TestBounceReplayBudgetOnlyCostsTime(t *testing.T) {
	withBenchAlbedos(t)
	withSwitch(t, &bounceReplayBudget, 20000)
	partial := bakedField(t, 2, 2)
	withSwitch(t, &replayBounceRays, false)
	cast := bakedField(t, 2, 2)
	sameTrees(t, partial, cast)
}

func TestParallelLightSumIsSerial(t *testing.T) {
	withBenchAlbedos(t)
	par := bakedField(t, 2, 1)
	withSwitch(t, &parallelLightSum, false)
	ser := bakedField(t, 2, 1)
	sameTrees(t, par, ser)
}

// streamedField is benchField(n) dealt into three chunks, the second of them
// streamed, so CSG has cross-chunk clips to report and cover faces to keep.
func streamedField(n int) *world.Level {
	lvl := benchField(n)
	all := lvl.Chunks[0].Brushes
	lvl.Chunks = []world.Chunk{{ID: "a"}, {ID: "b", Streamed: true}, {ID: "c"}}
	for i, b := range all {
		ch := &lvl.Chunks[i%3]
		ch.Brushes = append(ch.Brushes, b)
	}
	return lvl
}

// pillaredRooms is n×n closed rooms with pillars standing in them: plenty of
// portals, every flood shut in its own room, so the exact visibility is quick.
func pillaredRooms(n int) *world.Level {
	rng := rand.New(rand.NewSource(3))
	var brushes []world.Brush
	for x := 0; x < n; x++ {
		for z := 0; z < n; z++ {
			lo := mgl32.Vec3{float32(x) * 12, 0, float32(z) * 12}
			id := "r" + itoa(x*n+z)
			brushes = append(brushes, world.NewHollowRoom(id, lo, lo.Add(mgl32.Vec3{10, 4, 10}), 0.5, "white")...)
			for k := 0; k < 3; k++ {
				c := lo.Add(mgl32.Vec3{1.5 + rng.Float32()*7, 0, 1.5 + rng.Float32()*7})
				brushes = append(brushes, boxFromTo(id+"p"+itoa(k), c.Sub(mgl32.Vec3{0.4, 0, 0.4}), c.Add(mgl32.Vec3{0.4, 4, 0.4})))
			}
		}
	}
	return &world.Level{Chunks: []world.Chunk{{ID: "rooms", Brushes: brushes}}}
}

// CSG in parallel through the solid index is the serial walk over every
// solid: the same faces in the same order, the same streamed reports.
func TestParallelIndexedCSGIsTheSerialWalk(t *testing.T) {
	lvl := streamedField(4)
	fast, fastClips, err := csg(lvl)
	if err != nil {
		t.Fatal(err)
	}
	withSwitch(t, &parallelCSG, false)
	withSwitch(t, &indexSolids, false)
	slow, slowClips, err := csg(lvl)
	if err != nil {
		t.Fatal(err)
	}
	if len(slowClips) == 0 {
		t.Fatal("no streamed clips: the test is not exercising the reports")
	}
	covered := 0
	for i := range slow {
		if slow[i].CoveredBy != nil {
			covered++
		}
	}
	if covered == 0 {
		t.Fatal("no cover faces: the test is not exercising them")
	}
	if !reflect.DeepEqual(fast, slow) {
		t.Fatalf("the faces differ: %d against %d", len(fast), len(slow))
	}
	if !reflect.DeepEqual(fastClips, slowClips) {
		t.Fatalf("the streamed clips differ:\n%v\n%v", fastClips, slowClips)
	}
	for i := range fastClips {
		if math.Float32bits(fastClips[i].Area) != math.Float32bits(slowClips[i].Area) {
			t.Fatalf("clip %d's area: %v against %v", i, fastClips[i].Area, slowClips[i].Area)
		}
	}
}

// Scoring the splitter candidates in parallel picks the planes the serial
// scoring did: the same tree.
func TestParallelSplitterScoringIsSerial(t *testing.T) {
	faces, err := CSG(benchField(4))
	if err != nil {
		t.Fatal(err)
	}
	withSwitch(t, &parallelSplitterFaces, 8)
	fast := BuildBSP(faces)
	withSwitch(t, &parallelSplitterScore, false)
	slow := BuildBSP(faces)
	if !reflect.DeepEqual(fast, slow) {
		t.Fatal("the trees differ")
	}
}

// The candidate floods, the coarse rows and the padded wave rows all give the
// serial answers.
func TestParallelVisibilityIsSerial(t *testing.T) {
	faces, err := CSG(benchField(2))
	if err != nil {
		t.Fatal(err)
	}
	tr := BuildBSP(faces)
	portals := BuildPortals(tr)
	t.Logf("%d portals", len(portals))
	byLeaf := make([][]int, len(tr.Leaves))
	for i := range portals {
		byLeaf[portals[i].A] = append(byLeaf[portals[i].A], i)
	}
	state := func() *visState {
		s := &visState{portals: portals, byLeaf: byLeaf, portalBytes: (len(portals) + 7) / 8}
		s.buildMightSee()
		return s
	}
	coarse := func(s *visState) *bsp.PVS {
		v := bsp.NewPVS(len(tr.Leaves))
		s.coarseRows(v)
		return v
	}
	fast := state()
	fastRows := coarse(fast)
	withSwitch(t, &parallelMightSee, false)
	withSwitch(t, &parallelCoarseRows, false)
	slow := state()
	slowRows := coarse(slow)
	if !reflect.DeepEqual(fast.mightSee, slow.mightSee) {
		t.Fatal("mightSee differs")
	}
	if !reflect.DeepEqual(fastRows, slowRows) {
		t.Fatal("the coarse rows differ")
	}

	// The wave path, which a level this size would not take on its own.
	faces, err = CSG(pillaredRooms(4))
	if err != nil {
		t.Fatal(err)
	}
	tr = BuildBSP(faces)
	portals = BuildPortals(tr)
	t.Logf("%d portals in the rooms", len(portals))
	withSwitch(t, &waveFromPortals, 0)
	if len(portals)/flowWaves < 2 {
		t.Fatalf("%d portals make waves of %d: too few to run in parallel", len(portals), len(portals)/flowWaves)
	}
	padded := BuildPVS(tr, portals)
	withSwitch(t, &padWaveRows, false)
	plain := BuildPVS(tr, portals)
	if !reflect.DeepEqual(padded, plain) {
		t.Fatal("padded wave rows change the PVS")
	}
}

// referenceNearest is faceBVH.nearest as it was before the packed faces and
// the remembered box entries: every node's box tested as it comes off the
// stack, every face tested through its Face.
func referenceNearest(bv *faceBVH, origin, dir mgl32.Vec3, skip int) (int, float32, bool) {
	var inv mgl32.Vec3
	for a := 0; a < 3; a++ {
		if dir[a] != 0 {
			inv[a] = 1 / dir[a]
		} else {
			inv[a] = float32(math.Inf(1))
		}
	}
	best, bestT := -1, float32(math.Inf(1))
	var stack [64]int32
	sp := 1
	for sp > 0 {
		sp--
		n := &bv.nodes[stack[sp]]
		if _, ok := enterBox(origin, inv, n.lo, n.hi, bestT); !ok {
			continue
		}
		if n.count > 0 {
			for _, fi := range bv.items[n.left : n.left+n.count] {
				if int(fi) == skip {
					continue
				}
				if t, ok := rayHitsFace(&bv.faces[fi], origin, dir); ok && t < bestT {
					best, bestT = int(fi), t
				}
			}
			continue
		}
		l, r := &bv.nodes[n.left], &bv.nodes[n.right]
		tl, okl := enterBox(origin, inv, l.lo, l.hi, bestT)
		tr, okr := enterBox(origin, inv, r.lo, r.hi, bestT)
		switch {
		case okl && okr:
			if tl < tr {
				stack[sp], stack[sp+1] = n.right, n.left
			} else {
				stack[sp], stack[sp+1] = n.left, n.right
			}
			sp += 2
		case okl:
			stack[sp] = n.left
			sp++
		case okr:
			stack[sp] = n.right
			sp++
		}
	}
	return best, bestT, best >= 0
}

// referenceOccluded is faceBVH.occluded testing every face through its Face.
func referenceOccluded(bv *faceBVH, origin, dir mgl32.Vec3, maxT float32, skip int, buried bool) bool {
	var inv mgl32.Vec3
	for a := 0; a < 3; a++ {
		if dir[a] != 0 {
			inv[a] = 1 / dir[a]
		} else {
			inv[a] = float32(math.Inf(1))
		}
	}
	var stack [64]int32
	sp := 1
	for sp > 0 {
		sp--
		n := &bv.nodes[stack[sp]]
		if _, ok := enterBox(origin, inv, n.lo, n.hi, maxT); !ok {
			continue
		}
		if n.count == 0 {
			stack[sp], stack[sp+1] = n.left, n.right
			sp += 2
			continue
		}
		for _, fi := range bv.items[n.left : n.left+n.count] {
			f := &bv.faces[fi]
			if int(fi) == skip || (!buried && !f.DoubleSided && f.Plane.N.Dot(dir) >= 0) {
				continue
			}
			if t, ok := rayHitsFaceWithin(f, origin, dir, shadowSlack, -shadowSlack); ok && t < maxT {
				return true
			}
		}
	}
	return false
}

// The BVH's queries answer as the reference walk does, for rays fanned out
// from every corner of a subdivided field — where shared edges and equal
// distances are everywhere.
func TestFaceBVHQueriesMatchTheReferenceWalk(t *testing.T) {
	faces, err := CSG(benchField(3))
	if err != nil {
		t.Fatal(err)
	}
	tr := BuildBSP(faces)
	tr.SubdivideFaces(DefaultLightOptions().GridStep)
	bv := buildFaceBVH(tr, true)
	fan := cosineFan(24)
	rays, hits, blocked := 0, 0, 0
	for fi := range tr.Faces {
		f := &tr.Faces[fi]
		for pi, p := range f.Points {
			o := p.Add(f.Plane.N.Mul(float32(pi%3) * 0.02))
			tn, bn := basisAbout(f.Plane.N)
			for _, l := range fan {
				dir := tn.Mul(l.X()).Add(bn.Mul(l.Y())).Add(f.Plane.N.Mul(l.Z()))
				wi, wt, wok := referenceNearest(bv, o, dir, fi)
				gi, gt, gok := bv.nearest(o, dir, fi)
				if wi != gi || wok != gok || math.Float32bits(wt) != math.Float32bits(gt) {
					t.Fatalf("nearest from face %d: reference %d %v %v, got %d %v %v", fi, wi, wt, wok, gi, gt, gok)
				}
				for _, buried := range []bool{false, true} {
					w := referenceOccluded(bv, o, dir, 30, fi, buried)
					if g := bv.occluded(o, dir, 30, fi, buried); g != w {
						t.Fatalf("occluded from face %d: reference %v, got %v", fi, w, g)
					}
					if w {
						blocked++
					}
				}
				rays++
				if wok {
					hits++
				}
			}
		}
	}
	if hits == 0 || hits == rays || blocked == 0 {
		t.Fatalf("%d rays, %d hits, %d blocked: not exercising both answers", rays, hits, blocked)
	}
}
