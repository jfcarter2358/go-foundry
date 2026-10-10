package editor

// editor_waypoint_ai_panel.go — a waypoint point's instructions to an AI agent.
//
// The same point a brush glides through is a stop to an agent walking the path
// (world/waypoint_ai.go). This is the group that says what the agent does
// there, drawn under the point in the level editor and in the entity editor
// alike — minus the volume row in the entity editor, since a type cannot name a
// level's volume.

import (
	"fmt"
	"math"

	"foundry/internal/gizmo"
	"foundry/internal/world"

	"foundry/internal/imguibackend"
	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorWaypointAIUI draws point idx's AI block, or the box that adds one. lvl
// is the level whose volumes a stop may name, or nil where there is none to
// name. edited is told about every field change.
func editorWaypointAIUI(lvl *world.Level, a *world.WaypointAnimation, idx int, p *world.Waypoint, edited func(string)) {
	has := p.AI != nil
	if imgui.Checkbox("AI agents stop and act here##wpaion", &has) {
		EditorPushUndo()
		if has {
			p.AI = &world.WaypointAI{}
		} else {
			p.AI = nil
		}
		edited("wpaion")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("An AI agent walking this path (its AI group's Waypoints, or\n" +
			"ai_follow_waypoints) finds its own way to each point. Without this\n" +
			"a point is a corner it passes; with it, the point says how to get\n" +
			"here and what to do on arrival. Brushes and other objects ignore it.")
	}
	b := p.AI
	if b == nil {
		return
	}

	if lvl != nil {
		editorWaypointStopVolume(lvl, a, idx, b, edited)
	}

	if editorDragFloatStep("Wait (s)##wpaiwait", &b.Wait, 0, 3600, "%.2f", 0.25) {
		edited("wpaiwait")
	}
	if imgui.Checkbox("Wait for script (ai_continue)##wpaisig", &b.WaitForSignal) {
		edited("wpaisig")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Hold here, after Wait, until a script calls ai_continue(@agent).")
	}

	speed := b.EffectiveSpeedScale()
	if editorDragFloatStep("Speed ×##wpaispeed", &speed, 0.05, 10, "%.2f", 0.05) {
		b.SpeedScale = speed
		edited("wpaispeed")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Multiplies the agent's speed on the way here: 2 runs, 0.5 creeps.")
	}

	engage := int32(b.Engage)
	if imguibackend.Combo("Targets##wpaieng", &engage, world.AIEngageModeNames) {
		b.Engage = world.AIEngageMode(engage)
		edited("wpaieng")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("How the agent treats a target on the way here and while it stops:\n" +
			"inherit — its own On Target (a cover point holds instead)\n" +
			"chase — leave the path to chase, and come back to it after\n" +
			"hold — keep to the path, facing and engaging what it can see\n" +
			"ignore — keep to the path; only the script is told")
	}

	if imgui.Checkbox("Take cover##wpaicover", &b.TakeCover) {
		edited("wpaicover")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("On arrival, move to the nearest reachable spot hidden from the\n" +
			"agent's target — or, with none, from whatever lies the way this\n" +
			"point's arrow faces. It stays for Wait, and for as long after as it\n" +
			"still has a target. Posts :cover_taken: and :cover_lost:.")
	}
	if b.TakeCover && b.Volume == "" {
		r := b.EffectiveCoverRadius()
		if editorDragFloatStep("Cover radius##wpaicr", &r, 0.5, 100, "%.1f", 0.5) {
			b.CoverRadius = r
			edited("wpaicr")
		}
	}
	if b.TakeCover && b.Volume != "" {
		editorTextMutedWrapped("Cover is looked for anywhere inside the volume.")
	}

	if imgui.Checkbox("Face the arrow while waiting##wpaiface", &b.Face) {
		edited("wpaiface")
	}
	imgui.SetNextItemWidth(160)
	if imgui.InputTextV("Animation##wpaianim", &b.Animation, 0, nil) {
		edited("wpaianim")
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Played on the agent while it waits here, if it has one by this name.")
	}
	if !b.Stops() && b.Volume == "" {
		editorTextMutedWrapped("With no wait, signal or cover the agent passes through.")
	}
}

// editorWaypointStopVolume draws the row that makes a stop an area: a picker
// over the level's volumes, and the button that makes one around the point.
func editorWaypointStopVolume(lvl *world.Level, a *world.WaypointAnimation, idx int, b *world.WaypointAI, edited func(string)) {
	preview := b.Volume
	if preview == "" {
		preview = "(the point itself)"
	} else if world.FindVolume(lvl, b.Volume) == nil {
		preview = b.Volume + " (missing)"
	}
	if imguibackend.BeginCombo("Stop##wpaivol", preview) {
		if imguibackend.Selectable("(the point itself)") {
			EditorPushUndo()
			b.Volume = ""
			edited("wpaivol")
		}
		for i := range lvl.Volumes {
			v := &lvl.Volumes[i]
			if imguibackend.SelectableV(v.ID, v.ID == b.Volume, 0, imgui.Vec2{}) {
				EditorPushUndo()
				b.Volume = v.ID
				edited("wpaivol")
			}
		}
		imguibackend.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Make the stop an area: the agent goes anywhere inside the volume\n" +
			"it can reach, and takes cover anywhere inside it. The arrow still\n" +
			"says which way is forward.")
	}
	if b.Volume == "" {
		if imgui.Button("Create Volume Here##wpaivolnew") {
			v := createStopVolume(lvl, a, idx)
			b.Volume = v
			edited("wpaivolnew")
		}
	} else if world.FindVolume(lvl, b.Volume) == nil {
		editorTextWrappedColored(aiWarnColor, "No volume "+b.Volume+" in this level; agents stop at the point instead.")
	}
}

