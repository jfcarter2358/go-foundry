package world

// decal_project.go — building a decal's mesh out of the level's own triangles.
//
// The whole of what a decal draws is made here: every drawn triangle in the level
// that falls inside the decal's box, clipped to that box, re-textured by
// projecting it onto the decal's plane, and handed back as geometry.
//
// Three choices are worth knowing about before reading the code.
//
// One: the mesh is grouped by the SPACE its vertices are in, not by the decal. A
// static brush's triangles are chunk-local, a dynamic brush's are relative to its
// own authored rest position, and a compiled surface's are world-space — three
// different matrices, so three different pieces, each drawn under the matrix its
// source is drawn under. That is the whole of the support for decals on lifts, on
// doors, on a dropship's hull and on chunks that stream in and out: there is no
// special case for any of them, because a piece of a decal IS brush geometry as
// far as every transform in the engine is concerned.
//
// Two: the clip is done in WORLD space and the result transformed back. The
// decal's box is defined in world space and the sources are in three different
// spaces, so clipping where the box lives means one implementation instead of one
// per space — at the cost of a matrix multiply per vertex each way, paid when a
// decal is built rather than per frame.
//
// Three: a surface leaning more than MaxAngle away from the decal's facing is
// dropped rather than painted. A projection onto a surface nearly parallel to it
// stretches the texture into streaks metres long, which reads as a rendering bug;
// a decal that stops at the corner reads as a decal.
//
// One limitation worth stating, because it is a consequence of projecting where
// the decal stands: a decal built while its carrier has MOVED is projected at the
// carrier's current position, not the one it was authored against. Everything
// that triggers a rebuild — an edit, a geometry change — happens in the editor,
// where frames and dynamic brushes are at rest, so in practice this is invisible.
// A script that both moves a lift and rebuilds a decal on it would see it.

