package harness

// read.go — what a watch path names, and what it reads.
//
// A watch is `<id>.<field>`, `<entity>/<brush>.<field>` for a brush an entity
// carries, `<entity>#<attachment>.<field>` for a model attachment,
// `<screen>:<widget>.<field>` for a widget on a screen that is up, or
// `player.position`, `player.frame`, `player.attr.health` for the player — who
// is not one of the level's entities — or `music.track` / `music.section` for
// the level's music, as scripts last set it.
//
// Fields are the ones that matter for machinery: position, rotation, scale,
// velocity, angular_velocity, brightness, enabled, visible — and, on an entity
// or a volume, `attr.<name>` (the state a level's scripts keep on their
// objects), plus an agent's `ai_enabled`, `ai_waypoints`, `ai_mode`, `ai_target`
// and `hit_height`.
//
// Everything answers as a STRING, rounded hard, because the question a watch
// asks is "did this change" and a value jittering in the last decimal has not.

import (
	"fmt"
	"strconv"
	"strings"

	"foundry/internal/ai"
	"foundry/internal/ui"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func read(lvl *world.Level, w string) string {
	id, field, ok := strings.Cut(w, ".")
	if !ok {
		return "?"
	}
	if screen, widget, isUI := strings.Cut(id, ":"); isUI {
		return widgetField(screen, widget, field)
	}
	// The player is not one of the level's entities. Its position is read in
	// the world, whatever it rides; `frame` says what that is.
	if id == "player" && world.PlayerEntity != nil {
		p := world.PlayerEntity
		switch field {
		case "position":
			return vec(lvl.Frames.WorldPosition(p))
		case "frame":
			return p.Frame
		case "attr.health":
			return fmt.Sprint(p.Attributes["health"])
		}
		return "?"
	}
	if id == "music" {
		m := lvl.Music
		switch field {
		case "track":
			if m == nil {
				return "none"
			}
			return m.Track
		case "section":
			if m == nil {
				return "none"
			}
			return m.Section
		}
		return "?"
	}
	if ent, brush, isBrush := strings.Cut(id, "/"); isBrush {
		e := world.FindEntity(lvl, ent)
		if e == nil {
			return "no entity " + ent
		}
		for ci := range lvl.Chunks {
			ch := &lvl.Chunks[ci]
			for bi := range ch.Brushes {
				b := &ch.Brushes[bi]
				if b.ID == ent+"."+brush || b.ID == brush {
					return brushField(ch, b, field)
				}
			}
		}
		return "no brush " + brush
	}
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if ch.ID == id {
			switch field {
			case "enabled":
				return fmt.Sprint(!ch.Disabled)
			case "origin":
				return vec(ch.Origin)
			}
		}
		for bi := range ch.Brushes {
			if ch.Brushes[bi].ID == id {
				return brushField(ch, &ch.Brushes[bi], field)
			}
		}
	}
	// `<entity>#<attachment>.<field>` — a model attachment, which is where a
	// beam that has to be re-skinned has to live (a brush has no texture).
	if ent, att, isModel := strings.Cut(id, "#"); isModel {
		e := world.FindEntity(lvl, ent)
		if e == nil {
			return "no entity " + ent
		}
		m := e.Model(att)
		if m == nil {
			return "no attachment " + att
		}
		switch field {
		case "texture":
			return m.Texture
		case "visible":
			return fmt.Sprint(m.Visible)
		case "scale":
			return vec(m.Scale)
		case "scale.x":
			return num(m.Scale.X())
		case "alpha":
			return num(m.Alpha)
		case "uv_offset":
			return fmt.Sprintf("<%s, %s>", num(m.UVOffset.X()), num(m.UVOffset.Y()))
		case "uv_scroll":
			return fmt.Sprintf("<%s, %s>", num(m.UVScroll.X()), num(m.UVScroll.Y()))
		}
		return "?"
	}
	if e := world.FindEntity(lvl, id); e != nil {
		switch field {
		case "position":
			return vec(e.Position)
		case "rotation":
			return vec(e.Rotation)
		case "scale":
			return vec(e.Scale)
		case "visible":
			return fmt.Sprint(e.Visible)
		case "rotation.y":
			return num(e.Rotation.Y())
		case "ai_enabled":
			return fmt.Sprint(e.AI != nil && e.AI.Enabled)
		case "ai_waypoints":
			if e.AIState == nil || e.AIState.Route == "" {
				return "none"
			}
			return e.AIState.Route
		case "ai_mode":
			return world.AIModeName(ai.Mode(e))
		case "ai_target":
			if t := ai.Target(lvl, e); t != nil {
				return t.ID
			}
			return "none"
		case "hit_height":
			return num(e.HitScale())
		}
		// `attr.<name>`: the state a level's scripts keep on their objects.
		if name, isAttr := strings.CutPrefix(field, "attr."); isAttr {
			return attrField(e.Attributes, name)
		}
	}
	if v := world.FindVolume(lvl, id); v != nil {
		switch field {
		case "enabled":
			return fmt.Sprint(v.Enabled)
		}
		if name, isAttr := strings.CutPrefix(field, "attr."); isAttr {
			return attrField(v.Attributes, name)
		}
	}
	for i := range lvl.Lights.Points {
		p := &lvl.Lights.Points[i]
		if p.ID != id {
			continue
		}
		switch field {
		case "brightness":
			return num(p.Brightness)
		case "enabled":
			return fmt.Sprint(!p.Disabled)
		case "position":
			return vec(p.Position)
		case "color":
			return vec(p.Color)
		}
	}
	return "?"
}

