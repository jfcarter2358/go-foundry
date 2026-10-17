# Phase 5: materials — what was built

Phase 5 paints the generated canyon. It adds:

- `.fmat` materials on Nanite scenery;
- seven world-space surface effects;
- a `terrain_material` stage that splats the Poly Haven layers by the terrain's masks;
- rock materials written by the placing stages: a ground blend, a colour match and desert varnish;
- a triplanar base for generated rocks;
- silhouette seating for open-backed cliff scans.

Verifying it found two engine bugs. Each would have hidden the whole phase:

- **Material loading.** The material library had never loaded a file.
- **Startup crash.** Once the library could load files, the startup path that uploads their textures crashed: it runs before there is a GL context.

The user-facing chapters are the "Terrain and rock" section of
[docs/guide/28-materials.md](../guide/28-materials.md), and the
`terrain_material` and "Rock materials" sections of
[docs/guide/33-generators.md](../guide/33-generators.md).

## Files

| Package | Files | What |
| --- | --- | --- |
| `internal/material` | material_kinds_terrain.go, material_resolve_fx.go (new); material_resolve.go, material_file.go edited; material_fx_test.go (new) | The seven kinds and `SurfaceFx`. `ScanMaterialFiles` now returns names, not file names (the bug below). |
| `internal/gfx` | material_fx.go, material_fx_textures.go (new); material_surface.go, material_textures.go edited | `SurfaceState.Fx`, the uniforms, the sampler rest/live units, the splat atlases, the data maps, `EnsureMaterialFxTextures`. `EnsureMaterialTextures` no longer uploads an effect's own maps as world textures. |
| `resources/shaders` | surface_fx.glsl (new); fragment.glsl edited | The effects; see "Shader" below. |
| `internal/render` | nanite_scenery.go, shadow.go, material_preview.go edited; material_fx_gl_test.go, material_fx_units_test.go (new) | The material in the Nanite group key and signature; the effects' samplers bound with the shadow samplers. |
| `internal/world` | scenery.go, scenery_share.go, level_schema.go | `Scenery.Material` / `material:`, `SlotMaterial`, a `Materials` slot that names a material; the classic path's `MaterialName`; fx textures loaded at placement load. |
| `internal/procgen` | params.go, fgen_file.go, placement.go, genlevel/splice.go | `ParamDef.Added` (hash-stable new params); `Placement.Material`. |
| `internal/procgen/stages` | terrain_material.go, materials_out.go, cliffwall_back.go (new); terrain.go, terrain_run.go, scatter.go, scatter_run.go, cliffwall.go, polyhaven.go, rocks.go, rocks_run.go edited; terrain_material_test.go (new) | The stage, material writing, back profiles, the new params. |
| `internal/procgen/place` | backprofile.go (new); wall.go, place.go edited; backprofile_test.go (new) | `MeasureBack`, silhouette seating. |
| `internal/procgen/rocks` | meta.go | `Manifest.Material`, `Manifest.MatchGround` (additive, version unchanged). |
| `internal/app`, `internal/harness` | app.go; cmd_render.go, shot.go | Materials load before GL, their textures after `StandUpWorld`; the headless renderer loads materials too. |
| `internal/editor` | editor_generator_job.go; editor_yaml_docs_gen.go (regenerated) | After a run, rescan textures and reload materials. |
| `internal/pack` | refs.go (shared; one additive block) | A splat layer's `_height` and `_ao` answer to their albedo's name, so a release packs them. |
| `resources/materials` | desert_rock.fmat, desert_ground.fmat, rock_foot.fmat | Shipped examples, one per new effect (the shipped-materials test demands it). |
| docs | this file, guide 28, guide 33 | |

Shared files: `internal/pack/refs.go` got one additive block. The score
files and the other shared files were not touched.

## The effects

Each effect is a `material.RegisterKind` row, a resolver block, a field of
`material.SurfaceFx` (plain comparable values, so the vis pass's
`applied != *st` still works), uniforms, and a branch in
`surface_fx.glsl`.

