package world

import (
	"foundry/internal/geom"

	"github.com/go-gl/mathgl/mgl32"
)

// Chunk is a top-level spatial grouping of brushes.
//
// Brush verts, MinBound/MaxBound, and every other per-chunk coordinate are
// chunk-LOCAL, always — authored around the chunk's own origin, the same way
// a frame-owned chunk's geometry always has been. Origin/Rotation/Scale is
// this chunk's own live placement, a counterpart to a frame's Xform:
// ModelMatrix() is the one place that carries local coordinates into world
// space (or into an owning frame's space, for a frame-owned chunk — see
// FrameOwner below). Nothing else needs to know whether a chunk has moved.
//
// There is no shared point pool the way sectors had one: a sector's edges
// needed to share exact points with a neighbouring sector for a portal to
// line up, but a brush owns its verts outright and is never required to
// share topology with whatever else is nearby.
type Chunk struct {
	ID         string
	ScriptData // tags, attributes and the attached script
	Brushes    []Brush
	MinBound   mgl32.Vec3 // chunk-local; see WorldBounds for the world-space AABB
	MaxBound   mgl32.Vec3
	Origin     mgl32.Vec3 // this chunk's live translation; see ModelMatrix
	Rotation   float32    // this chunk's live Y-axis rotation, in radians; see ModelMatrix
	Scale      float32    // this chunk's live uniform scale (0 = 1.0); see ModelMatrix

	// Velocity is chunk units per second, added to Origin every tick of play
	// (AdvanceChunks, chunk_motion.go) — a lift that rises, a barge that
	// drifts, a wall that closes in, with nothing writing the origin every
	// frame to do it. Authored state the engine carries forward, exactly like
	// an entity's own Velocity: nothing damps it and nothing stops it, because
	// what should slow a platform down is a rule the game has rather than one
	// the engine invents.
	//
	// It is in the same space as Origin — world for an ordinary chunk, the
	// carrier's for a frame-owned one — so a moving chunk inside a moving ship
	// moves within the ship.
	//
	// A body standing on it is not carried just by colliding with it — that
	// still leaves it behind, exactly as before. An entity can be pinned to
	// this chunk explicitly instead, via its own AttachedChunk: see
	// CarryAttachedEntities, which every writer of Origin (this one included)
	// is expected to call. An interior meant to be properly ridden — walked
	// around inside, crossed through a portal — belongs to a frame (FrameOwner
	// below) and a vehicle, not this.
	Velocity mgl32.Vec3

	// OriginTween is a script easing Origin toward a target — `move_to` — or
	// nil. While it runs Velocity waits. Runtime-only: not saved and not
	// cloned. See tween.go.
	OriginTween *Tween

	// AngularVelocity is radians per second added to Rotation every tick of
	// play (AdvanceChunks, chunk_motion.go) — Velocity's rotational
	// counterpart, and the same contract: authored/live state the engine
	// carries forward, nothing damps it, and a body standing on the chunk is
	// not carried by it. A turntable, a revolving door built from several
	// brushes in one Moves chunk, or the rotating half of a lift that also
	// rises.
	AngularVelocity float32

	// RotationTween is a script easing Rotation toward a target — `rotate_to`
	// — or nil. While it runs AngularVelocity waits, the same bargain
	// OriginTween strikes with Velocity. Runtime-only: not saved and not
	// cloned. Stored as a Vec3 (see tween.go) with the target angle in X and
	// the rest unused, the same Tween type OriginTween already uses — a
	// chunk only ever turns about Y, so a second tween TYPE just for a
	// scalar would be new machinery for one fewer float per value.
	RotationTween *Tween

	// Waypoints is the chunk's progress along a waypoint animation (`
	// play_waypoints`), or nil. Runtime-only: not saved and not cloned, the
	// same as every other kind's — see waypoint_follow.go. While it is set,
	// AdvanceChunks writes Origin/Rotation from the path directly and
	// Velocity/AngularVelocity and the two tweens above all wait, the same
	// precedence a tween already has over a velocity.
	Waypoints *WaypointPlayback

	// Disabled takes the chunk out of the world: nothing draws it and nothing
	// collides with it. It is what a streaming script toggles as the player
	// moves between areas — see chunk_enable.go, which owns this flag,
	// UnloadWhenDisabled and Unloaded.
	//
	// It is only a flag. The chunk's geometry stays built while it is off, so
	// switching an area on mid-play costs an index rebuild rather than a
	// frame-stalling rebuild of every mesh; UnloadWhenDisabled is the opt-in
	// for a level that needs the memory back instead.
	//
	// Disabled rather than Enabled so the zero value is "on": every chunk
	// written before this existed, and every one a test or an import builds by
	// hand, means to be part of the level.
	Disabled bool

	// UnloadWhenDisabled makes switching the chunk off release its geometry as
	// well — vertex buffers and CPU vertices — and switching it back on rebuild
	// it, which is the expensive half. Authored, default false: a level has to
	// ask for the trade of memory against a stall. A chunk with it set that
	// loads disabled is never built at load either.
	UnloadWhenDisabled bool

	// Streamed tells the compiler to bake this chunk even though it is
	// disabled in the file: its surfaces go into the compiled level, start
	// hidden, and appear when a script switches the chunk on — a flag flip,
	// with no geometry to build. Without it a disabled chunk is not in the
	// world at compile time and is drawn live instead. Authored; see
	// compile.Bakes.
	//
	// Everything the compiler bakes is compiled as present at once — one CSG,
	// one BSP, one visibility set, one light bake — so streamed chunks have to
	// be able to coexist: neighbouring areas of one level, not alternatives
	// that occupy the same space at different times.
	Streamed bool

	// Moves marks a chunk a script moves at run time — a lift, a hangar that
	// drops away. The compiler never bakes it (compiled surfaces are drawn in
	// world space and would stay behind while its collision moved), and it is
	// drawn live like a vehicle's interior. Authored; see compile.Bakes.
	Moves bool

	// Terrain is the chunk's block terrain, or nil. It is chunk-local
	// like the brushes, drawn, collided with and baked into navigation beside
	// them, and never goes through CSG: see terrain.go.
	Terrain *Terrain

	// Unloaded is runtime-only (never serialized, never cloned): whether the
	// chunk's geometry has actually been released. It trails the flags by up
	// to a frame, because writing a flag may happen anywhere while freeing
	// buffers is gl.* work — SyncChunkGeometry is what closes the gap.
	Unloaded bool

	// indexedDisabled is runtime-only: the Disabled value SyncChunkGeometry
	// last reported to its caller, which is what the level indexes were
	// rebuilt from. Toggling no longer changes residency, so without it a
	// script switching a chunk off would leave nothing to say the spatial
	// hash is stale — and FindChunkSH trusts the hash. Unexported, so gob
	// leaves it out of a compiled level.
	indexedDisabled bool

	// FrameOwner names the entity whose local space this chunk's geometry is
	// authored in — a vehicle's interior, carried around the world by whatever
	// owns it. Empty is the ordinary case: a chunk placed directly in world
	// space by its own Origin/Rotation/Scale, with no carrier.
	//
	// A frame-owned chunk is deliberately kept out of the world spatial hash.
	// Its geometry sits around its own origin, so indexing it by those
	// coordinates would have a player standing at world (0,0) fall into a ship
	// parked a mile away.
	FrameOwner string

	// FromEntity is runtime-only (never serialized, never cloned): the entity
	// whose *definition* brought this chunk into the level, as one of its chunk
	// attachments (entity_chunk.go). Empty for a chunk the level file authored.
	//
	// It is what keeps the two kinds apart at save time. A materialised chunk is
	// the definition's content, not the level's, so LevelToRaw skips it — a
	// level that wrote one out would freeze a copy of the ship's hull into
	// itself and stop tracking the file every other level places.
	//
	// Not copied by Clone: a duplicated chunk is a new piece of level geometry
	// that no entity owns, and carrying the binding over would produce a second
	// chunk claiming to be the same attachment.
	FromEntity string

	// FromPrefab is runtime-only, on exactly the terms FromEntity is: the
	// prefab instance whose file brought this chunk into the level
	// (prefab.go). Empty for a chunk the level file authored.
	//
	// Two markers rather than one because the two answer different questions.
	// FromEntity says "this geometry belongs to a *type*, and every level
	// placing that type gets it"; FromPrefab says "this geometry belongs to a
	// *file this level references*". Both mean "do not write me out", and a
	// chunk can only be one or the other, but collapsing them would leave
	// nothing to say which file a stale copy came from.
	FromPrefab string

	// Xform is runtime-only (never serialized, never cloned): where this
	// chunk's frame currently is, refreshed every tick by RefreshFrames.
	// Identity for a chunk with no FrameOwner. ModelMatrix composes this with
	// the chunk's own Origin/Rotation/Scale, so a frame-owned chunk keeps its
	// authored placement within the frame even as the frame itself moves.
	Xform mgl32.Mat4

	// Compiled is runtime-only (never serialized, never cloned): this chunk's
	// surfaces are in the level's StaticGeometry, having been through CSG, and
	// must not be drawn from here as well.
	//
	// The chunk is still fully present for everything else — collision resolves
	// against its brushes, a raycast hits their triangles, a script addresses
	// them by name. See static.go on why a compiled level keeps its brushes at
	// all rather than inventing compiled equivalents of those three.
	Compiled bool

	// FrameScale is runtime-only alongside Xform (never serialized, never
	// cloned): the scale of the entity whose frame this chunk rides in,
	// republished every tick by RefreshFrames. 0 — what every unframed chunk
	// carries — reads as 1. Clear the two together with ClearFrameTransform.
	//
	// It is a field of its own rather than a factor folded into Xform because
	// the two are read in different spaces. Xform places the frame in the
	// world; this scales the chunk's geometry *within* that frame, which is the
	// space collision resolves in — the resolvers work from
	// LocalPlacementMatrix, having already had Xform divided out by the caller
	// that found the chunk. Folded into Xform, a ship scaled up would draw a
	// bigger hold with the deck a body walks on left exactly where it was.
	//
	// Scaling the carried geometry rather than the frame itself is also why a
	// rider is not scaled: the frame stays a rigid place to stand, so someone
	// boarding a ship scaled 2x keeps their own size and finds a hold twice as
	// big. See FrameLocalMatrix.
	FrameScale float32

	// convexPiecesGen/convexPieces cache ConvexBrushPieces' decomposition of
	// each concave solid brush, keyed by index into Brushes. Runtime-only
	// (not serialized, not cloned — a clone's brush indices and geometry can
	// both differ, so an inherited cache would either answer for the wrong
	// brush or simply be stale) and invalidated by comparing
	// convexPiecesGen against the global BrushMeshGeneration rather than
	// tracked per edit, the same trade the raycast BVH cache
	// (collision/raycast_accel.go) already makes: coarser than it needs to
	// be, but nothing here has to remember to invalidate it.
	convexPiecesGen uint64
	convexPieces    map[int][]BrushMesh
}

