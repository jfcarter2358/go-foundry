package editor

// editor_volume_panel.go — the Volumes section of the editor's side panel.
//
// Structurally this is the entity panel with the entity-specific parts
// removed: an Add-menu entry and the selected volume's properties. What is
// left after taking out definitions, models, animations and collision flags is
// a transform, a shape, and the tags/attributes/script every scriptable object
// shares — which is most of the point of volumes being their own type.
//
// The level's volume list is in the Explorer (editor_panel_explorer.go), with
// everything else the level contains.

import (
	"fmt"

	"foundry/internal/imguibackend"
	"foundry/internal/levelio"
	"foundry/internal/render"
	"foundry/internal/world"
	"strconv"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorVolumePropertiesSection draws the selected volume's fields. Nothing is
// drawn when no volume is selected, so the caller can call it unconditionally.
func editorVolumePropertiesSection() {
	v := activeVolume()
	if v == nil {
		return
	}
	imgui.Separator()
	editorFocusOpen(panelFocusVolume)
	if !imgui.CollapsingHeaderV("Volume: "+v.ID+"##volhdr", imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	editorFocusScroll(panelFocusVolume)
	editorVolumeProperties(v)
}

// editorVolumeAddMenu draws the volume half of the Explorer's Add menu: the ID
// to give the next one, and the row that arms placement.
func editorVolumeAddMenu() {
	imgui.SetNextItemWidth(160)
	imgui.InputTextV("ID##newvol", &Editor.NewVolumeID, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Leave empty to name it \"volume\".\nMade unique on placement.")
	}
	imgui.Separator()
	if imgui.MenuItem("Place By Clicking") {
		editorArmVolumePlacement()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Regions a script reacts to. Never solid, and not drawn\nduring play unless given fog to hold.")
	}
}

// editorVolumeProperties draws the selected volume's editable state.
func editorVolumeProperties(v *world.Volume) {
	editorVolumeCarrierNote(v)
	// The same groups an entity gets, in the same order, so the two blocks read
	// as one panel with different middles.
	editorPropGroupOpen("Identity##volidgrp", func() { editorVolumeIdentity(v) })
	editorPropGroupOpen("Transform##voltfgrp", func() { editorVolumeTransform(v) })
	editorPropGroupOpen("Shape##volshapegrp", func() { editorVolumeShape(v) })
	editorPropGroup("Fog##volfoggrp", 0, func() { editorVolumeFog(v) })
	editorPropGroup("Cloud##volcloudgrp", 0, func() { editorVolumeCloud(v) })
	editorPropGroup("Ambient (dark interior)##volambgrp", 0, func() { editorVolumeAmbient(v) })
	editorPropGroup("Reflection capture##volreflgrp", 0, func() { editorVolumeReflection(v) })
	editorPropGroup("Gravity##volgravgrp", 0, func() { editorVolumeGravity(v) })
	editorPropGroup("Scatter##volscattergrp", 0, func() { editorVolumeScatter(v) })
	editorPropGroup("Frame Doorway##voldoorgrp", 0, func() { editorVolumeDoorway(v) })
	editorPropGroupOpen("Level Exit##volexitgrp", func() { editorVolumeLevelExit(v) })
	editorPropGroupOpen("Checkpoint##volckgrp", func() { editorVolumeCheckpoint(v) })
	editorPropGroup("AI Objective##volobjgrp", 0, func() { editorVolumeObjective(v) })
	editorPropGroup("Assistant Context##volctxgrp", 0, func() { editorVolumeContext(v) })
	editorPropGroup("Environment Region##volreggrp", 0, func() { editorVolumeRegion(v) })
	editorObjectWaypointsUI(v)
	editorScriptDataUI(&v.ScriptData, "vol", strconv.Quote(v.ID))
	editorPropGroup("Actions##volactions", 0, func() { editorVolumeActions(v) })
}

// editorVolumeCarrierNote says, first, that a volume an entity's type hung on it
// belongs to that type — editorEmitterCarrierNote's twin, for its reason: the
// volume is an ordinary one in every other respect, so nothing else on screen
// would warn an author that what they change here is not kept.
func editorVolumeCarrierNote(v *world.Volume) {
	if v.FromEntity == "" {
		return
	}
	editorTextWrappedColored(imgui.Vec4{X: 1, Y: 0.8, Z: 0.35, W: 1},
		"Carried by entity "+v.FromEntity+". Its script, tags, attributes and "+
			"params are saved as that placement's own; its shape and placement "+
			"belong to the entity's type and are put back from it every time the "+
			"level loads.")
	if ent := world.FindEntity(Editor.level, v.FromEntity); ent != nil && ent.Type != "" {
		if imgui.Button("Edit Type##volcarrier") {
			OpenEntityEditorOn(ent.Type)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Open " + ent.Type + " in the entity editor, where this\nvolume is shaped and placed for good.")
		}
	}
	imgui.Separator()
}

// editorVolumeIdentity draws the ID field and the enabled toggle.
//
// The rename is committed on Enter rather than per keystroke: an ID is what a
// script addresses, and renaming through every intermediate spelling would fire
// a uniqueness check against half-typed names.
func editorVolumeIdentity(v *world.Volume) {
	id := v.ID
	if imgui.InputTextV("ID##volid", &id, imgui.InputTextFlagsEnterReturnsTrue, nil) && id != v.ID {
		lvl := Editor.level
		if id == "" {
			editorSetStatus("A volume needs an ID for a script to address it")
		} else if world.LevelIDTaken(lvl, id) {
			editorSetStatus("%q is already used by something else in this level", id)
		} else {
			EditorPushUndo()
			v.ID = id
		}
	}

	if imgui.Checkbox("Enabled##volenabled", &v.Enabled) {
		EditorPushUndo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A disabled volume contains nothing, so it fires no events.\nScripts switch this to arm and disarm a trigger.")
	}
}

// editorVolumeTransform draws position and rotation. There is no scale — a
// volume's size is its half-extents, edited under Shape.
func editorVolumeTransform(v *world.Volume) {
	editorVolumeAttachRows(v)

	pos := [3]float32{v.Position.X(), v.Position.Y(), v.Position.Z()}
	if editorDragFloat3("Position##volpos", &pos, 0, 0, "%.3f") {
		v.Position = mgl32.Vec3{pos[0], pos[1], pos[2]}
	}
	if !v.Attach.IsZero() && imgui.IsItemHovered() {
		imgui.SetTooltip("Local to " + v.Attach.Describe() + ", not the world.")
	}

	rot := vec3ToDegrees(v.Rotation)
	if editorDragFloat3Step("Rotation (deg)##volrot", &rot, -360, 360, "%.1f", 1) {
		v.Rotation = vec3FromDegrees(rot)
	}
}

// editorVolumeAttachRows draws what the volume rides, if anything: a vehicle's
// frame, an entity, or a bone of one of that entity's models.
//
// They sit inside Transform rather than beside it because they are part of what
// the position below them MEANS: attached, Position and Rotation are the
// carrier's coordinates, and the same three numbers describe a different place.
// The rows are the shared Attach widget, so a volume binds exactly the way a
// sprite, an emitter or a sound does.
//
// Nothing is rebased when the binding changes, the rule `attach` follows: an
// offset authored around the origin is what a region bolted to something wants.
func editorVolumeAttachRows(v *world.Volume) {
	lvl := Editor.level
	if lvl == nil {
		return
	}
	before := v.Attach
	if editorAttachUI(lvl, &v.Attach, "vol") {
		// The widget writes as it goes, so the snapshot has to be taken of the
		// binding being left, not the one arrived at.
		after := v.Attach
		v.Attach = before
		EditorPushUndo()
		v.Attach = after
	}
	if v.Attach.IsZero() {
		editorTextMutedWrapped("Unattached: Position is world-space.")
	} else {
		editorTextMutedWrapped("Rides " + v.Attach.Describe() + "; Position is local to it.")
	}
}

// editorVolumeScatter draws the scatter block: the checkbox that creates and
// destroys it, then what grows and how.
//
// The checkbox is the gesture the fog group uses, deliberately — an author who
// has filled a room with fog should recognise the control that fills one with
// grass. Ticking it off DELETES the scatter; turning it off while keeping the
// settings is the "Enabled" line below, the same distinction fog draws.
//
// The controls are grouped by what they answer: what grows, how much of it,
// how far it is drawn, and where on the ground it will take. A kind that has
// no opinion about a control does not show it — a rock has no bloom, and a
// wildflower has no clumping — because a slider that does nothing is worse
// than a missing one.
func editorVolumeScatter(v *world.Volume) {
	has := v.Scatter != nil
	if imgui.Checkbox("This volume grows things##volscatteron", &has) {
		EditorPushUndo()
		if has {
			sc := world.DefaultVolumeScatter()
			v.Scatter = &sc
			editorSetStatus("Volume %q now grows grass", v.ID)
		} else {
			v.Scatter = nil
			editorSetStatus("Removed the scatter from volume %q", v.ID)
		}
		render.EvictScatterCache(v.ID)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Instanced foliage fills the region, planted on whatever\n" +
			"geometry is underneath it. Cost is bounded by the draw\n" +
			"range, not by how large the volume is.")
	}
	if v.Scatter == nil {
		return
	}
	if v.Attach.Entity != "" {
		editorTextWrappedColored(imgui.Vec4{X: 1, Y: 0.55, Z: 0.3, W: 1},
			"This volume rides an entity, and scatter only grows on the world's ground "+
				"or a vehicle's decks — nothing is drawn while it is attached this way.")
	}
	s := v.Scatter

	on := s.Enabled
	if imgui.Checkbox("Enabled##volscatteren", &on) {
		EditorPushUndo()
		s.Enabled = on
	}

	if imguibackend.BeginCombo("Kind##volscatterkind", world.ScatterKindName(s.Kind)) {
		for i, name := range world.ScatterKindNames {
			kind := int32(i)
			if imguibackend.SelectableV(name, kind == s.Kind, 0, imgui.Vec2{}) && kind != s.Kind {
				EditorPushUndo()
				s.Kind = kind
				if kind == world.ScatterBiome && s.Biome.Preset == "" {
					s.Biome.Preset = defaultVolumeBiome()
				}
				// The slope gate's DEFAULT differs per kind — rock reads fine
				// on a cliff and grass does not — so a kind change moves where
				// the instances can land and the bake has to go.
				render.EvictScatterCache(v.ID)
			}
		}
		imguibackend.EndCombo()
	}

	if s.Kind == world.ScatterBiome {
		editorVolumeBiome(v, s)
		return
	}

	editorDragFloat("Density##volscatterdens", &s.Density, 0, 1, "%.2f")
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How many of the lattice's slots survive. The lattice itself\n" +
			"is fixed per kind, so this thins rather than spreads.")
	}
	rng := world.ScatterRange(s)
	if editorDragFloatStep("Draw Range##volscatterrange", &rng, 1, 400, "%.0f", 1) {
		s.Range = rng
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How far from the CAMERA the foliage is drawn — a ring that\n" +
			"follows you, not a property of the volume's own size.")
	}

	col := [3]float32{s.Color.X(), s.Color.Y(), s.Color.Z()}
	if imgui.ColorEdit3("Tint##volscattercol", &col) {
		s.Color = mgl32.Vec3{col[0], col[1], col[2]}
	}

	editorPropGroup("Size##volscattersize", 0, func() { editorScatterSize(s) })
	editorPropGroup("Ground##volscatterground", 0, func() { editorScatterGround(v, s) })
	editorPropGroup("Edges##volscatteredges", 0, func() { editorScatterEdges(s) })
}

