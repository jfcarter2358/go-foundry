# Materials

A **material** is a named bundle of shader effects and their settings, saved as
a `.fmat` in `resources/materials/`. Anything that can wear a texture can wear
one instead: a brush face, a compiled surface, a model or entity attachment, a
sprite, one layer of a particle effect, or the sky.

Open the editor with the **Material** button in the editor strip.

## Why

Every effect in this engine already existed. Water was six numbers on a surface;
`unlit` was a checkbox on an entity attachment and nothing else could reach it;
the second-texture blend was two texture names per face; the UV scroll was one
field that only a tank's tracks had; a sprite's blend mode and a particle
layer's were the same enum spelled out in two formats; the cutout threshold was
a constant in the shader and was not authorable at all.

They worked, and none of them could be **shared**. Two ponds with the same swell
meant typing the same six numbers twice, and then keeping them in step by hand.

A material is where those settings live now. Name one and the surface gets them.
Change the material and every surface wearing it changes.

## Wearing one

Every panel that has a **Texture** row now has a **Material** row under it, and
it works the same way: pick a name, or `(none)`. The **Edit** button beside it
opens the material editor on whatever is named.

A placed scenery mesh (a cooked `.fnm`) wears one with `material:` on the
placement — every slot — or by naming a material instead of a texture in its
`materials:` slot map.

In a level file it is one key:

```yaml
faces:
  - indices: [0, 1, 2, 3]
    texture: hull_plate
    material: hangar_strip
```

## It overlays; it does not replace

This is the rule to remember. **A material changes only what it actually
declares.**

A material carrying nothing but a scroll rate slides the texture of whatever
wears it and leaves that surface's own texture, alpha, tint and smoothing
exactly as the level set them. That is what lets one `conveyor.fmat` drive nine
belts of five different art.

It also means a material can be added to finished content without auditing it:
the worst a scroll-only material can do is scroll.

Two consequences worth knowing:

