package main

// editor_entity_marker.go — the wireframe the editor draws around an entity
// that has nothing else to show.
//
// An entity is legitimately mesh-less: a trigger, a script anchor, or simply
// one that was placed a moment ago and has not been given a model yet. In the
// 2D view those still read, because that view draws every entity's footprint.
// In 3D they would be nothing at all — invisible, and clickable only by
// guessing where they are — which makes "place one and then describe it" a
// thing the editor can only half do. So the bounding volume is drawn instead,
// on exactly the entities the renderer had nothing to draw for.
//
// The cages are three shared unit meshes scaled per draw rather than a mesh per
// entity. Bounds change under a drag, and a per-entity mesh would mean
// re-uploading a VBO every frame of it; a unit cage under a matrix costs
// nothing to resize. It also means the markers need no loading, no reloading
// after an undo, and no cleanup when a level is swapped out.

import (
	"foundry/internal/gfx"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// entityMarkerCages holds one unit cage per BoundingType, built on first use —
// which has to be inside the frame loop, since uploading needs a live GL
// context. A zero Geometry has no vertices, so an entry that failed to build
// simply draws nothing.
var entityMarkerCages = make([]gfx.Geometry, len(world.BoundingTypeNames))

// entityMarkerCage returns the unit cage for a bounding kind, building it if
// this is the first time it has been asked for.
func entityMarkerCage(kind world.BoundingType) *gfx.Geometry {
	i := int(kind)
	if i < 0 || i >= len(entityMarkerCages) {
		i = int(world.BoundingAABB)
	}
	g := &entityMarkerCages[i]
	if len(g.Vertices) > 0 {
		return g
	}
	if err := gfx.EnsureTextureLoaded(world.VolumeMarkerTexture); err != nil {
		return g
	}
	// Half-extents of 1: the draw matrix carries the entity's real ones, so one
	// mesh serves every size.
	var verts [][]float32
	var idx []int
	switch world.BoundingType(i) {
	case world.BoundingSphere:
		verts, idx = world.EllipsoidCage(1, 1, 1)
	case world.BoundingCylinder:
		verts, idx = world.CylinderCage(1, 1, 1)
	default:
		verts, idx = world.BoxCage(1, 1, 1)
	}
	if err := g.Load(verts, idx, world.VolumeMarkerTexture, mgl32.Vec3{}); err != nil {
		return g
	}
	return g
}

// entityHasVisibleGeometry reports whether the colour pass drew anything for
// this entity. It is the exact condition RenderEntities walks, so a marker
// appears precisely where a model did not.
func entityHasVisibleGeometry(e *world.Entity) bool {
	for i := range e.Models {
		if e.Models[i].Visible && e.Models[i].HasGeometry() {
			return true
		}
	}
	return false
}

// entityMarkerMatrix places the unit cage on an entity's bounding volume: the
// entity's own transform, the volume's offset within it, and its half-extents
// as a scale. The offset is applied unscaled because the entity's matrix has
// already scaled everything under it — the same composition BoundCenter does
// by hand.
func entityMarkerMatrix(e *world.Entity) mgl32.Mat4 {
	off := e.BoundOffset
	return e.ModelMatrix().
		Mul4(mgl32.Translate3D(off.X(), off.Y(), off.Z())).
		Mul4(mgl32.Scale3D(e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ))
}

// RenderEntityMarkers draws a cage around every entity the renderer had nothing
// to draw. Editor-only: the caller is responsible for not calling it during
// play, the same rule the spawn marker and the volume cages follow.
func RenderEntityMarkers(lvl *world.Level, cull *Frustum) {
	if lvl == nil {
		return
	}
	for ei := range lvl.Entities {
		e := &lvl.Entities[ei]
		// Hidden entities are skipped rather than marked. A marker on one would
		// be a thing the click-picker refuses to select (pick3D skips invisible
		// entities too), which is worse than not drawing it.
		if !e.Visible || !e.HasBounds() || entityHasVisibleGeometry(e) {
			continue
		}
		if !entityInFrustum(e, cull) {
			continue
		}
		if g := entityMarkerCage(e.BoundingKind); len(g.Vertices) > 0 {
			g.DrawLinesWithTransform(entityMarkerMatrix(e))
		}
	}
}
