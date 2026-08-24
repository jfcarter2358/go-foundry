<!-- Need to add support for chunk scaling in X, Y, and Z> -->
<!-- Need support for alpha floors/walls/ceilings -->
<!-- if any item >

<!-- if an object is clicked then it becomes selected, additionally control-click is available for multi-selection -->
# Objects
- Spawn Point <!-- Should be automatically placed at the origin -->
## Chunks [+] [up arrow] <!-- [up arrow] is a utf-8 up arrow to import a chunk from an exported file, [+] is a button to create a new chunk -->
<!--
[x] is a button that deletes the chunk
[+] is a button that duplicates the chunk
[save icon] is a utf-8 save icon to export a chunk to a file
[checkbox] toggles if the chunk exists in the level
-->
- [x] [save icon] [+] [checkbox] Chunk 1
- [x] [save icon] [+] [checkbox] Chunk 2
- [x] [save icon] [+] [checkbox] Chunk 3
## Entities [+] [up arrow] <!-- [up arrow] is a utf-8 up arrow to import an entity from an exported file, [+] is a button to create a new entity -->
<!--
[x] is a button that deletes the entity
[+] is a button that duplicates the entity
[save icon] is a utf-8 save icon to export an entity to a file
[checkbox] toggles if the entity exists within the level
-->
- [x] [+] [save icon] [checkbox] Entity 1
- [x] [+] [save icon] [checkbox] Entity 2
- [x] [+] [save icon] [checkbox] Entity 3
## Lights
- [checkbox] Global Light <!-- always exists in the level can just be toggled on and off but not removed, the checkbox toggles this-->
### Directional [+] <!-- [+] is a button to create a new directional light -->
<!--
[x] removes the light
[+] duplicates the light
[checkbox] enables/disables the light
-->
- [x] [+] [checkbox] Light 1
- [x] [+] [checkbox] Light 2
- [x] [+] [checkbox] Light 3
### Point [+]  <!-- [+] is a button to create a new point light -->
<!--
[x] removes the light
[+] duplicates the light
[checkbox] enables/disables the light
-->
- [x] [+] [checkbox] Light 1
- [x] [+] [checkbox] Light 2
- [x] [+] [checkbox] Light 3
### Scripts
<!--
[x] removes the script
[+] duplicates the script
[save icon] is a utf-8 save icon to export a script to a file
[right arrow] is a utf-8 right arrow which opens the script in the script editor
-->
- [x] [save icon] [+] Script 1
- [x] [save icon] [+] Script 2
- [x] [save icon] [+] Script 3
# Properties <!-- editing any properties applies those properties to everything selected of that type, that being said the relative positions/rotations/scales are respected as they are currently if multiple objects are selected>
## Transform
<!-- position, rotation, and scale applied to any selected object -->
X [-] [Number Slider] [+] [=]
Y [-] [Number Slider] [+] [=]
Z [-] [Number Slider] [+] [=]
Rotation Yaw [-] [Number Slider] [+] [=]
Rotation Pitch [-] [Number Slider] [+] [=]
Rotation Roll [-] [Number Slider] [+] [=]
Scale X [-] [Number Slider] [+] [=]
Scale Y [-] [Number Slider] [+] [=]
Scale Z [-] [Number Slider] [+] [=]
## Chunk <!-- only displayed if a chunk(s) is selected>
ID: <ID1, ID2, ...>
Sectors: <Sector count>
## Sector <!-- only displayed if a sector(s), wall, ceiling, or floor is selected>
ID: <ID1, ID2, ...>
### [checkbox] Floor <!-- only displayed if a floor is selected, checkbox enables/disables sector floor(s)>
Visible [checkbox]
Height [-] [Number Slider] [+] [=]
Texture <dropdown of available textures>
Terrain [checkbox]
Double Sided [checkbox]
Alpha [-] [Number Slider] [+] [=]
### [checkbox] Ceiling <!-- only displayed if a ceiling is selected, checkbox enables/disables sector ceiling(s)>
Visible [checkbox]
Height [-] [Number Slider] [+] [=]
Texture <dropdown of available textures>
Terrain [checkbox]
Double Sided [checkbox]
Alpha [-] [Number Slider] [+] [=]
### [checkbox] Wall <!-- only displayed if a wall is selected, checkbox enables/disables wall(s) >
Visible [checkbox]
Height [-] [Number Slider] [+] [=]
Texture <dropdown of available textures>
Terrain [checkbox]
Double Sided [checkbox]
Alpha [-] [Number Slider] [+] [=]
## Entity <!-- Only displayed if an entity is selected -->
ID: <ID1, ID2, ...>
Model <dropdown of available models>
Texture <dropdown of available textures>
Tags <textbox with comma separated tags>
### Bounding
Method <dropdown of bounding methods, AABB, cylinder, sphere>
Half X [-] [Number Slider] [+] [=]
Half Y [-] [Number Slider] [+] [=]
Half Z [-] [Number Slider] [+] [=]
### Attributes
- [x] [type dropdown] [name textbox] [default value textbox]
- [x] [type dropdown] [name textbox] [default value textbox]
- [x] [type dropdown] [name textbox] [default value textbox]
### Scripts
- [x] Script 1
- [x] Script 2
- [x] Script 3
## Lights
### Global  <!-- only visible if the global light is selected -->
[R:<num>] [G:<num>] [B:<num>] [color picker] Color
Brightness [-] [Number slider] [+] [=]
### Directional  <!-- only visible if a directional light(s) is selected, position + rotation come from the transform panel, scale does nothing -->
Direction: <direction x>, <direction y>, <direction z>
[R:<num>] [G:<num>] [B:<num>] [color picker] Color
Brightness [-] [Number slider] [+] [=]
Cast shadows [checkbox]
Shadow strength [-] [Number slider] [+] [=]
### Point <!-- only visible if a point light(s) is selected, position comes from the transform panel, rotation and scale do nothing -->
Color [R:<num>] [G:<num>] [B:<num>] [color picker]
Brightness [-] [Number slider] [+] [=]
Radius [-] [Number Slider] [+] [=]
Cast shadows [checkbox]
Shadow strength [-] [Number slider] [+] [=]
# Editor
Snap to grid [Checkbox]
Snap Delta [Number Slider]
