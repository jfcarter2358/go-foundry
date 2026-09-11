package world

// entity_brush.go — the geometry an entity is built out of.
//
// A model attachment (entity_model.go) gives an entity a *mesh*: a thing to
// look at, with no inside and nothing to stand on. This gives it *solid*:
// brushes — the same convex polyhedra a level is built from (brush.go) —
// authored around the entity's own origin and carried wherever it goes.
//
// This replaces the chunk attachment, which named a file under chunks/ and had
// the level materialise it. The reference-by-file arrangement was the right one
// when the only way to author world geometry was the level editor: a type could
// not hold geometry, so it borrowed a level's. Now that the entity editor edits
// brushes with the 3D view's own tools, a type holds its own, and the
// indirection bought nothing but a second file to keep in step. What is lost is
// sharing one hull between several types; what is gained is that an entity is
// one file, and that its brushes are *its* — addressable, scriptable, and moved
// by its own frame rather than by a chunk that happens to ride it.
//
// # Where the brushes are
//
// A brush's Verts are in the entity's LOCAL space, exactly as a level brush's
// are in its chunk's. The entity's position, rotation and scale carry them into
// the world, and the machinery that does it is the machinery that already
// existed: levelio materialises an entity's brushes into one Chunk whose
// FrameOwner is the entity (levelio/entity_geometry.go), so drawing, shadows,
// streaming, the spatial hash and the player walking around inside all work
// with no further change.
//
// # What that gets, for free
//
//   - A script that writes a brush's `velocity` moves it in the entity's own
//     coordinates. A door on a ship slides along the ship, not along the world,
//     because the chunk it lives in is the ship's frame. See brush_dynamic.go.
//   - A body standing on those brushes is inside the entity's frame, so it is
//     carried when the entity moves — it never moved, the coordinates did.
//
// What is NOT free, and lives in internal/collision, is being solid from the
// *outside*: a frame-owned chunk is deliberately absent from the world spatial
// hash (spatial.go), so a body that has not entered the frame would walk
// straight through the hull. See collision/entity_brushes.go.

import (
	"fmt"

	"github.com/go-gl/mathgl/mgl32"
)

// EntityGeometrySuffix is what an entity's ID is joined with to name the chunk
// its brushes materialise into. Readable in the Explorer and in a script
// address — `@shuttle.geometry` is plainly the shuttle's own solid — and
// distinct enough that a level authoring a chunk of the same name is a
// coincidence rather than a collision waiting to happen.
const EntityGeometrySuffix = "geometry"

// EntityGeometryChunkID is the ID the chunk built from an entity's brushes
// carries in a level.
//
// Qualified by the entity rather than a bare name, because two shuttles placed
// from one definition would otherwise both claim it, and a chunk ID has to name
// exactly one thing for `@id` to mean anything.
func EntityGeometryChunkID(entityID string) string {
	if entityID == "" {
		return EntityGeometrySuffix
	}
	return entityID + "." + EntityGeometrySuffix
}

// EntityBrushID is the level-unique ID one of an entity's brushes carries once
// materialised: the entity's ID and the brush's, joined.
//
// Brush IDs are level-unique (identity.go), and a definition's brush is named
// in the definition's own namespace — every shuttle's door is called "door".
// Qualifying by the placement is what lets a script address one of them.
func EntityBrushID(entityID, brushID string) string {
	if entityID == "" {
		return brushID
	}
	return entityID + "." + brushID
}

// ---------- what an entity's geometry is ----------

// HasBrushes reports whether this entity carries any solid at all — the test
// every path that would otherwise materialise an empty chunk takes first.
// Distinct from HasGeometry (entity.go), which asks whether anything on the
// entity would *draw*, and is about its models.
func (e *Entity) HasBrushes() bool { return e != nil && len(e.Brushes) > 0 }

// Brush looks up one of the entity's brushes by its *authored* ID — the name it
// carries in the definition, not the qualified one the materialised chunk gets.
func (e *Entity) Brush(id string) *Brush {
	if e == nil {
		return nil
	}
	for i := range e.Brushes {
		if e.Brushes[i].ID == id {
			return &e.Brushes[i]
		}
	}
	return nil
}

