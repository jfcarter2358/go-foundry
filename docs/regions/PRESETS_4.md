# Environment presets — step 4: the showcase's rendering bugs

Status: 2026-10-14. The four rendering faults PRESETS_3 left in the biome
showcase are root-caused and fixed in the engine, with tests; the showcase
was re-rendered before/after natively on the GTX 1070 (docs/canyon/PERF.md
method) and headless on WSLg, and every picture was looked at. Nothing in
the OneDrive project was regenerated or edited (no fix needed content
changes). Nothing was clicked in a running editor.

`$S` is this session's scratchpad,
`/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/9afc65f5-e4b2-4b3e-9788-2e06870a0755/scratchpad`.
PRESETS_3's contact sheets were no longer in it; the "before" pictures here
were re-rendered from the same level with the pre-fix code (a copy of the
tree with the changes reverted, cross-built as `foundry_before.exe`).

## What was wrong — four causes, not two

| Symptom (PRESETS_3) | Root cause | Fix |
| --- | --- | --- |
| Part of every fir trunk white natively, black on WSLg | **NaN normal**: `fir_tree_01`'s `trunk_c` primitive has no `TEXCOORD_0`, so every vertex cooks at UV (0,0); `fragment.glsl`'s `cotangentFrame` built the normal map's frame from UV derivatives — zero — and `inversesqrt(0) · 0` is NaN. NVIDIA drew the NaN light white, Mesa black | `cotangentFrame` builds a frame from N alone when the UV derivatives vanish (or are NaN) |
| White blocks over the scans from 600 m (pines, wetland, volcanic, tundra), white dashes on dune tile seams, white splats by the desert walls | **Visibility-buffer record overflow**: an ID names 2^20 records; the overview asks for **~1.19 M a frame** (read back on the 1070: 1,190,576–1,221,012). Dense numbering *dropped* every command and compute-rasterised cluster past the limit — nothing drew them, the colour target showed through white | Demand read back each frame (async, no stall); while it is near/over the limit, the compute rasteriser lists nothing and each pass's refused commands are turned round and **drawn the forward way** (`nanite_vis_overflow.go` + `nanite_vis_overflow_comp.glsl`) |
| Fan-shaped dark patch near the camera at 40 m (prairie), gone with VSM off | **Stale virtual-shadow-map pages**: the page pool (1024 MB, 5,461 slots) holds a third of the showcase's 19,459 pages, so at load most meshes are resident only at their DAG roots. A static VSM page drawn then kept the terrain tiles' root clusters — metres above the fine ground — as casters for good: the tracker hashed a placement's matrix and mesh pointer, never its residency | Per-mesh residency **band** (roots alone, then eighths of the rest), counted once it has held 30 frames, hashed into each placement's VSM signature (`nanite_stream_gen.go`, `vsm_track.go`) |
| Floating dark blobs over tundra/coast ground | **The same stale pages** (tundra ground pose and 40 m pose: the polygonal patches are root-level terrain, gone with the residency fix). Not the grass | as above |

Two more grass-shadow faults were found while checking 2b's suspects and
fixed; they were **not** the visible blobs (see "Honest notes"):
the grass caster box follows the camera, and the tracker handed in only the
current box, never the one the camera left (pages there kept old clump
shadows); and a field whose kinds do not cast (the tundra's, the coast's)
still marked a mover box every frame.

## Evidence

**Fir trunks.** Smallest case: a level of three firs (`$S/fir/resources/levels/firs.yaml`,
symlinked to the OneDrive assets): `$S/fir/shots/a.png` (tree c's trunk black on
WSLg) → `a_fix.png`. A scan of every cooked scan in the project
(scratch test, removed) found `fir_tree_01_c` slot `trunk_c` with all 4,576
level-0 triangles UV-degenerate, plus a few degenerate triangles in
coast_land_rocks_02, searsia_burchellii, rock_face_01 and coastal_cliff_02.
Materials, slots, vertex colours (all 255), textures (fine) and BC7 (off) were
ruled out on the way.

