# Phase 2: rock and cliff generator — what was built

Phase 2 adds the `rocks` stage: families of weathered sandstone and granite
rock variants — boulders, cliff chunks, mesa caps, ledge slabs, hoodoos,
rubble, pebbles — modelled as signed distance fields, meshed by a
multi-threaded manifold dual-contouring mesher, decimated to a triangle
budget, baked with vertex colour (strata tint, cavity, edge wear, varnish,
dust, ambient occlusion) and cooked to Nanite meshes, with a manifest
(`rocks.yaml`) that phase 3's scatter and cliff_wall place them from.

It also carries **per-vertex colour and cavity through the whole engine**:
`model.ImportMesh.Colors`, glTF `COLOR_0`, a `.fnm` format bump (old files
still load), the cluster pages, the Nanite pools (page pool and flat pool),
the visibility buffer's material pass, the GPU cull's forward draw, the
classic path, and `fragment.glsl`.

The user-facing description, with the full parameter table, is the `rocks`
section of [docs/guide/33-generators.md](../guide/33-generators.md).

## Packages and files

| Package | Files | What |
| --- | --- | --- |
| `internal/procgen/sdf` (new) | sdf.go, prim.go, ops.go, displace.go, strata.go, fracture.go, chips.go, bake.go, mesher.go, meshutil.go; mesher_test.go, ops_test.go | The SDF library and the mesher. Pure computation: imports only `procgen/noise` (layering-asserted). |
| `internal/procgen/rocks` (new) | rocks.go, presets.go, color.go, finish.go, meta.go; rocks_test.go | The generator: `Settings` → `Generate(s, seed) (*Result, error)`; the presets; the colour bake; decimation, frame, box UVs; the manifest. Imports `procgen/sdf`, `procgen/noise`, `cluster` (the simplifier) and yaml (layering-asserted). |
| `internal/procgen/stages` | rocks.go, rocks_run.go, rocks_preview.go; rocks_test.go, rocks_bench_test.go, rocks_dump_gl_test.go | The stage: schema, run, preview, Load, the `rocks_demo` template. |
| `internal/procgen` | params.go, hash.go, preview.go | `ParamDef.PreviewOnly` (left out of the stage hash, kept in the preview's: `PreviewHash`); `PreviewMesh.Colors` and `.Turntable`. |
| `internal/procgen/meshout` | write.go | `CookOptions.CollisionHulls`. |
| `internal/model` | model_gltf.go; model_gltf_color_test.go | `ImportMesh.Colors`; `COLOR_0` read (VEC3/VEC4, float or normalised byte/short) and written (normalised RGBA8). |
| `internal/nanite` | mesh.go, pages.go | Format 5: `Mesh.HasColors`, `Colors`, `FallbackColors`, `PageData.Colors`, per-cluster colour bases/widths in the pages; `MinFileVersion`, `FileVersionPlain`, `Mesh.Version()`. |
| `internal/nanitecook` | cook.go, mesh.go, cmd.go; color_test.go | Colours through the weld, the simplifier (`Options.ColorWeight`), the fallback and the pages; staleness by format; `nanite inspect` says "vertex colours". |
| `internal/gfx` | geometry.go, indexed_mesh.go, material_surface.go, batch.go, graphics.go | `Geometry.VertexColors`/`Cbo` (classic path, editor previews), `IndexedMesh.Colors`, `SurfaceState.VertexColors`, `Material.VertexColors`, `AttribColor` (15), `VertexColorsUniform`, `BindColorLayout`/`BindPackedColorLayout`. |
| `internal/render` | boot.go, nanite_scenery.go, nanite_stream.go, nanite_vis.go, nanite_vis_source.go; nanite_color_gl_test.go | The pools' colour buffers, the material pass's binding 7, the uniform's location. |
| `internal/world` | scenery.go | The classic path hands a cooked mesh's colours to its Geometry. |
| `resources/shaders` | vertex.glsl, fragment.glsl, nanite_material_frag.glsl | `vertColor` (location 15), `vertexColors`, `fragVertColor`; albedo × rgb, a = cavity/AO. |
| `internal/editor` | editor_generator_view3d.go; editor_generator_rocks_test.go, editor_generator_rocks_gl_test.go | Vertex colours and the turntable in the 3D view. |
| `internal/cluster` | dag.go, simplify.go; degenerate_test.go | Two NaN guards (below). |
| `internal` | layering_test.go | `procgen/sdf` and `procgen/rocks` rules. |
| docs | this file, docs/guide/33-generators.md | |

No shared file from the spec's list was touched.

## The SDF library (internal/procgen/sdf)

A `Shape` is `F func(Vec3) float64` (negative inside), a `Box` the surface
lies in, and a Lipschitz bound `Lip`. Float64 throughout.

- **Primitives:** `Sphere`, `Cuboid`, `RoundBox`, `Ellipsoid` (bounded),
  `Capsule`, `Cylinder`, `HalfSpace`/`Plane` (infinite box; they bound by
  intersection).
- **CSG:** `Union`, `Intersect`, `Subtract` and their smooth (polynomial smin)
  versions over a radius k; `Offset`; `Bound`.
- **Transforms:** `Translate`, `Rotate` (`Mat3`, `RotAxis`/`RotX/Y/Z`), `Scale`,
  `Stretch` (per axis; Lipschitz grows by the ratio).
- **Noise displacement:** `Field` (a function with a gradient bound); `FBM`,
  `Ridged`, `Cellular` (pits, grain), `CellEdges` (crack networks),
  `FractalField`, `Warped`, `Sum`; `Displace`, `DisplaceWhere` (masked).
- **Strata** (`Strata`, `StrataInset`): beds bottom-up and repeating, each
  `Thickness` and `Hardness`; soft beds weather back by `Inset·(1−h)^1.5`
  (`BedJitter` varies it bed to bed, `Variation` along the bed); a softer bed
  under a harder one recedes up under the overhang (`Undercut`: flush at its
  base, deepest under the lip); the boundaries blend over a width that keeps
  the inset's slope under `MaxSlope` (the overhang's ceiling slopes at
  1/MaxSlope, and the function stays a bound with Lipschitz 1 + MaxSlope);
  `Dip` and a `Warp` field tilt and waver the beds; `Lamination`/`LamPeriod`
  groove the laminations, inclined across each bed by up to `CrossBed`
  (cross-bedding, each bed its own direction) and etched only where
  `LamPatch` is positive.
