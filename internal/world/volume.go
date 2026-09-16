package world

// volume.go — Volume, a region of space a script can react to.
//
// A volume is a shape, an ID, and a script. It never collides and never moves on
// its own. Mostly what it does is answer "is this point inside", which is what
// the `enter`, `exit` and `interact` events resolve through — a doorway that
// opens, a room that dims the lights, a pit that kills.
//
// The one thing a volume can be that the player sees is fog: give it a Fog and
// the region fills with it, still invisible as a trigger but no longer invisible
// as a place. See volume_fog.go.
//
// A model-less entity could serve, and for a while the plan was that it should:
// it already has an ID, tags, attributes and a bounding volume. But an entity
// also carries velocity, gravity, four collision flags, model attachments and a
// definition it was instanced from, none of which a trigger has any use for.
// Telling an author "make a box entity, then turn off the three collision flags
// and give it no models" is a rule to remember rather than a thing to place, and
// it puts triggers in the same list as the crates and the barrels. A volume is
// the smaller idea, so it gets the smaller type.
//
// Volumes live on the level rather than in a chunk, for the same reason entities
// do: a trigger region can span a doorway between two chunks, and its authored
// position is world-space.

import (
	"foundry/internal/geom"
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// Volume is a scriptable region of space.
type Volume struct {
	// ID is unique within a level; a script addresses a volume by it (`@id`).
	ID string
	// ScriptData carries the tags, attributes and attached script — the same
	// three a sector or a wall has, so `#tag` reaches volumes on equal terms.
	ScriptData

	// Attach names what this volume rides, or nothing for the world. With it
	// set, Position and Rotation are in the carrier's coordinates — the binding
	// sprites, emitters and sounds share (attach.go), resolved the same way.
	//
	// A world-space volume was enough while every volume was a trigger bolted
	// to the level. A doorway (see FrameDoorway) is bolted to a *ship*, and a
	// trigger in a ship's hold — "the cargo is aboard", "someone is in the
	// cockpit" — wants the same thing. So does a region around a thing that
	// walks: a guard's field of view, the pickup radius on a crate. The frame
	// half of the binding is the first; the entity and bone half is the second.
	//
	// Only the frame half means anything to a doorway or a scatter, which are
	// questions about which space's chunks a region belongs to. See Frame.
	Attach Attach

	Position mgl32.Vec3
	Rotation mgl32.Vec3 // Euler angles (X, Y, Z) in radians

	// Shape and half-extents describe the region, using the same three kinds and
	// the same "box the shape is inscribed in" convention as an entity's bounds,
	// so one containment implementation serves both.
	Shape VolumeShape
	HalfX float32
	HalfY float32
	HalfZ float32

	// Enabled is what a script switches to arm or disarm a trigger without
	// having to move or delete it. A disabled volume contains nothing.
	Enabled bool

	// LoadLevel names the level to load when the player walks in — the level
	// exit, which is the one thing enough volumes do that making it an authored
	// field beats making everyone write the same three-line script. Empty is
	// the ordinary case: a volume that only does what its script says.
	//
	// The name is what `load_level` takes, and it goes through exactly that:
	// a request the frame loop picks up between frames, covered by the
	// `loading` screen. A volume may carry both this and a script, and the
	// script's `:enter:` still runs — it fires in the same frame the load is
	// requested, before the load starts, which is where a "save the player's
	// progress" handler belongs.
	//
	// Only the PLAYER triggers it. A patrolling guard crossing the exit should
	// not end the level, and there is no reading of "a player entered" that
	// includes them.
	LoadLevel string

	// Scatter fills the region with instanced foliage when set — see
	// volume_scatter.go. nil is the ordinary case.
	Scatter *VolumeScatter

	// Doorway makes the volume a frame doorway: standing in it is being aboard
	// the frame it belongs to. See volume_doorway.go. nil is the ordinary case.
	Doorway *FrameDoorway

	// Fog fills the region with fog when set — see volume_fog.go. nil is the
	// ordinary case: a volume that is only a trigger. It is a pointer for the
	// reason Level.Fog is, so a volume that never had fog writes no `fog:` key
	// and does not acquire one by being re-saved.
	Fog *VolumeFog

	// Cloud fills the region with volumetric cloud when set — see cloud.go.
	// nil is the ordinary case, and a pointer for the reason Fog is.
	Cloud *VolumeCloud

	// ClearsClouds keeps volumetric cloud out of the region: the march treats
	// every sample inside it as empty air, whichever cloud volume or deck the
	// sample belongs to. It is what lets a dropship sit in a cloud bank with its
	// hold clear — without it the cloud goes wherever the noise says, and the
	// noise has no idea there is a hull in the way. See CloudClearingVolumes.
	//
	// A flag rather than a block, because there is nothing to tune: the region's
	// shape is the whole of the answer. It only reaches what the player SEES —
	// the light a cloud lets through and the shadow it casts are left alone, so
	// the hold is shaded by the bank around it exactly as before.
	ClearsClouds bool

	// Objective makes the region somewhere AI agents are sent to act — see
	// volume_objective.go. nil is the ordinary case.
	Objective *VolumeObjective

	// FromPrefab is runtime-only, never serialized: the prefab instance whose
	// file put this here (prefab.go). Empty for one the level file authored.
	// It is what LevelToRaw checks before writing — a level that saved a copy
	// of a prefab's content would freeze it and stop tracking the file.
	FromPrefab string

	// FromEntity is runtime-only, never serialized: the entity whose type hung
	// this volume on it (entity_volume.go). Empty for one the level authored.
	// It plays the part Emitter.FromEntity plays: LevelToRaw skips these, and
	// DropEntityVolumes sweeps them when the entity goes.
	FromEntity string

	// Geometry is the editor's wireframe cage. Runtime-only, never serialized —
	// the cage is an authoring aid and is not drawn during play, whatever else
	// the volume does.
	Geometry gfx.Geometry

	// Waypoints is the waypoint animation this volume is travelling, or nil.
	// Runtime-only, never serialized; see waypoint_follow.go.
	Waypoints *WaypointPlayback
}

// VolumeShape is the region's shape. The values line up with BoundingType so the
// two can be converted without a lookup, which is what lets ShapeContainsPoint
// serve both.
type VolumeShape int

const (
	VolumeBox VolumeShape = iota
	VolumeSphere
	VolumeCylinder
)

// VolumeShapeNames is the YAML/UI name of each shape, indexed by the shape
// itself. Adding one means adding its name here and nowhere else — the same
// arrangement BoundingTypeNames uses, and for the same reason: a per-file switch
// is what lets an unknown name quietly load as the wrong shape.
var VolumeShapeNames = []string{
	"box",
	"sphere",
	"cylinder",
}

// VolumeShapeName returns a shape's YAML/UI name, falling back to the default
// for anything out of range.
func VolumeShapeName(k VolumeShape) string {
	if int(k) < 0 || int(k) >= len(VolumeShapeNames) {
		return VolumeShapeNames[VolumeBox]
	}
	return VolumeShapeNames[k]
}

// VolumeShapeFromName is the inverse. An unknown name loads as a box rather than
// failing, so a level written by a newer build still opens.
func VolumeShapeFromName(name string) VolumeShape {
	for i, n := range VolumeShapeNames {
		if n == name {
			return VolumeShape(i)
		}
	}
	return VolumeBox
}

// boundingKind maps a volume's shape onto the entity bounding kind describing
// the same solid, so ShapeContainsPoint can be shared.
func (v *Volume) BoundingKind() BoundingType {
	switch v.Shape {
	case VolumeSphere:
		return BoundingSphere
	case VolumeCylinder:
		return BoundingCylinder
	default:
		return BoundingAABB
	}
}

// HalfExtents returns the volume's half-extents as a vector.
func (v *Volume) HalfExtents() mgl32.Vec3 {
	return mgl32.Vec3{v.HalfX, v.HalfY, v.HalfZ}
}

// HasBounds reports whether the volume describes a usable region. All three
// extents must be positive: a zero on any axis is an unauthored volume rather
// than a flat one.
func (v *Volume) HasBounds() bool {
	return v.HalfX > 0 && v.HalfY > 0 && v.HalfZ > 0
}

// ContainsPoint reports whether p is inside the volume. A disabled volume
// contains nothing, which is the whole point of the flag — a script can turn a
// trigger off without the engine having to remember it used to be somewhere.
func (v *Volume) ContainsPoint(p mgl32.Vec3) bool {
	if !v.Enabled || !v.HasBounds() {
		return false
	}
	return ShapeContainsPoint(p, v.Position, v.Rotation, v.HalfExtents(), v.BoundingKind())
}

// WorldAABB returns the volume's world-space bounds, for culling the editor's
// wireframe and for broad-phase overlap tests. ok is false when no extents are
// authored — an unbounded volume is not a zero-sized one.
//
// A rotated or attached volume uses the extents' length as a uniform radius so
// the box stays valid at any orientation, matching Entity.WorldAABB. An attached
// one also takes its carrier's largest scale, since an entity scaled up carries
// a region scaled with it.
func (v *Volume) WorldAABB(lvl *Level) (min, max mgl32.Vec3, ok bool) {
	if !v.HasBounds() {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	h := v.HalfExtents()
	if v.Attach.IsZero() {
		if v.Rotation != (mgl32.Vec3{}) {
			r := h.Len()
			h = mgl32.Vec3{r, r, r}
		}
		return v.Position.Sub(h), v.Position.Add(h), true
	}
	m := v.Attach.Matrix(lvl)
	c := mulPoint(m, v.Position)
	s := max3(m.Col(0).Vec3().Len(), m.Col(1).Vec3().Len(), m.Col(2).Vec3().Len())
	r := h.Len() * s
	h = mgl32.Vec3{r, r, r}
	return c.Sub(h), c.Add(h), true
}

func max3(a, b, c float32) float32 {
	if b > a {
		a = b
	}
	if c > a {
		a = c
	}
	return a
}

// ---------- what the volume rides ----------

// Frame is the vehicle frame this volume's region belongs to, or "" for the
// world. It is the frame half of Attach, and only while nothing more specific is
// named: Attach.Matrix resolves an entity before a frame, so a volume that names
// both rides the entity, and belongs to no frame's chunks.
//
// This is the question a doorway and a scatter ask — "whose decks is this
// region part of" — which riding a walking entity has no answer to.
func (v *Volume) Frame() string {
	if v.Attach.Entity != "" {
		return ""
	}
	return v.Attach.Frame
}

// WorldMatrix is the volume's full placement: its carrier's transform, then its
// own. What the cage is drawn with and what a ray is tested against.
func (v *Volume) WorldMatrix(lvl *Level) mgl32.Mat4 {
	if v.Attach.IsZero() {
		return v.ModelMatrix()
	}
	return v.Attach.Matrix(lvl).Mul4(v.ModelMatrix())
}

// WorldPosition resolves the volume's centre through its attachment.
func (v *Volume) WorldPosition(lvl *Level) mgl32.Vec3 {
	if v.Attach.IsZero() {
		return v.Position
	}
	return mulPoint(v.Attach.Matrix(lvl), v.Position)
}

// LocalPoint carries a world-space point into the space Position is authored
// in — the carrier's — which is the space ContainsPoint and the shape tests
// expect. The identity for a world volume.
func (v *Volume) LocalPoint(lvl *Level, p mgl32.Vec3) mgl32.Vec3 {
	switch {
	case v.Attach.IsZero() || lvl == nil:
		// Nothing to resolve a carrier against reads as world space, the way
		// Attach.Matrix reads it.
		return p
	case v.Attach.Entity == "":
		// A frame keeps its inverse, so the common attached case — a trigger
		// in a ship's hold — inverts nothing per test.
		if f := lvl.Frames.Frame(v.Attach.Frame); f != nil {
			return f.FromWorld(p)
		}
		return p
	}
	return mulPoint(v.Attach.Matrix(lvl).Inv(), p)
}

// SetWorldPosition places the volume's centre at a world-space point, written
// back into its carrier's space.
func (v *Volume) SetWorldPosition(lvl *Level, p mgl32.Vec3) {
	v.Position = v.LocalPoint(lvl, p)
}

// ---------- level lookup ----------

// IsLevelTrigger reports whether the volume is an exit: an armed, bounded
// region that names a level to load. All three matter — a disarmed or
// unauthored trigger contains nothing, and one with no target is an ordinary
// scripted volume.
func (v *Volume) IsLevelTrigger() bool {
	return v.LoadLevel != "" && v.Enabled && v.HasBounds()
}

// ContainsWorldPoint is ContainsPoint for a caller holding a world-space point,
// carrying it into the volume's own frame first.
//
// The point is carried in rather than the volume carried out, for the reason
// the entity trigger test gives: a rotated region stays the shape it was
// authored as, where transforming its extents would not.
func (v *Volume) ContainsWorldPoint(lvl *Level, p mgl32.Vec3) bool {
	return v.ContainsPoint(v.LocalPoint(lvl, p))
}

// FindVolume returns the level volume with the given ID, or nil. This is what a
// script's `@id` resolves through for volumes.
func FindVolume(lvl *Level, id string) *Volume {
	if lvl == nil || id == "" {
		return nil
	}
	for i := range lvl.Volumes {
		if lvl.Volumes[i].ID == id {
			return &lvl.Volumes[i]
		}
	}
	return nil
}

// FindVolumesByTag returns every level volume carrying tag, in level order.
func FindVolumesByTag(lvl *Level, tag string) []*Volume {
	if lvl == nil || tag == "" {
		return nil
	}
	var out []*Volume
	for i := range lvl.Volumes {
		if lvl.Volumes[i].HasTag(tag) {
			out = append(out, &lvl.Volumes[i])
		}
	}
	return out
}

// VolumesContaining returns every enabled volume whose region holds the
// world-space point p, in level order. This is the query behind `interact`.
func VolumesContaining(lvl *Level, p mgl32.Vec3) []*Volume {
	if lvl == nil {
		return nil
	}
	var out []*Volume
	for i := range lvl.Volumes {
		if lvl.Volumes[i].ContainsWorldPoint(lvl, p) {
			out = append(out, &lvl.Volumes[i])
		}
	}
	return out
}

// FrameDoorways returns every enabled doorway volume belonging to the named
// frame, in level order. "" is the world's own doorways, of which there are
// normally none — a doorway is authored on the moving side.
func FrameDoorways(lvl *Level, frameID string) []*Volume {
	if lvl == nil {
		return nil
	}
	var out []*Volume
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		if v.Doorway != nil && v.Enabled && v.HasBounds() && v.Frame() == frameID {
			out = append(out, v)
		}
	}
	return out
}

// UniqueVolumeID returns base, or base with a numeric suffix that no volume in
// lvl is already using — the same guarantee UniqueEntityID gives, and for the
// same reason: two volumes sharing an ID would make `@id` ambiguous.
func UniqueVolumeID(lvl *Level, base string) string {
	if base == "" {
		base = "volume"
	}
	return uniqueLevelID(lvl, base)
}

// NewVolume returns a volume with the defaults a freshly placed one should have:
// a one-unit box, armed, sitting where it was put.
func NewVolume(id string, pos mgl32.Vec3) Volume {
	return Volume{
		ID:       id,
		Position: pos,
		Shape:    VolumeBox,
		HalfX:    1,
		HalfY:    1,
		HalfZ:    1,
		Enabled:  true,
	}
}

// ---------- serialization ----------

// VolumeFromRaw converts the YAML form. An unknown shape name loads as a box and
// a missing `enabled` arms the volume, so the smallest useful entry in a level
// file is an ID, a position and a size.
func VolumeFromRaw(r RawVolume) (Volume, error) {
	data, err := ScriptDataFromRaw(r.RawScriptData, "volume "+r.ID)
	if err != nil {
		return Volume{}, err
	}
	// `frame:` is how a volume said what it rode before it took the whole
	// Attach binding, and a level written then still says it that way.
	at := AttachFromRaw(r.Attach)
	if at.Frame == "" {
		at.Frame = r.Frame
	}
	return Volume{
		ID:           r.ID,
		ScriptData:   data,
		Attach:       at,
		Position:     mgl32.Vec3{r.Position.X, r.Position.Y, r.Position.Z},
		Rotation:     mgl32.Vec3{r.Rotation.X, r.Rotation.Y, r.Rotation.Z},
		Shape:        VolumeShapeFromName(r.Shape),
		HalfX:        r.Half.X,
		HalfY:        r.Half.Y,
		HalfZ:        r.Half.Z,
		Enabled:      BoolDefault(r.Enabled, true),
		LoadLevel:    r.LoadLevel,
		Fog:          VolumeFogFromRaw(r.Fog),
		Cloud:        VolumeCloudFromRaw(r.Cloud),
		ClearsClouds: r.ClearsClouds,
		Doorway:      FrameDoorwayFromRaw(r.Doorway),
		Scatter:      VolumeScatterFromRaw(r.Scatter),
		Objective:    VolumeObjectiveFromRaw(r.Objective),
	}, nil
}

// VolumeToRaw is the inverse.
func VolumeToRaw(v *Volume) RawVolume {
	return RawVolume{
		ID:            v.ID,
		RawScriptData: ScriptDataToRaw(v.ScriptData),
		Shape:         VolumeShapeName(v.Shape),
		Attach:        AttachToRaw(v.Attach),
		Position:      RawVec3{X: v.Position.X(), Y: v.Position.Y(), Z: v.Position.Z()},
		Rotation:      RawVec3{X: v.Rotation.X(), Y: v.Rotation.Y(), Z: v.Rotation.Z()},
		Half:          RawVec3{X: v.HalfX, Y: v.HalfY, Z: v.HalfZ},
		Enabled:       BoolPtrIfFalse(v.Enabled),
		LoadLevel:     v.LoadLevel,
		Fog:           VolumeFogToRaw(v.Fog),
		Cloud:         VolumeCloudToRaw(v.Cloud),
		ClearsClouds:  v.ClearsClouds,
		Doorway:       FrameDoorwayToRaw(v.Doorway),
		Scatter:       VolumeScatterToRaw(v.Scatter),
		Objective:     VolumeObjectiveToRaw(v.Objective),
	}
}

// VolumeMarkerTexture is what the cage is drawn with. It is an untextured line
// list, so this only has to be a texture that exists — the same reasoning as
// spawnMarkerTexture.
const VolumeMarkerTexture = "default"

// VolumeCageSegments is how many segments approximate a circle on a sphere or
// cylinder volume. Enough to read as round at the sizes a trigger is authored
// at, few enough that a level full of them is still cheap.
const VolumeCageSegments = 24

// ---------- the editor's wireframe cage ----------
//
// A volume is a region, not a thing: during play it is invisible, and the only
// evidence it exists is that a script fired. That makes it exactly the kind of
// object an author cannot place accurately without seeing it, so the editor
// draws each one as a wireframe cage.
//
// The mesh is built here, beside the type, because it is a method on it; the
// pass that draws it stays in volume_render.go.

// ModelMatrix builds the volume's transform. Rotation composes Y, then X, then Z
// — the order everything placed in this engine agrees on.
func (v *Volume) ModelMatrix() mgl32.Mat4 {
	T := mgl32.Translate3D(v.Position.X(), v.Position.Y(), v.Position.Z())
	return T.Mul4(EulerMatrixYXZ(v.Rotation))
}

// Load uploads the volume's wireframe cage. Safe to call repeatedly — the
// previous upload is released first, so it doubles as "rebuild after an edit".
//

// The mesh is built in the volume's own space and positioned by ModelMatrix, so
// moving or rotating a volume needs no rebuild; only a change of shape or size
// does.
func (v *Volume) Load() error {
	v.Geometry.Delete()
	if !v.HasBounds() {
		return nil
	}
	if err := gfx.EnsureTextureLoaded(VolumeMarkerTexture); err != nil {
		return err
	}
	verts, idx := v.CageMesh()
	return v.Geometry.Load(verts, idx, VolumeMarkerTexture, mgl32.Vec3{})
}

// CageMesh returns the line-list vertices and indices for the volume's shape, in
// volume-local space.
func (v *Volume) CageMesh() ([][]float32, []int) {
	switch v.Shape {
	case VolumeSphere:
		return EllipsoidCage(v.HalfX, v.HalfY, v.HalfZ)
	case VolumeCylinder:
		return CylinderCage(v.HalfX, v.HalfY, v.HalfZ)
	default:
		return BoxCage(v.HalfX, v.HalfY, v.HalfZ)
	}
}

// BoxCage builds the twelve edges of a box.
func BoxCage(hx, hy, hz float32) ([][]float32, []int) {
	verts := make([][]float32, 0, 8)
	for i := 0; i < 8; i++ {
		verts = append(verts, []float32{
			CornerSign(i, 0) * hx,
			CornerSign(i, 1) * hy,
			CornerSign(i, 2) * hz,
			0, 0,
		})
	}
	// Corner i's bits are (x, y, z) signs, so two corners share an edge exactly
	// when their indices differ in one bit.
	var idx []int
	for i := 0; i < 8; i++ {
		for bit := 0; bit < 3; bit++ {
			j := i | (1 << bit)
			if j != i {
				idx = append(idx, i, j)
			}
		}
	}
	return verts, idx
}

// EllipsoidCage builds three orthogonal rings, which is what reads as a sphere
// in a wireframe without drawing a full lat-long grid.
func EllipsoidCage(hx, hy, hz float32) ([][]float32, []int) {
	var verts [][]float32
	var idx []int
	ring := func(ax, ay, az, bx, by, bz float32) {
		base := len(verts)
		for i := 0; i < VolumeCageSegments; i++ {
			c, s := geom.CirclePoint(i, VolumeCageSegments)
			verts = append(verts, []float32{
				ax*c + bx*s,
				ay*c + by*s,
				az*c + bz*s,
				0, 0,
			})
			idx = append(idx, base+i, base+(i+1)%VolumeCageSegments)
		}
	}
	ring(hx, 0, 0, 0, hy, 0) // XY
	ring(hx, 0, 0, 0, 0, hz) // XZ
	ring(0, hy, 0, 0, 0, hz) // YZ
	return verts, idx
}

// CylinderCage builds the two caps and four uprights joining them.
func CylinderCage(hx, hy, hz float32) ([][]float32, []int) {
	var verts [][]float32
	var idx []int
	capBase := make([]int, 2)
	for c, y := range []float32{-hy, hy} {
		capBase[c] = len(verts)
		for i := 0; i < VolumeCageSegments; i++ {
			cs, sn := geom.CirclePoint(i, VolumeCageSegments)
			verts = append(verts, []float32{cs * hx, y, sn * hz, 0, 0})
			idx = append(idx, capBase[c]+i, capBase[c]+(i+1)%VolumeCageSegments)
		}
	}
	// Four uprights, at the quarter points, so the cage reads as a solid without
	// a wall of vertical lines.
	for q := 0; q < 4; q++ {
		i := q * VolumeCageSegments / 4
		idx = append(idx, capBase[0]+i, capBase[1]+i)
	}
	return verts, idx
}
