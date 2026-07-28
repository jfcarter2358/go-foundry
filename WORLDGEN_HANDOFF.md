# Handoff: Non-grid procedural city carved into the terrain grid

## Your mandate (read this first)

Implement the procedural world builder's **city generation** so that roads and
buildings are **NOT grid-aligned** — curved/diagonal roads and freely-rotated
buildings — while the terrain **remains a square-cell grid that you subdivide as
needed** so every piece of the city is a real, seamless terrain sector.

This is a **complete implementation task**. Do **all** of it, straight through:

- **No shortcuts.** Do not stub, fake, or approximate away the hard geometry.
- **No "out of scope" / "v1 / later" deferrals.** Every requirement below must
  actually work in the running app.
- **No overlay primitives to fake geometry.** Previous attempts that floated
  road/wall boxes *on top* of the terrain were rejected: they floated, z-fought,
  and flickered. The city must be the terrain sectors themselves.
- It must **compile and run**: `go build`, `go vet ./...`, `make linux` all
  clean, and the behaviors verified in-app (see Verification).

If something seems impossible in the current engine, your job is to extend the
engine until it is possible — not to declare it out of scope.

---

## Hard requirements (the full set, gathered across the project)

1. **Terrain stays a grid** of square cells (the existing `WorldGrid` →
   cell-sector bake). Keep brush painting (raise/lower/smooth/flatten, paint
   terrain type, paint city, paint road, erase) working exactly as now.