- **Fractures:** `Joints` — an anisotropic Voronoi network (`Spacing` per
  axis, `Jitter`, `Rotation`); `Border`/`BorderPair` (distance to the nearest
  bisector plane, cell IDs), `BlockValue` (a per-cell value blended across the
  borders), `CutJoints` (cracks `Gap` wide at the surface, closing `Depth` in —
  V-shaped — lips rounded by `Round`, open only where `Open` is positive, never
  narrower than `MinGap`); `JointSet` / `CutJointSet` (parallel planes, jittered
  and wavy); `BlockDisplace` (each Voronoi cell stands proud by its own amount:
  planar steps along the cell borders — broken rock's relief, several octaves
  for fractal fracturing); `Fracture` (cut by roughened half-spaces).
- **Spalls:** `PlaceChips`/`CutChips` (conchoidal: flattened ellipsoids at the
  most convex surface points; `Aspect` for an arched alcove; `ChipAt`,
  `TraceIn`), `PlaceFacets`/`CutFacets` (planar: the rock beyond a tilted
  plane within a wobbled ball — a flat-floored scar with a curved, sharp wall).
- **Weathering by curvature:** the mesher's `Weathering` (below), and the
  smooth CSG radii.
- **Bake helpers:** `Shape.AO(p, n, radius, steps)` (seven cones, room
  against distance, normalised so an open plane is 1 and the value at the
  point is subtracted first), `Shape.Laplacian(p, r)` (≈ r·(κ₁+κ₂): positive
  convex, negative concave), `Shape.Gradient`.

## The mesher (sdf.Polygonize)

Dual contouring on a sparse grid of 16³-cell blocks, capped at 512 cells a
side (`MaxCells`), multi-threaded (`Parallel`, deterministic by block order):

1. A coarse lattice (every 4 cells, the whole grid) is evaluated; a 4-cell
   subcube is active when its corners change sign or one is within
   `LocalLip·(diagonal + margin)` of the surface (LocalLip = Lip capped at 2:
   the global bound of a displaced, layered shape is far steeper than its
   gradient almost anywhere).
2. A block with any active subcube in its 25³-sample region (16 cells and a
   4-cell apron) is processed on its own goroutine: active subcubes evaluated
   exactly, the rest trilinear from the coarse corners.
3. **Weathering**: up to 3 steps of convex curvature flow, `d += rate/6 ·
   max(∇²d, 0)`, each capped at half a cell (so no sample further than that
   from the surface changes sign, and two blocks agree on a shared sample to
   the bit). Convex places — corners, edges — move in; faces and crevices stay.
