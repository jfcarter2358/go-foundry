# Handoff: sectors → Quake-style brushes

Branch: `feature/jc/brush-editing` (branched off `main`).
Approved plan: `/home/jfcarter2358/.claude/plans/wild-churning-lollipop.md`
(outside the repo — the decisions that matter are reproduced below, so this
document stands on its own).

**State: `go build ./...`, `go vet ./...` and `go test ./...` all pass.**
Phases 1–8 are done. Phase 9 (the compile pipeline) is one stage in. Phase 10
(cleanup and docs) is partial.

---

## What this is

The engine used to build levels out of **`Sector`s**: 2D floorplan polygons
owned by a `Chunk`, each with a floor/ceiling height field, per-edge walls or
portals, and a "single active sector" collision model that ranked overlapping
sectors by height.

It is being rewritten to build levels out of **`Brush`es**: convex polyhedra,
each face individually textured, edited in 3D the way the model editor edits a
mesh. Empty walkable space is whatever is *not* inside a brush.

This is a hard cutover. There is no dual-format support and no converter.

---

## Decisions already settled with the user — do not re-open these

1. **Hard cutover.** The sector loader and every existing level are gone. No
   migration path. Levels get rebuilt by hand as brush levels.
2. **Full BSP + PVS for release builds** (authentic id-software style), not
   just frustum culling. That is Phase 9.
3. **3D-only editing.** The 2D floorplan editor is deleted, not replaced with
   ortho viewports. Brushes are placed and shaped in the 3D view.
4. **Two run modes.** In-editor play evaluates brushes live/uncompiled every
   frame (simple, always in sync, pays overdraw and O(n) collision); release
   builds load only a precompiled level and never link the raw brush loader.
   Hook this into the existing `devtools_dev.go` / `devtools_release.go` split.
5. **Biome scatter becomes a Volume**, mirroring `Volume.Fog`. **Done** — see
   Phase 5 below.
6. **Prefabs** are live-linked by file reference. **Done** — see Phase 4.
7. **Baked lighting at compile time**, per-vertex (not lightmap atlases), so
   release builds skip most per-fragment light work for static geometry.
8. **Brushes are explicit convex polyhedra** (authored vertex + face list, like
   `ModelDoc`), *not* plane-based Quake `.map` brushes. This is what makes
   "edit it like the model editor" literal. Convexity is required and the
   editor is responsible for enforcing it (`Brush.IsConvex`).

---

## Phase status

| # | Phase | Status |
|---|---|---|
| 1 | Core data model + level schema | **done** |
| 2 | 3D brush editing tools | **done** |
| 3 | Collision | **done** |
| 4 | Prefabs | **done** |
| 5 | Volumes (frame doorway + scatter) | **done** |
| 6 | Streaming | done incidentally (chunk enable/reload walks brushes) |
| 7 | Rendering traversal | **done** |
| 8 | Scripting | **done** (scaled down — see regressions) |
| 9 | Compile pipeline (CSG + BSP + PVS + baked lighting) | **CSG done; the rest not started** |
| 10 | Cleanup / docs | partial — see below |

### Commits on this branch, migration only (oldest first)

```
9d92e1d  internal/world core data model
224e864  internal/collision against solid brush geometry
c37361c  internal/detail against BrushFace
b1aa87f  scripting addresses brushes, not sectors
2979d19  remove internal/biome pending its volume rebuild
24a2d90  render passes walk brush faces
9ad5128  level files carry brushes
9961c00  editor selection, picking and occlusion (partial)
9dbf841  the editor edits brushes
d632705  the editor's tests
6b32b7b  Phase 5 — frame doorways are volumes
f1f87cc  handoff update
a185ff4  Phase 4 — prefabs
40e3985  handoff update
6e966fc  prefab fixes (file cache, save-selection bug)
db1edef  Phase 5 — scatter is a volume
dfc101a  Phase 9 — CSG, the first compile stage
```

---

## The data model

`internal/world/brush.go` is the file to read first.

