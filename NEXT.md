# Features

## Done

- [x] The sky is scriptable, and a script can ease between two of them.
      `@level.prop.sky` (on/off, existence rather than a field), `sky_kind`,
      `sky_cubemap` (the editor's "fill from base name" button as a property),
      `sky_zenith`/`sky_horizon`/`sky_ground`, `sky_tint`, `sky_intensity` and
      `sky_rotation` are all writable now, with the same auto-vivify a fog
      volume gives a plain trigger: writing any of them on a level with no sky
      block gives it one at the defaults first. `ease_sky(seconds)` is the
      transition: it arms a blend that the very next `sky_*` writes are eased
      into rather than snapped to. A colour lerps, a `kind` cannot, so the blend
      is one level down from the fields — `world.Sky` keeps a runtime-only
      `TransitionFrom` snapshot plus an elapsed/duration pair `world.SkyUpdate`
      advances once a frame (the same way `AdvanceChunks` and `EmitterUpdate`
      make time pass for theirs), and `sky_frag.glsl` evaluates the sky twice —
      once for the state being eased away from, once for the one being eased
      toward, each with its own kind, cubemap, gradient stops, tint, intensity
      and rotation — and mixes the two finished colours by how far into the
      ease the level has ticked. `sky_vert.glsl` lost its own yaw so both
      evaluations could apply their own. Retriggering `ease_sky` before an
      earlier blend finishes does not splice the two: it starts the new ease
      from wherever the first one was heading, not from the partway point the
      screen was actually showing, which is one visible snap rather than a
      second field of interpolation math to keep in step with the shader's own.
- [x] A face selected in the 3D editor had no gizmo. Points and edges have had
      one since they became selections in their own right — an edge selection
      *is* the selection of its two endpoints, so the tools drag it without
      knowing edges exist — but a picked wall or floor drew its outline, opened
      its panel, left G/R/C showing as the tool in your hand, and put nothing in
      the viewport to grab. A face gets the same deal: it resolves to the corner
      handles it is built from (a wall is the four corners of its quad, a floor
      or ceiling its ring on one side), and every write path below the gizmos
      goes through that one resolver, so move, rotate and scale all work with no
      transform code of their own. The handles are derived rather than selected,
      so the right-click menu still offers the face's actions and the vertex
      panel stays shut.
- [x] A dropship, as content. `entities/dropship.yaml` is an assault shuttle
      with a hold you walk into and a powered blast door: two model attachments
      (`cmd/genmodels` generates the hull and its canopy, the way `gentextures`
      generates textures), a chunk attachment for the inside, four new textures,
      and `scripts/dropship.flang` for the sequence — walk in, the door shuts,
      the ship drops two hundred metres, the door opens somewhere else. It is
      deliberately not a vehicle: a frame comes from owning a chunk rather than
      from being drivable, so the passenger rides it by standing in it and the
      script simply writes the ship's position.
      **The door is a ceiling.** The doorway is its own thirty-centimetre sector
      and its ceiling is the leaf: `draw_gap` fills the height difference either
      side of it, so lowering it *draws* the door, and `ResolveCeilingEntry`
      refuses a move into a sector whose ceiling is at or below the floor being
      left, so lowering it to the deck *seals* the ship in both directions with
      nothing arranged per frame.
      Two things came out of building it. `@player.prop.<anything>` never
      worked — the property tables resolve entities through the ref cache, which
      knew nothing about the engine-owned player, so a documented address met a
      documented property and reported "no property". And the rule that a frame
      portal's two halves must *overlap* rather than meet, which nothing said
      anywhere: crossing fires on proximity, so co-locating them drops the
      walker into a sector neither space contains. Both are covered by tests and
      the second is in DESIGN.md. `levels/dropship_demo.yaml` is the ride
- [x] Bump and normal maps. A texture document carries a second layer stack
      beside its colour one — the bump channel — painted with the same brush,
      line, fill and layers, one *height* per texel rather than a palette
      index. The engine samples the tangent-space normal map derived from it
      (Sobel over a wrapped height field, strength baked in) and written beside
      the albedo as `<name>_normal.png`. Relief is a property of the texture,
      not of the surface: no level file mentions it, and nothing per-surface
      has to be switched on. Applies to walls, floors, ceilings, surface
      meshes, models and detail patches, on a tangent frame `fragment.glsl`
      reconstructs from screen-space derivatives so no mesh needed a tangent
      attribute; animated textures get animated normal maps on the same clock
- [x] Music and sound effects. `internal/audio` is the mixer: SDL2 output with
      no new native library, pure-Go WAV/OGG/MP3 decoding, a 32-voice pool with
      priority stealing and declick ramps, distance attenuation, constant-power
      panning, cones and doppler. `world.Sound` is the placed source — attachable
      to a frame, entity or bone like a sprite — with an editor panel, range
      rings, picking, undo and YAML round-trip; `world.Music` is the level's
      global track. Scripts get `play`, `stop`, `play_sound`, `play_sound_at`,
      `play_music` and `stop_music`, and a `sound` ref with the placement's
      properties. Master/music/effects gains live in the settings panel and are
      the first setting in the engine that survives a restart.
- [x] Directional lights
- [x] Global lights
- [x] Point lights
- [x] Ability to individually edit sector points positions
- [x] Ability to add/remove sectors
- [x] Ability to snap points together so we can connect sectors
- [x] Ability to fullscreen the application
- [x] Should be able to connect to vertices to create new sectors
- [x] 3D level editor should let me click on a face, vertex or edge to edit its properties
- [x] Shadows
- [x] Gravity (global)
- [x] 2-way rendering for walls/ceilings/floors
- [x] Optional FPS/position/debug/etc. display
- [x] Sector/chunk culling for rendering
- [x] UV editing
- [x] Textures should be loaded from a directory traversal, not a hard-coded map
- [x] Water
- [x] Procedural detail
- [x] Smooth shading (per-surface normal smoothing, including displaced detail)
- [x] Should start on empty world
- [x] Detail shadow casting
- [x] Multi-window mode
- [x] Remove fake ground-contact shadows (dynamic shadows only)
- [x] 3D mode edge selection
- [x] Skeletal animation (pose mode, keyframes, GPU skinning)
- [x] Surface meshes — one editable mesh per wall/floor/ceiling, shaped in the 3D
      editor with the model editor's gestures (extrude, inset, subdivide,
      connect, open). Replaces extrusions, terrain points and per-surface
      displacement; old levels migrate on load
