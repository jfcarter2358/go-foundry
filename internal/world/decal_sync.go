package world

// decal_sync.go — keeping a level's decals built, drawable and finite.
//
// A decal's mesh is clipped out of the world, so it goes stale for two reasons: a
// field of the decal itself moved the box, or the world under it changed. Both
// are caught by comparing a fingerprint — the decal's geometry-bearing fields
// mixed with BrushMeshGeneration, the counter every mesh edit, load and unload
// already bumps (brush_generation.go). Nothing has to be told to invalidate
// anything, which is the same trade the chunk batches and the raycast BVHs make.
//
// The fields that do NOT change a vertex — texture, material, tint, alpha, unlit
// — are pushed onto the built pieces instead of rebuilding them. That is what
// lets a script fade a decal out, or swap its texture for a burnt version, at no
// cost at all.
//
// The cap is the other half of this file. Decals are the one level object a
// script creates without meaning to keep: every rifle round leaves one. So the
// script-spawned ones (Decal.Transient) are a ring — past MaxTransientDecals the
// oldest is dropped — and the ones given a Lifetime are reaped when it runs out.
// An AUTHORED decal is never touched by either: an author placing a sign expects
// it to stay.

import (
	"math"

	"foundry/internal/geom"
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// SyncDecals brings every decal in lvl up to date: rebuilds the stale ones and
// refreshes the draw state of the rest.
//
// Called once a frame from the renderer, like SyncChunkBatches, because that is
// where the answer is needed and there is no invalidation to arrange. The common
// frame does one fingerprint per decal and no work at all.
//
// budget caps how many decals may be REBUILT in one call, so that a level opening
// with two hundred authored decals spreads the clipping over a few frames instead
// of stalling on the first one. Zero or less means no cap.
func SyncDecals(lvl *Level, budget int) {
	if lvl == nil {
		return
	}
	gen := BrushMeshGeneration()
	rebuilt := 0
	for i := range lvl.Decals {
		d := &lvl.Decals[i]
		if !d.builtOK || d.built != decalFingerprintAt(d, gen) {
			if budget > 0 && rebuilt >= budget {
				// Left stale on purpose: the fingerprint has not been written, so
				// the next call picks it up. A decal not yet built draws nothing,
				// which for one frame is invisible and for a stall is not.
				continue
			}
			BuildDecal(lvl, d)
			rebuilt++
		}
		RefreshDecalDrawState(d)
	}
}

// RefreshDecalDrawState pushes the decal's non-geometric fields onto its built
// pieces. Cheap enough to do every frame for every decal, which is what keeps a
// script's writes to alpha, tint and texture free of a rebuild.
func RefreshDecalDrawState(d *Decal) {
	alpha := d.DrawAlpha()
	blend := gfx.SurfaceBlendFor(d.Blend)
	for i := range d.Pieces {
		g := &d.Pieces[i].Geometry
		g.Texture = d.Texture
		g.MaterialName = d.Material
		g.Tint = d.Tint
		g.Alpha = alpha
		g.Unlit = d.Unlit
		// The shading stage has to know how the framebuffer blend will combine
		// what it produces: a multiply decal is a MASK rather than a surface, and
		// an additive one only ever adds light, so neither is shaded or fogged
		// the way an ordinary surface is. See fragment.glsl's surfaceBlend.
		g.Blend = blend
	}
}

// MarkDecalStale forces d to be rebuilt on the next SyncDecals.
//
// For the callers that change the geometry a decal is projected onto without
// going through a mesh edit — the editor's undo, which swaps the whole level —
// and for a test that wants a build it can be sure happened.
func MarkDecalStale(d *Decal) { d.builtOK = false }

// MarkDecalsStale is MarkDecalStale for a whole level.
func MarkDecalsStale(lvl *Level) {
	if lvl == nil {
		return
	}
	for i := range lvl.Decals {
		lvl.Decals[i].builtOK = false
	}
}

// ReleaseDecal frees one decal's GPU buffers and forgets its mesh. For the editor
// deleting one: a decal owns real buffers, unlike a sprite, so dropping the slice
// element would leak them.
func ReleaseDecal(d *Decal) {
	for i := range d.Pieces {
		d.Pieces[i].Geometry.Delete()
	}
	d.Pieces, d.builtOK = nil, false
}

// ReleaseDecals frees every decal's GPU buffers. For a level being torn down.
func ReleaseDecals(lvl *Level) {
	if lvl == nil {
		return
	}
	for i := range lvl.Decals {
		ReleaseDecal(&lvl.Decals[i])
	}
}

// ---------- the fingerprint ----------

// decalFingerprint is the current fingerprint of d and the world.
func decalFingerprint(d *Decal) uint64 {
	return decalFingerprintAt(d, BrushMeshGeneration())
}

// decalFingerprintAt is decalFingerprint with the generation passed in, so a
// whole level's decals are compared against one read of it.
//
// Only the fields that move a VERTEX are in here. Adding one that does not — a
// tint, an alpha — would rebuild the mesh every time a script faded a decal,
// which is the cost this whole file exists to avoid; adding a field that does and
// forgetting it here leaves the decal drawn in its old shape, which is worse. The
// rule for a new field is simply: does BuildDecal read it?
func decalFingerprintAt(d *Decal, gen uint64) uint64 {
	h := uint64(1469598103934665603)
	h = mixDecal(h, gen)
	h = mixDecalF(h, d.Position.X(), d.Position.Y(), d.Position.Z())
	h = mixDecalF(h, d.Rotation.X(), d.Rotation.Y(), d.Rotation.Z())
	h = mixDecalF(h, d.Width, d.Height, d.Depth)
	h = mixDecalF(h, d.EffectiveMaxAngle(), d.EffectivePush(), 0)
	return h
}

func mixDecal(h, v uint64) uint64 { return (h ^ v) * 1099511628211 }

func mixDecalF(h uint64, a, b, c float32) uint64 {
	h = mixDecal(h, uint64(math.Float32bits(a)))
	h = mixDecal(h, uint64(math.Float32bits(b)))
	return mixDecal(h, uint64(math.Float32bits(c)))
}

// ---------- drawing a piece ----------

// DecalPieceMatrix resolves the matrix a piece is drawn under, and reports
// whether it should be drawn at all.
//
// false covers three cases, all of them ordinary rather than exceptional: the
// source chunk has been switched off (a streamed-out district takes its decals
// with it), the source chunk or dynamic brush is gone entirely (a level edited
// since the decal was built — the decal is stale and about to be rebuilt), or a
// compiled chunk is hidden.
//
// The hint indices are what keep this cheap: a piece remembers where its source
// was last frame and only scans when the ID there no longer matches.
func (lvl *Level) DecalPieceMatrix(p *DecalPiece) (mgl32.Mat4, bool) {
	if lvl == nil {
		return mgl32.Ident4(), false
	}
	ch := lvl.decalChunk(p)
	if ch == nil || !ch.IsEnabled() {
		return mgl32.Ident4(), false
	}
	if p.Static {
		// A compiled surface's vertices are world-space, so there is no matrix —
		// only the question of whether the chunk it came from is drawn, which the
		// enable check above and the compiled hidden set below both answer.
		if lvl.Static != nil && lvl.Static.HiddenChunks[p.Chunk] {
			return mgl32.Ident4(), false
		}
		return mgl32.Ident4(), true
	}
	m := ch.ModelMatrix()
	if p.Brush == "" {
		return m, true
	}
	br := decalBrush(ch, p)
	if br == nil {
		return mgl32.Ident4(), false
	}
	return m.Mul4(br.Placement()), true
}

// decalChunk resolves a piece's chunk through its hint.
func (lvl *Level) decalChunk(p *DecalPiece) *Chunk {
	if p.chunkHint >= 0 && p.chunkHint < len(lvl.Chunks) &&
		lvl.Chunks[p.chunkHint].ID == p.Chunk {
		return &lvl.Chunks[p.chunkHint]
	}
	for i := range lvl.Chunks {
		if lvl.Chunks[i].ID == p.Chunk {
			p.chunkHint = i
			return &lvl.Chunks[i]
		}
	}
	return nil
}

// decalBrush resolves a piece's dynamic brush through its hint.
func decalBrush(ch *Chunk, p *DecalPiece) *Brush {
	if p.brushHint >= 0 && p.brushHint < len(ch.Brushes) &&
		ch.Brushes[p.brushHint].ID == p.Brush {
		return &ch.Brushes[p.brushHint]
	}
	for i := range ch.Brushes {
		if ch.Brushes[i].ID == p.Brush {
			p.brushHint = i
			return &ch.Brushes[i]
		}
	}
	return nil
}

// ---------- lifetimes and the cap ----------

// StepDecals ages every decal with a Lifetime and removes the expired ones.
// Called from the gameplay tick, with dt in seconds.
//
// Reports whether the list changed, which is what tells a caller holding an index
// into lvl.Decals — the script runtime's ref cache — to look again.
func StepDecals(lvl *Level, dt float32) bool {
	if lvl == nil || dt <= 0 {
		return false
	}
	changed := false
	kept := lvl.Decals[:0]
	for i := range lvl.Decals {
		d := &lvl.Decals[i]
		if d.Lifetime > 0 {
			d.Lifetime -= dt
			if d.Lifetime <= 0 {
				for j := range d.Pieces {
					d.Pieces[j].Geometry.Delete()
				}
				changed = true
				continue
			}
		}
		kept = append(kept, *d)
	}
	lvl.Decals = kept
	return changed
}

// CapTransientDecals drops the oldest script-spawned decals until no more than
// MaxTransientDecals remain, and reports whether it dropped any.
//
// Oldest by POSITION IN THE LIST rather than by a timestamp, because appending is
// the only way a decal joins it: the front of the transient run is the oldest one
// there is. Authored decals are never candidates however many there are — an
// author who placed three hundred signs meant to.
func CapTransientDecals(lvl *Level) bool {
	if lvl == nil {
		return false
	}
	n := 0
	for i := range lvl.Decals {
		if lvl.Decals[i].Transient {
			n++
		}
	}
	drop := n - MaxTransientDecals
	if drop <= 0 {
		return false
	}
	kept := lvl.Decals[:0]
	for i := range lvl.Decals {
		d := &lvl.Decals[i]
		if d.Transient && drop > 0 {
			for j := range d.Pieces {
				d.Pieces[j].Geometry.Delete()
			}
			drop--
			continue
		}
		kept = append(kept, *d)
	}
	lvl.Decals = kept
	return true
}

// ---------- finding a surface to lie on ----------

// SurfaceNormalNear returns the normal of the drawn surface nearest to p within
// radius, which is what turns an impact point into a decal's orientation.
//
// It exists because the thing a script has when it wants a decal is a POINT — the
// place a shot landed, reported by `hitscan` — and a decal needs a plane. Asking
// the geometry rather than making the script carry a normal around is what lets
// `decal "hole" point` be the whole of the call.
//
// The search is the decal clipper's broad phase run small: the brushes whose boxes
// reach p, and the triangle of theirs that p is closest to. ok is false when
// nothing is in reach, which a caller should read as "there is nothing here to put
// a decal on" rather than as an error.
func SurfaceNormalNear(lvl *Level, p mgl32.Vec3, radius float32) (mgl32.Vec3, bool) {
	if lvl == nil || radius <= 0 {
		return mgl32.Vec3{}, false
	}
	h := mgl32.Vec3{radius, radius, radius}
	lo, hi := p.Sub(h), p.Add(h)
	best, bestD := mgl32.Vec3{}, radius*radius
	found := false

	consider := func(g *gfx.Geometry, m mgl32.Mat4, identity bool) {
		verts := g.Vertices
		for i := 0; i+14 < len(verts); i += 15 {
			var tri [3]mgl32.Vec3
			for c := range 3 {
				vi := i + c*5
				q := mgl32.Vec3{verts[vi], verts[vi+1], verts[vi+2]}
				if !identity {
					q = mulPoint(m, q)
				}
				tri[c] = q
			}
			n := geom.TriangleNormal(tri[0], tri[1], tri[2])
			if n.LenSqr() < 1e-12 {
				continue
			}
			d := pointTriangleDistSqr(p, tri[0], tri[1], tri[2])
			if d < bestD {
				bestD, best, found = d, n.Normalize(), true
			}
		}
	}

	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		cMin, cMax := ch.WorldBounds()
		if !boxesOverlap(cMin, cMax, lo, hi) {
			continue
		}
		model := ch.ModelMatrix()
		for bi := range ch.Brushes {
			br := &ch.Brushes[bi]
			bMin, bMax := br.WorldAABB(ch)
			if !boxesOverlap(bMin, bMax, lo, hi) {
				continue
			}
			m := model
			if br.Dynamic {
				m = model.Mul4(br.Placement())
			}
			identity := m == mgl32.Ident4()
			mesh := br.DrawMesh()
			for fi := range mesh.Faces {
				if mesh.Faces[fi].NoDraw {
					continue
				}
				consider(&mesh.Faces[fi].Geometry, m, identity)
			}
		}
	}
	if lvl.Static != nil {
		id := mgl32.Ident4()
		for si := range lvl.Static.Surfaces {
			s := &lvl.Static.Surfaces[si]
			if !boxesOverlap(s.Lo, s.Hi, lo, hi) {
				continue
			}
			consider(&s.Geometry, id, true)
		}
	}
	return best, found
}

