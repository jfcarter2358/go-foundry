# Features

## Done

- [x] Save games and checkpoints. A save names its level and records
      everything play has changed in it. That covers:
      - every entity's fields, AI, animation clip and model state, including
        entities a script spawned (made again from their type under the same
        ID) and ones it destroyed (taken out again)
      - chunk switches and moves, dynamic brushes, volumes, lights, sprites,
        emitters, sounds, effects, the sky and the music
      - the player and their view
      - every script's `.state` and pending `after` timers, the play clock,
        which objects have heard `:spawn:`, who stands in what, and the
        checkpoints reached
      - the non-modal screens up, with what scripts wrote to their widgets

      Loading a save loads the level fresh through the ordinary level-load
      path and lays the save over it (`internal/savegame`), matching objects
      by ID. So a compiled and an authored level save alike, and a save
      survives the level being edited. The format is gob with a separate
      header (`internal/savefile`), in `saves/` beside `settings.yaml`.
      level1's save is about 110 KB, taken in about 3 ms and applied in about
      13 ms.

      **Checkpoints**:
      - A volume's `checkpoint: true` (the volume panel's Checkpoint group), or
        `checkpoint(name)` from a script.
      - Player only and once per play-through. It autosaves between frames,
        becomes what `retry()` returns to, and posts `(:checkpoint:, name)`
        to the player and the screens.
      - The death screen now has Retry (the last checkpoint, else a restart)
        and Restart Level (`restart_level()`).
      - The last checkpoint rides inside every save, so dying after a load
        retries from the checkpoint, not from the load.

      **Slots**:
      - `save_game`/`load_game`/`save_info`/`latest_save`/`delete_save`.
      - Per-level `save_mode: exact | checkpoint` (the level's Saving group):
        a `checkpoint` level saves its last checkpoint rather than the exact
        state.
      - `@level.prop.name`/`save_mode`/`last_checkpoint`.

      **Fixed on the way**: `installLevel` never stopped the outgoing level's
      voices, so its looping ambience carried on under the next level.

      **Content**:
      - level1 has `checkpoint_lz` (the landing-zone floor, below the hovering
        hold) and `checkpoint_basin` (the canyon mouth into the open basin
        with the compound and AA emplacement).
      - Pause menu Save/Load/Load Last Checkpoint, a main menu with Resume
        (loads the newest save) and Load Game, and shared
        `save_game`/`load_game` screens (`scripts/menus/saves.flang`).
      - A HUD "Checkpoint reached" toast.

      `levelsim -restore-at t` saves, reloads, restores and diffs the result,
      and prints `CHECKPOINT` requests. See `docs/guide/26-saves-and-checkpoints.md`.

- [x] Friendly units and squads. An AI agent can follow a leader — `ai: follow:`
      or `ai_follow(@agent, @player)` — keeping a place in a loose group
      spread across an arc behind it (each member nudged by an amount fixed to
      its ID), measured against the way the leader has been walking rather than
      its facing. It
      walks alongside a leader on the move, settles on its slot when the leader
      stops, runs at `run_speed` (playing `run_anim`) once it has fallen well
      behind, and tries a spot closer in when its own is off the grid or a long
      walk from the leader. With a target it fights from where it stands until the
      leader is `follow_leash` away, then regroups — back to its slot still
      facing and engaging. A waypoint path outranks the leader, so a squadmate
      can be sent somewhere and falls back in at the end. New modes
      `:escorting:` and `:regrouping:`; props `ai_follow`,
      `ai_follow_distance`, `ai_follow_leash`, `ai_run_speed`; a Leader group
      in the AI panel.
      Agents now ride ships: an AI walker that steps onto an entity's own
      brushes boards its frame (and steps off again) as the player does, the
      controller steers in world space and carries velocity and yaw into the
      frame, and aboard something moving it holds where it stands until it
      stops. Navigation bakes a frame's chunks while their frame is at rest and
      upright (`world.FrameAtRest`), so a parked ship's hold is walkable; an
      agent aboard gets no grid while a rebuild is pending. A target that is
      hidden or has its controller switched off is no longer a target — how a
      dead or sleeping agent drops off the other side's list.
      Content (level1): `entities/friendly_rifle.yaml` (the enemy rifleman in
      `friendly_uniform`, `enemy_combat.flang` told its side by `ally_tag`/
      `foe_tag`/`fights_player`); enemies hunt `#friendly`; player rounds pass
      through friendlies, grenades and rockets hurt them. `squad_1` waits in the
      hold, `squad_2` walks in along `squad_2_board` once the player is aboard,
      and the ship leaves when both are in; at the landing zone they jump down
      along the ship type's `disembark_1/2` paths and fall in behind the
      player (`scripts/level1/dropship_trigger.flang`).

- [x] AI waypoint paths. An AI agent walks a waypoint animation stop by stop —
      its `ai: waypoints:` (a level ID or its type's own path name, with
      `waypoints_loop`) or `ai_follow_waypoints` — finding its own way to each
      point and ignoring the times, and posting the same `:waypoint:`, point
      events and `:waypoints_end:` a brush gets. Each point may carry an `ai:`
      block (`world.WaypointAI`): a volume to stop anywhere inside instead of
      the arrow, a wait, wait-for-script (`ai_continue`), a speed multiplier,
      an engage mode for the leg and stop (inherit, chase, hold — keep to the
      path while facing and engaging — or ignore), take cover with a radius,
      face the arrow, and an animation. Taking cover samples reachable nav
      points (`nav.Grid.SamplePoints`) and keeps those where `LineBlocked`
      hides both chest and head from the threat's eye — the target, or with none
      a point along the arrow — rechecking twice a second (`:cover_taken:`,
      `:cover_lost:`); a cover stop holds while the agent still has a target.
      Settings on a point govern the leg arriving at it and the stop. The editor
      has a Point AI group in both the level and entity editors, Create Volume
      Here, cover rings, wait marks and volume links on the arrows, a Waypoints
      row in the agent's AI group, and the route preview follows the path.
      Also fixed: an agent whose goal jumped by less than half a metre could
      report arriving at its old path forever (`follow` now re-plans at once
      when the goal moves more than half the arrive radius).

- [x] Raycasts are spatially culled. Each chunk keeps a BVH over its static
      faces in its own coordinates (the ray is carried in through the chunk's
      live transform, so moving and frame chunks keep their trees); a compiled
      level keeps one over its surfaces; dynamic brushes sit outside the trees
      with boxes cached against their placement. Boxes come from each face's
      drawn vertices. Traversal is nearest-first and shrinks its range as it
      hits, and entities, volumes and scenery are tested only up to the
      nearest geometry, behind a trig-free bounding sphere. `LineBlocked` is
      the any-hit form, and AI line of sight uses it. Trees are invalidated by
      `world.BrushMeshGeneration`, which BrushMesh.Load/Release/Unload and the
      Dynamic checkbox bump. On a 10,000-brush level a 20-unit sight check went
      from ~7.3 ms to ~7.5 µs and a 1000-unit hitscan to ~27 µs; the old
      brute-force cast is kept as a test oracle and 9,000 randomised rays must
      match it exactly.

- [x] AI controllers. An `ai:` block on an entity or a type makes it walk the
      level by itself, with three behaviours out of the box: going where a
      script sends it (`ai_move_to`), hunting what is on its Targets list —
      `player`, IDs, `#tags` — once it hears or sees it (range, field of view,
      line of sight, a lose timer), and working **objective volumes**: a volume's
      new `objective:` block (action atom, duration, animation, capacity, agent
      tag, face, once) is somewhere an agent walks to, performs, and reports to
      both itself and the volume. Sequence, nearest and random order, looping
      for patrols, and wandering when idle.
      Navigation is `internal/nav`: a Recast-style column heightfield
      rasterised from every static solid brush — each face clipped to a
      column's whole square, so thin walls are not missed — then per agent size
      a graph of standable nodes linked by step and headroom, eroded by radius,
      labelled into regions, and searched with A* and string-pulled over the
      links. It is built on first use and rebuilt when a hash of the level's
      static solids changes; dynamic brushes, frame chunks and disabled chunks
      stay out of it. `internal/ai` runs the controllers before EntityUpdate
      and only ever writes Velocity and yaw, so collision is unchanged — except
      that AI-driven entities now step up stairs (`resolveEntityWorld` uses
      `StepUp`), and an entity dressed only in non-solid brushes collides by
      its bounds instead of by nothing. Events go through `hooks.AIEvent`.
      The editor has an AI group (placed entities and types), an AI Objective
      group on volumes, and **Show navigation**, which draws the walkable edges
      near the camera and the selected agent's route through its objectives.
      `resources/entities/guard.yaml` and `scripts/guard.flang` are the example.
      See `docs/guide/24-ai.md`.

- [x] Waypoint animations carry more than brushes. `play_waypoints` takes an
      entity, a sprite, an emitter, a sound, a light or a volume as well as a
      dynamic brush, and each reads the parts of a point it has: an entity all
      three channels; a sprite all three as well, on a runtime `Sprite.Scale`
      that multiplies its width and height (`DrawnSize`, which the renderer,
      the culling bounds and editor picking read, and a `scale` property); an
      emitter, sound or volume its position and rotation; a point light its
      position; and a directional light the
      arrow's aim — the turn that points +X along the light
      (`world.DirLightEuler`), so a sweeping sun is a row of arrows.
      The travelling was already one pose function, so what made the rest
      possible is `world.WaypointFollower` (waypoint_follow.go): the three things
      the step needed from a brush — a pose, a way to be put at one, a progress
      slot — so the step, the seek and play/stop are written once. Position stays
      world space for every kind, divided every tick through whatever the object
      rides: a chunk, an entity's or a volume's frame, a sprite's Attach.
      Entities step in EntityUpdate beside their tweens, without gravity; the
      rest in `AdvanceWaypointFollowers` after RefreshFrames, which reports a
      moved light so the frame loop uploads the uniforms again. Arrivals carry a
      `hooks.WaypointObject` kind and are posted to the ref that arrived, every
      kind reads `waypoints`, and the player and vehicles are refused as
      `move_to` refuses them. In the editor the preview takes any of them from
      an **Object** box, and each kind's panel has a **Waypoints** group. Every
      kind but a brush is posed by writing fields the level file holds, so the
      preview keeps a `world.WaypointRest` and lends it back to undo snapshots,
      saves and compiles.

- [x] Waypoint animations. A path for a dynamic brush, authored in the 3D view:
      a row of arrows joined by a line, each point a position, a rotation, a
      scale and a time, and `play_waypoints(@door, @door_open)` sends a brush
      along it, arriving at each point at its time.
      `world.WaypointAnimation` is level content under `waypoint_animations:`,
      an ID in the level's one namespace, and drawn only by the editor. Every
      channel eases by the same fraction of a leg, so a brush that turns on its
      way to a point finishes the turn as it arrives, and the rotation is
      blended as written, so 720° is two whole turns.
      **A point's time is when the brush arrives at it**, so the first point's
      time is the leg from wherever the brush was and needs no field of its
      own, and the animation's length may run past the last point to hold the
      brush there before it ends. Position is world space, divided through the chunk's
      transform every tick; rotation and scale are the brush's own channels.
      The travelling itself needed nothing downstream: the step writes the same
      placement `position` does, so drawing, shadows, collision, culling and
      raycasts follow. What did have to be built was **a scale channel on the
      placement** — `Brush.Scale`, about the centre, applied before the turn,
      normals carried by R·S⁻¹, and a `scale` property beside `rotation` —
      since a brush had never been able to grow. It runs on AdvanceBrushes'
      tick, holds the brush's rates while it follows, and posts `:waypoint:` at
      every point and `:waypoints_end:` when the length runs out, through a hooks buffer gated
      on play, so an editor preview announces nothing.
      In the editor, **Add > Waypoint Animation** places a path's first point,
      and **Ctrl+click** with a point selected lays the next one straight after
      it, carrying on its turn and scale and timed at a walk. Ctrl+click is multi-select
      everywhere else, but a waypoint selection excludes every brush and face
      selection, so there is nothing for it to add to. Deleting a point joins
      the one before to the one after and leaves the one before selected, so a
      Ctrl+click fills the gap; an undo keeps the waypoint selected, by ID. Clicking an arrow selects its point for move, rotate and scale (the
      rotation unwrapped, since the numbers are what is interpolated); the
      animation's Explorer row carries the whole path on the move gizmo.
      Selecting an animation docks a **timeline** under the view — the model
      editor's strip with one lane — where each point is a key at its time,
      dragged to retime it (held between its neighbours), beside a Length that
      stretches every time with it, a scrubbable playhead with an orange ghost
      arrow, and Key At Playhead. Each leg's distance is written above its line.
      **Preview** poses any dynamic brush at the playhead through
      `Brush.SeekWaypoints` — the same `PoseAt` the tick reads — from the
      brush's authored position, loops if asked, and puts the brush back on
      Stop, on entering play and on undo. A dynamic brush's own panel offers the
      same Preview, and **New Animation From Here**, which drops the first point
      on the brush's centre.

- [x] An entity type carries particle effects, the way it already carried
      brushes. A ship's thruster plume, a torch's flame, smoke off a wrecked
      crate: each was a level's problem, placed beside the thing it belonged to
      and re-placed by hand in every level that placed the thing — and lost the
      moment the entity moved unless someone remembered to attach it.
      A type holds its own now (`world/entity_emitter.go`), and the arrangement
      is the third instance of one that was already there twice. A definition
      holds templates with no runtime state; a level materialises one live
      object per placement. Models make meshes, brushes make a frame-owned
      chunk, and an emitter attachment makes a level `Emitter` whose `Attach`
      names the entity — after which *nothing else needed writing*. It
      simulates, because `EmitterUpdate` walks the level's list. It draws,
      because `gatherEmitters` does. It culls, it shows up in the Explorer and
      the bounds overlay, and a script says `@shuttle_2.thruster` and gets it,
      because a materialised emitter is an ordinary one.
      What is on the attachment is only what varies between two types sharing a
      document: which `.fpar`, where it hangs, `enabled`/`emitting`, a rate
      scale, a tint and its tags. Everything about how the effect *looks* is
      still the `.fpar`'s, so retuning a plume is one file rather than every
      ship that has one — the argument the type system makes, applied one level
      down.
      **Model and Bone are the reason this is not just an offset.** `Attach`
      already resolved entity-plus-model-plus-bone for a level's emitters, so a
      flame in a hand needs a definition to name the two and nothing more: it
      rides the same skinning palette the mesh and both its shadows read, and
      therefore swings with the hand rather than near it.
      The entity editor grew an **Emitters** list beside Models and Brushes, and
      the effects *run in it* — through `render.DrawSpriteInstances`, the
      engine's own instanced pass, on this window's own VAO, exactly as the
      particle editor previews. That is what makes a cone aimable: it is
      pointed by eye against the hull it comes out of, at the same scale, rather
      than by placing the type in a level and going to look. The move and rotate
      gizmos took no new code — `entityGizmoTargetOf` resolves an emitter to its
      offset and rotation, and deliberately offers no scale handle, since an
      effect's size is its document's.
      Saving skips them (`Emitter.FromEntity`, the twin of `Chunk.FromEntity`),
      so a level stores the placement and never the plume. Selecting one in the
      level editor says which entity owns it and offers the way through to the
      type — the fields are left live, because turning a knob to see what it
      does is how an author decides what to write, but nothing there is kept.
      One thing was fixed on the way past: `Emitter.Tint` had never been applied
      to anything. It is folded into each layer's own tint where the rate scale
      already was, so the knob the level editor has always shown now does what
      it says.

- [x] Nothing starts textured. A stamped brush's faces and a new model wore
      "concrete" and "default" — placeholder materials nobody chose, which the
      author has to notice before they can replace them, and which a blockout
      ends up full of. Both are `(none)` now, and an untextured surface is
      painted with **its own normal**: `n * 0.5 + 0.5` as the albedo, so every
      plane of a solid is a different, predictable colour and a room reads as
      shape while it is still being shaped. It is one uniform (`normalColor`)
      and one branch in each of the two fragment shaders that draw geometry —
      the world's and the model editor's — fed by one rule in Go,
      `Geometry.NormalColorFlag`: a geometry that names no texture at all. A
      name that fails to load is *not* it; a missing image stays the content
      error it always was.
      The colour is taken from the normal the polygon declares, **before** the
      back-face flip, or the inside of a hollowed room would be the photographic
      negative of its outside. Everything else about the surface is unchanged:
      it lights, shadows, fogs, sorts and collides exactly as a textured one
      does — only the albedo comes from somewhere else, which is what keeps the
      change to the albedo line rather than to the pass.
      `(none)` is a real choice and not a missing value, so it heads the texture
      picker and the right-click Texture menu on the surfaces where wearing
      nothing is a look — a brush face, a model — and is left out on a sprite,
      where no texture is a black quad. `texture:` is now omitted from a face
      that has none, and `ModelDoc.TextureName()`'s empty-means-"default" rule
      is gone with it; a legacy `.fmdl` whose faces say "default" still loads
      untextured, because that name was auto-filled rather than chosen.

- [x] Entities are built from brushes, and edited the way the 3D view edits a
      level. A type was models plus a list of chunk *attachments* — file names
      under `chunks/` that the level materialised — which was the right shape
      while the only way to author world geometry was the level editor, and
      which meant "give this ship a hold" was a round trip through the other
      window, an export and a picker. A type holds its own brushes now
      (`world/entity_brush.go`), written into `entities/<name>.yaml` in the
      level's OWN brush format so a new face field can never be forgotten in a
      second schema, and shaped in the entity editor with the level editor's own
      tools — the same functions, not a second implementation.
      The seam is `editor_workspace.go`. Everything about editing brushes lived
      on the one global `Editor`: which level, which chunk, what is selected,
      which unit a click addresses, the stamp, the snap, the gizmo's drag. That
      is a `brushWorkspace` now; `EditorState` embeds one so `Editor.level` and
      `Editor.SelectedFaces` still read the way they always have, and the entity
      editor holds a second over a scratch level of one chunk. The shared files
      say `ws()`, and the entity editor makes its own current for the length of
      its draw and its event handling — two `defer entityUseWorkspace()()` call
      sites, deliberately not a "current window" both windows write, since a
      workspace left installed would point the level editor's tools at a type's
      geometry and read as the main window being haunted. What that buys with no
      new code: the stamps and their ghost, extrude/inset/subdivide on E/I/B,
      object and edit mode on Tab, point/edge/face on 1/2/3, picking, the
      convexity warning, the brush and face panels, and move/rotate/scale —
      which needed no transform maths here either, because a brush's shape IS
      its corner list and the gizmo resolves a selection down to the same
      `[]vertexSel` a face already resolved to.
      **The scratch chunk's brushes are the live ones** and `entityDef.Brushes`
      is the document's copy, committed at the END of a frame. A tool pushes
      undo BEFORE it edits, so the document has to still hold the pre-edit state
      at that moment; committing eagerly would snapshot the edit as its own undo
      point and make Ctrl+Z a no-op.
      **A type's brushes materialise into one frame-owned chunk**, which is the
      machinery vehicle interiors already used — so drawing, shadows, streaming
      and the spatial hash needed no change, a script writing a brush's
      `velocity` moves it in the ENTITY's coordinates, and a body standing
      inside is carried because it never moved, the coordinates did. Two things
      did have to be built. A frame-owned chunk is absent from the world spatial
      hash, so **every entity's brushes are also solid in world space** — the
      body is carried into the entity's coordinates, pushed out there and
      carried back, which keeps the hull that stops you from outside and the one
      you walk on from inside from disagreeing about where it is. And **walking
      in needs no doorway authored**: an entity admits by containment in its
      geometry's own bounds, which is the one region that stays antisymmetric
      with stepping off. Padding it is exactly what must not be done — the pad
      is ground both tests claim, and a body in it is traded back and forth
      every tick; what makes the unpadded version safe is that an outside body
      is kept clear of the hull and so only reaches the bounds through an
      opening someone built. An entity that carries brushes also stops
      presenting its bounding cage to the entity-vs-entity pass, which would
      have pushed a body out of the very volume it was walking into.
      Importing is what is left of the file link, and it copies: a chunk under
      `chunks/`, or a chunk of the open level, read once into the type. Sharing
      one hull between several types is what that gives up.
      `entities/dropship.yaml` is the worked example, rebuilt: five static
      brushes and a dynamic blast door, authored shut because authored geometry
      is always a brush's rest state, slid to port by `scripts/dropship.flang`
      in the ship's own coordinates so it stays put through a two-hundred-metre
      fall. It needs no frame-portal volume lined up against it any more — park
      it and walk in
- [x] A real inset, shared by the model editor and detail mode.
      `model.InsetFaces` builds a new, smaller loop INSIDE a face's outline and
      walls the gap with a ring of quads, leaving the middle selected — so inset
      then extrude is a recess and inset then extrude-the-ring is a raised
      frame. Each corner is offset along its own angle bisector by
      `dist / sin(theta/2)`, which is the mitre a picture frame has: the border
      comes out the same width all the way round a 10x1 strip, where pulling the
      corners toward the centre by a fraction leaves one ten times wider than
      the other. The outline itself does not move, so no neighbouring face is
      dragged along with it. `I` in the model editor and in detail mode; edit
      mode keeps its own corner-pulling inset, which is the one that preserves
      convexity.
- [x] Subdivide splits a face into QUADRANTS, on the hull as well as on a detail
      mesh. It was a fan of triangles to the face's centre, which cannot be
      selected, dragged or textured as the quarters of a panel; it is now the
      model editor's midpoint split, shared outright. The T-junction the fan was
      dodging is repaired rather than avoided — a neighbour that shared a cut
      edge takes the new midpoint into its own outline, so it keeps its shape
      and gains a corner.
- [x] Detail mode: shaping a brush's surface without moving its collision.
      A brush is a convex solid, and convexity is not a preference — collision
      reads its corners as a hull and the compiler clips its faces as
      half-spaces — so every interesting piece of surface relief (a ledge, a
      window reveal, panelling, a lip on a step) was previously either a second
      brush or nothing. A brush now carries an optional second mesh, its
      `Detail`, which is DRAWN INSTEAD of its own faces and which nothing about
      collision, sealing or the partition ever reads. `Ctrl+Shift+click` a brush
      enters detail mode on it: the mesh is seeded as a copy of the hull so
      nothing changes on screen, the hull is drawn as a wire cage so you can see
      where the player will actually stop, clicks reach that one brush and
      nothing else, and the tools are the *model editor's own* — extrude with
      side walls, subdivide, Catmull-Clark, delete — called through
      `model.ModelDoc` rather than reimplemented, with a parallel attribute
      table carrying the texture, UV, blend and alpha a `ModelFace` has no room
      for. Everything below the selection is shared with the hull's editing:
      `vertexSel`, `faceSel` and `edgeSel` grew a `detail` flag saying which of
      the two vertex pools their indices name, and the gizmos, panels, outlines
      and undo work unchanged from there — which is also what lets an
      object-mode drag carry both meshes, so moving a wall takes its ledge with
      it. The mode draws two overlays of its own: every detail face outlined in
      black, depth-tested, the way the model editor outlines a mesh — without
      the seams a subdivided wall is one flat surface and there is no telling
      what a click will hit — and the hull as a dim wire cage, since it is
      otherwise not on screen at all. The seams are pulled a pixel toward the
      camera before they are drawn, because an edge between two coplanar faces
      lies exactly ON the surface and loses the depth test to the triangle it
      bounds. In the compiler a detail face is a Quake detail brush one level down:
      it is drawn and lit but never chooses a splitting plane and never decides
      what is solid, and a detailed brush emits its hull faces as structure
      nobody draws so that the tree, the portals and above all the SEAL come out
      exactly as they would have without it.
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
- [x] Conversion to brushes for geometry
- [x] Bounding box view

## TODO

> NOTE: `[/]` denotes an in-progress task

### Bugs


### Editor

- [ ] Debugger
- [ ] Configurable editor keybindings
- [ ] Audio mixer
- [ ] Gizmo in 3D editor to edit texture UVs


### Engine


- [ ] Physics engine
- [x] Configurable keybindings, other user settings
- [ ] Water reflections
- [ ] Volumetric displacement (e.g. sand, snow)
- [ ] Restitution/friction, and more than one solver iteration per frame
- [ ] Potentially change the max number of frames for animated textures as well as frame spacing/interpolation
- [ ] Volumes are brushes instead of boxes only
- [x] Game saves
- [x] Checkpoints
- [ ] Decal support
- [x] Volumetric clouds and explosions: cloud volumes (`cloud:` on a volume), a
      volumetric cloud deck on the sky that `ease_sky` blends, cloud shadows on
      the world, and `render: volume` particle layers for fireballs and smoke —
      one ray-marched pass at half or full resolution (F4), with
      `explosion.fpar` as the worked example
- [x] move_to (move to a position in a given amount of time)
- [x] Unwrap brush texture
- [x] Waypoint animations
- [x] Cannot select sprites/waypoints/etc. as refs in script dropdown
- [ ] Popout help menu

- [ ] Enable AI to enter vehicles
- [ ] RTS mode?
- [ ] 3rd person mode?
- [x] Audio control
- [ ] Shader editor
- [ ] Break apart model and skeleton with animations on skeletons

### Game

- [ ] No weapon state
- [ ] Holding weapons down