// GeometryAABB is the local-space box every one of the entity's brushes fits
// inside, and whether there was anything to measure.
//
// Local rather than world, because that is what the materialised chunk's own
// bounds are in and what the frame-entry test (collision/entity_brushes.go)
// compares against. A brush's run-time Offset is included: where the geometry
// IS is what a containment test has to mean, or a door that has slid open
// leaves a hole the bounds do not know about.
func (e *Entity) GeometryAABB() (min, max mgl32.Vec3, ok bool) {
	if e == nil {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	for i := range e.Brushes {
		bmin, bmax := e.Brushes[i].LocalAABB()
		if !ok {
			min, max, ok = bmin, bmax, true
			continue
		}
		for a := 0; a < 3; a++ {
			if bmin[a] < min[a] {
				min[a] = bmin[a]
			}
			if bmax[a] > max[a] {
				max[a] = bmax[a]
			}
		}
	}
	return min, max, ok
}

// ---------- serialization ----------
//
// An entity's brushes are written in the level's own brush format (RawBrush),
// not a form of their own. That is deliberate: they ARE level brushes, edited
// by the same tools, and a second schema would be a second place for a new
// face field to be forgotten.

// entityBrushesFromRaw converts a whole list, naming anything that arrived
// without an ID — an unnamed brush could be neither addressed nor overridden,
// and dropping it would lose geometry the author placed.
func entityBrushesFromRaw(raw []RawBrush, owner string) ([]Brush, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]Brush, 0, len(raw))
	seen := map[string]bool{}
	for i, r := range raw {
		b, err := BrushFromRaw(r)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", owner, err)
		}
		if b.ID == "" {
			b.ID = fmt.Sprintf("brush_%d", i+1)
		}
		if seen[b.ID] {
			return nil, fmt.Errorf("%s: duplicate brush %q", owner, b.ID)
		}
		seen[b.ID] = true
		out = append(out, b)
	}
	return out, nil
}

// entityBrushesToRaw is entityBrushesFromRaw's inverse.
func entityBrushesToRaw(brushes []Brush) []RawBrush {
	if len(brushes) == 0 {
		return nil
	}
	out := make([]RawBrush, 0, len(brushes))
	for i := range brushes {
		out = append(out, BrushToRaw(&brushes[i]))
	}
	return out
}

// CloneEntityBrushes deep-copies a brush list, so a definition and every
// instance stamped from it own their geometry outright.
//
// Deep rather than a slice copy, because a Brush holds slices — its verts, its
// faces, each face's index loop — and two entities sharing one backing array
// would mean dragging a corner of one barrel moved every barrel in the level.
// GPU handles are dropped on the way through (CloneBrush), so the copy is plain
// data until something loads it.
func CloneEntityBrushes(brushes []Brush) []Brush {
	if len(brushes) == 0 {
		return nil
	}
	out := make([]Brush, 0, len(brushes))
	for i := range brushes {
		out = append(out, CloneBrush(&brushes[i], ""))
	}
	return out
}

// EntityBrushesEqual reports whether two lists say the same thing, so an
// instance that never touched its type's geometry writes no `brushes:` block —
// and so the entity editor can tell a frame that changed the geometry from one
// that only moved the camera.
//
// Compared through the YAML form rather than field by field: that is exactly
// the set of properties that would be written out, so two brushes that compare
// equal here are two that would serialize identically — which is the only
// question the caller is actually asking. Run-time state (Offset, Velocity, GPU
// handles) is not in the raw form and correctly does not count.
func EntityBrushesEqual(a, b []Brush) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !rawBrushesEqual(BrushToRaw(&a[i]), BrushToRaw(&b[i])) {
			return false
		}
	}
	return true
}

// rawBrushesEqual compares two serialized brushes.
func rawBrushesEqual(a, b RawBrush) bool {
	if a.ID != b.ID || a.Dynamic != b.Dynamic || len(a.Verts) != len(b.Verts) ||
		len(a.Faces) != len(b.Faces) {
		return false
	}
	if (a.Solid == nil) != (b.Solid == nil) || (a.Solid != nil && *a.Solid != *b.Solid) {
		return false
	}
	for i := range a.Verts {
		if a.Verts[i] != b.Verts[i] {
			return false
		}
	}
	for i := range a.Faces {
		if !rawBrushFacesEqual(a.Faces[i], b.Faces[i]) {
			return false
		}
	}
	return (a.Detail == nil) == (b.Detail == nil)
}