// Clone returns a fully-independent deep copy of the chunk. All slices get
// fresh backing arrays so edits to the copy do not affect the source. GPU
// geometry handles are intentionally zeroed — the caller must re-upload brush
// geometry before the clone can be rendered. Brush IDs are rewritten with
// idSuffix so they stay unique within the level.
func (ch *Chunk) Clone(newID, idSuffix string) Chunk {
	cp := Chunk{
		ID:         newID,
		ScriptData: CloneScriptData(ch.ScriptData),
		MinBound:   ch.MinBound,
		MaxBound:   ch.MaxBound,
		Origin:     ch.Origin,
		Rotation:   ch.Rotation,
		Scale:      ch.Scale,
		// Velocity and AngularVelocity ride along with the placement they are
		// rates of change of: a copy of a moving platform is another moving
		// platform, not one that has stopped dead where the original
		// happened to be. OriginTween/RotationTween deliberately do NOT — a
		// script easing the original chunk somewhere is not a reason the
		// copy should arrive there too.
		Velocity:        ch.Velocity,
		AngularVelocity: ch.AngularVelocity,
		// FrameOwner rides along with Origin/Rotation/Scale because it is part
		// of the same answer to "where is this chunk". Dropping it turned a
		// copy of a vehicle interior into a world chunk whose geometry — still
		// authored around the ship's local origin — was then read as world
		// geometry at (0,0,0). Xform is deliberately NOT copied: it is
		// runtime-only and RefreshFrames owns it (ModelMatrix treats the zero
		// value as "not yet refreshed" and falls back to the local placement),
		// and FrameScale is left behind with it for the same reason.
		FrameOwner: ch.FrameOwner,
		// Disabled rides along for the same reason FrameOwner does: it is part
		// of the answer to "is this chunk in the world". Unloaded deliberately
		// does NOT — the copy has no buffers of its own yet, and the caller is
		// already required to re-upload its geometry (see this method's doc).
		// indexedDisabled is left at its zero value too: the copy is in no
		// index yet, so the next sync reporting it is correct.
		Disabled:           ch.Disabled,
		UnloadWhenDisabled: ch.UnloadWhenDisabled,
		Streamed:           ch.Streamed,
		Moves:              ch.Moves,
		Terrain:            ch.Terrain.Clone(),
	}

	cp.Brushes = make([]Brush, len(ch.Brushes))
	for bi := range ch.Brushes {
		cp.Brushes[bi] = CloneBrush(&ch.Brushes[bi], idSuffix)
	}

	return cp
}