4. **Manifold dual contouring**: each cell gets one vertex per *group* of its
   crossing edges — the edges marching cubes would join into one polygon, each
   face's ambiguous case decided the same way for both cells sharing it (inside
   corners kept apart) — so two sheets through one cell stay two sheets.
   Each vertex is the regularised QEF minimum of its group's crossings (normals
   from the field's central-difference gradient), pulled toward the mass point
   by `Sharpness` (0 is surface nets), clamped to the cell; with two groups in a
   cell each is kept nearer its own crossings. Each crossing edge owned by the
   block makes a quad, wound outward, split along the diagonal whose triangles
   agree best with the vertex normals. Vertex normals are the gradient,
   trilinear at the vertex.
5. `KeepLargest` drops fragments a joint cut free; `SplitNonManifold` (a
   safety net: splits a vertex shared by two fans and nudges the copies apart)
   finds nothing on everything tested.

Measured: a displaced rounded box to 350k triangles in 1.05 s (17 M
evaluations) on 20 threads; the outputs are closed, manifold and
closed-by-position (`nanite.Mesh.Closed`'s test) on every preset tested;
identical bytes on 1 and 13 workers.

## The generator (internal/procgen/rocks)

`Generate(s, seed)` for one variant:

1. **Shape**: the preset builds an SDF from the settings and an RNG keyed by
   the seed — **twice**: once to measure, then again knowing the finished
   mesh's vertex spacing, so detail finer than about three vertices a period
   (laminations, block steps, cracks) is left out of the geometry and the
   colour rather than aliased into dotted lines (`resolvable`, `minGap`).
2. **Resolution**: a coarse meshing (extent/40) measures the area (×1.3: the
   fine relief adds about that); the cell gives the mesh about three triangles
   per cell's worth of area at `triangles × oversample` (3× for scatter rocks,
   falling to 1× from 250k up), within the 512 cap. A hero piece (≥ 100k)
   that comes in under 80% of its budget is meshed once more at the corrected
   cell.
3. **Mesh** (above), weathering steps `round(2.4·Weathering)`, the preset's
   sharpness unless `Sharpness` ≥ 0.
4. **Bake** on the full mesh (color.go): RGB is the palette — sandstone: a
   colour per bed (soft beds darker and redder, each its own mix of base and
   band, up to ±9% brightness at full colour variation, blended into its
   neighbours over its outer fifth),
   bleached and stained patches, faint laminations (only where resolvable);
   granite: a feldspar/quartz mottle and dark-mineral specks — then a faint
   shade per joint block (blended across the joints), grain, the cavity
   darkening (40% fine Laplacian at 0.6% of the size, 60% broad at 5%), the
   edge lightening, desert varnish (steep faces, vertical streaks at two
   scales, heavier where AO is low, `varnish × preset factor`: 1.3 on cliff
   chunks, 0.2 on pebbles), dust on up-facing faces. A = 0.22 + 0.78 ×
   (1 − AO·(1−ao)) × (1 − 0.45·Cavity·cavity): never black (a crevice in a
   canyon still sees the warm rock round it).
5. **Decimate** (when over budget × 1.1, or × 1.4 from 200k): the cluster
   simplifier (quadric edge collapse onto existing vertices) with the normals
   (2% of the extent per radian) **and the colours** (3% per unit) weighed in,
   so vertices stay where a crack's dark line or a band's edge runs; then
   angle-weighted mesh normals (the gradient's, over decimated triangles, made
   streaks).
6. **Frame**: lowest point on y = 0, footprint centred; a cliff chunk's back on
   z = 0.
7. **Box UVs**: each triangle projected across its normal's main axis
   (mirrored on the negative sides), a vertex split wherever two projections
   meet; `uv_scale` per metre.

The presets (presets.go) and what each is built from:

