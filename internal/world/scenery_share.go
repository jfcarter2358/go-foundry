package world

// scenery_share.go — one classic-path mesh per cooked model, shared by every
// placement of it.
//
// # Why
//
// A cooked mesh (.fnm) the GPU-driven cull does not draw — no compute, the
// FOUNDRY_NANITE_MESHES switch off, a placement opted out (NoNanite), or a
// headless tool that loads with uploads off, which is where `foundry
// profile-load` lives — is drawn the classic way, as a gfx.Geometry per
// material slot over the full detail, with a second one for its shadow
// stand-in (classicShadowLOD's fallback cut). Building that used to happen PER
// PLACEMENT: each one de-indexed the whole mesh into per-corner rows, normals
// and colours, welded it back into a pool and uploaded its own buffers. On the
// canyon's walls that is 1,200 placements of four 120k-triangle cliff chunks,
// about 25 MB of Go heap each — some 30 GB, which is what exhausted memory
// (docs/canyon/PHASE_3.md, "Known gaps"). nanite.Load already shared the
// cooked mesh itself; this shares what is built from it.
//
// # What is shared and what is not
//
// What a slot's Geometry holds splits cleanly in two:
//
//   - the BUILT mesh — Vertices, Indices, Normals, the private colour records,
//     and the shadow stand-in's same — is a function of the cooked mesh and of
//     the two switches that change what is built (gfx.IndexedModels, which
//     decides whether it is welded, and NaniteClassicShadowLOD, whether the
//     stand-in exists). It is built once per key as a CPU-only TEMPLATE;
//   - everything else is draw-time state the placement owns on its own copy of
//     the Geometry struct: the texture name (its Materials override), Alpha
//     (SetAlpha, the editor ghost), the VAO, and — never stored on the
//     Geometry at all — the model matrix and wind, which the draw carries.
//
// So a placement's Submeshes are value copies of the templates: the same
// arrays, READ-ONLY to everyone (no reader of a scenery submesh writes into
// its vertices; picking, the point-shadow signature, raycasts and the
// instancer only read them), and on the GPU the same buffers, reference
// counted in gfx (Geometry.UploadShared) with a VAO of the placement's own.
// It is the scheme entity attachments already use (model_mesh_cache.go), and
// the instancer's grouping (render/scenery_instanced.go) is unchanged: it
// keys on the model path and the surface, not on the buffers.
//
// The texture is not in the key: it is a name the draw binds, and a moss
// override and a snow one draw from the same buffers.
//
// # Lifetime
//
// The cache holds an entry while some placement holds a CLAIM on it, counted
// here rather than through the GL buffers' count, because with uploads off
// (the headless tools) there are no buffers to count. Scenery.Delete — which
// Load calls first, and every owner already calls to give a placement up —
// releases the claim, and the last one drops the entry. A claim releases at
// most once: a Scenery struct copied by value shares its claim pointer, and
// deleting both copies must not count twice.
//
// A re-cooked file is a new *nanite.Mesh (nanite.Load stamps the file), so a
// placement reloaded after its model changed builds under a new key, and the
// old entry goes with the last placement still drawing it.

import (
	"fmt"
	"sync"

	"foundry/internal/gfx"
	"foundry/internal/nanite"

	"github.com/go-gl/mathgl/mgl32"
)

// shareSceneryMeshes switches the shared classic meshes on. Off, every
// placement builds its own, as before — for a test that compares the two.
var shareSceneryMeshes = true

// sceneryClassicKey is what changes the built classic mesh of a cooked model.
type sceneryClassicKey struct {
	mesh      *nanite.Mesh
	indexed   bool
	shadowLOD bool
}

// sceneryClassicMesh is one cooked mesh's classic submeshes, as CPU-only
// templates a placement copies, with the buffers its placements share.
type sceneryClassicMesh struct {
	key sceneryClassicKey
	// slots[i] is the material slot template subs[i] draws.
	slots []int
	subs  []gfx.Geometry
	// shares and lodShares are subs[i]'s buffers and its shadow stand-in's.
	shares, lodShares []*gfx.MeshShare
	// refs counts the placements holding a claim; guarded by the cache lock.
	refs int
	// cached is whether the entry is in the cache (false for an unshared
	// build, which nothing else can find).
	cached bool
}

// sceneryClassicClaim is one placement's hold on a classic mesh.
type sceneryClassicClaim struct {
	mesh     *sceneryClassicMesh
	released bool
}

var sceneryClassicCache = struct {
	sync.Mutex
	meshes map[sceneryClassicKey]*sceneryClassicMesh
}{meshes: map[sceneryClassicKey]*sceneryClassicMesh{}}