// createStopVolume places a volume around point idx for it to stop in, and
// returns its ID. The waypoint stays selected: the author is building a path.
func createStopVolume(lvl *world.Level, a *world.WaypointAnimation, idx int) string {
	EditorPushUndo()
	at := a.WorldPoint(lvl, idx)
	v := world.NewVolume(world.UniqueVolumeID(lvl, fmt.Sprintf("%s_stop_%d", a.ID, idx+1)), at.Add(mgl32.Vec3{0, 1, 0}))
	v.HalfX, v.HalfY, v.HalfZ = 2, 1, 2
	if err := v.Load(); err != nil {
		editorSetStatus("Volume %s: %v", v.ID, err)
	}
	lvl.Volumes = append(lvl.Volumes, v)
	editorSetStatus("Point %d of %q now stops in volume %q", idx+1, a.ID, v.ID)
	return v.ID
}

// ---------- drawing ----------

var (
	waypointCoverColor = mgl32.Vec3{0.35, 0.8, 1}
	waypointStopColor  = mgl32.Vec3{1, 0.85, 0.3}
)

// drawWaypointAIMarks draws what a point's AI block adds to its arrow: a ring
// on the ground for the cover it searches, a line to the volume it stops in,
// and a small square at the foot of a point an agent waits at. lvl may be nil,
// where no volume can be looked up.
func drawWaypointAIMarks(lvl *world.Level, space mgl32.Mat4, p *world.Waypoint) {
	b := p.AI
	if b == nil {
		return
	}
	at := waypointXform(space, p.Position)
	if b.Volume != "" && lvl != nil {
		if v := world.FindVolume(lvl, b.Volume); v != nil {
			gizmo.GizmoLine(at, v.WorldPosition(lvl), waypointStopColor)
		}
	}
	if b.TakeCover && (b.Volume == "" || lvl == nil) {
		const segs = 32
		r := b.EffectiveCoverRadius()
		for i := 0; i < segs; i++ {
			a0 := 2 * math.Pi * float64(i) / segs
			a1 := 2 * math.Pi * float64(i+1) / segs
			gizmo.GizmoLine(
				at.Add(mgl32.Vec3{float32(math.Cos(a0)) * r, 0, float32(math.Sin(a0)) * r}),
				at.Add(mgl32.Vec3{float32(math.Cos(a1)) * r, 0, float32(math.Sin(a1)) * r}),
				waypointCoverColor)
		}
	}
	if b.Wait > 0 || b.WaitForSignal {
		const s = float32(0.15)
		c := [4]mgl32.Vec3{{-s, 0, -s}, {s, 0, -s}, {s, 0, s}, {-s, 0, s}}
		for i := range c {
			gizmo.GizmoLine(at.Add(c[i]), at.Add(c[(i+1)%4]), waypointStopColor)
		}
	}
}
