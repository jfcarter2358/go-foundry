# Phase 6: atmosphere — what was built

Phase 6 gives the canyon its air. It adds:

- an **atmosphere** in the level's `fog:` block, off unless asked for:
  - exponential **height fog**, integrated in closed form along the view ray;
  - **aerial perspective**, with a Rayleigh-like per-channel extinction;
  - the **sun's in-scattering**;
  - the same integral on **sky** rays, so the horizon haze matches the ground's;
- a `dust_haze` **cloud preset** for the existing volumetric march;
- an **`environment` generator stage**. It writes fog, sky and sun into the target level through the splice. The splice gained **level settings** for this.

It also **fixes the live-GI black patches** that phase 2 found on large cooked meshes.

The user-facing chapters are:

- "The atmosphere" and "Presets" in [docs/guide/08-sky-and-fog.md](../guide/08-sky-and-fog.md);
- `environment (Level)` and "Level settings" in [docs/guide/33-generators.md](../guide/33-generators.md).

## Files

| Package | Files | What |
| --- | --- | --- |
| `resources/shaders` | fog.glsl, scene_block.glsl, sky_frag.glsl, light_model.glsl | The atmosphere (`fogExpDepth`, `fogSunLobe`, `fogAtmosSegment`, `fogAtmosAt`) inside `applyFog`, `applyFogAdditive` and `applyFogModulate`; mode 4 (`none`); sky haze; the GI fix. |
| `internal/gfx` | scene_block.go | 15 atmosphere members appended to `SceneBlock` (3856 → 3968 bytes). |
| `internal/world` | fog_atmosphere.go (new), fog.go, cloud.go, level_schema.go; fog_atmosphere_test.go (new), environment_test.go | `FogHeight`/`FogAerial`/`FogSun`, `FogNone`, the YAML, `packAtmosphere`, `FogTransmittance`, `CloudLookPreset` + `RawCloudLook.Preset`. |
| `internal/render` | fog_atmosphere_gl_test.go, gi_thick_gl_test.go (new); sky_fog_shader_gl_test.go | GL tests: the atmosphere against its closed forms, the GI regression and diagnostics, the atmosphere members exposed by every shading program. |
| `internal/procgen` | placement.go, run.go, context.go | `LevelSettings`, `RunContext.SetLevel`, `StageResult.Level`, `Result.LevelSettings()`, carried in `placements.yaml` (`level:`). |
| `internal/procgen/genlevel` | settings.go (new), splice.go | `SpliceSettings` (fog, sky, view_distance, sun, ambient; strict), `Options.Settings`, `Stats.Settings`. |
| `internal/procgen/stages` | environment.go, environment_test.go (new) | The stage. |
| `internal/procgen/gencli`, `internal/editor` | gencli.go; editor_generator_splice.go, editor_environment_panel.go, editor_yaml_docs_gen.go (regenerated) | The settings are passed to the file splice. The Fog panel has an Atmosphere section. |
| docs | this file; guide 08 and 33 | |

No shared file was touched except the regenerated YAML docs, which is generated output. The score paths were not touched either.

## The atmosphere

### Fields

```yaml
fog:
  mode: none            # new: no distance fade, only the atmosphere
  height:  {density, base, falloff, color}
  aerial:  {distance, color, tint, scale_height, base}
  sun:     {strength, anisotropy, color}
  sky_haze: 0..1        # default 1
  sky_distance: m       # default 20000
```

- **Each block is off when it is absent.** A part with no colour takes the fog's colour.
- **A part switched off keeps its numbers** (`disabled: true`), as the fog does.
- **Saving changes nothing for old levels.** A level without the blocks saves byte for byte as before (tested). `Fog` stays a comparable value type.

### The shader (fog.glsl)

**Two exponential media share the ray.** One is the height fog. The other is the aerial haze, with its own scale height. For each, the optical depth over the segment is the exact integral:

```
σ·len·(e^(−h0) − e^(−h1)) / (h1 − h0),  where h = k·(y − base)
```

- On a level ray (h1 = h0) this becomes σ·len·e^(−h0).
- The exponents are clamped only where e^ would overflow, deep below the base. They are never clamped on the side that would let a climbing ray see more fog than there is. A first draft clamped h1 and overstated an upward sky ray by about 125×.
- The aerial extinction is `tint` per channel.