```go
type Brush struct {
    ID         string
    ScriptData            // tags, attributes, attached script
    Verts      []mgl32.Vec3   // chunk-local; every vert is on the convex hull
    Faces      []BrushFace
    Solid      bool           // false = decorative, walk-through
}

type BrushFace struct {
    Indices     []int      // into Brush.Verts, CCW seen from outside
    Texture, BlendTexture, BlendMask string
    UVOffset, UVScale mgl32.Vec2
    UVRotation, SmoothAngle float32
    Smooth      bool
    Detail      DetailSpec // procedural rock displacement + scatter
    Alpha       float32
    DoubleSided bool
    NoDraw      bool // invisible but still solid (a buried brush side)
    NoCollide   bool // visible but walk-through
    Geometry          gfx.Geometry   // runtime
    SubmeshGeometries []gfx.Geometry // runtime
}
```

Constructors: `NewBoxBrush`, `NewHollowRoom` (floor + ceiling + four walls — the
editor's "hollow" primitive and what test fixtures use, since a single
`NewBoxBrush` is a solid block with no interior).

`Chunk` keeps everything it had (`Origin/Rotation/Scale`, `FrameOwner`,
streaming `Disabled` flag, spatial-hash bucketing) but its `Sectors`, `Points`,
`PointNames`, `BiomeSectors` and `WaterSurfaces` are replaced by one field:
`Brushes []Brush`. There is **no shared point pool** — a sector's edges had to
share exact points for portals to line up; a brush owns its verts outright.

Brush IDs are **level-unique** (`world/identity.go`), so a brush is addressed
by a bare name everywhere — no chunk qualifier, unlike sector IDs.

`Volume` has grown three fields since the plan was written: `Frame` (the vehicle
it rides), `Doorway` (Phase 5) and `Scatter` (Phase 5).

---

## What each finished phase built, and why it is the way it is

### Phase 2 — the editing tools

The 2D floorplan editor is gone and with it `Mode2D`, the biome layer state, the
shared point pool, the chunk-point drag, the join tools and the layer stack.
`editorPanelBody` no longer takes a panel kind: there is one viewport, so every
branch on which one was asking is dead.

**Surface shaping is not a sub-mode.** A sector's face was flat by construction,
so shaping one meant editing a parametric `SurfaceMesh` riding on top of it in
`(u, v, depth)` coordinates — a second geometry model with its own selection
unit and its own editing state. A brush *is* a mesh, so the ordinary
point/edge/face selection modes already address exactly what an extrude acts on.
`editor_surface*.go` (~1,400 lines) went with the sub-mode.

The tools, in `editor_brush_tools.go` and `editor_brush_mesh.go`:

- **Stamps**: box, wedge, cylinder — plus **hollow** (replaces a solid with the
  six brushes of a room filling it) and **duplicate**.
- **Extrude (E)** slides one face along its own normal. Deliberately not a free
  direction: a bump on a wall is a *second brush*, the way it is in every
  id-lineage editor, because extruding sideways makes the solid concave.
- **Inset (I)** draws a face's corners toward its centre in its own plane,
  turning the solid into a frustum of itself. Still convex.
- **Subdivide (B)** fans a face into triangles meeting at a new centre vertex.
  A centre fan rather than a midpoint split, because a midpoint lands on a
  *shared* edge and leaves the neighbouring face with a T-junction to repair.

Each of the three preserves convexity outright. A **free vertex drag** is the
one edit that can break it, and `warnIfSelectionBrokeConvexity` reports rather
than refuses — an editor that will not let you pass through an invalid
intermediate state is one you cannot shape anything in. Phase 9's compiler is
where it should become an error.

Brush edges are found by walking each face's index loop and keeping each
undirected pair once, so the edge two faces share is one handle
(`editor_3d_edge_pick.go`).

`editor_sel_transform.go` collapsed: a brush vertex is a position, owned by one
brush, so the write is a write. No shared-point averaging, no per-surface height
offset, no `PointMove` for a biome layer to ride.

### Phase 4 — prefabs

A reusable group linked by file: `world/prefab.go` holds the description,
`levelio/prefabs.go` stamps it, `editor/editor_prefab.go` places and edits it.
Structurally it is entity chunk attachments applied to a *file* rather than to a
*type* — the two are the same idea, and `levelio/entity_chunks.go` is worth
reading alongside.