import (
	"math"

	"foundry/internal/geom"
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// DecalPiece is one decal's mesh in one source's coordinate space.
//
// What identifies the source is its ID rather than its index, for the reason
// every other cross-reference in the level model is by ID: undo and a reload
// rebuild the level wholesale, and an index would then name a different brush.
// The hint fields are last frame's index, checked against the ID before being
// believed — see Level.DecalPieceMatrix.
type DecalPiece struct {
	// Chunk is the ID of the chunk this piece's source belongs to. Always set: a
	// compiled surface records the chunk it was compiled from, which is what lets
	// a streamed-out chunk take its decals with it.
	Chunk string
	// Brush is the ID of the dynamic brush this piece's vertices are relative to,
	// or "" when they are in plain chunk space.
	//
	// Only DYNAMIC brushes get a piece to themselves. Every static brush in a
	// chunk shares that chunk's matrix, so they share one piece, and a decal
	// across a wall made of six blocks is one draw rather than six.
	Brush string
	// Static marks a piece clipped out of a compiled level's static surfaces,
	// whose vertices are already in world space and need no matrix at all.
	Static bool
	// Geometry is the built mesh, with buffers of its own — it is drawn directly
	// (render/decal_render.go) rather than out of a batch, because a decal is
	// blended, ordered, and far too few in number for batching to pay.
	Geometry gfx.Geometry

	// chunkHint and brushHint are where the source was last found, so the common
	// case costs one string comparison instead of a scan.
	chunkHint, brushHint int
}

// BuildDecal builds d's mesh from lvl's geometry, replacing whatever it had.
//
// Safe to call with no GL context: Geometry.Load builds the CPU side
// unconditionally and uploads only when gfx.GeometryUploadsEnabled, which is what
// lets the headless tools and the tests project decals.
func BuildDecal(lvl *Level, d *Decal) {
	for i := range d.Pieces {
		d.Pieces[i].Geometry.Delete()
	}
	d.Pieces = nil
	// Marked built before the early returns below, not after the work: a decal
	// floating in mid-air finds no geometry at all, and without this it would be
	// rebuilt every frame for ever, since an empty Pieces is indistinguishable
	// from an unbuilt one.
	d.builtOK = true
	d.built = decalFingerprint(d)
	if lvl == nil || !d.HasSize() {
		return
	}
	lo, hi, ok := d.WorldAABB()
	if !ok {
		return
	}

	box := decalBox{
		centre: d.Position,
		minAng: float32(math.Cos(float64(mgl32.DegToRad(d.EffectiveMaxAngle())))),
		push:   d.EffectivePush(),
	}
	box.u, box.v, box.n = d.Basis()
	box.hu, box.hv, box.hn = d.Width*0.5, d.Height*0.5, d.Depth*0.5

	var b decalBuilders
	b.gatherBrushes(lvl, &box, lo, hi)
	b.gatherStatic(lvl, &box, lo, hi)
	d.Pieces = b.finish(d)
}

// ---------- the box ----------

// decalBox is the projection, pre-resolved: where it is, which way it points, how
// far it reaches, and how oblique a surface it will still paint.
type decalBox struct {
	centre     mgl32.Vec3
	u, v, n    mgl32.Vec3
	hu, hv, hn float32
	// minAng is cos(MaxAngle) — a surface is painted while its normal's dot with
	// n is at least this. Comparing cosines keeps an acos out of the inner loop,
	// which runs once per candidate triangle in the level.
	minAng float32
	push   float32
}

// uvAt projects a world point onto the decal's plane and returns its texture
// coordinates. V runs DOWN the texture — the decal's local +Y is the top of the
// image, at v = 0 — which is the convention the rest of the engine uses (see
// sprite_common.glsl, where the sprite quad flips V for the same reason).
func (bx *decalBox) uvAt(p mgl32.Vec3) (float32, float32) {
	rel := p.Sub(bx.centre)
	return 0.5 + rel.Dot(bx.u)/(bx.hu*2), 0.5 - rel.Dot(bx.v)/(bx.hv*2)
}

// ---------- gathering ----------

// decalVert is one corner of the polygon being clipped: a world position, the
// normal the source surface shades with there, and the source's baked light if it
// has any. They travel together because every one of them has to be interpolated
// at a clip intersection.
type decalVert struct {
	pos   mgl32.Vec3
	nrm   mgl32.Vec3
	light [gfx.LightStride]float32
	lit   bool
}

// decalBuilders accumulates the clipped triangles, one builder per source space.
type decalBuilders struct {
	keys []decalPieceKey
	bs   []*decalBuilder
}

// decalPieceKey is what makes two source surfaces share a piece: the same chunk,
// the same (dynamic) brush, and the same side of the compiled/authored divide.
type decalPieceKey struct {
	chunk  string
	brush  string
	static bool
}

// decalBuilder is one piece's mesh under construction.
//
// One builder per SOURCE SPACE, and that space's matrix is fixed for the life of
// the builder — which is what lets every static brush of a chunk share one: they
// are all drawn under the chunk's matrix, so their clipped triangles can all go
// into one mesh.
type decalBuilder struct {
	verts   [][]float32
	normals [][]float32
	light   [][]float32
	indices []int
	// lit records that at least one source triangle carried baked light. A piece
	// that mixes baked and unbaked sources cannot exist — they come from
	// different chunks, and a chunk is either compiled or it is not — so this is
	// a property of the whole piece.
	lit bool

	// source carries this piece's space into the world; identity says it IS the
	// world already, which is the common case and skips every multiply.
	source   mgl32.Mat4
	identity bool
	// toLocal is source inverted, for the return trip. toWorldN and toLocalN are
	// the same pair for NORMALS: a normal is carried out by the inverse transpose
	// of the matrix that carries a position, so it comes back by that matrix's
	// own transpose. Pre-multiplied rather than derived per vertex — a decal on a
	// terrain chunk clips thousands of triangles.
	toLocal, toWorldN, toLocalN mgl32.Mat4
}

// for_ returns the builder for one source space, making it on first sight.
func (bs *decalBuilders) for_(key decalPieceKey, source mgl32.Mat4) *decalBuilder {
	for i := range bs.keys {
		if bs.keys[i] == key {
			return bs.bs[i]
		}
	}
	b := &decalBuilder{source: source, identity: source == mgl32.Ident4()}
	if !b.identity {
		b.toLocal = source.Inv()
		b.toWorldN = b.toLocal.Transpose()
		b.toLocalN = source.Transpose()
	}
	bs.keys = append(bs.keys, key)
	bs.bs = append(bs.bs, b)
	return b
}

// gatherBrushes clips every drawn brush face in reach of the box.
//
// It walks lvl.Chunks itself rather than calling ChunksOverlapping, which drops
// exactly the two kinds of chunk a decal most wants: a frame-owned one (a
// dropship's hull, which is where a decal riding a vehicle has to come from) and
// a switched-off one. A disabled chunk is built into the decal here and skipped
// at DRAW time instead, so a script turning the chunk back on shows the decal on
// it without a rebuild.
func (bs *decalBuilders) gatherBrushes(lvl *Level, bx *decalBox, lo, hi mgl32.Vec3) {
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		cMin, cMax := ch.WorldBounds()
		if !boxesOverlap(cMin, cMax, lo, hi) {
			continue
		}
		chunkModel := ch.ModelMatrix()
		for bi := range ch.Brushes {
			br := &ch.Brushes[bi]
			bMin, bMax := br.WorldAABB(ch)
			if !boxesOverlap(bMin, bMax, lo, hi) {
				continue
			}
			// A dynamic brush's vertices are authored at its rest position and
			// carried by its own placement on top of the chunk's, so it gets a
			// piece — and a matrix — of its own. See brush_dynamic.go.
			source, key := chunkModel, decalPieceKey{chunk: ch.ID}
			if br.Dynamic {
				source = chunkModel.Mul4(br.Placement())
				key.brush = br.ID
			}
			b := bs.for_(key, source)
			mesh := br.DrawMesh()
			for fi := range mesh.Faces {
				f := &mesh.Faces[fi]
				if f.NoDraw {
					continue
				}
				b.clipGeometry(bx, &f.Geometry)
			}
		}
		if ch.Terrain != nil {
			bs.gatherTerrain(ch, chunkModel, bx, lo, hi)
		}
	}
}

