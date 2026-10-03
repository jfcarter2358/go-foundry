package world

// scenery.go — Scenery, a model standing in the world and nothing more.
//
// The level already has a way to place a mesh: an entity. But an entity is a
// transform *plus* a bag of everything a dynamic object might ever need — an
// optional definition, several named model attachments each with its own
// animation set, velocity and angular velocity, tags, typed attributes, script
// parameters, a script, four collision flags and a bounding volume the author
// has to dial in by hand. A fence post needs none of that, and a level wants a
// thousand fence posts.
//
// So scenery is the smaller type, on the same reasoning that gave sprites and
// volumes theirs: one model, a transform, and whether you can walk through it.
// There is no ScriptData on it and no `@id` addressing, because the moment a
// prop needs behaviour it is not a prop any more — it is an entity, and the
// editor can place one of those instead.
//
// Bounds are read off the model rather than authored. A scenery's collision
// volume is the model document's own AABB carried through the placement's
// rotation and scale, which is what makes "place a copy and move on" the whole
// of the workflow — see the brush in internal/editor/editor_scenery.go.
//
// Scenery is level-scoped rather than chunk-scoped for the reason volumes are:
// it is placed in world space and a chunk boundary is not something an author
// aims at when scattering rocks.

import (
	"fmt"
	"foundry/internal/fileutil"
	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/meshcollide"
	"foundry/internal/nanite"
	"log"

	"github.com/go-gl/mathgl/mgl32"
)

// Scenery is a placed, static model.
type Scenery struct {
	// ID names the placement in the Explorer and in editor status messages. It
	// is unique within a level like every other object's, but nothing resolves
	// a script reference through it — see the file comment.
	ID string
	// Model is the mesh file, stored relative to resources/ (see
	// fileutil.RelAssetPath). Both .fmdl and .obj are accepted, exactly as an
	// entity attachment's path is — and a cooked Nanite mesh (.fnm, see
	// internal/nanite), which draws through the GPU-driven cluster cull.
	Model string

	// Materials overrides a cooked mesh's material slots, slot name to
	// texture: the same rock in moss or in snow without a second cook. A slot
	// it does not name keeps the cooked texture. Ignored for any other model.
	Materials map[string]string

	Position mgl32.Vec3
	Rotation mgl32.Vec3 // Euler angles (X, Y, Z) in radians
	Scale    mgl32.Vec3 // per-axis; zero value treated as {1,1,1}

	// Solid is whether the player and entities are stopped by this. On by
	// default: a rock you walk through is the exception, and scenery exists
	// mostly to be walked around.
	Solid bool

	// Collision overrides how a cooked mesh collides (docs/nanite/PHASE_2b.md):
	// auto — the mesh's own choice, hulls for a placement under its auto size
	// and mesh above it — box, hulls, mesh or none. A model that is not
	// cooked has no hulls or mesh to offer and always collides as its box.
	Collision meshcollide.Mode

	// ---------- runtime, never authored and never saved ----------

	// Submeshes is the uploaded mesh, in model space — the draw carries
	// ModelMatrix, exactly as an entity attachment's does.
	Submeshes []gfx.Geometry
	// LocalMin and LocalMax are the model document's own bounds, kept because
	// they are the whole of this thing's collision volume and re-reading the
	// file to find them is not an option. Equal when nothing is loaded.
	LocalMin, LocalMax mgl32.Vec3

	// Nanite is the cooked mesh, when Model is one (shared with every other
	// placement of it: never modified). NaniteGPU says the GPU-driven cull
	// draws it (render/nanite_scenery.go): its Submeshes are then CPU-only —
	// for picking, raycasts and the like — and every classic draw path skips
	// it. Off, the Submeshes are uploaded and drawn as any model's are.
	Nanite    *nanite.Mesh
	NaniteGPU bool
	// NoNanite keeps a cooked mesh on the classic path even when the GPU cull
	// could draw it: for a placement that is not in the level's list, such as
	// the editor's placement ghost, which nothing would otherwise draw.
	NoNanite bool
}

// NaniteMeshes is whether cooked meshes draw through the GPU-driven cull. On
// by default; FOUNDRY_NANITE_MESHES=0 puts them on the classic per-model path
// (their cooked pool uploaded as an ordinary indexed mesh), which is the A/B
// switch. Read when a placement loads.
var NaniteMeshes = gfx.EnvFlag("FOUNDRY_NANITE_MESHES", true)

