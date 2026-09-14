package collision

// raycast_oracle_test.go — the brute-force raycast, kept verbatim from before
// spatial culling, as the oracle the accelerated one is checked against. It
// tests every triangle of every chunk and every body, so whatever it answers is
// what a ray through the level meets by definition.

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/world"
)

var _ = math.MaxFloat32

// Raycast casts one ray through the level and returns what it met.
//
// dir need not be normalised; it is, so that T is a distance in world units and
// comparable against Range whatever the caller handed in.
func bfRaycast(lvl *world.Level, origin, dir mgl32.Vec3, f RayFilter) RayResult {
	res := RayResult{Stop: origin}
	if lvl == nil || f.Range <= 0 || dir.Len() < 1e-6 {
		return res
	}
	dir = dir.Normalize()
	res.Stop = origin.Add(dir.Mul(f.Range))

	var hits []RayHit
	if f.Entities {
		hits = bfAppendEntityHits(hits, lvl, origin, dir, f)
	}
	if f.Volumes {
		hits = bfAppendVolumeHits(hits, lvl, origin, dir, f)
	}
	if f.Geometry {
		hits = bfAppendGeometryHits(hits, lvl, origin, dir, f)
		hits = bfAppendSceneryHits(hits, lvl, origin, dir, f)
	}

	// Nearest first, with a deterministic tiebreak. Two coincident surfaces at
	// a chunk boundary are the ordinary case, and a cast whose answer depended
	// on map iteration order would not be testable.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].T != hits[j].T {
			return hits[i].T < hits[j].T
		}
		if hits[i].Kind != hits[j].Kind {
			return hits[i].Kind < hits[j].Kind
		}
		return hits[i].ID < hits[j].ID
	})

	// The ray physically ends at the first piece of the level's shape. Truncate
	// before the reporting filters run, so that what a cast can see never
	// changes what stopped it.
	for i, h := range hits {
		if h.Kind.Blocks() {
			hits = hits[:i+1]
			res.Stop = h.Point
			res.Blocked = true
			break
		}
	}

	for _, h := range hits {
		if bfReportable(lvl, h, f) {
			res.Hits = append(res.Hits, h)
		}
	}
	return res
}

// reportable applies the filters that narrow what a cast *says* it saw, as
// opposed to the ones that change where the ray goes.
func bfReportable(lvl *world.Level, h RayHit, f RayFilter) bool {
	if h.Kind == RayHitScenery {
		// Solid, and so a blocker, but not addressable: nothing in the
		// scripting language names a scenery placement.
		return false
	}
	if f.Tag == "" && !f.VisibleOnly {
		return true
	}
	switch h.Kind {
	case RayHitEntity:
		e := entityByID(lvl, h.ID)
		if e == nil {
			return false
		}
		if f.VisibleOnly && !e.Visible {
			return false
		}
		return f.Tag == "" || e.HasTag(f.Tag)
	case RayHitVolume:
		v := volumeByID(lvl, h.ID)
		if v == nil {
			return false
		}
		return f.Tag == "" || v.HasTag(f.Tag)
	}

	if h.Kind == RayHitBrush {
		b := brushByID(lvl, h.ID)
		if b == nil {
			return false
		}
		// VisibleOnly has no extra meaning here: a hit only exists because a
		// drawn (non-NoDraw) face was actually met, so every brush hit already
		// qualifies.
		return f.Tag == "" || b.HasTag(f.Tag)
	}
	return false
}

// ---------- bodies ----------

func bfAppendEntityHits(out []RayHit, lvl *world.Level, origin, dir mgl32.Vec3, f RayFilter) []RayHit {
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if f.Ignore[e.ID] || !e.HasBounds() {
			continue
		}
		// An entity riding in a frame describes itself in that frame's
		// coordinates, so the frame goes on the outside — the same composition
		// RenderEntities uses to draw it.
		m := lvl.Frames.FrameFor(e).WorldMatrix().
			Mul4(mgl32.Translate3D(e.BoundCenter().X(), e.BoundCenter().Y(), e.BoundCenter().Z())).
			Mul4(e.RotationMatrix())
		t, ok := rayBounds(origin, dir, m, e.BoundHalfExtents(), e.BoundingKind)
		if !ok || t > f.Range {
			continue
		}
		out = append(out, RayHit{
			Kind: RayHitEntity, ID: e.ID, T: t, Point: origin.Add(dir.Mul(t)),
		})
	}
	return out
}

func bfAppendVolumeHits(out []RayHit, lvl *world.Level, origin, dir mgl32.Vec3, f RayFilter) []RayHit {
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		// A disabled volume contains nothing, which is the whole of what
		// disabling one means — so it is not there to be hit either.
		if f.Ignore[v.ID] || !v.Enabled {
			continue
		}
		half := mgl32.Vec3{v.HalfX, v.HalfY, v.HalfZ}
		if half.X() <= 0 || half.Y() <= 0 || half.Z() <= 0 {
			continue
		}
		// Through what it rides. rayBounds takes a general inverse, so a
		// carrier's scale is honoured along with its placement.
		m := v.WorldMatrix(lvl)
		t, ok := rayBounds(origin, dir, m, half, world.BoundingType(v.Shape))
		if !ok || t > f.Range {
			continue
		}
		out = append(out, RayHit{
			Kind: RayHitVolume, ID: v.ID, T: t, Point: origin.Add(dir.Mul(t)),
		})
	}
	return out
}