2. **Roads are non-grid**: painted road cells form **smooth, continuous,
   curved/diagonal** roads. The road surface IS terrain geometry (the carved
   sectors' floors, retextured), flush with the ground. No floating decks, no
   flicker. Roads must not change terrain height (no trenching) and must block
   city placement (already implemented in the brush).
3. **Buildings are non-grid**: freely-placed, **arbitrarily rotated** footprints
   (not locked to cell squares), **carved into the terrain grid** by
   subdividing the cells they overlap. Buildings sit **flush on the terrain**
   (never floating). Desert theme, **1–3 stories**, **large enough to walk into**,
   with **design variation** (varied footprints/sizes/heights, not identical
   blocks).
4. **Buildings are real sectors with walls and a ceiling** (enterable rooms),
   not decorative primitives. A door (walkable opening) on a road-facing side.
5. **Walls and ceilings have real thickness** — not infinitely thin single
   planes. This must be grid-native geometry (see "Thick walls" below), NOT
   overlay boxes.
6. **Seamless**: no cracks/T-junctions between subdivided sectors, no z-fighting,
   no gaps where a wall meets the ground on a slope.
7. **Manual edits persist across save/reload** (the level already saves full
   world-chunk geometry; keep that intact — see Persistence).
8. **Deterministic** from `WorldGrid.Seed` (same paint + seed ⇒ same city).
   "Generate City" re-rolls/regenerates; "Reseed" changes the seed.

---

## Where the code lives (current state)

The world builder is editor mode `ModeWorld`. Relevant files:

- `world_grid.go` — `WorldGrid` (the editable substrate) + accessors.
- `world_bake.go` — `BakeChunk`, `makeCellSector`, `RebakeCells`,
  `updateChunkBounds` (builds the terrain grid of cell sectors).
- `worldgen_city.go` — `GenerateCity`, `BuildingStyle`/`BuildingStyles`,
  building/road generation. **This is where most of your work goes.**
- `editor_world.go` — brush input/tick/render, `WorldEditorPanel`, "New World"
  dialog, `createWorld`, `generateCityForActive`.
- `editor.go` — `ModeWorld` enum + `EditorState.World*` fields + dispatch in
  `EditorHandleEvent`/`EditorTick`/`EditorRender` + `EditorInit` defaults.
- `editor_ui.go` — "World" toolbar button + panel dispatch.

Supporting engine (reuse these — do not reinvent):

- `chunk.go` — `Chunk` (has `Points []mgl32.Vec2`, `Sectors []Sector`,
  `Primitives`, `WorldGrid *WorldGrid`). `InferPortals`, `InferPortalsAndReload`,
  `RebuildGaps`, `Clone`.
- `sector.go` — `Sector` (`PointIndices []int`, `EdgeKinds []string`, `Walls`,
  `Portals`, `Floor`, `Ceiling`), `Floor`/`Ceiling` (`Height`, `VertexOffsets`,
  `VertexHeight(i)`, `Texture`, `Terrain`, `Alpha`), `SectorPoints(ch,s)`,
  `Sector.Load(points)` (uploads floor/ceiling/wall geometry; floor is a
  **triangle fan** over `PointIndices`, so sectors MUST be convex).
- `wall.go` — `Geometry` (`Load`, `Delete`, `Draw`), `Wall`
  (`StartPoint/EndPoint`, `Texture`, `Collision`, `Visible`, `Pinned`,
  `DoubleSided`, `Alpha`, `Extrusions`), `Extrusion`, `ExtrusionPoint`
  (wall U = distance along wall, V = distance **down from ceiling**).
- `extrusion.go` — **reuse for triangulation**: `delaunayTriangulate`,
  `earClip`, `earClipWithHoles`, `signedArea2D_UV`, `buildWallWithExtrusions`
  (cuts wall holes; auto-normalizes hole winding), `wallLocalVertex`.
- `level.go` — `Raw*` structs incl. `RawWorldGrid`; `loadRawChunk`,
  `worldGridFromRaw`, `LoadLevel`, `signedArea2D` (CCW > 0), `buildGapQuad`,
  `BuildSpatialHash`.
- `editor_save.go` — `chunkToRaw` (saves world chunk full geometry + grid
  block), `primitiveToRaw` (saves rotation), `saveExtrusions`.
- `collision.go` — `FindChunkAndSector`, `FindSector`, `pointInPolygon`,
  `InterpolateSurfaceHeight`, `ResolveWallCollision`, `resolveWallsForSector`,
  `ResolveFloorCollision`, `ResolvePrimitiveCollision`, `playerInWallExtrusion`,
  `getAdjacentSectors`.
- `spatial.go` — `BuildSpatialHash`, `FindChunkAndSectorSH`.
- `main.go` — render loop (per-sector frustum cull ~L450, portal cull ~L440),
  `ScanTextures`, `textureNames`, `LoadTexture`.

### WorldGrid (already implemented — use it)

```
Cols, Rows int; CellSize float32; Origin mgl32.Vec2; Seed int64
Heights  []float32           // (Cols+1)*(Rows+1), row-major, idx = vy*(Cols+1)+vx
CellType []WorldTerrainType  // Cols*Rows; TerrainGeneral/Road/River/Hill
CellCity []int32             // 0 = none, >0 = city region id
StyleName string
```
Accessors: `vIdx/cIdx/vertexWorld/cellCorners/cellCornerHeights/cellCenterWorld/
cellWorldRect/HeightAt(bilinear)/worldToVertex/terrainTexture`.

---

## CRITICAL engine constraints (hard-won — violating these is what broke prior attempts)

1. **Sectors must be CONVEX.** Floor rendering is a triangle fan over
   `PointIndices`; collision/PIP assume convex. When you subdivide a cell,
   output **triangles** (or convex polygons) only.
2. **Winding is CCW viewed from above** (`signedArea2D(points) > 0`, level.go).
   Floors face up only when CCW. `loadRawChunk` auto-reverses CW sectors, but
   build them CCW to begin with. There's also `signedArea2D_UV` in extrusion.go.
3. **Collision resolves at the player's *destination* sector.**
   `ResolveWallCollision` uses the sector returned by
   `FindChunkAndSectorSH(newPos)` and checks that sector's walls (+
   portal-adjacent). So **walls that belong to the sector the player is moving
   into will block them** — this is why buildings must BE the terrain sectors,
   not overlapping ones. `FindChunkAndSectorSH` returns the **first** sector (in
   spatial-hash insertion order = `BuildSpatialHash` chunk/sector slice order)
   whose polygon contains the point — so do NOT create overlapping sectors that
   compete; carve the cell so sub-sectors **tile** it without overlap.
4. **Primitive collision only checks the current + portal-adjacent sector's
   `PrimitiveIndices`** — this is why faking walls with primitives is fragile.
   Use sector walls for collision.