- [x] Remove entities/primitives from levels, and the scenery editor
- [x] Entities, reintroduced: reusable definitions in `entities/`, instances
      placed in a level, several named model attachments each with its own
      transform and animation set, tags, typed attributes, velocity and angular
      velocity
- [x] 2D mode point/edge/face selection modes (shared with the 3D view)
- [x] Move/rotate/scale gizmos in both editor views, acting on a multi-selection
      about its own centre
- [x] Ctrl+click multi-select on the 2D map, and Join (J / Shift+J) to turn the
      picked points into edges — replaces the ctrl+click connect chain
- [x] 2D map right-click context menu
- [x] Multi-select faces (ctrl+click) in both views; texture, visibility,
      collision and wall/portal conversion apply to the whole selection
- [x] Right click action menu (3D editor and model editor)
- [x] Selection modes and gizmo tools shared between the 2D map and the 3D view
- [x] 3D mode edge selection
- [x] Model editor
- [x] Animation editor
- [x] Models
- [x] Model bones
- [x] Animations
- [x] Volumes (scriptable regions; box/sphere/cylinder, tags, attributes)
- [x] Level load triggers — a volume carrying `load_level:` is the level exit:
      the player walking in loads that level, through the same deferred request
      `load_level` records, with no script needed. Only the player takes one,
      only while it is `enabled`, and a script attached to the same volume still
      gets its `:enter:` in the frame before the load starts
- [x] Enabling and disabling chunks — `@chunk.prop.enabled`, or `disabled: true`
      in the level file. A disabled chunk is not drawn, not collided with, out
      of the spatial hash and has its vertex buffers released; one authored
      disabled is never built at all. That makes the chunk the unit a level
      streams by: switch the next area on, drop the one behind
