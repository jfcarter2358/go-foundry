package editor

// editor_entity_editor_emitters.go — the effects a type carries, in the entity
// editor.
//
// The document holds descriptions (world/entity_emitter.go): a name, a .fpar to
// run, where on the entity it hangs, and four knobs. What this file adds is the
// half an author actually judges the work by — the effect *running*, at the
// right place on the right model, against the same origin and the same scale as
// the brushes and meshes beside it.
//
// # The preview is real emitters
//
// entityPreviewEmitters is one world.Emitter per attachment, built through the
// same world.EntityEmitterInstance a level materialises with. That is the rule
// the mesh preview already follows and it buys the same thing: the window
// cannot disagree with the game about what a definition means, because it is
// not a second implementation of it. An effect that looks right here looks right
// in the level.
//
// They are stepped from this window's own frame rather than by EmitterUpdate,
// because this document is not in any level — the particle editor's arrangement,
// for the same reason.
//
// # GL ownership
//
// The particles are drawn through render.DrawSpriteInstances, the engine's own
// instanced pass, whose buffers and programs are shared with the main window.
// Its VAO is not — no VAO ever is — so this window binds one of its own through
// render.SpriteVAOOverride, exactly as the particle editor does. See the header
// of editor_entity_editor_render.go.

import (
	"foundry/internal/gfx"
	"foundry/internal/gizmo"
	"foundry/internal/levelio"
	"foundry/internal/particle"
	"foundry/internal/render"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// entityPreviewEmitters is the live simulation, one per attachment and parallel
// to entityDef.Emitters. Runtime-only, like every other preview: it is rebuilt
// from the document rather than snapshotted with it.
var entityPreviewEmitters []world.Emitter

// entityEmitterFill is the staging buffer a frame of particles is filled into,
// reused so drawing them allocates nothing. Its own rather than the particle
// editor's, so neither window's buffer grows to the other's worst case.
var entityEmitterFill []particle.Instance

var (
	entityEmitterMarkerColor = mgl32.Vec3{0.4, 0.8, 1}
	entityEmitterSelColor    = mgl32.Vec3{1, 0.85, 0.3}
)

// entityEmitterMarkerSize is the half-length of the cross drawn at an
// attachment's origin, in world units. Small: a marker is there to be found and
// clicked, not to be the thing you look at.
const entityEmitterMarkerSize = float32(0.12)

// ---------- the preview simulation ----------

// entityEmitterPreviewSync brings the live emitters into line with the
// document. Called once a frame, before the tick.
//
// Two levels of response, for the reason the mesh preview has two: adding or
// removing an attachment, or pointing one at a different .fpar, rebuilds — it
// is a different simulation. Turning a knob does not: RateScale, Tint and
// Emitting are pushed into the running systems through SyncSystems, so an
// author sees a change reach the particles already in flight rather than only
// the next ones. Offset and Rotation need nothing at all, since the origin is
// resolved from the document every frame.
func entityEmitterPreviewSync() {
	if entityDef == nil {
		entityPreviewEmitters = nil
		return
	}
	if len(entityPreviewEmitters) != len(entityDef.Emitters) {
		entityEmitterPreviewRebuild()
		return
	}
	for i := range entityDef.Emitters {
		m := &entityDef.Emitters[i]
		e := &entityPreviewEmitters[i]
		if e.Effect != m.Effect {
			entityEmitterPreviewLoad(i)
			continue
		}
		e.Enabled = m.Enabled
		scale, tint := m.EffectiveRateScale(), m.EffectiveTint()
		if e.RateScale == scale && e.Tint == tint && e.Emitting == m.Emitting {
			continue
		}
		e.RateScale, e.Tint, e.Emitting = scale, tint, m.Emitting
		e.SyncSystems()
	}
}

// entityEmitterPreviewRebuild starts every simulation over from the document.
func entityEmitterPreviewRebuild() {
	entityPreviewEmitters = nil
	if entityDef == nil {
		return
	}
	entityPreviewEmitters = make([]world.Emitter, len(entityDef.Emitters))
	for i := range entityDef.Emitters {
		entityEmitterPreviewLoad(i)
	}
}

// entityEmitterPreviewLoad reads one attachment's document and starts it.
//
// The emitter is built with an empty entity ID, which is what makes it
// unattached: this window IS the entity's local space, so the transform a level
// would apply is the identity here and the offset is where the thing goes. What
// the bone does is resolved per frame instead — see entityEmitterOrigin.
func entityEmitterPreviewLoad(i int) {
	if entityDef == nil || i < 0 || i >= len(entityDef.Emitters) ||
		i >= len(entityPreviewEmitters) {
		return
	}
	m := &entityDef.Emitters[i]
	e := world.EntityEmitterInstance("", m)
	if err := e.Load(); err != nil {
		entityStatus = "Emitter " + m.Name + ": " + err.Error()
	}
	if e.Doc != nil {
		// The textures the effect reaches for, uploaded in THIS window's
		// context. Textures are shared between contexts in the group (unlike a
		// VAO), so an upload here is one the main window can draw with too.
		tex := map[string]bool{}
		for li := range e.Doc.Layers {
			tex[e.Doc.Layers[li].Texture] = true
		}
		if err := levelio.EnsureChunkTextures(tex); err != nil {
			entityStatus = "Emitter " + m.Name + ": " + err.Error()
		}
	}
	entityPreviewEmitters[i] = e
}

// entityEmitterPreviewRelease drops every live simulation. Called from
// entityPreviewRelease, though nothing here holds GL state — an emitter's
// particles are plain memory, and the buffers behind the pass belong to the
// main window.
func entityEmitterPreviewRelease() { entityPreviewEmitters = nil }

// entityEmitterPreviewTick advances every running attachment.
//
// Gated on the same Play toggle the animations are: a bone-mounted flame is
// only in the right place while the pose it rides is being advanced, and an
// author who has paused the type to look at a rest pose has not asked for the
// smoke to go on pouring out of it.
func entityEmitterPreviewTick(dt float32) {
	if dt <= 0 {
		return
	}
	for i := range entityPreviewEmitters {
		e := &entityPreviewEmitters[i]
		if !e.Enabled || !e.Loaded() {
			continue
		}
		origin := entityEmitterOrigin(i)
		for _, s := range e.Systems {
			s.Step(dt, origin)
		}
	}
}

// entityEmitterOrigin is the transform one attachment emits through, in the
// entity's own space: the bone it names, then its own offset and rotation.
//
// world.EntityBoneMatrix is the same resolution a level's Attach performs, minus
// the entity's own placement — which is exactly right here, because this window
// is that local space.
func entityEmitterOrigin(i int) mgl32.Mat4 {
	if entityDef == nil || i < 0 || i >= len(entityDef.Emitters) {
		return mgl32.Ident4()
	}
	m := &entityDef.Emitters[i]
	local := m.LocalMatrix()
	if bm, ok := world.EntityBoneMatrix(entityPreview, m.Model, m.Bone); ok {
		return bm.Mul4(local)
	}
	return local
}

// ---------- drawing ----------

// entityPreviewVolumes are this window's volumetric render targets, released
// with the window (onEntityEditorClose) in the context they were made in.
// entityPreviewVolumeLayers is the per-frame list of volume layers handed to
// them.
var (
	entityPreviewVolumes      render.VolumeTargets
	entityPreviewVolumeLayers []*particle.System
)

// entityDrawEmitterParticles puts the live particles on screen through the
// engine's own instanced pass.
//
// Layers are drawn in document order rather than sorted against each other, the
// rule the particle editor's preview settled on: order is the author's, and it
// is the only one that reliably keeps a smoke plume behind the flame it rises
// from.
func entityDrawEmitterParticles(projView mgl32.Mat4) {
	if entityEditorWindow == nil || len(entityPreviewEmitters) == 0 {
		return
	}
	camPos := entityCam.position()

	// This window's own vertex array, for the duration of this frame.
	render.SpriteVAOOverride = entityEditorWindow.SpriteVAO
	defer func() { render.SpriteVAOOverride = 0 }()

	entityPreviewVolumeLayers = entityPreviewVolumeLayers[:0]
	defer func() {
		render.DrawVolumeParticlePreview(&entityPreviewVolumes, entityPreviewVolumeLayers, projView, camPos)
	}()

	for i := range entityPreviewEmitters {
		e := &entityPreviewEmitters[i]
		if !e.Enabled || !e.Loaded() {
			continue
		}
		for _, s := range e.Systems {
			n := s.Count()
			if n == 0 || s.Layer.Muted {
				continue
			}
			if s.Layer.Render == particle.RenderVolume {
				entityPreviewVolumeLayers = append(entityPreviewVolumeLayers, s)
				continue
			}
			if cap(entityEmitterFill) < n {
				entityEmitterFill = make([]particle.Instance, n)
			}
			buf := entityEmitterFill[:n]
			sortIt := s.Layer.Blend == gfx.SpriteBlendAlpha
			written := s.Fill(buf, camPos, sortIt)
			if written == 0 {
				continue
			}
			render.DrawSpriteInstances(buf[:written], s.Layer.Texture, s.Layer.Blend,
				s.Layer.Billboard, projView, camPos)
		}
	}
}

// drawEntityEmitterMarkers puts a cross at every attachment's origin, with a
// stalk along the axis its emission shape points down.
//
// Without one an attachment that has no effect chosen yet, or one whose .fpar
// will not load, would be invisible — and so would the aim of a cone, which is
// the single thing about an emitter that has to be set by eye. The stalk is
// local +Y because that is the axis a cone opens along.
func drawEntityEmitterMarkers() {
	if entityDef == nil {
		return
	}
	for i := range entityDef.Emitters {
		col := entityEmitterMarkerColor
		if entitySel.Kind == entitySelEmitter && entitySel.Idx == i {
			col = entityEmitterSelColor
		}
		m := entityEmitterOrigin(i)
		at := func(v mgl32.Vec3) mgl32.Vec3 {
			p := m.Mul4x1(mgl32.Vec4{v.X(), v.Y(), v.Z(), 1})
			return mgl32.Vec3{p.X(), p.Y(), p.Z()}
		}
		const s = entityEmitterMarkerSize
		gizmo.GizmoLine(at(mgl32.Vec3{-s, 0, 0}), at(mgl32.Vec3{s, 0, 0}), col)
		gizmo.GizmoLine(at(mgl32.Vec3{0, -s, 0}), at(mgl32.Vec3{0, s, 0}), col)
		gizmo.GizmoLine(at(mgl32.Vec3{0, 0, -s}), at(mgl32.Vec3{0, 0, s}), col)
		gizmo.GizmoLine(at(mgl32.Vec3{}), at(mgl32.Vec3{0, s * 3, 0}), col)
	}
}

// ---------- structural edits ----------

// entityAddEmitter hangs a new effect on the type, with no document chosen yet
// — the same "add one, then pick" order a model attachment follows.
func entityAddEmitter() {
	if entityDef == nil {
		return
	}
	EntityDefPushUndo()
	entityDef.Emitters = append(entityDef.Emitters,
		world.NewEntityEmitter(world.UniqueEntityEmitterName(entityDef.Emitters, defaultEmitterName)))
	entitySel = entityEditorSelection{Kind: entitySelEmitter, Idx: len(entityDef.Emitters) - 1}
	entityMarkDirty()
	entityPreviewInvalidate()
}

// defaultEmitterName is what a new attachment is called before it has an effect
// to take a name from.
const defaultEmitterName = "emitter"

// entityRemoveEmitter drops one attachment.
func entityRemoveEmitter(idx int) {
	if entityDef == nil || idx < 0 || idx >= len(entityDef.Emitters) {
		return
	}
	EntityDefPushUndo()
	entityDef.Emitters = append(entityDef.Emitters[:idx:idx], entityDef.Emitters[idx+1:]...)
	entitySel = entityEditorSelection{}
	entityMarkDirty()
	entityPreviewInvalidate()
}

// entityEmitterModelNames lists the type's model attachments, for the socket
// picker. Taken from the document rather than the preview: an attachment whose
// mesh will not load is still one a bone can be named on, and the author has to
// be able to say so before fixing the path.
func entityEmitterModelNames() []string {
	if entityDef == nil {
		return nil
	}
	out := make([]string, 0, len(entityDef.Models))
	for i := range entityDef.Models {
		out = append(out, entityDef.Models[i].Name)
	}
	return out
}

// entityEmitterBoneNames lists the bones of the named attachment. The bone list
// comes from the PREVIEW, which is the only place the .fmdl has actually been
// read — the definition holds a path, not a document. Same seam the animation
// clip list uses.
func entityEmitterBoneNames(model string) []string {
	if entityPreview == nil || model == "" {
		return nil
	}
	m := entityPreview.Model(model)
	if m == nil || m.Doc == nil {
		return nil
	}
	out := make([]string, 0, len(m.Doc.Bones))
	for i := range m.Doc.Bones {
		out = append(out, m.Doc.Bones[i].Name)
	}
	return out
}

// entityEmitterLiveCount is how many particles one attachment has in flight —
// the panel's "is this thing running" readout.
func entityEmitterLiveCount(idx int) int {
	if idx < 0 || idx >= len(entityPreviewEmitters) {
		return 0
	}
	return entityPreviewEmitters[idx].LiveCount()
}

// entityEmitterLoaded reports whether one attachment's document was read.
func entityEmitterLoaded(idx int) bool {
	return idx >= 0 && idx < len(entityPreviewEmitters) && entityPreviewEmitters[idx].Loaded()
}

// entityEmitterRestart clears one attachment's live particles and rewinds its
// cycles — the panel's Restart, for judging a one-shot burst.
func entityEmitterRestart(idx int) {
	if idx >= 0 && idx < len(entityPreviewEmitters) {
		entityPreviewEmitters[idx].Reset()
	}
}
