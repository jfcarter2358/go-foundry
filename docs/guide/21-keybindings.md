# Keybindings

`F1` in the engine shows this same table, with the section for whatever mode you
are in brought to the front. It is generated from the engine's own bindings, so
it is never out of date.

## Global

| | |
| --- | --- |
| `F1` | the reference: keybindings, events, the stdlib, properties |
| `F3` | debug overlay (FPS, memory, counts) |
| `F10` | anti-aliasing |
| `F11` | fullscreen |
| `F12` | vsync |
| `~` / `` ` `` | toggle the level editor |
| `Esc` | close the reference |

## Gameplay

| | |
| --- | --- |
| `W` `A` `S` `D` | move |
| `Space` | jump (gravity on) / fly up (gravity off) |
| `Left Shift` | fly down (gravity off) |
| Arrow keys | turn / look |
| Mouse | look; the cursor is captured |
| `E` | sit / stand / get out of a vehicle, else interact |
| `0` | gravity (fly mode) |
| `8` | entity-vs-world collision, engine-wide |
| `9` | entity-vs-entity collision, engine-wide |

## Driving

| | |
| --- | --- |
| `E` | take a seat within reach; again to stand up or get out |
| `W` / `S` | throttle forward / back |
| `A` / `D` | steer |
| `Space` | brake |
| `Q` / `E` | roll (air and space) |
| `L Shift` / `L Ctrl` | pitch nose down / up (air and space) |
| Mouse | look around from the seat, within its limits |

## Editor — any mode

| | |
| --- | --- |
| `Ctrl+Z` | undo |
| `Ctrl+Y` / `Ctrl+Shift+Z` | redo |
| `Delete` | delete the current selection |

## Editor — 3D view

| | |
| --- | --- |
| `Tab` | object mode / edit mode |
| `Ctrl+Shift`+left click | shape that brush's detail, leaving its collision alone |
| `1` / `2` / `3` | point / edge / face select (enters edit mode) |
| `G` / `R` / `C` | move / rotate / scale gizmo |
| `Z` | wireframe |
| `B` | bounding boxes |
| `V` | orbit / fly camera |
| `F` | frame the selection |
| `W` `A` `S` `D` | fly the camera (orbit: slide the pivot) |
| `Space` / `Left Shift` | up / down |
| Right-drag / middle-drag | look around, or orbit the pivot |
| `Shift`+middle-drag | pan |
| Wheel | dolly in fly, zoom the orbit radius |
| Left click | object mode: pick up a brush. Edit mode: pick its point/edge/face |
| Left click on a handle | pick a point/edge; repeat clicks cycle overlaps |
| `Ctrl`+left click | add to the selection (brushes, faces, points, edges) |
| Drag a gizmo arm | translate the selection along that axis |

## Editor — object mode (3D)

| | |
| --- | --- |
| Left click | pick up a whole brush |
| `Ctrl`+left click | add another brush to the selection |
| `G` / `R` / `C` | move / turn / scale all of it |
| `Delete` | delete the selected brushes |
| `Tab` | into edit mode, to reshape the selected brush |
| `Ctrl+Shift`+left click | into detail mode, to shape what it looks like |

## Editor — edit mode (3D)

| | |
| --- | --- |
| `1` / `2` / `3` | a brush's points, edges or faces |
| `E` / `Shift+E` | extrude the selected faces along their normals / pull them in |
| `I` / `Shift+I` | inset the selected faces / widen them |
| `B` | subdivide the selected faces |
| `Delete` | delete the selected faces' brushes |
| `Tab` | back to object mode, with this brush selected |

## Editor — detail mode (3D)

Shaping the mesh drawn over one brush. Nothing here changes collision; see
[Shaping surfaces](03-shaping-surfaces.md).

| | |
| --- | --- |
| `Ctrl+Shift`+left click | go in on the brush under the cursor, or come back out of it |
| `1` / `2` / `3` | the detail mesh's points, edges or faces |
| `E` / `Shift+E` | extrude the selected faces, walling in the sides / press them in |
| `I` | inset — a mitred border, leaving the middle selected |
| `B` | subdivide the selected faces into quadrants |
| `Shift+B` | smooth the whole detail mesh (Catmull-Clark) |
| `Delete` / `X` | delete the selected detail faces |
| `G` / `R` / `C` | move / turn / scale the selection |
| `Tab` / `Esc` | back to object mode, with this brush selected |

## Editor — 2D sector map

| | |
| --- | --- |
| `W` `A` `S` `D`, middle-drag | pan |
| Wheel | zoom |
| `1` / `2` / `3`, `G` / `R` / `C` | as the 3D view — the modes are shared |
| Left click / drag | select and move a vertex |
| `Ctrl`+left click | add to the selection |
| `Shift`+left click | split the nearest edge, else drop a free point |
| `J` | join the picked points into a chain |
| `Shift+J` | join and close the loop, making a sector |
| Right click | remove the point under the cursor, else deselect |

## Editor — biome brush (2D)

| | |
| --- | --- |
| Left click | add a polygon point; click the first point to close |
| Right click | undo the last point while drawing, else delete a sector |
| `Enter` | finish the polygon |
| `Esc` | cancel |

## Editor — shaping a surface

| | |
| --- | --- |
| `1` / `2` / `3` | the surface's points, edges or faces |
| Left click | pick; `Ctrl+click` adds |
| Drag an arm | move — green is depth, red U, blue V |
| `E` | extrude the selected faces, then drag the depth arm |
| `I` | inset |
| `B` | subdivide |
| `C` | connect two selected points, splitting the face |
| `H` | open the selected faces as a hole, or close them |
| `Ctrl+A` | select all |
| `Delete` | delete the selected faces |
| `Esc` | done shaping |
| `W A S D` / `Space` / `Shift` | fly, exactly as outside surface mode |

## Editor — waypoint animations

The first point is placed from `Add... > Waypoint Animation` or the view's
right-click menu.

| | |
| --- | --- |
| Left click an arrow | select that point |
| `Ctrl+click` | with a point selected: add the next point there, straight after it |
| `G` / `R` / `C` | move, turn or scale the selected point |
| `Delete` | delete the selected point, joining the ones either side |

With an animation selected, its timeline runs along the bottom of the view:

| Timeline | |
| --- | --- |
| Click a diamond | select that point, and move the playhead to it |
| Drag a diamond | retime the point, in 0.05 s steps |
| `Shift` + drag a diamond | retime it freely |
| Click or drag elsewhere | scrub the playhead |

## Editor — brush stamp

Armed from `Add... > Brush` or the view's right-click menu.

| | |
| --- | --- |
| Left click | place a solid where the ghost box is; the stamp stays armed |
| `Ctrl+Z` | take back the last brush placed |
| `Esc` | put the stamp away |

## Editor — scenery brush

| | |
| --- | --- |
| Left click | place a copy where the ghost is; the brush stays armed |
| `[` / `]` | turn the ghost 15 degrees (5 with `Shift`) |
| `-` / `=` | shrink / grow |
| `Ctrl+Z` | take back the last copy placed |
| `Esc` | put the brush away |

## Model editor

| | |
| --- | --- |
| `1` / `2` / `3` / `4` | vertex / edge / face / bone select |
| `G` / `R` / `C` | move / rotate / scale (`C` again: local axes) |
| `E` / `Shift+E` | extrude (again: re-aim along the normal) / always extrude |
| `I` | inset — a mitred border round the selected faces (outside pose mode) |
| `V` | orbit / fly camera |
| `Z` | wireframe |
| `F` | frame the model |
| `P` | pose mode |
| `Delete` | delete the selected bones or faces |
| `Ctrl+Z` / `Ctrl+Y` | undo / redo |
| `Tab` | hand focus back to the main window |

### Pose mode

| | |
| --- | --- |
| `I` | key every posed bone at the playhead |
| `Space` | play / pause |
| `Left` / `Right` | step one frame |
| `Delete` | delete the selected keyframes |
| `Alt+G` / `Alt+R` / `Alt+S` | clear translation / rotation / scale |
| `G` / `R` / `C` | pose the selected bones (scale on the bone's own axes) |
| Click a diamond | select that keyframe (`Ctrl` to add) |
| Click / drag the timeline | scrub the playhead |

## Entity editor

| | |
| --- | --- |
| Middle- or right-drag | orbit (mouse-look in fly mode) |
| Right-click (no drag) | context menu for whatever is selected |
| `Shift`+middle-drag | pan |
| Wheel | zoom |
| `V` | orbit / fly camera |
| `WASD`, `Space` / `Shift` | move (`Ctrl` faster) — the eye in fly mode, the pivot while orbiting |
| `F` | frame the whole type |
| `Shift+G` / `B` | grid / bounds cage |
| `P` | play / pause the attachment animations |
| `Delete` / `X` | remove the selected model, brush or seat |
| `Ctrl+N` / `O` / `S` | new / open / save the type |
| `Ctrl+Shift+S` | save as |
| `Ctrl+Z` / `Ctrl+Shift+Z` | undo / redo |

The type's **brushes** take the 3D view's keys, unchanged — an author who has
blocked out a room already knows how to block out a hold:

| | |
| --- | --- |
| `Tab` | object / edit mode |
| `Ctrl+Shift`+left click | detail mode on that brush — shape it without changing collision |
| `1` / `2` / `3` | points / edges / faces (edit mode, and detail mode) |
| `G` / `R` / `C` | move / rotate / scale |
| `E` / `I` / `B` | extrude / inset / subdivide the selected face(s) |
| `Shift+E` / `Shift+I` | extrude inward / inset outward |
| `B` / `Shift+B` | in detail mode: subdivide / smooth the whole detail mesh |
| `Z` | wireframe |
| `Escape` | put an armed brush stamp away, or leave detail mode |

`B` is subdivide while a face is selected and the bounds cage when none is, and
`G` is the move tool rather than the grid — which is why the grid is on
`Shift+G` here, as it is in the 3D view.

## Texture editor

| | |
| --- | --- |
| Left click / drag | paint with the current tool |
| Middle-drag | pan the canvas |
| Wheel | zoom |
| Click a swatch | set the primary colour |
| Right-click a swatch | set the secondary colour |
| Double-click a swatch | edit that palette slot's colour |
| `Ctrl+Z` / `Ctrl+Y` | undo / redo (`Ctrl+Shift+Z` redoes too) |

## Particle editor

| | |
| --- | --- |
| `Space` | play / pause the preview |
| `R` | restart the effect |
| `.` | step one frame |
| `F` | frame the camera |
| `B` | show / hide the emission shape |
| Middle- or right-drag | orbit |
| `Shift`+middle-drag | pan |
| Wheel | zoom |
| Click a ramp | add a stop; drag moves it, right-click deletes |
| Double-click a colour stop | edit that colour |
| `Ctrl+N` / `O` / `S` | new / open / save |
| `Ctrl+Z` / `Ctrl+Shift+Z` | undo / redo |

## UI editor

| | |
| --- | --- |
| Click | select a widget; `Alt+click` selects its parent |
| Drag | move (`Alt` suspends snapping) |
| Drag a corner handle | resize |
| Drag an anchor marker | re-anchor without moving the widget |
| Middle-drag | pan the canvas |
| Wheel | zoom about the cursor |
| `F` | frame the canvas |
| `G` / `B` | grid / outlines |
| `Ctrl+D` | duplicate |
| `Delete` / `X` | delete |
| `Ctrl+N` / `O` / `S` | new / open / save |
| `Ctrl+Shift+S` | save as |
| `Ctrl+Z` / `Ctrl+Shift+Z` | undo / redo |

## Script editor

| | |
| --- | --- |
| `Ctrl+S` / `Ctrl+Shift+S` | save / save as |
| `Ctrl+O` | open a script in a new tab |
| `Ctrl+N` | new script |
| `Ctrl+W` | close the current tab (asks, if unsaved) |
| `Ctrl+Tab` / `Ctrl+Shift+Tab` | next / previous tab |
| `Ctrl+R` | reload every script from disk |
| Drag a script | move it into the folder it is dropped on |

### The buffer

| | |
| --- | --- |
| `Ctrl+X` / `C` / `V` | cut / copy / paste, through the system clipboard |
| `Ctrl+Z` / `Ctrl+Y` | undo / redo |
| `Ctrl+A` | select all |
| `Shift`+arrows, `Shift`+click | extend the selection |
| Double / triple click | select a word / a line |
| `Tab` | insert a tab |
| Right click | cut, copy, paste, select all |

### Completion

| | |
| --- | --- |
| Type a name | opens the list; `@`, `#`, `?`, `:` and `.` open it at once |
| Arrow keys | never open it |
| `Ctrl+Space` | open it without typing |
| `Up` / `Down` | move (`Page Up` / `Page Down` by ten) |
| `Tab` | accept the selected row |
| `Enter` | accept too, unless it is already exactly what is typed |
| Click a row | accept that one |
| `Esc` | dismiss for this word |

## Log window

| | |
| --- | --- |
| `Space` | pause / resume the view (the log keeps filling) |
| `Ctrl+L` | clear the buffer |
| `Ctrl+F` | jump to the filter box |

## Biome and shadow cost

| | |
| --- | --- |
| `F5` | biome scatter receives shadows |
| `F6` | rock / moss cast shadows |
| `F7` | foliage casts shadows (expensive) |
| `F8` | cycle how many cascades scatter casts into |
| `F9` | cycle how many LOD tiers cast real shadows |