- [x] Script editor
- [x] Log window (live script output, errors with positions, engine log)
- [x] Scripting (flang: `internal/flang`, `@id`/`#tag`, attributes and properties, messages, events, hot reload)
- [x] Script parameters (`param name type`, bound per object in the editor, read as `?name`)
- [x] Object collision physics (push-apart; no restitution or friction yet)
- [x] Vehicles (seated and walkable-interior, 6DOF, nested frames, frame portals)
- [x] Frames — moving coordinate systems that geometry and entities can live in
- [x] All windows should have the exit button on the top
- [x] New windows should open on top of the existing even in fullscreen mode
- [x] Re-do top bar
- [x] Re-organize 3D editor settings panel
- [x] Re-organize 2D editor settings panel
- [x] Re-organize model editor settings panel
- [x] Re-organize texture editor settings panel
- [x] Re-organize script editor settings panel
- [x] Re-organize log settings panel
- [x] One Explorer tree over every object in the level, with the selected
      object's properties under it and nothing else — replaces the six
      always-open Objects sections and their per-row button rows
- [x] Properties grouped by subject: everything about a floor (shape, UV,
      skirt, detail, tags/attributes/script) folds away under Floor, and every
      object type is grouped the same way
- [x] Entity-against-world and entity-against-entity collision, as per-type
      toggles (`collision: {world, entities, gravity, movable}`) with engine-wide
      master switches on 8 and 9 — no scripting needed
- [x] `enter`/`exit`/`interact` events over the overlap tests, plus `collide`,
      `spawn`, `destroy`, `tick` and the two animation events
- [x] Shape-aware push-apart (GJK/EPA over support functions; axis-aligned box
      pairs keep their own tuned min-axis path)
- [x] Swept collision — motion is split into steps no longer than the body, so
      nothing passes through a wall it was moving too fast to be sampled against
- [x] Scripting: `@id` and `#tag` addressing, and reading/writing attributes
- [x] Animated textures
- [x] Texture transparency
- [x] Texture blending
- [x] Animation blending between clips (crossfade on switch, configurable per
      animation via `blend:`, default 0.15s, 0 for a hard cut)
- [x] Sprites — camera-facing quads placed in a level, in three blend modes
      (cutout draws in the opaque pass and antialiases for free; alpha sorts;
      additive is order-independent)
- [x] Particles — `.fpar` effects authored in the particle editor and placed as
      emitters, simulated on the CPU and drawn through the same instanced
      billboard pass sprites use
- [x] Skeleton sockets — sprites and emitters attach to a frame, an entity, or a
      named bone inside one of its model attachments
- [x] Walking into a drawn gap that is taller than the player causes the player to walk through it
- [x] On exit play the camera position in the 3D editor should be reset to when play was initiated
- [x] Hitting play should not close my other editors
- [x] The current layer should always be displayed on top of the other layers so its selections are visible
- [x] Moving through geometry in the 3D editor causes some of it to just stop being displayed
- [x] Subdividing a wall surface face next to an already-subdivided one split it
      into five or six pieces instead of four
- [x] Point lights could only be moved by "Move To Camera" — they now take the
      translate gizmo in the 3D view, are clickable in the viewport, and have
      typed coordinates in the panel
- [x] Ctrl+Z while shaping a surface dropped out of surface shaping mode
- [x] A camera turn or gizmo drag released over the side panel never ended, so
      the view stayed stuck to the mouse until an unrelated right-click
- [x] A release build drew no biome scatter, ground patches, procedural face
      detail, sprites or particles. `GrassInit`/`DetailInit`/`SpriteInit` were
      called from `EditorInit`, and `-tags release` compiles the editor out
      entirely — so a shipped game called none of them, and because every pass
      early-outs on its own readiness flag, nothing logged, errored or crashed.
      They are one `render.InitWorldPasses()` now, called from the engine's own
      startup in both builds, with a test either side of the seam: one that the
      engine calls it and the editor does not, one that the call leaves every
      pass able to draw
- [x] `world_height` on floors and ceilings — `height` stays the writable,
      chunk-local, round-trippable number, and this is its read-only world-space
      companion, so a surface's Y can be compared against a player's or an
      entity's without mixing spaces
- [x] `resources/scripts/button.flang` — the parameters example SCRIPTING.md
      tells authors to copy, which did not exist as a file