// defaultVolumeBiome is the biome a volume switched to `biome` starts with.
func defaultVolumeBiome() string {
	if world.FoliagePresetByName("forest") != nil {
		return "forest"
	}
	if names := world.FoliagePresetNames(); len(names) > 0 {
		return names[0]
	}
	return ""
}

// editorVolumeBiome draws a `biome` volume's controls: which biome, how
// thickly, its tint and per-kind amounts — a terrain biome slot's — then
// the ground gate and the edge fray. None of the lattice's controls apply:
// each species brings its own size, look and draw distance.
func editorVolumeBiome(v *world.Volume, s *world.VolumeScatter) {
	b := &s.Biome
	if imguibackend.BeginCombo("Biome##volscatterbiome", terrainPresetLabel(b.Preset)) {
		for _, name := range world.FoliagePresetNames() {
			if imguibackend.SelectableV(terrainPresetLabel(name)+"##volbiome"+name, b.Preset == name, 0, imgui.Vec2{}) && b.Preset != name {
				EditorPushUndo()
				b.Preset = name
				// Amounts are kept: a shape the new biome does not grow
				// is simply not asked about.
			}
		}
		imguibackend.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A biome's species grow here as real plant instances, placed\n" +
			"once on the ground under the volume. Trees, shrubs, rocks and\n" +
			"logs draw as Nanite foliage; ground cover keeps its usual tiers.")
	}

	dens := b.EffectiveDensity()
	if imgui.SliderFloatV("Density##volbiomedens", &dens, 0.05, world.MaxFoliageAmount, "%.2fx", 0) {
		b.Density = dens
	}
	if imgui.IsItemActivated() {
		EditorPushUndo()
	}
	tint := b.EffectiveTint()
	col := [3]float32{tint[0], tint[1], tint[2]}
	if imgui.ColorEdit3("Tint##volbiometint", &col) {
		b.Tint = mgl32.Vec3{col[0], col[1], col[2]}
		if b.Tint == (mgl32.Vec3{1, 1, 1}) {
			b.Tint = mgl32.Vec3{}
		}
	}
	if imgui.IsItemActivated() {
		EditorPushUndo()
	}
	editorBiomeAmounts(b)

	editorPropGroup("Ground##volscatterground", 0, func() { editorScatterGround(v, s) })
	editorDragFloat("Edge Fade##volbiomeedge", &s.EdgeFadeDistance, 0, 40, "%.1f")
	if imgui.IsItemHovered() {
		imgui.SetTooltip("World units in from the volume's boundary over which the\n" +
			"plants thin out, frayed by noise so the edge is not a line.\n" +
			"0 is a hard edge.")
	}
}