// gatherTerrain clips the chunk's ground under the box. The terrain has no
// face meshes to hand over, so the faces under the box are written out as one
// in the layout clipGeometry reads — corners [X,Y,Z,U,V] and shading normals
// — and clipped in the chunk's space, on the chunk's own piece: a decal on the
// ground rides with its chunk, and streams out with it, like one on a wall.
func (bs *decalBuilders) gatherTerrain(ch *Chunk, model mgl32.Mat4, bx *decalBox, lo, hi mgl32.Vec3) {
	t := ch.Terrain
	// The box's corners carried into the chunk, for the faces to take.
	inv := model.Inv()
	llo := mgl32.Vec3{float32(math.Inf(1)), float32(math.Inf(1)), float32(math.Inf(1))}
	lhi := llo.Mul(-1)
	for i := 0; i < 8; i++ {
		c := lo
		if i&1 != 0 {
			c[0] = hi[0]
		}
		if i&2 != 0 {
			c[1] = hi[1]
		}
		if i&4 != 0 {
			c[2] = hi[2]
		}
		p := inv.Mul4x1(c.Vec4(1)).Vec3()
		for k := 0; k < 3; k++ {
			llo[k] = min(llo[k], p[k])
			lhi[k] = max(lhi[k], p[k])
		}
	}
	var g gfx.Geometry
	t.EachFaceIn(llo, lhi, func(f *TerrainFace) {
		for k := 0; k < 2; k++ {
			for _, c := range f.Tri(k) {
				p, n := f.Corners[c], f.Normals[c]
				g.Vertices = append(g.Vertices, p.X(), p.Y(), p.Z(), 0, 0)
				g.Normals = append(g.Normals, n.X(), n.Y(), n.Z())
			}
		}
	})
	if len(g.Vertices) == 0 {
		return
	}
	bs.for_(decalPieceKey{chunk: ch.ID}, model).clipGeometry(bx, &g)
}

// gatherStatic clips a compiled level's surfaces, whose vertices are already
// world-space.
//
// Grouped by the chunk each surface was compiled FROM rather than all together,
// so a streamed level's decals come and go with the chunk they lie on, exactly as
// the surfaces under them do.
func (bs *decalBuilders) gatherStatic(lvl *Level, bx *decalBox, lo, hi mgl32.Vec3) {
	if lvl.Static == nil {
		return
	}
	for si := range lvl.Static.Surfaces {
		s := &lvl.Static.Surfaces[si]
		if !boxesOverlap(s.Lo, s.Hi, lo, hi) {
			continue
		}
		b := bs.for_(decalPieceKey{chunk: s.Chunk, static: true}, mgl32.Ident4())
		b.clipGeometry(bx, &s.Geometry)
	}
}

