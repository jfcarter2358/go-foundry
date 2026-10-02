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
// A ray does not walk every triangle. Each chunk keeps a bounding-volume tree
// over its faces, a compiled level keeps one over its surfaces, and bodies are
// tested against a bounding sphere before their exact shape — see
// raycast_accel.go. Geometry is found first and its nearest hit then bounds how
// far every body has to be looked for. On a level of ten thousand brushes a
// short sight line costs microseconds, which is what lets every AI agent in a
// level look for its targets several times a second; LineBlocked is the cheaper
// still any-hit form of that question.
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
	// RayHitTerrain is a chunk's block terrain. Chunk and ID both name
	// the chunk, which is how a script addresses it.
	RayHitTerrain
)

// Blocks reports whether a hit of this kind ends the ray. The level's own shape
// does; the things standing in it do not.
func (k RayHitKind) Blocks() bool {
	switch k {
	case RayHitBrush, RayHitScenery, RayHitTerrain:
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

	// Player is a body to test beside the level's entities, reported as an
	// entity hit with its own ID. The player is not in Level.Entities (see
	// world/entity.go), so without it no ray could meet the player at all —
	// which is right for a shot fired from the player's own eye, and wrong for
	// one fired at them. Nil leaves the player out. Taking part needs
	// Entities as well, and Ignore still applies.
	Player *world.Entity
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

	// Geometry first, because it decides how far everything else has to be
	// looked for: only the nearest piece of the level's shape survives the
	// truncation below, and nothing beyond it is reported. So the nearest
	// geometry hit becomes the range the scenery, the entities and the volumes
	// are tested within — which for a shot fired at a wall a metre away is
	// nearly nothing. A body exactly at that distance is still tested, since
	// the sort puts it ahead of the wall it touches.
	var hits []RayHit
	limit := f.Range
	if f.Geometry {
		q := geometryQuery{lvl: lvl, origin: origin, dir: dir, ignore: f.Ignore, tMax: limit}
		q.run()
		hits = append(hits, q.nearest()...)
		if q.hit {
			limit = q.tMax
		}
		hits, limit = appendSceneryHits(hits, lvl, origin, dir, f, limit)
	}
	if f.Entities {
		hits = appendEntityHits(hits, lvl, origin, dir, f, limit)
	}
	if f.Volumes {
		hits = appendVolumeHits(hits, lvl, origin, dir, f, limit)
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
		if e == nil && f.Player != nil && h.ID == f.Player.ID {
			e = f.Player
		}
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

	if h.Kind == RayHitTerrain {
		ch := world.FindChunkByID(lvl, h.Chunk)
		return ch != nil && (f.Tag == "" || ch.HasTag(f.Tag))
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

func appendEntityHits(out []RayHit, lvl *world.Level, origin, dir mgl32.Vec3, f RayFilter, limit float32) []RayHit {
	n := len(lvl.Entities)
	if f.Player != nil {
		n++
	}
	for i := 0; i < n; i++ {
		var e *world.Entity
		if i < len(lvl.Entities) {
			e = &lvl.Entities[i]
		} else {
			e = f.Player
		}
		if (len(f.Ignore) > 0 && f.Ignore[e.ID]) || !e.HasBounds() {
			continue
		}
		// A sphere first, about the entity's origin and wide enough for its
		// volume at any turn — most bodies are nowhere near the ray, and the
		// exact test builds a rotation and inverts a matrix. A frame is rigid,
		// so carrying the origin out of it keeps every distance.
		frame := lvl.Frames.FrameFor(e)
		half := e.BoundHalfExtents()
		sc := e.EffectiveScale()
		off := mgl32.Vec3{e.BoundOffset.X() * sc.X(), e.BoundOffset.Y() * sc.Y(), e.BoundOffset.Z() * sc.Z()}
		if !sphereReaches(origin, dir, frame.ToWorld(e.Position), (off.Len()+half.Len())*1.001+1e-4, limit) {
			continue
		}
		// An entity riding in a frame describes itself in that frame's
		// coordinates, so the frame goes on the outside — the same composition
		// RenderEntities uses to draw it.
		c := e.BoundCenter()
		m := frame.WorldMatrix().
			Mul4(mgl32.Translate3D(c.X(), c.Y(), c.Z())).
			Mul4(e.RotationMatrix())
		// A ducking body is the bottom HitScale of its bounds: the volume
		// shrinks toward the feet, which stay where they were.
		if s := e.HitScale(); s < 1 {
			m = m.Mul4(mgl32.Translate3D(0, -half.Y()*(1-s), 0))
			half[1] *= s
		}
		t, ok := rayBounds(origin, dir, m, half, e.BoundingKind)
		if !ok || t > limit {
			continue
		}
		out = append(out, RayHit{
			Kind: RayHitEntity, ID: e.ID, T: t, Point: origin.Add(dir.Mul(t)),
		})
	}
	return out
}

func appendVolumeHits(out []RayHit, lvl *world.Level, origin, dir mgl32.Vec3, f RayFilter, limit float32) []RayHit {
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		// A disabled volume contains nothing, which is the whole of what
		// disabling one means — so it is not there to be hit either.
		if (len(f.Ignore) > 0 && f.Ignore[v.ID]) || !v.Enabled {
			continue
		}
		half := mgl32.Vec3{v.HalfX, v.HalfY, v.HalfZ}
		if half.X() <= 0 || half.Y() <= 0 || half.Z() <= 0 {
			continue
		}
		// A world volume's centre is its position, so the sphere needs no
		// matrix at all; an attached one's needs its carrier's.
		if v.Attach.IsZero() {
			if !sphereReaches(origin, dir, v.Position, half.Len()*1.001+1e-4, limit) {
				continue
			}
		}
		// Through what it rides. rayBounds takes a general inverse, so a
		// carrier's scale is honoured along with its placement.
		m := v.WorldMatrix(lvl)
		if !v.Attach.IsZero() && !sphereReaches(origin, dir, m.Col(3).Vec3(), half.Len()*maxColumnScale(m)*1.001+1e-4, limit) {
			continue
		}
		t, ok := rayBounds(origin, dir, m, half, world.BoundingType(v.Shape))
		if !ok || t > limit {
			continue
		}
		out = append(out, RayHit{
			Kind: RayHitVolume, ID: v.ID, T: t, Point: origin.Add(dir.Mul(t)),
		})
	}
	return out
}

// appendSceneryHits tests the solid props, through the placement broadphase
// (scenery_index.go), each in its collision mode: the box a body stops at,
// or a cooked mesh's hulls or triangles (scenery_mesh.go) — so a prop stops
// a shot exactly where it stops a body. A prop blocks, so only the nearest
// can matter, and it lowers the limit everything else is tested within.
func appendSceneryHits(out []RayHit, lvl *world.Level, origin, dir mgl32.Vec3, f RayFilter, limit float32) ([]RayHit, float32) {
	i, t, _, ok := sceneryRayNearest(lvl, origin, dir, limit, f.Ignore, false)
	if !ok || t > limit {
		return out, limit
	}
	return append(out, RayHit{
		Kind: RayHitScenery, ID: lvl.Scenery[i].ID, T: t, Point: origin.Add(dir.Mul(t)),
	}), t
}

// ---------- geometry ----------
//
// Traversal, culling and the trees it walks are raycast_accel.go's. What stays
// here is the one test every piece of geometry comes down to.

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