A materialised instance becomes ordinary level objects tagged `FromPrefab`, so
every downstream pass works on them unchanged. The one thing that must not
happen is the saver writing the expansion out, which would freeze a copy and
lose the link; that has two tests of its own.

Settled, do not re-open:

- **A prefab is not a chunk.** A chunk is a streaming and frame-ownership
  container — a property of *where* content is. A prefab is a property of *what*
  it is, and a level wants both.
- **The origin is the bottom centre** of what was selected, so a placement sits
  on the ground rather than half-buried.
- **Materialised last** in the load, so the level's own IDs win over a prefab's.
- **Scale reaches everything with a size** — extents and radii, not just
  geometry.
- **Brushes, entities, volumes and point lights only.** Nothing non-structural;
  scattering props is the scenery brush's job.

`LoadPrefab` caches by resolved PATH (not name — the install root moves under
tests). `SavePrefab` invalidates; so does the editor's explicit "Re-stamp from
file", which exists for a file changed on disk.

### Phase 5 — the two volume features

**Frame doorways** (`world/volume_doorway.go`, `collision/frame_portal.go`).
`Volume.Doorway` marks a volume as the boundary between two frames;
`Volume.Frame` is which vehicle it rides. A doorway is authored on the *moving*
side, so nothing has to be authored on the ground and a ship can land anywhere.

Of the two rules the old portal-edge doorway used, only **containment**
survives, and it is the better one: proximity to a line fires while the occupant
is still short of it, so both sides claim the same ground and a walker gets
traded back and forth once a tick unless a "crossings must go deeper" rule
arbitrates. Inside a volume and outside it cannot both be true. What is given up
is tunnelling protection — a doorway has to be deeper than one step.

**Scatter** (`world/volume_scatter.go`, `render/scatter.go`,
`render/scatter_mesh.go`, `internal/biome`). The old painted system did three
things; only the scatter came back:

- Ground patches are subsumed by `BrushFace.Detail` — the ground *is* geometry
  now, so a second draped mesh over it would z-fight.
- Water is **not replaced**. It needs a water volume, which is its own piece of
  work. Nothing in the engine currently draws water.

Two things changed and they are the whole difference from the painted version:
the survival test is the volume's own shape (box/sphere/cylinder, four uniforms)
instead of a ray-cast point-in-polygon against 64 uploaded points; and the
height texture is baked by casting rays DOWN through the level's brushes
(`internal/biome`) instead of looking up a sector's floor. Everything else — the
lattice, the three LOD tiers, the wind clock, the boundary jitter, the
three-program shadow plumbing — is unchanged, and the template meshes were never
the part that knew about polygons.

`RefBiome` was removed from `flang` rather than left as a hole: scatter is a
property of a Volume, so nothing addresses a biome as an object. Safe because a
ref kind is never serialised.

---

## Phase 9 — the compile pipeline. THIS IS THE NEXT WORK.

`internal/compile` is new. It is pure Go, GL-free and fully tested, and the rest
of the pipeline should keep that shape — the compiler is the one part of this
engine that can be checked without a window.

### What exists

**`plane.go`** — the primitive every later stage needs: `Plane`,
`SplitPolygon`, `ClipPolygon`, `ClassifyPolygon`, `PolygonArea`. CSG, the BSP
and portal generation are all the same operation with different bookkeeping, so
it is written once. Two decisions in it are load-bearing and must not be
"simplified":

- Classification is against `onPlaneEps`, not zero. Not defensive rounding: a
  vertex exactly on a splitting plane classifies as a hair in front on one pass
  and a hair behind on the next, and a BSP that keeps re-splitting a polygon it
  has already split **does not terminate**.
- A crossing edge's intersection point is computed ONCE and shared by both
  halves. Computing it per half is the classic way to make a hairline crack that
  only shows at certain camera angles.

**`csg.go`** — `CSG(lvl) []Face` clips every solid brush face against every
other solid brush and returns what sticks out. `Face` is world-space and carries
its own points (not indices into a brush), because a compiled face may be one
fragment of several and the source brush will not exist at run time.

The interesting cases are surfaces that TOUCH rather than overlap, which is most
of what a blocked-out level is. See `clipPlanesFor`'s doc comment — especially
the third case, where the coplanar loser is clipped against the solid's *other*
planes with the shared one **left out**. That is not an optimisation; clipping
against all of them keeps the face whole (a polygon on a plane reads as in front
of it) and dropping it whole is wrong the other way.