- [x] Skybox and distance fog. Two optional level-scoped blocks (`sky:`,
      `fog:`), so no existing level changed. The sky is one cube drawn between
      the opaque and transparent passes at depth 1.0, so it costs only the pixels
      the world left uncovered; it is either a six-texture cubemap or a
      three-stop gradient that needs no art, and an incomplete cubemap falls back
      to the gradient rather than to nothing. Fog is declared once in
      `shaders/fog.glsl` and spliced into every stage that shades — world,
      detail, biome scatter, ground patch, sprites and the sky — so nothing can
      fade at a different rate from the thing beside it; linear/exp/exp2, radial
      from the eye, with additive sprites attenuated rather than tinted. The two
      meet at `sky_blend`, which pulls the fog colour into the horizon so the
      ground does not fade into a crisp sky. Editor: the Environment block
      in the properties panel
- [x] Fog volumes. A volume may carry a `fog:` block of its own, and the region
      fills with fog — fog that has a position, where the level's `fog:` is the
      air of the whole level and has none. The term is the *length of the view
      ray inside the region* rather than a distance, so a bank is thick along its
      length and thin across it, a wall past its far face is fogged by exactly
      its depth, and walking in fills the screen; every shape is solved in a
      normalized space where it is the unit box, sphere or cylinder, so one
      routine per shape covers all sizes and rotations. It lives in the existing
      `fog.glsl` behind the existing `applyFog`, so the banks reach walls, grass,
      scattered rock, sprites and the sky with no pass changing, and
      `UploadFogTo` took the volumes as a second argument so forgetting one is a
      compile error. The sky takes them at full strength rather than the horizon
      share it takes of distance fog, since a volume is bounded. Eight at once,
      truncated in level order like point lights. A field on `Volume` rather than
      a new placed type, for the reason `load_level` is: the shape, rotation,
      extents, `enabled` flag, cage and gizmos already existed. Editor: the Fog
      group in the volume panel. Scripts: `fog`, `fog_color`, `fog_density`,
      `fog_max_opacity`, and disabling the volume puts the fog out with it
- [x] UI video playback. A `video` widget plays a clip from `video/` beside
      `sounds/`, decoded by `internal/video` with no new native dependency:
      motion-JPEG in an AVI (`ffmpeg -i in.mp4 -c:v mjpeg -q:v 3 -c:a pcm_s16le
      out.avi`) or an animated GIF, whose patches are composed against their
      disposal methods rather than drawn raw. A file's PCM soundtrack is demuxed
      into an `audio.Clip` and played on the music bus, and seeking it is a
      sub-slice of the samples rather than anything the mixer had to learn. The
      widget carries the clip, autoplay, loop, mute and volume; the playhead
      lives in a per-stack player registry swept once a frame, so a cloned
      document never starts half way through. Frames upload only when the
      decoder's revision moves. Scripts get `playing`, `time`, `duration`,
      `loop`, `muted`, `volume` and `video` as properties and `:finished:` as an
      event; the UI editor gets a clip picker and a transport that previews it
      in place
- [x] Release builds and installers. `-tags release` compiles the engine without
      the editor, the four sub-editors or imgui (`internal/app/devtools_*.go` is
      the seam; `make release-check` proves they are gone); `game.yaml` names the
      start level, the start screen and the window; settings and logs move to the
      per-user data directory so an installed game never writes into its own
      install; `cmd/foundrypack` stages a tree with only the assets the game can
      reach from its manifest; NSIS makes a Windows Setup.exe and
      `packaging/linux` a .deb or a tarball with an installer in it. See
      docs/RELEASE.md
- [x] Brushes, and the scenery they place. `world.Scenery` is a model with a
      position, rotation, scale and a collision flag, and nothing else — no
      script, no tags, no animation and no authored bounds; the collision volume
      is the model document's own AABB carried through the placement, so there
      is nothing to dial in. It is the small type beside an entity, for the case
      where a level wants a thousand of them. The *brush* is what makes them:
      pick a model and a translucent ghost of it follows the cursor onto
      whatever surface is under it, `[`/`]` turn it, `-`/`=` resize it, and every
      left-click stamps a copy — the brush stays armed and does not select what
      it made, because the thing to do after placing a rock is place another
      rock. Escape puts it away; random-yaw gives every copy its own facing.
      Props draw and cast shadows through the same passes entities do, are
      solid to the player and to any world-colliding entity (one immovable
      proxy through the existing push-apart, so a prop dropped at an angle is
      solid at that angle), and round-trip as a four-line `scenery:` entry each
