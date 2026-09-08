# Handoff: sectors → Quake-style brushes

Branch: `feature/jc/brush-editing` (branched off `main`, 11 commits in).
Approved plan: `/home/jfcarter2358/.claude/plans/wild-churning-lollipop.md`
(outside the repo — the decisions that matter are reproduced below, so this
document stands on its own).

> **Note:** this file replaces an older, unrelated `HANDOFF.md` (a completed
> "live chunk transforms" effort). The old content is in git history at
> `git show 6e4c4be:HANDOFF.md` if anyone wants it.

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
   just frustum culling. That is Phase 9 and has not been started.
3. **3D-only editing.** The 2D floorplan editor is deleted, not replaced with
   ortho viewports. Brushes are placed and shaped in the 3D view.
4. **Two run modes.** In-editor play evaluates brushes live/uncompiled every
   frame (simple, always in sync, pays overdraw and O(n) collision); release
   builds load only a precompiled level and never link the raw brush loader.
   Hook this into the existing `devtools_dev.go` / `devtools_release.go` split.
5. **Biome scatter becomes a Volume**, mirroring `Volume.Fog` — placed and
   sized like a fog volume rather than painted as a polygon on a floor.
6. **Prefabs** (reusable groups) are **live-linked by file reference**, like
   `Scenery.Model` points at an `.fmdl`. Editing the source updates every
   placed instance. They are called **Prefab**, deliberately *not* "chunk" —
   `Chunk` keeps its existing meaning (streaming + frame-ownership container).
   A prefab contains brushes, entities, volumes and point lights.
7. **Baked lighting at compile time**, per-vertex (not lightmap atlases), so
   release builds skip most per-fragment light work for static geometry.
8. **Brushes are explicit convex polyhedra** (authored vertex + face list, like
   `ModelDoc`), *not* plane-based Quake `.map` brushes. This is what makes
   "edit it like the model editor" literal. Convexity is required and the
   editor is responsible for enforcing it (`Brush.IsConvex`).

---

## Current state

`go build ./...` and `go test ./...` both pass. Every package builds and every
test is green.

### Commits (oldest first)

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
```

## The data model, as built

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
    Detail      DetailSpec // procedural rock displacement, unchanged from before
    Alpha       float32
    DoubleSided bool
    NoDraw      bool // invisible but still solid (a buried brush side)
    NoCollide   bool // visible but walk-through
    Geometry          gfx.Geometry   // runtime
    SubmeshGeometries []gfx.Geometry // runtime
}
```

Useful constructors already there: `NewBoxBrush`, `NewHollowRoom` (floor +
ceiling + four walls — the editor's future "hollow" primitive, and what test
fixtures use, since a single `NewBoxBrush` is a solid block with no interior).

`Chunk` keeps everything it had (`Origin/Rotation/Scale`, `FrameOwner`,
streaming `Disabled` flag, spatial-hash bucketing) but its `Sectors`, `Points`,
`PointNames`, `BiomeSectors` and `WaterSurfaces` are replaced by one field:
`Brushes []Brush`. There is **no shared point pool** — a sector's edges had to
share exact points for portals to line up; a brush owns its verts outright.

Brush IDs are **level-unique** (`world/identity.go`), so a brush is addressed
by a bare name everywhere — no chunk qualifier, unlike sector IDs.

---

## What each phase means, and where it stands

The plan's phase numbering, with real status:

| # | Phase | Status |
|---|---|---|
| 1 | Core data model + level schema | **done** |
| 2 | 3D brush editing tools | **done** |
| 3 | Collision | **done** |
| 4 | Prefabs | not started |
| 5 | Volumes | **half** — frame doorway done; biome scatter not started |
| 6 | Streaming | done incidentally (chunk enable/reload walks brushes) |
| 7 | Rendering traversal | **done** |
| 8 | Scripting | **done** (scaled down — see regressions) |
| 9 | Compile pipeline (CSG + BSP + PVS + baked lighting) | not started |
| 10 | Cleanup / docs | partial — see below |

---