| Kind | Params | Shader |
| --- | --- | --- |
| `triplanar` | `scale` (m), `sharpness`, `upright` | The surface's `tex` and `normalMap` projected along X/Y/Z and blended by `|n|^sharpness`. Side projections read left to right as seen and stand upright (v = −y), so bedding stays level, as terrain_frag's does. Normals are built in each projection's own (T, B) frame, made perpendicular to the geometric normal. |
| `splat` | `weights`, `bounds_min/max`, `layer1..4` with `_scale`, `_triplanar`, `_height`, `_tint`; `blend`, `normal_strength`, `ao`; `strata`, `strata_range`, `strata_strength`, `strata_warp` | Weights are RGBA over a world rectangle. score = w + h·height·0.35·min(4w, 1). A layer blends in by max(score − (best − blend), 0). Layers with w < 0.004 are not sampled. Triplanar layers are banded by a strata ramp read at a noise-warped world height. Layer AO feeds `fxAO`. |
| `ground_blend` | `ground`, `height`, `bounds_min/max`, `height_range`, `band`, `breakup`, `strength`, `contact` | The ground height comes from a 16-bit RG map: manual bilinear `texelFetch`, so neither filtering nor mips mix its bytes. The blend is (1 − smoothstep(0, band·noise, y − h))² toward the ground colour, plus a contact darkening over the last 20% of the band. It is applied after the vertex colour. |
| `streaks` | `strength`, `scale`, `length`, `color`, `overhang` | Vertically stretched fbm, across the face by its X or Z, on steep faces, multiplied toward the varnish colour; faces looking down are darkened. |
| `detail_normal` | `map`, `scale`, `strength`, `distance` | Triplanar `<map>_normal` (or the map itself), faded out by view depth. |
| `macro_variation` | `scale`, `strength`, `warmth` | Two fbm fields on the albedo's brightness and its red/blue balance. |
| `color_grade` | `match`, `target`, `source`, `hue`, `saturation`, `tint`, `brightness` | `match` multiplies by (target / luma(target)) / (source / luma(source)), with `source` defaulting to the texture's measured mean (`gfx.TextureMeanColor`). Then hue and saturation in YIQ, the tint with its luma divided out, and brightness. Every texel moves by the same ratio, so detail and lichen survive. |

### Shader rules, and why

- **Gating.** `matFx` is a bitmask written on every draw, 0 for surfaces without effects. Parameters and textures are written only when it is set.
- **Main-path changes for unaffected surfaces:**
  - normal mapping is skipped when the triplanar or splat bit is set;
  - the AO lighting branch is `vertexColors != 0 || fxAO < 1.0`;
  - `vertexAO *= fxAO` (= 1).
