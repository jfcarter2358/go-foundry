package world

// model_mesh_cache.go — one built mesh per model file, shared by every entity
// wearing it, and the parallel parse that fills the document cache before a
// level places its entities.
//
// # The meshes
//
// Building an attachment's mesh is the same work for every instance of a
// model: compile the submeshes out of the document, weld the corners into a
// pool, smooth the normals. Twenty marines did it twenty times and uploaded
// twenty identical sets of buffers. What an instance changes is all draw-time
// state — its matrix, alpha, texture name, material, pose palette — so the
// mesh is built once per (file version, skinned or not) and every instance
// gets a copy of the built geometry: the same CPU arrays, read-only, and the
// same GPU buffers, reference-counted in gfx (Geometry.UploadShared).
//
// The key is what changes the BUILT mesh and nothing else:
//
//   - the resolved path and the file's stamp (size, modification time, and
//     its linked skeleton's) — the same stamp the document cache checks, so a
//     model saved from the editor builds afresh;
//   - whether the instance is skinned, which decides whether the bone
//     attributes are built at all;
//   - gfx.IndexedModels, which decides whether the mesh is welded.
//
// The texture is NOT in it: it is a name the draw binds, and an instance's
// override (or a script's SetTexture) only changes the name on its own copy.
//
// An entry whose meshes no live instance draws is dropped by
// ForgetUnusedModelMeshes, which a level load calls before it places anything,
// so the cache holds what the levels in memory use rather than everything a
// session has ever loaded.
//
// # The prefetch
//
// The first instance of each model used to parse its .fmdl on the main thread,
// in the order the level happened to place them: a few hundred milliseconds of
// YAML per rigged character, one after another. PrefetchModelDocuments parses
// every distinct model a level is about to need on all cores at once, into the
// same cache loadModelDocument reads, so the entity loop finds them parsed.

import (
	"os"
	"runtime"
	"sync"

	"foundry/internal/gfx"
	"foundry/internal/model"
)

// shareModelMeshes switches the shared meshes on. Off, every instance builds
// and uploads its own, as before — for a test that compares the two.
var shareModelMeshes = true

type modelMeshKey struct {
	path    string
	size    int64
	mod     int64
	skel    int64
	skinned bool
	indexed bool
}

// modelMesh is one model's built submeshes, as templates an instance copies,
// with the buffers the instances of each share.
type modelMesh struct {
	subs   []gfx.Geometry
	shares []*gfx.MeshShare
}

var modelMeshCache = struct {
	sync.Mutex
	meshes map[modelMeshKey]*modelMesh
}{meshes: map[modelMeshKey]*modelMesh{}}

// modelMeshKeyFor is path's key for a skinned or unskinned instance, and
// false when the file cannot be stat'd (it is then not cached at all).
func modelMeshKeyFor(path string, doc *model.ModelDoc, skinned bool) (modelMeshKey, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return modelMeshKey{}, false
	}
	return modelMeshKey{
		path: path, size: info.Size(), mod: info.ModTime().UnixNano(),
		skel: skeletonStamp(path, doc.Skeleton), skinned: skinned, indexed: gfx.IndexedModels,
	}, true
}

// buildModelMesh compiles doc's submeshes as CPU-only templates: what
// EntityModel.Load used to build per instance, minus the upload and the
// per-instance fields.
func buildModelMesh(doc *model.ModelDoc, skinned bool) (*modelMesh, error) {
	mm := &modelMesh{}
	for _, sub := range doc.ModelSubmeshes() {
		if len(sub.Indices) == 0 {
			continue
		}
		// Indexed: a model loads as a shared vertex pool and an index list
		// (gfx.IndexedModels; docs/nanite/PHASE_0.md).
		g := gfx.Geometry{Smooth: sub.Smooth, SmoothAngle: doc.SmoothAngle, AutoSmooth: true, Indexed: true, CPUOnly: true}
		if skinned {
			// Set before Load, which de-indexes these alongside the positions.
			g.SkinIdx, g.SkinWt = sub.SkinIdx, sub.SkinWt
		}
		if err := g.Load(sub.Vertices, sub.Indices, sub.Texture, [3]float32{}); err != nil {
			return nil, err
		}
		mm.subs = append(mm.subs, g)
	}
	mm.shares = make([]*gfx.MeshShare, len(mm.subs))
	return mm, nil
}

// sharedModelMesh is the built mesh for key, building it from doc on a miss.
func sharedModelMesh(key modelMeshKey, doc *model.ModelDoc) (*modelMesh, error) {
	modelMeshCache.Lock()
	defer modelMeshCache.Unlock()
	if mm := modelMeshCache.meshes[key]; mm != nil {
		return mm, nil
	}
	mm, err := buildModelMesh(doc, key.skinned)
	if err != nil {
		return nil, err
	}
	modelMeshCache.meshes[key] = mm
	return mm, nil
}

// ForgetUnusedModelMeshes drops every cached mesh no live instance draws —
// including, with uploads off, every one, since nothing then holds a claim.
// A level load calls it first: the level it replaces is still alive at that
// point, so what both use survives and is shared across the change.
func ForgetUnusedModelMeshes() {
	modelMeshCache.Lock()
	defer modelMeshCache.Unlock()
	for k, mm := range modelMeshCache.meshes {
		used := false
		for _, s := range mm.shares {
			if s.Live() {
				used = true
				break
			}
		}
		if !used {
			delete(modelMeshCache.meshes, k)
		}
	}
}

// PrefetchModelDocuments parses every model file in paths (resolved, as
// loadModelDocument takes them) that the document cache does not already hold
// fresh, on all cores, and returns each one's document texture — the texture
// an attachment wears unless it overrides it — for a texture prefetch. A file
// that fails to parse is skipped here; the load that needs it reports it.
func PrefetchModelDocuments(paths []string) map[string]string {
	seen := map[string]bool{}
	var todo []string
	for _, p := range paths {
		if p != "" && !seen[p] {
			seen[p] = true
			todo = append(todo, p)
		}
	}
	textures := make([]string, len(todo))
	var next sync.Mutex
	at := 0
	var wg sync.WaitGroup
	workers := min(runtime.GOMAXPROCS(0), len(todo))
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				next.Lock()
				i := at
				at++
				next.Unlock()
				if i >= len(todo) {
					return
				}
				if doc, err := pristineModelDocument(todo[i]); err == nil {
					textures[i] = doc.Texture
				}
			}
		}()
	}
	wg.Wait()
	out := make(map[string]string, len(todo))
	for i, p := range todo {
		out[p] = textures[i]
	}
	return out
}