// editorScatterSize draws the per-kind appearance controls.
func editorScatterSize(s *world.VolumeScatter) {
	switch s.Kind {
	case world.ScatterRock, world.ScatterMoss:
		editorDragFloat("Scale##volscatterrock", &s.RockScale, 0.01, 5, "%.3f")
		if s.Kind == world.ScatterRock {
			sides := s.RockSides
			if imgui.DragIntV("Sides##volscattersides", &sides, 1, 3, 12, "%d", 0) {
				s.RockSides = sides
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip("Equatorial vertices of the rock blob — 2x this many faces.")
			}
		}
		return
	}

	label := "Blade"
	if s.Kind != world.ScatterGrass {
		label = "Stem"
	}
	editorDragFloat(label+" Height##volscatterh", &s.GrassHeight, 0.01, world.MaxStemHeight, "%.3f")
	editorDragFloat(label+" Width##volscatterw", &s.GrassWidth, 0.001, 0.5, "%.4f")

	if s.Kind == world.ScatterGrass {
		clump := s.GrassClump
		if editorDragFloat("Clumping##volscatterclump", &clump, 0, 1, "%.2f") {
			s.GrassClump = world.ClampGrassClump(clump)
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Gathers the even scatter into tufts that read as individual\n" +
				"plants: blades pull in toward a shared root and splay outward.\n" +
				"0 is the plain one-blade-per-slot scatter.")
		}
		return
	}

	editorDragFloat("Head Size##volscatterhead", &s.HeadScale, 0.01, 1, "%.3f")
	if s.Kind != world.ScatterWildflower {
		return
	}

	imgui.Checkbox("Spike Bloom##volscatterspike", &s.BloomSpike)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("An elongated head instead of a squat petal disc.")
	}
	imgui.Checkbox("Bloom Centre##volscattercenter", &s.BloomCenter)
	if s.BloomCenter {
		cc := [3]float32{s.CenterColor.X(), s.CenterColor.Y(), s.CenterColor.Z()}
		if imgui.ColorEdit3("Centre Colour##volscattercc", &cc) {
			s.CenterColor = mgl32.Vec3{cc[0], cc[1], cc[2]}
		}
	}
	imgui.Checkbox("One Colour##volscattersolid", &s.BloomSolidColor)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Use the tint directly rather than picking a random hue per\n" +
			"flower — a deliberate colour instead of a mixed meadow.")
	}
	imgui.Checkbox("Basal Leaves##volscatterleaves", &s.StemLeaves)
	if s.StemLeaves {
		ls := world.ResolvedLeafScale(s.LeafScale)
		if editorDragFloat("Leaf Size##volscatterleafsz", &ls, 0.1, world.MaxWildflowerLeafScale, "%.2f") {
			s.LeafScale = ls
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("A multiplier on leaves already sized as a fraction of the\n" +
				"stem, so 1 keeps them in proportion on any height of flower.")
		}
	}
}

