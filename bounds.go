package main

// bounds.go — the bounding shapes a level object can wear.
//
// An entity has one and so does a volume, and the overlap test is the same
// either way. It lives apart from both for that reason: volume.go used to reach
// into entity.go for BoundingType and shapeContainsPoint, which made a region
// depend on the things that walk through it.

import (
	"foundry/internal/geom"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// BoundingType is the shape an entity's bounds describe.
//
// The half-extents mean the same thing in every case — they are the box the
// shape is inscribed in — so culling and the editor's footprint can read them
// without caring which kind it is. Only overlap tests need the distinction, and
// they are the scripting engine's `enter`/`exit` events rather than the
// player-against-world collision the engine already does.
type BoundingType int

const (
	BoundingAABB BoundingType = iota
	BoundingSphere
	BoundingCylinder
)

// boundingTypeNames is the YAML/UI name of each kind, indexed by the kind
// itself. Adding a shape means adding its name here and nowhere else — the same
// arrangement primitiveTypeNames used, and for the same reason: a per-file
// switch is what lets an unknown name quietly load as the wrong shape.
var boundingTypeNames = []string{
	"aabb",
	"sphere",
	"cylinder",
}

// boundingTypeName returns a kind's YAML/UI name, falling back to the default
// kind for anything out of range.
func boundingTypeName(k BoundingType) string {
	if int(k) < 0 || int(k) >= len(boundingTypeNames) {
		return boundingTypeNames[BoundingAABB]
	}
	return boundingTypeNames[k]
}

// boundingKindFromName is the inverse of boundingTypeName. An unknown name
// loads as an AABB rather than failing, so a level written by a newer build
// still opens.
func boundingKindFromName(name string) BoundingType {
	for i, n := range boundingTypeNames {
		if n == name {
			return BoundingType(i)
		}
	}
	return BoundingAABB
}

// shapeContainsPoint is the containment test itself, independent of what owns
// the volume — entities and volumes (volume.go) describe their bounds the same
// way and so share one implementation of what "inside" means.
//
// The point is moved into the volume's own space rather than the volume being
// moved into the world's, so a rotated cylinder stays a cylinder instead of
// degenerating into the conservative sphere WorldAABB has to use.
func shapeContainsPoint(p, center, rotation, half mgl32.Vec3, kind BoundingType) bool {
	if half.X() <= 0 || half.Y() <= 0 || half.Z() <= 0 {
		return false
	}
	d := p.Sub(center)
	if rotation != (mgl32.Vec3{}) {
		// The inverse of a rotation is its transpose, so this is the forward
		// matrix read the other way round rather than a matrix inversion.
		inv := eulerMatrixYXZ(rotation).Transpose().Mul4x1(d.Vec4(0))
		d = mgl32.Vec3{inv.X(), inv.Y(), inv.Z()}
	}
	switch kind {
	case BoundingSphere:
		// Normalised against each axis, so a non-uniform "sphere" is the
		// ellipsoid inscribed in the same box rather than silently round.
		nx, ny, nz := d.X()/half.X(), d.Y()/half.Y(), d.Z()/half.Z()
		return nx*nx+ny*ny+nz*nz <= 1
	case BoundingCylinder:
		// Upright: a circular cross-section in XZ, flat caps in Y.
		if d.Y() < -half.Y() || d.Y() > half.Y() {
			return false
		}
		nx, nz := d.X()/half.X(), d.Z()/half.Z()
		return nx*nx+nz*nz <= 1
	default:
		return geom.Abs32(d.X()) <= half.X() && geom.Abs32(d.Y()) <= half.Y() && geom.Abs32(d.Z()) <= half.Z()
	}
}

// normalizeTags sorts, de-duplicates and drops empties. Every path that sets a
// tag list goes through it — loading, merging a definition with an instance,
// the panel — so `#tag` membership never depends on authoring order and a saved
// file never churns.
func normalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}