**White blocks.** Bisected natively at the 600 m overview: ground-only and
scenery-only levels were clean; `FOUNDRY_NANITE_SWRASTER=0` removed most
blocks, `_PX=8` most, `_PX=64` far more (`$S/p4/native/ab/ov_*`); the page
pool (`STREAM=0`) and barycentric clamping were not it. Marking compute-raster
records in the material pass (a shader hack) showed **no** compute-raster
pixel reached shading in the full level while a 101-scan test level shaded
them all — and a counter readback showed the frame over the record limit.
White pixels in the overview: **2,885 → 163** (what is left is single-pixel
dots on tile seams, below).

**Fan shadows.** Bisected natively at prairie 40 m looking down: present with
the scenery within 1,300 m, absent within 700 m; narrowed to the ground tiles
plus the desert wall sheets — and still present with the walls **moved 5 km
away**, absent with half of them (pool pressure, not the walls' geometry).
`FOUNDRY_NANITE_STREAM=0` and `FOUNDRY_NANITE_LOD=0` (no streaming) removed it;
`NANITE_SHADOWS=0`, a 6,000-page VSM pool, no rect budget, no draw bounds did
not. A build that invalidated every VSM page every frame removed it: stale
cache, confirmed. (Note: a first round of env A/Bs was void — `cmd.exe`
splits batch arguments on `=`, so `set "%3"` set nothing; redone with
name/value as separate arguments.)

## Before / after pictures

Native GTX 1070, 1280×720, user graphics.yaml, poses of `run_before/after`:
- `$S/p4/sheets/native_1.png` — overview 600 m, spawn 600 m, pine 40 m
- `$S/p4/sheets/native_2.png` — tundra 40 m, coast 40 m, prairie 40 m
- `$S/p4/sheets/native_3.png` — pine ground, tundra ground, prairie ground,
  prairie 40 m looking down
- frames: `$S/p4/native/{before,after}/pose_0..9.png`; A/B runs `$S/p4/native/ab/`

WSLg (Mesa d3d12, `foundry play run`, `-hz 10`): `$S/p4/sheets/wsl_1.png`,
`wsl_2.png` (frames `$S/p4/wsl/`). WSLg still cannot draw this level's
scenery colour pass correctly (cull command buffer 220 MB > Mesa's 128 MB
block, PRESETS_3), so pines are missing there before and after; its shadows
and grass are right after.

Looked at: trunks brown, no white blocks or dune dashes, no fan or tundra
patches; grass, ferns, water, cliffs, sky otherwise unchanged.

## Cost (native, `docs/regions/perf_presets4/`)

Steady state (300 warm frames, 60 measured): pine ground **68.6 → 69.1 ms**
p50, prairie 40 m **33.7 → 33.7** (`steady_before/after.txt`). The sun's VSM
cost is the same (0.77 / 0.67 ms GPU).

The overview pays for correctness: **97 → 147 ms** p50 — ~170 k overflowing
commands now drawn and shaded the forward way, plus a fix-up pass over every
command slot (`nanite: vis overflow`). Other poses in the 10-frame run after
each jump (`native_before/after.txt`) include the VSM redrawing pages as tiles
refine — work that used to be skipped (wrongly) — and vary run to run.

Two earlier versions were measured and replaced: hashing every residency
change took the pine forest's VSM from 0.7 to 38 ms (the pool thrashes ~16
pages a frame with the camera still); and `glClientWaitSync(…, 0)` on the
demand fence blocked ~15 ms a frame on NVIDIA (the readback now uses the
streamer's `readbackSlot`/`fencePassed`, as PHASE_3 documented).

## Files

New: `internal/render/nanite_vis_overflow.go`,
`resources/shaders/nanite_vis_overflow_comp.glsl`,
`internal/render/nanite_stream_gen.go`; tests
`normal_map_constant_uv_gl_test.go`, `taa_nan_gl_test.go`,
`nanite_vis_overflow_gl_test.go`, `vsm_track_grass_test.go`,
`vsm_track_stream_test.go`.
Changed (additive): `resources/shaders/fragment.glsl` (`cotangentFrame`),
`taa_resolve_frag.glsl` (`finite3`: a NaN/Inf pixel is read as black, never
blended — defence: on Mesa an unguarded NaN stayed one pixel, on NVIDIA
unmeasured), `render/nanite_vis.go` (demand note, overflow call, record limit
variable), `nanite_swraster.go` (no listing under overflow risk),
`nanite_stream.go` (`gen`/`band` fields, `noteDrawGen` in `flushRecords`),
`vsm_track.go` (residency hash; last grass box), `grass_field_shadow.go`
(casting fields only).

## Tests

```
export GOFLAGS=-p=6 GOMEMLIMIT=6GiB
go build ./...                                                    clean
go vet ./internal/render/                                         clean
go test ./internal/render/                                        ok
FOUNDRY_GL_TEST=1 go test ./internal/render/ -run 'TestNormalMapConstantUV|TestTAAResolveKeepsANaN|
  TestNaniteVisOverflow|TestVisBufferShadesTheForwardPicture|TestNaniteVis|TestNaniteSWRaster|
  TestNaniteStream|TestVSM|TestGrassField'                        ok
FOUNDRY_GL_TEST=1 go test ./internal/render/ -run 'TestNaniteCutout|TestNaniteDrawBound|TestNaniteScenery|
  TestNaniteColor|TestSceneBlock|TestMaterialPass…|TestVisMaterialProgramBuilds|TestTAA|TestFoliage|
  TestDecal|TestWindBends|TestSurfaceFx|TestSkyAndFog|TestMaterialFx|TestMaterialVariants|
  TestFxUnitsAreFree|TestChunkNanite|TestStaticNanite|TestNaniteLOD|TestNaniteLiveTiles|
  TestNaniteCompact|TestNaniteWind'                               ok (before the last overflow edit)
go test ./internal/editor/...     fails only TestScoreSibeliusKeymap, TestScoreRebinding (score session)
go test ./internal/ -run TestPackagesDoNotDependUpward            ok
```

- `TestNormalMapConstantUVLightsOnEveryPath`: a constant-UV card with a tilted
  normal map beside a control card, vis buffer / forward / classic: fails
  (0,0,0) with the old `cotangentFrame`, passes after.
- `TestTAAResolveKeepsANaNAPixel`: one NaN + one Inf pixel through 12 resolves:
  2 bad pixels before, 0 after.
- `TestNaniteVisOverflowDrawsWhatTheRecordsCannotName`: 16 cards with the
  record limit cut to 3 — the control loses them, the overflow pass matches
  the unlimited frame (0 pixels over 8 levels).
- `TestVSMTrackerRedrawsAPlacementWhoseStreamedMeshChanged`,
  `TestResidencyBand`, `TestStreamerMeshGen`,
  `TestVSMTrackerHandsInTheGrassBoxTheCameraLeft`,
  `TestGrassThatDoesNotCastHasNoShadowBox` (no GL).

## Honest notes / still wrong

- **Seam dots**: ~160 single white pixels along terrain-tile seams in the 600 m
  overview remain (also with the compute rasteriser off). Likely cracks
  between neighbouring tiles' independent LOD cuts; not root-caused.
- The first one or two frames of an overflow (before the readback lands) still
  lose commands; while overflowing, the compute rasteriser is off for the
  frame. The overview's 1.19 M commands are mostly DAG roots of 14 k scans —
  raising `nanite_lod_error` to 2 did not reduce them; a per-placement draw
  distance / impostor tier (PRESETS_3) is still what this world needs.
- A mesh whose pages the pool keeps trading never settles a band and keeps
  the shadow it was last drawn with (a level off, not a tile's roots).
  Chunk-Nanite and Nanite-foliage streaming are not hashed the same way.
- fir `trunk_c` now shades with one bark texel (its source has no UVs), and
  the importer ignores `KHR_texture_transform` (fir bark). Content, not fixed.
- The grass fixes are real but were not the cause of the blobs; scatter
  volumes have the same "box leaves reach" pattern (not changed).
- Pre-existing, untouched: OneDrive `levels/biome_showcase.yaml.bak` and the
  generator `.bak` from PRESETS_3; other sessions' `%LOCALAPPDATA%\Temp\foundry-*`
  copies. This session's `foundry-p4` copy was deleted; nothing left running.