## What the editor does now (Phase 2, as built)

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
  six brushes of a room filling it) and **duplicate**. Convex by construction.
- **Extrude (E)** slides one face along its own normal. Deliberately not a free
  direction: a bump on a wall is a *second brush*, the way it is in every
  id-lineage editor, because extruding sideways makes the solid concave.
- **Inset (I)** draws a face's corners toward its centre in its own plane,
  turning the solid into a frustum of itself. Still convex.
- **Subdivide (B)** fans a face into triangles meeting at a new centre vertex.
  A centre fan rather than a midpoint split, because a midpoint lands on a
  *shared* edge and leaves the neighbouring face with a T-junction to repair.

Each of the three preserves convexity outright. A **free vertex drag** is the
one edit that genuinely can break it, and `warnIfSelectionBrokeConvexity`
reports rather than refuses — an editor that will not let you pass through an
invalid intermediate state is one you cannot shape anything in. Phase 9's
compiler is where it becomes an error.

Brush edges are found by walking each face's index loop and keeping each
undirected pair once, so the edge two faces share is one handle
(`editor_3d_edge_pick.go`). The sector's floor/ceiling/vertical triple was an
artifact of a 2D polygon with a height field.

`editor_sel_transform.go` collapsed: a brush vertex is a position, owned by one
brush, so the write is a write. No shared-point averaging, no per-surface height
offset, no `PointMove` for the biome layer to ride.

---

## What Phase 5 has so far

**Frame doorways are done.** `Volume.Doorway` (`world/volume_doorway.go`) marks
a volume as the boundary between two frames, and `Volume.Frame` is which vehicle
the volume rides. A doorway is authored on the *moving* side, because the ship
knows where its own ramp is and nothing has to be authored on the ground —
which is what lets a ship land where the pilot puts it.

Of the two rules the old portal-edge doorway was resolved by, only **containment**
survives, and it is the better one. Proximity to an authored line fires while
the occupant is still short of it, so both sides claim the same ground and a
walker in the overlap is traded back and forth once a tick (the screen strobing
between the two interiors); the old file carried a whole "a crossing must go
deeper than the way back" function to arbitrate that. Inside a volume and
outside it cannot both be true, so that failure has no way to happen and the
arbitration is gone. What is given up is tunnelling protection — a doorway has
to be deeper than one step of whatever walks through it.

**Biome scatter as a volume is not started.** That is the bigger half: the
render pass, the ground patches, the scatter caches and the `@biome` script
surface all went with `internal/biome`. See the regressions below.

## Known regressions — all marked `TODO(brush migration` in the source

Grep for `TODO(brush migration` to find them. Summary:

- **Biome scatter is gone entirely** — `internal/biome`, `render/grass.go`,
  `render/biome_ground.go`, the paint editor, and the `@biome` script surface.
  `render/biome_light.go` was deliberately **kept**: its shader-composition
  helpers are shared with sprites and the detail passes.
- **Scripts cannot move geometry.** `brushProps` exposes only `solid`. The
  shipped `resources/scripts/button.flang` example drives
  `@target.floor.prop.height` and is now broken; the lift/animated-floor tests
  were removed with the property. Brush faces have no ID, so there is no
  `RefFace` and no per-face texture write from script either.
- **All shipped levels deleted** (`resources/levels/`, `resources/chunks/`) per
  decision 1. There is nothing to load — the "New Level" path writes a hollow
  8×8×4 room and that is the only content that exists.
- **Water surfaces** are gone with biome (`WaterParamsFromRaw` and the depth
  helpers survive in `world/water_surface.go`).
- **The F1 reference lists no `biome` kind** and `render.InitWorldPasses` no
  longer calls `GrassInit`. Both are asserted-on-absence rather than left red:
  see the `TODO(brush migration, Phase 5)` markers in
  `app/script_reference.go` and `app/world_passes_test.go`. Put both back with
  the pass.
- **Nothing places the dropship.** The entity type is intact and boarding works
  again, but `resources/levels/dropship_demo.yaml` went with every other level.
  Marked in `resources/entities/README.md`.