| preset | size | default tris | construction |
| --- | --- | --- | --- |
| boulder | 1.6 m | 15k | an inflated ellipsoid cut by 7–13 random fracture planes (a polyhedron), smooth-intersected over 1–8% of the size by weathering (granite: rounder, spheroidal), low lumps, one octave of block relief, its beds (subtle inset), 6–9 planar spalls, a crack network open in places, honeycomb pits in patches, grain, a random landing tilt, conchoidal chips |
| cliff_chunk | 8 m high | 600k | a rounded box with a plan outline (bays, buttresses), a front-view outline (ragged top, stepped sides), large bulges, a broken top, two octaves of bed-following block relief, six beds with undercuts, 0–2 arched alcoves, slab scars on the front, a vertical joint set and a blocky network (both open in patches), vertical flutes, grain, chips; flat back and base |
| mesa_cap | 10 m wide | 300k | a slab with a noisy plan outline, flat block relief, caprock over a soft bed weathered back beneath it (undercut), rim spalls, rim joints open in places, potholes on top, chips; flat base |
| ledge_slab | 4 m long | 60k | a rounded slab cut to a 5–7-sided polygon by tilted joint faces, a bed warp, flat block relief, bedding beds, edge spalls, an optional crack, chips |
| pillar | 7 m high | 300k | a leaning stack of capsule lumps smooth-unioned with a flared pedestal, squeezed in section, a fractured caprock block, lumps, wavy beds necked in softly (MaxSlope 1), deep vertical rills, side spalls, horizontal joints, chips; flat base |
| rubble | 0.4 m | 3k | an inflated sphere cut by 7–11 half-spaces, slightly rounded, lumps, block relief, faint beds, spalls, tilt, chips |
| pebble | 8 cm | 800 | a flattened ellipsoid lying on its side, low lumps, smooth (sharpness 0) |

## The stage

`rocks` (stages/rocks*.go): 35 parameters (the table is in the guide):
`preset`, `variants`, `seed`, `size`, `size_jitter`, `triangles`,
`lithology`, `palette`, `color`, `band_color`, `color_variation`, `fracture`,
`joint_spacing`, `weathering`, `roughness`, `chips`, `strata_thickness`,
`strata_relief`, `undercut`, `cavity`, `ao`, `ao_radius`, `varnish`, `dust`,
`sharpness`, `texture`, `uv_scale`, `collision`, `hull_size`, `cook`,
`cook_memory`, `gallery`, `gallery_origin`, and the preview-only
`preview_variant`, `preview_triangles`.

