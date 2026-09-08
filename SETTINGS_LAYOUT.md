<!--
SETTINGS_LAYOUT.md — the proposed layout for the editor's panels.

A design target, not a description of what is on screen today. It covers both
editors and every mode in each, and the one rule that ties them together: a
panel only ever shows what the current mode and the current selection can
actually act on.

Open items carried over from the first pass:
  - Chunk scaling in X, Y and Z. A chunk still carries a single uniform scale.
  - Alpha on floors / walls / ceilings. Built — every surface, gap and
    extrusion has an Alpha, and it is laid out below.
  - Still nowhere in any panel, though the level format already carries them:
    level-wide scripts; the tags / attributes / script a chunk, a light and a
    portal each hold; a portal's target frame; a wall's pinned flag.
-->

# Conventions

<!--
Widget vocabulary — the same marks mean the same thing in every section below.

[x]             deletes the row's object
[+]             duplicates the row's object; on a section header, creates one
[Export]        exports the object to a file
[Import]        imports an object from an exported file
[Edit]          opens the object in its own editor
[checkbox]      the object exists / is enabled in the level
[eye]           the object is drawn; it still exists, still collides, still
                scripts. Separate from [checkbox] because the two are separate
                fields on every surface and every model attachment.
[color picker]  swatch opening a picker, beside its R/G/B readouts
[-] [Number Slider] [+] [=]
                a number: step down, drag, step up, [=] to type it exactly
<dropdown ...>  a combo over whatever the named catalog holds
<textbox>       free text
<readout>       derived, not editable

Every label above is plain text, deliberately — the editor's font is the
default ImGui bitmap font, which only has glyphs for Basic Latin and the Latin-1
Supplement (up to U+00FF). An arrow, an em dash, or an icon glyph outside that
range does not fall back to a blank space; it falls back to a literal "?",
which reads as broken rather than as a missing icon. No control in any panel
uses one.

|               marks a toolbar group boundary below — drawn as an actual
                vertical divider (a Separator, not a typed pipe character),
                and the same divider in every toolbar the editor has: this
                one, the sector map's own, and the model editor's.
-->

<!--
Selection — a click selects; ctrl+click adds to the selection. Editing a
property applies it to everything selected of that type, and relative
positions / rotations / scales are respected as they are currently.

Context — every section below carries the condition it appears under, and
nothing else is drawn. A section with nothing to act on is absent, not empty
and not greyed out. The one deliberate exception is the Explorer: it stays
reachable with nothing selected, because it is how the first of anything gets
placed and how any of it gets picked.

Grouping — inside a Properties block, related fields are one collapsible
group, and a group holds everything about its subject: a floor's group carries
its height and texture, the mesh it is shaped with, its UV, its skirt, its
smoothing, its procedural detail and its own tags/attributes/script, and folds
all of it away as a unit. Nothing about the floor sits outside it and nothing
belonging to another face sits inside it. A group is a tree node, not a framed
header — the framed bar is the properties block itself, and groups indent under
it. The group holding what the object *is* (Identity, Transform, Brush, Light)
opens with the block; the rest start shut, and the one a fresh selection points
at opens itself.

Every object type is grouped the same way, so the blocks read as one panel with
different middles: Identity, Transform, then whatever is particular to the
type, then Scripting (Tags / Attributes / Script), then Actions.

Exactly one thing is selected at a time. Picking a chunk, a sector, a biome
sector, an entity, a volume, a light, a face, a vertex or the spawn point
drops whatever was selected before it, so Properties below is always one
object's fields — two blocks up at once, neither of them wrong, was the panel
saying nothing loudly.

Order — every panel is one column, always in this order:

    Context -> Explorer -> Properties -> View -> Editor

so a section keeps its place no matter which mode put it there, and the two
editors read as the same panel with different middles.
-->

# Context <!-- top of every panel, in every mode, always visible -->
Editor: <3D | Sector Map | Surface>
Mode: <Point | Edge | Face> select
Gizmo: <Move | Rotate | Scale>
Selection: <n> <what is selected>
Chunk: <active chunk ID>
Status: <transient message from the last action>

# Toolbar <!-- the strip above the viewport, in every window that has one -->
<!--
One row, always the same shape: a run of buttons, a | boundary, the next run.
Every toolbar in the editor — this one, the sector map's own pop-out, the
model editor's — is built from the same groups in the same left-to-right
order wherever both happen to carry a given group, and divided the same way,
so switching windows does not mean re-learning where things live.

No Exit button anywhere. The window's own close button quits, the same as
every other window the editor opens — a red button beside the mode toggles is
a level lost to a misclick.
-->

## Main window
Windows: [2D Window] [Textures] [Model] [Script] [Particles] [UI] [Log]
<!-- each window button is lit while its window is open -->
| Select: [Point 1] [Edge 2] [Face 3]
<!--
Shared with the sector map's own toolbar below: what a click selects is one
piece of editor-wide state, not a per-window setting, so both offer it.
-->
| Gizmo: [Move G] [Rotate R] [Scale C] <!-- shared with the sector map, same reason -->
| Shading: [Solid/Wireframe Z] Camera: [Orbit/Fly V] <!-- 3D-only: the map has no shading or camera mode of its own -->
| Level: <level file name> [Open] [Save] [Undo] [Redo] [Save As...]
<!--
No chunk picker here. Which chunk is active is a selection like any other, so
it is made where every other selection is made — the Chunks list under Objects
— and the panel's Context line reports it. A dropdown in the toolbar was a
second way to select one thing, out of reach of the list that owns it and
silently disagreeing with it.
-->

## Sector map (its own window)
Select: [Point 1] [Edge 2] [Face 3]
| Gizmo: [Move G] [Rotate R] [Scale C]
<!--
Nothing else. The chunk, the zoom, and the click hints this used to carry as
plain text now live on the panel's Context line, which is always on screen —
repeating them here was two places disagreeing the moment either one changed.
The window's own Close button sits at the row's right edge.
-->

## Model editor (its own window)
<!-- row 1: New / Open / Save / Save As / Import OBJ / Export OBJ / Undo / Redo, and Pop In at the right edge -->
<document name>
| Select: [Vertex 1] [Edge 2] [Face 3] [Bone 4]
| Gizmo: [Move G] [Rotate R] [Scale C] <!-- plus a local-axes toggle beside Scale, when Scale is the active tool -->
| Shading: [Solid/Wireframe Z] Camera: [Orbit/Fly V]

---

# 3D Editor

<!--
The main window. Fly or orbit through the level, click what you want, edit it
on the right.

What the panel shows is decided by two things at once: the select mode, which
says what a click can land on, and the selection itself, which says which
Properties sections appear.

  Point select [1]   clicks land on sector corner handles
                     -> Properties shows Vertex
  Edge select  [2]   clicks land on the prism around a sector edge
                     -> Properties shows Vertex, for the edge's two ends
  Face select  [3]   clicks land on a wall, floor, ceiling, water plane,
                     entity, volume or the spawn point
                     -> Properties shows Sector / Entity / Volume / Water /
                        Spawn, whichever was hit