**How the result is formed:**

- T = max(e^(−τ), 1 − maxOpacity).
- The medium's colour is each part's colour weighted by its share of τ, plus the sun lobe.
- The result is `surface·T + colour·(1 − T)`.

**The sun lobe** is Henyey-Greenstein relative to isotropic, toward the level's first switched-on directional light. It is applied as `sun·strength·(lobe − 1)` and clamped at 0. This is energy-neutral: the haze glows toward the sun and dims a little away from it, and averages its own colour. The first version added the whole lobe, and its sky went white over half the screen (`$S/shots6/sheet_a1.png`).

**Order of the terms:** volumes, then the atmosphere, then the legacy distance fade, outermost.

**Additive and modulate blends** take T: a spark is swallowed, and a decal fades to white.

**The sky** (sky_frag) integrates the same segment out to `sky_distance` along its ray and mixes in `sky_haze` of it. A level ray through the haze becomes the haze colour. Straight up takes σ·H. With `sky_haze: 0` the sky is untouched.

**Gating.** `fogAtmos` is 0 for every level without the blocks. That costs one uniform branch per fogged pixel. Nothing changes for those levels: the gallery level's render matches the pre-phase image except 15 TAA-noise pixels (≤5/255). Mode 4 also skips the old `skyFogBlend`.

### Every pass

Everything goes through `applyFog`, `applyFogAdditive` and `applyFogModulate` in fog.glsl, and through `fogAtmosSegment` in sky_frag. So the passes that inject or include fog.glsl all get it:

| Pass | Shader or program |
| --- | --- |
| World | fragment.glsl, and its detail and tessellation reuses |
| Nanite visibility-buffer material pass | splices fragment.glsl |
| Terrain | terrain_frag.glsl |
| Biome scatter | biome_frag.glsl |
| Biome ground patches | ground_frag.glsl |
| Foliage and Nanite foliage | foliage_frag, foliage_imp_frag, nfoliage_frag, nfoliage_voxel (compute) |
| Sprites and particles | sprite_frag.glsl |
| Volumetric march | `applyFog` at the mass's mean depth, before compositing |
| Sky | sky_frag.glsl |

**No samplers and no derivatives were added.** The visibility-buffer rewrite is untouched, and the material pass's sampler count stays at 30 of 32. `TestVisMaterialProgramBuilds`, `TestMaterialFxSamplersFitTheMaterialPass` and nanite_vis_source_test pass. `TestEveryShadingProgramGotTheFog` and `TestTheWorldShaderGotTheFog` now also check the atmosphere members.

### Script props and editor

**Script props.** The level's distance fog has no script props or easing today; only fog *volumes* have them. So, as the spec allows, none were added (see Known gaps).

**Editor.** The Fog group has an Atmosphere section: a switch per part, its fields, and the sky haze.

## Dust haze preset

`cloud: {preset: dust_haze, ...}` starts a cloud from a named look (`world.CloudLookPreset`; `fair` is the default):

| Field | dust_haze value |
| --- | --- |
| Colour | warm tan 0.86/0.74/0.58 |
| Coverage | 0.8 |
| Density | 0.006 |
| Scale | 90 m |
| Detail | 25 m |
| Erosion | 0.15 |
| Wind | 3, 0, 1 |
| Absorption | 0.25 |
| Ambient | 1.6 |
| Shadows | none |

- Keys written beside the preset override it.
- A save writes the numbers, not the preset's name.
- It runs in the existing volumetric march; there is no new march.

## The `environment` stage

**What it writes:**

- level settings `fog`, `sky` (`sky_canyon` cubemap, gradient, or keep) and `sun`;
- optionally `view_distance`;
- with `dust`, one box volume `gen.<g>.<id>.dust` over the terrain, at the dust_haze preset.

**Heights** are measured from the named terrain's lowest point.

**The desert defaults** were tuned by looking (iterations below):