// pointTriangleDistSqr is the squared distance from p to the closest point of
// triangle abc — the standard region-by-region solve, written out rather than
// pulled from geom because nothing else in the engine has wanted it.
func pointTriangleDistSqr(p, a, b, c mgl32.Vec3) float32 {
	ab, ac, ap := b.Sub(a), c.Sub(a), p.Sub(a)
	d1, d2 := ab.Dot(ap), ac.Dot(ap)
	if d1 <= 0 && d2 <= 0 {
		return ap.LenSqr()
	}
	bp := p.Sub(b)
	d3, d4 := ab.Dot(bp), ac.Dot(bp)
	if d3 >= 0 && d4 <= d3 {
		return bp.LenSqr()
	}
	if vc := d1*d4 - d3*d2; vc <= 0 && d1 >= 0 && d3 <= 0 {
		return p.Sub(a.Add(ab.Mul(d1 / (d1 - d3)))).LenSqr()
	}
	cp := p.Sub(c)
	d5, d6 := ab.Dot(cp), ac.Dot(cp)
	if d6 >= 0 && d5 <= d6 {
		return cp.LenSqr()
	}
	if vb := d5*d2 - d1*d6; vb <= 0 && d2 >= 0 && d6 <= 0 {
		return p.Sub(a.Add(ac.Mul(d2 / (d2 - d6)))).LenSqr()
	}
	if va := d3*d6 - d5*d4; va <= 0 && (d4-d3) >= 0 && (d5-d6) >= 0 {
		w := (d4 - d3) / ((d4 - d3) + (d5 - d6))
		return p.Sub(b.Add(c.Sub(b).Mul(w))).LenSqr()
	}
	// Inside the triangle's own region: the distance to its plane.
	n := ab.Cross(ac)
	if n.LenSqr() < 1e-12 {
		return ap.LenSqr()
	}
	d := ap.Dot(n.Normalize())
	return d * d
}