func widgetField(screen, widget, field string) string {
	sc := ui.Game.Find(screen)
	if sc == nil {
		return "screen " + screen + " not up"
	}
	n := sc.Root.Find(widget)
	if n == nil {
		return "no widget " + widget
	}
	switch field {
	case "visible":
		return fmt.Sprint(n.Visible)
	case "alpha":
		return num(n.Alpha)
	case "text":
		return fmt.Sprintf("%q", n.Text.Value)
	case "world_anchor":
		if p, ok := n.WorldAnchor(); ok {
			return vec(p)
		}
		return "none"
	case "compass_target":
		if p := n.CompassPin(); p.On && p.UseTarget {
			return vec(p.Target)
		}
		return "none"
	}
	return "?"
}

// `ch` is needed for one thing: Brush.Position() answers in CHUNK coordinates,
// and a chunk whose origin is at z -399 reports a brush sitting there as z 0 —
// which reads as a bug in whatever script put it there, and is not one. World
// space is what a script writes and what the watch list should show.
func brushField(ch *world.Chunk, b *world.Brush, field string) string {
	switch field {
	case "position":
		return vec(b.Position().Add(ch.Origin))
	case "offset":
		return vec(b.Offset)
	case "rotation":
		return vec(b.Rotation)
	case "scale":
		return vec(b.EffectiveScale())
	case "scale.x":
		return num(b.EffectiveScale().X())
	case "angular_velocity":
		return vec(b.AngularVelocity)
	case "angular_velocity.z":
		return num(b.AngularVelocity.Z())
	case "velocity":
		return vec(b.Velocity)
	case "solid":
		return fmt.Sprint(b.Solid)
	case "texture":
		// The first face's texture. A script re-skinning a brush writes every
		// face, so one is enough to watch a cycle.
		if len(b.Faces) > 0 {
			return b.Faces[0].Texture
		}
		return ""
	}
	return "?"
}

// Rounded hard, so a value that is merely jittering in the last decimal does
// not print a line every tick.
func num(f float32) string { return strconv.FormatFloat(float64(f), 'f', 2, 32) }

func vec(v mgl32.Vec3) string {
	return fmt.Sprintf("<%s, %s, %s>", num(v.X()), num(v.Y()), num(v.Z()))
}

// attrField prints one attribute of a set, whatever its type.
func attrField(s world.AttrSet, name string) string {
	a, ok := s[name]
	if !ok {
		return "unset"
	}
	if i, ok := a.Int(); ok {
		return fmt.Sprint(i)
	}
	if f, ok := a.Float(); ok {
		return num(float32(f))
	}
	if b, ok := a.Bool(); ok {
		return fmt.Sprint(b)
	}
	return a.S
}
