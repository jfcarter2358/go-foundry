# Nanite Phase 2b: collision for Nanite meshes

Status: **partial** — every item of the phase is built and tested except
"attached chunks via matrix", which has nothing to attach to in this engine
(see [Known gaps](#known-gaps--notes)). Nothing is committed; Phase 2b is in
the working tree on top of `671b3c9` (phase 2).

Before this phase a cooked Nanite mesh collided as every prop always had: its
bounding box, pushed apart from bodies after they moved, and a linear scan of
every prop per body and per ray. Now a cooked mesh carries its own collision —
a coarse cut of its DAG, cleaned, under a tree, and split into convex hulls —
and a placement collides as its **box**, its **hulls**, its **mesh** or
**none**. Hulls and mesh join the resolve chain bodies already run against
brushes and terrain, so walkers step onto, stand on and slide along a rock or
a cliff exactly as they do a brush. Rays, particles and navigation see the same
shape. All of it is CPU-only Go.

## What was built

### The collision data (`internal/meshcollide`, new, pure CPU)

- **`Build`** takes a collision cut and cleans it:
  - **Weld** by position alone (a grid a millionth of the mesh's size), so a UV
    or crease seam the renderer needs two records for is one vertex here.
  - **Drop degenerates**: triangles with two corners welded together, or no
    area.
  - **Record edges**: `OpenEdges` (used by one triangle) and `NonManifold`
    (more than two).
  - **Quantise** vertices to 16 bits a coordinate over the cut's box (1.5 mm
    over 100 m). Everything after is built from the *dequantised* positions,
    so the tree bounds exactly what the runtime tests.
  - **Tag** each triangle with its material slot (`Tags`, `TagNames`).
- **SAH BVH** (`bvh.go`): binned (12 bins), leaves of up to 4 triangles,
  laid out depth first (left child next, `Right` index), triangles reordered
  to leaf order. Depth is capped (median splits past 40) so queries' fixed
  128-entry stacks cannot overflow. Built and queried **in mesh-local space**.
- **`Resident`**: the decoded form (float positions, per-triangle normals) with
  `Raycast` / `AnyHit` (double-sided Möller–Trumbore, nearest-first traversal,
  any direction length — t is in the direction's units, so a ray carried in by
  an affine inverse keeps its world distance), `NearBox` and `TrianglesInBox`.
- **Convex decomposition** (`decompose.go`, a Go implementation, no cgo):
  recursive axis-aligned plane cuts scored by summed hull volume, V-HACD's
  clipping stage without its voxels.
  - A piece's **concavity** is how deep its own surface (corners, centroids,
    edge midpoints) lies inside its hull.
  - The most concave piece is cut while it exceeds the tolerance
    (`max(Concavity 5 cm, ConcavityRel 2% of the diagonal)`) and there is room
    under `MaxHulls` (16).
  - Candidates are 7 fractions of the piece's box per axis plus a plane
    through its deepest point. They are scored in parallel and picked in
    candidate order, so the result is deterministic.
  - Flat pieces are thickened to a sliver so a sheet still has hulls.
  - **Vertex cap** `MaxHullVerts` (128): half the budget goes to support
    points along evenly spread directions, then greedy farthest-point
    insertion. The capped hull is inside the full one by `VertCapError` at
    worst: 3.4% of a sphere's radius at 128 points, 6.3% at 64.
  - **Quickhull** (`quickhull.go`) with outside sets, float64, an
    extent-scaled epsilon, and the visible region walked across edges so the
    horizon is always one loop.
- **Modes and settings**: `Mode` (auto/box/hulls/mesh/none), `Settings` (error
  3 cm, 16 hulls, 128 points a hull, auto size 4 m), `Data.Resolve(override,
  size)`. Auto means hulls up to 4 m across and mesh above. A mode the data
  cannot serve falls back to the box.

### The cook (`.fnm` format 3, cook version 4)

- `nanite.Mesh.Collision` holds a `*meshcollide.Data`; `Validate` checks it.
- `nanitecook.cookCollision`: the cut is every slot's clusters `Selected` at
  the collision error. That is a cut of the DAG at a world-space error (in
  mesh units), crack-free like any cut, so a closed mesh gives a closed cut.
- **Hash**: the resolved collision settings are folded into `HashSource`, so
  changing them re-cooks and a default spelled out hashes like the default
  (`UpToDateWith`).
- **Project settings**: `game.yaml`'s `nanite:` block (`collision_error`,
  `collision_max_hulls`, `collision_max_hull_verts`, `collision_auto_size`),
  read leniently by `game.ReadNanite` → `nanitecook.ProjectOptions`. The CLI
  cook, Cook Nanite Meshes, Cook and use, and Re-cook all use it.