| Part | Defaults |
| --- | --- |
| Distance fog | `mode none`, max opacity 1 |
| Height fog | 0.006 /m at the floor, falloff 0.12 (e every 8 m), warm dust 0.84/0.74/0.62 |
| Aerial | 1100 m, 0.80/0.78/0.74 (cubemap horizon), tint 0.75/1/1.35, scale height 300 m |
| Sun glow | 0.05, g 0.7 |
| Sky | sky_haze 0.6; sky canyon |
| Sun | yaw 50, pitch −30, 1/0.95/0.85 |
| Dust | off |

**The preview** is notes: the share of a surface left at 50, 100, 250 and 500 m at two heights, and per channel at 500 m.

**How the splice handles level settings** (genlevel/settings.go):

- The keys are fog, sky, view_distance, sun and ambient. Each is decoded strictly through the level schema, all of them before anything is written. A misspelt key or an unknown level key fails the whole splice (tested).
- Fog, sky and view_distance are replaced.
- **sun** sets the first directional light's yaw, pitch, direction, colour, brightness, shadows and strength. It keeps the light's ID, script and bake switches. If the level has no directional light, it makes one.
- **ambient** sets the global light's colour and brightness.
- Settings ride `placements.yaml`, so a skipped stage still supplies them.
- `Stats` prints `settings fog sky sun`.
- A removed stage leaves its last values in the level. Settings have no prefix to say they were generated.

## The live-GI black patches

### Reproduction

- **Scenes.** In `$S/p4`, the gallery level (namaqualand_cliff_01/02, rock_face_01) and a new `gitest` level (`generators/gitest.fgen`). gitest has two generated 300k-triangle hoodoos (`rocks` preset pillar, 1 min 25 s to make).
- **On screen.** The patches are small and the half-resolution GI and TAA soften them. The clearest is a black sliver on the canyon wall behind the gallery cliffs (`gi_crop_62_12.png`, left), which is present in every phase-4/5 gallery shot.
- **Diagnostic.** I wrote a compute probe that evaluates the shading's own `giIrradiance` (light_model.glsl) at points over every face of a mesh. It is `TestGIThickCookedMeshes`, run with `FOUNDRY_GI_FNM`.
  - namaqualand_cliff_02: **183 of 4774** surface points had irradiance exactly (0, 0, 0) at cover 1.00.
  - cliff_01: 8 of 990.
  - The hoodoos and plain boxes, as brushes or as mesh grids: 0.

### Root cause

The bad points were answered by **cascade 1** (4 m spacing).

1. **All eight probes were refused by the plane test.** A debug probe of the eight probes around a bad point showed all eight alive, in open air, with sensible irradiance. Every one had been refused by the nearest-surface plane test.
   - That test was added for level1's thin hangar deck. It skips a probe when the point lies behind the plane of the surface nearest the probe.
   - On a rugged scan, every probe of a coarse cell is within half a spacing of *some* knob or ledge. The plane through that bump puts points on other parts of the same side "behind" it.
2. **The cascade then claimed full cover with nothing.** With every probe refused, `giSampleCascade` returned 0. `giIrradiance` still counted the cascade's full cover.
3. **The result was black.** `ambientLight` then gave `0 + sky·mix(1, giAmbientFloor = 0, 1)`, which is exactly black in shadow.

The same zero is reached when the cell's probes are all *buried* inside thick geometry, combined with plane refusals.

**Ruled out:** the SDF sign (the flood fill is correct on these meshes), the mesh-SDF transform, probe relocation, and leaks.

### The fix (light_model.glsl only)

1. **`giSampleCascade` returns `found`.** It is 0 when no probe passed. `giIrradiance` multiplies the cascade's weight by `found`. A cell with no usable probe now falls through to the next cascade, the far field, or the flat ambient, instead of a confident zero.
2. **The plane test needs facing too.** It now applies only where the point's own normal faces away from the probe's surface (`dot(n, plane.n) < 0`). That is the thin-floor case it exists for: the top of the deck the probe is under. A point facing the same way is round a bump, and the Chebyshev visibility test decides it.

Each change alone (measured on the real cooks):

| Variant | cliff_02 least irradiance | Black points |
| --- | --- | --- |
| `found` alone | 0.179 | none |
| facing alone | 0.000 | 1 black point left (buried plus refused) |
| both | 0.233 | none |

**The baker does not change.** This is GI probe *sampling*, which the bake has no part in. The light_model terms the baker mirrors (wrap, hemisphere ambient) are unchanged.

