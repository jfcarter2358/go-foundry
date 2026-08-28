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
		Title:  "Model editor",
		Active: editor.ModelEditorOpen,
		Binds: []uiwin.Keybind{
			{Keys: "1 / 2 / 3 / 4", Desc: "Vertex / edge / face / bone select"},
			{Keys: "G / R / C", Desc: "Move / rotate / scale gizmo (C again: local axes)"},
			{Keys: "E / Shift+E", Desc: "Extrude (again: re-aim along the normal) / always extrude"},
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
			{Keys: "F10", Desc: "Toggle anti-aliasing"},
			{Keys: "F11", Desc: "Toggle fullscreen"},
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
			{Keys: "W / A / S / D", Desc: "Move"},
			{Keys: "Space", Desc: "Jump (gravity on) / fly up (gravity off)"},
			{Keys: "Left Shift", Desc: "Fly down (gravity off)"},
			{Keys: "Arrow keys", Desc: "Turn / look"},
			{Keys: "Mouse", Desc: "Look (cursor is captured)"},
			{Keys: "E", Desc: "Sit / stand / get out of a vehicle, else interact (fires :interact:)"},
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
			{Keys: "1 / 2 / 3", Desc: "Point / edge / face select"},
			{Keys: "Z", Desc: "Toggle wireframe"},
			{Keys: "V", Desc: "Toggle orbit and fly camera"},
			{Keys: "F", Desc: "Frame the selection"},
			{Keys: "W / A / S / D", Desc: "Fly the camera (orbit: slide the pivot)"},
			{Keys: "Space / Left Shift", Desc: "Up / down"},
			{Keys: "Right-drag / Middle-drag", Desc: "Look around, or orbit the pivot"},
			{Keys: "Shift+Middle-drag", Desc: "Pan"},
			{Keys: "Mouse wheel", Desc: "Dolly in fly, zoom the orbit radius"},
			{Keys: "Left click", Desc: "Select whatever the current mode picks"},
			{Keys: "Left click (handle)", Desc: "Pick a point/edge; repeat clicks cycle overlaps"},
			{Keys: "Ctrl+Left click", Desc: "Add every point/edge under the cursor"},
			{Keys: "Left click (armed)", Desc: "Place the spawn point ahead of the camera"},
			{Keys: "Drag gizmo arm", Desc: "Translate the selection along that axis"},
		},
	},
	{
		Title: "Editor — 2D sector map (its own window)",
		// The map only exists in its own window, and Editor.Mode says Mode2D
		// only inside that window's draw brackets — never while the reference
		// is being rendered — so the section keys off the window instead.
		Active: func() bool { return editor.Editor.Active && editor.Editor2DPoppedOut() },
		Binds: []uiwin.Keybind{
			{Keys: "W / A / S / D", Desc: "Pan the map"},
			{Keys: "Middle-drag", Desc: "Pan the map"},
			{Keys: "Mouse wheel", Desc: "Zoom"},
			{Keys: "Left click / drag", Desc: "Select and move a vertex"},
			{Keys: "Shift+Left click", Desc: "Split the nearest edge, else drop a free point"},
			{Keys: "Ctrl+Left click", Desc: "Connect two points"},
			{Keys: "Right click", Desc: "Remove the point under the cursor, else deselect"},
		},
	},
	{
		Title: "Editor — biome brush (2D)",
		Active: func() bool {
			return editor.Editor.Active && editor.Editor2DPoppedOut() && editor.Editor.BiomeLayerActive
		},
		Binds: []uiwin.Keybind{
			{Keys: "Left click", Desc: "Add a polygon point; click the first point to close"},
			{Keys: "Right click", Desc: "Undo the last point while drawing, else delete a sector"},
			{Keys: "Enter", Desc: "Finish the polygon"},
			{Keys: "Esc", Desc: "Cancel the polygon"},
		},
	},
	{
		Title:  "Editor — shaping a surface",
		Active: func() bool { return editor.Editor.Active && editor.Editor.SurfaceEditing },
		Binds: []uiwin.Keybind{
			{Keys: "W A S D / Space / Shift", Desc: "Fly, exactly as outside surface mode"},
			{Keys: "1 / 2 / 3", Desc: "Select the surface's points, edges or faces"},
			{Keys: "Left click", Desc: "Pick; Ctrl+click adds to the selection"},
			{Keys: "Drag an arm", Desc: "Move the selection — green is depth, red U, blue V"},
			{Keys: "E", Desc: "Extrude the selected faces, then drag the depth arm"},
			{Keys: "I", Desc: "Inset the selected faces"},
			{Keys: "B", Desc: "Subdivide the selected faces"},
			{Keys: "C", Desc: "Connect two selected points, splitting the face"},
			{Keys: "H", Desc: "Open the selected faces as a hole, or close them"},
			{Keys: "Ctrl+A", Desc: "Select all"},
			{Keys: "Delete", Desc: "Delete the selected faces"},
			{Keys: "Esc", Desc: "Done shaping"},
		},
	},
}