// FindChunkByID returns the chunk with the given level-unique ID, or nil.
func FindChunkByID(lvl *Level, id string) *Chunk {
	if lvl == nil || id == "" {
		return nil
	}
	for i := range lvl.Chunks {
		if lvl.Chunks[i].ID == id {
			return &lvl.Chunks[i]
		}
	}
	return nil
}

// RecomputeBounds re-derives the chunk's local AABB (MinBound/MaxBound; see
// WorldBounds for the world-space box used to frustum-cull the whole chunk
// before descending into per-brush culling) from its current brushes.
// Nothing else rolls this up automatically, so any editor operation that
// adds/removes/moves a brush without calling this leaves MinBound/MaxBound
// stale — geometry added outside the original bounds then gets
// frustum-culled along with the rest of the chunk even when it's on screen.
func (ch *Chunk) RecomputeBounds() {
	if len(ch.Brushes) == 0 && ch.Terrain == nil {
		return
	}
	first := true
	extend := func(min, max mgl32.Vec3) {
		if first {
			ch.MinBound = min
			ch.MaxBound = max
			first = false
			return
		}
		for i := 0; i < 3; i++ {
			if min[i] < ch.MinBound[i] {
				ch.MinBound[i] = min[i]
			}
			if max[i] > ch.MaxBound[i] {
				ch.MaxBound[i] = max[i]
			}
		}
	}

	for bi := range ch.Brushes {
		extend(ch.Brushes[bi].LocalAABB())
	}
	if ch.Terrain != nil {
		extend(ch.Terrain.LocalBounds())
	}
}

