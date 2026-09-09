# Foundry — designer's guide

Foundry is a 3D engine with its editor built in. You block levels out of convex
solids — **brushes** — shaped in the 3D view the way a model is, then fill them
with entities, props, effects and scripts. Everything here is authored in the
running engine: there is no export step and no separate viewer.

> **TODO(brush migration, Phase 10):** levels used to be built out of *sectors*
> — 2D floorplan polygons with a floor and ceiling height, drawn on a top-down
> map. That model, and the 2D editor that went with it, have been replaced. Some
> chapters below still describe the old one and are being rewritten; the ones
> updated so far are [Editor basics](01-editor-basics.md),
> [Building levels](02-building-levels.md),
> [Shaping surfaces](03-shaping-surfaces.md), [Smooth shading](05-smoothing.md),
> [Scatter](06-scatter.md), [Prefabs](22-prefabs.md),
> [Vehicles and frames](12-vehicles-and-frames.md) and
> [Compiling levels](23-compiling-levels.md).

Start the editor:

```
make run-linux          # or: make run-windows
```

It opens on a blank level with the editor active. Press `` ` `` (backtick) or
`~` to drop into play, and again to come back. Press `F1` at any time for the
keybinding and API reference, which is generated from the engine's own tables
and is always current.

## The guides

| | |
| --- | --- |
| [Editor basics](01-editor-basics.md) | Windows, toolbar, selection, gizmos, cameras, undo, saving |
| [Building levels](02-building-levels.md) | Chunks, brushes, faces, convexity, dynamic brushes |
| [Prefabs](22-prefabs.md) | Reusable groups, linked by file: build once, place twenty |
| [Shaping surfaces](03-shaping-surfaces.md) | Edit mode shapes the solid; detail mode shapes what you see |
| [Textures](04-textures.md) | The texture editor, palettes, layers, animation, bump maps |
| [Smooth shading](05-smoothing.md) | Welding normals so a run of faces reads as one curved surface |
| [Scatter](06-scatter.md) | Volumes of grass, rock, wildflowers, cattails and moss |
| [Lighting](07-lighting.md) | Global, directional and point lights; shadows and their cost |
| [Sky and fog](08-sky-and-fog.md) | Skyboxes, gradients, distance fog, fog volumes |
| [Models](09-models.md) | The model editor: primitives, shaping, UVs, OBJ import |
| [Animation](10-animation.md) | Armatures, skin weights, pose mode, clips |
| [Entities](11-entities.md) | Types and instances, bounds, collision, attachments |
| [Vehicles and frames](12-vehicles-and-frames.md) | Drivable things and walkable interiors |
| [Volumes](13-volumes.md) | Trigger regions, level exits, fog volumes |
| [Scenery and the brush](14-scenery-brush.md) | Stamping hundreds of static props |
| [Sprites and particles](15-sprites-and-particles.md) | Billboards and the particle editor |
| [Audio](16-audio.md) | Placed sounds, music, attenuation, the mixer settings |
| [UI screens](17-ui-screens.md) | The UI editor, anchors, bindings, themes, video |
| [Scripting](18-scripting.md) | flang: events, messages, properties, parameters |
| [Testing and debugging](19-testing-and-debugging.md) | Play mode, the log window, engine switches |
| [Compiling levels](23-compiling-levels.md) | CSG, visibility, baked light, Preview — and why a level has to be sealed |
| [Shipping a game](20-shipping.md) | Release builds, `game.yaml`, installers |
| [Keybindings](21-keybindings.md) | The whole table, in one place |

## Where things live

Everything the game reads is under `resources/`. Names in a level file are names,
not paths — the loader resolves them against the folder for that kind of asset.

```
resources/
  levels/      .yaml    a level: chunks, lights, entities, volumes, sky, fog
  chunks/      .yaml    exported geometry, reusable across levels
  entities/    .yaml    entity type definitions
  models/      .fmdl    meshes, armatures and animation clips (.obj also loads)
  textures/    .png     world textures; <name>_normal.png beside one is its bump
               .ftex    the layered document the texture editor edits
  particles/   .fpar    particle effects
  sounds/      .wav .ogg .mp3
  video/       .avi .gif
  ui/          .fui     screens; theme.fth is the shared palette
  scripts/     .flang   scripts, in any folder structure you like
  fonts/       .ttf
  game.yaml             read only by a release build: title, start level, menu
```

## Units and conventions

- One world unit is roughly one metre. The player is about 1.8 units tall.
- Y is up. Rotations are shown in degrees everywhere and stored in radians.
- A sector's floor and ceiling heights are **chunk-local** — they are relative to
  the chunk's origin, so moving a chunk moves everything in it.
- IDs are unique across a level. Tags are not, and that is the point.
