# Lighting quality pass — plan

Goal: the most realistic lighting the engine can give at 60 fps on a GTX 1070,
with no hardware ray tracing. What is already there and stays: live GI (DDGI
probes traced through the SDF clipmap, `render/gi.go`), the per-vertex light
bake with bounce (`internal/compile`), cascades + the sun's virtual shadow map,
TAA in an RGBA16F scene target, the optional per-level `tonemap:` HDR.

Six phases, in order. Each is gated by a player graphics setting (graphics.yaml,
Graphics tab) so a weaker GPU can turn it off, and each degrades to exactly the
old picture when off.

| # | Phase | Est. GPU (1070, 1080p) | Setting |
|---|-------|------------------------|---------|
| 1 | PBR direct specular (GGX) + `pbr` material effect + ARM maps | ~0 ms | `specular` |
| 2 | Specular image-based lighting: sky env cube, reflection volumes, GI-normalised reflections, SSR | 0.3–1.5 ms | `reflections` (off/low/high) |
| 3 | GTAO (half-res, reprojected) + SDF AO + specular occlusion | 0.5–1 ms | `ambient_occlusion` |
| 4 | HDR by default, auto-exposure, bloom | 0.3–0.5 ms | `bloom`, level `tonemap:` |
| 5 | Per-texel lightmaps for compiled levels | 0 ms (memory) | compile `lightmap:` |
| 6 | Contact shadows, PCSS soft cascades, foliage translucency | ~0.5 ms | `contact_shadows`, `soft_shadows` |

## Shared decisions

- **Two shading paths carry everything.** `fragment.glsl` (brushes, models,
  compiled surfaces, and the visibility buffer's material pass, which runs the
  same `main`) and `biome_light.glsl` (terrain, foliage, grass, scatter, lit
  sprites). New terms go into a new include, `specular.glsl`, that both pull
  in. It is NOT pulled into the GI compute programs (light_model.glsl is), so
  its samplers never reach them.
- **Diffuse is untouched.** Wrapped Lambert + hemispheric ambient stays what the
  baker computes (`world.GlobalLight.LightWrap/AmbientAt`). Specular is
  view-dependent, so it is always live and never baked: the baker does not
  change for phases 1–4.
- **Unwritten = old.** Every new uniform is a bias or a flag whose zero is the
  previous behaviour, and scene-wide switches live in the scene block (zero
  until `world.UploadLightsTo` writes the player's setting).
- **Screen-space terms are reprojected, not prepassed.** The renderer is
  forward with no full depth prepass, so GTAO and contact shadows are computed
  after the opaque pass from that frame's depth (single-sample in the scene
  target) and read by the NEXT frame's shading at the fragment's position
  reprojected through last frame's view-projection, with a depth check to drop
  disocclusions. One frame of lag, hidden by TAA. Applied inside
  `ambientLight` / the sun term, so they touch only the light they should.
  Only in scene-target frames (TAA, the default); MSAA/off frames go without.

## Phase 1 — PBR direct specular

- `specular.glsl`: GGX NDF, height-correlated Smith visibility, Schlick
  Fresnel. `pbrSpecular(n, v, l, rough, F0)` returns the specular radiance
  factor (already × N·L).
- Per-surface parameters as uniforms biased from the defaults: roughness
  `PBR_DEFAULT_ROUGHNESS` (0.7), metallic 0, specular 0.5 (F0 = 0.08·specular,
  so 0.04 for dielectrics). Metal: F0 = albedo, diffuse × (1 − metallic).
