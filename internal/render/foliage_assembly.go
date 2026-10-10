package render

// foliage_assembly.go — a plant as an ASSEMBLY: part templates placed by
// transforms, and the bones it bends on. Phase 6 of the Nanite plan
// (docs/nanite/PHASE_6.md), "Nanite Assemblies" and "Nanite Skinning".
//
// # Parts
//
// The generators in foliage_mesh.go build a plant out of a handful of
// primitives — cards, blades, tubes, blobs, fronds — and the classic path
// draws what they leave behind as one merged triangle list. Most of a tree's
// triangles are CARDS, and every card is the same quad: a unit card of its
// atlas tile, stretched across `right` and up `up` from `base` and given its
// shading normal, tone and sway. That is an affine transform of one
// template, so the Nanite path keeps a card as exactly that — a PART: the
// template (one per atlas tile and colouring) and a transform — and the
// cluster cull places it (nfoliage_cull_comp.glsl), rather than storing a
// tree's worth of copies. Everything else (trunks, branches, blades, stones)
// stays as the plant's own CORE mesh.
//
// The assembly is recorded, not rebuilt: while recordFoliageAssembly runs a
// generator, every primitive helper notes the range of the merged list it
// wrote (asmRecording). So the merged list the classic path draws and the
// assembly are the same plant by construction — flattening the assembly
// gives back the merged list, which TestAnAssemblyFlattensToTheMergedPlant
// checks for every variant.
//
// # Bones
//
// The classic path bends a plant with a per-vertex SWAY weight: every vertex
// moves along the wind by sway² times a gust, in world space. The Nanite path
// bends it on BONES, posed by a compute pass each frame
// (nfoliage_bones_comp.glsl), so a branch swings about where it leaves the
// trunk and its leaves go with it, and the motion vectors TAA reprojects
// with are each vertex's own (render/taa.go).
//
// The bones come from the plant's own structure: every primitive that grows
// along an axis — a tube (trunk, branch, stem), a blade, a frond — is a bone
// from its first point to its last. Its parent is the earlier bone it grows
// out of: the one whose axis passes nearest its root, somewhere along that
// axis rather than at its base (two grass blades from one root are siblings,
// not parent and child). A plant's bone 0 is its ROOT, which never moves.
// Everything that does not grow along an axis — a card, a stone, a coconut —
// rides the bone nearest it.
//
// A vertex on bone k blends two poses by its place t along the bone (0 at
// the root end, 1 at the tip): the bone's BASE pose B_k — its parent's
// blended at the point the bone leaves it, so a branch never comes away from
// its trunk — and its own M_k = B_k · (a rotation about its root). Each
// bone's bend is calibrated so its tip moves as far as the classic sway
// moved it (0.35·sway² of the plant's height in a full gust), whatever its
// parents already contribute; and a small, quick flutter rides on top.