// reverseTriangleWinding turns every triangle in an index buffer round in
// place, so the geometry it describes faces the other way.
//
// Buffers are flat triples throughout (Geometry.Load de-indexes them in this
// order), so swapping the first and last of each triple is the whole job. A
// trailing partial triple is left alone rather than dropped: it is already
// malformed, and this is not the place that would notice.
func reverseTriangleWinding(indices []int) {
	for i := 0; i+2 < len(indices); i += 3 {
		indices[i], indices[i+2] = indices[i+2], indices[i]
	}
}

// SignedArea2D computes the signed area of a 2D polygon (shoelace formula).
// Positive = CCW winding (interior to the left); negative = CW.
func SignedArea2D(pts []mgl32.Vec2) float32 {
	n := len(pts)
	area := float32(0)
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		area += pts[i].X()*pts[j].Y() - pts[j].X()*pts[i].Y()
	}
	return area / 2
}

// InFrame reports whether this chunk's geometry is authored in a moving frame
// rather than in world space.
func (ch *Chunk) InFrame() bool { return ch.FrameOwner != "" }

// chunkLocalMatrix builds the transform a chunk's own Origin/Rotation/Scale
// describe: translate, then rotate about Y, then a uniform scale. Scale of 0
// means "not set" (1.0), the same convention the YAML loader has always used.
//
// The scale is uniform on purpose, not XZ-only: a chunk's local geometry
// (brush verts, baked straight in) is one coordinate space, and one matrix
// carries all of it into the parent space together.
func chunkLocalMatrix(origin mgl32.Vec3, rotation, scale float32) mgl32.Mat4 {
	sc := scale
	if sc == 0 {
		sc = 1
	}
	T := mgl32.Translate3D(origin.X(), origin.Y(), origin.Z())
	R := mgl32.HomogRotate3DY(rotation)
	S := mgl32.Scale3D(sc, sc, sc)
	return T.Mul4(R).Mul4(S)
}

