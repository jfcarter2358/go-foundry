package editor

// editor_ai_panel.go — the AI block, and the objective block, in the panels.
//
// One body draws an entity's controller in both places it is edited: a placed
// entity's AI group in the level editor, and a type's in the entity editor.
// They differ in who is told about an edit — the entity editor marks its
// document dirty per change, the level editor pushes undo only for structural
// changes, as every other field there does — so the body takes that as a
// function and is otherwise the same code.
//
// The panel also owns the one piece of AI that is only ever seen in the editor:
// the navigation overlay. What an agent can reach is otherwise invisible until
// a guard is watched failing to reach it, so the group has a switch that draws
// the grid its body walks on, and the route it would take through its
// objectives.

import (
	"fmt"
	"math"
	"strings"

	"foundry/internal/ai"
	"foundry/internal/gizmo"
	"foundry/internal/nav"
	"foundry/internal/world"

	"foundry/internal/imguibackend"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// aiPanelHost is what the AI body needs from whichever panel it is drawn in.
type aiPanelHost struct {
	// id makes every widget ID unique to the panel.
	id string
	// edited is told about every field change, by a label stable across the
	// frames of one drag.
	edited func(label string)
	// removable says the controller may be taken off. A placed entity whose
	// type gives it one may only switch it off: loading would put it back.
	removable bool
	// animations are the names the entity's attachments can play.
	animations []string
	// paths are the waypoint animations the agent could walk: the level's IDs
	// and the names of the paths its type carries.
	paths []string
	// bounds, collision and gravity say how the body is set up, for the notes
	// about what an agent needs.
	hasBounds, world, gravity bool
}

// editorAISpecUI draws a controller, or the button that adds one.
func editorAISpecUI(spec **world.AISpec, h aiPanelHost) {
	if *spec == nil {
		editorTextWrapped("Decides nothing for itself. An AI controller walks it about the " +
			"level on its own: to where a script sends it, after what it hunts, and " +
			"through the objective volumes it is given.")
		if imgui.Button("Give it an AI controller##aiadd" + h.id) {
			EditorPushUndo()
			s := world.DefaultAISpec()
			*spec = &s
			h.edited("aiadd")
		}
		return
	}
	s := *spec

	if imgui.Checkbox("Enabled##aien"+h.id, &s.Enabled) {
		h.edited("aien")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Off stops the agent where it stands. A script can switch it\nback on with @this.prop.ai_enabled = true.")
	}
	if !h.hasBounds {
		editorTextWrappedColored(aiWarnColor, "No bounding volume: an agent's size comes from its bounds, "+
			"so it is planned as a player-sized body.")
	}
	if !h.world {
		editorTextWrappedColored(aiWarnColor, "World collision is off, so this agent walks its route "+
			"but is not stopped by walls when it strays off it.")
	}
	if !h.gravity {
		editorTextMutedWrapped("No gravity: the agent follows the route's heights by itself, as a hovering drone would.")
	}

	if imgui.TreeNodeV("Movement##aimove"+h.id, imgui.TreeNodeFlagsDefaultOpen) {
		aiDrag("Speed (u/s)", "aispeed", h, &s.Speed, 0, 100, 0.1)
		aiDrag("Acceleration (u/s²)", "aiaccel", h, &s.Accel, 0, 500, 0.5)
		turn := mgl32.RadToDeg(s.TurnRate)
		if editorDragFloatStep("Turn rate (deg/s)##aiturn"+h.id, &turn, 0, 7200, "%.0f", 15) {
			s.TurnRate = mgl32.DegToRad(turn)
			h.edited("aiturn")
		}
		aiDrag("Step height (0 = level's)", "aistep", h, &s.StepHeight, 0, 10, 0.05)
		aiDrag("Arrive radius", "aiarrive", h, &s.ArriveRadius, 0.05, 20, 0.05)
		if imgui.Checkbox("Face the way it walks##aiface"+h.id, &s.FaceMovement) {
			h.edited("aiface")
		}
		imgui.TreePop()
	}

	if imgui.TreeNodeV("Targets##aitgt"+h.id, imgui.TreeNodeFlagsDefaultOpen) {
		editorTextMutedWrapped("What it hunts: player, an entity ID, or #tag.")
		aiNameList(&s.Targets, "aitgtl", h, aiTargetSuggestions(Editor.level))
		if len(s.Targets) > 0 {
			mode := int32(s.OnTarget)
			if imguibackend.Combo("On target##aionT"+h.id, &mode, world.AITargetModeNames) {
				s.OnTarget = world.AITargetMode(mode)
				h.edited("aiont")
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip("chase: close to attack range, then :target_in_range:.\n" +
					"watch: stay put and turn to face it.\nignore: only tell the script.")
			}
			aiDrag("Sight range", "aisight", h, &s.SightRange, 0, 1000, 0.5)
			aiDrag("Field of view (deg)", "aifov", h, &s.FOV, 0, 360, 5)
			if imgui.Checkbox("Needs a clear line of sight##aireq"+h.id, &s.RequireSight) {
				h.edited("aireq")
			}
			aiDrag("Hearing range", "aihear", h, &s.HearingRange, 0, 1000, 0.25)
			if imgui.IsItemHovered() {
				imgui.SetTooltip("Closer than this, a target is noticed whichever way the\nagent faces and whatever is between them.")
			}
			aiDrag("Lose after (s)", "ailose", h, &s.LoseTime, 0, 600, 0.25)
			aiDrag("Attack range", "airange", h, &s.AttackRange, 0, 1000, 0.1)
			aiDrag("Attack interval (s)", "aiint", h, &s.AttackInterval, 0.05, 600, 0.05)
		}
		imgui.TreePop()
	}

	if imgui.TreeNodeV("Objectives##aiobj"+h.id, imgui.TreeNodeFlagsDefaultOpen) {
		editorTextMutedWrapped("Objective volumes it works through: an ID or #tag.")
		aiNameList(&s.Objectives, "aiobjl", h, aiObjectiveSuggestions(Editor.level))
		if len(s.Objectives) > 0 {
			order := int32(s.Order)
			if imguibackend.Combo("Order##aiorder"+h.id, &order, world.AIObjectiveOrderNames) {
				s.Order = world.AIObjectiveOrder(order)
				h.edited("aiorder")
			}
			if imgui.Checkbox("Loop##ailoop"+h.id, &s.Loop) {
				h.edited("ailoop")
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip("Start the list again once every objective on it is done.\nA patrol is a looped sequence.")
			}
			if lvl := Editor.level; lvl != nil {
				if missing := aiMissingObjectives(lvl, s.Objectives); len(missing) > 0 {
					editorTextWrappedColored(aiWarnColor, "Not objectives in this level: "+strings.Join(missing, ", ")+
						". Tick AI Objective on a volume to make it one.")
				}
			}
		}
		imgui.TreePop()
	}

	if imgui.TreeNodeV("Waypoint path##aipath"+h.id, imgui.TreeNodeFlagsDefaultOpen) {
		editorTextMutedWrapped("A waypoint animation to walk stop by stop. Each point's AI group says what to do there: wait, take cover, hold fire.")
		preview := s.Waypoints
		if preview == "" {
			preview = "(none)"
		}
		if imguibackend.BeginCombo("Waypoints##aiwp"+h.id, preview) {
			if imguibackend.Selectable("(none)") {
				EditorPushUndo()
				s.Waypoints = ""
				h.edited("aiwp")
			}
			if len(h.paths) == 0 {
				editorTextMuted("(no waypoint animations)")
			}
			for _, name := range h.paths {
				if imguibackend.SelectableV(name, name == s.Waypoints, 0, imgui.Vec2{}) {
					EditorPushUndo()
					s.Waypoints = name
					h.edited("aiwp")
				}
			}
			imguibackend.EndCombo()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Walked ahead of the objectives, which take over once a path\n" +
				"that does not loop is finished. A type's own path is named by\n" +
				"the name the type gives it.")
		}
		if s.Waypoints != "" {
			if imgui.Checkbox("Loop the path##aiwploop"+h.id, &s.WaypointsLoop) {
				h.edited("aiwploop")
			}
		}
		imgui.TreePop()
	}

	if imgui.TreeNodeV("Leader##aifollow"+h.id, 0) {
		editorTextMutedWrapped("Someone to keep up with, as a squadmate: a place in a loose group behind them, fighting from there until they are the leash away.")
		preview := s.Follow
		if preview == "" {
			preview = "(nobody)"
		}
		if imguibackend.BeginCombo("Follow##aifollowc"+h.id, preview) {
			if imguibackend.Selectable("(nobody)") {
				EditorPushUndo()
				s.Follow = ""
				h.edited("aifollowc")
			}
			for _, name := range aiLeaderSuggestions(Editor.level) {
				if imguibackend.SelectableV(name, name == s.Follow, 0, imgui.Vec2{}) {
					EditorPushUndo()
					s.Follow = name
					h.edited("aifollowc")
				}
			}
			imguibackend.EndCombo()
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("A waypoint path comes first: the agent walks it, then falls in.\n" +
				"A script sets this with ai_follow(@agent, @player).")
		}
		aiDrag("Spacing", "aifollowd", h, &s.FollowDistance, 0.5, 20, 0.1)
		aiDrag("Leash (0 = never regroup)", "aifollowl", h, &s.FollowLeash, 0, 500, 0.5)
		aiDrag("Run speed (catching up)", "airunspd", h, &s.RunSpeed, 0, 100, 0.1)
		if s.RunSpeed > s.Speed {
			aiAnimCombo("Run animation", "airuna", h, &s.RunAnim)
		}
		imgui.TreePop()
	}

	if imgui.TreeNodeV("Idle and animation##aiidle"+h.id, 0) {
		aiDrag("Wander radius (0 = stand)", "aiwander", h, &s.WanderRadius, 0, 1000, 0.5)
		if s.WanderRadius > 0 {
			aiDrag("Wander pause (s)", "aipause", h, &s.WanderPause, 0, 600, 0.25)
		}
		aiAnimCombo("Idle animation", "aiidlea", h, &s.IdleAnim)
		aiAnimCombo("Move animation", "aimovea", h, &s.MoveAnim)
		imgui.TreePop()
	}

	imgui.Checkbox("Show navigation##ainav"+h.id, &Editor.ShowNavigation)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Draw where a body this size can walk, and the route it would\n" +
			"take through its objectives. Rebuilt as the level changes.")
	}

	if h.removable {
		if imgui.Button("Remove AI controller##airm" + h.id) {
			EditorPushUndo()
			*spec = nil
			h.edited("airm")
		}
	} else {
		editorTextMutedWrapped("The type gives this entity its controller. Untick Enabled to switch it off here.")
	}
}