The Explorer, View and Editor are the same in all three modes. Switching
between Point and Edge keeps the selection; crossing to or from Face clears
it, because the other unit has no handles to explain it.
-->

## Particle editor (its own window)
[New] [Open] [Save] [Save As] | <effect name, * while unsaved> | [Pop In]
[checkbox] Grid  [checkbox] Ground  [checkbox] Shape [B]  | [color picker] BG | <status line>
<!--
Three columns under the toolbar and one bar across the bottom. The left column
is the layer list, the right the parameters for whichever layer is selected, and
the middle is the live preview - which drives the same simulation and the same
draw path the game does, so an effect that looks right here looks right in the
level.
-->

### Layers <!-- left column -->
[S] [M] <layer name> <!-- S solos (a view state, not saved), M mutes (saved with the effect) -->
[+] [-] [^] [v] <!-- order is draw order -->
<!-- right-click a row for Duplicate / Move up / Move down / Delete -->

### Parameters <!-- right column, one group per stage of building an effect -->
#### Effect <!-- the whole document rather than one layer -->
ID <textbox>, Gravity, Seed <!-- the seed fixes every random draw, so the preview is reproducible -->
#### Emission
Rate /s, Duration, [checkbox] Loop, [checkbox] Prewarm, Max
Bursts: a row of t / n per burst, [+ burst]
#### Shape
Kind <dropdown: point | sphere | hemisphere | box | cone | disc>
Radius / Angle / Half extents / Thickness <!-- whichever the kind actually reads -->
Direction <dropdown: shape | random | fixed>, Vector, Spread (deg)
#### Initial <!-- min/max pairs on one row each, which is how they are read -->
Life, Speed, Size, Rotation (deg), Rot speed (deg/s)
#### Motion
Gravity, Drag, Radial, Tangential, Turbulence, Turb freq
[checkbox] Local space <!-- carry live particles when the emitter moves -->
#### Appearance
Texture, Sheet cols / rows, Frames <dropdown: over-life | random | static>
Blend, Billboard, Tint, [checkbox] Lit, [checkbox] Cast shadows
#### Collision <!-- collapsed: most effects are smoke and fire, which do not bounce -->
Mode <dropdown: none | stop | bounce | kill>, Radius, Bounce, Friction

### Transport and ramps <!-- the bar across the bottom -->
[Play/Pause] [Restart] [Step] <speed dropdown> | t <clock> / <duration>   live <n> / <cap>
[Size] [Alpha] [Color] <!-- one strip at a time; three stacked would leave the preview a letterbox -->
<the active ramp, drawn as a canvas>
<!--
Click adds a stop, drag moves it, right-click deletes, and on the colour strip a
double-click opens a picker. Stops are tracked by their position on the curve
rather than by index, because dragging one past its neighbour re-sorts the list.
-->

## UI editor (its own window)
[New] [Open] [Save] [Save As] | [Undo] [Redo] | <screen name, * while unsaved> | [Pop In]
[checkbox] Grid  [checkbox] Outlines  [checkbox] Safe area | <resolution dropdown> | [Data: Sample|Literal|Live] | <status line>
<!--
Three columns under the toolbar: the hierarchy on the left, the canvas in the
middle, the properties on the right. The canvas is a live preview drawn by the
same screen-space pass the game uses, laid out by the same solver - so a screen
that looks right here looks right in the level, for the reason the particle
editor's preview drives the game's simulation.

The resolution dropdown is the size the document is laid out at, which is not
this window's size: the numbers on the panel have to be the numbers the game
will use.

Data decides what bound widgets read. Sample invents plausible values that
*move*, because the only way to see whether a fill direction or a nine-slice
works is to watch one change. Literal turns bindings off, so every widget shows
what the document says. Live reads the level the script runtime has.
-->

### Hierarchy <!-- left column -->
[checkbox] <node id>  <kind>   <!-- the checkbox is Visible -->
[+ Add] [^] [v] [x]            <!-- order is draw order; there is no z field -->
<!--
Right-click a row for Duplicate / Move up / Move down / Wrap in panel / Delete,
or Add child. Drag a row onto another to reparent it. Nodes spliced in from an
include are shown muted and cannot be selected: editing one would write the
change into a file that is not the one being edited.
-->

### Widget <!-- right column, one group per subject, matching the level panel's ordering -->
#### Identity
ID <textbox>, Kind <readout>
[checkbox] Visible, [checkbox] Enabled, [checkbox] Blocks input
[-] Alpha [+] [=]   <!-- multiplied down the whole subtree -->
#### Rect
<the anchor preset grid: nine corner/edge/centre pins, then stretch-x, stretch-y, stretch>
Anchor min / max, then per axis either Pos+Size or the two insets
<!-- whichever spelling fits how that axis is anchored; showing both at once is
     where RectTransform's reputation comes from -->
Pivot X / Y, Rotation <!-- leaf kinds only -->, Scale X / Y, [checkbox] Clip children
#### Style
[color picker] Color, Texture <dropdown>, UV X / Y / W / H
Slice left / top / right / bottom <!-- in texels of the source image -->
Fit <dropdown: stretch | fit | fill | tile>
#### States <!-- collapsed; interactive kinds only -->
[checkbox] Hover / [checkbox] Press / [checkbox] Disabled, each with a colour and a texture
<!-- unset states are derived by tinting, so a button nobody styled still responds -->
#### Text <!-- text, button, input, toggle -->
Text <textbox>, Font <dropdown>, Size, [color picker] Color
Align <dropdown>, Vertical <dropdown>, [checkbox] Wrap
Bind <textbox>, Format <textbox>, <readout of what it currently shows>
#### Value <!-- bar, slider, toggle -->
Value / Min / Max / Step, Fills <dropdown: lr | rl | bt | tb>
[color picker] Fill color, Fill texture <dropdown>
Bind <textbox>, Bind max <textbox>
#### Navigation <!-- collapsed; interactive kinds only -->
Up / Down / Left / Right <dropdown over the screen's node ids>, Action <textbox>
#### Include <!-- include kind only -->
Source <dropdown over .fui files> [Edit], then a row per {{param}}
#### Actions
[Duplicate] [Wrap in panel] [Delete]

### Screen <!-- the same column, when the root is selected: the root is the canvas -->
#### Screen
ID <textbox>
#### Canvas
Scale <dropdown: screen | pixel>, Reference width / height, Match width/height
[checkbox] Snap to pixels
#### Input
[checkbox] Modal, [checkbox] Pauses the game, [checkbox] Shows the cursor
Initial focus <dropdown>

