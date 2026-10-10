# Nanite Phase 0: baseline and indexed meshes

Status: **complete**. Nothing is committed; everything is in the working tree.

Phase 0 does two things:

1. It records a per-pass baseline of level1, level2 and level3 before any Nanite
   work lands.
2. It adds an indexed mesh type that later phases can cluster. Models now load
   and draw indexed, and the picture does not change.

## What was built

### `gfx.IndexedMesh` (internal/gfx/indexed_mesh.go)

`gfx.IndexedMesh` is a vertex pool plus a `uint32` triangle list. Its per-vertex
arrays run in parallel and use the same layouts `Geometry` uploads:

| Array | Floats per vertex |
| --- | --- |
| `Vertices` (`X,Y,Z,U,V`) | 5 |
| `Normals` | 3 |
| `Light`, `LightSmooth` (the baked light record) | `LightStride` = 7 each |
| `SkinIdx`, `SkinWt` | 4 each |

An attribute the mesh lacks is `nil`. When `Indices` is `nil` the mesh is a
corner soup, which is the shape `Geometry` has always held.

| Function | What it does |
| --- | --- |
| `WeldCorners(soup)` | Merges corners whose whole attribute record matches bit for bit. Order is first use. Uses open hashing on FNV over the float bits. A ragged attribute array is dropped, not trusted. |
| `Deindex()` | Turns an indexed mesh back into a corner soup. |
| `Validate()` | Checks that the arrays run in parallel and that every index is inside the pool. |
| `Bounds()` | Returns the mesh's bounding box. |
| `ForEachTriangle(fn)` | Calls `fn` for each triangle. |
| `VertexCount`, `CornerCount`, `TriangleCount` | Counts. |

The comparison is bitwise on purpose. Corners that share a position but differ
anywhere else stay separate vertices: UV seams, creases that smoothing split, and
smooth/sharp edges all stay split. So every corner reaches the vertex shader
with exactly the inputs it had before.

### `Geometry` changes (internal/gfx/geometry.go)

**New fields.**
- `Indexed` is an input, set before `Load`.
- `Indices []uint32` holds the index list on the CPU side.
- `Ebo` is the index buffer. It is part of the VAO's state.

**What `Load` does.** `Load` builds the per-corner arrays as before. If
`Indexed && gfx.IndexedModels` is true, it then welds them through
`Geometry.weld()`. The weld covers positions, UVs, normals (including smoothed
normals split at creases), both baked-light records, and the skin index and
weight arrays. Each `Load` resets `Indices`, so reloading never leaves a stale
index list.

**New methods.**
- `DrawTriangles()` and `DrawTrianglesInstanced(first, count)` call
  `glDrawElements[InstancedBaseInstance]` when the geometry has an index list,
  and the old `glDrawArrays*` call otherwise. Every draw of a `Geometry`'s own
  VAO now goes through them:
  - `Draw`, `DrawWithTransform` and `DrawInstanced`
  - `DrawThroughProgram` (material_surface.go)
  - `drawGeometryShadow` (shadow.go), which is used by the cascade, live and
    point shadow passes
  - the instanced scenery shadow loop (scenery_instanced.go)
  - the entity editor's `entityDrawGeometry`
  - the model editor's `drawModelMesh`
- `Mesh()`, `IsIndexed()`, `CornerCount()`, `ForEachTriangle()` and
  `TriangleVertices()` serve CPU code that reads triangles.

**Cleanup.** `Delete` frees the `Ebo`, and `Unload` drops `Indices`.

### Models switched to indexed

The three places that load models now set `Indexed: true`:
- `world.Scenery.Load` (scenery.go)
- `world.EntityModel.Load` (entity_model.go), which covers entity attachments,
  the player's models and the first-person viewmodel
- the model editor's mesh upload (editor_model_render.go)

Brush faces, compiled static surfaces, decals, markers and volumes still use the
de-indexed soup. The chunk and static batches concatenate those buffers, so
they stay as they are until Phase 1 clusters them.

### CPU code that read model `Vertices` as a triangle list

Model `Vertices` is now a vertex pool, not a triangle list, so these readers
changed:

| File | Change |
| --- | --- |
| render/gi_sources.go | The entity-model mesh SDF bake walks `ForEachTriangle`. |
| editor/editor_3d_pick.go, editor_scenery.go | Picking an entity or a scenery placement goes through the new `pickModelGeometryT`. It walks the index list directly and does not copy out a soup on each pick. |
| render/point_shadow_cache.go | The point-shadow signature also hashes the index list's length and identity. |

Every other reader of `.Vertices` was audited. They read brush faces, static
surfaces, decals or markers, which stay de-indexed:
- collision/raycast*.go
- world/decal_*.go
- biome/ground.go
- editor_detail.go and editor_3d_visibility.go, which work on brush detail
  meshes
