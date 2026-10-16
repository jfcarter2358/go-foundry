package editor

// editor_generator_water.go — the Generator's WATER BODIES panel for a
// ground_edit stage (docs/terrain-paint/PHASE_W2.md; the stage is
// procgen/stages/ground_edit*.go).
//
// A painted pond's water stands at its basin's spill height unless the
// stage's `bodies` table pins it. Editing that table by hand means knowing
// a point inside each body; this panel lists the bodies the last run carved
// (its water_bodies.yaml) instead, one row each:
//
//	layer (style)   level  [Auto|Lock]   preset   area
//
//   - LEVEL: an automatic body shows its level and spill height; Lock
//     writes a `bodies` entry at the body's anchor (its most inland point,
//     the key that survives repainting the edges) holding the current
//     level, which the row then drags. Auto drops the pin (keeping the
//     entry, marked auto, when it still carries a preset).
//   - PRESET: the water material, from every loaded material with a water
//     effect; "(layer's)" is the water layer's (or its style's) default.
//   - Entries that hold no painted body any more (painted dry) are listed
//     with Remove.
//
// Every change is a document edit (one undo step, the preview rerun) and
// kicks the live rerun like a brush stroke (editor_generator_paint_live.go),
// so with the level open the water moves in the 3D view a moment later. The
// table is reread when the file changes on disk (after each run).

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"foundry/internal/imguibackend"
	"foundry/internal/material"
	"foundry/internal/procgen"
	"foundry/internal/procgen/stages"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// genWaterUI caches the last water_bodies.yaml read.
var genWaterUI struct {
	path   string
	mod    time.Time
	bodies *stages.GroundBodies
	err    error
}

// genWaterBodies is the stage's bodies table from its last run, reread
// when the file changed; nil before any run.
func genWaterBodies(s *procgen.Stage) (*stages.GroundBodies, error) {
	path := filepath.Join(procgen.OutDirFor(genDoc, procgen.Options{}), s.ID, stages.BodiesFile)
	fi, err := os.Stat(path)
	if err != nil {
		genWaterUI.path, genWaterUI.bodies, genWaterUI.err = path, nil, nil
		return nil, nil
	}
	if path != genWaterUI.path || !fi.ModTime().Equal(genWaterUI.mod) {
		genWaterUI.path, genWaterUI.mod = path, fi.ModTime()
		genWaterUI.bodies, genWaterUI.err = stages.ReadGroundBodies(path)
	}
	return genWaterUI.bodies, genWaterUI.err
}

// genWaterMaterials is every loaded material with a water effect.
func genWaterMaterials() []string {
	var out []string
	for _, n := range material.Names() {
		if d := material.Get(n); d != nil && d.Find(material.KindWater) != nil {
			out = append(out, n)
		}
	}
	return out
}

// genWaterEdit applies fn to the stage's bodies list as one document edit.
func genWaterEdit(s *procgen.Stage, fn func(items []procgen.Params) []procgen.Params) {
	GeneratorPushUndo()
	cur := s.Params["bodies"]
	if cur.Type != procgen.ParamList {
		cur = procgen.List()
	}
	cur.Items = fn(cur.Items)
	s.Params["bodies"] = cur
	genAfterEdit()
	genPaintLiveKick()
}

// genStageExtraPanels draws what a stage kind shows above its parameters:
// a ground_edit's water bodies.
func genStageExtraPanels(s *procgen.Stage) {
	if s.Kind == stages.GroundEditKind {
		genWaterBodiesPanel(s)
		genRiversPanel(s) // phase W3 (editor_generator_river.go)
		genPathsPanel(s)  // phase P4 (editor_generator_path.go)
	}
}