### Theme <!-- a tab beside Properties, editing the one theme every screen reads -->
Font <dropdown>, Default size, [color picker] Text, [color picker] Muted text
[color picker] Panel, Panel texture <dropdown>, Slice left / top / right / bottom
[color picker] Normal / Hover / Press / Disabled
[color picker] Bar background / Bar fill / Accent
[Save theme] [Revert] [Defaults]
<!--
A tab rather than a window: the theme is one small file every screen reads, and
a second tool would mean a second place to remember to save. Edits are visible in
the preview immediately, which is the only way to see what a colour does to a
real screen; Revert puts the file's version back.
-->

## Explorer <!-- one tree over everything the level contains, in a scroll box of its own so Properties stays on screen -->
[Add...] <!-- the one menu that creates anything; see below -->
Click in the view to place <what> [Cancel] <!-- only while a placement is armed -->
<!--
A row is the selection and nothing else: click it and Properties below draws
that object's fields, and only that object's. What acts on a row as a whole
lives on its right-click menu, so it costs no line of the panel; what creates
something lives on the one Add menu.

The tree used to be six always-open sections, each with its own catalog, its
own place button and a row of buttons beside every row — all of it on screen
at once, in both editors, whether or not any of it was what was being worked
on.
-->
- Player Spawn <!-- one per level; "(none)" when the level has none. Right-click: Move To Camera / Place By Clicking / Delete, or Add when there is none -->
- Chunks (<n>) <!-- right-click a chunk: Duplicate / Export... / Delete -->
  - Chunk 1 (active) <!-- clicking a row makes that chunk both active and selected; this list is the only place that happens, and the active one starts expanded -->
    - Sectors (<n>) <!-- listed in both editors now, so a sector whose surfaces are all switched off is still reachable -->
      - Sector 1
    - Biome (<n>) <!-- the chunk's painted polygons, when it has any. Right-click: Delete -->
      - bs1 (Grass)
  - Chunk 2
- Entities (<n>) <!-- right-click: Duplicate / Move To Camera / Delete -->
  - Entity 1  (<type>)
- Volumes (<n>) <!-- right-click: Enabled / Duplicate / Delete. A disarmed one reads "(disabled)" -->
  - Volume 1
- Scenery (<n>) <!-- placed static models. Right-click: Solid / Use As Brush / Duplicate / Delete. A row reads "(no mesh)" when the model would not load, "(not solid)" when collision is off -->
  - rock_01
  <!-- capped at 200 rows plus the selected one, then "(N more - click one in the 3D view to select it)": a brush makes hundreds, and a tree that draws all of them costs more per frame than the rest of the panel put together -->
- Lights (<n>) <!-- flat: the labels already say which kind each is. Right-click: Enabled / Duplicate / Delete -->
  - Global Light <!-- always exists, can be switched off but not removed -->
  - Light 1  (directional)
  - Light 2  (point)
- Scripts (<n>) <!-- the level's own .flang handlers. Clicking one opens it in the script editor; right-click: Remove From Level -->
  - scripts/level.flang

### Add... <!-- the menu behind the Explorer's one create button -->
Chunk... <!-- names it, then places it at the camera -->
Import Chunk... <!-- from a file in chunks/ -->
Entity > ID <textbox>, then a row per definition under entities/ plus "(blank entity)"; picking one arms the next viewport click
Volume > ID <textbox>, then Place By Clicking
Sprite > ID <textbox>, Texture <dropdown>, then Place By Clicking <!-- lands standing on the surface clicked -->
Scenery > Model <dropdown of .fmdl/.obj under models/>, then Start/Stop Brushing <!-- no "Place By Clicking" row: picking a model IS arming the brush, and the brush is not a one-shot -->
Emitter > ID <textbox>, Effect <dropdown of .fpar under particles/>, then Place By Clicking
Sound > ID <textbox>, Clip <dropdown of .wav/.ogg/.mp3 under sounds/>, then Place By Clicking
Directional Light <!-- greyed past the shader's limit -->
Point Light <!-- at the camera; greyed past the shader's limit -->
Biome Sector <!-- sector map only: draw its corners, then Enter -->
Player Spawn <!-- only when the level has none -->
Level Script > Rescan Folder, then a row per .flang the level is not already running

## Properties <!-- editing any property applies it to everything selected of that type; relative positions/rotations/scales are respected when several objects are selected -->

### Transform <!-- shown whenever the selection has a position; the gizmo drags the same fields -->
X [-] [Number Slider] [+] [=]
Y [-] [Number Slider] [+] [=]
Z [-] [Number Slider] [+] [=]
Rotation Yaw [-] [Number Slider] [+] [=]
Rotation Pitch [-] [Number Slider] [+] [=]
Rotation Roll [-] [Number Slider] [+] [=]
Scale X [-] [Number Slider] [+] [=]
Scale Y [-] [Number Slider] [+] [=]
Scale Z [-] [Number Slider] [+] [=]
<!--
Rotation is shown in degrees and stored in radians, everywhere in both editors.
Rows the selection cannot use are absent: a volume has no scale (its size is
its half-extents), a point light has neither rotation nor scale, and a
directional light has neither position nor scale.
-->

### UI screens <!-- level-scoped: not a property of anything placed -->
<one row per screen: <name> [Edit] [x]>
[Add screen] [New screen]
<!--
The .fui screens this level puts up when play starts. A menu a script opens
later is not listed here - show_screen names it directly.
-->

### Spawn Point <!-- only when the spawn marker is selected -->
Facing: <yaw>°, <pitch>° <readout> <!-- what gameplay actually reads, after the pitch clamp -->
[Face Camera] [Move To Camera] <!-- aim / place it where the fly camera is looking -->
[Delete Spawn Point]

### Player <!-- level-scoped: the player is engine-owned, so nothing in the viewport opens this -->
#### Movement
[checkbox] This level sets a step height
Step height [-] [Number Slider] [+] [=] <!-- only when the checkbox is ticked -->
<!--
How tall a riser the player walks up rather than being stopped by it. Unticked
writes no `player:` block at all and follows the engine's default, the player's
own height - which is the same gesture the Sky and Fog groups open with, and for
the same reason: a level that never cared must not freeze today's number into
its file. Unticking is a return to the default, not a way to say "no stepping":
zero clearance would be refused by the seam between two floors that differ by a
rounding error.
-->

### Chunk <!-- only when a chunk is selected -->
ID: <ID1, ID2, ...>
Sectors: <sector count> <readout>
Origin X/Y/Z [-] [Number Slider] [+] [=]
Rotation Yaw [-] [Number Slider] [+] [=] <!-- Y-axis only; a chunk does not pitch or roll -->
Scale X [-] [Number Slider] [+] [=] <!-- see the open item at the top: one uniform scale today -->
Scale Y [-] [Number Slider] [+] [=]
Scale Z [-] [Number Slider] [+] [=]
#### Motion <!-- carried forward every frame during play, not in the editor; nothing standing on the chunk rides it -->
Velocity X/Y/Z (u/s) [-] [Number Slider] [+] [=]
[Stop]
#### Carried By
Carried by <dropdown of vehicle entities, plus "(nothing — world chunk)"> <!-- the chunk is this vehicle's interior; sets both sides of the binding -->
#### Scripting <!-- the shared Tags / Attributes / Script group; see Scripting, below -->
#### Actions
[Switch to Sector Editor] <!-- opens the map centred on this chunk -->