// rawBrushFacesEqual compares two serialized faces, index loop included.
func rawBrushFacesEqual(a, b RawBrushFace) bool {
	if len(a.Indices) != len(b.Indices) {
		return false
	}
	for i := range a.Indices {
		if a.Indices[i] != b.Indices[i] {
			return false
		}
	}
	if (a.Alpha == nil) != (b.Alpha == nil) || (a.Alpha != nil && *a.Alpha != *b.Alpha) {
		return false
	}
	if len(a.UVs) != len(b.UVs) {
		return false
	}
	for i := range a.UVs {
		if a.UVs[i] != b.UVs[i] {
			return false
		}
	}
	return a.Texture == b.Texture && a.BlendTexture == b.BlendTexture &&
		a.BlendMask == b.BlendMask && a.UVOffset == b.UVOffset &&
		a.UVScale == b.UVScale && a.UVRotation == b.UVRotation &&
		a.Smooth == b.Smooth && a.SmoothAngle == b.SmoothAngle &&
		a.DoubleSided == b.DoubleSided && a.NoDraw == b.NoDraw &&
		a.NoCollide == b.NoCollide
}

// ---------- naming ----------

// UniqueEntityBrushID returns base, or base with a numeric suffix no brush in
// the list is using. What the entity editor's stamps name a new solid.
func UniqueEntityBrushID(brushes []Brush, base string) string {
	if base == "" {
		base = "brush"
	}
	taken := func(id string) bool {
		for i := range brushes {
			if brushes[i].ID == id {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	for n := 2; ; n++ {
		id := fmt.Sprintf("%s_%d", base, n)
		if !taken(id) {
			return id
		}
	}
}

// ---------- cloning a definition ----------

// Clone returns a fully independent copy of a definition — every slice and map
// with a fresh backing array, and the vehicle block copied rather than shared.
//
// It exists for the entity editor's undo, which snapshots whole documents the
// way the model and particle editors do. The rule that makes that safe is that
// nothing in an EntityDef holds GPU state: the models are templates, and
// CloneBrush zeroes a brush's buffer handles. A snapshot is therefore plain
// data, and restoring one is an assignment plus a preview rebuild.
//
// The shared *EntityDef handed out by LoadEntityDef must not be mutated (see
// its doc comment), so the editor edits a clone from the moment it opens one.
func (d *EntityDef) Clone() *EntityDef {
	if d == nil {
		return nil
	}
	c := *d
	c.Tags = append([]string(nil), d.Tags...)
	c.Attributes = d.Attributes.Clone()
	c.Params = d.Params.Clone()
	c.Brushes = CloneEntityBrushes(d.Brushes)
	// The effect and volume lists too. A plain struct copy shares their backing
	// arrays, so an undo snapshot of the effects would be the live list itself
	// and dragging a plume would rewrite the state undo was meant to restore.
	c.Emitters = CloneEntityEmitters(d.Emitters)
	c.Volumes = CloneEntityVolumes(d.Volumes)
	c.WaypointAnimations = CloneWaypointAnimations(d.WaypointAnimations)
	c.Models = nil
	for i := range d.Models {
		c.Models = append(c.Models, d.Models[i].TemplateCopy())
	}
	c.Vehicle = CloneVehicleSpec(d.Vehicle)
	return &c
}

// CloneVehicleSpec deep-copies a vehicle block. A definition normally *shares*
// its spec with every instance, because nothing at runtime mutates it — but an
// editor does, so the copy it edits has to be its own.
func CloneVehicleSpec(v *VehicleSpec) *VehicleSpec {
	if v == nil {
		return nil
	}
	c := *v
	c.Interiors = append([]string(nil), v.Interiors...)
	c.Seats = append([]VehicleSeat(nil), v.Seats...)
	return &c
}
