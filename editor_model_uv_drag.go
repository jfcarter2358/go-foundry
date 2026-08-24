package main

// editor_model_uv_drag.go — the model editor's live UV-slider session.
//
// This is editor session state, not model core: it reads the open document
// (modelDoc) and pushes onto the model editor's undo stack. The document
// operations it drives — ModelPlanarUnwrap, ModelFlipUV — stay in model_uv.go,
// which knows nothing about an editing session.
//
// ModelFace stores explicit UVs per corner rather than the offset/scale/rotation
// triple a Wall carries, because subdivide and extrude have to interpolate and
// generate them per vertex — there is no single transform that describes a face
// after it has been split four ways.
//
// That makes "drag a UV scale slider" awkward: applying a scale to already
// scaled UVs compounds every frame the slider moves. The fix here is to capture
// the selection's UVs once, then treat the sliders as a transform re-applied to
// that captured base each time they change. Moving on to a different selection
// recaptures, which is what makes the previous transform permanent.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// modelUVTransform is the live transform driven by the panel's UV controls.
type modelUVTransform struct {
	Offset   mgl32.Vec2
	Scale    mgl32.Vec2
	Rotation float32 // radians
}

func identityUVTransform() modelUVTransform {
	return modelUVTransform{Scale: mgl32.Vec2{1, 1}}
}

func (t modelUVTransform) isIdentity() bool {
	return t.Offset == (mgl32.Vec2{}) && t.Scale == (mgl32.Vec2{1, 1}) && t.Rotation == 0
}

// apply transforms one UV: scale about the origin, then rotate, then offset —
// the same order applyUVTransform (wall.go) uses, so a texture behaves the same
// way on a model face as on a wall.
func (t modelUVTransform) apply(uv mgl32.Vec2) mgl32.Vec2 {
	scale := t.Scale
	if scale == (mgl32.Vec2{}) {
		scale = mgl32.Vec2{1, 1}
	}
	u := uv.X() * scale.X()
	v := uv.Y() * scale.Y()
	cos := float32(math.Cos(float64(t.Rotation)))
	sin := float32(math.Sin(float64(t.Rotation)))
	return mgl32.Vec2{
		u*cos - v*sin + t.Offset.X(),
		u*sin + v*cos + t.Offset.Y(),
	}
}

// modelUVEdit holds the captured base UVs and the transform being applied to
// them.
var modelUVEdit struct {
	Xform modelUVTransform
	Base  map[int][]mgl32.Vec2 // face index -> UVs as they were at capture time
	Key   string               // the selection the base belongs to
	Dirty bool                 // an undo entry has been pushed for this capture
}

// modelUVSelectionKey identifies the current face selection, so the base UVs can
// be recaptured when it changes.
func modelUVSelectionKey(faces []int) string {
	key := make([]byte, 0, len(faces)*4)
	for _, fi := range faces {
		key = append(key, byte(fi), byte(fi>>8), byte(fi>>16), byte(fi>>24))
	}
	return string(key)
}

// modelUVSync captures the base UVs when the selection changes, resetting the
// transform to identity. Called every frame the UV section is open.
func modelUVSync(faces []int) {
	key := modelUVSelectionKey(faces)
	if modelUVEdit.Key == key && modelUVEdit.Base != nil {
		return
	}
	modelUVEdit.Key = key
	modelUVEdit.Xform = identityUVTransform()
	modelUVEdit.Dirty = false
	modelUVEdit.Base = make(map[int][]mgl32.Vec2, len(faces))
	if modelDoc == nil {
		return
	}
	for _, fi := range faces {
		if fi >= 0 && fi < len(modelDoc.Faces) {
			modelUVEdit.Base[fi] = append([]mgl32.Vec2(nil), modelDoc.Faces[fi].UVs...)
		}
	}
}

// modelUVApply rewrites the selected faces' UVs from the captured base through
// the current transform.
func modelUVApply(faces []int) {
	if modelDoc == nil {
		return
	}
	// One undo entry per capture, not per slider frame — a UV drag is one
	// action, the same rule the translate gizmo follows.
	if !modelUVEdit.Dirty {
		ModelPushUndo()
		modelUVEdit.Dirty = true
	}
	for _, fi := range faces {
		base, ok := modelUVEdit.Base[fi]
		if !ok || fi < 0 || fi >= len(modelDoc.Faces) {
			continue
		}
		f := &modelDoc.Faces[fi]
		for i := range f.UVs {
			if i < len(base) {
				f.UVs[i] = modelUVEdit.Xform.apply(base[i])
			}
		}
	}
	modelDoc.touch()
}

// modelUVInvalidate forces a recapture, so an operation that rewrote UVs behind
// the transform's back does not get undone by the next slider move.
func modelUVInvalidate() {
	modelUVEdit.Base = nil
	modelUVEdit.Key = ""
	modelUVEdit.Xform = identityUVTransform()
}

// ---------- unwrapping ----------