### Sector <!-- only when a sector, wall, portal, ceiling or floor is selected -->
ID: <ID1, ID2, ...>
<!-- with more than one face selected, a note says which face the panel is editing and that the right-click menu edits them all -->

<!--
Both editors draw this block from one place, so a wall answers the same
questions whichever window is asking; the map used to carry its own copy, and
that copy had lost the Pinned flag, the portal's target frame and scripting
altogether.
-->

#### Floor <!-- a group; the checkbox inside it enables/disables the sector floor(s) -->
[eye] Visible <!-- hides the structural texture without disabling the floor — for one fully covered by a biome ground patch -->
Height [-] [Number Slider] [+] [=]
Texture <dropdown of available textures>
Terrain [checkbox]
Double Sided [checkbox]
Alpha [-] [Number Slider] [+] [=]
[Edit Floor Surface] <!-- enters the Surface Editor on this face -->
##### Texture UV
##### Floor Skirt <!-- floors only: what closes the band under a stacked floor -->
##### Detail
##### Scripting <!-- Tags / Attributes / Script -->
<!-- the sub-groups above are identical on every face; laid out once under Surface Sub-Panels, below -->

#### Ceiling <!-- a group; the checkbox inside it enables/disables the sector ceiling(s) -->
[eye] Visible
Height [-] [Number Slider] [+] [=]
Texture <dropdown of available textures>
Terrain [checkbox]
Double Sided [checkbox]
Alpha [-] [Number Slider] [+] [=]
[Edit Ceiling Surface]
##### Texture UV / Detail / Scripting

#### Wall <!-- a group; only when a wall is the selected edge -->
Wall <edge ID> <readout> <!-- the ID, not the index: a script reaches it as @sector.edge.<id>, and the index shifts under nearly every edit -->
Kind <dropdown: wall, portal> <!-- converting carries the edge's ID across -->
[eye] Visible
Texture <dropdown of available textures>
Collision [checkbox]
Pinned [checkbox] <!-- portal inference will not convert this wall -->
Terrain [checkbox]
Double Sided [checkbox]
Alpha [-] [Number Slider] [+] [=]
[Edit Wall Surface]
##### Texture UV / Detail / Scripting

#### Portal <!-- only when a portal is selected -->
Portal <edge ID> <readout>
Kind <dropdown: wall, portal>
To Sector <dropdown of sectors, or free text>
To Frame <dropdown of frames, plus "(this chunk)"> <!-- a frame portal: a ramp opening onto a deck that moves relative to it -->
Draw Gap [checkbox] <!-- fills the height difference between the two sectors -->
Gap Texture <dropdown of available textures> <!-- only when Draw Gap is on -->
[eye] Visible
Double Sided [checkbox]
Alpha [-] [Number Slider] [+] [=]
##### Scripting

#### Scripting <!-- the sector's own, after the faces that each carry theirs -->

