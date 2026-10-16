package play

// state.go — what the player can tell about where they are, in words.
//
// A screenshot answers "does it look right"; this answers "what is going on",
// which is the question most of a test actually asks: am I where I meant to
// go, is the door open, did the enemy notice me, what is my gun pointed at.
// Both are wanted, and this one costs no GL at all — it is what a headless run
// with no display has instead of eyes.

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"foundry/internal/ai"
	"foundry/internal/collision"
	"foundry/internal/gfx"
	"foundry/internal/platform"
	"foundry/internal/ui"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// NearRange is how far around the player State looks for things worth
// mentioning. Far enough to cover a room and the corridor out of it; not so far
// that a level's every distant agent is listed.
const NearRange = 60

// State describes the player's situation: where they stand, what they are
// holding, what is on screen, what their crosshair is on and what is near them.
//
// t is the run's own clock, printed as the first line so a log of several
// reports reads as a sequence.
func State(lvl *world.Level, t float64) string {
	var b strings.Builder
	if lvl == nil || world.PlayerEntity == nil {
		return "no level loaded\n"
	}
	p := world.PlayerEntity
	eye := Eye(lvl)
	yaw, pitch := Look()

	fmt.Fprintf(&b, "t %.2f  at %s  yaw %.0f pitch %.0f  facing %s\n",
		t, vec(lvl.Frames.WorldPosition(p)), yaw, pitch, compass(yaw))
	line := []string{}
	if p.Frame != "" {
		line = append(line, "riding "+p.Frame)
	}
	if p.Seat != "" {
		line = append(line, "seat "+p.Seat)
	}
	if platform.IsOnGround {
		line = append(line, "on ground")
	} else {
		line = append(line, "in the air")
	}
	if !platform.PlayerControlEnabled {
		line = append(line, "control off (a script has taken it)")
	}
	if !platform.GravityEnabled {
		line = append(line, "no gravity")
	}
	fmt.Fprintf(&b, "  %s\n", strings.Join(line, ", "))

	// Water (docs/terrain-paint/PHASE_W5.md): how deep the feet and the eye
	// are, when there is a surface over or near them.
	feet := p.Position.Sub(mgl32.Vec3{0, p.BoundHalfY, 0})
	if wf := world.WaterAt(lvl, feet); wf.Found && wf.Depth > -2 {
		we := world.WaterAt(lvl, eye)
		fmt.Fprintf(&b, "  water %s: surface %.2f, feet %.2f m under, eye %.2f m under\n",
			wf.Material, wf.Surface, wf.Depth, we.Surface-eye.Y())
	}

	if s := attrs(p.Attributes); s != "" {
		fmt.Fprintf(&b, "  player: %s\n", s)
	}

	// The crosshair. A shot from here would meet this, which is the difference
	// between "the enemy is in the room" and "the enemy is in my sights".
	dir := gfx.DirectionFromAngles(gfx.Cam.Yaw, gfx.Cam.Pitch)
	res := collision.Raycast(lvl, eye, dir, collision.RayFilter{
		Range: 200, Entities: true, Geometry: true,
		Ignore: map[string]bool{"player": true},
	})
	if h, ok := res.First(); ok {
		fmt.Fprintf(&b, "  crosshair: %s %s at %.1fm\n", hitKind(h.Kind), h.ID, h.T)
	} else {
		fmt.Fprintf(&b, "  crosshair: nothing within 200m\n")
	}

	if s := screens(); s != "" {
		b.WriteString(s)
	}
	b.WriteString(nearby(lvl, eye, yaw))
	return b.String()
}

// hitKind names what a ray met in the words the rest of the report uses.
func hitKind(k collision.RayHitKind) string {
	switch k {
	case collision.RayHitEntity:
		return "entity"
	case collision.RayHitVolume:
		return "volume"
	case collision.RayHitScenery:
		return "scenery"
	case collision.RayHitTerrain:
		return "terrain"
	}
	return "brush"
}

