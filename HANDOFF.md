# Handoff: sectors → Quake-style brushes

Branch: `feature/jc/brush-editing` (branched off `main`).
Approved plan: `/home/jfcarter2358/.claude/plans/wild-churning-lollipop.md`
(outside the repo — the decisions that matter are reproduced below, so this
document stands on its own).

**State: `go build ./...`, `go vet ./...`, `go test ./...`,
`go build -tags release ./...` and `make release-check` all pass.**
Phases 1–8 are done. Phase 9's COMPILER is finished — every stage, the file
format, and both ways of running it. What is not started is the RUNTIME that
consumes the output, which turns out to be bigger than the original plan implied;
see "Phase 9b" below. Phase 10 (cleanup and docs) is partial.

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
| 9a | Compile pipeline (CSG + BSP + PVS + baked lighting), the format, and running it | **done** |
| 9b | The runtime that loads and draws a compiled level | **not started — read below before planning it** |
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
9d1abce  remove old docs
3043ef3  Phase 9 — the BSP, and convexity as an error
4857cbe  Phase 9 — portals, the void fill, and the PVS
f738f18  Phase 9 — baked per-vertex lighting
15ab724  Phase 9 — the pipeline, and the compiled format
e003ba3  Phase 9 — running the compiler
eee146a  docs: compiling levels
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

## Phase 9a — the compiler. DONE.

`internal/compile` is pure Go, GL-free and fully tested, and the rest of the
pipeline kept that shape — the compiler is the one part of this engine that can
be checked without a window.

`compile.Compile(lvl, opt)` runs every stage. The order is not a choice among
several that would work:

1. **Validate** (`validate.go`) — convexity is a WARNING in the editor and an
   ERROR here. "Inside is behind every plane" is simply false for a concave
   brush and everything below inherits the wrong answer. All problems are
   reported at once, sorted, so an author sees the whole list.
2. **CSG** (`csg.go`) — clips every solid face against every other solid brush.
   Unchanged from the previous session except that it returns an error now.
3. **BSP** (`bsp.go`) — cuts the world into convex cells with the faces' own
   planes. Solidity comes from the face normals: CSG leaves every face pointing
   out of its solid, so a splitter's back child is material and its front child
   is air, and a region that runs out of faces is whichever it last was. A
   coplanar face belongs to the NODE; node faces are attached to the empty
   leaves they bound afterwards, which is what gives each cell its draw list.
4. **Visibility** (`pvs.go`, `outside.go`, `region.go`) — portals, the void
   fill, then the flood. See below; this is where the hard-won knowledge is.
5. **Light** (`light.go`) — LAST, because it subdivides faces and so renumbers
   them, which would invalidate the cell face lists visibility had just built.

`internal/levelio/compiled_level.go` is the artefact: gob, magic header, version
stamp, `.fbsp` beside the `.yaml`. `CompileLevel` (a live level) and
`CompileLevelFile` (headless, from disk) are the two ways in.

Running it: the editor's **Compile** button (`editor/editor_compile.go`) and
`cmd/foundrycompile`. The CLI goes through the real loader with its uploads
switched off (`levelio.LoadLevelHeadless`) rather than growing a second reading
of the same file — so a level that compiles is a level that loads.

### The three things about visibility that are load-bearing

Do not "simplify" any of these; each was arrived at by watching the obvious
version fail.

1. **The void around the level MUST be filled in.** The tree partitions a box
   sixteen units bigger than the level, so most of what it produces is cells in
   the space *around* it — and that exterior is one enormous region carved into
   dozens of cells that all see each other through portals the clipping never
   narrows. Two hollow rooms took **53 seconds**. Flooding in from the box
   corners and labelling what it reaches as `Outside` (exactly qbsp's
   `FillOutside`) takes the same level to nothing. This is what makes SEALING a
   requirement, and `BSPTree.LeaksAt` is how a leak is reported.

2. **The bookkeeping is over PORTALS, not cells.** A sight line is characterised
   by the apertures it passed through, and it is the shrinking set of still
   reachable apertures that lets a branch be abandoned. Mark cells alone and the
   search enumerates every distinct PATH through the graph. Portals are flooded
   cheapest-first so a finished portal's exact set bounds the later floods —
   that is Quake's `PortalFlow` and it matters.

