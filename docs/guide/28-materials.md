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
  ignored. `water.fmat` names `water`, so a face wearing it is water whatever it
  was painted with.
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
| **Water** | a surface's `water:` block | The swell, the per-pixel wave normal and the fresnel mix |
| **Smooth shading** | `smooth:` per surface | Shade from interpolated normals whatever the player's Smooth Lighting setting says |

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
| **Splat (terrain layers)** | Up to four layers mixed by a **weights** texture laid over a world rectangle (red the first layer's weight, green the second's, blue, alpha), height-blended by each layer's `_height`, with its `_normal` and `_ao`. A layer can be triplanar; a **strata** ramp (a strip of tints read by world height) bands the triplanar layers. The surface's own texture is not drawn | `weights`, `bounds_min/max`, `layer1..4` + `_scale`, `_triplanar`, `_height`, `_tint`; `blend`, `ao`; `strata`, `strata_range`, `strata_strength`, `strata_warp`; `bedding`, `bedding_spacing`, `wall_normal` (phase 7: ledges bent into the triplanar layers' normals on steep faces, and a stronger normal map on walls); `layer1..4_relief` (phase 8: parallax depth in metres for a top-down layer — its height map moves its texels along the view so stones stand up off the ground, crevices darkened; fades with distance); `layer1..4_anti_tile` (phase 8: the layer is laid in patches about a repeat across, each shifted — and, top down, turned — its own way and height-blended where they meet, so no feature recurs on a grid; about half again the layer's texture reads) |
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

`resources/materials/` comes with one worked example per effect: `water`,
`water_rough`, `unlit`, `hologram`, `conveyor`, `mossy_rock`, `foliage`,
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