var aiWarnColor = imgui.Vec4{X: 1, Y: 0.8, Z: 0.35, W: 1}

func aiDrag(label, id string, h aiPanelHost, v *float32, lo, hi, step float32) {
	if editorDragFloatStep(label+"##"+id+h.id, v, lo, hi, "%.2f", step) {
		h.edited(id)
	}
}

// aiNameList edits a list of names, with a free-text add row and a combo of
// the names the level offers.
func aiNameList(list *[]string, id string, h aiPanelHost, suggestions []string) {
	remove := -1
	for i := range *list {
		name := (*list)[i]
		imgui.Text(name)
		imgui.SameLine()
		if imgui.Button(fmt.Sprintf("x##%s%s%d", id, h.id, i)) {
			remove = i
		}
	}
	if remove >= 0 {
		EditorPushUndo()
		*list = append((*list)[:remove:remove], (*list)[remove+1:]...)
		h.edited(id + "rm")
		return
	}
	if len(*list) == 0 {
		editorTextMuted("(none)")
	}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		for _, cur := range *list {
			if cur == name {
				return
			}
		}
		EditorPushUndo()
		*list = append(*list, name)
		h.edited(id + "add")
	}
	imgui.SetNextItemWidth(140)
	if imgui.InputTextV("##"+id+"new"+h.id, &Editor.NewAINameText, imgui.InputTextFlagsEnterReturnsTrue, nil) {
		add(Editor.NewAINameText)
		Editor.NewAINameText = ""
	}
	imgui.SameLine()
	if imguibackend.BeginCombo("##"+id+"pick"+h.id, "Add...") {
		if len(suggestions) == 0 {
			editorTextMuted("(nothing in the open level)")
		}
		for _, s := range suggestions {
			if imguibackend.Selectable(s) {
				add(s)
			}
		}
		imguibackend.EndCombo()
	}
}