3. **The forward test must demand a STRICTLY forward part.** A target merely
   touching the source portal's plane is coplanar with it, and two apertures in
   one plane see each other edge-on, which is to say not at all. This is not an
   exotic case: a **U-bend has both its corners on the same plane**, and
   accepting the coplanar reading makes the two ends of the U visible to each
   other straight through the block between them. `uLevel` in `pvs_test.go` is
   that fixture and `TestABendBlocksSight` is the assertion.

The depth cap and step budget fall back to plain portal reachability rather than
to silence, so they cost draws and never holes.

### On testing visibility

Two facts make PVS fixtures much harder to write than they look, and both cost
time to rediscover:

- **Two portals on the boundary of one convex cell always see each other.** So a
  three-cell chain is always fully mutually visible, however bent. A fixture
  that proves anything needs FOUR cells, or a bend whose two apertures are
  coplanar (which is what `uLevel` uses).
- **Cells are large.** A room with a corridor off it becomes two or three cells,
  not a grid of them, so "room A cannot see room B" is usually FALSE and
  correctly so. Assert on the pairs you have actually reasoned about.

### Phase 9b — the runtime. NOT STARTED. Read this first.

The original plan said "a release build should load only the compiled format and
never link the raw brush loader". That understates it considerably, and the
sequencing question is real:

- **Rendering.** `internal/app/app.go`'s world loop walks
  `Chunk -> Brush -> BrushFace.Geometry`. A compiled level has no brushes. The
  bridge is not hard — `world.BuildBrushFaceMesh(pts, doubleSided)` takes a
  polygon and is exactly what a compiled `Face` needs — but it wants a new
  runtime type (compiled faces + the tree + the PVS) and a draw path that walks
  the camera's PVS row instead of the chunk list.
- **Baked light needs a shader path.** `gfx.Geometry` vertices are `[X,Y,Z,U,V]`;
  the baked value is a third attribute plus a `fragment.glsl` branch that uses it
  instead of the light loop. Until that exists, a compiled level can still be
  drawn — it just re-lights per fragment, so CSG and PVS pay off and the bake
  does not.
- **The shadow and detail passes** (`render/shadow.go`, `render/detail_render.go`)
  walk chunks and brushes too.
- **Collision is the real problem.** `internal/collision` runs GJK/EPA against
  brush geometry, and a compiled level has none. Either the brushes are kept
  (which defeats the point) or collision gets a BSP path — `BSPTree.LineOfSight`
  and `FindLeaf` are already there and are the start of one, but a swept body
  against a solid BSP is its own piece of work. **This is the thing to design
  first**, because it decides whether the compiled format needs to carry the
  brushes after all.

A reasonable first cut, if the whole of that is too much at once: load the
compiled level for RENDERING and keep the raw chunks for collision, in a
development build, to prove the geometry and the culling. Then decide about
collision with something on screen.

`internal/app/devtools_dev.go` / `devtools_release.go` are the existing seam and
are deliberately thin wrappers — read them first.
`TestTheEditorDoesNotStandUpTheWorldPasses` in `internal/app` is the shape of the
assertion that would guard the split, and `make release-check` is the other half.

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
10. **Faces are subdivided on the WORLD grid, not on their own bounds.** Two
    faces meeting at an edge have to be cut at the same coordinates along it.
    Cut each on its own extent and the shared edge gets vertices on one side
    with no counterpart on the other, and every one of those is a visible seam
    once the two sides interpolate different baked light.
11. **`_ "image/png"` belongs in `internal/gfx`, not in a main.** It was in
    `internal/app`, so the first binary that read a texture without going
    through app — the level compiler — got "image: unknown format" from a
    perfectly good PNG. Moved.

### Editor

12. **`editorVisReset()` before an occlusion assertion.** The visibility cache is
    keyed on the selection, so two tests asking about different world positions
    must ask about different corners or the second reads the first's answer.
13. **A vertex on the NEAR face is the self-occlusion case.** A corner on the
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
**22-prefabs** (new), **23-compiling-levels** (new — the sealing rule lives
nowhere else, so read it before touching the compiler), and the two
troubleshooting entries in **19-testing-and-debugging**.

Still describing sectors, walls, portals, the 2D map and the surface-shaping
sub-mode: **01-editor-basics**, **02-building-levels** (the big one — it is the
chapter about the format that no longer exists), **03-shaping-surfaces** (should
probably be folded into 02, since shaping is no longer a mode),
**05-detail-and-smoothing**, **07-lighting**, **18-scripting** (the `@sec.edge`
addressing is gone), **21-keybindings**.

`resources/entities/README.md` has a `TODO(brush migration, Phase 10)` pointing
at the deleted dropship level.