// LocalPlacementMatrix is this chunk's own Origin/Rotation/Scale, without an
// owning frame's transform layered on — the piece of ModelMatrix a chunk
// contributes on its own. FindChunkAndSectorAmong uses exactly this: its
// caller has already carried the query position out of the frame, so only the
// chunk's own placement within that frame remains to undo.
// It carries the owning entity's scale (FrameScale) as well as the chunk's own,
// which is what makes a ship's hold grow with the ship: the geometry a rider
// walks on is expressed in this space, so a factor applied here reaches the
// drawn floor and the collided one together.
func (ch *Chunk) LocalPlacementMatrix() mgl32.Mat4 {
	local := chunkLocalMatrix(ch.Origin, ch.Rotation, ch.Scale)
	k := ch.EffectiveFrameScale()
	if k == 1 {
		return local
	}
	// Outside the chunk's own placement, so the owner's scale carries Origin
	// along with the geometry: an attachment a metre forward of a ship's centre
	// sits two metres forward once the ship is twice the size.
	return mgl32.Scale3D(k, k, k).Mul4(local)
}

// EffectiveScale is the chunk's own authored scale, with the zero value read as
// 1 — a chunk that never mentioned a scale is full size, not a point.
func (ch *Chunk) EffectiveScale() float32 {
	if ch.Scale == 0 {
		return 1
	}
	return ch.Scale
}

// EffectiveFrameScale is the same for the scale its frame owner contributes.
func (ch *Chunk) EffectiveFrameScale() float32 {
	if ch.FrameScale == 0 {
		return 1
	}
	return ch.FrameScale
}

// PlacementScale is the whole factor between this chunk's local units and the
// units of the space it is placed in — its own scale and its frame owner's
// together.
//
// Anything converting a *distance* across that boundary rather than a point —
// a click radius, a scatter range — has to go through this rather than
// reading Scale directly, or it is off by the owner's factor on every chunk
// an entity carries.
func (ch *Chunk) PlacementScale() float32 {
	return ch.EffectiveScale() * ch.EffectiveFrameScale()
}