- **Run**: variants side by side as far as `cook_memory` allows (1500 B per
  triangle to make, plus the cook's 1000 mesh-only or 8500 with hulls), each
  with its share of the workers; each cooked by `meshout.Cook` with
  `CollisionHulls` (under `hull_size`) or `CollisionMesh` (above it, and for
  `none` — the cook then skips the convex decomposition, per phase 1's
  meshcollide change); `rocks.yaml` written; with `gallery`, every variant
  placed in a row along +X. The variant seed is `Hash64(stage seed, "rock",
  i)`.
- **Preview**: the chosen variant at ≤ `preview_triangles`, one
  `PreviewMesh{Turntable, Colors, UVs, Normals}`, notes (triangles meshed and
  made, extent, footprint, top, time per step), the footprint in 2D. The two
  preview parameters are `PreviewOnly`: changing them never makes the stage
  stale (tested).
- **Load**: `rocks.LoadManifest` → `*stages.RocksArtifact{Dir, RelDir,
  Manifest}`; `ModelPath(v)` is a variant's resources-relative model path.
- **Template** `rocks_demo`: boulders (8), cliff chunks (3), pebbles (12).

## The manifest (what phase 3 reads)

`resources/generated/<generator>/<stage>/rocks.yaml` (`rocks.Manifest`,
`rocks.LoadManifest`; strict YAML):

| key | meaning |
| --- | --- |
| `version` | 1 (`rocks.ManifestVersion`) |
| `preset`, `lithology`, `palette` | the stage's |
| `frame` | the placement frame, in words (below) |
| `variants[]` | one per variant: |
| `.name` | `<preset>_<nn>`: the file stem and the gallery placement's ID suffix |
| `.file` | the cooked mesh (`.fnm`; `.glb` with `cook` off), relative to the manifest |
| `.seed` | the variant's seed |
| `.size` | the preset's main dimension as this variant came out (boulder/rubble/pebble/ledge length, cliff/pillar height, mesa width) |
| `.min`, `.max`, `.extent` | bounds in the placement frame, and their size |
| `.height` | `max.y` (the base is at 0) |
| `.radius` | bounding cylinder about +Y |
| `.footprint_radius`, `.base_band` | the cylinder of what lies within `base_band` of the ground (min of 12% of the height and a quarter of the plan extent): what touches the ground — for spacing and overlap rejection on the ground |
| `.sphere_center`, `.sphere_radius` | a bounding sphere (3D overlap tests) |
| `.top` | the 90th percentile height of the up-facing (n.y > 0.8) vertices: where a ledge is walked, where the next tier of a wall sits |
| `.back` | `[0, 0, -1]` for a variant with a flat back (cliff_chunk): the back is on z = 0, the rock along +Z; absent otherwise |
| `.flat_base` | the base was cut flat (cliff_chunk, mesa_cap, ledge_slab, pillar): it sits without sinking |
| `.volume` | m³ |
| `.triangles`, `.vertices` | the mesh before the cook (level 0 of the cook is the same triangles) |
| `.collision` | what the cook built: `hulls` (decomposition) or `mesh` (mesh only) |
| `.place_collision` | what a placement should ask for: `hulls`, `mesh` or `none` (pebbles) |
| `.mesh_ms`, `.simplify_ms`, `.bake_ms`, `.cook_ms` | where the time went |
| `.mesh_triangles`, `.cell` | what the mesher made, at what cell |

**The frame**: +Y up; the lowest point on y = 0 (a placement at a ground point
sits on it — sink it by moving it down a fraction of `size`); the footprint's
bounding box centred on the Y axis. A variant with `back` has its flat back on
z = 0 facing −Z, centred in X: place it at a point on a wall with +Z turned out
of the wall (yaw = the wall normal's heading). Scale a placement uniformly to
vary size further; the manifest's lengths scale with it.

## Vertex colour end to end

- **Import/export**: `ImportMesh.Colors [][4]uint8` (nil: none). glTF
  `COLOR_0` read as VEC3/VEC4 float or normalised unsigned byte/short (taken as
  stored: the engine's textures are not linearised either), written as
  normalised RGBA8. A mesh without colours encodes to the GLB bytes it always
  did (every in-memory cook hash depends on that; tested).
- **Cook**: colours ride in the weld record (`IndexedMesh.Colors`), become
  `Mesh.Colors` (RGBA8 words, R low — `unpackUnorm4x8`'s order) beside the
  pool, are weighed by the DAG's simplification (`Options.ColorWeight`,
  default 0.1 of the radius per unit; coarse levels index the same pool, so
  they only ever use source colours — tested), kept in the fallback
  (`FallbackColors`), and written in the pages.
- **Format 5**: a page's cluster carries, after its index width, four 8-bit
  channel bases and 4-bit widths, and every vertex its four channels at those
  widths after its normal (a rock's colour varies slowly: 3–5 bits a channel
  is typical). `HasColors` and `FallbackColors` are gob fields of the
  metadata. **A mesh without colours is still written as format 4**, byte for
  byte as before; `Read` takes 4 and 5 (`MinFileVersion`); `CookMeshFile` and
  `UpToDateWith` expect 5 exactly when the source has colours (so a format-4
  cook of a coloured glTF, made before colours were read, is stale — tested).
- **GPU pools**: the page pool allocates `cbo` (one RGBA8 word a vertex a
  slot) only when a mesh in it has colours (slot size and budget account for
  it), uploads pages' colours through the staging ring or directly, and the
  flat pool concatenates them (white for uncoloured meshes, never read). The
  pool VAO binds them at location 15 (`BindPackedColorLayout`, normalised
  bytes). Groups are keyed by (texture, smooth, colours): a coloured mesh and
  a plain one never share a draw, and plain ones keep `vertexColors` 0.
- **Visibility buffer**: `visGroup`/`visBin.cbo`; the material pass binds it
  at SSBO 7 (6 is the tile lists) or the dummy; `nanite_material_frag.glsl`
  fetches `unpackUnorm4x8(colorData[v])` into `vertColor` per corner (gated by
  `vertexColors`) and interpolates `vx_fragVertColor`; `fragVertColor` joined
  `visVaryings`. The material pass sets `vertexColors` per bin through
  `SurfaceLocs`.
- **Forward and classic**: `SurfaceState.VertexColors` → `vertexColors` in
  `Apply`/`ApplyLocs` on every draw (and reset by the line draw and after the
  cooked groups), so nothing leaks; `Geometry.VertexColors` (source-parallel
  RGBA floats) de-index, weld and upload to `Cbo` at location 15; the classic
  scenery path fills it from the cooked colours.
- **Shaders**: vertex.glsl `layout(location = 15) in vec4 vertColor;
  uniform int vertexColors; out vec4 fragVertColor` (white when off).
  fragment.glsl: untextured (`normalColor`) with colours is white, not the
  normal colour; `texColor.rgb *= fragVertColor.rgb`; `vertexAO =
  fragVertColor.a`; in the unbaked branch with colours, `ambient·AO + direct ·
  mix(1, AO, 0.35)`. **With `vertexColors` 0 every line of the old lighting
  runs unchanged**, operation for operation, so existing content renders
  identically (the GL suites' parity tests pass unchanged).

## A Nanite cook bug the rocks found

A generated mesa cap cooked with **NaN LOD errors** at every DAG level and
drew as nothing: a NaN is never under the LOD threshold, so the cut never
selected a cluster (`nanite inspect` showed "error up to NaN", no fallback, no
SDF). The cause was pre-existing and not the colours (an uncoloured cook of
the same mesh did the same): the cook's quantisation can put two corners of a
sliver triangle on one point, and `pointTriDist2` (the DAG's measured
deviation) divides 0 by 0 for such a triangle; one NaN in `deviation` became
the group's error. Fixed in `internal/cluster`: `deviation` skips a NaN
distance (the degenerate triangle is no nearer than its neighbours), and the
simplifier refuses a collapse whose cost is NaN or infinite (a sliver's
attribute fit) instead of costing it. Both change only what was a NaN before,
so every cook that worked cooks the same bytes (CookVersion not bumped; a
cooked file that has NaN errors needs a re-cook). The mesher also keeps a dual
contouring vertex a thousandth of a cell inside its cell, so two neighbours'
vertices clamped to their shared face no longer meet (`degenerate_test.go`;
the stage test asserts no NaN error in any cooked cluster).

## Tests

| Package | What |
| --- | --- |
| model | `COLOR_0` round trip (RGBA8 exact), float VEC3 read as opaque, VEC2 refused, a mesh without colours writes no `COLOR_0` |
| nanitecook | a coloured rock cooks to format 5, every pool and fallback vertex keeps its source colour, the file and `LoadPage` decode the same words, root clusters use only source colours; a plain mesh cooks to format 4 with no colours; coloured `CookMeshFile` skips when unchanged; a format-4 cook of a coloured glTF is stale |
| procgen/sdf | sphere: closed, manifold, volume and area within 1–2%, normals radial; box corners kept by DC; identical on 1 and 13 workers; weathering takes convex corners only; a cracked, displaced, weathered shape is closed and manifold without help; fragments dropped; cancellation; primitives, transforms and boxes; smooth operators; strata profile (soft over hard, the slope bound, LayerAt repeating); Voronoi borders and BlockDisplace continuity; facets local with flat floors; AO open ≈ 1, slot low; Laplacian signs |
| procgen/rocks | every preset: closed by position, near budget, coloured/UV'd/normal'd, base on 0, footprint centred, cliff back on z = 0, sane volume/footprint/top/size, a bake that varies; deterministic on 1 and 13 workers, seed-sensitive; granite grey vs sandstone warm, palettes; unknown preset refused |
| procgen/stages | end to end (manifest, files, format 5 with fallback colours, hulls cooked for boulders, closed solids, gallery placements, Load, rerun skipped, preview-only change not stale, shape change stale); a cliff chunk collides and cooks mesh-only with its back; deterministic cook on 1 vs 7 workers (same source hash, pages, pool, colours, manifest bar timings); the preview's coloured turntable mesh, notes, footprint and that it is the run's variant; validation and the collision rule |
| procgen (core) | existing hash/preview tests pass with PreviewHash |
| editor | the rocks stage's panel, 3D and 2D views and the overlay draw; the turntable turns about the variant's axis only with Spin on |
| editor (GL) | the rock preview draws through the preview world program with its colours (≥ 90% of its pixels the sandstone's warm colour, not normal colours) |
| render (GL) | `TestVisMaterialProgramBuilds`: the visibility buffer's programs build from the repo's shaders (a failed build would silently fall back to forward), and the material source has the colour fetch; `TestNaniteColorReachesPixelsOnEveryPath`: a plain, a green and an occluded green rock on five paths — visibility buffer and forward on the page pool and the flat pool, and the classic path — the green rock green, the plain one untinted, the occluded one darker, the same values on every path |
| render | `nanite_vis_source_test` passes |
| internal | layering: `procgen/sdf`, `procgen/rocks` |

### Test results

- `go build ./...` and `go vet ./...` clean (the other session's score
  editor broke the editor package's build for a while mid-phase; the rock
  pictures were taken through a GL test in `procgen/stages` meanwhile).
- `go test` passes for model, gfx, nanite, nanitecook, cluster, meshcollide,
  procgen/... (core, gencli, genlevel, meshout, noise, rocks, sdf, stages,
  terrain), world/..., render/..., editor, levelio, biome, pack and internal
  (layering).
- GL (`FOUNDRY_GL_TEST=1`): gfx, editor and procgen/stages pass; render
  passes except `TestNaniteFoliageDrawsTheClassicNearField` ("the forest
  covers only 4588 pixels"), the failure phases 0 and 1 recorded as predating
  this work (401 s run). `TestVisMaterialProgramBuilds`,
  `TestNaniteColorReachesPixelsOnEveryPath` and the visibility buffer's
  parity tests (`TestVisBufferShadesTheForwardPicture`) pass.

## Measured numbers (dev box: 20 threads, WSL2, Mesa d3d12)

One variant at a time with every thread on it (`TestRocksBench`,
`FOUNDRY_ROCK_BENCH=1`): made, decimated, baked and cooked as the stage does,
collision by `auto` (4 m hull size). "meshed" is what the mesher made before
the decimation; "cell" its grid spacing; the cooked `.fnm` holds every DAG
level and the colour channel.

| preset | budget | triangles | meshed | cell | mesh | decimate | bake | cook | .fnm | collision |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| pebble | 800 | 800 | 1740 | 0.3 cm | 0.01 s | 0.03 s | 0.01 s | 0.05 s | 0.1 MB | none |
| rubble | 3000 | 3000 | 6428 | 0.9 cm | 0.21 s | 0.11 s | 0.05 s | 0.10 s | 0.1 MB | hulls |
| boulder | 5000 | 5000 | 11036 | 3.0 cm | 0.63 s | 0.19 s | 0.27 s | 0.21 s | 0.2 MB | hulls |
| boulder | 15000 | 15000 | 33516 | 1.7 cm | 1.76 s | 0.64 s | 0.70 s | 0.35 s | 0.6 MB | hulls |
| boulder | 50000 | 50000 | 112200 | 1.0 cm | 5.65 s | 2.31 s | 2.07 s | 0.87 s | 1.6 MB | hulls |
| boulder | 150000 | 150000 | 300252 | 0.6 cm | 28.91 s | 5.81 s | 6.40 s | 2.29 s | 4.4 MB | hulls |
| ledge_slab | 60000 | 60000 | 99220 | 1.6 cm | 1.81 s | 1.86 s | 1.47 s | 1.35 s | 2.0 MB | hulls |
| mesa_cap | 300000 | 304784 | 304784 | 3.4 cm | 15.18 s | 0.00 s | 4.11 s | 4.65 s | 10.0 MB | mesh |
| pillar | 300000 | 299976 | 299976 | 2.0 cm | 20.21 s | 0.00 s | 4.36 s | 5.21 s | 9.5 MB | mesh |
| cliff_chunk | 150000 | 149999 | 315688 | 3.0 cm | 20.80 s | 6.28 s | 5.64 s | 1.99 s | 6.9 MB | mesh |
| cliff_chunk | 600000 | 615044 | 615044 | 2.3 cm | 44.82 s | 0.00 s | 10.87 s | 15.11 s | 22.1 MB | mesh |
| cliff_chunk | 1200000 | 738580 | 738580 | 2.1 cm | 32.41 s | 0.00 s | 12.96 s | 19.05 s | 26.0 MB | mesh |

- The 1.2M cliff chunk hit the mesher's 512-cell cap (2.1 cm over 8 m plus
  its margin): 739k triangles is the most a chunk that size gets.
- Making dominates: the SDF's evaluation (a boulder's ~30 M evaluations at
  150k; the block relief's Voronoi searches and the joints are most of each).
  Decimation is single-threaded (the cluster simplifier) and matters only for
  scatter rocks, which are made several at a time.
- Hero cooks are mesh-only (no hull decomposition): 15 s for 615k triangles;
  with hulls the same cook would hold ~8.5 kB a triangle.
- A 600k chunk at 2.3 cm: ~70 s from nothing to cooked. Scatter rocks: 1–3 s
  each, several side by side.

The scratch project run (`foundry gen run`, the eight stages below, one
terrain patch and 36 variants: 6 sandstone and 4 granite boulders, 2 cliff
chunks, a mesa cap, 3 ledges, 2 hoodoos, 8 rubble, 10 pebbles) took 4 min 34 s
with `-force` and 1.34 GB peak RSS: boulders 20 s (6 at a time, 3 workers
each), granite 13 s, cliff chunks 2 min 14 s (2 at a time, 10 workers each:
613k and 590k triangles, 22 and 20 MB), the mesa cap 22 s (300k, 10.6 MB),
ledges 17 s, hoodoos 58 s (300k each, 10 MB), rubble 3.9 s, pebbles 0.4 s.
Running two variants side by side on half the workers each is slower per
variant than the table's all-threads figure (the SDF's evaluation scales with
threads; the cook's DAG build only partly) but finishes the stage sooner.

## Pictures inspected

All in the session scratchpad
(`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/8b1c0d12-038d-4935-9ad4-2bf2380ff5b6/scratchpad/p2/`):

- `r1/` … `r7/`: the tuning rounds through the preview world program
  (`TestRockDumpGL`: four views a variant), `r7/boulders.png` and
  `r7/cliffs.png` contact sheets; `r8/` the scratch generator's own hoodoos.
- `gl/color_*.png`: the five-path colour GL test's frames.
- `proj/` is a scratch project (shaders and textures symlinked to the repo's)
  with `generators/rocks.fgen` and `levels/rocks.yaml` (the gallery spliced
  in); `shots/*.png` are `foundry play run` pictures in it. The final ones:
  `f_boulders.png`, `f_granite.png`, `f_cliffs.png`, `f_mesa.png`,
  `f_ledges.png`, `f_pillars.png` (`sheet_final.png` and
  `mesa_ledges_final.png` put them together). Earlier: `boulders.png`,
  `granite.png`, `cliffs2.png`, `mesa.png`, `ledges.png`, `rubble.png`,
  `sheet1.png`; `pillars.png` (live GI on: black patches),
  `pillars_gioff.png`, `pillars_fwd.png` (visibility buffer off: the same
  patches), `pillars_plain.png` (one pillar cooked without colours, textured:
  the same patch); `mesa_cmp.png` (the mesa invisible before the NaN fix).

What the tuning changed, in order, and why (each from looking):
laminations as deep regular grooves read as **wood grain** → a few
millimetres, patchy, and left out where unresolvable; an even Voronoi joint
network read as **tiling** → joints open only in patches, a minimum gap; FBM
displacement at the rock's scale made **pillows** → planar facets
(flat-floored spalls), then polyhedral boulder cores; circular scar walls read
as **crescents** → wobbled outlines; box-like cliffs → plan and front-view
outlines, broken tops, multi-octave block relief (planar steps), arched
alcoves; dotted cracks and steps (**aliasing**) → resolvability against the
vertex spacing; ink-outlined steps → the cavity mostly from the broad
Laplacian; decimation **streaks** → mesh normals after the decimation and the
colours weighed into it; uniform orange → per-bed and mottled colour, varnish
streaks, blended bed edges, faint block tints; pure black crevices → an AO
floor.

**How they read.** In the game (`shots/`): granite boulders read as real
weathered granite (rounded, speckled, grounded by their shadows); sandstone
boulders and rubble as weathered sandstone blocks with bedding, a little too
smooth up close; hoodoos as hoodoos, though still too regular (turned-column
rings); cliff chunks and mesa caps have the right features (undercut beds,
blocks, scars, alcoves) but their large faces are too smooth and their
silhouettes still box-like — they will read as rock once phase 3 stacks and
overlaps them and phases 4–5 put a rock texture and micro-normal on them.
Without textures everything is smooth below the vertex spacing (2–3 cm on a
scatter boulder): micro-detail is the texture phases' job.

## Known gaps

- **Realism is not photographic yet.** Shapes and colour carry the forms; the
  surfaces are smooth below the mesh's vertex spacing and read slightly
  plastic until phases 4–5 add rock textures, normal maps and triplanar
  materials. Hoodoos are too regular; cliff chunks too boxy alone.
- **Live GI blacks out patches of big cooked rocks** (pillars, cliff chunks:
  `shots/pillars.png`); with `-gi off` they are gone, and an uncoloured,
  textured cook of the same pillar shows the same patch
  (`shots/pillars_plain.png`) — a pre-existing DDGI/SDF limitation on large
  cooked meshes (probes or distance-field cells inside the rock), not the
  colour path. Worth a look before phase 7's canyon.
- **Triangle budgets are approximate**: scatter rocks land within 10% (the
  decimation); hero pieces are meshed directly and land within about 20%
  after the one correction pass.
- **Generation cost** is in the SDF's evaluation: a boulder is ~1–3 s on two
  threads, a 600k cliff chunk ~30 s on all of them (see the table). The block
  relief (three Voronoi searches a sample) and the joints dominate.
- **Cooked size**: a 600k-triangle chunk is ~15 MB on disk (≈ 37 bytes a
  triangle with every DAG level and the colour channel).
- **Multi-shot `foundry play` runs hang on WSLg** after two pictures (the
  glReadPixels deadlock phase 1 recorded); every picture here was taken in a
  run of its own.
- **Colour space**: vertex colours are taken as display values, like the
  engine's textures; a linear-workflow change later would treat both alike.
- **Granite** has no exfoliation sheets or tafoni yet; sandstone has no
  liesegang bands or iron concretions.
- The phase-0 gap on `list_assets` still stands.