func aiAnimCombo(label, id string, h aiPanelHost, v *string) {
	preview := *v
	if preview == "" {
		preview = "(none)"
	}
	if imguibackend.BeginCombo(label+"##"+id+h.id, preview) {
		if imguibackend.Selectable("(none)") {
			*v = ""
			h.edited(id)
		}
		for _, a := range h.animations {
			if imguibackend.SelectableV(a, a == *v, 0, imgui.Vec2{}) {
				*v = a
				h.edited(id)
			}
		}
		imguibackend.EndCombo()
	}
}

// aiPathSuggestions is what an agent could walk: the names of the paths its
// type carries, then the level's own waypoint animations (skipping the ones
// entities carry, which are addressed by those names instead).
func aiPathSuggestions(lvl *world.Level, carried []world.WaypointAnimation) []string {
	var out []string
	for i := range carried {
		out = append(out, carried[i].ID)
	}
	if lvl != nil {
		for i := range lvl.WaypointAnimations {
			if lvl.WaypointAnimations[i].FromEntity == "" {
				out = append(out, lvl.WaypointAnimations[i].ID)
			}
		}
	}
	return out
}

// aiTargetSuggestions is what an agent could hunt in the open level: the
// player, each entity by ID, and each tag an entity carries.
func aiTargetSuggestions(lvl *world.Level) []string {
	out := []string{"player"}
	if lvl == nil {
		return out
	}
	tags := map[string]bool{}
	var ids []string
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		ids = append(ids, e.ID)
		for _, t := range e.Tags {
			tags["#"+t] = true
		}
	}
	return append(append(out, sortedKeys(tags)...), ids...)
}

