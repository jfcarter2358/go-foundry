# Environment presets — step 2b: the Poly Haven grass instancer, and a polish pass

Status: built and tested, 2026-10-13. The instancer (generator stage, file
formats, renderer, settings, shadows, profiler row) has unit, stage and GL
tests; eight presets were moved onto it and re-rendered on WSLg (Mesa d3d12
on the GTX 1070) and looked at; a prairie and a savannah view were measured
natively on the GTX 1070 (docs/canyon/PERF.md method). Nothing was clicked in
a running editor; the OneDrive project was not touched.

`$S` is this session's scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/9afc65f5-e4b2-4b3e-9788-2e06870a0755/scratchpad`.
Scratch project `$S/presets2` (levels `p_<preset>`). **Before** sheets:
`$S/p2/sheets/<preset>.png` (step 2). **After** sheets:
`$S/p2b/sheets/<preset>_f.png` (same poses, except the coast's: its terrain
changed, new poses in `$S/p2b/poses/coast.txt`). Iterations: `_b`, `_c`, `_d`.

## Why a new path

Step 2 placed every Poly Haven grass clump as scenery: ~1 ms of CPU frame per
thousand in view, so a prairie was ~3% of the ground under a clump. The
engine's foliage biomes (.fbio) are off the table (the user's direction:
plants are Poly Haven only). So this is a third, dedicated path: Poly Haven
clumps → a kit of small LOD meshes; instances per terrain tile in a compact
binary file; one level volume per tile; GPU instancing culled per cell on the
CPU. It reuses only the low-level machinery (biome_light.glsl lighting,
wind_common.glsl's wind block, foliage_frag's dither scheme, the VSM).

## Design and formats

**`internal/grassfield`** (pure Go; layering entry added):
- **Kit `.fgk`**: kinds, each a Poly Haven piece: albedo texture name (the
  `_cov` cutout), height/radius, wind bend/flutter, range share, flags
  (casts), and 1–3 LOD meshes of 12 floats a vertex (pos, normal, uv, wind
  weights up/out/bend/flutter). LOD 0 is the piece as imported (capped);
  LODs 1–2 are **thinned** (`Thin`: a seeded subset of whole blades, each
  widened in XZ about its own axis by 1/√f, height kept — DecimateCards
  enlarges about the centre, which would sink a blade).
- **Field `.fgrass`**: one terrain tile. 12 bytes an instance — x,z u16 over
  the tile box, y u16 over its height range, yaw u8, scale u8, tint rgb
  (1.0 = 128), **rank** u8 = hash of world XZ. Instances grouped into runs by
  (16 m cell, kind), each run in rank order; per-cell height range for
  culling. Decoders bounds-check everything (truncation tests).
- **Falloff** (`falloff.go`, mirrored line for line in
  `grassfield_common.glsl`; constants pinned by a test): kept fraction
  `(start/d)^thin` past `start`, faded over the last 18% of the range, times
  the density setting. The CPU draws the prefix of each run its cell's
  NEAREST point admits; the vertex stage fades each drawn instance by its own
  distance over a 6% rank band and grows survivors (keep^-½, capped 3×, a
  share into height). Same instance always drops at the same distance — no
  wave. Tiers: LOD0 to 11 m, LOD1 to 32 m, LOD2 beyond (×the distance
  setting), cross-faded per instance by complementary dither over 15% of
  the boundary. Defaults: range 170 m, start 18 m, thin 1.4.