5. **Per-sector frustum cull** (main.go ~L450) uses `Floor.Height` and
   `Ceiling.Height` for the AABB Y-range. If a sector has a tall building above
   it, set its `Ceiling.Height` to cover the real top or it gets culled.
6. **Portal culling** (`PortalVisibleSectors`) only activates when the player's
   `CurrentChunk == chunk.ID` and floods from `CurrentSector` through portals.
   **Do not connect the whole terrain into one portal graph** — it blanks the
   world (this happened). Inter-terrain-cell edges must be `Pinned` walls (which
   `InferPortals` will NOT promote), not portals. Building **interiors** may use
   portals (small local clusters); that's fine and desirable.
7. **Floors interpolate per-vertex height for collision** without needing
   terrain mode (`InterpolateSurfaceHeight`, collision.go) — so a sloped
   convex floor sector with `VertexOffsets` set is walkable. Use `VertexOffsets`
   to make sub-sector floors follow the terrain heightmap exactly (seamless).
8. **The point pool reorders on save/load.** `chunkToRaw` writes points as a
   name→coord MAP; `loadRawChunk` rebuilds via map iteration → **order changes
   on reload.** Therefore: never rely on `chunk.Points`/sector indices matching
   grid order after a load. `RebakeCells` maps points→grid vertices by
   COORDINATE (`worldToVertex`); `GenerateCity` rebuilds `ch.Points` canonically
   before generating; `createWorld` builds the level in memory (no round-trip).
   Keep these patterns. **Sector slice order IS preserved** across save/load.
9. **Wall door openings**: cut with a `Wall.Extrusion` (Outer/Inner same,
   Depth 0, Visible/BackVisible false). V is measured down from the ceiling
   (`worldY = ceilH - V`); collision pass-through is `playerInWallExtrusion`
   (collision.go); the wall builder auto-normalizes hole winding. See existing
   `addDoorExtrusion` in worldgen_city.go.
10. **Textures**: floor/ceiling/wall reference a logical name; ensure it's
    uploaded (`ensureTexturesLoaded` / the `texNeeded` path in `LoadLevel`).
    Existing PNGs in `resources/textures`: brick, concrete, dirt, grass, rock,
    square, stone_floor, stucco, wood_floor, default. Add desert PNGs if wanted.

---

## Required architecture: per-cell constrained subdivision

The core new capability is: **given a grid cell (convex polygon) and a set of
constraint segments crossing it, split the cell into convex (triangle)
sub-sectors, each tagged by region** (terrain / road / building-floor /
building-wall). Build all of these as real sectors sharing one deduped point
pool. This is the engine extension you must write. Suggested new file:
`world_subdivide.go`.

### 1. Define the city geometry as 2D polygons (world XZ), independent of cells

- **Roads**: from the painted road cells, build a centerline graph (road-cell
  centers, orthogonal adjacency), split into chains at junctions/endpoints,
  smooth each chain (Catmull-Rom or Chaikin), then **offset** the smoothed
  centerline by half the road width on both sides to get a **road ribbon
  polygon** (a long thin polygon, possibly with mitered joints). Roads may also
  be authored directly as splines if you prefer, but deriving from painted cells
  keeps the existing brush UX.
- **Buildings**: choose footprints as **rotated rectangles** placed over city
  regions (random angle + position + size within style limits), rejecting
  overlaps and footprints that touch road/water cells. Each footprint has an
  **outer** rectangle and an **inner** rectangle inset by wall thickness (for
  thick walls — see below).

### 2. Rasterize constraints to cells, then subdivide each affected cell

For each grid cell:
- Collect the constraint **edges** that intersect the cell: road-ribbon
  boundary edges, building outer-rect edges, building inner-rect edges.
- Compute all intersection points of those edges with the cell boundary and
  with each other, **inside the cell**.
- **Constrained-triangulate** the cell: triangulate the cell polygon with the
  constraint segments forced as edges. You can implement this as: insert all
  constraint endpoints + intersections as Steiner points, split the cell into
  sub-polygons along constraint chords, then `earClip`/`delaunayTriangulate`
  each sub-polygon. Reuse `delaunayTriangulate`/`earClip` from `extrusion.go`.