import (
	"math"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// foliageMaxBones is the most bones one plant gets; any axis primitive past
// it rides the nearest bone instead.
const foliageMaxBones = 64

// What a recorded span of the merged list is.
const (
	asmSpanCard  = iota // a card: a part
	asmSpanAxis         // something that grows along an axis: a bone
	asmSpanOther        // anything else: core, riding the nearest bone
	// asmSpanRide grows along an axis but is no bone of its own (a moss
	// shell over a limb): it rides, whole, the bone nearest its pivot.
	asmSpanRide
)

// asmCard is a card as foliage_mesh.go's card() was asked for it.
type asmCard struct {
	base, right, up, n mgl32.Vec3
	tile, part         int
	sway0, sway1, tone float32
}

// asmSpan is one primitive's range of the merged list, in vertices.
type asmSpan struct {
	m          *fmesh
	first, end int
	kind       int
	pivot, tip mgl32.Vec3
	card       asmCard
}

// asmRecorder gathers the spans while a generator runs.
type asmRecorder struct{ spans []asmSpan }

// asmRecording is the recorder in use, nil outside recordFoliageAssembly.
// Assemblies are built once, at start-up, on the main thread.
var asmRecording *asmRecorder

// mark is where the next vertex will go.
func (m *fmesh) mark() int { return len(m.v) / foliageVertexFloats }

func (m *fmesh) recordCard(first int, c asmCard) {
	if r := asmRecording; r != nil {
		r.spans = append(r.spans, asmSpan{m: m, first: first, end: m.mark(), kind: asmSpanCard, card: c})
	}
}

func (m *fmesh) recordAxis(first int, pivot, tip mgl32.Vec3) {
	if r := asmRecording; r != nil {
		r.spans = append(r.spans, asmSpan{m: m, first: first, end: m.mark(), kind: asmSpanAxis, pivot: pivot, tip: tip})
	}
}

func (m *fmesh) recordRide(first int, pivot mgl32.Vec3) {
	if r := asmRecording; r != nil {
		r.spans = append(r.spans, asmSpan{m: m, first: first, end: m.mark(), kind: asmSpanRide, pivot: pivot})
	}
}

func (m *fmesh) recordOther(first int) {
	if r := asmRecording; r != nil {
		r.spans = append(r.spans, asmSpan{m: m, first: first, end: m.mark(), kind: asmSpanOther})
	}
}

// foliageBone is one bone of a plant, in its frame at unit scale.
type foliageBone struct {
	pivot, tip mgl32.Vec3
	parent     int32
	// at is where its pivot lies along its parent (0..1): its base pose is
	// the parent's blended there.
	at float32
	// flex is its bend in radians in a full gust of strength 1; flutter
	// the amplitude of its quick shiver, and phase where that starts.
	flex, flutter, phase float32
}

// t is where p lies along the bone, 0 at its pivot and 1 at its tip.
func (b *foliageBone) t(p mgl32.Vec3) float32 {
	ax := b.tip.Sub(b.pivot)
	l2 := ax.Dot(ax)
	if l2 < 1e-12 {
		return 0
	}
	return min(max(p.Sub(b.pivot).Dot(ax)/l2, 0), 1)
}

// dist is how far p is from the bone's segment.
func (b *foliageBone) dist(p mgl32.Vec3) float32 {
	q := b.pivot.Add(b.tip.Sub(b.pivot).Mul(b.t(p)))
	return p.Sub(q).Len()
}

// foliagePart is one card of an assembly.
type foliagePart struct {
	card asmCard
	bone int32
	// w0, w1 are its bottom and top edges' places along its bone.
	w0, w1 float32
}

// foliageAssembly is one plant variant: its core mesh, its card parts and
// its bones.
type foliageAssembly struct {
	// core is the merged list's triangles that are not cards
	// (foliageVertexFloats a vertex), and coreBind each vertex's bone and
	// place along it.
	core     []float32
	coreBind [][2]float32
	parts    []foliagePart
	bones    []foliageBone
	// merged is the classic path's list, for the flatten test.
	merged []float32
}

// recordFoliageAssembly runs shape's generator for variant at full detail
// and returns the plant as an assembly.
func recordFoliageAssembly(shape world.FoliageShape, variant int) foliageAssembly {
	rec := &asmRecorder{}
	asmRecording = rec
	merged := buildFoliageTemplate(shape, variant, 0)
	asmRecording = nil
	nv := len(merged) / foliageVertexFloats
	vert := func(i int) []float32 { return merged[i*foliageVertexFloats : (i+1)*foliageVertexFloats] }
	pos := func(i int) mgl32.Vec3 { v := vert(i); return mgl32.Vec3{v[0], v[1], v[2]} }

	// Spans must all be the merged list's, and must not overlap; a vertex no
	// span claims is core riding the nearest bone.
	owner := make([]int, nv)
	for i := range owner {
		owner[i] = -1
	}
	var spans []asmSpan
	var mesh *fmesh
	for _, s := range rec.spans {
		if mesh == nil {
			mesh = s.m
		}
		if s.m != mesh || s.end > nv {
			continue
		}
		si := len(spans)
		spans = append(spans, s)
		for v := s.first; v < s.end; v++ {
			if owner[v] < 0 {
				owner[v] = si
			}
		}
	}

	a := foliageAssembly{merged: merged}
	a.bones = []foliageBone{{tip: mgl32.Vec3{0, 1e-3, 0}, parent: -1}}
	plantH := float32(0)
	for i := 0; i < nv; i++ {
		plantH = max(plantH, pos(i).Y())
	}
	plantH = max(plantH, 0.1)
	// The bones, in the order their primitives were made — trunks before
	// branches before twigs, which is the order every generator builds in.
	boneOf := make([]int32, len(spans))
	for si := range spans {
		boneOf[si] = -1
		s := &spans[si]
		if s.kind != asmSpanAxis || len(a.bones) >= foliageMaxBones || s.tip.Sub(s.pivot).Len() < 1e-4 {
			continue
		}
		parent := int32(0)
		best := 0.12 * plantH
		for bi := 1; bi < len(a.bones); bi++ {
			b := &a.bones[bi]
			// Out of the parent's length, not its root: siblings from one
			// root are not each other's children.
			if b.t(s.pivot) < 0.08 {
				continue
			}
			if d := b.dist(s.pivot); d < best {
				parent, best = int32(bi), d
			}
		}
		nb := foliageBone{pivot: s.pivot, tip: s.tip, parent: parent}
		if parent > 0 {
			nb.at = a.bones[parent].t(s.pivot)
		}
		boneOf[si] = int32(len(a.bones))
		a.bones = append(a.bones, nb)
	}
	nearest := func(p mgl32.Vec3) int32 {
		best, bd := int32(0), float32(math.MaxFloat32)
		for bi := 1; bi < len(a.bones); bi++ {
			if d := a.bones[bi].dist(p); d < bd {
				best, bd = int32(bi), d
			}
		}
		// A plant whose only bone is far from p (a stone at a stem's foot)
		// leaves it on the root.
		if best > 0 && bd > 0.35*plantH {
			return 0
		}
		return best
	}

	// An axis past the bone budget, and a riding span, goes whole with the
	// bone nearest where it grows from — a twig with the limb it is on —
	// rather than each of its vertices with whichever bone is nearest it.
	for si := range spans {
		s := &spans[si]
		if boneOf[si] < 0 && (s.kind == asmSpanAxis || s.kind == asmSpanRide) && len(a.bones) > 1 {
			boneOf[si] = nearest(s.pivot)
		}
	}

	// Cards to parts; everything else to the core, bound vertex by vertex.
	for si := range spans {
		s := &spans[si]
		if s.kind != asmSpanCard || s.end-s.first != 6 {
			continue
		}
		c := s.card
		b := nearest(c.base)
		bn := &a.bones[b]
		a.parts = append(a.parts, foliagePart{card: c, bone: b, w0: bn.t(c.base), w1: bn.t(c.base.Add(c.up))})
	}
	for i := 0; i < nv; i++ {
		si := owner[i]
		if si >= 0 && spans[si].kind == asmSpanCard && spans[si].end-spans[si].first == 6 {
			continue
		}
		var b int32
		if si >= 0 && boneOf[si] >= 0 {
			b = boneOf[si]
		} else {
			b = nearest(pos(i))
		}
		a.core = append(a.core, vert(i)...)
		a.coreBind = append(a.coreBind, [2]float32{float32(b), a.bones[b].t(pos(i))})
	}
	a.calibrate()
	return a
}

// calibrate sets every bone's bend so its tip moves as far as the classic
// sway moved it, and its flutter likewise (see the file comment).
func (a *foliageAssembly) calibrate() {
	// The most sway anything on each bone has, near its tip.
	tipSway := make([]float32, len(a.bones))
	for i, bt := range a.coreBind {
		b := int(bt[0])
		if b > 0 && bt[1] > 0.6 {
			tipSway[b] = max(tipSway[b], a.core[i*foliageVertexFloats+8])
		}
	}
	for _, p := range a.parts {
		if p.bone > 0 {
			tipSway[p.bone] = max(tipSway[p.bone], p.card.sway1)
		}
	}
	for bi := 1; bi < len(a.bones); bi++ {
		b := &a.bones[bi]
		want := 0.35 * tipSway[bi] * tipSway[bi]
		// What the parents already move the tip by, in a full gust.
		poses := poseFoliageBones(a.bones[:bi+1], 1, 0, 0)
		base := poses[2*bi]
		have := mulAffine(base, b.tip).Sub(b.tip).Len()
		arm := b.tip.Sub(b.pivot)
		l := float32(math.Hypot(float64(arm.X()), float64(arm.Y())))
		if l < 1e-3 {
			continue
		}
		b.flex = min(max((want-have)/l, 0), 1)
		b.flutter = 0.03 * tipSway[bi] / max(arm.Len(), 0.05)
		b.phase = float32(math.Mod(float64(bi)*2.39996, 2*math.Pi))
	}
}

// poseFoliageBones poses bones for a wind of the given amount (strength ×
// gust) blowing along the plant's +X (dir 0) or +Z (dir 1), with flutter
// angle flutter (radians, times each bone's own): the base and own pose of
// each bone, interleaved (B0, M0, B1, M1, ...).
func poseFoliageBones(bones []foliageBone, amount float32, dir int, flutter float32) []mgl32.Mat4 {
	return poseFoliageBonesBy(bones, amount, dir, func(*foliageBone) float32 { return flutter })
}

// poseFoliageSlot is one of nfoliage_bones_comp.glsl's slots on the CPU —
// gust level g of nfGusts, flutter phase f of nfFlutters, at time — which
// that shader must match.
func poseFoliageSlot(bones []foliageBone, strength float32, g, f int, time float32, dir int) []mgl32.Mat4 {
	gust := float32(g) / float32(nfGusts-1)
	return poseFoliageBonesBy(bones, strength*(0.2+0.8*gust), dir, func(b *foliageBone) float32 {
		return strength * float32(math.Sin(float64(time*6.5+float32(f)*1.5707963+b.phase)))
	})
}

// poseFoliageBonesBy is the posing itself, each bone's flutter angle (to be
// times its own) from flutter.
func poseFoliageBonesBy(bones []foliageBone, amount float32, dir int, flutter func(*foliageBone) float32) []mgl32.Mat4 {
	out := make([]mgl32.Mat4, 2*len(bones))
	axis := mgl32.Vec3{0, 0, -1}
	if dir == 1 {
		axis = mgl32.Vec3{1, 0, 0}
	}
	for i := range bones {
		b := &bones[i]
		if b.parent < 0 {
			out[2*i], out[2*i+1] = mgl32.Ident4(), mgl32.Ident4()
			continue
		}
		pb, pm := out[2*b.parent], out[2*b.parent+1]
		base := lerpMat4(pb, pm, b.at)
		ang := b.flex*amount + b.flutter*flutter(b)
		r := mgl32.Translate3D(b.pivot[0], b.pivot[1], b.pivot[2]).
			Mul4(mgl32.HomogRotate3D(ang, axis)).
			Mul4(mgl32.Translate3D(-b.pivot[0], -b.pivot[1], -b.pivot[2]))
		out[2*i], out[2*i+1] = base, base.Mul4(r)
	}
	return out
}

func lerpMat4(a, b mgl32.Mat4, t float32) mgl32.Mat4 {
	var o mgl32.Mat4
	for i := range o {
		o[i] = a[i] + (b[i]-a[i])*t
	}
	return o
}

func mulAffine(m mgl32.Mat4, p mgl32.Vec3) mgl32.Vec3 {
	return m.Mul4x1(p.Vec4(1)).Vec3()
}

// cardCorners are a part's four corners in its card's own order (a, b, c,
// d of card()), and the unit template's.
var cardTemplateCorners = [4][2]float32{{-1, 0}, {1, 0}, {1, 1}, {-1, 1}}

// cardVertex is corner k of a card as card() wrote it.
func cardVertex(c asmCard, k int) []float32 {
	x, y := cardTemplateCorners[k][0], cardTemplateCorners[k][1]
	p := c.base.Add(c.right.Mul(x)).Add(c.up.Mul(y))
	uv := tileUV(c.tile, (x+1)/2, y)
	sw := c.sway0
	if y > 0 {
		sw = c.sway1
	}
	return []float32{p[0], p[1], p[2], c.n[0], c.n[1], c.n[2], uv[0], uv[1], sw, float32(c.part), c.tone}
}

// cardCornerOrder is the corners of a card's two triangles (quad(a,b,c,d)
// = tri(a,b,c), tri(a,c,d)).
var cardCornerOrder = [6]int{0, 1, 2, 0, 2, 3}

// flatten is the assembly back as one triangle list: the core, then every
// part's card.
func (a *foliageAssembly) flatten() []float32 {
	out := append([]float32(nil), a.core...)
	for _, p := range a.parts {
		for _, k := range cardCornerOrder {
			out = append(out, cardVertex(p.card, k)...)
		}
	}
	return out
}