// aiLeaderSuggestions is who an agent could follow in the open level: the
// player, and each entity by ID.
func aiLeaderSuggestions(lvl *world.Level) []string {
	out := []string{"player"}
	if lvl == nil {
		return out
	}
	for i := range lvl.Entities {
		out = append(out, lvl.Entities[i].ID)
	}
	return out
}

// aiObjectiveSuggestions is every objective volume in the open level, and every
// tag one carries.
func aiObjectiveSuggestions(lvl *world.Level) []string {
	if lvl == nil {
		return nil
	}
	tags := map[string]bool{}
	var ids []string
	for i := range lvl.Volumes {
		v := &lvl.Volumes[i]
		if v.Objective == nil {
			continue
		}
		ids = append(ids, v.ID)
		for _, t := range v.Tags {
			tags["#"+t] = true
		}
	}
	return append(sortedKeys(tags), ids...)
}

// aiMissingObjectives lists the entries naming nothing that is an objective in
// lvl. A type's list names tags the level may simply not use, so only a level
// entity's panel is really wrong to have one — but the note helps either way.
func aiMissingObjectives(lvl *world.Level, refs []string) []string {
	var out []string
	for _, r := range refs {
		found := false
		if strings.HasPrefix(r, "#") {
			for _, v := range world.FindVolumesByTag(lvl, r[1:]) {
				if v.Objective != nil {
					found = true
					break
				}
			}
		} else if v := world.FindVolume(lvl, strings.TrimPrefix(r, "@")); v != nil && v.Objective != nil {
			found = true
		}
		if !found {
			out = append(out, r)
		}
	}
	return out
}