// screens lists what is on screen and, for anything with text on it, what it
// says — a HUD's ammo counter and objective line are half of what a play test
// is checking.
func screens() string {
	var b strings.Builder
	for _, sc := range ui.Game.Screens() {
		labels := []string{}
		sc.Root.Walk(func(_, n *ui.Node) bool {
			if !n.Visible {
				// An invisible node's children are not on screen either.
				return false
			}
			if n.Text.Value != "" && len(labels) < 8 {
				name := n.ID
				if name == "" {
					name = ui.KindName(n.Kind)
				}
				labels = append(labels, fmt.Sprintf("%s=%q", name, n.Text.Value))
			}
			return true
		})
		modal := ""
		if sc.Modal {
			modal = " (modal)"
		}
		fmt.Fprintf(&b, "  screen %s%s: %s\n", sc.ID, modal, strings.Join(labels, " "))
	}
	return b.String()
}

// nearby is what is around the player, nearest first: the entities that are
// awake, alive and close enough to matter, with the bearing and the distance to
// each and whether it can actually be seen from here.
func nearby(lvl *world.Level, eye mgl32.Vec3, yaw float64) string {
	type row struct {
		text string
		d    float32
	}
	var rows []row
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		at := lvl.Frames.WorldPosition(e)
		d := at.Sub(eye).Len()
		if d > NearRange || !e.Visible {
			continue
		}
		parts := []string{fmt.Sprintf("%s %.0fm %s", e.ID, d, bearing(eye, at, yaw))}
		if e.Type != "" {
			parts = append(parts, e.Type)
		}
		if len(e.Tags) > 0 {
			parts = append(parts, "#"+strings.Join(e.Tags, " #"))
		}
		if e.AI != nil && e.AI.Enabled {
			parts = append(parts, "awake")
		}
		if h, ok := e.Attributes["health"]; ok {
			parts = append(parts, "health "+h.String())
		}
		if !ai.LineOfSight(lvl, eye, aimPoint(lvl, e), "player", e.ID) {
			parts = append(parts, "hidden")
		}
		rows = append(rows, row{strings.Join(parts, ", "), d})
	}
	if len(rows) == 0 {
		return fmt.Sprintf("  nothing within %dm\n", NearRange)
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].d < rows[b].d })
	var b strings.Builder
	fmt.Fprintf(&b, "  near (%dm):\n", NearRange)
	for i, r := range rows {
		if i == 12 {
			fmt.Fprintf(&b, "    … and %d more\n", len(rows)-i)
			break
		}
		fmt.Fprintf(&b, "    %s\n", r.text)
	}
	return b.String()
}

// bearing says where something is relative to the way the player faces, which
// is the part a position alone never answers: "behind you" is what a report
// needs to say.
func bearing(from, to mgl32.Vec3, yaw float64) string {
	d := to.Sub(from)
	a := math.Atan2(float64(d.Z()), float64(d.X()))*180/math.Pi - yaw
	for a > 180 {
		a -= 360
	}
	for a < -180 {
		a += 360
	}
	switch {
	case a > -20 && a < 20:
		return "ahead"
	case a >= 20 && a < 70:
		return "ahead-right"
	case a >= 70 && a < 110:
		return "right"
	case a >= 110:
		return "behind-right"
	case a <= -20 && a > -70:
		return "ahead-left"
	case a <= -70 && a > -110:
		return "left"
	}
	return "behind-left"
}

// compass is the yaw as a direction, because +X and +Z mean nothing to anyone
// reading a log for the first time.
func compass(yaw float64) string {
	dirs := []string{"+X", "+X+Z", "+Z", "-X+Z", "-X", "-X-Z", "-Z", "+X-Z"}
	i := int(math.Round(yaw/45)) % 8
	if i < 0 {
		i += 8
	}
	return dirs[i]
}

// attrs prints the state a level's scripts keep on the player: health, ammo,
// whatever the game invented. Sorted, because a map's order would make two
// otherwise identical reports look different.
func attrs(set world.AttrSet) string {
	if len(set) == 0 {
		return ""
	}
	names := make([]string, 0, len(set))
	for k := range set {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s=%s", k, set[k].String()))
	}
	return strings.Join(parts, " ")
}
