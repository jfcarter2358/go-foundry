package main

// model_uv.go — per-face UV editing.
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

// ModelPlanarUnwrap regenerates the given faces' UVs by projecting each face
// onto its own plane, normalised into 0..1.
//
// This is the repair tool for geometry the mesh operations produced rather than
// the user: an extruded side inherits its source face's UVs, and a face dragged
// far from where it started keeps whatever mapping it had. Reprojecting gives
// each face a sane mapping again without hand-editing corners.
//
// fitUnit scales each face to fill 0..1; when false the projection keeps world
// scale, so texel density stays consistent across faces of different sizes.
func (m *ModelDoc) ModelPlanarUnwrap(faces []int, fitUnit bool) {
	for fi := range m.faceSet(faces) {
		f := &m.Faces[fi]
		if len(f.Verts) < 3 {
			continue
		}
		n := m.faceNormal(f)
		if n.Len() < 0.5 {
			continue
		}
		u := perpAxis(n)
		if u.Len() < 1e-6 {
			continue
		}
		u = u.Normalize()
		v := n.Cross(u).Normalize()

		origin := m.Verts[f.Verts[0]]
		uvs := make([]mgl32.Vec2, len(f.Verts))
		minU, minV := float32(math.MaxFloat32), float32(math.MaxFloat32)
		maxU, maxV := -float32(math.MaxFloat32), -float32(math.MaxFloat32)
		for i, vi := range f.Verts {
			d := m.Verts[vi].Sub(origin)
			uv := mgl32.Vec2{d.Dot(u), d.Dot(v)}
			uvs[i] = uv
			minU, maxU = min(minU, uv.X()), max(maxU, uv.X())
			minV, maxV = min(minV, uv.Y()), max(maxV, uv.Y())
		}
		if fitUnit {
			spanU, spanV := maxU-minU, maxV-minV
			if spanU < 1e-6 {
				spanU = 1
			}
			if spanV < 1e-6 {
				spanV = 1
			}
			for i := range uvs {
				uvs[i] = mgl32.Vec2{
					(uvs[i].X() - minU) / spanU,
					(uvs[i].Y() - minV) / spanV,
				}
			}
		}
		f.UVs = uvs
	}
	m.touch()
}

// ModelFlipUV mirrors the given faces' UVs about the centre of their current
// range. Flipping about 0.5 instead would be wrong for any face whose mapping
// is not already inside the unit square.
func (m *ModelDoc) ModelFlipUV(faces []int, flipU, flipV bool) {
	if !flipU && !flipV {
		return
	}
	for fi := range m.faceSet(faces) {
		f := &m.Faces[fi]
		if len(f.UVs) == 0 {
			continue
		}
		minU, minV := float32(math.MaxFloat32), float32(math.MaxFloat32)
		maxU, maxV := -float32(math.MaxFloat32), -float32(math.MaxFloat32)
		for _, uv := range f.UVs {
			minU, maxU = min(minU, uv.X()), max(maxU, uv.X())
			minV, maxV = min(minV, uv.Y()), max(maxV, uv.Y())
		}
		for i, uv := range f.UVs {
			nu, nv := uv.X(), uv.Y()
			if flipU {
				nu = minU + maxU - nu
			}
			if flipV {
				nv = minV + maxV - nv
			}
			f.UVs[i] = mgl32.Vec2{nu, nv}
		}
	}
	m.touch()
}
