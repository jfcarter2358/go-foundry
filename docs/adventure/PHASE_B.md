# Phase B — light shafts and dark interiors

Status: done (uncommitted). Three pieces: the VSM ghost shadow found and fixed,
a shadowed in-scattering march (light shafts), and ambient volumes that darken
the sky light inside caves and slots when GI is off.

## 1. The ghost shadow (VSM)

**Symptom** (PHASE_A): with the virtual shadow map on, a moving body left a
copy of its shadow on lit ground where it had been, seen at a distance; the
cascades were clean; the tracker reported old and new boxes correctly.

**Root cause** — in the lookup, not the cache. `vsmSampleLevel`
(resources/shaders/vsm.glsl) returned `1 - lit/9`, and `-1` for "a page is not
resident"; `vsmShadow` walked to the next coarser level while the answer was
`< 0`. On a *fully lit* receiver the nine bilinear taps sum to 9 — or, rounded,
a hair over it (on this driver: every time), so `1 - lit/9` came out at about
`-5e-8` and was taken for "not resident". Every lit pixel therefore fell through
its own level and the next (the two the marks ask for) down the clipmap until a
level whose answer happened to be ≥ 0 — in practice a coarse page that nobody
requests. Unrequested pages are kept in the table even when stale (by design:
"out of date beats a hole"), and they are only redrawn when requested, so a
coarse page that held the body's shadow from a while ago kept it for ever, and
lit ground read it. Shadowed receivers (sum < 9) were unaffected, which is why
everything looked right except lit ground near where something had moved.
Found by instrumenting the pager (stale-shown unrequested pages per level), a
table that hid stale pages per level (the ghost went with levels 7–10 hidden),
then colouring receivers by the level their lookup ended on: lit pixels fell
through, the body's own (shadowed) pixels did not.

**Fix**: both lookups clamp to [0, 1] and the sentinel test is
`s > VSM_NOT_RESIDENT` (-0.5) instead of `s >= 0`. It affected every mover (and
cost every lit pixel two to nine wasted level lookups), not only the body.