- **Both render paths.** There are no derivatives in the new code. Every sample is `textureGrad`, with gradients projected from main's `dPdx`/`dPdy` (`dFdx` in the forward pass, the analytic `nvDPdx` in the vis material pass), or `texelFetch`. The new code also has no `texture(tex,` calls and no `in` declarations, so `nanite_vis_source.go`'s rewrite is untouched and still checks everything.
- **Sampler budget.** The material pass had 23 samplers; it now has 30 of the driver's 32 (measured: `TestMaterialFxSamplersFitTheMaterialPass`). The splat's sixteen layer textures are packed into two 2×2 atlases: albedo with height in alpha, and normal with AO in alpha. Each tile has a gutter of 1/32 of its texels, wrapped around. Gradients come from the unfolded uv, so `fract` makes no mip seam.
- **Samplers rest on unit 0** (the albedo's; all are `sampler2D`) and are pointed at units 24–30 only while an effect draws (`fxSamplersLive` / `fxSamplersRest`, tracked per program and link). The first design used 2D arrays and fixed units. While chasing a WSLg frame-time difference I tried several sampler layouts. In the end, interleaved A/B runs showed no regression with any of them: the difference was the machine (below). The atlas and rest-unit design stays because it is legal GL and adds nothing to draws without effects.
- **Lighting.** `light_model.glsl` and the baker are unchanged. The AO is applied in the existing vertex-AO branch, and baked surfaces ignore it.

### The textures

- **Data maps** (weights, ground colour, height, strata ramp, detail):
  - decoded without image/draw's premultiply, because the weights' alpha is a weight;
  - RGBA8, never compressed, never mip-skipped;
  - the height map has no mips and NEAREST filtering;
  - clamped at the edges;
  - re-stat'ed once a second, and reloaded when a file changes.
- **Names.** A material names a map by texture name or by a resources-relative path without `.png` (`gfx.FxTexturePath`), so generators keep their maps in their own folders.
- **Splat atlases** are built on first use, decoded in parallel, and cached by the four layer names. Measured: 0.42 s for four 2k layers (atlas 4352²), about 200 MB VRAM for the pair, shown in F3 as `fx:splat:*`.

## Nanite scenery carries a material

- `Scenery.Material` (`material:`) is worn by every slot.
- A `materials:` slot entry that names a loaded material wears that material instead, and keeps the cooked texture.
- The GPU path's group key is (texture, material, smooth, colours). The signature hashes `SlotMaterial`, so a change regroups.
- The classic path sets `Geometry.MaterialName`.
- Placement load calls `gfx.EnsureMaterialFxTextures`, which also loads the material's own texture, because nothing else would.
- A material that resolves to no effects draws exactly as before.

## The two bugs

1. **The material library never loaded a file.** `material.ScanMaterialFiles` returned file names with `.fmat`, and both `LoadAll` and the material editor's list then passed them to `MaterialPath`, which adds `.fmat` again. Phase 4's log already said "loaded 0 materials". Fixed in `ScanMaterialFiles`, with a test.
   - **This changes existing content:** any level naming a material now gets it. That was the documented behaviour, but no level has ever actually rendered with one.
2. **Startup crash.** `app.Run` called `gfx.LoadMaterials()` before SDL and GL existed. It had been harmless only because of bug 1: with a material present, `GenTextures` crashed (SIGSEGV). Now the documents load early and `EnsureMaterialTextures` runs after `StandUpWorld`. The headless renderer (`foundry render`, shots) now loads materials too.

## The generators

- **`terrain_material`** (new, Materials). Its inputs are a terrain, a `polyhaven_textures` stage, and four layer names (sand, cracked, gravel, rock; rock defaults to `cliff`).
  - **Weights, per sample**, in priority order: rock first, then gravel, then cracked, with sand taking the rest. Edges are noise-bent, and the result is blurred by one sample.
    - Rock: smoothstep of the slope over 32–46°, or the cliff mask.
    - Gravel: the flow mask blurred over `wash_width`.
    - Cracked: flat × (1 − sand) × patch noise.
  - **Outputs:**
    - `weights.png`, `ground.png` (layer means × weights) and `height.png` (16-bit), all over the terrain rectangle, with texel centres on the samples;
    - `strata.png`, a 256-texel ramp of the terrain's strata colours as tints, clamped to 0.7–1.3;
    - `terrain_material.yaml`, the artifact: maps, rectangle, height range, ground mean and coverage;
    - `materials/gen_<doc>_<id>.fmat`: splat (rock triplanar at 6 m), macro variation and streaks.
  - `layer_match` tints the cracked, gravel and rock layers toward the sand's chroma.
- **`terrain`** gets `material` (a string, `Added`). The tiles wear it.
- **scatter (per rule) and cliff_wall** get `ground` (a terrain_material stage), `match_ground` (−1 means the source's), `ground_band` and `streaks` (`Added`). They write `gen_<doc>_<stage>_<rule|chunks|ledges>`: the source's base material plus ground_blend, color_grade (target = ground mean) and streaks. Placements wear it. Plants get nothing.
- **polyhaven_rocks** gets `match_ground` (default 0), stored in the manifest.
- **rocks** (generated) gets `textures`, `texture_layer` (`cliff`) and `triplanar_scale`. It writes a base material (the layer's albedo, triplanar) as `Manifest.Material`, and the gallery placements wear it.
- **Materials are rewritten** when their stage runs, with a header naming the stage. They live in `resources/materials/`, outside the stage folder, so they are not in the manifest.
- **`ParamDef.Added`**: a parameter at its default is left out of the stage hash, so existing generators are not made stale by the new params (tested). cliff_wall's Version is bumped (6 → 7), because its seating changed.

## Seating open-backed scans

`place.MeasureBack` builds a 24² z-buffer of the chunk's cooked fallback mesh, seen from behind in its wall frame.

- A cell is **closed** (gap 0) where the first surface hit faces the wall, and **open** (gap = depth past the box's back plane) where it faces away: that is the inside of an open shell.
- The **outline** is the covered cells with an empty neighbour, leaving out the bottom row.

Measured on the live scans:

| Variant | Size | Rim cells open | Rim gap median | Rim gap max |
| --- | --- | --- | --- | --- |
| namaqualand_cliff_01 | 8.5 × 5.0 × 4.4 m | 66/71 | 2.26 m | 4.21 m |
| namaqualand_cliff_02 | 20.2 × 7.2 × 6.9 m | 68/71 | 2.94 m | 6.82 m |
| rock_face_01 | 5.0 × 3.6 × 3.9 m | 66/68 | 1.37 m | 3.80 m |

So the rim stands at about half the depth: that is the phase-4 gap.

**How seating uses it.** `seatChunk` takes each outline cell's need: face at the cell's height (at the nearest of the three transects) + Inset/2 + gap·scale − lean. The least the back may go in becomes the `BackCover` quantile of those needs (0.6). If that is more than `MaxBackGap`·depth (0.75) past the box's need, the next variant is tried, up to three, and the tier is dropped if none fits.

**What I tried before the defaults:**

- The whole rim (max): 91 of 199 columns dropped.
- The 85% quantile: kept 300 chunks, but buried them until only their forward lips stuck out, as floating slivers.

**What the current defaults give:** 300 chunks, none reseated or dropped (`seat_cmp.png`). The stacks sit into the wall, with smaller dark gaps behind and beneath than box seating. One lip-like sliver per view remains. `seat: box` restores phase 3's seating.

## Verification (phase-4 scratch project `$S/p4`)

`$S` = `/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/8b1c0d12-038d-4935-9ad4-2bf2380ff5b6/scratchpad`.

**The generator** (`p4/resources/generators/canyon.fgen`; the phase-4 version is saved as `$S/canyon_p4.fgen.orig`) gained:

- a `polyhaven_textures` stage, `tex` (offline; the cache served it);
- a `terrain_material` stage, `ground_mat`;
- `material: gen_canyon_ground_mat` on the terrain;
- `ground: ground_mat` on scatter and walls;
- `match_ground` 0.6 on the boulder rules and 0.7 on the walls.

**Run times:**

- Full run: 5.1 s at 250 MB peak, nearly all of it the terrain rerunning for its new `material` value.
- Rerun of ground_mat + walls + scatter: 0.3–0.8 s.
- ground_mat alone: 0.6 s (513² maps).
- Coverage: sand 36%, cracked 3%, gravel 14%, rock 47%. This terrain is steep: median slope 37°, a quarter of it cliff.

**Renders** (`$S/shots5/<name>/pose_0.png`, 1920×1080, TAA, visibility buffer, VSM):

| Shot | Pose | What |
| --- | --- | --- |
| `fin_wall1` | −25, 32.5, 1.9, 1.962, 0 | Canyon wall close-up: colour-matched stacked scans over the rock_face heightfield wall. |
| `fin_field2` | −97, 12.3, 82.6, 0.785, −0.209 | Canyon floor and wash: gravel bed, sand fans, triplanar rock on the slopes, boulders. |
| `fin_over1` | −60, 70, −60, 0.785, −0.45 | Overview: rock walls, sand in the fans and floors, a faint strata banding. |
| `fin_seam` | 15.98, 19.11, −13.96, 2.070, −0.25 | A boulder on sand: its foot takes the sand's colour, with a contact darkening. |
| `fin_wash` | 29.39, 3.86, 16.18, 2.070, −0.168 | Pebbles in a gravel wash, by a sunlit sand slope. |
| `fin_wall3` | −15.5, 36.5, −21.2, 1.962, −0.12 | A sand slope under rock walls. |

Composites:

- `sheet5.png`: four of the above.
- `match_cmp.png`: one boulder at match 0, 0.6 and 1. That scan is already orange, so the match barely moves it, which is correct.
- `wall_match_cmp.png`: wall chunks at match 0 (grey-olive Karoo granite), 0.7 and 1 (warm red sandstone). The detail and variation are kept, and it does not read as paint.
- `seat_cmp.png`: box (left) against silhouette (right), front and side.
- `wview.png`: the weights in false colour, beside the ground map.

Compare phase 4's `shots4/sheet2.png`, where the terrain is one flat brown.

**Iterations found by looking:**

1. The default `rock` layer (red_sandstone_wall) is masonry. It drew brick walls on slopes and pale green-white strata bands. Rock now defaults to `cliff`, and the strata tint is clamped and weaker.
2. The flow mask's one-sample channels drew dotted gravel lines. Washes are now the blurred flow alone, and the weights get a one-sample blur.
3. The cracked scan was olive. `layer_match` tints the layers toward the sand.
4. Height blend 0.12 cut layer edges like decals. It is now 0.3.
5. Rock at 2 m a repeat read as corduroy from 100 m away. It is now 6 m.
6. The seating quantile; see above.

**Frame cost** (WSLg, Mesa d3d12 on the GTX 1070, p50, 90 frames):

| View | Phase 4 binary, phase-4 scene | Phase 5, painted scene |
| --- | --- | --- |
| wall3 | 56.7 ms | 60.0 ms |
| over1 | 86.9 ms | 90.7 ms |
| field2 | 31.6 ms | 32.8 ms |

| Gallery level (no materials), interleaved A/B | Phase 4 binary | Phase 5 binary |
| --- | --- | --- |
| run 1 / 2 / 3 | 24.8 / 25.4 / 25.0 ms | 25.6 / 25.1 / 23.9 ms |

- **No measurable cost on unmaterialed content.**
- **About 1–4 ms on the painted canyon** (the splat's up to 16 fetches per pixel). The terrain also changed between the two canyon columns.
- **WSLg flips between two modes.** Earlier the gallery measured 21 ms on both binaries; later 25 ms with a one-second stall frame on both. Only interleaved runs can be compared.
- GPU timers read 0 on Mesa d3d12, as in earlier phases. Judge on the 1070 under Windows.

## Tests

| Package | What |
| --- | --- |
| material | Every kind has a shipped example and validates. Splat and ground blend need their maps to resolve. Defaults; a round trip; layer param names. `LoadAll` finds files by name. |
| render (GL) | `TestMaterialFxOnEveryPath`: eight rocks (plain, splat, triplanar, grade, ground blend, streaks, macro, detail), each checked for its effect (red splat, blue triplanar, green match, green foot and plain top, a darker underside, a change from plain), on the vis page pool, vis flat pool, forward and classic paths, which agree within 14/255 (in practice within 0.1). `TestMaterialFxSamplersFitTheMaterialPass`: the material program builds with 30 samplers, 7 of them the effects'. `TestVisMaterialProgramBuilds` and the colour test still pass. |
| render | `TestFxUnitsAreFree`: the effect units collide with no other world sampler. |
| procgen/stages | `TestTerrainMaterialAndRockMaterials`: on the fake Poly Haven, the terrain_material stage end to end. Coverage sums to 1. Weights sum to 255 per texel and are NRGBA. The height map round-trips the terrain to 1 cm. The splat material resolves (weights path, triplanar rock, macro, streaks). Tiles wear it; boulders and wall chunks wear ground blend plus the configured match, and keep the scan's texture; plants wear nothing. `TestAddedParamsKeepOldHashes`. |
| procgen/place | `TestMeasureBackFindsTheOpenRim` (side rim gaps match the shell's curl; a back facing the wall is closed). `TestSilhouetteSeatingBuriesTheRim`: box < default silhouette ≤ whole rim. |

### Results

- `go build ./...` and `go vet ./...` are clean.
- `go test` passes for material, gfx, world, render, procgen/..., pack, levelio, app, harness, biome, and internal (layering).
- editor passes except the score session's `TestScoreSibeliusKeymap` and `TestScoreRebinding`. `TestYAMLFieldDocsUpToDate` passes after regenerating for `material:`.
- GL (`FOUNDRY_GL_TEST=1`):
  - gfx passes;
  - render passes except the recorded `TestNaniteFoliageDrawsTheClassicNearField` (421 s run);
  - editor's Generator, Poly Haven and Material tests pass.

## Known gaps

- **Triplanar is world-space only.** A moving or rotating placement swims through its texture. That suits static scenery, which is all the canyon has.
- **The splat ignores the terrain's strata dip.** The terrain's default dip is 0.
- **Splat atlases are uncompressed**: about 200 MB per four-layer 2k set. A BC7 path, `texture_compression` and a rebuild on the skip-mips setting are not done; skip-mips is read only when an atlas is first built.
- **They build at startup** for every loaded material, whether or not the level uses it (0.4 s and the VRAM).
  (fixed in phase 7: atlases build only at level load or first draw, and go up as BC7 with `texture_compression` on, 193 → 48 MB, skip-mips dropping cached levels.)
- **Mip bleeding** across atlas tiles is possible below the gutter's mip level (very distant only).
- **The terrain shows blotchy rock-and-sand camouflage** on mid slopes (32–46°) seen from far off. The edge noise and the colour contrast between rock_face and sand cause it; it is tunable (`breakup`, `rock_slope`), but no default is right for every terrain.
- **One lip sliver per wall view** remains. That is the `BackCover` trade-off.
- **Generated rocks' triplanar material** is tested through the stage only. No render was taken: the p4 project has no generated rocks.
- **The editor's material panel** draws the splat's 25 parameters flat. That works but is long; there is no layer-grouping UI.
- **Rerun materials are picked up** by the editor after a run (`material.LoadAll`). The game picks them up on load only, as before. Live editing in the material editor works for all the effects except the atlas, which follows its file stamps.
- **Interaction with fixed bug 1 is untested** on the user's own levels that name materials: the shipped ones now actually load.