// clipGeometry walks one source mesh's triangles and adds whatever survives the
// box to the builder.
func (b *decalBuilder) clipGeometry(bx *decalBox, g *gfx.Geometry) {
	verts := g.Vertices
	if len(verts) < 15 {
		return
	}
	count := len(verts) / 5
	normals := g.Normals
	hasNormals := len(normals) == count*3
	light := g.BakedLightData()
	hasLight := len(light) == count*gfx.LightStride

	// A triangle clipped by six planes cannot exceed nine corners, so neither
	// buffer here ever has to grow.
	var poly, clipped [16]decalVert
	for i := 0; i+14 < len(verts); i += 15 {
		for c := range 3 {
			vi := i + c*5
			p := mgl32.Vec3{verts[vi], verts[vi+1], verts[vi+2]}
			if !b.identity {
				p = mulPoint(b.source, p)
			}
			poly[c] = decalVert{pos: p}
			if hasNormals {
				ni := (i/5 + c) * 3
				n := mgl32.Vec3{normals[ni], normals[ni+1], normals[ni+2]}
				if !b.identity {
					n = mulDir(b.toWorldN, n)
				}
				poly[c].nrm = n
			}
			if hasLight {
				li := (i/5 + c) * gfx.LightStride
				copy(poly[c].light[:], light[li:li+gfx.LightStride])
				poly[c].lit = true
			}
		}

		// The triangle's own plane, in world space, which is what the facing test
		// and the push both read. Taken from the world-space corners rather than
		// from the mesh's normals so that a mirrored or scaled chunk matrix
		// cannot leave it pointing inward.
		face := geom.TriangleNormal(poly[0].pos, poly[1].pos, poly[2].pos)
		if face.LenSqr() < 1e-12 {
			continue
		}
		face = face.Normalize()
		if face.Dot(bx.n) < bx.minAng {
			continue
		}
		if !hasNormals {
			for c := range 3 {
				poly[c].nrm = face
			}
		}

		n := clipToBox(poly[:], clipped[:], bx, 3)
		if n < 3 {
			continue
		}
		b.emit(bx, clipped[:n], face)
	}
}

// emit fans the clipped polygon into triangles and appends them, taking every
// vertex back into the piece's own space on the way.
func (b *decalBuilder) emit(bx *decalBox, poly []decalVert, face mgl32.Vec3) {
	base := len(b.verts)
	for i := range poly {
		// Out along the surface's own normal, in WORLD space, before the return
		// trip: a push applied in local space would be the wrong length under a
		// scaled chunk.
		p := poly[i].pos.Add(face.Mul(bx.push))
		// The UVs come off the UNPUSHED point. The push is a rendering nudge of a
		// couple of millimetres, and letting it slide the projection would make a
		// decal's texture depend on it.
		u, v := bx.uvAt(poly[i].pos)
		nrm := poly[i].nrm
		if !b.identity {
			p = mulPoint(b.toLocal, p)
			nrm = mulDir(b.toLocalN, nrm)
		}
		if nrm.LenSqr() > 1e-12 {
			nrm = nrm.Normalize()
		}
		b.verts = append(b.verts, []float32{p.X(), p.Y(), p.Z(), u, v})
		b.normals = append(b.normals, []float32{nrm.X(), nrm.Y(), nrm.Z()})
		if poly[i].lit {
			b.lit = true
			b.light = append(b.light, append([]float32(nil), poly[i].light[:]...))
		} else {
			b.light = append(b.light, make([]float32, gfx.LightStride))
		}
	}
	for i := 1; i+1 < len(poly); i++ {
		b.indices = append(b.indices, base, base+i, base+i+1)
	}
}