**Test**: `TestVSMLitLookupNeverFallsThrough`
(`internal/render/vsm_lookup_gl_test.go`): a compute kernel runs vsm.glsl's
`vsmShadow` and `vsmShadow1` on 16k receivers over a table whose own level is
all lit and every coarser level all shadow; with the old shader 16384/16384
fell through, with the fix 0. Repro in `tp_test` (PHASE_A's script) is clean
with the default rect budget.

## 2. Light shafts

YAML (`fog:` block; off when absent; `FOUNDRY_SHAFTS=0` kills the pass):

```yaml
fog:
  shafts: {density: 0.012, distance: 150, steps: 24, color: {r:1,g:1,b:1},
           base: 0, falloff: 0, anisotropy: 0.5, strength: 1, ambient: 0.5}
```

- `world/fog_shafts.go` (`FogShafts`, raw block, `Fog.ShaftsOn`),
  `render/shafts.go`, `resources/shaders/shafts_frag.glsl`.
- Drawn in the volumetric pass (`RenderVolumetricsLit`, called from
  DrawWorld with the frame's `ShadowFrame`) as the first fullscreen draw into
  its half-resolution premultiplied target, so clouds/fireballs march over it,
  composited by the existing depth-aware upsample; the pass now runs for a
  level with shafts even with no cloud.
- Program = shafts_frag.glsl + biome_light snippet (`BIOME_LIGHT_VSM_ONE_TAP`):
  `biomeDirShadow` reads the VSM (one tap) or the cascades, plus
  `cloudShadowFactor`; samplers via `UploadShadowUniformsTo`.
- March: `steps` jittered samples (IGN + golden-ratio turn per TAA frame) from
  the eye to min(scene distance, `distance`); density = `density·e^(−falloff(y−base))`
  + ambient-volume `dust`; light = sun · HG phase (relative to isotropic) ·
  shadow · cloud shadow + level ambient · `ambient` · ambient-volume cut;
  Hillaire step integral; output (L, 1−T).
- Cheap march: below gi_quality High under TAA, half the steps.
- Procgen: environment stage `shafts`, `shafts_density`, `shafts_distance`,
  `shafts_steps`, `shafts_color`, `shafts_falloff` (from the terrain floor),
  `shafts_anisotropy` — all `Added`, default off, so hashes don't move; no
  version bump (default output unchanged). `genlevel/settings.go` needed no
  change (it decodes into `RawFog`).
- Editor: Environment → Fog → **Light shafts** section.

**Cost (estimate, to be measured natively in phase F)**: 2560×1080 → march at
1280×540; user settings (gi off ⇒ cheap) 12 steps = 8.3 M samples, each one
VSM table fetch + one textureGather, one cloud-shadow tap, an exp and a short
ambient-volume loop; plus an early-out once T < 1%. Expect ~0.4–0.8 ms on the
1070, ~1–1.4 ms at 24 full steps. If over, drop steps or (TODO) quarter res.

## 3. Ambient volumes

```yaml
volumes:
- id: cave_dark
  position: {...}
  half: {...}
  ambient: {sky: 0.08, ground: 0.12, fade: 2.5, fog: 1, dust: 0.03}
```

- `world/volume_ambient.go` (`VolumeAmbient`, `PackAmbientVolumes` — nearest 8
  to the camera — `AmbientFactorAt` CPU mirror, raw block), `Volume.Ambient`,
  `RawVolume.ambient`; packed by `PackFog` so every program's scene block has it.
- Scene block: `numAmbVolumes`, `ambVolBasis[8]` (mat3), `ambVolCenter`
  (xyz + shape), `ambVolHalf` (xyz + fade), `ambVolParams` (sky, ground, fog,
  dust) — 3968 → 4736 bytes; `gfx.SceneBlock` mirrors it
  (`TestSceneBlockMatchesTheShader`, `TestSceneBlockSizesAreTheEngines`).
- `light_model.glsl`: `ambientVolumeFactor(p, n)` (mix ground→sky by n.y, eased
  over `fade` metres inside the faces, darkest wins) multiplies the sky term in
  `ambientLight` — the one function every lit pass calls (world/fragment.glsl,
  Nanite material pass and its variants, entities, biome_light: terrain
  ground, scatter, sprites, foliage). With GI on it scales the sky share GI
  leaves (`giAmbientFloor`); GI's own bounce is untouched.
- `fog.glsl`: `ambVolFogPath` — the fraction of the eye→surface segment inside
  each volume dims the atmosphere's in-scatter and the distance-fog colour by
  the volume's mean cut × `fog`.
- Editor: volume panel **Ambient (dark interior)** group; duplicate deep-copies it.

Authoring rule (in the guide): the fade is *inside* the faces, so the volume
must enclose the rock (floor/walls/roof ≥ `fade` inside) with its mouth face
where the darkening should begin.

## Verification

- Unit: `world/volume_ambient_test.go` (round trip, defaults, factor
  inside/outside/fade/overlap/disabled, nearest-8 packing and zeroing, shafts
  round trip + `shafts: {}`), environment round-trip test updated for the new
  field; `go test ./internal/world` ok; procgen `stages -run Environment` ok.
- GL (one suite at a time): `internal/render -run
  'SceneBlock|VSM|Fog|Volumetric|Sky|Shadow'` ok, new VSM lookup test ok.
- YAML docs regenerated (`TestYAMLFieldDocsUpToDate`).
- Visual (`foundry play run`, scratch project, levels `shaft_test` /
  `shaft_novol` from `scratchpad/phb/gen_shaft_level.py`: a 6 m × 40 m slot
  with chockstones, a closed 30 × 20 × 12 m cave with a west door and a 4 m
  roof skylight, sun yaw 4 pitch −62): shafts on/off and ambient volume on/off.
  Shafts: the skylight beam in the dark cave; shadow-striped glow looking
  up-slot toward the sun; haze in the slot. Ambient volume: cave goes from
  outdoor-lit to dark with the mouth wall brighter (fade), roof black.

## Gaps

- Looking toward the sun with dense dust in LDR the in-scatter blows out
  (physical, but tune density/anisotropy per level; HDR levels handle it).
- Air samples read VSM levels by distance; pages nobody marked fall back to
  coarser ones (level 9 is always requested), which may be stale where
  something moved — a mover's shadow in the shafts can lag at coarse levels.
- No quarter-resolution or separate bilateral blur for the shafts; relies on
  TAA + the volume pass's depth-aware upsample. Without TAA there is grain.
- Ambient volumes are boxes/spheres/cylinders; a winding cave needs several
  (8 nearest in effect). They do not occlude point lights or the sun.
- WSLg runs of `foundry play` hung intermittently (~1 in 4) before the first
  picture, with and without shafts (VSM finish on D3D12); retried.
- Native GPU timing deferred to phase F.