func entityAnimationNames(models []world.EntityModel) []string {
	seen := map[string]bool{}
	for i := range models {
		for _, a := range models[i].Animations {
			seen[a.Name] = true
		}
	}
	return sortedKeys(seen)
}

// ---------- the level editor's entity panel ----------

// editorEntityAISection is a placed entity's AI group.
func editorEntityAISection(e *world.Entity) {
	removable := true
	if e.Type != "" {
		if def, err := world.LoadEntityDef(e.Type); err == nil && def.AI != nil {
			removable = false
		}
	}
	editorAISpecUI(&e.AI, aiPanelHost{
		id:         "ent",
		edited:     func(string) {},
		removable:  removable,
		animations: entityAnimationNames(e.Models),
		paths:      aiPathSuggestions(Editor.level, e.WaypointAnimations),
		hasBounds:  e.HasBounds(),
		world:      e.Collision.World,
		gravity:    e.Collision.Gravity,
	})
	if e.Type != "" && e.AI != nil {
		editorTextMutedWrapped("What is set here is this placement's own. Set it on the type and every one placed gets it.")
	}
}

// ---------- the entity editor ----------

// entityPanelAI is a type's AI group.
func entityPanelAI() {
	editorAISpecUI(&entityDef.AI, aiPanelHost{
		id:         "def",
		edited:     entityBeginEdit,
		removable:  true,
		animations: entityAnimationNames(entityDef.Models),
		paths:      aiPathSuggestions(nil, entityDef.WaypointAnimations),
		hasBounds:  entityDef.BoundHalfX > 0 && entityDef.BoundHalfY > 0 && entityDef.BoundHalfZ > 0,
		world:      entityDef.Collision.World,
		gravity:    entityDef.Collision.Gravity,
	})
	if entityDef.AI != nil {
		editorTextMutedWrapped("A type does not know which level it will be placed in, so give its " +
			"objectives as #tags, and tag the volumes in each level. A placement may name its own.")
	}
}

// ---------- the volume panel ----------

// editorVolumeObjective draws a volume's objective block: the checkbox that
// makes one, then what performing it is.
func editorVolumeObjective(v *world.Volume) {
	has := v.Objective != nil
	if imgui.Checkbox("Agents are sent here to act##volobjon", &has) {
		EditorPushUndo()
		if has {
			o := world.DefaultVolumeObjective()
			v.Objective = &o
			editorSetStatus("Volume %q is now an objective", v.ID)
		} else {
			v.Objective = nil
			editorSetStatus("Volume %q is no longer an objective", v.ID)
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("An AI agent that lists this volume walks to a spot inside it,\n" +
			"stays for Duration, and then the action is done: both the agent\n" +
			"and this volume receive the action's atom and :objective_complete:.")
	}
	if v.Objective == nil {
		return
	}
	o := v.Objective
	imgui.Checkbox("Enabled##volobjen", &o.Enabled)

	imgui.SetNextItemWidth(160)
	imgui.InputTextV("Action##volobjact", &o.Action, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("The atom posted when it is done: hack posts :hack:.\nLeave empty for :objective_complete: alone.")
	}
	editorDragFloatStep("Duration (s)##volobjdur", &o.Duration, 0, 3600, "%.2f", 0.25)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How long an agent stays inside performing it. 0 is done on arrival.")
	}
	imgui.SetNextItemWidth(160)
	imgui.InputTextV("Animation##volobjanim", &o.Animation, 0, nil)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Played on the agent while it performs, if it has an animation by that name.")
	}
	capacity := int32(o.Capacity)
	imgui.SetNextItemWidth(160)
	if imgui.InputInt("Capacity (0 = any)##volobjcap", &capacity) {
		if capacity < 0 {
			capacity = 0
		}
		o.Capacity = int(capacity)
	}
	imgui.SetNextItemWidth(160)
	imgui.InputTextV("Only agents tagged##volobjtag", &o.AgentTag, 0, nil)
	imgui.Checkbox("Face the volume's front##volobjface", &o.Face)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Turn a performing agent to face the way this volume faces (its -Z).")
	}
	imgui.Checkbox("Only once##volobjonce", &o.Once)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Switch the objective off after it is first done.")
	}

	if lvl := Editor.level; lvl != nil {
		var agents []string
		for i := range lvl.Entities {
			e := &lvl.Entities[i]
			if e.AI == nil {
				continue
			}
			for _, r := range e.AI.Objectives {
				if r == v.ID || r == "@"+v.ID || (strings.HasPrefix(r, "#") && v.HasTag(r[1:])) {
					agents = append(agents, e.ID)
					break
				}
			}
		}
		if len(agents) == 0 {
			editorTextWrappedColored(aiWarnColor, "No agent in this level lists it yet. Add "+v.ID+
				" (or one of its tags) to an entity's AI Objectives.")
		} else {
			editorTextMutedWrapped("Listed by: " + strings.Join(agents, ", "))
		}
		if inPlaySession() {
			imgui.Text(fmt.Sprintf("Agents working it now: %d", ai.ObjectiveAgents(lvl, v)))
		}
	}
	if !v.Enabled {
		editorTextWrapped("The volume is disabled, so no agent will take this objective.")
	}
	imgui.Checkbox("Show navigation##volobjnav", &Editor.ShowNavigation)
}