Frame-owned chunks are left out entirely: a vehicle's interior moves, so it
cannot be baked into a static world-space list — and it must not clip the static
world either, or a parked ship carves a hole in the ground under it.

### What is left, in order

1. **BSP** (`bsp.go`). Build a binary space partition over the compiled faces'
   planes; leaves are convex cells. Pick splitters by the usual balance-versus-
   splits heuristic. `Plane.ClassifyPolygon` and `SplitPolygon` already do the
   per-node work; the new part is the plane choice and the leaf bookkeeping.
   Watch for: a coplanar face belongs to the NODE, not to either child.

2. **Portals + PVS** (`pvs.go`). Generate portals by clipping each leaf's
   boundary against its neighbours, then flood-fill visibility leaf-to-leaf. The
   output is a bit array, one row per leaf. This is the expensive stage; a
   naive implementation is fine to start, and the test should be a small level
   whose answer can be reasoned about by hand.

3. **Light bake** (`light.go`). Per-vertex, not lightmap atlases (decision 7).
   For each compiled face vertex, sum the level's lights with a shadow ray
   against the compiled faces. `Face.Light` is already declared and parallel to
   `Face.Points`, waiting to be filled.

4. **The compiled format** (`internal/levelio`, probably `compiled_level.go`).
   Holds: the post-CSG faces with their baked light, the BSP tree, the PVS bit
   array, and everything level-scoped that is not geometry (entities, volumes,
   lights, spawn, scripts, UI — those can be the existing raw forms). Needs a
   version stamp.

5. **The dev/release split** (decision 4). `internal/app/devtools_dev.go` and
   `devtools_release.go` are the existing seam and are deliberately thin
   wrappers — read them first. A release build should load only the compiled
   format and never link the raw brush loader. `TestTheEditorDoesNotStandUpThe
   WorldPasses` in `internal/app` is the shape of the assertion that would
   guard it.

6. **Invoking it.** An editor button (compile the open level) and probably a
   `cmd/` entry point for a build step.

### One thing the compiler should also do

`Brush.IsConvex` exists and the editor only *warns* when a drag breaks it. The
compiler is where it should be an **error** — a concave brush makes CSG's
"inside is behind every plane" rule wrong, and both collision and the BSP depend
on it. That check belongs at the top of `CSG`.

---

## Known regressions — all marked `TODO(brush migration` in the source

Grep for `TODO(brush migration` to find them. Summary:

- **Scripts cannot move geometry.** `brushProps` exposes only `solid`. The
  shipped `resources/scripts/button.flang` example drives
  `@target.floor.prop.height` and is broken; the lift/animated-floor tests were
  removed with the property. Brush faces have no ID, so there is no `RefFace`
  and no per-face texture write from script either.
- **Water is gone.** The Water biome kind auto-managed a companion plane;
  nothing replaces it. `WaterParamsFromRaw` and the depth helpers survive in
  `world/water_surface.go`, and `gfx.WaterParams` is intact.
- **All shipped levels deleted** (`resources/levels/`, `resources/chunks/`) per
  decision 1. There is nothing to load — the "New Level" path writes a chunk
  holding one hollow room and that is the only content that exists. This is also
  why nothing places the shipped dropship entity, which is otherwise intact and
  whose boarding works again.

---

## Gotchas learned the hard way

### Collision

1. **GJK/EPA converges to a tolerance, not to zero.** Pushing a body out by
   exactly the reported penetration depth leaves a hairline of residual
   penetration that reports as still-touching next frame. `pushOutOfChunkBrushes`
   adds a `separationEpsilon` (0.001) past the reported depth.
2. **A fast substep's raw target can land outside the chunk's AABB.** Brush
   chunks are tightly fitted where sector chunks were huge, so a body moving
   fast enough to try to push through an outer wall would look up "which chunk
   am I in", get nil, and skip collision entirely. Both `MovePlayer` and
   `resolveEntityWorld` fall back to the chunk the body was already in.
3. **Body shape for world collision is a `Cylinder`, not a `Box`.** Matches what
   the old wall resolver did, so corners don't catch on walls. Don't "fix" this
   without playtesting.
