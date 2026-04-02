# Bugs

- [ ] Wall collision should only be checked up to the height of the wall
- [ ] Clicking on the editor mode buttons crashes the editor with `16:26:59 [foundry] PANIC: Assertion failed! File: /home/jfcarter2358/go/1.23.1/pkg/mod/github.com/inkyblackness/imgui-go/v4@v4.7.0/imgui/imgui.h, Line 1730 Expression: Size > 0`
- [x] Editing a chunk's position doesn't actually edit it
- [x] Sector connections should be inferred from shared edges

# TODO

- [x] Directional lights
- [x] Global lights
- [x] Point lights
- [x] Ability to individually edit sector points positions
- [x] Ability to add/remove sectors
- [x] Ability to snap points together so we can connect sectors
- [x] Ability to fullscreen the application
- [ ] Level editor should pop out into a separate window and I should be able to switch between them, using a key to switch between the two (handling mouse focus as well)
- [ ] Direct YAML editor
- [x] Should be able to connect to vertices to create new sectors
- [x] 3D level editor should let me click on entities to then edit their properties
- [x] Entity collision
- [x] Shadows
- [ ] Baked lighting
- [ ] Animations
- [ ] Scripting engine
- [ ] RDS (isometric?) mode
- [ ] Default entity definitions
- [x] Gravity (global)
- [ ] Gravity (volumes)
- [ ] Script trigger volumes
- [ ] Vehicles
- [ ] Level builder
- [ ] UI elements
- [ ] UI builder
- [ ] Better test level
- [ ] Basic test models
- [x] 2-way rendering for walls/ceilings/floors
- [ ] Object collision physics
- [x] Optional FPS/position/debug/etc. display
- [ ] "Static" models (don't move, baked into collision/lighting)
- [ ] Primitives (plane, square, etc.)
- [ ] Sprites
- [x] Sector/chunk culling for rendering
- [ ] Transparency in textures
- [ ] Textures/models/entities/etc. should be loaded from a directory traversal, not a hard-coded map
