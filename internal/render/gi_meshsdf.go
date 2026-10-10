package render

// gi_meshsdf.go — a model's signed distance field, for GI to trace against.
//
// A brush is a convex hull, and its distance is a few plane tests away
// (gi_sources.go); a model is a bag of triangles, so it is turned into a small
// grid of distances once, the first time GI meets it, and placed in the field
// by its entity's matrix after that (gi_sdf_comp.glsl's meshDist).
//
// The bake itself lives in internal/meshsdf, which a Nanite mesh's cook also
// runs (its grid is baked into the cooked file and handed to GI as is — see
// gi_sources.go's sceneryNaniteSources). Bind pose only. A skinned model is
// left out of GI rather than traced in a pose it is not in (gi_sources.go).

import (
	"foundry/internal/meshsdf"

	"github.com/go-gl/mathgl/mgl32"
)

// giMeshRes is the cells on each side of a model's grid.
const giMeshRes = 32

// giMeshBand is how many cells out from the surface a bake writes the true
// distance; beyond it a voxel holds the band.
const giMeshBand = meshsdf.Band

// giMeshSDF is one model's baked distance grid, in its own local space.
type giMeshSDF = meshsdf.Grid

// bakeMeshSDF builds the grid for a triangle list (three points a triangle).
// nil for an empty or degenerate mesh.
func bakeMeshSDF(tris []mgl32.Vec3, n int) *giMeshSDF { return meshsdf.Bake(tris, n) }

// pointTriangleDistance is the distance from p to triangle abc.
func pointTriangleDistance(p, a, b, c mgl32.Vec3) float32 {
	return meshsdf.PointTriangleDistance(p, a, b, c)
}