// editorScatterGround draws the slope gate — which parts of the surface under
// the volume will take anything.
//
// Both sliders re-bake the height texture, which is a few thousand ray casts,
// so they are applied on release rather than per drag frame. That is what
// editorDragFloat's own return already reports; what this adds is the eviction.
func editorScatterGround(v *world.Volume, s *world.VolumeScatter) {
	minA, maxA := world.ResolvedMinSlope(s), world.ResolvedMaxSlope(s)
	changed := editorDragFloatStep("Min Slope##volscatterminslope", &minA, 0, 90, "%.0f deg", 1)
	changed = editorDragFloatStep("Max Slope##volscattermaxslope", &maxA, 0, 90, "%.0f deg", 1) || changed
	if changed {
		s.MinSlopeAngle, s.MaxSlopeAngle = minA, maxA
		render.EvictScatterCache(v.ID)
	}
	editorTextMutedWrapped("Degrees from level. Nothing grows outside the range, " +
		"feathered over a few degrees at each end so the limit is not a line " +
		"across the hillside.")

	if imgui.Button("Rebuild Ground##volscatterrebake") {
		render.EvictScatterCache(v.ID)
		editorSetStatus("Rebuilt %q's ground sampling", v.ID)
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Re-reads the geometry under the volume. The editor does this\n" +
			"for you when the volume or the level's brushes change; this is\n" +
			"for the cases it cannot see.")
	}
}

// editorScatterEdges draws the three softening controls, all of which shrink
// instances toward their root rather than fading alpha — the pass draws opaque,
// so a fade would be a hard cut with extra steps.
func editorScatterEdges(s *world.VolumeScatter) {
	editorDragFloat("Range Fade##volscatterrangefade", &s.RangeFadeFraction, 0, 1, "%.2f")
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Fraction of the draw range the camera ring fades over.\n0 is a hard cut-off at the range.")
	}
	editorDragFloat("Edge Fade##volscatteredgefade", &s.EdgeFadeDistance, 0, 20, "%.2f")
	if imgui.IsItemHovered() {
		imgui.SetTooltip("World units in from the VOLUME's own boundary an instance\n" +
			"takes to reach full size. 0 is off; it costs a per-instance\n" +
			"distance query, so it is worth paying for where the edge\n" +
			"will be seen up close.")
	}
	jitter := world.ResolvedBoundaryJitter(s)
	if editorDragFloat("Boundary Jitter##volscatterjitter", &jitter, 0, 20, "%.2f") {
		s.BoundaryJitter = jitter
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Bulges the circular camera-ring and LOD boundaries in and out,\n" +
			"so the transition reads as an organic patch rather than a\n" +
			"circle centred on the player.")
	}
}