// finish loads every accumulated piece and returns the ones that hold anything.
func (bs *decalBuilders) finish(d *Decal) []DecalPiece {
	var out []DecalPiece
	for i, b := range bs.bs {
		if len(b.indices) == 0 {
			continue
		}
		p := DecalPiece{Chunk: bs.keys[i].chunk, Brush: bs.keys[i].brush, Static: bs.keys[i].static}
		g := &p.Geometry
		// Smooth on outright, not through AutoSmooth: the normals here are the
		// SOURCE surface's, so a decal on a smoothed cylinder has to shade off
		// them however the player's Smooth Lighting setting is set. The wall under
		// it always does — its own normals were welded the same way — and a decal
		// shading flat on a wall shading round is a visible patch of different
		// lighting.
		g.Smooth = true
		g.VertexNormals = b.normals
		if b.lit {
			// The bake the source surface carries, interpolated. Without it a
			// decal in a compiled level is lit live while the wall under it is lit
			// from its bake, and the mark reads as a bright sticker in every
			// shadowed room.
			g.VertexLight = b.light
		}
		if err := g.Load(b.verts, b.indices, d.Texture, mgl32.Vec3{}); err != nil {
			continue
		}
		out = append(out, p)
	}
	// Texture, material, tint, alpha and unlit are deliberately NOT set here. None
	// of them changes a vertex, so they are pushed onto the pieces every frame
	// instead (RefreshDecalDrawState) — which is what lets a script fade a decal
	// out or swap its texture without the mesh being clipped out of the world
	// again.
	return out
}

// ---------- clipping ----------

// clipToBox clips poly against the box's six planes, writing the result into out
// and returning its length. Both buffers must hold at least nine vertices, which
// is the most a triangle against six planes can produce.
//
// Sutherland-Hodgman, one plane at a time, with every attribute interpolated at
// the crossing. Convex clip volume and convex input, so the output stays a single
// convex loop and can be fanned.
func clipToBox(poly, out []decalVert, bx *decalBox, n int) int {
	planes := [6]struct {
		axis mgl32.Vec3
		half float32
	}{
		{bx.u, bx.hu}, {bx.u.Mul(-1), bx.hu},
		{bx.v, bx.hv}, {bx.v.Mul(-1), bx.hv},
		{bx.n, bx.hn}, {bx.n.Mul(-1), bx.hn},
	}
	src, dst := poly, out
	for _, pl := range planes {
		n = clipToPlane(src, dst, bx.centre, pl.axis, pl.half, n)
		if n < 3 {
			return 0
		}
		src, dst = dst, src
	}
	// Six planes is an even number of swaps, so the answer is back in poly. out
	// is what the caller reads, so copy it across.
	if &src[0] != &out[0] {
		copy(out[:n], src[:n])
	}
	return n
}

// clipToPlane keeps the part of src on the inside of one plane, where inside is
// `dot(p - centre, axis) <= half` — so the two planes of a box axis are that axis
// and its negation with the same half-extent.
func clipToPlane(src, dst []decalVert, centre, axis mgl32.Vec3, half float32, n int) int {
	m := 0
	put := func(v decalVert) {
		if m < len(dst) {
			dst[m] = v
			m++
		}
	}
	for i := range n {
		a, b := src[i], src[(i+1)%n]
		// Signed distance to the plane, positive inside.
		da := half - a.pos.Sub(centre).Dot(axis)
		db := half - b.pos.Sub(centre).Dot(axis)
		if da >= 0 {
			put(a)
		}
		if (da >= 0) != (db >= 0) {
			put(lerpDecalVert(a, b, da/(da-db)))
		}
	}
	return m
}

// lerpDecalVert interpolates every attribute a clipped corner carries.
func lerpDecalVert(a, b decalVert, t float32) decalVert {
	out := decalVert{
		pos: a.pos.Add(b.pos.Sub(a.pos).Mul(t)),
		nrm: a.nrm.Add(b.nrm.Sub(a.nrm).Mul(t)),
		lit: a.lit || b.lit,
	}
	for i := range out.light {
		out.light[i] = a.light[i] + (b.light[i]-a.light[i])*t
	}
	return out
}

// ---------- small helpers ----------

// boxesOverlap is the broad phase: two AABBs sharing any volume at all.
func boxesOverlap(aLo, aHi, bLo, bHi mgl32.Vec3) bool {
	return aLo.X() <= bHi.X() && aHi.X() >= bLo.X() &&
		aLo.Y() <= bHi.Y() && aHi.Y() >= bLo.Y() &&
		aLo.Z() <= bHi.Z() && aHi.Z() >= bLo.Z()
}

// mulDir carries a direction through a matrix, ignoring its translation.
func mulDir(m mgl32.Mat4, d mgl32.Vec3) mgl32.Vec3 {
	v := m.Mul4x1(mgl32.Vec4{d.X(), d.Y(), d.Z(), 0})
	return mgl32.Vec3{v.X(), v.Y(), v.Z()}
}
