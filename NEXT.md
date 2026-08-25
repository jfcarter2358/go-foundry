# Features

## Done

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
- [x] Script editor
- [x] Log window (live script output, errors with positions, engine log)
- [x] Scripting (flang: `internal/flang`, `@id`/`#tag`, attributes and properties, messages, events, hot reload)
- [x] Script parameters (`param name type`, bound per object in the editor, read as `@name`)
- [x] Object collision physics (push-apart; no restitution or friction yet)
- [x] Vehicles (seated and walkable-interior, 6DOF, nested frames, frame portals)
- [x] Frames — moving coordinate systems that geometry and entities can live in

## TODO

### Editor

- [ ] Debugger
- [ ] UI builder
- [ ] Re-do top bar
- [ ] Re-organize 3D editor settings panel
- [ ] Configurable editor keybindings
- [ ] All windows should have the exit button on the top
- [ ] New windows should open on top of the existing even in fullscreen mode

### Engine


- [ ] Animated textures
- [ ] Sprites
- [ ] Particles
- [ ] Skeleton sockets
- [ ] Physics engine
- [ ] Texture transparency
- [ ] Texture blending
- [ ] Trees
- [ ] Configurable keybindings
- [ ] Water reflections
- [ ] Music and Sound Effects
- [ ] Volumetric displacement (e.g. sand, snow)
- [ ] Re-do wall extrusion setup to be like the model editor

### Follow-on from entities

Entities answered "how does an authored model get into a world" — a definition
under `entities/`, placed as an instance by a level — and re-fed the GPU skinning
path. What is left is what deliberately was not built with them:

- [x] Entity-against-world and entity-against-entity collision, as per-type
      toggles (`collision: {world, entities, gravity, movable}`) with engine-wide
      master switches on 8 and 9 — no scripting needed
- [x] `enter`/`exit`/`interact` events over the overlap tests, plus `collide`,
      `spawn`, `destroy`, `tick` and the two animation events
- [x] Shape-aware push-apart (GJK/EPA over support functions; axis-aligned box
      pairs keep their own tuned min-axis path)
- [x] Swept collision — motion is split into steps no longer than the body, so
      nothing passes through a wall it was moving too fast to be sampled against
- [ ] Restitution/friction, and more than one solver iteration per frame
- [x] Scripting: `@id` and `#tag` addressing, and reading/writing attributes
- [ ] Animation blending between clips (a switch is a hard cut today)
- [ ] Editing entity *definitions* in the editor. The panel edits an instance;
      changing the type still means editing its YAML and pressing Reload.