// editorVolumeDoorway draws the frame-doorway block: the checkbox that makes
// the volume a boundary, and which frame it opens onto.
//
// A doorway is authored on the MOVING side — it rides the ship, and the ship is
// what knows where its own ramp is — so the row is disabled until the volume has
// a frame to ride. Saying that in place beats letting someone tick the box on a
// world volume and wonder why nothing boards.
func editorVolumeDoorway(v *world.Volume) {
	lvl := Editor.level
	if lvl == nil {
		return
	}
	has := v.Doorway != nil
	frame := v.Frame()
	imguibackend.EditorPushDisabled(frame == "" && !has)
	if imgui.Checkbox("This volume is a way in and out##voldooron", &has) {
		EditorPushUndo()
		if has {
			v.Doorway = &world.FrameDoorway{}
			editorSetStatus("Volume %q is now a doorway onto the world", v.ID)
		} else {
			v.Doorway = nil
			editorSetStatus("Volume %q is no longer a doorway", v.ID)
		}
	}
	imguibackend.EditorPopDisabled()
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Standing in it is being aboard the vehicle above.\n" +
			"Walking out of the vehicle's chunks entirely is leaving.\n" +
			"Make it deeper than one step, or a fast walk passes through it.")
	}
	if frame == "" && !has {
		editorTextMutedWrapped("A doorway rides the vehicle it lets you into, so set this " +
			"volume's Frame under Transform first — and no entity, which would take precedence.")
		return
	}
	if v.Doorway == nil {
		return
	}

	label := v.Doorway.To
	if label == "" {
		label = "world"
	}
	if imguibackend.BeginCombo("Opens onto##voldoorto", label) {
		if imguibackend.SelectableV("world", v.Doorway.To == "", 0, imgui.Vec2{}) && v.Doorway.To != "" {
			EditorPushUndo()
			v.Doorway.To = ""
		}
		for i := range lvl.Entities {
			e := &lvl.Entities[i]
			// A doorway onto the vehicle it is bolted to would be a way from a
			// ship into itself.
			if e.Vehicle == nil || e.ID == frame {
				continue
			}
			if imguibackend.SelectableV(e.ID, v.Doorway.To == e.ID, 0, imgui.Vec2{}) && v.Doorway.To != e.ID {
				EditorPushUndo()
				v.Doorway.To = e.ID
			}
		}
		imguibackend.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The space on the other side. \"world\" is the ground, and is\n" +
			"what a ramp almost always opens onto; naming a vehicle makes\n" +
			"this a dock that only that one can be entered from.")
	}
	if frame != "" && v.Doorway.To == world.FrameID(frame) {
		editorTextWrappedColored(imgui.Vec4{X: 1, Y: 0.55, Z: 0.3, W: 1},
			"This doorway opens onto the vehicle it rides, so it leads nowhere.")
	}
	editorTextMutedWrapped("The two spaces have to be gravity-aligned to cross: while the " +
		"vehicle is rolled or pitched more than a few degrees the doorway seals and reads as solid.")
}

// editorVolumeShape draws the shape combo and the half-extents.
//
// Both rebuild the wireframe cage, which is the only thing here that needs one:
// the cage is built in volume-local space, so moving or rotating a volume needs
// no rebuild at all.
func editorVolumeShape(v *world.Volume) {
	if v.Region != nil {
		// A region's box is fitted to its footprint (editor_volume_region.go):
		// dragging X or Z here scales the footprint to match.
		editorTextMutedWrapped("An environment region: its box is fitted to the footprint, " +
			"and changing X or Z scales the footprint.")
	}
	if imguibackend.BeginCombo("Shape##volshape", world.VolumeShapeName(v.Shape)) {
		for i, name := range world.VolumeShapeNames {
			kind := world.VolumeShape(i)
			if imguibackend.SelectableV(name, kind == v.Shape, 0, imgui.Vec2{}) && kind != v.Shape {
				EditorPushUndo()
				v.Shape = kind
				rebuildActiveVolumeCage()
			}
		}
		imguibackend.EndCombo()
	}

	half := [3]float32{v.HalfX, v.HalfY, v.HalfZ}
	if editorDragFloat3Step("Half-extents##volhalf", &half, 0.01, 1000, "%.3f", 0.05) {
		v.HalfX, v.HalfY, v.HalfZ = half[0], half[1], half[2]
		rebuildActiveVolumeCage()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Half the region's size on each axis — the box the shape is inscribed in.")
	}
}

