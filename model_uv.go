package main

// model_uv.go — per-face UV document operations.
//
// ModelFace stores explicit UVs per corner rather than the offset/scale/rotation
// triple a Wall carries, because subdivide and extrude have to interpolate and
// generate them per vertex — there is no single transform that describes a face
// after it has been split four ways.
//
// Everything here is a method on ModelDoc and takes the faces it acts on as an
// argument: no open document, no selection, no undo stack. The interactive
// slider session that drives these lives in editor_model_uv_drag.go.

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

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
