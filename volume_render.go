package main

// volume_render.go — drawing a volume in the editor, and only in the editor.
//
// A volume is a region, not a thing: during play it is invisible, and the only
// evidence it exists is that a script fired. That makes it exactly the kind of
// object an author cannot place accurately without seeing it, so the editor
// draws each one as a wireframe cage the same way the spawn point draws an
// arrow — an authoring aid that must never appear in the shot the player sees.
//
// The cage is a line list rather than a translucent solid. A solid would sort
// into the transparent pass and fight with the water and the glass already
// there, and stacking two overlapping triggers would produce a muddle no one
// could read. Lines stay legible however many overlap.

import (
	"foundry/internal/geom"
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// volumeMarkerTexture is what the cage is drawn with. It is an untextured line
// list, so this only has to be a texture that exists — the same reasoning as
// spawnMarkerTexture.
const volumeMarkerTexture = "default"

// volumeCageSegments is how many segments approximate a circle on a sphere or
// cylinder volume. Enough to read as round at the sizes a trigger is authored
// at, few enough that a level full of them is still cheap.
const volumeCageSegments = 24

// ModelMatrix builds the volume's transform. Rotation composes Y, then X, then Z
// — the order everything placed in this engine agrees on.
func (v *Volume) ModelMatrix() mgl32.Mat4 {
	T := mgl32.Translate3D(v.Position.X(), v.Position.Y(), v.Position.Z())
	return T.Mul4(eulerMatrixYXZ(v.Rotation))
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
	if err := gfx.EnsureTextureLoaded(volumeMarkerTexture); err != nil {
		return err
	}
	verts, idx := v.cageMesh()
	return v.Geometry.Load(verts, idx, volumeMarkerTexture, mgl32.Vec3{})
}

// cageMesh returns the line-list vertices and indices for the volume's shape, in
// volume-local space.
func (v *Volume) cageMesh() ([][]float32, []int) {
	switch v.Shape {
	case VolumeSphere:
		return ellipsoidCage(v.HalfX, v.HalfY, v.HalfZ)
	case VolumeCylinder:
		return cylinderCage(v.HalfX, v.HalfY, v.HalfZ)
	default:
		return boxCage(v.HalfX, v.HalfY, v.HalfZ)
	}
}

// boxCage builds the twelve edges of a box.
func boxCage(hx, hy, hz float32) ([][]float32, []int) {
	verts := make([][]float32, 0, 8)
	for i := 0; i < 8; i++ {
		verts = append(verts, []float32{
			cornerSign(i, 0) * hx,
			cornerSign(i, 1) * hy,
			cornerSign(i, 2) * hz,
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

// ellipsoidCage builds three orthogonal rings, which is what reads as a sphere
// in a wireframe without drawing a full lat-long grid.
func ellipsoidCage(hx, hy, hz float32) ([][]float32, []int) {
	var verts [][]float32
	var idx []int
	ring := func(ax, ay, az, bx, by, bz float32) {
		base := len(verts)
		for i := 0; i < volumeCageSegments; i++ {
			c, s := geom.CirclePoint(i, volumeCageSegments)
			verts = append(verts, []float32{
				ax*c + bx*s,
				ay*c + by*s,
				az*c + bz*s,
				0, 0,
			})
			idx = append(idx, base+i, base+(i+1)%volumeCageSegments)
		}
	}
	ring(hx, 0, 0, 0, hy, 0) // XY
	ring(hx, 0, 0, 0, 0, hz) // XZ
	ring(0, hy, 0, 0, 0, hz) // YZ
	return verts, idx
}

// cylinderCage builds the two caps and four uprights joining them.
func cylinderCage(hx, hy, hz float32) ([][]float32, []int) {
	var verts [][]float32
	var idx []int
	capBase := make([]int, 2)
	for c, y := range []float32{-hy, hy} {
		capBase[c] = len(verts)
		for i := 0; i < volumeCageSegments; i++ {
			cs, sn := geom.CirclePoint(i, volumeCageSegments)
			verts = append(verts, []float32{cs * hx, y, sn * hz, 0, 0})
			idx = append(idx, capBase[c]+i, capBase[c]+(i+1)%volumeCageSegments)
		}
	}
	// Four uprights, at the quarter points, so the cage reads as a solid without
	// a wall of vertical lines.
	for q := 0; q < 4; q++ {
		i := q * volumeCageSegments / 4
		idx = append(idx, capBase[0]+i, capBase[1]+i)
	}
	return verts, idx
}

// RenderVolumes draws every volume's cage. Editor-only: the caller is
// responsible for not calling it during play.
//
// Depth testing is left on so a trigger behind a wall stays behind it — an
// author needs to see which room a volume is actually in.
func RenderVolumes(lvl *Level, cull *Frustum) {
	if lvl == nil || len(lvl.Volumes) == 0 {
		return
	}
	for vi := range lvl.Volumes {
		v := &lvl.Volumes[vi]
		if len(v.Geometry.Vertices) == 0 {
			continue
		}
		if cull != nil {
			if lo, hi, ok := v.WorldAABB(); ok && !AABBInFrustum(*cull, lo, hi) {
				continue
			}
		}
		v.Geometry.DrawLinesWithTransform(v.ModelMatrix())
	}
}

// LoadVolumes uploads every volume's cage, for use after a level load or an
// undo. Errors are not fatal: a marker that failed to build is a missing
// authoring aid, not a broken level.
func LoadVolumes(lvl *Level) {
	if lvl == nil {
		return
	}
	for i := range lvl.Volumes {
		_ = lvl.Volumes[i].Load()
	}
}

// DeleteVolumes releases every volume's GPU handles, so replacing a level does
// not leak the previous one's markers.
func DeleteVolumes(lvl *Level) {
	if lvl == nil {
		return
	}
	for i := range lvl.Volumes {
		lvl.Volumes[i].Geometry.Delete()
	}
}