// ClearFrameTransform drops the runtime placement a frame published onto this
// chunk, for when the chunk stops being carried. The two fields go together: a
// stale FrameScale left behind would keep resizing geometry no entity owns any
// more, and unlike Xform it has no "not yet refreshed" fallback to hide it.
func (ch *Chunk) ClearFrameTransform() {
	ch.Xform = mgl32.Mat4{}
	ch.FrameScale = 0
}

// ModelMatrix is the transform this chunk's local geometry is drawn/collided
// through: its own Origin/Rotation/Scale, composed under its owning frame's
// live Xform when it has one. Brush verts, MinBound/MaxBound and every other
// per-chunk coordinate are chunk-local; this is the one matrix that carries
// them into world space (or into the frame's space, for a frame-owned chunk —
// RefreshFrames only ever updates Xform, never this). Xform is runtime-only
// and its zero value is the ZERO matrix, not the identity — so a frame-owned
// chunk whose frames have not been refreshed is treated as if it were
// unframed rather than composed against a singular matrix. Without that guard
// such a chunk collapses to a point (LocalToWorld returns the origin for
// every vertex) and, worse, WorldToLocal inverts a singular matrix and hands
// back NaN, which then spreads silently through collision and picking.
// RebuildIndexes leaves exactly this state behind whenever BuildFrames fails —
// a chunk bound to a missing owner or a cycle — and some editor call sites
// ignore that error. Falling back to the chunk's own authored placement keeps
// the level usable.
func (ch *Chunk) ModelMatrix() mgl32.Mat4 {
	local := ch.LocalPlacementMatrix()
	if ch.InFrame() && ch.Xform != (mgl32.Mat4{}) {
		return ch.Xform.Mul4(local)
	}
	return local
}