- editor_entity_editor_render.go's vertex walk, which builds a bounding box and
  works on either shape

## Flags

| Flag | Default | Effect |
| --- | --- | --- |
| `FOUNDRY_NANITE_INDEXED=0/1` (`gfx.IndexedModels`) | **on** | Off puts models back on the de-indexed path, which is the fallback and the A/B switch. Read when a model loads. |

The flag defaults to on because every parity check below came back exact.

## Tests

| Test | What it checks |
| --- | --- |
| `gfx/indexed_mesh_test.go` | The weld shares a quad's diagonal. It keeps seams, creases and skin boundaries apart. Weld then de-index round-trips every attribute bit for bit on a random soup. Re-welding is a no-op. Ragged attributes are dropped. `Validate` catches bad meshes. An indexed `Geometry.Load` matches the de-indexed load at both a creasing and a smoothing angle, with skin and a short light row. Turning the switch off, and reloading, clears the indices. `envFlag` parses the switch. `BenchmarkWeldCorners` is the benchmark. |
| `world/model_indexed_test.go` | Loads every `.fmdl` in the repo through both `Scenery.Load` and `EntityModel.Load`, with the switch off and then on, and requires the de-indexed indexed mesh to equal the soup bit for bit: positions, UVs, normals, bones and weights. A rigged cube checks skin attributes. `FOUNDRY_NANITE_MODELS_DIR=<project>/resources/models` adds a game project's models to the run. |
| `render/model_indexed_gl_test.go` (GL) | Pixel-exact comparisons of indexed and de-indexed drawing. **(a)** Three dropship placements, painted in their own smoothed normal colours so a mis-welded normal changes colour, drawn per placement and through the instanced scenery path. **(b)** Depth from the cascade shadow program, per placement and instanced. **(c)** A skinned cube posed off its rest pose. |

A mutation check confirms the GL test can fail. When the weld key was cut down to
position and UV only, (a) failed with 50 of 624 drawn pixels different. The weld
was then restored.

All of the following pass:
- `go build ./...` and `go vet ./...`
- `go test ./...`
- `FOUNDRY_GL_TEST=1 go test ./internal/render/... ./internal/gfx/...`, on a
  real GL 4.3 context (Mesa llvmpipe under WSLg)

`FOUNDRY_GL_TEST=1 go test ./internal/harness/` fails in
`TestAPlaySessionDrawsTheRoomItIsStandingIn` ("the picture is one flat colour").
**That failure predates this phase.** A clean `git archive HEAD` build fails the
same way, and so does the working tree with `FOUNDRY_NANITE_INDEXED=0`. It is not
caused by this phase.

## Measurements

### Vertex records saved

From `world/model_indexed_test.go` with the game project's models directory
added. Each entry is corners drawn, then pool vertices after the weld (scenery
load path):

```
apc.fmdl          20406 -> 11710 (1.74x)   soldier.fmdl   11052 -> 4824 (2.29x)
marine.fmdl        4500 ->  2290 (1.97x)   mechanic.fmdl   4362 -> 1959 (2.23x)
human.fmdl         4137 ->  1403 (2.95x)   tank_hull.fmdl  2832 -> 1372 (2.06x)
banner.fmdl        1536 ->   306 (5.02x)   flag.fmdl        864 ->  182 (4.75x)
fp_arms.fmdl        930 ->   542 (1.72x)   dropship.fmdl   1080 ->  651 (1.66x)
all 34 models:    65022 -> 31269 vertex records (2.08x fewer)
```

The vertex buffer of an unskinned model with normals shrinks from 32 B per corner
to 32 B per vertex plus 4 B per index. Across the 34 models that is about
2.08 MB of buffers down to 1.26 MB. Each placement uploads its own buffers, so
the saving scales with the number of placements. Sharing buffers between
placements is Phase 1's job.

Most of the cube-like props (pickups, weapons) only reach 1.5x. `ModelSubmeshes`
gives every face its own corner records, so only each quad's diagonal can be
shared. The cost is the weld at load time: `BenchmarkWeldCorners` measures
0.83 ms for a soldier-sized mesh (11k corners, with normals and skin) on the dev
box. Level load times in the bench did not change: 3.3 s on and off for level1
and level2, and 3.4 to 3.6 s for level3, which is within run-to-run noise.

### Frame baseline (FOUNDRY_BENCH, native)

**Setup.**
- GTX 1070, Windows, cross-built dev exe from this working tree
  (`foundry-nanite0.exe`).
- Run in the scratch project copy `%LOCALAPPDATA%\Temp\foundry-bench`, with
  shaders identical to the working tree.