// editorVolumeLevelExit draws the level this volume sends the player to.
//
// A combo over the levels that actually exist rather than a free-text field:
// the name is resolved at the moment the player walks in, so a typo shows up as
// a trigger that silently does nothing, in play, in whichever room the author
// was not looking at. "(none)" is the ordinary case and comes first.
func editorVolumeLevelExit(v *world.Volume) {
	current := v.LoadLevel
	if current == "" {
		current = "(none)"
	}
	open := imguibackend.BeginCombo("Load Level##volloadlevel", current)
	// Re-read the folder as the list drops down rather than on every frame it
	// is held open — it fills the same cache the Open dialog uses, refreshed at
	// the one moment it could be stale.
	if open && !Editor.VolLevelComboOpen {
		Editor.LevelFiles = levelio.ScanLevelFiles(Editor.LevelsDir)
	}
	Editor.VolLevelComboOpen = open
	if open {
		if imguibackend.SelectableV("(none)", v.LoadLevel == "", 0, imgui.Vec2{}) && v.LoadLevel != "" {
			EditorPushUndo()
			v.LoadLevel = ""
		}
		for _, f := range Editor.LevelFiles {
			name := strings.TrimSuffix(f, levelio.LevelExt)
			if imguibackend.SelectableV(name, name == v.LoadLevel, 0, imgui.Vec2{}) && name != v.LoadLevel {
				EditorPushUndo()
				v.LoadLevel = name
			}
		}
		imguibackend.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Walking the player into this volume loads that level,\n" +
			"covered by the `loading` screen. No script needed.\n" +
			"Only the player triggers it, and only while the volume is enabled.")
	}
	if v.LoadLevel != "" && v.Script != "" {
		imgui.Text("Its script still gets `enter`, before the load starts.")
	}
}

// editorVolumeCheckpoint draws the one switch that makes a volume a
// checkpoint. See world.Volume.Checkpoint.
func editorVolumeCheckpoint(v *world.Volume) {
	on := v.Checkpoint
	if imgui.Checkbox("Checkpoint##volcheckpoint", &on) {
		EditorPushUndo()
		v.Checkpoint = on
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The first time the player walks in, the game autosaves,\n" +
			"and dying offers Retry from here. Only the player triggers it,\n" +
			"only while the volume is enabled, and once per play-through.")
	}
	if v.Checkpoint && v.Script != "" {
		imgui.Text("Its script still gets `enter`; the save is taken after.")
	}
}

// editorVolumeContext draws the block that makes a volume a named place the
// Claude Code panel can be pointed at. See world.VolumeContext.
//
// It changes nothing about what the volume does in the game, which is the first
// thing the panel says: a region placed purely to talk about is a new idea, and
// an author dropping one around a room should not have to wonder whether they
// have just added a trigger to their level.
func editorVolumeContext(v *world.Volume) {
	has := v.Context != nil
	if imgui.Checkbox("Describe this region to Claude##volctxon", &has) {
		EditorPushUndo()
		if has {
			v.Context = &world.VolumeContext{}
		} else {
			v.Context = nil
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Makes this region a place you can name in the Claude Code panel:\n" +
			"\"restyle @" + volumeContextExampleID(v) + "\". It answers with everything inside\n" +
			"and where it is. Nothing about the game changes.")
	}
	if v.Context == nil {
		return
	}
	if v.ID == "" {
		editorTextColored(scriptIssueColor, "Give the volume an ID — that is the name you will type.")
	} else {
		editorTextMuted("Refer to it as @" + v.ID)
	}
	note := v.Context.Note
	imgui.SetNextItemWidth(-1)
	if imgui.InputTextV("##volctxnote", &note, 0, nil) && note != v.Context.Note {
		EditorPushUndo()
		v.Context.Note = note
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Optional: what this place is, in your own words.\n" +
			"\"the crew's mess — lived-in, warm light\". The geometry cannot say it.")
	}
	editorTextMuted("What this place is (optional)")
}

// volumeContextExampleID is the volume's own ID for the tooltip's example, or a
// stand-in while it has none.
func volumeContextExampleID(v *world.Volume) string {
	if v.ID == "" {
		return "mess_hall"
	}
	return v.ID
}