// ---------- the overlay ----------

// navOverlayRadius is how far from the camera the grid is drawn. A level's
// whole grid is hundreds of thousands of columns; the part worth seeing is the
// part being looked at.
const navOverlayRadius = float32(30)

var (
	navEdgeColor  = mgl32.Vec3{0.2, 0.95, 0.4}
	navDotColor   = mgl32.Vec3{0.15, 0.55, 0.3}
	navRouteColor = mgl32.Vec3{1, 0.55, 0.1}
)

// drawNavigationOverlay draws the walkable grid near the camera — its edges,
// and a sparse dot on the floor between them — sized for the selected entity's
// controller, or for the player when nothing with one is selected. With an agent
// selected it also draws the route it would walk through its objectives.
func drawNavigationOverlay() {
	lvl := ws().level
	if !Editor.ShowNavigation || lvl == nil || lvl != Editor.level {
		return
	}
	var agent *world.Entity
	if e := activeEntity(); e != nil && e.AI != nil {
		agent = e
	}
	nav.Sync(lvl)
	grid := nav.For(lvl, ai.ProfileFor(lvl, agent))
	if grid == nil || len(grid.Nodes) == 0 {
		return
	}
	cam := ws().camPos()
	hf := grid.HF
	lift := mgl32.Vec3{0, 0.04, 0}
	lo := mgl32.Vec2{cam.X() - navOverlayRadius, cam.Z() - navOverlayRadius}
	ci0 := clampInt(int(math.Floor(float64((lo.X()-hf.Origin.X())/hf.Cell))), 0, hf.W-1)
	cj0 := clampInt(int(math.Floor(float64((lo.Y()-hf.Origin.Y())/hf.Cell))), 0, hf.D-1)
	span := int(2*navOverlayRadius/hf.Cell) + 1
	ci1, cj1 := clampInt(ci0+span, 0, hf.W-1), clampInt(cj0+span, 0, hf.D-1)
	half := hf.Cell / 2
	// The two corners of each of a column's four sides, in link order.
	sides := [4][2]mgl32.Vec2{
		{{half, -half}, {half, half}},
		{{-half, half}, {half, half}},
		{{-half, -half}, {-half, half}},
		{{-half, -half}, {half, -half}},
	}
	for j := cj0; j <= cj1; j++ {
		for i := ci0; i <= ci1; i++ {
			c := i + j*hf.W
			for _, n := range grid.ColumnNodes(c) {
				nd := &grid.Nodes[n]
				p := grid.NodePos(n).Add(lift)
				if absf32(p.Y()-cam.Y()) > navOverlayRadius {
					continue
				}
				for d := 0; d < 4; d++ {
					if nd.Links[d] >= 0 {
						continue
					}
					a, b := sides[d][0], sides[d][1]
					gizmo.GizmoLine(p.Add(mgl32.Vec3{a.X(), 0, a.Y()}), p.Add(mgl32.Vec3{b.X(), 0, b.Y()}), navEdgeColor)
				}
				if i%4 == 0 && j%4 == 0 {
					s := hf.Cell * 0.3
					gizmo.GizmoLine(p.Add(mgl32.Vec3{-s, 0, 0}), p.Add(mgl32.Vec3{s, 0, 0}), navDotColor)
					gizmo.GizmoLine(p.Add(mgl32.Vec3{0, 0, -s}), p.Add(mgl32.Vec3{0, 0, s}), navDotColor)
				}
			}
		}
	}

	if agent != nil {
		drawAgentRoute(lvl, agent)
	}
}