*(Frame-portal boarding was on this list and is fixed — see Phase 5 above.)*

---

## Gotchas learned the hard way — read before touching collision

1. **GJK/EPA converges to a tolerance, not to zero.** Pushing a body out by
   exactly the reported penetration depth leaves a hairline of residual
   penetration that reports as still-touching next frame. `pushOutOfChunkBrushes`
   adds a `separationEpsilon` (0.001) past the reported depth. The old wall
   resolver had the same epsilon for the same reason. Several tests asserting
   bit-exact rest positions had to be widened.
2. **A fast substep's raw target can land outside the chunk's AABB.** Brush
   chunks are tightly fitted where sector chunks were huge, so a body moving
   fast enough to try to push through an outer wall would look up "which chunk
   am I in", get nil, and skip collision entirely for that substep — sailing
   through. Both `MovePlayer` and `resolveEntityWorld` now fall back to the
   chunk the body was *already standing in*.
3. **Body shape for world collision is a `Cylinder`, not a `Box`.** That
   matches what the old wall resolver did (it used
   `radius = max(BoundHalfX, BoundHalfZ)` against wall segments), so corners
   don't catch on walls. Don't "fix" this to a box without playtesting.
4. **`Cylinder`/`OBB`/`Ellipsoid` need `Rot: mgl32.Ident4()` set explicitly.**
   The zero value of `mgl32.Mat4` is the *zero* matrix, not the identity, and
   `rotateInv` against it returns a zero vector — every support query silently
   returns the same point.
5. **`NewBoxBrush` is a solid block, not a room.** Test fixtures that want
   somewhere to stand need `NewHollowRoom`. A fixture using a solid box for a
   "room" will have a player resting on its outside.
6. **Set `Chunk.ID` in fixtures.** An empty chunk ID makes `p.CurrentChunk`
   come back `""` even when the lookup succeeded, which reads as a lookup bug
   and is not one.
7. **Sealed ceilings are real now.** A brush roof is solid geometry, so falling
   onto a sealed room from outside lands you on the roof. The old sector
   ceiling was a math plane that a falling body leaked through after one frame.
   A test that dropped a player from y=400 into a sealed room was relying on
   that quirk.

---

## Working conventions in this branch

- **Commit per package**, with the package building and its tests green before
  moving on. Commit messages explain *why* something went, not just what.
- **Delete tests for deleted behaviour** rather than leaving them red. Several
  test files were removed wholesale because their subject (openings, slabs,
  surface meshes, portal culling, sector enter/exit events) no longer exists.
- **Audit deletions by content, not filename.** Three files were nearly lost
  this way — `attach.go` (sprite/emitter frame-riding, nothing to do with
  sectors), `detail_test.go` and `water_test.go` (both generic). Check what a
  file actually does before removing it because its name looks sector-ish.
- Keep the `TODO(brush migration, Phase N)` marker format — it is how the
  deferred work is tracked. Where a *test* is what would go red, put the marker
  at the assertion and narrow it rather than deleting the test — see
  `app/world_passes_test.go`, which lists the passes a build must stand up and
  currently names three of four.
- **Prefer deleting a rule to porting it.** Several things in the sector model
  existed only to paper over the sector model: the surface-shaping sub-mode, the
  crossing-hysteresis rule, the floor/ceiling ring halving, the shared-point
  averaging in the vertex write-back. Each was correct, and each had nothing
  left to be correct *about*. The commit messages say which and why, because
  "where did X go" is the question a reader of this branch will have.

---

## Unrelated: something deleted files from the working tree

`plan.md`, `river.yaml`, `render.test` and `SETTINGS_LAYOUT.md` are present in
`HEAD` but missing from the working tree, and nothing in this migration removed
them (all deletions here were under `internal/`, `docs/` and
`resources/levels|chunks`). Several other agent sessions were running against
this repo at the same time, which is the likely cause. Still unresolved, and
still worth confirming with the user before anyone commits a sweeping
`git add -A` — every commit on this branch has staged explicit paths for that
reason.

(`HANDOFF.md` was on that list and has since been restored and rewritten.)