// editorVolumeFog draws the region's fog: the checkbox that creates and destroys
// the block, then what it looks like.
//
// The checkbox is the same gesture the Environment panel's fog group uses, and
// deliberately so — an author who has filled a level with fog should recognise
// the control that fills one room with it. Ticking it off DELETES the fog;
// turning the fog off while keeping the numbers is the "Enabled" line below,
// which is the distinction the level fog draws too.
func editorVolumeFog(v *world.Volume) {
	has := v.Fog != nil
	if imgui.Checkbox("This volume is full of fog##volfogon", &has) {
		EditorPushUndo()
		if has {
			f := world.DefaultVolumeFog()
			// Start it agreeing with the level's fog when there is one, for the
			// reason the level fog starts agreeing with the sky: a bank whose
			// colour fights the air around it is the commonest way for a first
			// attempt to look wrong, and matching is right often enough to be
			// the default rather than a button.
			if lvl := Editor.level; lvl != nil && lvl.Fog != nil {
				f.Color = lvl.Fog.Color
			}
			v.Fog = &f
			editorSetStatus("Volume %q now fills with fog", v.ID)
		} else {
			v.Fog = nil
			editorSetStatus("Removed the fog from volume %q", v.ID)
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The region fills with fog the player can see and walk into.\n" +
			"Thickness follows how far the view travels INSIDE the shape,\n" +
			"so a bank reads as deep along its length and thin across it.")
	}
	if v.Fog == nil {
		return
	}

	f := v.Fog
	on := f.Enabled
	if imgui.Checkbox("Enabled##volfogen", &on) {
		EditorPushUndo()
		f.Enabled = on
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Switches the fog off while keeping these numbers.\nThe volume's own Enabled puts it out too, and disarms the trigger with it.")
	}

	editorSkyColor("Colour##volfogcol", &f.Color)
	if lvl := Editor.level; lvl != nil && lvl.Fog != nil {
		imgui.SameLine()
		if imgui.Button("Match level fog##volfogmatch") {
			EditorPushUndo()
			f.Color = lvl.Fog.Color
			editorSetStatus("Fog colour matched to the level's fog")
		}
	}

	editorDragFloatStep("Density##volfogdens", &f.Density, 0, world.VolumeFogMaxDensity, "%.3f", 0.01)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Per world unit travelled inside the region, so it means the\n" +
			"same thing whatever size the volume is. At 0.15 a 10-unit\n" +
			"crossing is about three-quarters fog.")
	}

	editorDragFloatStep("Max opacity##volfogmax", &f.MaxOpacity, 0, 1, "%.2f", 0.05)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Caps the blend, so a thick bank can still leave what is\nstanding in it as a silhouette rather than erasing it.")
	}

	if !v.Enabled {
		imgui.Text("The volume is disabled, so the fog is not drawn.")
	}
}

// editorVolumeAmbient draws the region's ambient cut (world/volume_ambient.go):
// the checkbox that creates and removes the block, then its numbers — the
// fog group's gesture.
func editorVolumeAmbient(v *world.Volume) {
	has := v.Ambient != nil
	if imgui.Checkbox("Darkens the sky light inside##volambon", &has) {
		EditorPushUndo()
		if has {
			a := world.DefaultVolumeAmbient()
			v.Ambient = &a
			editorSetStatus("Volume %q now darkens the ambient inside it", v.ID)
		} else {
			v.Ambient = nil
			editorSetStatus("Removed the ambient cut from volume %q", v.ID)
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A cave, a slot canyon, a room: surfaces inside take less of the\n" +
			"sky's ambient light — what live GI would find out by itself, for\n" +
			"players who play with GI off.")
	}
	if v.Ambient == nil {
		return
	}
	a := v.Ambient
	on := a.Enabled
	if imgui.Checkbox("Enabled##volamben", &on) {
		EditorPushUndo()
		a.Enabled = on
	}
	editorDragFloatStep("Sky##volambsky", &a.Sky, 0, 2, "%.2f", 0.01)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Multiplies the sky light an upward-facing surface gets deep inside:\n0 none, 1 unchanged.")
	}
	editorDragFloatStep("Ground##volambgnd", &a.Ground, 0, 2, "%.2f", 0.01)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The same for a surface facing down (a roof, an overhang).")
	}
	editorDragFloatStep("Fade (m)##volambfade", &a.Fade, 0, 100, "%.1f", 0.1)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How far in from the volume's walls the cut eases in.\n0 is a hard edge.")
	}
	editorDragFloatStep("Fog##volambfog", &a.Fog, 0, 1, "%.2f", 0.05)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How much the sky-lit haze inside is dimmed with it.")
	}
	editorDragFloatStep("Dust##volambdust", &a.Dust, 0, 1, "%.3f", 0.002)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Extra light-shaft density inside, per metre (the level fog's shafts).")
	}
	a.Clamp()
	if !v.Enabled {
		imgui.Text("The volume is disabled, so the ambient is not cut.")
	}
}

// editorVolumeReflection draws the region's reflection capture
// (world/volume_reflection.go), on the ambient group's terms.
func editorVolumeReflection(v *world.Volume) {
	has := v.Reflection != nil
	if imgui.Checkbox("Captures its own reflection##volreflon", &has) {
		EditorPushUndo()
		if has {
			r := world.DefaultVolumeReflection()
			v.Reflection = &r
			editorSetStatus("Volume %q now captures a reflection", v.ID)
		} else {
			v.Reflection = nil
			editorSetStatus("Removed the reflection capture from volume %q", v.ID)
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Shiny surfaces inside reflect the room, captured from the volume's\n" +
			"centre and projected onto its box, instead of the sky. Size the\n" +
			"box to the room's walls and leave it unrotated. Needs live GI\n" +
			"for the capture; without it the volume reflects the sky.")
	}
	if v.Reflection == nil {
		return
	}
	r := v.Reflection
	on := r.Enabled
	if imgui.Checkbox("Enabled##volreflen", &on) {
		EditorPushUndo()
		r.Enabled = on
	}
	editorDragFloatStep("Intensity##volreflint", &r.Intensity, 0, 4, "%.2f", 0.05)
	editorDragFloatStep("Fade (m)##volreflfade", &r.Fade, 0, 100, "%.1f", 0.1)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How far in from the box's walls its reflection takes over.")
	}
	r.Clamp()
	if !v.Enabled {
		imgui.Text("The volume is disabled, so nothing is captured.")
	}
}