**Generator stage `grass_field`** (`procgen/stages/grass_field*.go`,
Regional PerRegion, Placement): rules name a `polyhaven_plants` stage
(`pieces` name filter, `min_size`, `max_triangles` LOD0 cap), density per m²,
scale, edge shrink, the scatter bands (reused `place.Rule.Weight`), shore
band + `max_depth` (from the terrain's water mask via `waterProximity`),
two-colour tint drift, wind, range share, shadows. Placement is
`procgen/place/grass.go`: a world-aligned jittered lattice, every random
draw a hash of the world lattice cell, so a tile's grass is independent of
which tile is asked for (tested: a rectangle = the union of its quarters)
and region runs agree across the band. Writes `grass.fgk`, one
`grass_<i>_<j>.fgrass` per terrain tile, `grass.yaml` (Load), and **one
volume per field** with a `grass:` block — the splice owns them by prefix as
it does every volume. Meshes come from the import's own glTF
(`phimport.LoadPlantParts`, the exact parts the import cooked; `_lite`
copies skipped).

**Level** (`world/volume_grass.go`): `RawVolume.Grass {file, chunk}`;
`chunk: auto` → `SceneryChunkAt` under the volume, resolved per level; a
switched-off chunk's field is neither drawn nor cast. Not a trigger.
`volumeRoleSummary`, editor duplicate, YAML field docs updated. Pack follows
field → kit → textures out of the binary files (`pack/grass_refs.go`).

**Renderer** (`render/grass_field.go`, `grass_field_shadow.go`; shaders
`grassfield_{common,vert,frag,shadow_vert,shadow_frag}.glsl`):
- Residency: a field loads when the camera is within range + 24 m of its
  box (4 loads a frame; all at once on a level's first frame), evicted 240
  frames after leaving reach. One VAO per kit (separate attribute format;
  the field's instance VBO bound at binding 1, divisor 1, so
  MultiDrawElementsIndirect's baseInstance offsets it).
- Per frame: cells frustum- and range-culled on the CPU; commands per
  (field, texture/range/LOD-count group, tier); one MDI per batch; one
  indirect buffer upload.
- Colour pass in the opaque pass right after the terrain foliage (profile
  row **`grass`**), on both frame paths (after the vis buffer's material
  pass resolved the scene). Per-vertex lighting through biome_light.glsl
  with **one** VSM tap (`vsmShadow1`, new in vsm.glsl, selected by
  `BIOME_LIGHT_VSM_ONE_TAP`; every other pass keeps the nine-tap filter),
  base AO, back-light; `_cov` albedo per pixel; A2C / TAA dither / ½ test;
  fog. A level without a grass volume skips the pass entirely.
- Shadows: cells within `grass_shadow_distance` (14 m default), casting
  kinds only, coarsest mesh, into cascades 0–1 or as VSM movers (tracker
  box round the camera; `drawMovers` row `grass`).
- Settings (`gfx/grass_settings.go`, graphics.yaml, `@level.prop.*`):
  `grass_density` 0.25–1, `grass_distance` 0.25–2, `grass_shadow_distance`
  0–60 m. Bench report prints a `grass` line (fields, cells, commands,
  instances, triangles). `FOUNDRY_GRASS_LIGHT=flat|noshadow` is an A/B
  knob only.

## The rectangular patches (fixed — a shader precision bug)

Sheet pose 3 of the prairie: sharp-edged axis-aligned rectangles of
different brightness from 40 m. Isolated by A/B: still there with GI off,
sun shadows off, the visibility buffer off, software raster off, anti-tile
off; the lighting-only view showed none; **removing `macro_variation` from
the material removed them**, a 6000 m macro scale too. The value noise
(`surface_fx.glsl` `fxHash`/`fxNoise`) amplifies its input ~2000×, and the
driver folded `(i + 1)·123.34` into an FMA in one cell and not the next, so
the hash of a shared corner differed across a cell edge: discontinuous
noise, i.e. rectangles (60 m cells at the macro scale). Not a tile-boundary
bug. Fix: `precise` on the hash's intermediates and the corner sums —
values identical to the math as written, continuous on the GPU. Test:
`TestSurfaceFxNoiseIsContinuous` draws fxFbm over 512 m at the macro scale
and bounds the neighbour step: 0.48 before the fix (fails), 0.030 after
(bound 0.065). It affects every surface using the noise (macro, anti-tile
bands, breakup): their patterns are now continuous; elsewhere unchanged.

## Presets (after sheets: `$S/p2b/sheets/<preset>_f.png`)

| Preset | Grass on the instancer | Clumps / 512 m region | Other changes |
| --- | --- | --- | --- |
| tallgrass_prairie | tall clumps ×4.4–6.4 at 3/m², mid ×5–7 at 1.35/m², dry patches, flower drifts | 1.26 M | ground tint olive (0.64, 0.66, 0.38) |
| oak_savannah | the same at 1.6/m², ×0.8 | 0.67 M | — |
| savannah | grass_medium_02 straw sward 1.6/m² + taller patches | 0.47 M | scenery grass rule removed (canyon_savannah worlds change too) |
| wetland | reeds: tall ×6–9, 4/m², shore 0–7 m, to 0.45 m deep; tussock sedge | 0.81 M | — |
| coast | dune grass ×3–4.5 behind the beach, tufts onto it | 0.20 M | **cliff**: 24 m over 6 m, light erosion → ~23 m at ~43°, rock shows |
| tundra | sedge tufts ×1.5–2.6, 0.9/m², no shadows | 0.18 M | — |
| alpine | meadow tufts, small/mid ×1.6–2.4 | 0.06 M | rock = **rock_surface** @16 m, tint (0.62, 0.64, 0.68): no orange, no lattice |
| pine_forest | fern_02 understory ×1.6–2.6 at 1.1/m², clearing grass | 0.40 M | firs ×1.2–1.7 at 0.32/100 m² spacing 0.55, saplings doubled; floor tint darker |

Looked at, each against its before sheet: the prairie is a continuous 1–2 m
sward with flowers from the ground (was a texture with scattered clumps);
the patches are gone from 40 m. Oak savannah: oaks over a grass sward.
Savannah: dry straw sward. Wetland: dense reed beds at the shores. Coast:
reads as a rocky coast (cliff face, narrow beach, dune grass on top).
Alpine: grey granite ranges with snow. Pine: a closer stand over a fern
floor. Dunes and volcanic were not changed.

## Native numbers (GTX 1070, 2560×1080, TAA, VSM, GI off, pool 1024 MB)

Dev exe cross-built with mingw, project copied to
`%LOCALAPPDATA%\Temp\foundry-grass-perf` (deleted at the end), 60 warm-up +
120 measured frames a pose, the step-2 sheet poses. ms:

| View | p50 on → off | p95 on | `grass` GPU | grass shadow GPU | instances / triangles |
| --- | --- | --- | --- | --- | --- |
| prairie ground pose 0 | 14.5 → 10.4 | 15.6 | 3.47 | 0.28 | 36.7 k / 4.48 M |
| prairie ground pose 1 | 15.5 → 11.4 | 16.7 | 3.25 | 0.34 | 34.4 k / 4.12 M |
| prairie ground pose 2 | 14.3 → 10.3 | 15.3 | 3.22 | 0.31 | 36.6 k / 4.38 M |
| prairie 40 m up | 13.8 → 13.2 | 14.8 | 0.38 | 0.00 | 12.8 k / 0.54 M |
| savannah pose 0 | 21.5 → 19.4 | 22.7 | 1.29 | 0.18 | 13.0 k / 2.22 M |
| savannah pose 1 | 21.1 → 18.8 | 23.1 | 1.22 | 0.23 | 12.2 k / 2.11 M |
| savannah pose 2 | 24.0 → 22.4 | 26.8 | 1.18 | 0.20 | 12.9 k / 2.20 M |
| savannah 40 m up | 30.7 → 30.5 | 31.9 | 0.20 | 0.00 | 4.6 k / 0.39 M |

(The prairie "off" column is the previous build, same level, `hide=grass`;
the savannah's is the final build.) Reports:
`%…%\out\pra3_on.txt`, `sav3_on.txt`, `sav3_off.txt`, `pra2_off.txt`
(copied to `$S/p2b/native/`).

How it got there (prairie pose 0, grass GPU): first build **12.3 ms** —
entirely vertex-bound (12.3 at 2560×1080, 11.8 at 1280×540).
`FOUNDRY_GRASS_LIGHT=flat` 3.5, `noshadow` 4.2: the per-vertex nine-tap VSM
filter was ~8 ms. One tap: **5.4**. Then LOD0 caps 900 → 450–500, LOD1/LOD2
at 11/32 m (were 16/45), thinning from 18 m (was 22): 8.7 → 4.5 M triangles,
**3.5 ms**. Shadows: mid mesh at 18 m cost 0.96 ms; coarsest mesh at 14 m
**0.3 ms**. A full prairie ground view adds ~4 ms of frame (p50), the top of
the 3–4 ms target; the savannah ~2 ms.

## Tests

- `grassfield`: kit and field round trips (and every truncation is an
  error), run order and quantisation, deterministic build (shuffled input,
  same bytes), uniform ranks, falloff monotone/scaled/grow bounds, the CPU
  prefix covers every instance the vertex stage would show, narrow fade
  band, tier weights sum to 1 and every shown tier is drawn, thinning keeps
  height and widens blades.
- `place`: tile independence + determinism, slope/water/shore/region/avoid
  bands, lattice density.
- `stages`: grass_field end to end on the fake Poly Haven (kit LODs
  thinner, one field per tile, every clump inside its volume and on the
  ground, volumes decode through the level schema, Load, byte-identical
  rerun), validation and preview; `TestPresetGrassIsOnTheInstancer` (eight
  presets: a valid `sward`, rules name plants stages, no scatter rule
  places a grass set); savannah test re-pinned
  (`TestSavannahPresetGrassIsPolyHavenGrassField`).
- `world`: volume grass block round trip, chunk resolution.
- `pack`: field → kit → texture followed, an unreferenced field pruned.
- `render` (no GL): cell culling (frustum, range), thinning and density,
  tiers and windows, shadow gather (casting group, coarsest mesh); shader
  constants match Go.
- GL (`FOUNDRY_GL_TEST=1`): `TestGrassFieldDraws` (no grass volume → the
  frame is identical with the pass on/off; a field draws on the forward and
  the vis-buffer path — 7.6 k pixels, the two agree within 8 levels on all
  but 4 pixels; density 0.25 halves the instances; out of range draws
  nothing; shadow box), `TestSurfaceFxNoiseIsContinuous`; plus the existing
  material/sampler-budget, vis-program, scene-block, VSM, foliage, cutout
  and decal GL tests (commands below). The known-failing
  TestNaniteFoliageDrawsTheClassicNearField and
  TestNaniteStreamStreamsInFromTheRoots were not run.

```
export GOFLAGS=-p=6 GOMEMLIMIT=6GiB
go build ./...                                                         clean
go vet grassfield render procgen/stages procgen/place world pack gfx app script phimport editor   clean
go test ./internal/grassfield/ ./internal/world/... ./internal/levelio/... ./internal/pack/ ./internal/app/
        ./internal/script/ ./internal/gfx/ ./internal/polyhaven/... ./internal/procgen/... ./internal/render/   ok
go test ./internal/editor/...   fails only TestScoreSibeliusKeymap, TestScoreRebinding (the score session's)
go test ./internal/ -run TestPackagesDoNotDependUpward                 ok
FOUNDRY_GL_TEST=1 go test ./internal/render/ -run 'TestGrassField|TestSurfaceFxNoise|TestMaterialFx|
    TestMaterialPassBuildsTheEightLayerVariant|TestVisMaterialProgramBuilds|TestMaterialVariants|
    TestSceneBlockMatchesTheShader|TestFxUnitsAreFree|TestSkyAndFogShadersCompile|
    TestVisBufferShadesTheForwardPicture|TestWindBends'                ok (159 s)
FOUNDRY_GL_TEST=1 go test ./internal/render/ -run 'TestVSM|TestFoliage|TestNaniteCutout|TestDecal'   ok
FOUNDRY_GL_TEST=1 go test ./internal/gfx/...                           ok
```

## Files

New: `internal/grassfield/{grassfield,pack,codec,falloff,thin}.go` + test;
`internal/procgen/place/grass.go` + test;
`internal/procgen/stages/{grass_field,grass_field_run,environments_grass}.go`
+ `grass_field_test.go`, `environments_grass_test.go`;
`internal/polyhaven/phimport/plant_parts.go`;
`internal/world/volume_grass.go` + test; `internal/gfx/grass_settings.go`;
`internal/render/{grass_field,grass_field_shadow}.go` + `grass_field_test.go`,
`grass_field_gl_test.go`, `surface_fx_noise_gl_test.go`;
`internal/pack/grass_refs.go` + `refs_grass_test.go`;
`resources/shaders/grassfield_{common,vert,frag,shadow_vert,shadow_frag}.glsl`.
Changed (additive): `world/{level_schema,volume,volume_context}.go`,
`editor/editor_volume.go` (duplicate), `editor/editor_yaml_docs_gen.go`
(regenerated), `render/{init,world_frame,shadow,vsm,vsm_track}.go`,
`app/{graphics_settings,bench}.go`, `script/script_props_level.go`,
`pack/refs.go`, `layering_test.go`; shaders `surface_fx.glsl` (precise),
`vsm.glsl` (vsmShadow1), `biome_light.glsl` (one-tap switch); presets
`environment_{prairie,alpine,coast,pine_forest,tundra,wetland}.go`,
`environments_builtin.go`, `environments_shared.go` (rock_surface layer),
`canyon_savannah_test.go`. Docs: `docs/guide/33-generators.md`
(grass_field), `13-volumes.md` (grass fields), `35-regions.md`.

## Gaps and honest notes

- **Prairie cost is at the top of the target**: ~4 ms of frame at a ground
  view (3.5 grass + 0.3 shadow + VSM movers). It is vertex-bound; next
  steps if needed: per-instance lighting (a compute pre-pass), smaller
  cells (cells straddling a tier boundary draw both meshes), lighter LOD0
  for the mid clumps.
- **Disk**: the prairie is ~4.8 M clumps per km² — ~58 MB of fields per
  km². A 3×2 km showcase with ~1 km² of grassland is fine; a world of
  prairie would want a coarser density or compression.
- **Grass from altitude is thin**: from 40 m up the clumps are vertical
  cards seen from above, so the ground texture carries the colour (the
  prairie's was darkened to the sward's olive for that). No far "card" or
  impostor tier: past the range the texture is the grass.
- The lighting debug view still shows the clumps' albedo (only the tint
  goes through giDebugAlbedo).
- Pine forest: denser and with an understory, but fir_tree_01's thinned
  crowns stay sparse and its dead parts stand as bare poles; not a closed
  canopy. Over the 3,000/km² scenery target (~5,000).
- Alpine: rock colour and lattice fixed; the scree/turf blend still draws
  swirly contour bands in the valleys, and snow against pale scree is
  low-contrast.
- Coast: the cliff now reads; there is a second ledge in its face where the
  beach rise meets the cliff; dune grass is sparse sticks rather than tufts.
- The rectangles fix changes the look (not the character) of every
  existing surface that used macro variation, anti-tile or breakup noise:
  continuous where it was broken. Existing levels without grass otherwise
  render as before (tested for the grass pass; the shader fix is the one
  deliberate change).
- Native numbers for oak savannah, wetland and pine with grass were not
  taken (WSLg only). No editor UI beyond the generic param panel; nothing
  clicked in a running editor.
- Grass does not take decals, does not part for the player, and is not in
  the nav or collision (by design).