// LocalToWorld carries a point out of this chunk's local space, through
// ModelMatrix.
func (ch *Chunk) LocalToWorld(p mgl32.Vec3) mgl32.Vec3 {
	m := ch.ModelMatrix()
	if m == mgl32.Ident4() {
		return p
	}
	r := m.Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// WorldToLocal carries a point into this chunk's local space — the inverse of
// LocalToWorld/ModelMatrix. Every collision or spatial-hash lookup that tests
// a world position against chunk-local geometry (brush verts,
// MinBound/MaxBound) goes through this first, so it is testing in the same
// space that geometry is actually expressed in.
func (ch *Chunk) WorldToLocal(p mgl32.Vec3) mgl32.Vec3 {
	m := ch.ModelMatrix()
	if m == mgl32.Ident4() {
		return p
	}
	r := m.Inv().Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// ---------- the chunk's own placement, stopping at the frame ----------
//
// LocalToWorld and its three companions above go all the way out to the world,
// through ModelMatrix. These four stop one step short, at the space the chunk
// is PLACED in — the world for an ordinary chunk, and the owning entity's own
// coordinates for one a frame carries.
//
// For every chunk that is not frame-owned the two matrices are the same thing,
// so these are exactly LocalToWorld and friends there and nothing has to choose
// between them. Where they diverge is where it matters: a brush that an entity
// carries has a position and a velocity that mean something in that entity's
// space and nothing much in the world's, since the entity is where they are
// authored, drawn and collided relative to. See script/script_props_geom.go,
// and collision.go's chunkLocal, which makes the same distinction for the same
// reason.

// LocalToPlacement carries a point out of this chunk's local space into the
// space the chunk is placed in.
func (ch *Chunk) LocalToPlacement(p mgl32.Vec3) mgl32.Vec3 {
	m := ch.LocalPlacementMatrix()
	if m == mgl32.Ident4() {
		return p
	}
	r := m.Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// PlacementToLocal is LocalToPlacement's inverse.
func (ch *Chunk) PlacementToLocal(p mgl32.Vec3) mgl32.Vec3 {
	m := ch.LocalPlacementMatrix()
	if m == mgl32.Ident4() {
		return p
	}
	r := m.Inv().Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// LocalToPlacementDirection is LocalToPlacement for a direction rather than a
// point — the W=0 multiply, which drops the translation a direction has no
// business being offset by.
func (ch *Chunk) LocalToPlacementDirection(d mgl32.Vec3) mgl32.Vec3 {
	m := ch.LocalPlacementMatrix()
	if m == mgl32.Ident4() {
		return d
	}
	r := m.Mul4x1(d.Vec4(0))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// PlacementToLocalDirection is LocalToPlacementDirection's inverse.
func (ch *Chunk) PlacementToLocalDirection(d mgl32.Vec3) mgl32.Vec3 {
	m := ch.LocalPlacementMatrix()
	if m == mgl32.Ident4() {
		return d
	}
	r := m.Inv().Mul4x1(d.Vec4(0))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// LocalToWorldDirection carries a direction — not a point — out of this
// chunk's local space. WorldToLocalDirection's inverse; see its own doc
// comment for why a direction needs its own conversion rather than
// LocalToWorld's.
func (ch *Chunk) LocalToWorldDirection(d mgl32.Vec3) mgl32.Vec3 {
	m := ch.ModelMatrix()
	if m == mgl32.Ident4() {
		return d
	}
	r := m.Mul4x1(d.Vec4(0))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// WorldToLocalDirection carries a direction — not a point — into this chunk's
// local space: the rotation and scale WorldToLocal applies, without the
// translation a direction has no business being offset by (the W=0 in the
// homogeneous multiply is what drops it). Used wherever an offset or a delta,
// rather than a position, needs to cross into a chunk's own space — an
// axis-constrained drag, say, where dragging one world unit along X should
// move a 2×-scaled chunk's own geometry by half a local unit, in whichever
// local direction its own rotation carries world X to.
func (ch *Chunk) WorldToLocalDirection(d mgl32.Vec3) mgl32.Vec3 {
	m := ch.ModelMatrix()
	if m == mgl32.Ident4() {
		return d
	}
	r := m.Inv().Mul4x1(d.Vec4(0))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// WorldBounds is the chunk's AABB in world space: MinBound/MaxBound carried
// through ModelMatrix and re-fitted.
func (ch *Chunk) WorldBounds() (min, max mgl32.Vec3) {
	return transformAABB(ch.ModelMatrix(), ch.MinBound, ch.MaxBound)
}

// PlacementBounds is WorldBounds stopping at the frame: the chunk's AABB in the
// space it is placed in, through LocalPlacementMatrix. The same box as
// WorldBounds for a chunk no frame carries.
func (ch *Chunk) PlacementBounds() (min, max mgl32.Vec3) {
	return transformAABB(ch.LocalPlacementMatrix(), ch.MinBound, ch.MaxBound)
}

// transformAABB carries a local axis-aligned box through m and re-fits it.
//
// A rotated box does not stay a box, so the eight corners are transformed and
// re-bounded rather than the two extremes being transformed in place — which
// would produce a box that is not even the right size, let alone conservative.
// m == identity is the common case (an unmoved, unrotated, unscaled chunk),
// short-circuited so most callers never pay for the corner loop.
func transformAABB(m mgl32.Mat4, lo, hi mgl32.Vec3) (min, max mgl32.Vec3) {
	if m == mgl32.Ident4() {
		return lo, hi
	}
	h := hi.Sub(lo).Mul(0.5)
	c := lo.Add(h)
	for i := 0; i < 8; i++ {
		corner := mgl32.Vec3{
			h.X() * CornerSign(i, 0),
			h.Y() * CornerSign(i, 1),
			h.Z() * CornerSign(i, 2),
		}
		r := m.Mul4x1(c.Add(corner).Vec4(1))
		p := mgl32.Vec3{r.X(), r.Y(), r.Z()}
		if i == 0 {
			min, max = p, p
			continue
		}
		min, max = geom.VecMin(min, p), geom.VecMax(max, p)
	}
	return min, max
}