// editorVolumeCloud draws the region's volumetric cloud: the checkbox that
// creates and destroys the block, then what the cloud looks like. The same
// gesture the fog group above it makes, for the same reason.
// editorVolumeGravity draws the region's gravity override.
//
// A checkbox first, for the reason the level's own gravity has one: 0 is the
// most likely value anybody will ever ask this field for — the zero-g chamber —
// so "no value" cannot be spelled the same way.
func editorVolumeGravity(v *world.Volume) {
	has := v.Gravity != nil
	if imgui.Checkbox("This volume sets a gravity##volgravon", &has) {
		EditorPushUndo()
		if has {
			g := float32(0)
			v.Gravity = &g
			editorSetStatus("Volume %q is now zero-g", v.ID)
		} else {
			v.Gravity = nil
			editorSetStatus("Volume %q leaves gravity alone again", v.ID)
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Anything whose centre is inside the region falls at this rate\n" +
			"instead of the level's — the player, entities and vehicles alike.\n" +
			"Only while the volume is enabled, so a script can switch the room's\n" +
			"gravity by arming it. Where two gravity volumes overlap, the smaller wins.")
	}
	if v.Gravity == nil {
		editorTextMutedWrapped(fmt.Sprintf(
			"Leaving gravity alone: bodies in here fall at whatever the level says (%.1f right now).",
			world.LevelGravity(Editor.level)))
		return
	}
	editorDragFloatStep("Gravity##volgravity", v.Gravity, -200, 200, "%.2f", 0.5)
	editorTextMutedWrapped(fmt.Sprintf(
		"Units a second squared, downward. The level's is %.1f; 0 is zero-g and a negative number falls upward.",
		world.LevelGravity(Editor.level)))
}

func editorVolumeCloud(v *world.Volume) {
	has := v.Cloud != nil
	if imgui.Checkbox("This volume is full of cloud##volcloudon", &has) {
		EditorPushUndo()
		if has {
			c := world.DefaultVolumeCloud()
			v.Cloud = &c
			editorSetStatus("Volume %q now fills with cloud", v.ID)
		} else {
			v.Cloud = nil
			editorSetStatus("Removed the cloud from volume %q", v.ID)
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The region fills with ray-marched cloud, lit by the level's sun and sky.\n" +
			"A big flat box over the level is a cloud layer; a sphere is one cumulus.")
	}
	// The opposite of the checkbox above, and independent of it: a region that
	// keeps everyone else's cloud out rather than holding its own.
	clears := v.ClearsClouds
	if imgui.Checkbox("Clears clouds##volcloudclear", &clears) {
		EditorPushUndo()
		v.ClearsClouds = clears
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("No volumetric cloud is drawn inside this region — cloud volumes and the sky deck alike.\n" +
			"Put one in a ship's hold so it stays clear while the ship sits in a cloud bank.\n" +
			"Up to " + strconv.Itoa(world.MaxCloudClearVolumes) + " in view at once, and only while the volume is enabled.")
	}
	if v.Cloud == nil {
		return
	}

	c := v.Cloud
	on := c.Enabled
	if imgui.Checkbox("Enabled##volcloud_en", &on) {
		EditorPushUndo()
		c.Enabled = on
	}
	editorCloudLook("vol", &c.CloudLook)
	editorDragFloatStep("Softness##volcloudsoft", &c.Softness, 0.01, 1, "%.2f", 0.01)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How far in from the region's walls the cloud fades up to full,\n" +
			"as a fraction of the half-extent. Low shows the shape's flat faces.")
	}
	c.Clamp()

	if !v.Enabled {
		imgui.Text("The volume is disabled, so the cloud is not drawn.")
	}
}

// editorVolumeActions draws duplicate and delete.
//
// Both are shut off for a volume an entity carries, on the emitter panel's
// reasoning: a duplicate would be an orphan the next re-materialisation drops,
// and a delete would come back on the next load.
func editorVolumeActions(v *world.Volume) {
	imguibackend.EditorPushDisabled(v.FromEntity != "")
	if imgui.Button("Duplicate##voldup") {
		duplicateActiveVolume()
	}
	imgui.SameLine()
	if imgui.Button("Delete##voldel") {
		deleteActiveVolume()
	}
	imguibackend.EditorPopDisabled()
	if v.FromEntity != "" && imgui.IsItemHovered() {
		imgui.SetTooltip("Entity " + v.FromEntity + " owns this volume. Remove it from\n" +
			"that entity's type instead.")
	}
}