// genWaterBodiesPanel draws the panel (file comment) above a ground_edit
// stage's parameters.
func genWaterBodiesPanel(s *procgen.Stage) {
	if !imgui.CollapsingHeaderV("Water bodies##genwater", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	bodies, err := genWaterBodies(s)
	switch {
	case err != nil:
		editorTextWrappedColored(genWarnColor, err.Error())
		return
	case bodies == nil:
		editorTextMutedWrapped("Run the generator to list the water bodies its water layers carve.")
		return
	case len(bodies.Bodies) == 0 && len(bodies.Orphans) == 0:
		editorTextMutedWrapped("No water bodies: paint a water layer this stage lists (the Pond, Lake and Marsh presets do).")
		return
	}
	entries := s.Params["bodies"].Items
	mats := genWaterMaterials()
	flags := imgui.TableFlagsBordersInnerH | imgui.TableFlagsSizingFixedFit
	if imgui.BeginTableV("##genwatertable", 5, flags, imgui.Vec2{}, 0) {
		imgui.TableSetupColumn("Body")
		imgui.TableSetupColumn("Level")
		imgui.TableSetupColumn("")
		imgui.TableSetupColumn("Preset")
		imgui.TableSetupColumn("Area")
		imgui.TableHeadersRow()
		for i, b := range bodies.Bodies {
			imgui.PushID(fmt.Sprintf("body%d", i))
			genWaterRow(s, b, entries, mats)
			imgui.PopID()
		}
		imgui.EndTable()
	}
	for _, o := range bodies.Orphans {
		if o < 0 || o >= len(entries) {
			continue
		}
		at := entries[o].Vec2("at")
		editorTextWrappedColored(genWarnColor, fmt.Sprintf("Entry %d at (%.1f, %.1f) holds no water any more.", o+1, at[0], at[1]))
		imgui.SameLine()
		if imgui.Button(fmt.Sprintf("Remove##orphan%d", o)) {
			idx := o
			genWaterEdit(s, func(items []procgen.Params) []procgen.Params {
				if idx < len(items) {
					return append(items[:idx:idx], items[idx+1:]...)
				}
				return items
			})
		}
	}
}

// genWaterRow is one body's row.
func genWaterRow(s *procgen.Stage, b stages.GroundBody, entries []procgen.Params, mats []string) {
	entry := -1
	if b.Entry >= 0 && b.Entry < len(entries) {
		entry = b.Entry
	}
	imgui.TableNextRow()
	imgui.TableNextColumn()
	imgui.Text(fmt.Sprintf("%s (%s)", b.Layer, b.Style))
	editorMenuTooltip(fmt.Sprintf("Anchored at (%.1f, %.1f); deepest %.2f m; painted %.0f m².", b.Anchor[0], b.Anchor[1],
		b.MaxDepth, b.Painted))

	imgui.TableNextColumn()
	if b.Locked && entry >= 0 {
		lv := float32(entries[entry].Float("level"))
		imgui.SetNextItemWidth(80)
		if imgui.DragFloatV("##level", &lv, 0.01, -10000, 10000, "%.2f m", 0) {
			v := genF32(lv)
			genBeginEdit(fmt.Sprintf("%s/bodies/%d/level", s.ID, entry))
			entries[entry]["level"] = procgen.Float(v)
			genAfterEdit()
		}
		if imgui.IsItemDeactivatedAfterEdit() {
			genPaintLiveKick()
		}
		editorMenuTooltip(fmt.Sprintf("The pinned water level. The rim's lowest ground is %.2f m: above it a berm holds the water.", b.Spill))
	} else {
		imgui.Text(fmt.Sprintf("%.2f m", b.Level))
		editorMenuTooltip(fmt.Sprintf("Automatic: the spill height (%.2f m, the rim's lowest ground) less the freeboard.", b.Spill))
	}

	imgui.TableNextColumn()
	if b.Locked {
		if imgui.Button("Auto") {
			genWaterAuto(s, b)
		}
		editorMenuTooltip("Back to the spill height.")
	} else {
		if imgui.Button("Lock") {
			genWaterLock(s, b)
		}
		editorMenuTooltip("Pin the water at its current level (then drag it). The pin is keyed by a point inside the body,\n" +
			"so repainting its edges keeps it.")
	}

	imgui.TableNextColumn()
	cur := ""
	if entry >= 0 {
		cur = entries[entry].Str("material")
	}
	label := cur
	if label == "" {
		label = "(layer's) " + b.Material
	}
	imgui.SetNextItemWidth(150)
	if imguibackend.BeginCombo("##preset", label) {
		if imguibackend.SelectableV("(layer's)", cur == "", 0, imgui.Vec2{}) && cur != "" {
			genWaterSetMaterial(s, b, "")
		}
		for _, m := range mats {
			if imguibackend.SelectableV(m, m == cur, 0, imgui.Vec2{}) && m != cur {
				genWaterSetMaterial(s, b, m)
			}
		}
		imguibackend.EndCombo()
	}
	editorMenuTooltip("The body's water material (a water preset).")

	imgui.TableNextColumn()
	imgui.Text(genWaterArea(b.Area))
}

// genWaterEntry is the bodies entry a body was matched to, or -1.
func genWaterEntry(s *procgen.Stage, b stages.GroundBody) int {
	if b.Entry >= 0 && b.Entry < len(s.Params["bodies"].Items) {
		return b.Entry
	}
	return -1
}

// genWaterLock pins a body at its current level: its entry, or a new one
// at its anchor.
func genWaterLock(s *procgen.Stage, b stages.GroundBody) {
	entry := genWaterEntry(s, b)
	genWaterEdit(s, func(items []procgen.Params) []procgen.Params {
		if entry >= 0 {
			items[entry]["auto"] = procgen.Bool(false)
			items[entry]["level"] = procgen.Float(b.Level)
			return items
		}
		return append(items, procgen.Params{"at": procgen.Vec2(b.Anchor[0], b.Anchor[1]), "level": procgen.Float(b.Level)})
	})
}

// genWaterAuto unpins a body: its entry goes, or stays marked auto while it
// carries a material.
func genWaterAuto(s *procgen.Stage, b stages.GroundBody) {
	entry := genWaterEntry(s, b)
	if entry < 0 {
		return
	}
	genWaterEdit(s, func(items []procgen.Params) []procgen.Params {
		if items[entry].Str("material") != "" {
			items[entry]["auto"] = procgen.Bool(true)
			return items
		}
		return append(items[:entry:entry], items[entry+1:]...)
	})
}

// genWaterSetMaterial sets a body's own material ("" for its layer's),
// adding an auto entry at its anchor when it has none.
func genWaterSetMaterial(s *procgen.Stage, b stages.GroundBody, name string) {
	entry := genWaterEntry(s, b)
	if entry < 0 && name == "" {
		return
	}
	genWaterEdit(s, func(items []procgen.Params) []procgen.Params {
		if entry >= 0 {
			items[entry]["material"] = procgen.String(name)
			if name == "" && items[entry].Bool("auto") {
				return append(items[:entry:entry], items[entry+1:]...)
			}
			return items
		}
		return append(items, procgen.Params{"at": procgen.Vec2(b.Anchor[0], b.Anchor[1]), "auto": procgen.Bool(true),
			"material": procgen.String(name)})
	})
}

// genWaterArea is an area for the table: m², or hectares when large.
func genWaterArea(a float64) string {
	if a >= 10000 {
		return fmt.Sprintf("%.2f ha", a/10000)
	}
	return fmt.Sprintf("%.0f m²", math.Round(a))
}
