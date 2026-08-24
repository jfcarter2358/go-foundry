package main

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
			{Keys: "Esc", Desc: "Close the window"},
		},
	},
	{
		Title:  "Script editor",
		Active: editor.ScriptEditorOpen,
		Binds: []uiwin.Keybind{
			{Keys: "Ctrl+S / Ctrl+Shift+S", Desc: "Save / save as"},
			{Keys: "Ctrl+O", Desc: "Open a script"},
			{Keys: "Ctrl+N", Desc: "New script"},
			{Keys: "Ctrl+R", Desc: "Reload every script from disk"},
			{Keys: "Esc", Desc: "Close the window (the buffer is kept)"},
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
			{Keys: "F1", Desc: "Toggle this keybinding reference"},
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
			{Keys: "E", Desc: "Interact with whatever you are standing in (fires :interact:)"},
			{Keys: "0", Desc: "Toggle gravity (fly mode)"},
			{Keys: "8", Desc: "Toggle entity-vs-world collision (engine-wide)"},
			{Keys: "9", Desc: "Toggle entity-vs-entity collision (engine-wide)"},
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
		Title:  "Editor — 2D surface editor",
		Active: func() bool { return editor.Editor.Active && editor.Editor.Mode == editor.ModeSurface2D },
		Binds: []uiwin.Keybind{
			{Keys: "Middle-drag", Desc: "Pan"},
			{Keys: "Mouse wheel", Desc: "Zoom"},
			{Keys: "Left click / drag", Desc: "Select and move a point"},
			{Keys: "Shift+Left click", Desc: "Place a point (starts an extrusion if idle)"},
			{Keys: "Right click", Desc: "Delete the nearest terrain point"},
			{Keys: "Enter", Desc: "Finalize the pending extrusion"},
			{Keys: "Delete / Backspace", Desc: "Drop the last pending point"},
			{Keys: "Esc", Desc: "Cancel placement, else leave the surface editor"},
		},
	},
	{
		Title:  "Editor — extrusion placement",
		Active: func() bool { return editor.Editor.Active && editor.Editor.ExtrusionMode },
		Binds: []uiwin.Keybind{
			{Keys: "Left click", Desc: "Place a polygon point on the wall plane"},
			{Keys: "Tab", Desc: "Switch between the outer and inner polygon"},
			{Keys: "Enter", Desc: "Finalize the extrusion"},
			{Keys: "Esc", Desc: "Cancel"},
		},
	},
}