// NaniteCollision is whether a cooked mesh may collide as its own shape —
// its hulls or its collision cut (docs/nanite/PHASE_2b.md). On by default;
// FOUNDRY_NANITE_COLLISION=0 puts every prop back on its bounding box, as
// before the cook carried collision, for comparing the two.
var NaniteCollision = gfx.EnvFlag("FOUNDRY_NANITE_COLLISION", true)

// NaniteGPUAvailable reports whether the renderer can draw a cooked mesh
// through the GPU cull. Set by internal/render, which owns the answer (it
// builds the compute programs); nil — a headless tool, a test — is no.
var NaniteGPUAvailable func() bool

// NewScenery returns a placement of model at pos, unrotated, unscaled and
// solid — what the brush stamps before the author touches anything.
func NewScenery(id, model string, pos mgl32.Vec3) Scenery {
	return Scenery{
		ID:       id,
		Model:    model,
		Position: pos,
		Scale:    mgl32.Vec3{1, 1, 1},
		Solid:    true,
	}
}

// EffectiveScale treats the zero value as identity, so a scenery that never
// mentioned scale behaves as one scaled {1,1,1} rather than collapsing.
func (s *Scenery) EffectiveScale() mgl32.Vec3 {
	if s.Scale == (mgl32.Vec3{}) {
		return mgl32.Vec3{1, 1, 1}
	}
	return s.Scale
}

// ModelMatrix is the placement's transform. Rotation composes Y, then X, then
// Z, as everything placed in this engine does, so a yaw is a yaw everywhere.
func (s *Scenery) ModelMatrix() mgl32.Mat4 {
	sc := s.EffectiveScale()
	T := mgl32.Translate3D(s.Position.X(), s.Position.Y(), s.Position.Z())
	S := mgl32.Scale3D(sc.X(), sc.Y(), sc.Z())
	return T.Mul4(EulerMatrixYXZ(s.Rotation)).Mul4(S)
}

// ---------- bounds ----------

// HasBounds reports whether the model gave up a usable volume. A mesh that is
// flat on one axis — a decal quad, a painted sign — has none, and treating its
// zero extent as a collision box would make it an infinitely thin wall.
func (s *Scenery) HasBounds() bool {
	d := s.LocalMax.Sub(s.LocalMin)
	return d.X() > 0 && d.Y() > 0 && d.Z() > 0
}

// BoundHalfExtents is the model's own half-size, scaled. Scale is absolute: a
// negative scale mirrors a mesh but cannot give it negative size.
func (s *Scenery) BoundHalfExtents() mgl32.Vec3 {
	sc := s.EffectiveScale()
	h := s.LocalMax.Sub(s.LocalMin).Mul(0.5)
	return mgl32.Vec3{
		h.X() * geom.Abs32(sc.X()),
		h.Y() * geom.Abs32(sc.Y()),
		h.Z() * geom.Abs32(sc.Z()),
	}
}

// BoundCenter is where that volume actually sits in the world.
//
// A model is rarely centred on its own origin — the convention across the
// engine's meshes is an origin at the base, so the box is half a height up —
// and the offset has to travel through the placement's rotation and scale to
// stay on the mesh.
func (s *Scenery) BoundCenter() mgl32.Vec3 {
	c := s.LocalMin.Add(s.LocalMax).Mul(0.5)
	p := s.ModelMatrix().Mul4x1(c.Vec4(1))
	return mgl32.Vec3{p.X(), p.Y(), p.Z()}
}

