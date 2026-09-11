package collision

// raycast.go — one ray against the level, and what it met on the way.
//
// The engine has had ray casting since the 3D editor could be clicked in, but
// only in internal/editor, against every authoring aid as well as the world,
// and phrased as "which handle did the user grab". Gameplay wants a different
// question — "what is in front of the player" — so this is a second cast rather
// than a shared one, and it answers in the terms scripts address things in.
//
// Two kinds of thing are tested two different ways, deliberately:
//
//   - Bodies (entities, volumes, solid scenery) are tested against their
//     *bounding volume*, the same one `:enter:` and the push-apart resolver
//     work in. A skinned model's CPU vertices are its bind pose — the GPU holds
//     the posed mesh — so hitting triangles would put a walking guard's
//     hitbox wherever it was standing when the artist exported it.
//   - Geometry (brush faces) is tested against its real triangles, because a
//     shaped or displaced face has no bounding volume worth the name, and the
//     surface is static so its vertices are exactly what is drawn.
//     Geometry.Load fills those in with no GL context
//     (gfx.GeometryUploadsEnabled), which is what keeps this testable headless.
//
// One ray walks every triangle of every enabled chunk, which is what the
// editor's own picker does on a click and is fine at the rate gameplay fires
// shots. A script casting one per object per frame is not what this is for.
//
// Only geometry stops the ray. Bodies are reported and passed through, so a
// caster that wants the nearest takes the first hit and one that wants a line
// of fire takes them all — a cast that stopped at the first entity could not
// answer the second question at all.

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/gfx"
	"foundry/internal/world"
)

// RayHitKind is what a ray met.
type RayHitKind int

const (
	RayHitEntity RayHitKind = iota
	RayHitVolume
	// RayHitBrush is a face of the level's solid geometry.
	RayHitBrush
	// RayHitScenery is a solid prop. It stops the ray like any other piece of
	// the level's shape, but nothing addresses a scenery placement by name —
	// see world/scenery.go — so a caller has nothing to report for it beyond
	// the fact that the ray ended there.
	RayHitScenery
)

// Blocks reports whether a hit of this kind ends the ray. The level's own shape
// does; the things standing in it do not.
func (k RayHitKind) Blocks() bool {
	switch k {
	case RayHitBrush, RayHitScenery:
		return true
	}
	return false
}

// RayHit is one thing a ray met, named the way a script addresses it: by ID
// rather than by index, for the reason script_ref.go gives — an index does not
// survive the level being rebuilt.
type RayHit struct {
	Kind RayHitKind
	// Chunk is set for a brush hit and empty otherwise.
	Chunk string
	// ID is the entity's, the volume's, the scenery placement's, or the
	// brush's. Brush IDs are level-unique (see world/identity.go), so this
	// alone is enough to look one up.
	ID string
	// T is the distance along the ray, in world units, and Point is where it
	// landed.
	T     float32
	Point mgl32.Vec3
}

// RayFilter narrows a cast. The zero value reports nothing and stops at
// nothing, so callers are expected to fill it in; DefaultRayFilter is the
// sensible starting point.
//
// The switches divide into two kinds, and the division is the whole of what
// makes them composable. Range, Ignore and Geometry are *physical*: they change
// where the ray goes and what ends it. Tag and VisibleOnly are *reporting*
// filters, applied after the ray has already been stopped — so a wall carrying
// no tag still ends a tag-filtered cast rather than letting it shoot through
// the room.
type RayFilter struct {
	// Range is how far the ray travels, in world units. Zero or negative casts
	// nothing.
	Range float32

	// Entities, Volumes and Geometry say which kinds take part at all.
	// Clearing Geometry is what makes a cast see through walls.
	Entities bool
	Volumes  bool
	Geometry bool

	// VisibleOnly reports only what is drawn: entities and walls whose visible
	// flag is set. It never changes what stops the ray.
	VisibleOnly bool

	// Tag reports only objects carrying it. Empty means every object.
	Tag string

	// Ignore is a set of object IDs the ray passes through entirely, blocking
	// included — how a caster excludes itself.
	Ignore map[string]bool
}

// DefaultRayFilter is what a cast with nothing said about it does: entities and
// the level's shape, volumes left out, everything reported.
//
// Volumes are out by default because a trigger region is not a thing anybody
// aims at — a level's rooms are routinely wrapped in one, and including them
// would make almost every shot report the room before it reported what was
// standing in it.
func DefaultRayFilter(rng float32) RayFilter {
	return RayFilter{Range: rng, Entities: true, Geometry: true}
}

// RayResult is a finished cast.
type RayResult struct {
	// Hits are the reported hits, nearest first.
	Hits []RayHit
	// Stop is where the ray ended: the blocking hit's point, or the far end of
	// its range when nothing stopped it. Useful even when Hits is empty, which
	// is what a shot into the sky is.
	Stop mgl32.Vec3
	// Blocked reports whether the level's own shape ended the ray.
	Blocked bool
}