// claimSceneryClassic is the built classic mesh for m, from the cache or
// built on a miss, with a claim on it for the caller.
func claimSceneryClassic(m *nanite.Mesh) (*sceneryClassicClaim, error) {
	key := sceneryClassicKey{mesh: m, indexed: gfx.IndexedModels, shadowLOD: NaniteClassicShadowLOD}
	if !shareSceneryMeshes {
		cm, err := buildSceneryClassic(key)
		if err != nil {
			return nil, err
		}
		cm.refs = 1
		return &sceneryClassicClaim{mesh: cm}, nil
	}
	// Built under the lock: two loads of one mesh at once must not both
	// build it, and a build is the expensive part either way.
	sceneryClassicCache.Lock()
	defer sceneryClassicCache.Unlock()
	cm := sceneryClassicCache.meshes[key]
	if cm == nil {
		var err error
		if cm, err = buildSceneryClassic(key); err != nil {
			return nil, err
		}
		cm.cached = true
		sceneryClassicCache.meshes[key] = cm
	}
	cm.refs++
	return &sceneryClassicClaim{mesh: cm}, nil
}

// release gives up the claim, dropping the entry with its last one. The GL
// buffers are not touched here: each placement's Geometry.Delete gives up its
// own hold on them.
func (c *sceneryClassicClaim) release() {
	if c == nil || c.released {
		return
	}
	c.released = true
	cm := c.mesh
	if !cm.cached {
		cm.refs--
		return
	}
	sceneryClassicCache.Lock()
	defer sceneryClassicCache.Unlock()
	cm.refs--
	if cm.refs <= 0 && sceneryClassicCache.meshes[cm.key] == cm {
		delete(sceneryClassicCache.meshes, cm.key)
	}
}

// sceneryClassicCached reports how many classic meshes the cache holds and
// how many claims they have between them, for tests.
func sceneryClassicCached() (meshes, claims int) {
	sceneryClassicCache.Lock()
	defer sceneryClassicCache.Unlock()
	for _, cm := range sceneryClassicCache.meshes {
		meshes++
		claims += cm.refs
	}
	return meshes, claims
}

// buildSceneryClassic builds key's mesh: for each material slot with
// triangles, the full detail as a CPU-only Geometry wearing the cooked
// texture, with its shadow stand-in. What loadNanite used to build per
// placement, minus the upload.
func buildSceneryClassic(key sceneryClassicKey) (*sceneryClassicMesh, error) {
	m := key.mesh
	// The classic path draws the full detail: decode every page
	// (docs/nanite/PHASE_3.md); a no-op once done.
	if err := m.Flat(); err != nil {
		return nil, err
	}
	cm := &sceneryClassicMesh{key: key}
	for si := range m.Slots {
		first, count := m.SlotTriangles(si)
		if count == 0 {
			continue
		}
		tex := m.Slots[si].Texture
		g := gfx.Geometry{Smooth: m.Slots[si].Smooth, AutoSmooth: true, Indexed: true, CPUOnly: true}
		idx := m.Indices[3*first : 3*(first+count)]
		rows := make([][]float32, 0, len(idx))
		normals := make([][]float32, 0, len(idx))
		order := make([]int, len(idx))
		for k, v := range idx {
			rows = append(rows, m.Vertices[5*v:5*v+5])
			normals = append(normals, m.Normals[3*v:3*v+3])
			order[k] = k
		}
		g.VertexNormals = normals
		if m.HasColors && len(m.Colors) == len(m.Vertices)/5 {
			// The cooked vertex colours (docs/canyon/PHASE_2.md), for the
			// classic draw to multiply in as the GPU path does.
			g.VertexColors = make([][]float32, 0, len(idx))
			for _, v := range idx {
				c := m.Colors[v]
				g.VertexColors = append(g.VertexColors, []float32{float32(c&0xff) / 255, float32(c>>8&0xff) / 255,
					float32(c>>16&0xff) / 255, float32(c>>24) / 255})
			}
		}
		if err := g.Load(rows, order, tex, mgl32.Vec3{}); err != nil {
			return nil, err
		}
		if key.shadowLOD {
			lod, err := classicShadowLOD(&g, m, si, tex)
			if err != nil {
				return nil, fmt.Errorf("shadow LOD: %w", err)
			}
			g.ShadowLOD = lod
		}
		cm.slots = append(cm.slots, si)
		cm.subs = append(cm.subs, g)
	}
	cm.shares = make([]*gfx.MeshShare, len(cm.subs))
	cm.lodShares = make([]*gfx.MeshShare, len(cm.subs))
	return cm, nil
}

// placeSceneryClassic gives s its submeshes from its claimed classic mesh:
// value copies of the templates wearing the placement's own textures, each
// drawing from the shared buffers through a VAO of its own.
func (s *Scenery) placeSceneryClassic(c *sceneryClassicClaim) {
	cm := c.mesh
	for i := range cm.subs {
		tex := s.SlotTexture(cm.slots[i])
		ensureSceneryTexture(s.ID, tex)
		g := cm.subs[i]
		g.Texture = tex
		g.MaterialName = s.SlotMaterial(cm.slots[i])
		gfx.EnsureMaterialFxTextures(g.MaterialName, tex)
		g.UploadShared(&cm.shares[i])
		if t := cm.subs[i].ShadowLOD; t != nil {
			lod := *t
			lod.Texture = tex
			lod.UploadShared(&cm.lodShares[i])
			g.ShadowLOD = &lod
		}
		s.Submeshes = append(s.Submeshes, g)
	}
}
