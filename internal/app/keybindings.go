// Development-build only: the F1 reference describes the editor, and a release
// build has neither the editor nor the overlay that draws this. See
// devtools_dev.go.

//go:build !release

package app

import (
	"foundry/internal/editor"
	"foundry/internal/textureui"
	"foundry/internal/uiwin"
)

// keybindings.go — what the F1 overlay says.
//
// The bindings themselves live where they are handled (input.go, editor.go and
// the per-mode editor files); this file is the single place that *describes*
// them. Sections carry an Active predicate so the overlay can foreground the
// bindings that apply to whatever mode is on screen right now and dim the rest
// — the list is long enough that showing it flat is close to useless.
//
// Those predicates read editor state, which is why the table is here rather
// than beside the overlay that draws it (keybind_help.go).

func init() { uiwin.KeybindSections = keybindSectionTable }

var keybindSectionTable = []uiwin.KeybindSection{
	{
		Title:  "Script log",
		Active: editor.LogWindowOpen,
		Binds: []uiwin.Keybind{
			{Keys: "Space", Desc: "Pause / resume the view (the log keeps filling)"},
			{Keys: "Ctrl+L", Desc: "Clear the buffer"},
			{Keys: "Ctrl+F", Desc: "Jump to the filter box"},
		},
	},
	{
		Title:  "Script editor",
		Active: editor.ScriptEditorOpen,
		Binds: []uiwin.Keybind{
			{Keys: "Ctrl+S / Ctrl+Shift+S", Desc: "Save / save as"},
			{Keys: "Ctrl+O", Desc: "Open a script in a new tab"},
			{Keys: "Ctrl+N", Desc: "New script"},
			{Keys: "Ctrl+W", Desc: "Close the current tab (asks, if it is unsaved)"},
			{Keys: "Ctrl+Tab", Desc: "Next tab (Ctrl+Shift+Tab: previous)"},
			{Keys: "Ctrl+R", Desc: "Reload every script from disk"},
			{Keys: "Drag a script", Desc: "Move it into the folder it is dropped on"},
		},
	},
	{
		Title:  "Script editor — the buffer",
		Active: editor.ScriptEditorOpen,
		Binds: []uiwin.Keybind{
			{Keys: "Ctrl+X / C / V", Desc: "Cut / copy / paste, through the system clipboard"},
			{Keys: "Ctrl+Z / Ctrl+Y", Desc: "Undo / redo"},
			{Keys: "Ctrl+A", Desc: "Select all"},
			{Keys: "Shift+arrows", Desc: "Extend the selection"},
			{Keys: "Shift+click", Desc: "Extend the selection to the click"},
			{Keys: "Double / triple click", Desc: "Select a word / a line"},
			{Keys: "Tab", Desc: "Insert a tab (the buffer takes it, rather than moving on)"},
			{Keys: "Right click", Desc: "Cut, copy, paste and select all as a menu"},
		},
	},
	{
		Title:  "Script editor — completion",
		Active: editor.ScriptEditorOpen,
		Binds: []uiwin.Keybind{
			{Keys: "Type a name", Desc: "Opens the list; `@`, `#`, `:` and `.` open it at once"},
			{Keys: "Arrow keys", Desc: "Never open it — moving the caret about leaves them alone"},
			{Keys: "Ctrl+Space", Desc: "Open the list without typing"},
			{Keys: "Up / Down", Desc: "Move the selection (Page Up / Page Down by ten)"},
			{Keys: "Tab", Desc: "Accept the selected row"},
			{Keys: "Enter", Desc: "Accept it too, unless it is already exactly what is typed"},
			{Keys: "Click a row", Desc: "Accept that one"},
			{Keys: "Esc", Desc: "Dismiss the list for this word"},
		},
	},
	{
		Title:  "Texture editor",
		Active: textureui.TextureEditorOpen,
		Binds: []uiwin.Keybind{
			{Keys: "Ctrl+Z / Ctrl+Y", Desc: "Undo / redo (Ctrl+Shift+Z redoes too)"},
			{Keys: "Left click / drag", Desc: "Paint with the current tool"},
			{Keys: "Middle-drag", Desc: "Pan the canvas"},
			{Keys: "Mouse wheel", Desc: "Zoom the canvas"},
			{Keys: "Click a swatch", Desc: "Set the primary colour"},
			{Keys: "Right-click a swatch", Desc: "Set the secondary colour"},
			{Keys: "Double-click a swatch", Desc: "Edit that palette slot's colour"},
			{Keys: "Drag (Rect Select / Lasso)", Desc: "Select; Shift adds, Alt or Ctrl subtracts, a click clears"},
			{Keys: "Drag inside the selection", Desc: "Move what it holds"},
			{Keys: "Arrow keys", Desc: "Nudge the selection's contents (Shift: 8 texels)"},
			{Keys: "Ctrl+C / Ctrl+X / Ctrl+V", Desc: "Copy / cut / paste the selection"},
			{Keys: "Ctrl+A / Ctrl+D or Esc", Desc: "Select all / deselect"},
			{Keys: "Ctrl+Shift+I", Desc: "Invert the selection"},
			{Keys: "Delete", Desc: "Clear the selection's texels"},
		},
	},
	{
		Title:  "Particle editor",
		Active: editor.ParticleEditorOpen,
		Binds: []uiwin.Keybind{
			{Keys: "Space", Desc: "Play / pause the preview"},
			{Keys: "R", Desc: "Restart the effect"},
			{Keys: ".", Desc: "Step one frame"},
			{Keys: "F", Desc: "Frame the camera"},
			{Keys: "B", Desc: "Show / hide the emission shape"},
			{Keys: "MMB drag / RMB drag", Desc: "Orbit"},
			{Keys: "Shift+MMB drag", Desc: "Pan"},
			{Keys: "Wheel", Desc: "Zoom"},
			{Keys: "Click a ramp", Desc: "Add a stop; drag moves it, right-click deletes"},
			{Keys: "Double-click a colour stop", Desc: "Edit that colour"},
			{Keys: "Ctrl+N / Ctrl+O / Ctrl+S", Desc: "New / open / save"},
			{Keys: "Ctrl+Z / Ctrl+Shift+Z", Desc: "Undo / redo"},
		},
	},
	{
		Title:  "Score editor",
		Active: editor.ScoreEditorOpen,
		Binds: []uiwin.Keybind{
			{Keys: "F1 (in the Score window)", Desc: "The active keymap over the score, with your rebindings"},
			{Keys: "Edit > Keyboard Shortcuts", Desc: "Sibelius (default) or MuseScore keymap; rebind any command"},
			{Keys: "N, then click", Desc: "Note input: a click writes the pitch the key implies there"},
			{Keys: "A-G / 2-9 / Shift+2-9", Desc: "Letters after the selection; intervals above / below (Sibelius)"},
			{Keys: "Keypad 1-6, F8 / Alt+1-6", Desc: "Durations 32nd..whole; second layout 64th..breve"},
			{Keys: "Click / Ctrl+click / Shift+click", Desc: "Select a bar / it in every line / extend"},
			{Keys: "Ctrl+C / Ctrl+V / R / Alt+click", Desc: "Copy / paste / repeat after / paste at the click"},
			{Keys: "Space / P / Home", Desc: "Play or stop from the play head / play from the selection / to start"},
			{Keys: "L / drag the ruler", Desc: "Loop the selected bars / loop between two beats"},
			{Keys: "M (Sibelius) / F10 (MuseScore)", Desc: "Score / Mixer view"},
		},
	},
	{
		Title:  "Tracker editor",
		Active: editor.TrackerEditorOpen,
		Binds: []uiwin.Keybind{
			{Keys: "Z S X D C V G B H N J M", Desc: "Notes, lower octave (grid focused, note column)"},
			{Keys: "Q 2 W 3 E R 5 T 6 Y 7 U I", Desc: "Notes, upper octave"},
			{Keys: "[ / ]", Desc: "Octave down / up"},
			{Keys: "1 / `", Desc: "Note off / note cut"},
			{Keys: "0-9 A-F", Desc: "Hex entry in instrument, volume and effect columns"},
			{Keys: "Backspace / Delete", Desc: "Clear the field"},
			{Keys: "Insert / Shift+Backspace", Desc: "Insert / delete a row"},
			{Keys: "Space", Desc: "Toggle edit mode (off: keys only audition)"},
			{Keys: "Ctrl+0..9", Desc: "Edit step"},
			{Keys: "Arrows / Tab / PgUp PgDn", Desc: "Move; next channel; by a bar"},
			{Keys: "Shift+arrows / drag", Desc: "Select a block"},
			{Keys: "Ctrl+C / X / V", Desc: "Copy / cut / paste"},
			{Keys: "Ctrl+Shift+V", Desc: "Mix-paste (empty fields only)"},
			{Keys: "Ctrl+Up / Down", Desc: "Transpose a semitone (+Shift: an octave)"},
			{Keys: "Ctrl+I", Desc: "Interpolate volume / effects across the selection"},
			{Keys: "Ctrl+[ / Ctrl+]", Desc: "Previous / next instrument"},
			{Keys: "F5 / F6 / F7 / F8", Desc: "Play song / pattern / from cursor; stop"},
			{Keys: "Right-click the grid", Desc: "Selection tools: humanise, chords, scale lock, fill"},
			{Keys: "Ctrl+N / Ctrl+O / Ctrl+S", Desc: "New / open / save"},
			{Keys: "Ctrl+Z / Ctrl+Y", Desc: "Undo / redo"},
		},
	},
	{
		Title:  "UI editor",
		Active: editor.UIEditorOpen,
		Binds: []uiwin.Keybind{
			{Keys: "Click", Desc: "Select a widget; Alt+click selects its parent"},
			{Keys: "Drag", Desc: "Move the selection; Alt suspends snapping"},
			{Keys: "Drag a corner handle", Desc: "Resize"},
			{Keys: "Drag an anchor marker", Desc: "Re-anchor without moving the widget"},
			{Keys: "MMB drag", Desc: "Pan the canvas"},
			{Keys: "Wheel", Desc: "Zoom about the cursor"},
			{Keys: "F", Desc: "Frame the canvas"},
			{Keys: "G / B", Desc: "Show / hide the grid, show / hide outlines"},
			{Keys: "Ctrl+D", Desc: "Duplicate the selection"},
			{Keys: "Delete / X", Desc: "Delete the selection"},
			{Keys: "Ctrl+N / Ctrl+O / Ctrl+S", Desc: "New / open / save"},
			{Keys: "Ctrl+Shift+S", Desc: "Save as"},
			{Keys: "Ctrl+Z / Ctrl+Shift+Z", Desc: "Undo / redo"},
		},
	},
	{
		Title:  "Entity editor",
		Active: editor.EntityEditorOpen,
		Binds: []uiwin.Keybind{
			{Keys: "MMB drag / RMB drag", Desc: "Orbit (mouse-look in fly mode)"},
			{Keys: "Shift+MMB drag", Desc: "Pan"},
			{Keys: "Wheel", Desc: "Zoom"},
			{Keys: "V", Desc: "Orbit / fly camera"},
			{Keys: "WASD / Q / E", Desc: "Fly (Shift to go faster)"},
			{Keys: "F", Desc: "Frame the whole type"},
			{Keys: "G / B", Desc: "Show / hide the grid, show / hide the bounds cage"},
			{Keys: "Space", Desc: "Play / pause the attachment animations"},
			{Keys: "Delete / X", Desc: "Remove the selected model, chunk or seat"},
			{Keys: "Ctrl+N / Ctrl+O / Ctrl+S", Desc: "New / open / save the type"},
			{Keys: "Ctrl+Shift+S", Desc: "Save as"},
			{Keys: "Ctrl+Z / Ctrl+Shift+Z", Desc: "Undo / redo"},
		},
	},
	{
		Title:  "Model editor",
		Active: editor.ModelEditorOpen,
		Binds: []uiwin.Keybind{
			{Keys: "1 / 2 / 3 / 4", Desc: "Vertex / edge / face / bone select"},
			{Keys: "G / R / C", Desc: "Move / rotate / scale gizmo (C again: local axes)"},
			{Keys: "E / Shift+E", Desc: "Extrude (again: re-aim along the normal) / always extrude"},
			{Keys: "I", Desc: "Inset — a mitred border round the selected faces"},
			{Keys: "J", Desc: "Join the selected faces into one"},
			{Keys: "M / Shift+M", Desc: "Merge the selected points at their centre / on the first picked"},
			{Keys: "X", Desc: "Dissolve the selected points out of the lines they sit on"},
			{Keys: "V", Desc: "Toggle orbit and fly camera"},
			{Keys: "Z", Desc: "Toggle wireframe"},
			{Keys: "F", Desc: "Frame the model"},
			{Keys: "P", Desc: "Toggle pose mode"},
			{Keys: "Delete", Desc: "Delete the selected bones or faces"},
			{Keys: "Ctrl+Z / Ctrl+Y", Desc: "Undo / redo"},
			{Keys: "Tab", Desc: "Hand focus back to the main window"},
		},
	},
	{
		Title:  "Model editor — pose mode",
		Active: func() bool { return editor.ModelEditorOpen() && editor.ModelPoseMode },
		Binds: []uiwin.Keybind{
			{Keys: "I", Desc: "Key every posed bone at the playhead"},
			{Keys: "Space", Desc: "Play / pause"},
			{Keys: "Left / Right", Desc: "Step one frame"},
			{Keys: "Delete", Desc: "Delete the selected keyframes"},
			{Keys: "Alt+G / Alt+R / Alt+S", Desc: "Clear translation / rotation / scale"},
			{Keys: "G / R / C", Desc: "Pose the selected bones (scale runs on the bone's own axes)"},
			{Keys: "Click a diamond", Desc: "Select that keyframe (Ctrl to add)"},
			{Keys: "Click / drag the timeline", Desc: "Scrub the playhead"},
		},
	},
	{
		Title: "Global",
		Binds: []uiwin.Keybind{
			{Keys: "F1", Desc: "Toggle this reference: keybindings, events, the stdlib, properties"},
			{Keys: "F3", Desc: "Toggle debug overlay (FPS, memory, counts)"},
			{Keys: "F4", Desc: "Cycle volumetrics: half resolution, full resolution, off"},
			{Keys: "F10", Desc: "Toggle anti-aliasing"},
			{Keys: "F11", Desc: "Toggle fullscreen"},
			{Keys: "F12", Desc: "Toggle vsync"},
			{Keys: "~ / `", Desc: "Toggle the level editor"},
			{Keys: "Esc", Desc: "Close this reference"},
		},
	},
	{
		Title: "Biome lighting cost switches",
		Binds: []uiwin.Keybind{
			{Keys: "F5", Desc: "Biome scatter receives shadows on/off"},
			{Keys: "F6", Desc: "Rock / moss shadow casting on/off"},
			{Keys: "F7", Desc: "Foliage shadow casting on/off"},
			{Keys: "F8", Desc: "Cycle how many cascades scatter casts into"},
			{Keys: "F9", Desc: "Cycle how many LOD tiers cast real shadows"},
		},
	},
	{
		Title:  "Gameplay",
		Active: func() bool { return !editor.Editor.Active },
		Binds: []uiwin.Keybind{
			{Keys: "Settings screen", Desc: "Every key here but 0 / 8 / 9, and in Driving, is a default a player can rebind (bind_key)"},
			{Keys: "W / A / S / D", Desc: "Move"},
			{Keys: "Space", Desc: "Jump (gravity on) / fly up (gravity off)"},
			{Keys: "Left Shift", Desc: "Sprint toggle (gravity on) / fly down (gravity off)"},
			{Keys: "Arrow keys", Desc: "Turn / look"},
			{Keys: "Mouse", Desc: "Look (cursor is captured)"},
			{Keys: "E", Desc: "Sit / stand / get out of a vehicle, else interact (fires :interact:)"},
			{Keys: "Left / Right mouse", Desc: "Primary / secondary fire (fires :primary_fire:/:secondary_fire: and their _end pair)"},
			{Keys: "G", Desc: "Throw (fires :throw:)"},
			{Keys: "1 – 5", Desc: "Weapon slot (fires :weapon_slot: with the number pressed)"},
			{Keys: "Mouse wheel / Q", Desc: "Previous / next weapon (fires :weapon_cycle:; not while seated)"},
			{Keys: "R", Desc: "Reload (fires :reload:)"},
			{Keys: "C / Left Ctrl (hold)", Desc: "Crouch"},
			{Keys: "0", Desc: "Toggle gravity (fly mode)"},
			{Keys: "8", Desc: "Toggle entity-vs-world collision (engine-wide)"},
			{Keys: "9", Desc: "Toggle entity-vs-entity collision (engine-wide)"},
		},
	},
	{
		Title:  "Driving",
		Active: func() bool { return !editor.Editor.Active },
		Binds: []uiwin.Keybind{
			{Keys: "E", Desc: "Take a seat within reach; press again to stand up or get out"},
			{Keys: "W / S", Desc: "Throttle forward / back"},
			{Keys: "A / D", Desc: "Steer"},
			{Keys: "Space", Desc: "Brake"},
			{Keys: "Q / E", Desc: "Roll (air and space)"},
			{Keys: "L Shift / L Ctrl", Desc: "Pitch nose down / up (air and space)"},
			{Keys: "Mouse", Desc: "Look around from the seat, within its limits"},
		},
	},
	{
		Title:  "Gamepad",
		Active: func() bool { return !editor.Editor.Active },
		Binds: []uiwin.Keybind{
			{Keys: "Settings screen", Desc: "Every button here but Start is a default a player can rebind (bind_key slot 3)"},
			{Keys: "Left stick", Desc: "Move / throttle and steer, as far as it is pushed; pressed in: sprint"},
			{Keys: "Right stick", Desc: "Look (rate, curve, sensitivity, aim assist — @level.prop.pad_*)"},
			{Keys: "RT / LT", Desc: "Primary / secondary fire"},
			{Keys: "A / B / X / Y", Desc: "Jump (brake) / crouch / interact / next weapon"},
			{Keys: "RB / LB", Desc: "Reload / throw on foot; roll right / left in the air"},
			{Keys: "D-pad", Desc: "Weapons 1–4 (up, right, down, left)"},
			{Keys: "Start", Desc: "Pause menu; closes a menu, as Escape does"},
			{Keys: "In a menu", Desc: "D-pad or left stick moves focus, A presses, B goes back"},
		},
	},
	{
		Title:  "Editor — any mode",
		Active: func() bool { return editor.Editor.Active },
		Binds: []uiwin.Keybind{
			{Keys: "Ctrl+Z", Desc: "Undo"},
			{Keys: "Ctrl+Y / Ctrl+Shift+Z", Desc: "Redo"},
			{Keys: "Delete", Desc: "Delete the current selection"},
		},
	},
	{
		Title:  "Editor — 3D view",
		Active: func() bool { return editor.Editor.Active && editor.Editor.Mode == editor.Mode3D },
		Binds: []uiwin.Keybind{
			{Keys: "Tab", Desc: "Object mode / edit mode"},
			{Keys: "1 / 2 / 3", Desc: "Point / edge / face select (enters edit mode)"},
			{Keys: "Z", Desc: "Toggle wireframe"},
			{Keys: "B", Desc: "Toggle the bounding-box view"},
			{Keys: "V", Desc: "Toggle orbit and fly camera"},
			{Keys: "F", Desc: "Frame the selection"},
			{Keys: "T", Desc: "Terrain tool: mine, build and paint biomes on chunks' block terrain"},
			{Keys: "W / A / S / D", Desc: "Fly the camera (orbit: slide the pivot)"},
			{Keys: "Space / Left Shift", Desc: "Up / down"},
			{Keys: "Caps Lock", Desc: "Fly 4x faster while it is on"},
			{Keys: "Right-drag / Middle-drag", Desc: "Look around, or orbit the pivot"},
			{Keys: "Shift+Middle-drag", Desc: "Pan"},
			{Keys: "Mouse wheel", Desc: "Dolly in fly, zoom the orbit radius"},
			{Keys: "Left click", Desc: "Object mode: pick up a brush. Edit mode: pick its point/edge/face"},
			{Keys: "Left click (handle)", Desc: "Pick a point/edge; repeat clicks cycle overlaps"},
			{Keys: "Ctrl+Left click", Desc: "Add to the selection (brushes, faces, points, edges)"},
			{Keys: "Left click (armed)", Desc: "Place the spawn point ahead of the camera"},
			{Keys: "Drag gizmo arm", Desc: "Translate the selection along that axis"},
		},
	},
	{
		Title:  "Editor — scenery brush (3D)",
		Active: func() bool { return editor.Editor.Active && editor.Editor.BrushActive },
		Binds: []uiwin.Keybind{
			{Keys: "Left click", Desc: "Place a copy where the ghost is; the brush stays armed"},
			{Keys: "[ / ]", Desc: "Turn the ghost by 15 degrees (5 with Shift)"},
			{Keys: "- / =", Desc: "Shrink / grow the ghost"},
			{Keys: "Esc", Desc: "Put the brush away"},
			{Keys: "Ctrl+Z", Desc: "Take back the last copy placed"},
		},
	},
	{
		Title:  "Editor — terrain tool (3D)",
		Active: func() bool { return editor.Editor.Active && editor.TerrainToolActive() },
		Binds: []uiwin.Keybind{
			{Keys: "Left click", Desc: "Mine, place or re-texture a block, or paint a biome; one undo step per stroke"},
			{Keys: "Hold left button", Desc: "Keep going: a block at a time, or brushing biome paint"},
			{Keys: "Shift+click", Desc: "The opposite mode for this stroke: place/mine, erase/paint"},
			{Keys: "1 - 6", Desc: "Mine, Place, Material, Paint Biome, Erase Biome, Volume"},
			{Keys: "Volume: drag a face", Desc: "Size the box a block at a time"},
			{Keys: "Volume: Enter / Shift+Enter", Desc: "Fill the box with the material / carve it out"},
			{Keys: "[ / ]", Desc: "Reach down / up (0 is one block or face)"},
			{Keys: ", / .", Desc: "Previous / next material (biome while painting)"},
			{Keys: "N", Desc: "Show the terrain as blocks or as a smooth surface"},
			{Keys: "T / Esc", Desc: "Put the tool away"},
		},
	},
	{
		Title: "Editor — shaping a brush (edit mode)",
		Active: func() bool {
			return editor.Editor.Active && editor.Editor.Mode == editor.Mode3D &&
				editor.EditorInEditMode()
		},
		Binds: []uiwin.Keybind{
			{Keys: "E", Desc: "Extrude the selected faces along their normals (Shift+E pulls in)"},
			{Keys: "I", Desc: "Inset the selected faces (Shift+I widens them)"},
			{Keys: "B", Desc: "Subdivide the selected faces into quadrants"},
			{Keys: "J", Desc: "Join the selected faces into one (they must lie flat together)"},
			{Keys: "M / Shift+M", Desc: "Merge the selected points at their centre / on the first picked"},
			{Keys: "X", Desc: "Dissolve the selected points out of the lines they sit on"},
			{Keys: "Delete", Desc: "Delete the selected faces' brushes"},
			{Keys: "Tab", Desc: "Back to object mode, with this brush selected"},
		},
	},
	{
		Title: "Editor — placing a brush (object mode)",
		Active: func() bool {
			return editor.Editor.Active && editor.Editor.Mode == editor.Mode3D &&
				!editor.EditorInEditMode() && !editor.EditorInDetailMode()
		},
		Binds: []uiwin.Keybind{
			{Keys: "Left click", Desc: "Pick up a whole brush (Ctrl to add another)"},
			{Keys: "Ctrl+Shift+click", Desc: "Shape that brush's detail, leaving its collision alone"},
			{Keys: "G / R / C", Desc: "Move / turn / scale all of it"},
			{Keys: "Delete", Desc: "Delete the selected brushes"},
			{Keys: "Tab", Desc: "Into edit mode, to reshape the selected brush"},
		},
	},
	{
		Title: "Editor — shaping a surface (detail mode)",
		Active: func() bool {
			return editor.Editor.Active && editor.Editor.Mode == editor.Mode3D &&
				editor.EditorInDetailMode()
		},
		Binds: []uiwin.Keybind{
			{Keys: "E", Desc: "Extrude the selected faces, walling in the sides (Shift+E pushes in)"},
			{Keys: "I", Desc: "Inset — a mitred border, leaving the middle selected"},
			{Keys: "B", Desc: "Subdivide the selected faces into quadrants"},
			{Keys: "Shift+B", Desc: "Smooth the whole detail mesh (Catmull-Clark)"},
			{Keys: "J", Desc: "Join the selected faces into one (they must lie flat together)"},
			{Keys: "M / Shift+M", Desc: "Merge the selected points at their centre / on the first picked"},
			{Keys: "X", Desc: "With points selected: dissolve them out of their lines"},
			{Keys: "Delete / X", Desc: "Delete the selected detail faces"},
			{Keys: "1 / 2 / 3", Desc: "Select points, edges or faces of the detail mesh"},
			{Keys: "G / R / C", Desc: "Move / turn / scale the selection"},
			{Keys: "Ctrl+Shift+click", Desc: "Detail another brush, or the same one again to leave"},
			{Keys: "Tab / Esc", Desc: "Back to object mode, with this brush selected"},
		},
	},
}
