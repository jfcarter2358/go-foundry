package main

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

func init() { keybindSections = keybindSectionTable }

var keybindSectionTable = []KeybindSection{
	{
		Title:  "Script log",
		Active: LogWindowOpen,
		Binds: []Keybind{
			{"Space", "Pause / resume the view (the log keeps filling)"},
			{"Ctrl+L", "Clear the buffer"},
			{"Ctrl+F", "Jump to the filter box"},
			{"Esc", "Close the window"},
		},
	},
	{
		Title:  "Script editor",
		Active: ScriptEditorOpen,
		Binds: []Keybind{
			{"Ctrl+S / Ctrl+Shift+S", "Save / save as"},
			{"Ctrl+O", "Open a script"},
			{"Ctrl+N", "New script"},
			{"Ctrl+R", "Reload every script from disk"},
			{"Esc", "Close the window (the buffer is kept)"},
		},
	},
	{
		Title:  "Texture editor",
		Active: TextureEditorOpen,
		Binds: []Keybind{
			{"Ctrl+Z / Ctrl+Y", "Undo / redo (Ctrl+Shift+Z redoes too)"},
			{"Left click / drag", "Paint with the current tool"},
			{"Middle-drag", "Pan the canvas"},
			{"Mouse wheel", "Zoom the canvas"},
			{"Click a swatch", "Set the primary colour"},
			{"Right-click a swatch", "Set the secondary colour"},
			{"Double-click a swatch", "Edit that palette slot's colour"},
		},
	},
	{
		Title:  "Model editor",
		Active: ModelEditorOpen,
		Binds: []Keybind{
			{"1 / 2 / 3 / 4", "Vertex / edge / face / bone select"},
			{"G / R / C", "Move / rotate / scale gizmo (C again: local axes)"},
			{"E / Shift+E", "Extrude (again: re-aim along the normal) / always extrude"},
			{"V", "Toggle orbit and fly camera"},
			{"Z", "Toggle wireframe"},
			{"F", "Frame the model"},
			{"P", "Toggle pose mode"},
			{"Delete", "Delete the selected bones or faces"},
			{"Ctrl+Z / Ctrl+Y", "Undo / redo"},
			{"Tab", "Hand focus back to the main window"},
		},
	},
	{
		Title:  "Model editor — pose mode",
		Active: func() bool { return ModelEditorOpen() && modelPoseMode },
		Binds: []Keybind{
			{"I", "Key every posed bone at the playhead"},
			{"Space", "Play / pause"},
			{"Left / Right", "Step one frame"},
			{"Delete", "Delete the selected keyframes"},
			{"Alt+G / Alt+R / Alt+S", "Clear translation / rotation / scale"},
			{"G / R / C", "Pose the selected bones (scale runs on the bone's own axes)"},
			{"Click a diamond", "Select that keyframe (Ctrl to add)"},
			{"Click / drag the timeline", "Scrub the playhead"},
		},
	},
	{
		Title: "Global",
		Binds: []Keybind{
			{"F1", "Toggle this keybinding reference"},
			{"F3", "Toggle debug overlay (FPS, memory, counts)"},
			{"F10", "Toggle anti-aliasing"},
			{"F11", "Toggle fullscreen"},
			{"~ / `", "Toggle the level editor"},
			{"Esc", "Close this reference"},
		},
	},
	{
		Title: "Biome lighting cost switches",
		Binds: []Keybind{
			{"F5", "Biome scatter receives shadows on/off"},
			{"F6", "Rock / moss shadow casting on/off"},
			{"F7", "Foliage shadow casting on/off"},
			{"F8", "Cycle how many cascades scatter casts into"},
			{"F9", "Cycle how many LOD tiers cast real shadows"},
		},
	},
	{
		Title:  "Gameplay",
		Active: func() bool { return !Editor.Active },
		Binds: []Keybind{
			{"W / A / S / D", "Move"},
			{"Space", "Jump (gravity on) / fly up (gravity off)"},
			{"Left Shift", "Fly down (gravity off)"},
			{"Arrow keys", "Turn / look"},
			{"Mouse", "Look (cursor is captured)"},
			{"E", "Interact with whatever you are standing in (fires :interact:)"},
			{"0", "Toggle gravity (fly mode)"},
			{"8", "Toggle entity-vs-world collision (engine-wide)"},
			{"9", "Toggle entity-vs-entity collision (engine-wide)"},
		},
	},
	{
		Title:  "Editor — any mode",
		Active: func() bool { return Editor.Active },
		Binds: []Keybind{
			{"Ctrl+Z", "Undo"},
			{"Ctrl+Y / Ctrl+Shift+Z", "Redo"},
			{"Delete", "Delete the current selection"},
		},
	},
	{
		Title:  "Editor — 3D view",
		Active: func() bool { return Editor.Active && Editor.Mode == Mode3D },
		Binds: []Keybind{
			{"1 / 2 / 3", "Point / edge / face select"},
			{"Z", "Toggle wireframe"},
			{"V", "Toggle orbit and fly camera"},
			{"F", "Frame the selection"},
			{"W / A / S / D", "Fly the camera (orbit: slide the pivot)"},
			{"Space / Left Shift", "Up / down"},
			{"Right-drag / Middle-drag", "Look around, or orbit the pivot"},
			{"Shift+Middle-drag", "Pan"},
			{"Mouse wheel", "Dolly in fly, zoom the orbit radius"},
			{"Left click", "Select whatever the current mode picks"},
			{"Left click (handle)", "Pick a point/edge; repeat clicks cycle overlaps"},
			{"Ctrl+Left click", "Add every point/edge under the cursor"},
			{"Left click (armed)", "Place the spawn point ahead of the camera"},
			{"Drag gizmo arm", "Translate the selection along that axis"},
		},
	},
	{
		Title: "Editor — 2D sector map (its own window)",
		// The map only exists in its own window, and Editor.Mode says Mode2D
		// only inside that window's draw brackets — never while the reference
		// is being rendered — so the section keys off the window instead.
		Active: func() bool { return Editor.Active && Editor2DPoppedOut() },
		Binds: []Keybind{
			{"W / A / S / D", "Pan the map"},
			{"Middle-drag", "Pan the map"},
			{"Mouse wheel", "Zoom"},
			{"Left click / drag", "Select and move a vertex"},
			{"Shift+Left click", "Split the nearest edge, else drop a free point"},
			{"Ctrl+Left click", "Connect two points"},
			{"Right click", "Remove the point under the cursor, else deselect"},
		},
	},
	{
		Title:  "Editor — biome brush (2D)",
		Active: func() bool { return Editor.Active && Editor2DPoppedOut() && Editor.BiomeLayerActive },
		Binds: []Keybind{
			{"Left click", "Add a polygon point; click the first point to close"},
			{"Right click", "Undo the last point while drawing, else delete a sector"},
			{"Enter", "Finish the polygon"},
			{"Esc", "Cancel the polygon"},
		},
	},
	{
		Title:  "Editor — 2D surface editor",
		Active: func() bool { return Editor.Active && Editor.Mode == ModeSurface2D },
		Binds: []Keybind{
			{"Middle-drag", "Pan"},
			{"Mouse wheel", "Zoom"},
			{"Left click / drag", "Select and move a point"},
			{"Shift+Left click", "Place a point (starts an extrusion if idle)"},
			{"Right click", "Delete the nearest terrain point"},
			{"Enter", "Finalize the pending extrusion"},
			{"Delete / Backspace", "Drop the last pending point"},
			{"Esc", "Cancel placement, else leave the surface editor"},
		},
	},
	{
		Title:  "Editor — extrusion placement",
		Active: func() bool { return Editor.Active && Editor.ExtrusionMode },
		Binds: []Keybind{
			{"Left click", "Place a polygon point on the wall plane"},
			{"Tab", "Switch between the outer and inner polygon"},
			{"Enter", "Finalize the extrusion"},
			{"Esc", "Cancel"},
		},
	},
}