- [x] Hitscan. `hitscan(:atom:)` casts a ray from the reticle — the centre of
      the screen, which is exactly where the camera looks — and posts
      `(:atom:, hit, at)` back to the caster: the nearest object hit, or with
      `{"all": true}` a list of everything in the line of fire, plus the point
      the ray stopped at. It answers with a message rather than a return value
      so the handler for "I hit something" is the same shape as the one for
      `:collide:`. Bodies do not stop the ray and geometry does, which is what
      lets one cast answer both questions; a body is met at the *bounding
      volume* `:enter:` and the resolver already work in, so a shot and a shove
      agree about where a thing is and an animated model's hitbox does not
      wander, while geometry is met at its real triangles. A wall comes back as
      the edge ref `@sector.edge.<id>` produces, so `hit.prop.texture` works on
      what the shot landed on. Options are `all`, `range`, `entities`,
      `volumes`, `geometry`, `visible_only`, `tag` and `ignore`, split into the
      physical ones that change where the ray goes and the reporting ones
      applied after it has already been stopped; an unknown key is a typo, and
      the linter says so with a suggestion before the script runs. The cast
      itself is `internal/collision`'s and is tested with no GL context
- [x] Vector components in flang. `v.x`, `v.y` and `v.z` read a vec's three
      floats, always as `float`, so a script can finally decompose the impact
      point a `hitscan` hands back — a headshot check, a knockback along one
      axis, anything comparing a height. Read-only, and there is no `v[0]`: a
      vec is a value rather than a container, so the one a property hands back
      is a copy and `@self.prop.position.y = 1.0` would quietly write to the
      copy. Rebuilding (`<p.x, p.y + 1.0, p.z>`) is the spelling that always
      means what it looks like, and both mistakes say so
- [x] Entity editor. A window of its own that edits a *type* rather than a
      placement: `entities/<name>.yaml`, with a 3D view of the thing being
      assembled, an outline of what it is made of down the left, and the type's
      own properties down the right. Models, bounds, collision, tags,
      attributes, the script and its parameters, and a vehicle block with its
      seating plan — everything the level editor's entity panel could only ever
      set on one placed copy. Rigged attachments preview in motion: the model
      editor's headlight shader took the shared skinning path (`skin_common.glsl`)
      so an .fmdl's clips can be named and played here rather than guessed at.
      Saving writes the file, drops the definition cache and re-stamps every
      instance of that type in the level open beside it, so the change is
      visible in both windows at once. Reached from the toolbar's **Entity**
      button, from **Edit Type** beside a placed entity, and from **Make Type**
      beside a blank one — which seeds a new definition from something already
      placed, and is the blank-entity promotion `entities/README.md` describes
- [x] Entities carry chunks. A definition may name geometry — a file under
      `chunks/`, placed in the entity's own space — and every instance
      materialises it as a chunk bound to that entity's frame. The frame
      machinery already existed for vehicle interiors; what was missing was a
      way to author the binding on a *type*, so a drivable ship with a hold you
      can walk around in was a level's hand-wiring rather than a thing you could
      place twice. The reference is by file: the level stores the placement and
      never the geometry, a materialised chunk is skipped by the level saver and
      rebuilt from the definition on load, and a fix to the hull reaches every
      ship in the game. The entity editor's import dialog offers both sources —
      a file already under `chunks/`, or a chunk of the level open in the main
      window, which it exports and attaches in one click