- **Tag each output triangle** by testing its centroid: inside a building inner
  rect → building-floor; inside a building wall band (outer minus inner) →
  building-wall; inside a road ribbon → road; else → terrain.
- **Dedup points** into the chunk pool by coordinate (a `map[rounded-x,z]→index`)
  so neighboring cells/triangles share vertices exactly (NO T-junctions, NO
  cracks). Round to ~1e-4. Every triangle's vertices reference shared indices.

Cells with no constraints stay as the existing single quad (don't over-triangulate
empty terrain — keep it cheap; only subdivide cells the city touches).

### 3. Assign heights so everything is flush and seamless

- Terrain & road & building-floor triangle vertices: set floor `Height` +
  `VertexOffsets` so each vertex sits at the grid heightmap height
  (`HeightAt`/bilinear, or exact `Heights` at grid vertices, interpolated along
  constraint edges). This makes the carved floor coincide with the terrain
  surface — **flush, no floating, no cracks**.
- Road triangles: same height as terrain (flush), floor texture = road texture.
- Building-floor triangles: floor at terrain height (flush); ceiling enabled at
  a flat `roofY = maxGroundUnderFootprint + stories*storyHeight`.

### 4. Walls and ceilings WITH THICKNESS (grid-native, no boxes)

Thickness must come from geometry that is part of the carved sectors, not
overlay primitives. Required approach (implement fully):