4. **`Cylinder`/`OBB`/`Ellipsoid` need `Rot: mgl32.Ident4()` set explicitly.**
   The zero value of `mgl32.Mat4` is the *zero* matrix, and `rotateInv` against
   it returns a zero vector — every support query silently returns the same
   point.
5. **`NewBoxBrush` is a solid block, not a room.** Fixtures that want somewhere
   to stand need `NewHollowRoom`.
6. **Set `Chunk.ID` in fixtures.** An empty chunk ID makes `p.CurrentChunk` come
   back `""` even when the lookup succeeded.
7. **Sealed ceilings are real now.** A brush roof is solid geometry, so falling
   onto a sealed room from outside lands you on the roof.

### Geometry and the compiler

8. **An axis-facing plane's `D` is signed by its normal.** A box's `-X` face at
   x=-1 and its `+X` face at x=+1 both have D=1. Test the x-intercept
   (`D / N.X()`), not D. This cost a wrong test in the CSG work.
9. **A face resting flush on another is the coplanar case, not the cutting
   case.** A pillar standing exactly on a floor does not cut the floor — the two
   coplanar surfaces resolve to one whole survivor. Only a pillar *sunk into*
   the floor cuts it. Both have tests saying so.

### Editor

10. **`editorVisReset()` before an occlusion assertion.** The visibility cache is
    keyed on the selection, so two tests asking about different world positions
    must ask about different corners or the second reads the first's answer.
11. **A vertex on the NEAR face is the self-occlusion case.** A corner on the
    far side of a brush is genuinely behind its near face and correctly hidden.

---

## Working conventions on this branch

- **Commit per package or per phase**, with the package building and its tests
  green before moving on. Commit messages explain *why* something went, not just
  what.
- **Delete tests for deleted behaviour** rather than leaving them red. Where a
  *test* is what would go red for deferred work, put a `TODO(brush migration,
  Phase N)` marker at the assertion and narrow it rather than deleting it — see
  the history of `app/world_passes_test.go`, which lists the passes a build must
  stand up.
- **Audit deletions by content, not filename.** Several files were nearly lost
  this way — `attach.go` (sprite/emitter frame-riding), `detail_test.go`,
  `water_test.go`. `activeChunk()` and `distToSegment2D` *were* lost with
  `editor_biome.go` and `editor_3d_edge_pick.go` and had to be recovered from
  git; both were generic helpers that happened to live in a doomed file.
- **Prefer deleting a rule to porting it.** Several things in the sector model
  existed only to paper over the sector model: the surface-shaping sub-mode, the
  crossing-hysteresis rule, the floor/ceiling ring halving, the shared-point
  averaging in the vertex write-back. Each was correct, and each had nothing left
  to be correct *about*.
- **Stage explicit paths, never `git add -A`.** See below.

---

## Unresolved: something deleted files from the working tree

`plan.md`, `river.yaml`, `render.test` and `SETTINGS_LAYOUT.md` are present in
`HEAD` but missing from the working tree, and nothing in this migration removed
them. Several other agent sessions were running against this repo at the same
time, which is the likely cause.

Still worth confirming with the user before anyone commits a sweeping
`git add -A` — every commit on this branch has staged explicit paths for that
reason.

---

## Phase 10 — cleanup and docs, what is left

`docs/guide/README.md` carries a `TODO(brush migration, Phase 10)` note saying
plainly that the sector-era chapters are stale and which have been rewritten.
Rewritten so far: **06-scatter** (was 06-biomes), **12-vehicles-and-frames**,
**22-prefabs** (new), and the two troubleshooting entries in
**19-testing-and-debugging**.

Still describing sectors, walls, portals, the 2D map and the surface-shaping
sub-mode: **01-editor-basics**, **02-building-levels** (the big one — it is the
chapter about the format that no longer exists), **03-shaping-surfaces** (should
probably be folded into 02, since shaping is no longer a mode),
**05-detail-and-smoothing**, **07-lighting**, **18-scripting** (the `@sec.edge`
addressing is gone), **21-keybindings**.

`resources/entities/README.md` has a `TODO(brush migration, Phase 10)` pointing
at the deleted dropship level.