// drawAgentRoute draws the walk from a selected agent through the objectives
// its list names, in list order — what a sequence patrol will do — so a
// volume placed somewhere unreachable is seen before play rather than during it.
func drawAgentRoute(lvl *world.Level, e *world.Entity) {
	const maxLegs = 12
	from := ai.Feet(lvl, e)
	var goals []mgl32.Vec3
	loop := e.AI.Loop
	if a := ai.ResolveWaypoints(lvl, e, e.AI.Waypoints); a != nil && len(a.Points) > 0 {
		// A path first, since it is walked first: each point, or the centre of
		// the volume a point stops in.
		loop = e.AI.WaypointsLoop
		for i := range a.Points {
			g := a.WorldPoint(lvl, i)
			if b := a.Points[i].AI; b != nil && b.Volume != "" {
				if v := world.FindVolume(lvl, b.Volume); v != nil {
					g = v.WorldPosition(lvl)
					g[1] -= v.HalfY
				}
			}
			goals = append(goals, g)
		}
	} else {
		for _, v := range aiRouteStops(lvl, e.AI.Objectives) {
			g := v.WorldPosition(lvl)
			g[1] -= v.HalfY
			goals = append(goals, g)
		}
	}
	if len(goals) == 0 {
		return
	}
	if loop && len(goals) > 1 {
		goals = append(goals, goals[0])
	}
	lift := mgl32.Vec3{0, 0.12, 0}
	for leg, goal := range goals {
		if leg >= maxLegs {
			break
		}
		p, ok := ai.FindPath(lvl, e, from, goal)
		if !ok {
			gizmo.GizmoLine(from.Add(lift), goal.Add(lift), mgl32.Vec3{1, 0.2, 0.2})
			return
		}
		prev := from
		for _, pt := range p.Points {
			gizmo.GizmoLine(prev.Add(lift), pt.Add(lift), navRouteColor)
			prev = pt
		}
		if p.Partial {
			gizmo.GizmoLine(prev.Add(lift), goal.Add(lift), mgl32.Vec3{1, 0.2, 0.2})
			return
		}
		from = prev
	}
}

// aiRouteStops expands an Objectives list into objective volumes, in order.
func aiRouteStops(lvl *world.Level, refs []string) []*world.Volume {
	var out []*world.Volume
	for _, r := range refs {
		if strings.HasPrefix(r, "#") {
			for _, v := range world.FindVolumesByTag(lvl, r[1:]) {
				if v.Objective != nil {
					out = append(out, v)
				}
			}
			continue
		}
		if v := world.FindVolume(lvl, strings.TrimPrefix(r, "@")); v != nil && v.Objective != nil {
			out = append(out, v)
		}
	}
	return out
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func absf32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