// First returns the nearest reported hit, or false when the ray reported none.
func (r RayResult) First() (RayHit, bool) {
	if len(r.Hits) == 0 {
		return RayHit{}, false
	}
	return r.Hits[0], true
}

// Raycast casts one ray through the level and returns what it met.
//
// dir need not be normalised; it is, so that T is a distance in world units and
// comparable against Range whatever the caller handed in.
func Raycast(lvl *world.Level, origin, dir mgl32.Vec3, f RayFilter) RayResult {
	res := RayResult{Stop: origin}
	if lvl == nil || f.Range <= 0 || dir.Len() < 1e-6 {
		return res
	}
	dir = dir.Normalize()
	res.Stop = origin.Add(dir.Mul(f.Range))

	var hits []RayHit
	if f.Entities {
		hits = appendEntityHits(hits, lvl, origin, dir, f)
	}
	if f.Volumes {
		hits = appendVolumeHits(hits, lvl, origin, dir, f)
	}
	if f.Geometry {
		hits = appendGeometryHits(hits, lvl, origin, dir, f)
		hits = appendSceneryHits(hits, lvl, origin, dir, f)
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
		if reportable(lvl, h, f) {
			res.Hits = append(res.Hits, h)
		}
	}
	return res
}

// reportable applies the filters that narrow what a cast *says* it saw, as
// opposed to the ones that change where the ray goes.
func reportable(lvl *world.Level, h RayHit, f RayFilter) bool {
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

func appendEntityHits(out []RayHit, lvl *world.Level, origin, dir mgl32.Vec3, f RayFilter) []RayHit {
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

func appendVolumeHits(out []RayHit, lvl *world.Level, origin, dir mgl32.Vec3, f RayFilter) []RayHit {
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
func appendSceneryHits(out []RayHit, lvl *world.Level, origin, dir mgl32.Vec3, f RayFilter) []RayHit {
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

func appendGeometryHits(out []RayHit, lvl *world.Level, origin, dir mgl32.Vec3, f RayFilter) []RayHit {
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

// rayGeometry returns the nearest hit among a de-indexed [X,Y,Z,U,V] triangle
// list, which is the one shape every buffer in the engine has once it has been
// through Geometry.Load.
func rayGeometry(origin, dir mgl32.Vec3, g *gfx.Geometry) (float32, bool) {
	if g == nil {
		return 0, false
	}
	v := g.Vertices
	best, found := float32(math.MaxFloat32), false
	for i := 0; i+14 < len(v); i += 15 {
		t, ok := rayTriangle(
			origin, dir,
			mgl32.Vec3{v[i+0], v[i+1], v[i+2]},
			mgl32.Vec3{v[i+5], v[i+6], v[i+7]},
			mgl32.Vec3{v[i+10], v[i+11], v[i+12]},
		)
		if ok && t < best {
			best, found = t, true
		}
	}
	return best, found
}

// rayTriangle is Möller–Trumbore, double-sided: a wall is routinely seen from
// the side it was not wound for, and a floor is a ceiling from underneath.
func rayTriangle(origin, dir, a, b, c mgl32.Vec3) (float32, bool) {
	const eps = 1e-7
	e1, e2 := b.Sub(a), c.Sub(a)
	p := dir.Cross(e2)
	det := e1.Dot(p)
	if det > -eps && det < eps {
		return 0, false
	}
	invDet := 1 / det
	tv := origin.Sub(a)
	u := tv.Dot(p) * invDet
	if u < 0 || u > 1 {
		return 0, false
	}
	q := tv.Cross(e1)
	vv := dir.Dot(q) * invDet
	if vv < 0 || u+vv > 1 {
		return 0, false
	}
	t := e2.Dot(q) * invDet
	if t < 0 {
		return 0, false
	}
	return t, true
}

// rayBounds intersects a ray with a bounding volume, reading the three kinds the
// same way world.ShapeContainsPoint does: half-extents are the box the shape is
// inscribed in, and a non-uniform "sphere" is the ellipsoid inside that box.
//
// m is the volume's rigid local-to-world transform — its centre and rotation,
// with scale already folded into half. The ray is moved into that space rather
// than the volume out of it, which is what keeps a rotated cylinder a cylinder;
// t survives the move because origin and direction go through together.
//
// A ray starting inside the volume hits it at t=0 rather than missing: the
// caster is in the thing, which is a hit by any reading.
func rayBounds(origin, dir mgl32.Vec3, m mgl32.Mat4, half mgl32.Vec3, kind world.BoundingType) (float32, bool) {
	if half.X() <= 0 || half.Y() <= 0 || half.Z() <= 0 {
		return 0, false
	}
	inv := m.Inv()
	if inv == (mgl32.Mat4{}) {
		return 0, false
	}
	o4 := inv.Mul4x1(origin.Vec4(1))
	d4 := inv.Mul4x1(dir.Vec4(0))
	o := mgl32.Vec3{o4.X(), o4.Y(), o4.Z()}
	d := mgl32.Vec3{d4.X(), d4.Y(), d4.Z()}

	switch kind {
	case world.BoundingSphere:
		// Normalising each axis by its extent turns the ellipsoid into the unit
		// sphere. Scaling o and d by the same factors leaves t meaning what it
		// meant, so no unscaling is needed on the way out.
		return rayUnitSphere(divVec(o, half), divVec(d, half))
	case world.BoundingCylinder:
		return rayUnitCylinder(divVec(o, half), divVec(d, half))
	default:
		return raySlab(o, d, half)
	}
}

// raySlab is the standard three-slab box test.
func raySlab(o, d, half mgl32.Vec3) (float32, bool) {
	tmin, tmax := float32(0), float32(math.MaxFloat32)
	for axis := 0; axis < 3; axis++ {
		lo, hi := -half[axis], half[axis]
		if abs32(d[axis]) < 1e-9 {
			// Parallel to this pair of faces: a miss unless it is already
			// between them.
			if o[axis] < lo || o[axis] > hi {
				return 0, false
			}
			continue
		}
		t1 := (lo - o[axis]) / d[axis]
		t2 := (hi - o[axis]) / d[axis]
		if t1 > t2 {
			t1, t2 = t2, t1
		}
		if t1 > tmin {
			tmin = t1
		}
		if t2 < tmax {
			tmax = t2
		}
		if tmin > tmax {
			return 0, false
		}
	}
	return tmin, true
}

// rayUnitSphere solves |o + t·d|² = 1 for the nearest non-negative t.
func rayUnitSphere(o, d mgl32.Vec3) (float32, bool) {
	a := d.Dot(d)
	if a < 1e-12 {
		return 0, false
	}
	b := 2 * o.Dot(d)
	c := o.Dot(o) - 1
	disc := b*b - 4*a*c
	if disc < 0 {
		return 0, false
	}
	root := float32(math.Sqrt(float64(disc)))
	t1 := (-b - root) / (2 * a)
	t2 := (-b + root) / (2 * a)
	return nearestForward(t1, t2)
}

// rayUnitCylinder is the unit-radius, unit-half-height upright cylinder: the
// infinite tube clipped to |y| ≤ 1, plus the two caps.
func rayUnitCylinder(o, d mgl32.Vec3) (float32, bool) {
	// The tube, solved in XZ alone.
	a := d.X()*d.X() + d.Z()*d.Z()
	best, found := float32(math.MaxFloat32), false
	keep := func(t float32) {
		if t >= 0 && t < best {
			best, found = t, true
		}
	}
	if a > 1e-12 {
		b := 2 * (o.X()*d.X() + o.Z()*d.Z())
		c := o.X()*o.X() + o.Z()*o.Z() - 1
		if disc := b*b - 4*a*c; disc >= 0 {
			root := float32(math.Sqrt(float64(disc)))
			for _, t := range [2]float32{(-b - root) / (2 * a), (-b + root) / (2 * a)} {
				if y := o.Y() + t*d.Y(); y >= -1 && y <= 1 {
					keep(t)
				}
			}
		}
	}
	// The caps.
	if abs32(d.Y()) > 1e-9 {
		for _, y := range [2]float32{-1, 1} {
			t := (y - o.Y()) / d.Y()
			px, pz := o.X()+t*d.X(), o.Z()+t*d.Z()
			if px*px+pz*pz <= 1 {
				keep(t)
			}
		}
	}
	if !found {
		return 0, false
	}
	// Inside the volume: every surface is behind the origin in the sense that
	// matters, so the hit is where the caster stands.
	if inUnitCylinder(o) {
		return 0, true
	}
	return best, true
}

func inUnitCylinder(o mgl32.Vec3) bool {
	return o.Y() >= -1 && o.Y() <= 1 && o.X()*o.X()+o.Z()*o.Z() <= 1
}

// nearestForward picks the nearer of two roots that is not behind the ray,
// reporting 0 when the origin is between them — inside the volume.
func nearestForward(t1, t2 float32) (float32, bool) {
	if t1 > t2 {
		t1, t2 = t2, t1
	}
	if t2 < 0 {
		return 0, false
	}
	if t1 < 0 {
		return 0, true
	}
	return t1, true
}

func divVec(v, by mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{v.X() / by.X(), v.Y() / by.Y(), v.Z() / by.Z()}
}

// ---------- lookups ----------
//
// By ID rather than by index, because a RayHit outlives the walk that produced
// it and the level underneath it can be rebuilt wholesale.

func entityByID(lvl *world.Level, id string) *world.Entity {
	for i := range lvl.Entities {
		if lvl.Entities[i].ID == id {
			return &lvl.Entities[i]
		}
	}
	return nil
}

func volumeByID(lvl *world.Level, id string) *world.Volume {
	for i := range lvl.Volumes {
		if lvl.Volumes[i].ID == id {
			return &lvl.Volumes[i]
		}
	}
	return nil
}

// brushByID finds a brush anywhere in the level by its (level-unique) ID.
func brushByID(lvl *world.Level, id string) *world.Brush {
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		for bi := range ch.Brushes {
			if ch.Brushes[bi].ID == id {
				return &ch.Brushes[bi]
			}
		}
	}
	return nil
}