- Compiled `.fbsp`, 1600x900, 8x MSAA, 90 warm-up and 240 measured frames per
  pose, `menus=answer`.
- Raw reports are in `docs/nanite/phase0_bench/{l1,l2,l3}_{on,off}.txt`. "off" is
  the baseline (`FOUNDRY_NANITE_INDEXED=0`) and "on" is the Phase 0 default.

**Caveat:** the GPU was not idle. The user's own `foundry.exe` editor was running
and `nvidia-smi` showed 43% utilisation before the runs. Absolute numbers are
somewhat pessimistic. The on/off pairs ran back to back under the same load.

All times are in ms. GPU columns are averages from the profiler.

| Level / pose | Frame avg (off → on) | GPU frame (off → on) | `entities` GPU | `world: compiled` GPU | Biggest GPU pass |
| --- | --- | --- | --- | --- | --- |
| level1 spawn | 29.68 → 29.44 | 28.13 → 27.93 | 0.03 → 0.03 | 0.00 | `gi: probes` 20.26 |
| level1 hangar `1.5,102,-20,-1.5708,-0.12` | 39.39 → 39.20 | 36.93 → 36.68 | 0.22 → 0.21 | 0.00 | `gi: probes` 28.79; `world: chunks` 5.95 |
| level1 ground `1.5,4,-77,3.14,0` | 8.54 → 8.54 | 6.70 → 6.70 | 0.00 | 0.00 | `gi: probes` 5.15 |
| level2 spawn | 22.84 → 22.74 | 21.36 → 21.29 | 1.18 → 1.16 | 4.56 → 4.54 | `gi` 14.61 |
| level2 `0,8,10,-1.5708,-0.12` | 23.38 → 23.11 | 21.82 → 21.62 | 1.17 → 1.15 | 4.75 → 4.71 | `gi` 14.69 |
| level3 spawn | 27.76 → 27.74 | 25.48 → 25.46 | 1.85 → 1.81 | 4.97 → 4.93 | `gi` 17.58 |

**What the baseline says for the later phases:**

- **Every level is GPU-bound** (`gpu wait` is 5 to 34 ms). On this card the
  biggest single cost is the live GI probe trace, at 15 to 29 ms, not geometry.
- **Geometry passes cost 5 to 7 ms of GPU.** Compiled static surfaces cost
  4.5 to 5 ms on level2 and level3, and level1's unbaked hangar chunk costs
  5.9 ms (`world: chunks`). That is what Phase 1's cluster culling has to cut.
- **Entities cost 1.2 to 1.9 ms of GPU** on level2 and level3.
- **Indexed models are 0 to 2% faster on the `entities` pass**, within noise.
  That was expected: the win this phase is the vertex data and the cluster-ready
  shape, not frame time.
- **CPU is high on level2 and level3** (15 to 20 ms a frame). `world: compiled`
  takes 5 to 7 ms of CPU and `shadows: sun` takes about 3 ms. A GPU-driven cull
  would also remove much of that.

### Visual parity

| Check | Result |
| --- | --- |
| Controlled GL tests above | Exact: 0 differing pixels and 0 differing depth samples. |
| `foundry play run -level level2 -compiled -gi off`, deterministic at 60 Hz, two shots including the skinned first-person arms and rifle | Shot b: 0 px differ. Shot a: 7 px differ, all ≤ 9/255, in distant particles. Two runs with the switch off differ by 11 px in the same place, so this is the noise floor. |
| Native bench screenshots (`shots=`) | Not usable for parity. Two runs with the switch off already differ by tens of thousands of pixels from idle sway, AI and particles, and on vs off falls inside that range. |

## Known gaps / notes

- **The weld runs at every model load**, about 0.8 ms per soldier-sized
  attachment. It is not cached per document, so a level with hundreds of
  identical attachments pays it hundreds of times. Phase 1's cook, keyed on the
  source hash, is the natural cache. Buffers are also still per placement, with
  no shared pool yet. Both are Phase 1 work.
- **`ModelSubmeshes` keeps per-face corner records.** That limits sharing on
  flat-shaded props to 1.5x. Re-sharing UV-continuous corners across faces
  already happens through the weld whenever records match. More would need the
  model format to share UVs.
- **Brushes and compiled surfaces stay de-indexed.** Phase 1 clusters them from
  `internal/compile`.
- **`foundry render viewmodel` draws an empty frame** for level1 and level2 of the
  game project. This predates the phase: a HEAD build does the same. So
  `foundry play` was used for the deterministic capture instead.
- **No new render pass, so no new F3 profiler section.** The existing
  `entities`, `scenery` and shadow sections time the indexed draws.