- The **wall band** = the ring between the building's outer rect and inner rect
  (triangulated in step 2, tagged building-wall). Model each wall-band sector as
  a **raised solid**: floor at `roofY` (its top is the wall's top surface) with
  its perimeter rendered as walls. The vertical faces come from the **height
  difference** between the wall-band sector (floor `roofY`) and the adjacent
  building-floor/terrain sectors (floor at ground): make the shared edges
  **portals with `DrawGap=true`** so `RebuildGaps` fills the ground→`roofY`
  vertical faces (these are the visible thick inner/outer wall faces). The band
  has real width (outer−inner), so the wall reads as a solid volume with a flat
  top. Verify the gap risers also collide (if `RebuildGaps` geometry is
  non-colliding, additionally give the band's boundary `Wall`s `Collision=true`
  spanning ground→roofY, OR keep thin collidable `Wall`s on the inner rect edges
  as the collider). The player must be blocked by the wall and able to pass the
  door.
  - **Alternative if the gap-riser route proves unworkable**: build the wall as
    an explicit prism — two parallel wall quads (inner + outer faces) plus a top
    cap quad — emitted as sector geometry (extend `Sector`/`Geometry` if needed).
    Either way the result must have visible thickness, be collidable, and be
    part of the chunk geometry (saved/loaded as sectors), NOT loose primitives.
- The **ceiling/roof** must also have thickness: give the building-floor
  sectors a ceiling at `roofY`, and the wall-band top (floor at `roofY`) plus a
  thin raised roof layer forms the slab. Concretely, ensure the roof is a solid
  with a visible underside (the room ceiling) and a top surface offset by a roof
  thickness — model the roof as its own thin raised sector layer over the
  footprint (floor `roofY`, ceiling `roofY + roofThick`) or via the same
  gap-riser technique on the roof edges. No floating slabs.
- **Door**: cut a `Wall.Extrusion` opening (see `addDoorExtrusion`) on the
  inner-rect wall facing the nearest road, AND leave the corresponding wall-band
  span open so the player can walk through the thickness. Door reaches the floor
  and is tall enough to walk through (~2.4 clearance).

### 5. Build the chunk

- `GenerateCity(ch, grid, style)`: rebuild `ch.Points` canonically (grid
  vertices) first, then run subdivision, appending all dedup'd sub-sector points
  and sectors. Terrain (unsubdivided) cells stay quads. Tag sector IDs uniquely
  and stably (e.g. derived from cell + building index) so deterministic.
- Run `ch.InferPortals()` — but ensure inter-terrain-cell shared edges are
  `Pinned` walls (no portals), building-interior shared edges are portals, and
  wall-band/floor edges are portals where you need gap risers. Then
  `Sector.Load` every sector, `ch.RebuildGaps()`, `grid.updateChunkBounds(ch)`.
- Keep `GenerateCity` deterministic from `grid.Seed` (per-region/per-building
  RNG seeded from `grid.Seed`).

---

## Persistence (keep working)

World chunks save **full geometry** (sectors/points/primitives) + the
`world_grid` block (`chunkToRaw`, `loadRawChunk` already do this; primitive
rotation and wall extrusions round-trip). Manual edits made in the editor to a
world chunk must survive save/reload. Your subdivided sectors are ordinary
sectors, so they serialize fine — just confirm the round-trip after a reload
(the point pool reorders; rely on coordinates, not indices — see constraint #8).
`GenerateCity` re-rolls from the grid+seed; loading does NOT regenerate.

---

## Editor wiring (already present — keep it)

- Brush tools and `WorldApplyBrush`/`RebakeCells` (world_bake.go, editor_world.go).
  After your change, painting must still work; `RebakeCells` rebuilds only
  unsubdivided terrain cells (cells the city doesn't touch). Decide cleanly how a
  brush stroke over a built area behaves (simplest: it edits the underlying grid
  data; the city is only rebuilt on "Generate City").
- "Generate City" / "Reseed" buttons call `generateCityForActive`
  (editor_world.go) → `GenerateCity`. The 2D overlay (`WorldEditorRender`) should
  still show roads/city tints; update it to reflect the curved roads if helpful.

---

## Suggested task order (do all of them)

1. Write `world_subdivide.go`: convex-cell + constraint-segments →
   tagged convex triangles, with a shared deduped point pool. Unit-reason it
   against simple cases (a diagonal across one cell; a rotated rect overlapping 4
   cells). This is the crux — get it right and seamless first.
2. Buildings: rotated footprints (outer+inner rects) → carved building-floor +
   wall-band + thick walls + ceiling + door. Flush on terrain. Collision verified.
3. Roads: centerline graph → smoothed spline → ribbon polygon → carved road
   sub-sectors (retextured, flush). Curved/diagonal.
4. Wire into `GenerateCity`; canonical point rebuild; portals/pins correct;
   bounds correct.
5. Persistence round-trip; brush still works.
6. Build + vet + make; in-app verification.

---

## Verification (must pass)

Build: `go build ./... ` (or `CGO_ENABLED=1 go build -o /tmp/x *.go`),
`go vet ./...`, `make linux` — all clean.

In-app (`./dist/foundry`, press `~`, World toolbar, New World 32×32):
- Paint a **winding** road → it renders as a **smooth curved/diagonal** strip
  that is **flush with the ground**, no flicker, no floating, no z-fighting,
  seamless with terrain at its edges.
- Paint a city region and **Generate City** → buildings are **rotated/off-grid**,
  **carved into the terrain** (no floating), **enterable** (walk through the
  door), with **visibly thick walls and ceiling**, and **collide** (you cannot
  walk through walls; you can through the door). Variation in size/height.
- Walk the whole area: no falling through floors, no invisible walls, no holes,
  no cracks between sub-sectors.
- Orbit/translate camera near tall buildings and across the world origin: no
  sectors pop out / get wrongly culled.
- Save, restart, reload: the city (terrain paint + roads + buildings) is exactly
  preserved (full-geometry save). Generate City again with the same seed →
  identical result; Reseed → different but valid.

If any verification item fails, fix it — do not document it as a limitation.

---

## Anti-shortcut checklist (the receiving agent must be able to answer YES to all)

- [ ] Roads are curved/diagonal and are terrain geometry (carved sectors), not
      overlay primitives.
- [ ] Buildings are rotated/off-grid and carved into the grid, flush, enterable.
- [ ] Walls AND ceilings have real, visible thickness, grid-native, collidable.
- [ ] Cells are subdivided via real constrained triangulation; output sectors
      are convex; no cracks/T-junctions; shared points deduped.
- [ ] Terrain remains a grid; only city-touched cells are subdivided.
- [ ] Collision, frustum culling, and portal culling all behave (see constraints).
- [ ] Edits persist across save/reload; generation deterministic from seed.
- [ ] `go build`, `go vet`, `make linux` clean; all in-app checks pass.