### Regression test

`TestGIThickRuggedTopIsNeverBlack` shades the top of a 24×7×24 m rugged slab (a knobbed heightfield, as a cooked mesh's 64³ distance grid), seen from 30 m away.

- Against HEAD's light_model.glsl: **146 of 4251 points black** (fails).
- With the fix: 0, least 0.365.

Also:

- `TestGIThickBlockFacesAreNeverBlack` (hoodoo and cliff-sized blocks as brushes and as meshes) passes before and after.
- The thin-floor test `TestGIProbeUnderAThinFloorKnowsTheFloorIsBetween` still passes.
- With the fix, the real cooks have 0 black points (cliff_02 least 0.233, cliff_01 0.191, rock_face_01 0.274, hoodoo 0.311).

### Renders

- **`$S/shots6/gi_crop_62_12.png`**: the gallery wall's black sliver, gone after the fix.
- **`gi_crop_over.png`**: the canyon overview. A black patch under a cliff_wall chunk's overhang, and a dark chunk face, are lit after the fix. In that view 8,209 pixels brightened by more than 30/255.
- **`gi_cmp_62_12.png`, `gi_cmp_75_8.png`**: full frames, before above after.

## Verification in `$S/p4`

`$S` = `/tmp/claude-1000/-home-jfcarter2358-personal-code-main-foundry/8b1c0d12-038d-4935-9ad4-2bf2380ff5b6/scratchpad`. Renders are `$S/shots6/<name>/pose_0.png`, at 1920×1080 with TAA, the visibility buffer, VSM and GI Medium. The script is `$S/bench6.sh`.

**Changes to the scratch project:**

- canyon.fgen gained `- stage: environment, id: air, params: {terrain: ground}`. The phase-5 copy is `$S/canyon_p5.fgen.orig`.
- The run took 11 ms for the stage (322 ms for the whole document, everything else up to date) and spliced `settings fog sky sun`.
- The phase-5 level is kept as `$S/canyon_p5_level.yaml.orig`.

**Poses:**

| Shot | Pose | Before | After |
| --- | --- | --- | --- |
| Overview at distance | −150, 105, −150, 0.785, −0.32 | `before_OVER` | `after_OVER` |
| Canyon floor, looking down-canyon | 45, 6.5, −92, 1.116, −0.02 | `before_FLOOR` | `after_FLOOR` |
| Toward the sun | 90, 14, 12, −2.257, 0.30 | `before_SUN2` | `after_SUN` |
| Wall close-up (unchanged, as it should be) | −25, 32.5, 1.9, 1.962, 0 | | `after_WALL` |
| Dust haze on | | | `dust_OVER`, `dust_FLOOR` |

Composites: `sheet_before_after.png` (before left, after right), `sheet_after.png`, `sheet_dust.png`.

**Iterations found by looking:**

1. **a1** (`sheet_a1.png`): aerial 900 m, warm pink-beige haze, sun lobe 0.25 added on top, sky haze 1.
   - The overview's sky and the cubemap's landscape went to one flat beige.
   - The toward-the-sun sky went white across the top half.
2. **a2** (`sheet_a2.png`), now the defaults:
   - the energy-neutral lobe at 0.05;
   - the haze colour taken from the cubemap's measured horizon (≈0.82 grey, a little warm);
   - aerial 1100 m with scale height 300 m;
   - sky haze 0.6.
3. **The result:**
   - The far mesas and plains fade into a pale, faintly blue-grey haze, so the canyon reads deeper.
   - The floor carries a warm dust haze that thickens down-canyon.
   - Toward the sun the sky brightens round the sun and the far notch hazes; the shadowed walls stay dark rather than going grey.
   - Close-ups are unchanged.
   - No grey soup: the near ground keeps its full contrast.

**Frame cost** (WSLg, Mesa d3d12 on the GTX 1070, p50 of 60 frames):

| View | Before (no atmosphere) | After (environment defaults) | With dust |
| --- | --- | --- | --- |
| Overview | 149.6 ms | 149.1 ms | 151.7 ms |
| Floor | 77.0 ms | 77.4 ms | 79.8 ms |
| Toward the sun | 58.2 ms | 58.4 ms | |
| Gallery, no atmosphere (GI fix only) | 24.0 ms | 25.7 ms | |

- **The atmosphere costs nothing measurable.** It is a few exponentials per pixel.
- **The dust haze costs about 1–2.5 ms.**
- **The gallery difference is WSLg's run-to-run swing.** Phase 5 recorded the same swing; only interleaved runs compare.
- **The overview is slow (150 ms) with or without this phase.** The wide view of 1,540 placements plus GI is what costs; it is not the atmosphere.
- GPU timers read 0 on Mesa d3d12. Judge on the 1070 under Windows.

## Tests

| Package | Tests |
| --- | --- |
| world | `TestFogWithoutAnAtmosphereIsUnchanged`: an old block reads and writes back with no new keys and packs `fogAtmos 0`; `DefaultFog` has none. `TestFogAtmosphereRoundTrips`: mode none, defaults and fog-colour inheritance, switched-off parts. `TestFogAtmospherePacksTheSun`: density and falloff, the first lit sun, no sun, switched off. `TestFogAerialTintTakesBlueFirst`. `TestCloudDustHazePreset`. The existing `TestFogRoundTripsThroughItsRawForm` was adjusted for the new fields' defaults. |
| render (GL) | `TestFogAtmosHeightFogIntegratesTheExponential`: level rays at three heights, up and down a 30 m column, off. `TestFogAtmosAerialTintsTowardBlueWithRange`: per channel, grey haze, the cap, additive. `TestFogAtmosSunGlowsTowardTheSun`: toward, away, the sun's colour, no sun. `TestFogAtmosHazesTheHorizonNotTheZenith`: the sky, at sky_haze 0. `TestGIThickBlockFacesAreNeverBlack`. `TestGIThickRuggedTopIsNeverBlack` (the regression). `TestGIThickCookedMeshes` (diagnostic, `FOUNDRY_GI_FNM`). `TestSceneBlockMatchesTheShader` at 3968 bytes. |
| procgen/stages | `TestEnvironmentStageSetsTheLevelsAir`: end to end through the run, the rerun from file, the splice, the hand-made sun's ID kept, its direction from the angles, the dust volume and its preset. `TestLevelSettingsAreDecodedStrictly`. `TestEveryKindDocumentsItself` covers the new kind. |

### Results

- `go build ./...` and `go vet ./...` are clean.
- `go test` passes for world, procgen/..., levelio, gfx, pack, material, meshsdf, app, render, script, harness, and internal (layering).
- editor passes except the score session's `TestScoreSibeliusKeymap` and `TestScoreRebinding`. `TestYAMLFieldDocsUpToDate` passes after regenerating.
- GL (`FOUNDRY_GL_TEST=1`):
  - gfx passes;
  - render passes except the recorded `TestNaniteFoliageDrawsTheClassicNearField` (448 s run);
  - editor's Generator, Environment, Fog, Sky, Material and Poly Haven tests pass.

## Known gaps

- **No script props for the atmosphere.** The level's distance fog has no `@level` props or easing today, so none were added. A day-to-dusk script cannot ease the haze yet. Adding `fog_*` level props would be a small, separate addition.
- **The sun glow is unshadowed.** There is no light march, so a canyon in shadow still glows if you look toward the sun through it. It is mild at the default strength. God rays would need a march.
- **The sky haze is approximate.** The atmosphere is laid over a skybox whose own haze is painted in. `sky_haze` 0.6 is a judgement, and a different cubemap wants its own value.
- **A removed environment stage leaves its settings in the level.** A settings key has no prefix to say it was generated (documented in the guide).
- **The dust haze is a single box over the terrain**, so it does not follow the floor's shape. Banks drift over mesa tops when the box is deep. It is off by default for its cost.
- **The cloud preset's name is not written back on save** (the numbers are). The editor's cloud panel has no preset picker: it is YAML only.
- **GI fix: some side effects remain.**
  - The `found` fallback can give a point the next cascade's coarser light. That shows as a soft brightness step instead of a black hole.
  - The flat ambient is used only where no cascade has a usable probe.
  - Under-overhang points on very thin, rugged shells may still be dimmer than physical. The visibility test's crush is unchanged.
- **The scratch `gitest` generator and level stay in `$S/p4`** as the GI repro.
