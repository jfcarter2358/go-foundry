package world

// brush_generation.go — a counter that moves whenever a brush's drawn mesh
// might have.
//
// Anything that caches a structure built from brush meshes — the raycast's
// bounding-volume trees (collision/raycast_accel.go) — needs to know when to
// throw it away, and has to find out without walking every brush on every
// query, which is exactly the cost the cache exists to avoid. Every way a
// brush's drawn geometry changes goes through BrushMesh.Load, Release or
// Unload, so those bump this; a cache stamped with an older value re-checks
// what it was built from.
//
// The one thing that changes what a ray should do with a brush without
// touching its mesh is the Dynamic flag, which decides whether it may move.
// Whoever flips it calls MarkBrushMeshesChanged.

import "sync/atomic"

var brushMeshGeneration atomic.Uint64

// BrushMeshGeneration is the current value. Only ever compared for equality.
func BrushMeshGeneration() uint64 { return brushMeshGeneration.Load() }

// MarkBrushMeshesChanged moves the counter on.
func MarkBrushMeshesChanged() { brushMeshGeneration.Add(1) }
