# Stylized lighting

**Experimental.** A level can be drawn as if it were a painted miniature:

- shadows turn a **cool** colour instead of going black,
- brightly lit surfaces **warm** up,
- **convex edges** are picked out in a light highlight,
- **creases and recesses** take a dark wash,
- **raised detail** is dry-brushed: scuffed with light paint where it stands
  proud.

It is the level's look, saved with it, so play draws what the editor shows.

## Turning it on

In the 3D editor, **View > Stylized Lighting (Experimental)** switches it on
and off. The first time, it gives the level a `stylize:` block with the
defaults. After that, switching off keeps your numbers, the way switching
off fog does. Both are ordinary edits: undoable, and they mark the level
changed.

The numbers are in **Properties > Environment > Stylized lighting
(experimental)**. **Reset to defaults** puts them back.

## The two halves

**The light ramp** bends the light every surface takes before its colour
multiplies it. Light below the *shadow point* counts as shadow and turns
toward the *shadow colour*. A little of that colour (the *fill*) is added
there, so nothing goes black. Light above the *light point* warms toward the
*highlight colour*. The tints change the light's hue without darkening or
brightening it. The ramp applies to everything that is lit: brushes, models,
cooked meshes, terrain, foliage, grass and sprites. It shows in every
anti-aliasing mode.

**The paint** (edge, wash, dry-brush) is drawn over the frame from the
surfaces' own normals. Where they spread apart across a few pixels the surface
is a ridge and gets the edge highlight; where they close up it is a crease and
gets the wash. The dry-brush catches fine bumps in a surface's normal map and
the broad shoulders of its shape, broken up by brush strokes fixed to the
world. Edges facing the sun and the tops of things take more paint than
undersides, as a painter would do.

The paint needs the **TAA** anti-aliasing mode, which is the default and is also
what the editor's filled view uses. In MSAA mode, in wireframe, and with
`FOUNDRY_EDITOR_SCENE_FRAME=0`, only the light ramp shows. The Environment
section says so when that is the case.

What takes paint: brush faces, compiled surfaces, models, cooked (Nanite)
meshes and block terrain. What doesn't: foliage, grass, scatter, sprites,
decals, water, glass, self-lit (`unlit`) surfaces and the first-person hands.
The ramp still lights all of them.

## The block

```yaml
stylize:
  shadow_color: {r: 0.35, g: 0.42, b: 0.78}
  shadow_tint: 0.55        # 0..1
  shadow_fill: 0.06        # shadow-coloured light lifted into the dark
  highlight_color: {r: 1, g: 0.85, b: 0.62}
  highlight_tint: 0.4      # 0..1
  shadow_point: 0.05       # light as shown on screen, 0..1
  light_point: 0.6
  edge_color: {r: 1, g: 0.93, b: 0.8}
  edge: 0.8                # 0..2, 0 for none
  edge_width: 1.5          # pixels of a 1080-line picture
  wash_color: {r: 0.32, g: 0.24, b: 0.18}
  wash: 0.5                # 0..1, 0 for none
  drybrush_color: {r: 0.95, g: 0.9, b: 0.8}
  drybrush: 0.35           # 0..2, 0 for none
  drybrush_scale: 6        # strokes per world unit
  drybrush_threshold: 0.3  # how raised detail must be before it catches
  fade_distance: 80        # world units: the paint is gone by here
  disabled: true           # keep the numbers, draw the level plain
```

Every key is optional: `stylize: {}` is the defaults. A key left out is its
default, and the editor writes only the keys you have changed.

## Tuning it

- **Everything looks blue.** The level is dim enough that most of it sits
  below the light point. Lower `light_point` and `shadow_point`, or
  `shadow_tint`.
- **Edges are too thin or too thick.** Adjust `edge_width`. It is in pixels of
  a 1080-line picture and scales with the screen, so it looks the same at any
  resolution.
- **Scanned rock looks busy.** Its normals turn everywhere, so it is all
  ridges and creases. Lower `edge` and `drybrush`, or raise
  `drybrush_threshold`.
- **Distant hillsides are speckled.** Lower `fade_distance`.

`FOUNDRY_STYLIZE_DEBUG` shows what the paint pass sees:

| Value | Shows |
| --- | --- |
| `1` | the normal buffer: black where nothing wrote, green where a surface's record is good, red where something drawn later has covered it |
| `2` | the paint's masks: red the edge highlight, green the dry-brush, blue the wash |

For example: `FOUNDRY_STYLIZE_DEBUG=2 foundry play run -level mylevel -script 'shot'`.