- `foundry nanite inspect` prints the cut (kept/dropped/welded, vertices, tree
  nodes, open and non-manifold edges, KB) and the hulls (count, cap, points,
  capped, concavity, mode).

### The placement (`world.Scenery`)

- `Scenery.Collision` is the override (`collision:` in the level YAML; absent
  means auto). An unknown value is logged and read as auto.
- `CollisionMode()` resolves the mode:
  - **none**: not solid, or set to none.
  - **box**: an uncooked model, or a mode the cook cannot serve. A box with no
    volume is none.
  - Otherwise the override, or auto by `CollisionSize()`.
- A cooked **sheet**, flat on one axis, now collides in the mesh mode. Its box
  never could.
- `SceneryCollisionWarnings`: the list `foundry check` and the editor share.
- `FOUNDRY_NANITE_COLLISION=0` (`world.NaniteCollision`) puts every cooked
  prop back on its box.

### The runtime (`internal/collision`)

- **Placement broadphase** (`scenery_index.go`). A median-split BVH over the
  solid placements' world boxes replaces the linear scans in
  `ResolveSceneryCollisions`, `LineBlocked`, `appendSceneryHits` and
  `ParticleCollide`.
  - Each entry caches the resolved mode, the matrix and its inverse, the world
    box (for hulls and mesh it is the cooked collision's own box), each hull's
    world box, and the box mode's proxy.
  - The index is keyed on the level and its scenery slice, and checked against
    a per-placement fingerprint once per physics tick (`Epoch`, called by
    `EntityUpdate` and `MovePlayer`). With fewer than 64 props every query
    checks it. `InvalidateScenery` forces a rebuild; the editor's Collision
    group calls it.
- **Surface interface** (`surface.go`). terrain.go's probes — wall rays at
  shins, middle and head; floor rays inside the footprint; one ceiling ray —
  now run against a `probeSurface` (`probe` gives distance and normal; `near`
  is the cheap reject). Terrain implements it (`terrainSurface`), and so does a
  mesh-mode placement (`meshSurface`).
  - `meshSurface` carries the ray into mesh space by the inverse, through the
    BVH, and carries the normal out by the inverse's transpose. The sheet is
    two-sided: the normal faces back along the ray.
  - `pushOutOfTerrain` and `terrainProbe` are thin wrappers.
- **Hulls**. Each hull is a `ConvexHull{Pts, Xform: placement matrix}`, whose
  support is the farthest transformed vertex. `pushOutOfHulls` is
  `pushOutOfChunkBrushes`' loop over a placement's hulls: a box reject per
  hull, GJK/EPA, a push past the depth, and floor/ceiling credit by slope.
- **Compound** (`shape.go`). A placement's hulls as one proxy:
  `Overlap`, `Penetration` (the deepest part), `AABB`, and
  `SweepHitCompound` (the earliest contact over the parts). It is not a
  `Shape`: GJK on a union would answer for its hull.
- **The resolve chain**. `pushOutOfScenery` (hulls and mesh only) runs inside
  the `resolve` closures of `MovePlayer` and `resolveEntityWorld`, for bodies
  in world space. So step-up, floor/ceiling credit and the sweep's sub-steps
  apply to cooked props as to brushes. Box-mode props stay in
  `ResolveSceneryCollisions`, now through the broadphase.
- **Rays**. `sceneryRay` per mode:
  - box: `rayBoxNormal`
  - hulls: `rayConvex` over the hull's planes in mesh space
  - mesh: the BVH

  `sceneryRayNearest` walks the broadphase nearest-first. `sceneryHit` is
  gone; `LineBlocked` stops at the first hit.
- **Particles**. `probeScenery` is the same vertical probe through every
  solid prop in the column, in its mode, merged with the chunk probes.
- **Residency**. A mesh-mode placement's `Resident` is decoded the first time a
  query reaches it — a body's box or a ray touching it. It is dropped after
  `residentTTL` (600) ticks with no query (`residentTick`, from
  `EntityUpdate`). Hulls need no decoding.
- **Stats** (`TakeSceneryStats`): props by mode, resident count and bytes,
  rebuilds, decodes, evictions, and CPU time per query kind while
  `ProfileScenery` is on.

### Navigation (`internal/nav`)

- `appendScenerySolids` rasterises hulls- and mesh-mode props. Box props stay
  out, as scenery always has. Mirrored placements flip winding.
  - Each **hull** is a solid, its faces the hull's triangles.
  - A **watertight mesh** (no open or non-manifold edges) is one `closed`
    solid. The rasteriser finds its spans by **parity** (`paritySpans`): faces
    looking down open a run, faces looking up close it, neighbouring faces of
    one surface count as one crossing, and steep faces are their own span. A
    mesa's inside is solid and a cave or arch stays open, where a brush's
    lowest-to-highest span would fill both in.
  - An **open mesh** becomes one solid per triangle, floor from above.
- `Signature` hashes each such prop's mode, its collision's size and box, and
  its transform (position, rotation, effective scale: the matrix's inputs,
  since the signature runs every frame), so moving one or changing its mode
  rebuilds the grid.

### Editor (`editor/editor_scenery_collision.go`)

- **The Collision group** of a placed prop:
  - **Solid**
  - a **Mode** combo (cooked meshes only), with a tooltip per mode
  - **Show collision**
  - a report: the resolved mode and why (auto's size against the threshold),
    what the cook made (error, triangles, hulls, open edges), and the shared
    warnings
- **Show collision** draws, over the selected prop and through everything:
  every hull edge, the cut's edges (capped at 60k), or the box.

### F3 and the bench

- `render.ProfileAddCPU` (new) adds a CPU-only row under the open section.
  `sim.reportSceneryCollision` adds `props: resolve`, `props: rays`,
  `props: particles` and `props: index` under `entity update` each frame,
  since the queries are spread through the player's move, the AI and the
  entity pass.
- The overlay adds one line, `Prop collision: N box / H hulls / M mesh, R
  meshes resident (KB)`, shown when any prop is in hulls or mesh.
- The bench reports pick the rows up through `ProfileSamples`.

### `foundry check`

- `checkSceneryCollision` prints warnings, without failing the check:
  - the hull cap hit
  - hulls capped to points enough to move them more than the concavity
    tolerance
  - hulls of a mesh with open edges
  - a solid prop that collides as nothing
- Warnings are grouped per model, mode and text, so a field of 256 rocks is
  one line.

## Flags and settings

| Flag / setting | Default | Effect |
| --- | --- | --- |
| `FOUNDRY_NANITE_COLLISION` | on | Cooked props collide as hulls/mesh; off puts every prop on its box. |
| `collision:` (level YAML, per placement) | auto | auto / box / hulls / mesh / none. |
| `game.yaml` `nanite.collision_error` | 0.03 | The cut's error, metres (mesh units). |
| `nanite.collision_max_hulls` | 16 | Decomposition cap. |
| `nanite.collision_max_hull_verts` | 128 | Points a hull. |
| `nanite.collision_auto_size` | 4 | Auto's hulls/mesh split, metres across. |

## Tests

| Test | What it checks |
| --- | --- |
| `meshcollide/meshcollide_test.go` (new) | **Quickhull:** a cube with 200 interior points gives 8 vertices and volume 1. 20 random blobs: every point inside, closed, V−E+F=2. A flat set is refused. **Clean:** a seam-split box welds to 8 vertices, its degenerate is dropped, and it has 0 open edges; a 4×4 sheet has 16 open edges and still gets a hull. **Quantisation** stays within its step. **BVH oracle:** 3000 rays against 1,280 sphere triangles plus 300 loose ones match brute force in t and hit/miss (`AnyHit` too). **Box queries** report every touching triangle. **Hulls:** a box is 1 hull; an L splits at its inner corner, with every corner inside and the notch in no hull, and the 1-hull mutation bridges the notch. The vertex cap error falls 32→64→128 and the rod case holds. Determinism. **Modes:** `Resolve` and `ParseMode`. |
| `nanitecook/nanite_test.go` (2 new) | A cook's collision is closed, smaller than the full detail, tags both slots, every collision vertex is inside some hull (within concavity + cap error), and it survives the file. Settings are in the hash. `ProjectOptions` reads `game.yaml`. |
| `collision/scenery_mesh_test.go` (new) | Mirrors `terrain_test`: the player stands on a cooked sheet; walking a turned hill does not bounce or sink; a cooked 8 m block is a wall whose top is floor. Mirrors `step_up_test`: a player steps onto a 0.4 m cooked curb in both modes; a 2.5 m cooked wall stops it in both. Mirrors `entity_collision_slide_test`: a player slides along a 30°-turned hull wall and never gets inside. An entity falls onto a hulls rock and rests on it. Box mode is the old proxy; hulls is not in the push-apart pass; none passes through. **Ray oracle** (mirrors `raycast_oracle_test`): 1500 rays from outside a field of 60 turned, scaled props in all three modes match brute force over every collision triangle, for `Raycast` and `LineBlocked`. The broadphase matches brute force over 300 props. The index sees edits (per tick for a big level, per query for a small one, `InvalidateScenery`). Particles land on box, hulls and mesh props. Residency: nothing decodes for a far body, a near one decodes, eviction after the TTL, and the stats. **GJK hull cases:** an affinely placed hull's box; penetration along both its axes; no overlap when clear; a sphere sweep; a compound's sweep, its gap, its deepest part and its box. The kill switch. Benchmarks. |
| `nav/scenery_test.go` (new) | A hulls rock leaves no ground node inside it and is walked round. The box-mode mutation shows ground under it. A mesh ramp has floor on it and reaches its platform, while a 5 m mesh cliff is not reachable. The signature sees a move and a mode change. |
| `ai/ai_nanite_test.go` (new) | An agent sent to the top of a watertight cooked mesa (in the mesh mode) takes its ramp, never the cliff, never sinks under the surface, and arrives on the plateau. |
| `harness/cmd_sim_nanite_test.go` (new) | **`foundry sim` with AI across a Nanite cliff:** a mesa written as OBJ and cooked by the real cook (16,896 triangles → a 262-triangle cut, 3 hulls), placed in a saved level with an AI walker and an objective on the plateau. `SimMain` runs the scenario, and the expectation that the walker reaches the plateau holds. The watched positions show it climbed by the ramp: 85 of 289 on the ramp, none off the ground beside it. |
| `harness/cmd_check_scenery_test.go` (new) | `CheckMain` on a saved level with real cooks warns about an open sheet in hulls and a solid prop set to none, does not warn about a watertight mesa, and exits 0. The cap warnings hold in hulls mode only. |
| `editor/editor_nanite_test.go` (2 new) | The Collision report for box and cooked props. The mode round-trips through the level file. The none warning. Show collision draws exactly the hulls' edges, the cut's, the box's 12, nothing for none, and nothing while off. |
| `render/profile_cpu_test.go` (new) | `ProfileAddCPU` adds a CPU-only row, accumulates, and issues no queries. |
| `layering_test.go` | `meshcollide` is pure CPU, below `nanite`, `collision`, `nav` and everything that draws. |

**Mutation checks run:**
- Disabling `pushOutOfScenery` fails 9 collision tests.
- Dropping one hull plane fails the ray oracle.
- Removing the scenery solids from nav fails both nav tests.

**Runs on this box** (WSL2, Mesa D3D12 on the GTX 1070):

- **`go build ./...`, `go vet ./...` and `go test ./...` are clean.**
- **The new tests also pass with `FOUNDRY_NANITE_COLLISION=0`** in the
  environment. They pin the switch on, so the flag cannot turn them into tests
  of the box path.
- **`FOUNDRY_GL_TEST=1` render (70 s), gfx, devui and editor pass.**
- **The harness fails only the known
  `TestAPlaySessionDrawsTheRoomItIsStandingIn`.**

## Independent verification

A separate verifier pass re-ran everything on the same box with nothing else
using the GPU:

- `go build ./...`, `go vet ./...`, `go test -count=1 ./...`: clean.
- `go test -race` on meshcollide, collision, nav and nanitecook: clean.
- `FOUNDRY_GL_TEST=1`: render (73 s, including
  `TestIndexedModelDrawsTheDeindexedPicture`, which also passed three times on
  its own in 0.2-3.4 s; the earlier hang was the two sessions sharing the
  GPU), gfx, devui, editor, collision, nav and world pass. The harness fails
  only the known `TestAPlaySessionDrawsTheRoomItIsStandingIn`.
- Flags off: collision, nav, ai, harness, editor, world, meshcollide,
  nanitecook and sim pass with `FOUNDRY_NANITE_COLLISION=0`; collision, nav,
  ai, harness and world pass with `FOUNDRY_NANITE_MESHES=0`.
- Changed: nav's `Signature` no longer builds each cooked prop's model matrix
  every frame (it hashes the matrix's inputs), and hashes the collision's
  whole box rather than two of its coordinates XORed.
- Not part of this phase but worth knowing (it was reported as "scenery
  shadows draw a huge number of vertices"): `RenderSceneryShadow` skips every
  placement the GPU cull draws (`NaniteGPU`), so a cooked mesh only reaches
  it when the GPU cull is unavailable or the placement opts out
  (`NoNanite`). Then `Scenery.loadNanite` uploads the full-detail triangles
  (not the fallback cut) for each placement, and every cascade and point
  light face draws them. On the bench rock that is 82k triangles per rock
  per shadow view.

## Measurements

**The placement broadphase** against HEAD's linear scan. Box props, the only
kind HEAD knows, in a 200 × 200 m field, with 50 walkers. Same benchmark file in
both trees, i9-10850K under WSL2, `-benchtime 2s`:

| Props | `ResolveSceneryCollisions` HEAD → now | `LineBlocked` (20 m segments) HEAD → now |
| --- | --- | --- |
| 100 | 2.81 ms → 3.5 µs | 4.80 µs → 0.43 µs |
| 1,000 | 28.2 ms → 33.9 µs | 30.6 µs → 0.98 µs |
| 5,000 | 146 ms → 136 µs | 59.8 µs → 1.28 µs |

HEAD rebuilt every proxy per call and tested every body against every prop.

**Per-body cost of the new modes:**

- A player walking a 28,800-triangle cooked hillside in mesh mode costs
  **15.7 µs** a `MovePlayer` tick (`BenchmarkMovePlayerOnACookedHill`).
- A body dropped at random into a dense 40 × 40 m field of 600 hull, mesh and
  box props costs 78 µs a resolve (`BenchmarkPushOutOfSceneryField`). That is
  a stress case: most samples are buried in a prop. Mesh probes are 38% of it,
  GJK/EPA 25%.

**Cooks:**

| Mesh | Full detail | Collision cut (3 cm) | Hulls | Cook total / collision |
| --- | --- | --- | --- | --- |
| `bench_rock.glb` (bumpy sample rock, 2.1 m) | 81,920 | 53,070 (0 open) | 16 (cap hit, concavity 7.9 cm; 14 capped, cap error 2 mm) | 3.0 s / 2.0 s |
| mesa `.obj` (sim test, 16 × 1.6 m) | 16,896 | 262 | 3 | — |

- `bench_rock.fnm` grows from 2.31 MB to 3.46 MB.
- The bumpy sample rock keeps most of its triangles at 3 cm, because its first
  simplification already costs 5 cm (PHASE_2.md). A smooth or faceted mesh
  collapses: the mesa's cut is 1.6% of it.
- `foundry check nanite_bench` on the re-cooked bench copy prints one grouped
  warning: the hull cap was hit for all 256 placements.

No native frame bench was run. The change is CPU collision, and none of the
benched poses has a body near a cooked prop. The F3 rows above are where a
native run would show its cost.

## Known gaps / notes

- **"Attached chunks via matrix" is not done (the reason for `partial`).**
  - Scenery is level-scoped and placed in world space (world/scenery.go). No
    prop rides a chunk or a frame, so there is no placement whose collision
    could follow a chunk's matrix.
  - Colliders are built from the placement's full affine matrix, so a parent
    transform would compose into `sceneryEntry.m` alone.
  - Props that move would need a GPU instance update in
    `render/nanite_scenery.go`: a feature beyond this CPU-only phase.
  - Bodies in a frame (riders in a ship) still never meet world props,
    whatever the mode, as before. Bodies on a `Entity.AttachedChunk` are in
    world space and do meet them.
- **The hull compound joins the resolve chain, not `resolveEntityOverlap`.**
  The push-apart pass runs after a move with no step-up and no sub-steps. Box
  props stay there; hulls get the chain's behaviour. `Compound` and
  `SweepHitCompound` are tested on their own.
- **Collision is in mesh units.** `collision_error` is metres for a mesh
  placed at scale 1. A prop scaled ×4 strays ×4 as far.
- **The full cut is stored even for a hulls-mode mesh**, because a placement
  may override to mesh. For the bumpy bench rock that is most of the file
  growth.
- **The hull cap is hit by very bumpy meshes.** The bench rock's 16 hulls
  bridge hollows up to 7.9 cm, and `foundry check` warns. Raise
  `collision_max_hulls` or use the mesh mode.
- **Residency is decoding, not paging.**
  - The quantised data stays in memory with the cooked mesh (the load cache).
    What comes and goes by proximity is the float form a query uses.
  - Paging from disk is Phase 3.
- **The index checks a big level's props once per tick.** An edit between two
  queries of one tick (more than 64 props) is seen at the next tick, or at
  once after `InvalidateScenery`.
- **Navigation:**
  - Box props are still not rasterised.
  - A watertight mesh's parity spans assume a column's faces pair up. A mesh
    that is closed by edge count but self-intersecting can mis-span a column.
- **Cook determinism.** Two cooks of the same `.obj` gave 286 and 288
  clusters. A glTF cooks identically every time, and the collision of each is
  deterministic. This looks like the OBJ/`.fmdl` import path (Phase 1/2 code)
  and was not chased.
- **Format bump.** `.fnm` is format 3 / cook version 4: every cooked mesh must
  be re-cooked (`foundry nanite cook -all`). The scratch bench copy's
  `bench_rock.fnm` was re-cooked; the old cook is kept beside it as
  `bench_rock.fnm.v2`. The user's own project files were not touched.
- **Another session was editing the working tree during this run** (setup,
  pack, release, app). Those files were not touched, and the full suites above
  ran with them in place.