- Material effect kind `pbr` {roughness, metallic, specular}; an ARM map
  (`<texture>_arm`, Poly Haven's AO/roughness/metal) is bound when present and
  multiplies the factors (glTF semantics); its AO channel joins vertex AO.
- Every direct light adds specular with the same shadow/cloud visibility as its
  diffuse. On baked surfaces: the baked sun's specular uses the live cascade
  shadow where it reaches and the baked share beyond; static point lights add
  specular only (their diffuse is baked), shadowed by their cube when they cast.
- Biome passes: grass/foliage roughness 0.6, ground 0.85, rock 0.75, all
  dielectric; two-sided blades take specular only on the lit side.
- Water keeps its own Fresnel/glint path.

## Phase 2 — specular image-based lighting

- **Sky environment cube** (`render/envmap.go`): the sky rendered into a 128²
  cube when it changes (and every N frames while it eases), GGX-prefiltered
  into its mips by a compute pass (split sum), plus a small BRDF LUT (computed
  once on the GPU). Sampled at `textureLod(env, R, rough * maxMip)`.
- **GI normalisation**: a reflection's brightness is scaled by
  lum(GI irradiance along R) / lum(env irradiance along R) when GI is on, so an
  indoor surface does not mirror the bright sky; with GI off, the ambient volume
  factor and AO stand in. Rough surfaces lean on the GI probes directly.
- **Reflection volumes**: `Volume` kind `reflection` (box = parallax proxy,
  capture at centre). Captured by tracing the GI distance field from the
  centre in a compute pass, shading hits with the GI trace's own hit light
  (`directAt` + `bounceAt`), sky on a miss; refreshed one face a frame.
  Box-projected lookup; nearest/smallest containing volume wins with a fade.
- **SSR** (high tier): half-res Hi-Z march of last frame's resolved colour
  (`render/hiz.go` exists), reprojected; blended over the env answer by
  confidence, fading out at roughness 0.5.

## Phase 3 — ambient occlusion

- GTAO compute at half resolution from the scene target's depth, 2 directions
  × 6 steps, per-pixel rotation that changes per TAA frame; 4×4 bilateral blur.
  Stores AO + linear depth (RG16F). Read via reprojection (above).
- SDF AO when GI is on: 3–4 samples of the GI field along the normal (0.15–1.6
  m), the scale between GTAO's and the probes'.
- Applied to `ambientLight`'s result and, through Lagarde's specular occlusion
  from AO and roughness, to the IBL specular. Never to direct light.

## Phase 4 — HDR, exposure, bloom

- HDR is on for every scene-target frame unless a level says `tonemap: {off:
  true}`. Exposure is automatic unless the level fixes it: a 64-bin log
  luminance histogram of the scene, a trimmed mean, adapted per frame with
  separate up/down speeds; the level's `exposure` becomes a compensation.
- Calibrated so a level that never asked for HDR reads as it did: the target
  key maps the old "lit at 1 = albedo" middle to the same display value.
- Bloom: 13-tap downsample / tent upsample mip chain (6 levels) of the HDR
  colour, thresholdless, mixed in at a small strength before the curve.

## Phase 5 — lightmaps

- Compiled static surfaces get a lightmap UV per face (planar chart on the face
  plane at `lightmap.texel` metres, default 0.25), packed into 2048² pages
  (shelf packing, 2-texel gutters). Direct static light and the sun share are
  baked per texel (2×2 supersampled, the same shadow test as the vertex bake);
  the indirect (ambient + bounce) stays per vertex, which it is smooth enough
  for, so the grid refinement is no longer needed for lightmapped faces.
- The baked-light vertex record keeps its 7 floats: in a lightmapped surface
  xy is the lightmap UV and z the page; `bakedLight == 2` tells the shader.
  Pages are RGBA16F (rgb light, a sun share) stored in the `.fbsp`
  (CompiledVersion bump).

## Phase 6 — shadows and translucency

- Contact shadows: in the GTAO pass, a 16-step screen-space march toward the
  sun over 0.5 m; stored with the AO, applied to the sun's direct term.
- Soft (PCSS) cascades: blocker search through a second, non-comparison
  sampler on the cascade array; penumbra from the light's angular size
  (`softness` on the directional light, default the sun's 0.5°).
- Foliage translucency: back-lit transmission (view-aligned forward scatter)
  tinted by the albedo for two-sided biome surfaces.

## As built (2026-10-15)

All six phases landed. Where the build differs from the plan above:

- **Sampler budget.** The visibility buffer's material pass (fragment.glsl's
  main run full-screen) was at 31 of the 32 fragment samplers a GTX 1070
  allows. The eight point-light shadow cubes became one
  `samplerCubeArrayShadow` (render/shadow.go: cube `Layer` of
  `Shadows.PointArray`, `pointShadowCube[]` in the scene block), freeing units
  3–9 (the array itself is on 2). The lighting pass uses 3 (env cube array),
  4 (AO), 5 (SSR history) and 6 (lightmaps); 50 is the ARM map; 60/61 are
  the lighting compute passes' inputs. The material pass is at 29 of 32.
- **Phase 1.** `specular.glsl` is included by fragment.glsl and
  biome_light.glsl only. `pbr` effect + `<texture>_arm` maps
  (gfx/texture_armmap.go — not `_arm.go`, which Go reads as GOARCH=arm).
  `specularOn` in the scene block.
- **Phase 2.** `render/envmap.go`: one RGBA16F cube array (cube 0 the sky,
  1–8 reflection volumes), 128² × 6 mips, GGX-prefiltered by compute
  (`env_prefilter_comp.glsl`); the split sum's second half is Karis's
  analytic fit (no LUT). Sky recaptured every 60 frames, on a transition, or on
  an HDR/LDR switch. Volumes (`Volume.Reflection`, world/volume_reflection.go)
  are captured by `env_capture_comp.glsl`, which shares the probe trace's hit
  shading (`gi_hit_light.glsl`, extracted from gi_probe_trace_comp.glsl);
  new volumes at once, the rest every 4th frame round-robin. With GI on the sky
  reflection is scaled by the probes' irradiance along R over the sky cube's
  own (and, as roughness grows, replaced by it); with GI off, by the surface's
  ambient over the open sky's. SSR (`envOn` 2) is marched in the forward
  shader through the AO texture's distances and samples TAA's history.
- **Phase 3.** `render/ao.go`: `ao_comp.glsl` (GTAO 2 slices × 6 steps, SDF
  AO when GI runs, contact shadow 12 steps / 0.5 m) at half resolution after
  the transparent pass, `ao_blur_comp.glsl`, read NEXT frame via
  `ssaoPrevViewProj` with a distance check. Sample at a pixel CENTRE of the
  2×2 block — sampling the block's middle (a corner) striped everything.
- **Phase 4.** HDR is on by default (`gfx.HDRByDefault`, `FOUNDRY_HDR=0`);
  a `tonemap:` block with only an exposure keeps it fixed; `auto: true`
  meters. `render/exposure.go` meters with one compute workgroup and reads
  back through fences (never stalls); adaptation is partial
  (`(0.13/mean)^0.6`, clamped 0.4–1.8). `render/bloom.go`: 6-mip compute
  chain, mixed at 5 % before the curve. Every lit biome pass was moved to
  scene units (`hdrLightMax`, `applyFogScene`) so terrain and brushes agree.
  The render package's tests run with `HDRByDefault` off (TestMain), since
  automatic exposure re-meters between frames a test compares.
- **Phase 5.** `compile/lightmap.go`; levels read from a file bake lightmaps
  unless `compile: {bake: vertex}`; in-code fixtures keep the per-vertex
  bake. A texel holds the non-sun static lights' light (flat normal) and the
  sun's visibility; the sun's term is computed per pixel. Lightmapped rows
  reuse the 7-float record: (u, v, page, -2 or -3 for sun-excluded,
  indirect rgb). `.fbsp` version 11 (`face.lightmap`, `lightmap.shape`,
  `lightmap.texels` columns). level1: compile 61 → 33 s; level2: 99.7k → 69.8k
  surfaces, +86 MB of pages.
- **Phase 6.** Contact shadows ride the AO pass; PCSS is in vsm.glsl (the
  virtual shadow map, which the sun uses near the camera; the cascades keep
  their fixed-width filter); translucency is in `biomeLightingSpec` for
  two-sided surfaces (the impostors' own copy was removed).
- **Debug views**: `specular`, `mirror`, `ao` (render/view.go).
- **A/B**: `FOUNDRY_LIGHTING="specular=off,reflections=off,..."` and
  `foundry play -lighting ...`.