- A material that **does** name a texture supplies it, and the surface's own is
  ignored. A face wearing `water.fmat` is water whatever it was painted with (a
  water surface ignores textures altogether: it draws its ripples and foam from
  a generated map — see [Water](#water) below).
- Tints and alphas **multiply** rather than replace, so a script fading a sprite
  out still reaches zero and an author's red lamp stays red under a warming
  material.

## The editor

The left column lists every `.fmat` in the project — switching between two
materials you are balancing against each other is one click.

The middle is a **live preview** drawn through the engine's own world shader.
It is not an approximation: the swatch is shaded by the same code as the wall,
with the material resolved the same way, so a material that looks right here
looks right in the level. Choose the shape with the dropdown — a **plane** reads
a tiling and a scroll, a **sphere** reads the lighting and a normal map, a
**cube** reads both plus how the material meets an edge. Left- or right-drag to
orbit, middle-drag to pan, wheel to zoom, `Space` to stop the spin.

The swatch holds still by default — a surface that will not stop moving is
harder to judge than one that will. Turn **Spin** on to watch a scroll or a wave
travel past a fixed point.

**Live Update** (on by default) pushes every edit into the running level as you
make it, so the walls in the level window change as you drag a slider. Turn it
off and only a save publishes.

The right panel is the material itself.

### Kind

Which domain the material belongs to, and therefore which effects it may carry:

| | |
| --- | --- |
| `surface` | Brush faces, compiled surfaces, models, entity attachments |
| `sprite` | Billboards |
| `particle` | One layer of a `.fpar` |
| `sky` | The sky dome and its cloud deck |

A wall is not offered a sky and a sprite is not offered water, because an effect
in the wrong domain resolves to nothing. Changing the kind keeps the effects
already on the document and reports the ones that no longer belong — switching
back makes them work again.

### Effects

**Add...** lists every effect legal in this kind. Each one has a checkbox on the
left to switch it off without deleting it — which is how you compare with and
without — and an **x** on the right to delete it, which `Ctrl+Z` puts back.
Right-click an effect's header to move it up or down.

A parameter still at its default is marked `default` and **is not written to the
file**. The `x` beside a changed one puts it back. That is what keeps a `.fmat`
readable: it says what you changed, not what the engine happens to think today.

## The effects

Every one of these was somewhere else before. The note says where, because
that is usually what you are looking for.

### Surface

| Effect | Was | Does |
| --- | --- | --- |
| **Unlit** | `unlit:` on an entity attachment | The texture's own colour — no light, no shadow. Fog still applies; the surface goes to the blended pass |
| **Alpha** | `alpha:` per face | A constant multiplier. Below 1 it draws in the sorted transparent pass |
| **Cutout** | a constant in `fragment.glsl` | Where a cutout's silhouette sits. `0.5` reproduces a hard alpha edge; lower fattens the shape, higher eats it |
| **Blend** | `blend_texture:`/`blend_mask:` | Mixes a second texture over the first by a mask's luminance. Any texture serves as the mask |
| **Normal map** | implicit, from `<texture>_normal` | Names one explicitly, so a material can borrow another texture's relief |
| **UV scroll** | `uv_scroll:` on an attachment | Slides the texture, in UV units a second |
| **UV transform** | the face's own UV fields | A tiling and a turn, **composed on top of** the face's own placement |
| **Water** | a surface's `water:` block | Real water: depth colour, refraction, reflection, sun glint, ripples, foam, caustics — see [Water](#water) |
| **Smooth shading** | `smooth:` per surface | Shade from interpolated normals whatever the player's Smooth Lighting setting says |

### Water

A surface wearing the **water** effect is drawn in a pass of its own, after the
sky and before the glass and smoke, over copies of the scene it lies on. That is
what lets it look like water rather than a tinted sheet:

- **Depth colour.** The bed shows through the shallows and is lost in the deeps:
  light from the bed is absorbed along its way down and back up
  (`absorption`, `shallow_color`), and what is lost is replaced by the water's
  own colour lit by the scene (`scatter_color`). A sandy ledge under 30 cm of
  clear water is nearly the sand; 5 m down it is the deep colour.
- **A soft shoreline** — the water fades in over `shore_fade` metres of depth —
  and **foam** where the bed comes up near the surface (`foam`).
- **Refraction**: the bed is seen bent by the ripples (`refraction`).
- **Reflection**: whatever the screen shows along the reflected ray (rocks,
  banks, a ship), and where that runs out the level's own sky, blurred by
  `roughness`; on a level with no sky, the ambient light. It is strongest at
  grazing angles, as water's is (`reflectivity`: 0.5 is physical).
- **Sun glint** on the ripples, and a back-lit wave crest glowing faintly
  (`subsurface`): looking toward the sun, the high part of each wave and the
  faces turned toward you take the deep colour brightened by what half a
  metre of the water lets through, so clear water's crests glow green and a
  swamp's barely.
- **Ripples**: a generated ripple map at three sizes drifting along
  `flow_direction` at `flow_speed`, over the old three-sine swell
  (`wave_amplitude`, `wave_length`, `wave_speed`) and, for open water, a
  sharp-crested `swell`.
- **Caustics** on the bed (`caustics`). They are drawn by the water itself
  onto the bed it shows, faded with the depth under that surface, carried
  down a river's current and only where the sun reaches the water: a pond,
  a sloping river and anything lying under either take them; dry ground
  beside the water never does, however low it lies.
- **A wet bed**: the ground under water is a little darker than the same
  ground dry, so a shallow clear stream over pale gravel reads as water,
  not as a pale strip.
- **Flow maps** (`flow_map`): on a mesh whose vertex colours carry a
  current — a generated river's ribbons (ground_edit's rivers,
  [Generators](33-generators.md)) — the ripples and the foam are carried
  downstream by it, faster where it is faster, two copies half a cycle apart
  cross-faded so the pattern never stretches. The colour's green and blue
  are the current along X and Z (0.5 still, ±4 m/s at the ends), red adds
  foam. Without vertex colours a `flow_map` material drifts along
  `flow_direction` like any other. On a flow-mapped river the shore foam
  band is a third of a lake's (a shallow stream was white bank to bank);
  its foam comes from the vertex foam — riffles, boulders, a fall's pool, a
  confluence's mixing line — and that white water is also choppier and
  rougher, as churned water is.
- **Falling water** (`falls`): a waterfall's sheet. Streaks run down the
  mesh (its V, metres down the sheet) at `falls_speed`; the sheet is white
  and opaque where it is aerated and see-through where it is glassy, with
  the scene behind it bent through it; its sides fray. A generated river's
  falls wear `water_falls` on a sheet ground_edit builds
  ([Generators](33-generators.md)), whose vertex colours say where it is
  aerated (red), how near a side (green) and how far down (blue). On a
  mesh without vertex colours — a brush face hung as a fall — the sheet is
  taken as fairly aerated all over.
- **Rough water is never a full mirror**: the fresnel's grazing limit falls
  with `roughness`, so a choppy river or a marsh shows its own colour under
  the reflected sky.
- **Rings** where something disturbs it: a foot in the shallows, a body
  dropping in, a shot striking the surface. Each spreads as a few crests
  from where it started and fades over a second or two; a big one leaves a
  little foam at its heart. They need no setting — any water surface rings
  — and the game makes them itself (see [Water](37-water.md)).
- **Rain** (`rain_ripples:` in the level, `@level.prop.rain_ripples`, 0–1):
  small drop rings over every water surface, more of them the harder it
  rains.
- **Seen from under the surface**: inside a cone overhead (Snell's window)
  the world above, bent by the ripples; outside it the water mirrors its own
  murk (total internal reflection). The water between the eye and anything
  is the material's murk — its colour from `scatter_color`, how fast it
  thickens from `absorption` and `shallow_color` — and at the high tier the
  bed and everything under the surface take the caustics from below. See
  [Water](37-water.md).
- **Foam in patches and streaks**, not an even net: the lace gathers in
  clots that drift with the water, stretched into streaks along a river's
  current.

Water casts no shadow, and how much of this a player sees is their **water
quality** setting (graphics.yaml `water_quality`, `@level.prop.water_quality`,
the console's `water_quality`): `low` is the depth colour and the sky reflection,
blended over the bed; `medium` adds refraction; `high` — the default — adds the
screen-space reflection and the caustics on the bed. The F3 profiler shows the pass as
`water: copy` (the copies and the sky map) and `water`.

| Parameter | Default | |
| --- | --- | --- |
| `wave_amplitude`, `wave_length`, `wave_speed` | 0.03, 4, 0.3 | The rolling swell: metres, metres a cycle, cycles a second |
| `reflectivity` | 0.5 | The reflection's strength; 0.5 is water's own fresnel, 1 twice it |
| `reflect_color` | white | A tint on the reflection (its hue), and the reflection itself on a level with no sky |
| `absorption` | 0.6 | How fast light is lost with depth, per metre: 0.15 mountain water, 3 a swamp |
| `shallow_color` | pale cyan | The colour light keeps through the water (water drinks red first) |
| `scatter_color` | deep teal | The colour deep water tends to |
| `subsurface` | 0.5 | The back-lit crest glow |
| `roughness` | 0.06 | The glint's spread and the reflection's blur |
| `refraction` | 0.35 | How far the ripples bend the view of the bed |
| `foam`, `shore_fade` | 0.5, 0.12 | Shore foam (0 none), and the metres of depth the edge fades over |
| `flow_speed`, `flow_direction` | 0.25, [1, 0.35] | The ripples' drift, m/s along an X/Z heading |
| `detail_scale`, `detail_strength` | 3, 1 | Metres per ripple tile, and how strongly they tilt the surface |
| `swell`, `swell_length` | 0, 24 | A sharp-crested open-water swell, metres high and long |
| `caustics` | 1 | The light dancing on the bed (high quality) |
| `ssr` | 1 | How much of the reflection may come from the screen (high quality) |
| `flow_map` | 0 | How strongly the mesh's vertex-colour flow map steers the ripples and foam (1: as the current says); 0 off |
| `falls` | 0 | Shade the surface as a waterfall's falling sheet, this strongly; 0 is ordinary water |
| `falls_speed` | 4 | How fast a falling sheet's streaks run down it, m/s |

**Presets.** `water` is the default (a clear temperate lake), spelled out;
`water_tropical` (turquoise shallows, strong caustics), `water_alpine` (glass
clear, cold blue-green), `water_lake` (green-brown lowland lake), `water_river`
(flow-mapped on a generated river, fast ripples along `flow_direction`
elsewhere, foamy, a little silt), `water_swamp` (tea-brown, opaque, still), `water_sea` (a long swell, ocean blue, white water at the shore) and `water_falls` (a waterfall's falling sheet: no swell, no flow map, the river's colours). Name
one on a face, or as a generated terrain's `water_material` (see
[Generators](33-generators.md)); copy one to change a number.

Everything about water beyond its look — the rings, rain, splashes, the
underwater view, river sound, asking where water is from a script — is in
[Water](37-water.md).

### Sprite and particle

| Effect | Was | Does |
| --- | --- | --- |
| **Blend mode** | `blend:` on a sprite and on a layer | `cutout` (opaque, hard edge, writes depth), `alpha` (sorted, no depth write), `additive` (adds light, never occludes) |
| **Billboard** | `billboard:` on both | `full`, `y-axis` (keeps world up — anything standing on the ground), `fixed` (does not billboard) |
| **Tint** | `tint:` on a sprite, `tint:`/`intensity:` on the sky | A colour and a scale, multiplied into whatever the art produced |
| **Lit** / **Cast shadows** | two checkboxes on a sprite | Both can only be turned **on** by a material: there is no effect meaning "not lit" |
| **Sprite sheet** | `sheet_cols:`/`sheet_rows:` | How the texture divides into cells. **Which cell** stays on the object |
| **Volumetric** | a layer's `volume:` block | Draws the layer's particles as marched balls of medium rather than quads |

The two **ramps** of a volumetric layer — erosion over life, emission over life
— stay on the layer. A ramp is a curve over a particle's life, and a material's
parameters are constants.

### Sky

| Effect | Was | Does |
| --- | --- | --- |
| **Sky gradient** | the level's `zenith`/`horizon`/`ground` | The three stops of a gradient sky |
| **Sky cubemap** | `faces:` and `rotation:` | Six textures. Set the `+X` face alone and the other five fill in by the `<base>_px` convention |
| **Clouds** | `clouds:` | The volumetric deck — and the same parameters describe a cloud volume |
| **Fog** | a volume's `fog:` | Extinction per world unit travelled inside the region |

### Terrain and rock

These are new rather than moved: the effects a generated landscape is
painted with (docs/canyon/PHASE_5.md). They project in **world space**
rather than reading the mesh's UVs — a heightfield's UVs are a sheet
stretched down every cliff, a generated rock's seam on every edge — and they
are what the generator stages write for their terrain and rocks, but any
surface can wear them.

| Effect | Does | Main settings |
| --- | --- | --- |
| **Triplanar** | The surface's own texture and normal map projected along the three world axes, blended by the normal. Upright: the side projections keep the texture's top toward the sky, so bedding stays level | `scale` (m a repeat), `sharpness`, `upright` |
| **Splat (terrain layers)** | Up to four layers (eight with `weights2`: see "Eight layers" below) mixed by a **weights** texture laid over a world rectangle (red the first layer's weight, green the second's, blue, alpha), height-blended by each layer's `_height`, with its `_normal` and `_ao`. A layer can be triplanar; a **strata** ramp (a strip of tints read by world height) bands the triplanar layers. The surface's own texture is not drawn | `weights`, `bounds_min/max`, `layer1..4` + `_scale`, `_triplanar`, `_height`, `_tint`; `blend`, `ao`; `strata`, `strata_range`, `strata_strength`, `strata_warp`; `bedding`, `bedding_spacing`, `wall_normal` (phase 7: ledges bent into the triplanar layers' normals on steep faces, and a stronger normal map on walls); `layer1..4_relief` (phase 8: parallax depth in metres for a top-down layer — its height map moves its texels along the view so stones stand up off the ground, crevices darkened; fades with distance); `layer1..4_anti_tile` (phase 8: the layer is laid in patches about a repeat across, each shifted — and, top down, turned — its own way and height-blended where they meet, so no feature recurs on a grid; about half again the layer's texture reads) |
| **Ground blend** | Fades the surface toward the ground's colour within a band above the ground's height — a rock's foot takes the sand's colour, which hides the seam — with a contact shadow at the line | `ground` (colour map), `height` (16-bit map: red high byte, green low), `bounds_min/max`, `height_range`, `band`, `breakup`, `strength`, `contact` |
| **Streaks (desert varnish)** | Dark vertical streaks down steep faces and a darkening under overhangs, in world space, so a wall of many pieces streaks as one | `strength`, `scale` (width), `length`, `color`, `overhang` |
| **Detail normal** | A second normal map at a small scale, world-projected, fading out with distance | `map`, `scale`, `strength`, `distance` |
| **Macro variation** | Slow world-space noise on the albedo's brightness and warmth: hides a texture's repeat at distance | `scale`, `strength`, `warmth`, `vertical` (how much the patches also change with height, so a wall is not one colour top to bottom) |
| **Colour grade** | `match` carries the texture's average colour toward `target` — every texel by the same ratio, brightness kept, so detail and variation survive — then a hue turn, saturation, a brightness-free `tint`, and a plain `brightness`. What turns grey granite scans into red-rock country without painting them | `match`, `target`, `source` (black: measured from the texture), `hue`, `saturation`, `tint`, `brightness` |

Texture parameters of these effects take a texture name **or a
resources-relative path without `.png`** (`generated/canyon/ground_mat/weights`),
which is how a generator points a material at maps it wrote beside its
output. A splat's four layers are packed into two 2×2 atlases (albedo with
height, normal with AO) when the material is first used, at most 2048² a
layer (`FOUNDRY_SPLAT_MAX_SIZE`); the data maps are read exactly, never
compressed. Every effect is a branch a surface without it does not take, so
nothing else draws differently, and they look the same on the forward path,
the classic path and the Nanite visibility buffer.

**Eight layers.** A splat with `weights2` and any of `layer5..8` mixes eight
layers: layers 5–8 take `weights2`'s red, green, blue and alpha over the
same rectangle, with the same per-layer settings (`layer5..8_scale`,
`_triplanar`, `_height`, `_tint`, `_anti_tile`, `_relief`). All eight are
height-blended together, so where two biomes meet their layers settle by
their height maps. This is what a world generator's
[region_material](33-generators.md#region_material-materials) writes for a
world of two environments. More than four distinct layers pack into 4×2
atlases on the same two units, at most 1024² a layer by default (the pair
is 4352×2176; `FOUNDRY_SPLAT_MAX_SIZE` still caps it). It costs one more
texture unit (the second weights map), and a pixel reads only the layers
whose weight can still win, as with four. A splat that names no layer 5–8
is the four-layer splat exactly — same atlas, same shader path (layers 5–8
are behind their own effect bit, compiled out of the visibility buffer's
four-layer variants). `layer1..8_strata` (default on) says whether the
strata ramp and the bedding band a triplanar layer: a region material turns
it off on every environment's rock but the lead's, whose strata the ramp
is.

**Soft ground (sand, snow).** `layer1..8_soft` makes a layer ground that gives
underfoot. Its values are `none` (the default), `sand` or `snow`.

- **Footprints.** Anyone walking on it, the player or an AI walker, presses
  footprints into it, with a raised rim.
- **Steps.** Each step plays `step_sand` or `step_snow` from `sounds/` and kicks
  up the matching `particles/` puff, tinted the ground's own colour.
- **Drag.** It slows the stride: sand by 12%, snow by 25%. The player's eye
  sinks a few centimetres into it.
- **Depth and refill.** A print is 3.5 cm deep in sand and 8 cm in snow.
  `layer1..8_soft_depth` (metres) overrides that. Sand prints refill within a
  couple of minutes; snow prints last the session.

The world generators set `_soft` themselves from the texture:

- A Poly Haven scan whose id contains `sand` (not `sandstone`) is sand.
- One whose id contains `snow` is snow.

Prints are drawn only within 24 m of the player and are not saved. They are
shading, not geometry: a parallax hole with its own normal and a darker
floor, with no shadow of its own. `FOUNDRY_TRAILS=0` turns the prints off
(steps still sound). Design notes: `docs/trails/SPEC.md`.

`desert_rock`, `desert_ground` and `rock_foot` are the shipped examples (with
stand-in maps; a generator writes the real ones).

The sky is the one domain where the material is **folded into** the level's own
sky rather than read at draw time. `ease_sky` blends two complete sky values
over a few seconds, and a sky whose numbers moved under it would ease toward a
moving target. The practical effect is the same — a coverage slider still thins
the clouds over the level on the next frame — but the rows in the sky panel
show what the material set, and editing one by hand holds only until the level
is reloaded.

## From a script

A model attachment and a sprite both carry a `material` property:

```
@console.model.screen.prop.material = "panel_on"
@spark.prop.material = ""
```

Swapping a material is how a script changes several things about a surface at
once — its blend, its tint, its scroll — without setting each of them and having
to remember what they were.

## What ships

`resources/materials/` comes with one worked example per effect: `water` (and
its presets `water_tropical`, `water_alpine`, `water_lake`, `water_river`,
`water_swamp`, `water_sea`), `water_rough`, `unlit`, `hologram`, `conveyor`, `mossy_rock`, `foliage`,
`glass`, `deck_tiled`, `glow_sprite`, `foliage_sprite`, `poster`, `flipbook`,
`ember`, `smoke`, `fireball`, `sky_day`, `sky_canyon`, `sky_space`,
`sky_overcast`, `fog_bank`, `desert_rock`, `desert_ground` and `rock_foot`. They
are meant to be opened and pulled around.

## Notes

- Materials are referenced **by name**, like textures: `hangar_strip.fmat` is
  `hangar_strip` everywhere.
- A texture reached only through a material is still packed into a release, and
  is not reported as unused.
- A material file written from outside the editor — by Claude, or by a
  generator — is picked up and re-published without a restart.
- A material a level names that no longer exists is left named, marked in the
  picker, and simply does not resolve. The surface draws with its own settings.

## How shiny a surface is: `pbr`

Every lit surface takes a highlight from every light and a reflection of its
surroundings ([Lighting](07-lighting.md#lighting-quality)). Without a `pbr`
effect it is a rough non-metal. With one:

```yaml
kind: surface
texture: hull_plate
effects:
  - effect: pbr
    roughness: 0.35   # 0 a mirror, 1 a matte surface (default 0.7)
    metallic: 1       # 1 a metal: no diffuse colour, the reflection takes the albedo's tint
    specular: 0.5     # a non-metal's reflectance: 0.5 is the 4% of most materials
    # arm: hull_plate_arm  # an AO/roughness/metal map other than <texture>_arm
```

A texture's `<name>_arm.png` (red AO, green roughness, blue metal) is used
whether or not the material names it; the factors multiply it. Without a `pbr`
effect, a texture with an ARM map takes its roughness and metal as they are.
`brushed_metal.fmat` is the worked example.