// WorldAABB is bounds wide enough to cull against at any orientation. ok is
// false when nothing is loaded, in which case the caller must not cull — an
// unloaded scenery is not a zero-sized one.
//
// A rotated placement uses the extents' length as a uniform radius rather than
// shrink-wrapping a box that would clip the moment it turned, the same
// compromise Entity.WorldAABB makes.
func (s *Scenery) WorldAABB() (min, max mgl32.Vec3, ok bool) {
	if !s.HasBounds() {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	h := s.BoundHalfExtents()
	if s.Rotation != (mgl32.Vec3{}) {
		r := h.Len()
		h = mgl32.Vec3{r, r, r}
	}
	c := s.BoundCenter()
	return c.Sub(h), c.Add(h), true
}

// CollisionAABB is the *exact* box the rotated volume spans, found by carrying
// the eight corners through the transform — the cheap reject the push-apart
// runs before it builds a shape at all. Entity.CollisionAABB is the same idea
// for the same reason.
func (s *Scenery) CollisionAABB() (min, max mgl32.Vec3, ok bool) {
	if !s.HasBounds() {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	c := s.BoundCenter()
	h := s.BoundHalfExtents()
	if s.Rotation == (mgl32.Vec3{}) {
		return c.Sub(h), c.Add(h), true
	}
	rot := EulerMatrixYXZ(s.Rotation)
	min = mgl32.Vec3{float32(1e30), float32(1e30), float32(1e30)}
	max = min.Mul(-1)
	for i := 0; i < 8; i++ {
		corner := mgl32.Vec3{
			pickExtent(i&1 != 0, h.X(), -h.X()),
			pickExtent(i&2 != 0, h.Y(), -h.Y()),
			pickExtent(i&4 != 0, h.Z(), -h.Z()),
		}
		r := rot.Mul4x1(corner.Vec4(0))
		p := c.Add(mgl32.Vec3{r.X(), r.Y(), r.Z()})
		for k := 0; k < 3; k++ {
			if p[k] < min[k] {
				min[k] = p[k]
			}
			if p[k] > max[k] {
				max[k] = p[k]
			}
		}
	}
	return min, max, true
}

// pickExtent is a float32 ternary — the corner walk above reads as eight sign
// choices with it and as twenty lines without.
func pickExtent(cond bool, a, b float32) float32 {
	if cond {
		return a
	}
	return b
}

// ---------- collision ----------

// CollisionData is the cooked mesh's collision, or nil for a model that is
// not cooked.
func (s *Scenery) CollisionData() *meshcollide.Data {
	if s.Nanite == nil {
		return nil
	}
	return s.Nanite.Collision
}

// CollisionSize is the placement's largest extent: what auto weighs against
// the mesh's auto size.
func (s *Scenery) CollisionSize() float32 {
	h := s.BoundHalfExtents()
	return 2 * max(h.X(), h.Y(), h.Z())
}

// CollisionMode is how this placement collides, resolved: none when it is
// not solid, box for a model with no cooked collision (or a mode the cook
// cannot serve), and otherwise the override or the mesh's choice by size.
// A box needs a volume; the mesh mode does not, so a cooked sheet — a
// ground patch flat on one axis — collides as itself where its box could
// not.
func (s *Scenery) CollisionMode() meshcollide.Mode {
	if !s.Solid || s.Collision == meshcollide.ModeNone {
		return meshcollide.ModeNone
	}
	m := meshcollide.ModeBox
	if d := s.CollisionData(); d != nil && NaniteCollision {
		m = d.Resolve(s.Collision, s.CollisionSize())
	}
	if m == meshcollide.ModeBox && !s.HasBounds() {
		return meshcollide.ModeNone
	}
	return m
}

// SceneryCollisionWarnings is what is worth fixing about a prop's collision:
// what `foundry check` prints, and the editor's Collision group with it. A
// solid prop that collides as nothing; and, in the hulls mode, a
// decomposition the hull cap cut short, hulls capped to fewer points than
// keep them near the surface, and hulls of a mesh with open edges, which
// fill in the space behind its open side.
func SceneryCollisionWarnings(s *Scenery) []string {
	var out []string
	mode := s.CollisionMode()
	if s.Solid && mode == meshcollide.ModeNone {
		if s.Collision == meshcollide.ModeNone {
			out = append(out, "solid, but its collision mode is none: nothing stops at it")
		} else {
			out = append(out, "solid, but it has no volume to collide with")
		}
	}
	d := s.CollisionData()
	if d == nil || mode != meshcollide.ModeHulls {
		return out
	}
	set := d.Settings.Resolved()
	if d.HullCapHit {
		out = append(out, fmt.Sprintf("the convex decomposition stopped at the hull cap (%d): its hulls bridge hollows up to %.2f m deep (game.yaml nanite.collision_max_hulls, or the mesh mode)",
			set.MaxHulls, d.Concavity))
	}
	if d.VertCapped > 0 && d.VertCapError > set.Concavity {
		out = append(out, fmt.Sprintf("%d hulls were capped to %d points, which moved their surface up to %.2f m (game.yaml nanite.collision_max_hull_verts)",
			d.VertCapped, set.MaxHullVerts, d.VertCapError))
	}
	if d.OpenEdges > 0 {
		out = append(out, fmt.Sprintf("its mesh has %d open edges, and hulls fill in behind an open side: the mesh mode follows the surface", d.OpenEdges))
	}
	return out
}

// ---------- loading ----------

// Load uploads the model and records the bounds collision reads.
//
// Safe to call repeatedly: the previous upload is released first, which is what
// makes it "rebuild after the model path changed in the editor" as well as
// "build after a level load".
//
// A missing texture is logged rather than returned. The mesh is still worth
// drawing untextured, and a level that will not open because one prop lost its
// material is the worse failure — the same rule an entity attachment follows.
func (s *Scenery) Load() error {
	s.Delete()
	if s.Model == "" {
		return nil
	}
	if nanite.IsCookedPath(s.Model) {
		return s.loadNanite()
	}
	doc, err := loadModelDocument(fileutil.ResolveAssetPath(s.Model))
	if err != nil {
		return fmt.Errorf("scenery %q model %q: %w", s.ID, s.Model, err)
	}
	s.LocalMin, s.LocalMax = doc.Bounds()

	for _, sub := range doc.ModelSubmeshes() {
		if len(sub.Indices) == 0 {
			continue
		}
		if err := gfx.EnsureTextureLoaded(sub.Texture); err != nil {
			log.Printf("scenery %s: texture %q: %v", s.ID, sub.Texture, err)
		}
		// Indexed: a model loads as a shared vertex pool and an index list
		// (gfx.IndexedModels; docs/nanite/PHASE_0.md).
		g := gfx.Geometry{Smooth: sub.Smooth, SmoothAngle: doc.SmoothAngle, AutoSmooth: true, Indexed: true}
		// The mesh is model-space and the draw carries ModelMatrix, so the
		// geometry's own origin — the transparent pass's sort key — is zero.
		if err := g.Load(sub.Vertices, sub.Indices, sub.Texture, mgl32.Vec3{}); err != nil {
			return fmt.Errorf("scenery %q model %q: %w", s.ID, s.Model, err)
		}
		s.Submeshes = append(s.Submeshes, g)
	}
	return nil
}

// loadNanite is Load for a cooked mesh: one submesh per material slot over
// the cooked vertex pool, CPU-only when the GPU cull draws it.
func (s *Scenery) loadNanite() error {
	m, err := nanite.Load(fileutil.ResolveAssetPath(s.Model))
	if err != nil {
		return fmt.Errorf("scenery %q model %q: %w", s.ID, s.Model, err)
	}
	s.Nanite = m
	s.LocalMin, s.LocalMax = m.Lo, m.Hi
	s.NaniteGPU = NaniteMeshes && !s.NoNanite && GeometryUploadsOn() && NaniteGPUAvailable != nil && NaniteGPUAvailable()
	if !s.NaniteGPU || len(m.Fallback) == 0 {
		// The classic path draws the full detail, and a mesh with no
		// fallback has nothing smaller for the CPU: decode every page
		// (docs/nanite/PHASE_3.md). The GPU path streams the pages instead
		// and keeps none of them here.
		if err := m.Flat(); err != nil {
			return fmt.Errorf("scenery %q model %q: %w", s.ID, s.Model, err)
		}
	}
	for si := range m.Slots {
		first, count := m.SlotTriangles(si)
		if count == 0 {
			continue
		}
		tex := s.SlotTexture(si)
		if err := gfx.EnsureTextureLoaded(tex); err != nil {
			log.Printf("scenery %s: texture %q: %v", s.ID, tex, err)
		}
		g := gfx.Geometry{Smooth: m.Slots[si].Smooth, AutoSmooth: true, Indexed: true}
		if s.NaniteGPU {
			// The GPU's LOD selection draws it; what the CPU keeps is for
			// picking and the point-shadow signature, and the fallback
			// mesh — the DAG cut at a middle level, over its own small pool
			// — serves those for a fraction of the triangles
			// (docs/nanite/PHASE_2.md). Nothing to upload: the cull's pool
			// draws it.
			verts, normals, idx := m.FallbackVertices, m.FallbackNormals, m.SlotFallback(si)
			if len(idx) == 0 {
				// No fallback triangles for this slot: the full pool,
				// decoded from the pages (a no-op once done).
				if err := m.Flat(); err != nil {
					return fmt.Errorf("scenery %q model %q: %w", s.ID, s.Model, err)
				}
				verts, normals, idx = m.Vertices, m.Normals, m.Indices[3*first:3*(first+count)]
			}
			g.CPUOnly = true
			g.Vertices, g.Normals, g.Indices = verts, normals, idx
			g.Texture, g.Alpha = tex, 1
			g.Model = mgl32.Ident4()
		} else {
			idx := m.Indices[3*first : 3*(first+count)]
			rows := make([][]float32, 0, len(idx))
			normals := make([][]float32, 0, len(idx))
			order := make([]int, len(idx))
			for k, v := range idx {
				rows = append(rows, m.Vertices[5*v:5*v+5])
				normals = append(normals, m.Normals[3*v:3*v+3])
				order[k] = k
			}
			g.VertexNormals = normals
			if err := g.Load(rows, order, tex, mgl32.Vec3{}); err != nil {
				return fmt.Errorf("scenery %q model %q: %w", s.ID, s.Model, err)
			}
			if err := s.loadShadowLOD(&g, m, si, tex); err != nil {
				return err
			}
		}
		s.Submeshes = append(s.Submeshes, g)
	}
	return nil
}

// loadShadowLOD gives a classic-path submesh of a cooked mesh its shadow
// stand-in: slot si of the fallback cut, uploaded beside it (gfx.Geometry.
// ShadowLOD), which the cascade and point-light passes draw instead of the
// full detail.
//
// Why: a placement the GPU cull does not draw (no compute, the switch off, the
// placement opted out) is drawn whole — every triangle of level 0 — and every
// sun cascade and every face of every shadowed point light draws it again. On
// the 244-rock bench that is ~82k triangles a rock, times three cascades, a
// frame (docs/nanite/PHASE_4.md). The fallback is the DAG cut at a middle
// level the cook already stores for exactly this kind of consumer (picking,
// the GI bake): 3,480 triangles for the bench rock. Its error is the cut's,
// FallbackError, a few centimetres on a two-metre rock, which the cascades'
// slope bias already spans; the colour pass keeps the full detail, so what
// the eye sees is unchanged and only the shadow's outline moves by that much.
//
// NaniteClassicShadowLOD=false (FOUNDRY_NANITE_CLASSIC_SHADOW_LOD=0) keeps the
// full detail in the shadows too, as before.
func (s *Scenery) loadShadowLOD(g *gfx.Geometry, m *nanite.Mesh, si int, tex string) error {
	if !NaniteClassicShadowLOD || len(m.Fallback) == 0 {
		return nil
	}
	idx := m.SlotFallback(si)
	if _, count := m.SlotTriangles(si); len(idx) == 0 || len(idx)/3 >= int(count) {
		return nil // nothing smaller to cast with
	}
	rows := make([][]float32, 0, len(idx))
	normals := make([][]float32, 0, len(idx))
	order := make([]int, len(idx))
	for k, v := range idx {
		rows = append(rows, m.FallbackVertices[5*v:5*v+5])
		normals = append(normals, m.FallbackNormals[3*v:3*v+3])
		order[k] = k
	}
	lod := &gfx.Geometry{Smooth: g.Smooth, AutoSmooth: true, Indexed: true, VertexNormals: normals}
	if err := lod.Load(rows, order, tex, mgl32.Vec3{}); err != nil {
		return fmt.Errorf("scenery %q model %q: shadow LOD: %w", s.ID, s.Model, err)
	}
	g.ShadowLOD = lod
	return nil
}

// NaniteClassicShadowLOD is whether a cooked mesh drawn the classic way casts
// its fallback cut rather than its full detail (loadShadowLOD). Read when a
// placement loads.
var NaniteClassicShadowLOD = gfx.EnvFlag("FOUNDRY_NANITE_CLASSIC_SHADOW_LOD", true)

// SlotTexture is the texture slot si of the cooked mesh draws with: the
// placement's override, or the cooked one.
func (s *Scenery) SlotTexture(si int) string {
	if s.Nanite == nil || si < 0 || si >= len(s.Nanite.Slots) {
		return ""
	}
	if t, ok := s.Materials[s.Nanite.Slots[si].Name]; ok {
		return t
	}
	return s.Nanite.Slots[si].Texture
}

// GeometryUploadsOn is gfx.GeometryUploadsEnabled, readable as a function so
// a cooked placement loaded headlessly never asks for the GPU path.
func GeometryUploadsOn() bool { return gfx.GeometryUploadsEnabled }

// Delete releases the GPU buffers. Reloading without it would leak the previous
// upload's VAO/VBO.
func (s *Scenery) Delete() {
	for i := range s.Submeshes {
		s.Submeshes[i].Delete()
	}
	s.Submeshes = nil
	s.LocalMin, s.LocalMax = mgl32.Vec3{}, mgl32.Vec3{}
	s.Nanite, s.NaniteGPU = nil, false
}

// HasGeometry reports whether there is anything uploaded to draw.
func (s *Scenery) HasGeometry() bool {
	for i := range s.Submeshes {
		if len(s.Submeshes[i].Vertices) > 0 {
			return true
		}
	}
	return false
}

// SetAlpha applies a to every submesh. Transparency is a per-Geometry property,
// so it has to reach all of them or half a model would stop blending — which is
// what the editor's placement ghost is built out of.
func (s *Scenery) SetAlpha(a float32) {
	for i := range s.Submeshes {
		s.Submeshes[i].Alpha = a
	}
}

// ---------- level lookup ----------

// FindScenery returns the level's scenery with the given ID, or nil.
func FindScenery(lvl *Level, id string) *Scenery {
	if lvl == nil || id == "" {
		return nil
	}
	for i := range lvl.Scenery {
		if lvl.Scenery[i].ID == id {
			return &lvl.Scenery[i]
		}
	}
	return nil
}

// UniqueSceneryID returns base, or base with a numeric suffix that nothing in
// lvl is already using — the guarantee every other mint site gives.
func UniqueSceneryID(lvl *Level, base string) string {
	if base == "" {
		base = "scenery"
	}
	return uniqueLevelID(lvl, base)
}

// DeleteScenery releases every placement's GPU buffers, so replacing a level
// does not leak the outgoing one's props.
func DeleteScenery(lvl *Level) {
	if lvl == nil {
		return
	}
	for i := range lvl.Scenery {
		lvl.Scenery[i].Delete()
	}
}

// ---------- serialization ----------

// SceneryFromRaw converts the YAML form. The smallest useful entry in a level
// file is a model and a position.
func SceneryFromRaw(r RawScenery) Scenery {
	return Scenery{
		ID:        r.ID,
		Model:     r.Model,
		Position:  mgl32.Vec3{r.Position.X, r.Position.Y, r.Position.Z},
		Rotation:  mgl32.Vec3{r.Rotation.X, r.Rotation.Y, r.Rotation.Z},
		Scale:     vec3FromRawPtr(r.Scale, mgl32.Vec3{1, 1, 1}),
		Solid:     BoolDefault(r.Solid, true),
		Materials: copyStringMap(r.Materials),
		Collision: collisionModeFromRaw(r.ID, r.Collision),
	}
}

// collisionModeFromRaw reads a placement's collision: key. A value that is
// not a mode is logged and read as auto — a level should not refuse to open
// over a typo in one prop.
func collisionModeFromRaw(id, v string) meshcollide.Mode {
	m, ok := meshcollide.ParseMode(v)
	if !ok {
		log.Printf("scenery %s: collision %q is not one of %v; using auto", id, v, meshcollide.ModeNames())
	}
	return m
}

func copyStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// SceneryToRaw is the inverse. Defaults are written as absent, so a prop that
// was dropped and left alone stays a three-line entry however many thousand of
// them a level holds.
func SceneryToRaw(s *Scenery) RawScenery {
	r := RawScenery{
		ID:       s.ID,
		Model:    s.Model,
		Position: RawVec3{X: s.Position.X(), Y: s.Position.Y(), Z: s.Position.Z()},
		Rotation: RawVec3{X: s.Rotation.X(), Y: s.Rotation.Y(), Z: s.Rotation.Z()},
		Solid:    BoolPtrIfFalse(s.Solid),

		Materials: copyStringMap(s.Materials),
	}
	if s.Collision != meshcollide.ModeAuto {
		r.Collision = s.Collision.String()
	}
	if sc := s.EffectiveScale(); sc != (mgl32.Vec3{1, 1, 1}) {
		r.Scale = &RawVec3{X: sc.X(), Y: sc.Y(), Z: sc.Z()}
	}
	return r
}