- [x] Floor and ceiling thickness. A surface was a plane, and from the room under
      a mezzanine that reads as a sheet of nothing. `thickness` turns one into a
      slab: a second face offset away from the room — down from a floor, up from
      a ceiling — and a rim round the perimeter joining the two. The far face is
      a face in its own right, clickable in the 3D view with its own texture,
      blend slots, rim texture, UV and double-sidedness, which is the point:
      what you see from underneath a floor is rarely what you want to walk on.
      It is built from the *same* surface mesh and the same openings as the
      surface it hangs off, so carving a pit dips the underside with it and
      cutting a stairwell takes the hole all the way through — a slab is a
      constant offset of the shape above it, not a second floor free to drift
      away from the first. Two things fell out of building it. The offset frame
      has to keep the surface's own winding, because `SyncSurfaceBoundary`
      rewrites boundary points on every rebuild and a reversed outline would
      renumber the corners of the mesh the two share; the far face is turned
      outward by reversing its triangles instead. And the rim skips *portal*
      edges — across a portal the slab is not exposed, it continues into the
      sector next door, and drawing both rims is a z-fighting seam down every
      doorway. Alpha and smoothing stay the surface's, since it is one piece of
      material. Thickness 0 is the default, builds nothing and writes nothing.
      Scripts get `thickness`, `slab_texture`, `slab_rim_texture`,
      `slab_hidden` and `slab_shaped`
- [x] A thick floor is solid, and its two faces shape independently. Two
      follow-ons to the slab above, and each fixes something the first pass left
      half-done. **Solid:** what used to stop a body walking under a mezzanine
      was the ceiling of the room down there, so a mezzanine over an open hall
      had nothing underneath it and you rose straight through the floor above. A
      slab's underside is a hard limit now, folded into the single query every
      clamp in `internal/collision` already went through — player, entities,
      particles, portal entry and step entry all get it from one place — and
      asked relative to the *sector's own floor* rather than to the body, so the
      answer does not depend on which frame a jump lands in. Openings cut the
      whole solid, so a stairwell stays climbable; a ceiling slab gets none,
      because a ceiling's underside is already the head-stop it always was.
      **Independent:** the far face keeps mirroring the surface's mesh until you
      shape it, and then it has one of its own — a vaulted ceiling under a flat
      roof, a floor with joists under it. Shaping starts from whatever the
      mirror was showing rather than from a flat ring, which works because the
      two frames share a parameter space; a button in the panel empties the mesh
      and puts it back to following, which is a different thing from surface
      mode's "Reset to flat" and says so. The perimeter stays pinned to the
      sector outline either way, so the rim meets both faces at their corners
      however the interiors are carved
- [x] Gizmos in the entity editor. Move, rotate and scale on the same G/R/C keys
      the 3D view and the model editor use, drawn with the same arms, rings and
      handles and running the same shared drag maths through this window's own
      camera — an author who has dragged a chunk into place in a level already
      knows how to drag a hull into place on a type. What a drag writes is
      whatever the outline has selected, and each kind offers exactly the
      channels it has: a model attachment its offset, a full Euler rotation and
      a per-axis scale; a chunk its offset, a Y-only rotation and a uniform
      scale, because a chunk's placement has never had more; a seat its position
      and its yaw, with nothing to scale. A tool with nothing to write shows no
      handles rather than offering a drag that would quietly do nothing. The
      grid toggle moved to Shift+G to free the key. Two things fell out of it:
      the preview now takes a transform-only sync, since a full rebuild re-reads
      every chunk file off disk and re-uploading that per motion event is the
      difference between a live drag and a stalled one; and the seat marker's
      facing arrow, which was hand-rolled from a cosine and a sine, turned out
      to spin the opposite way to `world.SeatMatrix` — it is drawn through that
      matrix now
- [x] Configurable step height that player can walk over
- [x] Should be able to set entity velocity from scripts
- [x] Can't shift click between two points in the editor to select everything between them
- [x] Update after to use the message post format so I can post it to whoever I like
- [x] Use ?param for referencing params instead of @param
- [x] UI builder
- [x] Entity editor
- [x] Closing the main editor should close all other windows

## TODO

> NOTE: `[/]` denotes an in-progress task

### Bugs


### Editor

- [ ] Debugger
- [ ] Configurable editor keybindings
- [ ] Audio mixer
- [ ] Bounding box view
- [ ] Gizmo in 3D editor to edit texture UVs


### Engine


- [ ] Physics engine
- [ ] Configurable keybindings, other user settings
- [ ] Water reflections
- [ ] Volumetric displacement (e.g. sand, snow)
- [ ] Restitution/friction, and more than one solver iteration per frame
- [ ] Potentially change the max number of frames for animated textures as well as frame spacing/interpolation
- [ ] Volumes are brushes instead of boxes only