### Vertex <!-- only in Point or Edge select, with at least one handle picked -->
Vertex #<n> (<Floor | Ceiling>) — <sector ID>
<!-- with several picked the title is "<n> Vertices Selected", and editing a field collapses all of them onto it -->
X [-] [Number Slider] [+] [=]
Z [-] [Number Slider] [+] [=]
Floor Offset [-] [Number Slider] [+] [=] <!-- per-vertex Y delta from the floor's height -->
Ceiling Offset [-] [Number Slider] [+] [=]
Height Offset [-] [Number Slider] [+] [=] <!-- replaces the two above on a multi-selection: each handle's own surface takes it -->
[Snap To Grid] [Select This Sector's Vertices] [Deselect]

### Entity <!-- only when an entity is selected -->
ID <textbox> <!-- committed on Enter; a name already in use is refused rather than silently suffixed -->
Type: <type name, or (none)> <readout> <!-- stamped from entities/<type>.yaml; the level stores only what differs -->
[eye] Visible
In: chunk <id> / sector <id> <readout>
#### Bounding
Method <dropdown of bounding methods: AABB, cylinder, sphere>
Half X [-] [Number Slider] [+] [=]
Half Y [-] [Number Slider] [+] [=]
Half Z [-] [Number Slider] [+] [=]
Centre Offset X/Y/Z [-] [Number Slider] [+] [=] <!-- where the volume sits relative to the entity's origin -->
#### Motion <!-- carried forward every frame during play, not in the editor -->
Velocity X/Y/Z (u/s) [-] [Number Slider] [+] [=]
Angular Velocity X/Y/Z (deg/s) [-] [Number Slider] [+] [=]
[Stop]
#### Collision
Collide with world [checkbox]
Collide with entities [checkbox] <!-- both sides have to opt in -->
Gravity [checkbox]
Movable [checkbox] <!-- off makes it immovable scenery: it pushes and is never pushed -->
Resting on ground <readout>
<!--
These belong on the type. The panel says so, and says when the engine-wide
switches under Editor -> Engine are off, since that is the first thing to
check when something that should be solid is not.
-->
#### Vehicle <!-- [Make this a vehicle] when the entity has no vehicle block -->
Mode <dropdown: ground, air, water, space>
Interior <list of bound chunks, each with a [Remove]> <dropdown to add another unowned chunk> <!-- the chunk(s) that are this vehicle's decks — a floor and a roof can both bind here; sets each chunk's owner too -->
##### Seats [+]
<!-- [x] removes the seat; [checkbox] makes it the driver's, taking it off whichever seat had it -->
- [x] [checkbox] driver
  Name <textbox>
  Position X/Y/Z [-] [Number Slider] [+] [=] <!-- vehicle-local, the occupant's feet -->
  Exit X/Y/Z [-] [Number Slider] [+] [=] <!-- vehicle-local, where they are put down on leaving -->
  Yaw [-] [Number Slider] [+] [=] <!-- rest facing -->
  Eye [-] [Number Slider] [+] [=] <!-- view height above the seat -->
  Yaw Limit [-] [Number Slider] [+] [=] <!-- 0 = unrestricted -->
  Pitch Limit [-] [Number Slider] [+] [=] <!-- 0 = unrestricted -->
##### Drive
Max Speed / Accel / Brake / Reverse [-] [Number Slider] [+] [=]
Turn Rate / Pitch Rate / Roll Rate [-] [Number Slider] [+] [=]
Drag / Angular Drag [-] [Number Slider] [+] [=]
Lift [-] [Number Slider] [+] [=] <!-- air only -->
Buoyancy [-] [Number Slider] [+] [=] <!-- water only -->
Ground Stick / Max Climb [-] [Number Slider] [+] [=] <!-- ground and water only -->
[Reset to defaults] <!-- the mode's defaults -->
#### Models [+] <!-- [+] adds an attachment with no mesh yet -->
<!-- [x] removes the attachment; [eye] hides it without removing it -->
- [x] [eye] Model 1
  Name <textbox> <!-- how a script asks for an animation on this model rather than another -->
  Model <dropdown of available models> <!-- .fmdl and .obj under models/, plus "(none)" -->
  Texture <dropdown of available textures> <!-- "(none)" falls back to what the mesh was authored with -->
  Offset X/Y/Z [-] [Number Slider] [+] [=]
  Rotation X/Y/Z (deg) [-] [Number Slider] [+] [=]
  Scale X/Y/Z [-] [Number Slider] [+] [=]
  Alpha [-] [Number Slider] [+] [=]
  ##### Animations [+] <!-- only when the model has an armature and clips -->
  <!-- [x] removes the animation -->
  - [x] Animation 1
    Clip <dropdown of the model's clips>
    Speed [-] [Number Slider] [+] [=]
    Loop <dropdown: follow clip, loop, once>
    Autoplay [checkbox]
    [Play] <!-- reads "(playing)" while it is -->
  [Stop]
[Reload Models] <!-- re-read every attachment from disk, after editing its .fmdl -->
#### Tags <!-- merged from the definition, so an entity's are edited as their own set -->
#### Attributes
#### Script
#### Actions
[Duplicate] [Move To Camera] [Reset To Definition] [Delete Entity]
<!-- Reset To Definition only when the entity has a type; it keeps only where it is -->

### Volume <!-- only when a volume is selected -->
ID <textbox> <!-- committed on Enter; an ID used by anything else in the level is refused -->
[checkbox] Enabled <!-- a disabled volume contains nothing, so it fires no events; scripts flip this to arm a trigger -->
Shape <dropdown of volume shapes>
Half-extents X/Y/Z [-] [Number Slider] [+] [=] <!-- half the region's size on each axis: the box the shape is inscribed in -->
#### Tags / Attributes / Script
[Duplicate] [Delete]

### Sprite <!-- only when a sprite is selected -->
ID <textbox> <!-- committed on Enter; an ID used by anything else in the level is refused -->
[checkbox] Visible <!-- a hidden sprite stays selectable here and in the Explorer -->
#### Transform
Position X/Y/Z [-] [Number Slider] [+] [=] <!-- local to the carrier when attached -->
Width [-] [Number Slider] [+] [=]
Height [-] [Number Slider] [+] [=]
Roll (deg) [-] [Number Slider] [+] [=] <!-- one field while billboarded; the full X/Y/Z triple only in Fixed, where the other two axes are actually read -->
#### Appearance
Texture <dropdown>
Billboard <dropdown: full | y-axis | fixed> <!-- each option carries a line on what it is for; picking wrong is the usual way a sprite looks broken -->
Blend <dropdown: cutout | alpha | additive> <!-- decides which pass it draws in, not merely how it sorts -->
[R:<num>] [G:<num>] [B:<num>] [color picker] Tint
Alpha [-] [Number Slider] [+] [=]
[checkbox] Lit <!-- takes the scene's lights and shadows; off for anything emissive -->
[checkbox] Casts shadow <!-- faces the light in the depth passes rather than the camera -->
#### Sheet <!-- collapsed: most sprites use a whole texture -->
Columns / Rows / Cell [-] [Number Slider] [+] [=]
#### Attach <!-- collapsed; shared with emitters -->
Frame / Entity / Model / Bone <dropdown each> <!-- cascading: each row is disabled until the one above is answered -->
#### Tags / Attributes / Script
[Duplicate] [Delete]

### Scenery <!-- only when a placed prop is selected -->
ID <textbox> <!-- committed on Enter; an ID used by anything else in the level is refused -->
#### Identity
Model <dropdown of .fmdl/.obj under models/>
[Reload] [Use As Brush] <!-- Use As Brush arms the brush with this prop's model, facing and size -->
#### Transform
Position X/Y/Z, Rotation X/Y/Z (deg), Scale X/Y/Z [-] [Number Slider] [+] [=] <!-- a real per-axis scale on the model, unlike a volume's or a sprite's extents -->
#### Collision
[checkbox] Solid <!-- stops the player and any entity that collides with the world -->
<!-- a line giving the box read off the model, or saying the mesh is flat/unloaded and collides with nothing. Nothing here is editable: a prop's bounds are its model's -->
[Duplicate] [Delete]
<!-- No tags, attributes or script: a prop that needs behaviour is an entity, not scenery -->

### Scenery Brush <!-- whenever the brush has a model; NOT selection-driven, because it describes the next click rather than anything selected -->
Model <dropdown of .fmdl/.obj under models/>
[Start] / [Stop] <!-- armed, the brush survives the click that uses it: every click places another copy until Escape -->
Yaw (deg) [-] [Number Slider] [+] [=] <!-- or [ and ] in the 3D view, 15-degree steps, 5 with Shift -->
Scale [-] [Number Slider] [+] [=] <!-- uniform; or - and = in the 3D view -->
[checkbox] Random yaw per copy <!-- each stamp gets its own facing; the ghost keeps the yaw above -->
[checkbox] Solid <!-- the collision flag each copy takes -->
<!-- a line giving how many props are in the level and how many the current view drew -->

### Emitter <!-- only when an emitter is selected -->
ID <textbox>
[checkbox] Enabled <!-- off stops the simulation dead; live particles vanish -->
[checkbox] Emitting <!-- off stops new particles but lets the ones in flight finish; this is what stop_emit does -->
#### Effect
Effect <dropdown of .fpar> [Edit...] <!-- Edit opens the particle editor on this document -->
<!-- a line naming the effect's layers and how many particles are live, or why it loaded nothing -->
[Reload] [Restart]
Rate scale [-] [Number Slider] [+] [=] <!-- multiplies every layer's rate -->
[R:<num>] [G:<num>] [B:<num>] [color picker] Tint <!-- over every layer's own -->
#### Transform
Position X/Y/Z, Rotation X/Y/Z (deg) [-] [Number Slider] [+] [=] <!-- rotation aims the emission shape -->
[checkbox] Show emission shape <!-- outlines the region particles are born in -->
#### Attach <!-- the same block sprites get -->
#### Tags / Attributes / Script
[Duplicate] [Delete]

### Sound <!-- only when a sound is selected -->
ID <textbox>
[checkbox] Enabled <!-- off gives up the voice entirely -->
[checkbox] Playing <!-- off stops the sound but keeps the placement; this is what the stop verb does -->
[checkbox] Loop <!-- on for ambience; off plays once -->
#### Clip
Clip <dropdown of .wav/.ogg/.mp3 under sounds/>
<!-- a line naming the clip's length, rate and channel count, or why it loaded nothing -->
Gain [-] [Number Slider] [+] [=] <!-- named gain, not volume: Volume is a level object type -->
Pitch [-] [Number Slider] [+] [=] <!-- clamped to what the mixer resamples cleanly -->
Bus <dropdown: effects, music> <!-- which of the player's sliders it answers to -->
#### Transform
Position X/Y/Z, Rotation X/Y/Z (deg) [-] [Number Slider] [+] [=] <!-- rotation aims the cone -->
#### Attenuation <!-- how far it carries; the substance of the panel -->
Ref distance [-] [Number Slider] [+] [=] <!-- full gain within this radius -->
Max distance [-] [Number Slider] [+] [=] <!-- silent beyond it, and skipped by the mixer -->
Rolloff [-] [Number Slider] [+] [=]
Falloff <dropdown: inverse, linear, exponential>
[checkbox] Show ranges <!-- draws the two distances as rings; the only way to place a sound by eye -->
#### Cone <!-- collapsed by default; most sounds are omnidirectional -->
Inner (deg) / Outer (deg) [-] [Number Slider] [+] [=] <!-- total widths, not half-angles -->
Outside gain [-] [Number Slider] [+] [=]
#### Attach <!-- the same block sprites and emitters get -->
#### Tags / Attributes / Script
[Audition] [Duplicate] [Delete] <!-- Audition plays it at the camera, ignoring distance -->

### Water <!-- part of the biome brush block below, shown for the Water kind; reached by clicking the plane or the sector -->
<!-- shape and extent come from the painted polygon; they are edited in the sector map's biome layer -->
Depth [-] [Number Slider] [+] [=]
Wave Amplitude [-] [Number Slider] [+] [=]
Wave Length [-] [Number Slider] [+] [=]
Wave Speed [-] [Number Slider] [+] [=]
Reflectivity [-] [Number Slider] [+] [=]
[R:<num>] [G:<num>] [B:<num>] [color picker] Reflect Color

### Lights
#### Global <!-- only when the global light is selected -->
[R:<num>] [G:<num>] [B:<num>] [color picker] Color
Brightness [-] [Number slider] [+] [=]
#### Directional <!-- only when a directional light is selected; it has no position and no scale -->
Yaw (deg) [-] [Number slider] [+] [=] <!-- aimed by angle, not by vector: a vector edited one component at a time passes through zero -->
Pitch (deg) [-] [Number slider] [+] [=]
Direction: <direction x>, <direction y>, <direction z> <readout>
[R:<num>] [G:<num>] [B:<num>] [color picker] Color
Brightness [-] [Number slider] [+] [=]
Cast shadows [checkbox]
Shadow strength [-] [Number slider] [+] [=] <!-- only when Cast shadows is on -->
<!-- a compass/elevation card in the corner of the viewport shows where the selected one points -->
#### Point <!-- only when a point light is selected; position comes from the Transform block, rotation and scale do nothing -->
[R:<num>] [G:<num>] [B:<num>] [color picker] Color
Brightness [-] [Number slider] [+] [=]
Radius [-] [Number Slider] [+] [=]
Cast shadows [checkbox]
Shadow strength [-] [Number slider] [+] [=]
#### Tags / Attributes / Script <!-- a light is addressable, so it carries the same block everything else does -->

## View
Shading <dropdown: Solid, Wireframe> [Z]
<!-- solid hides the far side of the world and only lets you click what you can see; wireframe draws it as lines and lets you click any of it -->
Camera <dropdown: Orbit, Fly> [V]
Show All Point/Edge Boxes [checkbox] <!-- wireframe already draws them all; this is the same for a solid view -->
[Frame Selection] [F]
[Fwd] [Back] [Left] [Right] [Up] [Down] <!-- hold to nudge the camera, for working without the keyboard -->

## Editor
Snap to grid [Checkbox]
Snap Delta [Number Slider]

---

# 2D Editor — Sector Map

<!--
Its own window, opened from the toolbar. The top-down view of one chunk: the
shared point pool, the sectors built out of it, and the biome polygons painted
over it.

The same select modes and the same gizmo tools as the 3D view, on the same
state, so a selection made in one is legible in the other. What differs is
what a click can reach: the map picks chunk points, wall and portal lines,
sectors, entities, volumes and the spawn marker, but nothing that only exists
in three dimensions.

The map has one mode of its own — the biome layer — which replaces what a
click does entirely. It gets its own section, below.
-->

## Context <!-- as above, plus what only the map has -->
Zoom: <n> px/unit <readout>
Editing: <Floor | Ceiling> corners <!-- which of a corner's two handles the map picks and drags -->
Scroll: zoom | MMB drag: pan | Shift+click: add point | Ctrl+click: multi-select | RMB: menu

## Map
[Floor Verts] [Ceiling Verts] <!-- which height each corner handle carries; lit for the one in force -->
[Join Points (n edges)] [J] <!-- draws an edge between each picked point and the next, in the order picked -->
[Join & Close Loop (n edges)] [Shift+J] <!-- the same, plus the edge back to the first, which closes it into a sector -->
[Discard n Unjoined Edge(s)] <!-- only when there are any; the points themselves stay -->
[Add Point Here] [Delete n Points] [Clear Selection]
Show All Point Boxes [checkbox]
Edit Biome Layer [checkbox] <!-- the switch into the biome mode below; it is what a click reaches, not a property of anything -->
[Refresh Heights] <!-- only with the layer on and something painted -->

## Explorer
<!--
Identical to the 3D editor's Explorer — the same spawn point, chunks, sectors,
biome sectors, entities, volumes, lights and scripts, because both views edit
the same level. The chunk list is what switches which chunk the map is
drawing, and each chunk's Sectors node is what the map's own sector list used
to be.
-->

## Properties

### Sector <!-- only when a sector is selected -->
<!--
The same Sector block the 3D editor draws, with the same Floor / Ceiling /
Wall / Portal sub-blocks and the same six surface sub-panels. Two differences,
both because the map is the view that owns the outline:

  - Floor and Ceiling are both always available here, not only the face that
    happened to be clicked.
  - the Vertex block below edits a corner of the outline, in X and Z, which
    is what the map is for.
-->

### Vertex <!-- only when a corner is selected -->
Vertex #<n>
X [-] [Number Slider] [+] [=]
Z [-] [Number Slider] [+] [=]
Floor Offset [-] [Number Slider] [+] [=]
Ceil Offset [-] [Number Slider] [+] [=]

### Edge <!-- only when a wall or portal line is selected; the same block the 3D editor draws -->

### Entity / Volume / Spawn Point <!-- only when one is selected; the same blocks the 3D editor draws -->

## View
Show All Point Boxes [checkbox]
Zoom [-] [Number Slider] [+] [=]
[Frame Chunk]
[Switch to 3D Editor]

## Editor
Snap to grid [Checkbox]
Snap Delta [Number Slider]

---

# 2D Editor — Biome Layer

<!--
A mode of the sector map, not a window of its own. While it is on, the map
draws, selects and edits biome polygons instead of structural geometry, and
the structural sections above are replaced by these.

A sector owns its brush outright: editing one only ever affects that one, and
sectors may be drawn overlapping to layer looks together.

Sub-modes:
  idle      click selects a polygon, or grabs a vertex of the selected one;
            right-click deletes the polygon under the cursor
  drawing   click adds a point, click near the first (or Enter) closes it,
            right-click undoes the last point, Esc cancels
-->

<!--
The layer switch is under Map, the sectors are under the chunk that holds them
in the Explorer, and a new one is drawn from the Explorer's Add menu — with
the corner count and Finish/Cancel on the Explorer's placement banner while
it is being drawn. What is left here is the selected sector's brush, which is
a properties block like any other and is drawn in both editors: a water plane
clicked in the 3D view selects the biome sector behind it.
-->

## Properties <!-- only when a biome sector is selected -->
Biome: <ID> <!-- header -->
<kind>, <n> points <readout>
Brush <dropdown: Grass, Rock, Wildflower, Cattail, Sand, Water, Moss>
<!-- switching resets to that kind's defaults rather than carrying stale fields over -->
Density [-] [Number Slider] [+] [=] <!-- only for kinds that scatter -->
Draw Dist [-] [Number Slider] [+] [=] <!-- only for kinds that scatter -->
[R:<num>] [G:<num>] [B:<num>] [color picker] Color <!-- every kind but Sand and Water -->

### Grass <!-- only for the Grass brush -->
Grass Height [-] [Number Slider] [+] [=]
Blade Width [-] [Number Slider] [+] [=]
Clumping [-] [Number Slider] [+] [=] <!-- 0 scatters evenly; above that, blades gather into tufts -->

### Rock <!-- only for the Rock brush -->
Rock Sides [-] [Number Slider] [+] [=]
Rock Size [-] [Number Slider] [+] [=]

### Wildflower <!-- only for the Wildflower brush -->
Stem Height [-] [Number Slider] [+] [=]
Stem Width [-] [Number Slider] [+] [=]
Bloom Size [-] [Number Slider] [+] [=]
Leaves [checkbox]
Leaf Size [-] [Number Slider] [+] [=] <!-- only when Leaves is on -->
Spike Bloom [checkbox] <!-- elongated and cattail-like, against a squat petal disc -->
Random Bloom Colors [checkbox] <!-- off uses Color above directly; on picks a hue and tints it by Color -->
Bloom Center [checkbox]
[R:<num>] [G:<num>] [B:<num>] [color picker] Center Color <!-- only when Bloom Center is on -->

### Cattail <!-- only for the Cattail brush -->
Reed Height [-] [Number Slider] [+] [=]
Reed Width [-] [Number Slider] [+] [=]
Seed Head Size [-] [Number Slider] [+] [=]

### Sand <!-- only for the Sand brush -->
Dune Height [-] [Number Slider] [+] [=] <!-- volumetric displacement, no scatter — paint Rock on top for outcrops -->

### Moss <!-- only for the Moss brush -->
Scatter Clumps [checkbox] <!-- off is a ground patch only; this gates whether Density and Draw Dist mean anything -->
Clump Size [-] [Number Slider] [+] [=] <!-- only when Scatter Clumps is on -->

### Water <!-- only for the Water brush; auto-fills the painted area with a water plane -->
<!-- the same block the 3D editor's Water section draws, reached there by clicking the plane -->
Depth [-] [Number Slider] [+] [=]
Wave Amplitude / Wave Length / Wave Speed / Reflectivity [-] [Number Slider] [+] [=]
[R:<num>] [G:<num>] [B:<num>] [color picker] Reflect Color

### Slope Range <!-- only for kinds that follow displacement: Grass, Rock, Sand, Moss -->
<!-- scatter only appears where the ground's angle is in range; sloped, terrain and displaced floors all count -->
Min Angle [-] [Number Slider] [+] [=]
Max Angle [-] [Number Slider] [+] [=]

### Level of Detail <!-- only for kinds that render three tiers -->
<!-- mesh detail drops Near -> Mid -> Far with distance; only Near casts real per-instance shadows by default -->
Near Dist [-] [Number Slider] [+] [=]
Mid Dist [-] [Number Slider] [+] [=]
Far tier spans Mid Dist to Draw Dist (<n>) <readout>

### Soft Edges <!-- only for kinds that scatter -->
Range Fade [-] [Number Slider] [+] [=] <!-- the outer camera-ring boundary; 0 is a hard cutoff at Draw Dist -->
Edge Fade Dist [-] [Number Slider] [+] [=] <!-- the sector's own painted boundary; 0 is a hard cutoff at the polygon edge -->
Boundary Jitter [-] [Number Slider] [+] [=] <!-- bulges Draw Dist and any LOD rings so the transition does not read as a circle -->

### Ground <!-- every kind, including Water, whose patch is the lakebed under the surface -->
Ground Texture <dropdown of available textures>
Texture Scale U [-] [Number Slider] [+] [=] <!-- same world-XZ units as a structural floor's UV scale -->
Texture Scale V [-] [Number Slider] [+] [=]
Roughness [-] [Number Slider] [+] [=] <!-- absent for Sand, whose amplitude is Dune Height -->
Detail Frequency [-] [Number Slider] [+] [=]
Detail Octaves [-] [Number Slider] [+] [=]

---

# Surface Editor (2D)

<!--
A mode of the main window, entered from any floor, ceiling or wall — from its
[Edit ... Surface] button, or from the face's right-click menu. Esc returns to
whichever mode opened it.

It is the surface's own flat view: the holes cut into it, and the points that
give it its shape. One face at a time, deliberately — every coordinate here is
in that face's UV space, and two faces do not share one.

Sub-modes:
  idle       drag an existing point
  extrusion  shift+click places polygon points, Enter finishes, Esc cancels
  terrain    shift+click places a terrain point, right-click deletes the
             nearest, [Done] leaves
-->

## Context
Surface: <wall | floor | ceiling> | Sector: <ID>
Zoom: <n> px/unit <readout>
[Back (Esc)]
Edit Layer: [Both] [Outer] [Inner] <!-- which extrusion points may be dragged; the two are drawn cyan and magenta, joined by grey lines -->

## Extrusions
[+ New Extrusion] <!-- only when nothing else is being placed -->
Placing extrusion (<n> pts) <readout> [Finish (Enter)] [Cancel (Esc)] <!-- only while placing -->
<!-- [x] deletes the extrusion; [+] duplicates it -->
- [x] [+] Ext #0 (<n> pts)
  Depth [-] [Number Slider] [+] [=]
  Texture <dropdown of available textures> <!-- "(none)" falls back to the face's -->
  [eye] Visible
  Back Visible [checkbox]
  Alpha [-] [Number Slider] [+] [=]
  Outer Shape <!-- U/V per point -->
  Inner Shape <!-- U/V per point -->
  Texture UV

## Terrain
Terrain Enabled [checkbox]
[+ Place Terrain Points] <!-- only when nothing else is being placed -->
Shift+click to place | Right-click to delete <readout> [Done] <!-- only while placing -->
<n> points — click to select <readout>
### Point #<n> <!-- only when one is selected -->
U [-] [Number Slider] [+] [=]
V [-] [Number Slider] [+] [=]
Depth [-] [Number Slider] [+] [=]
[Delete]

## Editor
Snap to grid [Checkbox]
Snap Delta [Number Slider]

---

# Surface Sub-Panels

<!--
The six blocks every floor, ceiling and wall carries. Identical wherever the
face was selected from — the 3D view or the sector map — so they are laid out
once here rather than three times above.
-->

## Texture UV
UV Offset U [-] [Number Slider] [+] [=]
UV Offset V [-] [Number Slider] [+] [=]
UV Scale U [-] [Number Slider] [+] [=]
UV Scale V [-] [Number Slider] [+] [=]
UV Rotation (deg) [-] [Number Slider] [+] [=]

## Extrusions (<n>)
<!-- the list and the per-extrusion fields the Surface Editor draws, reachable without leaving the face -->
- [x] [+] Ext #0 (<n> pts)
  Depth / Texture / Visible / Back Visible / Alpha / Outer Shape / Inner Shape / Texture UV

## Terrain (<n> pts)
Enabled [checkbox]
[+ Add Point]
- Point #0
  U [-] [Number Slider] [+] [=]
  V [-] [Number Slider] [+] [=]
  Depth [-] [Number Slider] [+] [=]
  [Delete]

## Smooth Shading
Smooth Shading [checkbox] <!-- weld the normals along shared edges, so neighbouring faces read as one curved surface -->
Crease Angle (deg) [-] [Number Slider] [+] [=] <!-- only when Smooth Shading is on -->

## Detail <!-- per-face procedural displacement and scatter -->
Enabled [checkbox]
Preset <dropdown of built-in and user presets, plus "(custom)">
<textbox> [Save Preset | Update Preset] [Delete] <!-- only user presets are deletable; the built-ins are compiled in -->
Seed <textbox> [Randomize]
### Displace surface [checkbox]
- Wave 1 [checkbox]
  Amplitude / Frequency / Rotation (deg) / Tessellation [-] [Number Slider] [+] [=]
- Wave 2 [checkbox]
- Wave 3 [checkbox]
Noise [checkbox]
  Amplitude / Frequency / Octaves / Tessellation [-] [Number Slider] [+] [=]
Effective tessellation: <n> <readout> <!-- tessellation is per-patch, so the active layers share the highest level -->
Edge Taper [-] [Number Slider] [+] [=]
### Scatter meshes [checkbox]
Rock Texture <dropdown of available textures>
Density / Spacing / Scale / Jitter / Draw Range [-] [Number Slider] [+] [=]

## Tags / Attributes / Script <!-- see Scripting, below -->

---

# Scripting

<!--
Every addressable object carries the same three blocks: an entity, a volume, a
sector, an edge, a floor, a ceiling, a chunk and a light. One layout, drawn
wherever they appear, so there is one thing to learn.

An entity's tags and attributes are merged from its definition rather than
held on the instance, so they are edited as their own set — the widgets are
the same, the storage is not.
-->

## Tags
<!-- [x] removes the tag; the list is kept sorted, since a tag is what a script's #tag addresses -->
- [x] #tag1
- [x] #tag2
<textbox> [Add Tag]

## Attributes
<!-- [x] removes the attribute; changing a type converts the value rather than resetting it -->
- [x] [type dropdown] [name textbox] [default value textbox]
- [x] [type dropdown] [name textbox] [default value textbox]
- [x] [type dropdown] [name textbox] [default value textbox]
Name <textbox>
Type <dropdown of attribute types>
[Add Attribute]

## Script
File <dropdown of .flang files under scripts/, plus "(none)"> [Rescan] [right arrow]
<!-- a combo rather than a text field: a mistyped path is a script that silently never runs -->
<owner> runs <path> <readout>
### Parameters <!-- one row per `param` the attached script declares; absent when it declares none and none are bound -->
@name <type> [x] <!-- [x] hands the parameter back to the script's default; "(default)" is shown instead when nothing is bound -->
  <value editor matching the type; a `ref` is a dropdown of the level's IDs>
<path> does not declare: <names> <readout> <!-- values left behind by a renamed param -->
[Clear Unused]

---

# Editor Settings

<!--
The bottom block of every panel, in every mode, in both editors. Same
controls, same place, whatever is being edited — these are about the editor
rather than about the level, and hunting for them per mode is what the revamp
is meant to stop.
-->

## Grid
Snap to grid [Checkbox]
Snap Delta [Number Slider]

## Engine <!-- engine-wide switches, not level data; they do not save with the level -->
Entity/world collision [checkbox] [8] <!-- each entity still has to opt in; this says whether the engine listens -->
Entity/entity collision [checkbox] [9]
Gravity [checkbox] [0]
<!-- entities only move while the editor is closed; the panel says so -->

## Audio <!-- the only settings in this block that persist; written to <exe>/audio.yaml when a slider is released -->
Master gain [-] [Number Slider] [+] [=] <!-- multiplies everything, music included -->
Music gain [-] [Number Slider] [+] [=]
Effects gain [-] [Number Slider] [+] [=]
<!-- a line reporting live voices and the last block's peak, and any underruns -->
<!-- with no audio device the whole group is replaced by one line saying so -->

## Rendering <!-- the biome shadow cost switches, today reachable only by function key -->
Biome scatter receives shadows [checkbox] [F5]
Rock / moss cast shadows [checkbox] [F6]
Foliage casts shadows [checkbox] [F7] <!-- expensive: re-instances every blade into every cascade -->
Cast Cascades [-] [Number Slider] [+] [=] [F8] <!-- how many cascades the scatter is drawn into, nearest first -->
Cast LOD Tiers [-] [Number Slider] [+] [=] [F9] <!-- how many LOD tiers cast at all, nearest first -->
Cast Range [-] [Number Slider] [+] [=] <!-- extra cap on the scatter ring for shadow passes; 0 = whatever the shadow map itself reaches -->
Anti-aliasing [checkbox] [F10]
Debug overlay [checkbox] [F3]
Fullscreen [checkbox] [F11]

## Panel
Panel width [-] [Number Slider] [+] [=] <!-- also draggable from the panel's left edge -->
Explorer height [-] [Number Slider] [+] [=] <!-- how tall the Explorer's tree is before it scrolls; the rest of the panel is the selection's properties -->
[Keybindings (F1)]

<!--
Not covered here: the model editor, the texture editor, the script editor and
the log. Each is its own window with its own document, rather than a mode of
either level editor, and each deserves its own pass.
-->