// appendSceneryHits tests the solid props. It goes through sceneryProxy so that
// a prop stops a shot in exactly the box it stops a body in — the alternative
// is a second reading of "solid" that drifts from the resolver's.
func bfAppendSceneryHits(out []RayHit, lvl *world.Level, origin, dir mgl32.Vec3, f RayFilter) []RayHit {
	for i := range lvl.Scenery {
		s := &lvl.Scenery[i]
		if f.Ignore[s.ID] {
			continue
		}
		proxy, ok := sceneryProxy(s)
		if !ok {
			continue
		}
		m := mgl32.Translate3D(proxy.Position.X(), proxy.Position.Y(), proxy.Position.Z()).
			Mul4(world.EulerMatrixYXZ(proxy.Rotation))
		t, hit := rayBounds(origin, dir, m, proxy.BoundHalfExtents(), proxy.BoundingKind)
		if !hit || t > f.Range {
			continue
		}
		out = append(out, RayHit{
			Kind: RayHitScenery, ID: s.ID, T: t, Point: origin.Add(dir.Mul(t)),
		})
	}
	return out
}

// ---------- geometry ----------

func bfAppendGeometryHits(out []RayHit, lvl *world.Level, origin, dir mgl32.Vec3, f RayFilter) []RayHit {
	// A compiled level's static surfaces. They are what the eye sees, so they
	// are what a ray should hit — and the chunks they came from carry no meshes
	// to test, which is deliberate: keeping a second copy of every mesh purely
	// so rays could hit it would also have rays hitting surfaces CSG had
	// already established were invisible.
	//
	// Already in world space, so no transform, and the hit reports the chunk
	// and brush the surface was authored in — the same answer a script would
	// have got from the level before it was compiled.
	if lvl.Static != nil {
		for si := range lvl.Static.Surfaces {
			surf := &lvl.Static.Surfaces[si]
			if f.Ignore[surf.Brush] || lvl.Static.Hidden(si) {
				continue
			}
			t, ok := rayGeometry(origin, dir, &surf.Geometry)
			if !ok || t > f.Range {
				continue
			}
			out = append(out, RayHit{
				Kind: RayHitBrush, Chunk: surf.Chunk, ID: surf.Brush,
				T: t, Point: origin.Add(dir.Mul(t)),
			})
		}
	}

	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		// A compiled chunk's static brushes were tested above, out of the
		// compiled list — but its dynamic ones were not, because the compiler
		// left them out of that list on purpose. They keep their own meshes
		// (Chunk.DrawsBrushLive) and are tested here.
		if ch.Compiled && !ch.HasDynamicBrushes() {
			continue
		}
		if !ch.IsEnabled() {
			// Released for streaming: not drawn, not collided against, and its
			// vertex buffers are gone. See world/chunk_enable.go.
			continue
		}
		// Brush verts are chunk-local. Moving the ray into that space rather
		// than the geometry into the world leaves t alone — origin and
		// direction are transformed together — so distances stay comparable
		// with the world-space hits above.
		inv := ch.ModelMatrix().Inv()
		if inv == (mgl32.Mat4{}) {
			continue
		}
		lo := inv.Mul4x1(origin.Vec4(1))
		ld := inv.Mul4x1(dir.Vec4(0))
		lOrigin := mgl32.Vec3{lo.X(), lo.Y(), lo.Z()}
		lDir := mgl32.Vec3{ld.X(), ld.Y(), ld.Z()}

		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			if f.Ignore[b.ID] || !ch.DrawsBrushLive(b) {
				continue
			}
			// The uploaded meshes are in the brush's AUTHORED coordinates, so
			// a brush a script has moved needs the ray taken one space further
			// in — its own placement — exactly as the chunk transform was
			// divided out above, and for the same reason: t survives it.
			bOrigin, bDir := lOrigin, lDir
			if !b.AtRest() {
				pinv := b.Placement().Inv()
				if pinv == (mgl32.Mat4{}) {
					continue
				}
				bo := pinv.Mul4x1(lOrigin.Vec4(1))
				bd := pinv.Mul4x1(lDir.Vec4(0))
				bOrigin = mgl32.Vec3{bo.X(), bo.Y(), bo.Z()}
				bDir = mgl32.Vec3{bd.X(), bd.Y(), bd.Z()}
			}
			// A ray reports what it can SEE, so it is cast against the drawn
			// mesh — the detail one for a detailed brush. That deliberately
			// differs from where a BODY stops, which is always the hull: a
			// crosshair lands on the ledge it is pointing at, while the player
			// walks past the flat wall the ledge is carved into.
			faces := b.DrawFaces()
			for fi := range faces {
				face := &faces[fi]
				// A NoDraw face has no uploaded geometry to test at all — see
				// Brush.Load — so it is transparent to a ray the same way it
				// is invisible to the eye.
				t, ok := rayGeometry(bOrigin, bDir, &face.Geometry)
				if !ok || t > f.Range {
					continue
				}
				out = append(out, RayHit{
					Kind: RayHitBrush, Chunk: ch.ID, ID: b.ID,
					T: t, Point: origin.Add(dir.Mul(t)),
				})
			}
		}
	}
	return out
}
