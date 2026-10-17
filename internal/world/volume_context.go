package world

// volume_context.go — a region an author can hand to an assistant.
//
// The Claude Code panel can tell Claude what the editor is selecting, and Claude
// can read every file in the project. Neither of those answers "what is in this
// room". The level file is one long list of brushes in chunk-local coordinates
// with no notion of rooms in it at all, so "restyle the mess hall" means working
// out, from vertex arrays, which forty of the four hundred brushes are the mess
// hall — which is exactly the kind of work a person should not have to check.
//
// A context volume is the author saying where a place is. Drop one around the
// room, name it, and `@mess_hall` is then a thing that can be talked about: the
// panel's context_volume tool answers with everything inside it and where it all
// is. The volume itself does nothing else — it is not a trigger, it draws
// nothing, and it is skipped by the compiler.
//
// The gathering lives here rather than in the editor because it is a question
// about a level, not about a window: it needs no GL and no editor state, so it
// can be tested on a level built in a test, and a headless tool could use it.

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

// VolumeContext marks a volume as a region to describe.
//
// A block rather than a bool because of Note: "the crew's mess — lived-in,
// warm light" is the sort of thing that is obvious standing in the room and
// invisible in the geometry, and it is the author's to say.
type VolumeContext struct {
	// Note is what the author says this place is, in their own words. Optional.
	Note string
}

// VolumeContextFromRaw converts the YAML form. A present but empty block is a
// context volume with nothing said about it, which is the common case.
func VolumeContextFromRaw(r *RawVolumeContext) *VolumeContext {
	if r == nil {
		return nil
	}
	return &VolumeContext{Note: r.Note}
}

// VolumeContextToRaw is the inverse.
func VolumeContextToRaw(c *VolumeContext) *RawVolumeContext {
	if c == nil {
		return nil
	}
	return &RawVolumeContext{Note: c.Note}
}

// IsContext reports whether the volume is one of these.
func (v *Volume) IsContext() bool { return v != nil && v.Context != nil }

// ContextVolumes returns the level's context volumes, in level order.
func ContextVolumes(lvl *Level) []*Volume {
	if lvl == nil {
		return nil
	}
	var out []*Volume
	for i := range lvl.Volumes {
		if lvl.Volumes[i].IsContext() {
			out = append(out, &lvl.Volumes[i])
		}
	}
	return out
}

// FindContextVolume returns the context volume with the given ID, or nil.
//
// The ID may be written with the leading `@` a script or a prompt would use.
func FindContextVolume(lvl *Level, id string) *Volume {
	id = strings.TrimPrefix(strings.TrimSpace(id), "@")
	v := FindVolume(lvl, id)
	if v == nil || !v.IsContext() {
		return nil
	}
	return v
}

// ContextItem is one thing found inside a region.
type ContextItem struct {
	// Kind is what it is: "brush", "entity", "light", and so on. Grouped by this
	// when the contents are written out.
	Kind string
	// ID is what a level file or a script calls it, empty for the things that
	// have no id of their own.
	ID string
	// Detail says which one it is beyond its id — an entity's type, a brush's
	// textures, a sound's file.
	Detail string
	// Position is where it is in the world. For a brush it is the centre of its
	// bounds.
	Position mgl32.Vec3
	// Min and Max are world bounds, set only for the things that have them.
	Min, Max  mgl32.Vec3
	HasBounds bool
	// Chunk names the chunk a brush belongs to, empty otherwise.
	Chunk string
}

// ContextContents is everything a region holds.
type ContextContents struct {
	Volume *Volume
	Note   string
	// Center, Min, Max and Size are the region itself, in world space.
	Center, Min, Max, Size mgl32.Vec3
	Items                  []ContextItem
	// Partial lists the brushes that only overlap the region rather than sitting
	// inside it — the walls of a room are usually these, since a wall is shared
	// with whatever is on its other side.
	Partial int
}

// GatherContext collects everything inside a context volume.
//
// Two containment tests, because two kinds of thing are being asked about:
//
//   - Anything with bounds — a brush — counts if its bounds *overlap* the
//     region. A room's walls straddle its edge by their very nature, and a
//     gathering that left them out would answer "what is in this room" without
//     the room.
//   - Anything that is a point — an entity, a light, a sound — counts if the
//     point is inside. An entity whose bounds merely graze the region is in the
//     next room, and listing it would be noise.
func GatherContext(lvl *Level, v *Volume) ContextContents {
	out := ContextContents{Volume: v}
	if lvl == nil || v == nil {
		return out
	}
	if v.Context != nil {
		out.Note = v.Context.Note
	}
	min, max, ok := v.WorldAABB(lvl)
	if !ok {
		return out
	}
	out.Min, out.Max = min, max
	out.Center = min.Add(max).Mul(0.5)
	out.Size = max.Sub(min)

	// A context volume describes a place whether or not it is armed: Enabled is
	// about triggers firing, and this one never fires. ContainsWorldPoint honours
	// Enabled, so the test goes through a copy that is always on.
	probe := *v
	probe.Enabled = true
	inside := func(p mgl32.Vec3) bool { return probe.ContainsWorldPoint(lvl, p) }

	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		cmin, cmax := ch.WorldBounds()
		if !aabbOverlap(cmin, cmax, min, max) {
			continue
		}
		for bi := range ch.Brushes {
			b := &ch.Brushes[bi]
			bmin, bmax := b.WorldAABB(ch)
			if !aabbOverlap(bmin, bmax, min, max) {
				continue
			}
			centre := bmin.Add(bmax).Mul(0.5)
			if !inside(centre) {
				out.Partial++
			}
			out.Items = append(out.Items, ContextItem{
				Kind:      "brush",
				ID:        b.ID,
				Detail:    brushTextureSummary(b),
				Position:  centre,
				Min:       bmin,
				Max:       bmax,
				HasBounds: true,
				Chunk:     ch.ID,
			})
		}
	}

	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if !inside(e.Position) {
			continue
		}
		detail := e.Type
		if detail == "" {
			detail = "(no type)"
		}
		if tags := e.Tags; len(tags) > 0 {
			detail += " #" + strings.Join(tags, " #")
		}
		out.Items = append(out.Items, ContextItem{
			Kind: "entity", ID: e.ID, Detail: detail, Position: e.Position,
		})
	}

	for i := range lvl.Lights.Points {
		p := &lvl.Lights.Points[i]
		if !inside(p.Position) {
			continue
		}
		out.Items = append(out.Items, ContextItem{
			Kind: "light", ID: p.ID, Position: p.Position,
			Detail: fmt.Sprintf("point, colour [%.2f %.2f %.2f], radius %.1f",
				p.Color.X(), p.Color.Y(), p.Color.Z(), p.Radius),
		})
	}

	for i := range lvl.Sprites {
		s := &lvl.Sprites[i]
		if !s.Attach.IsZero() || !inside(s.Position) {
			continue
		}
		out.Items = append(out.Items, ContextItem{
			Kind: "sprite", ID: s.ID, Detail: s.Texture, Position: s.Position,
		})
	}

	for i := range lvl.Emitters {
		e := &lvl.Emitters[i]
		if e.FromEntity != "" || !e.Attach.IsZero() || !inside(e.Position) {
			continue
		}
		out.Items = append(out.Items, ContextItem{
			Kind: "emitter", ID: e.ID, Detail: e.Effect, Position: e.Position,
		})
	}

	for i := range lvl.Sounds {
		s := &lvl.Sounds[i]
		if !s.Attach.IsZero() || !inside(s.Position) {
			continue
		}
		out.Items = append(out.Items, ContextItem{
			Kind: "sound", ID: s.ID, Detail: s.Clip, Position: s.Position,
		})
	}

	for i := range lvl.Scenery {
		s := &lvl.Scenery[i]
		if !inside(s.Position) {
			continue
		}
		out.Items = append(out.Items, ContextItem{
			Kind: "scenery", ID: s.ID, Detail: s.Model, Position: s.Position,
		})
	}

	for i := range lvl.Decals {
		d := &lvl.Decals[i]
		// The transient ones are left out: a room whose context listed the two
		// hundred bullet holes a fight put in it would say nothing about the room.
		if d.Transient || !inside(d.Position) {
			continue
		}
		out.Items = append(out.Items, ContextItem{
			Kind: "decal", ID: d.ID, Detail: d.DrawTexture(), Position: d.Position,
		})
	}

	// Other volumes in the region are worth knowing about — a trigger inside the
	// room is part of what the room does — but the region itself is not.
	for i := range lvl.Volumes {
		o := &lvl.Volumes[i]
		if o == v || !inside(o.Position) {
			continue
		}
		out.Items = append(out.Items, ContextItem{
			Kind: "volume", ID: o.ID, Detail: volumeRoleSummary(o), Position: o.Position,
		})
	}

	if lvl.Spawn != nil && inside(lvl.Spawn.Position) {
		out.Items = append(out.Items, ContextItem{
			Kind: "spawn", ID: "spawn", Detail: "the player starts here",
			Position: lvl.Spawn.Position,
		})
	}

	sort.SliceStable(out.Items, func(i, j int) bool {
		if out.Items[i].Kind != out.Items[j].Kind {
			return out.Items[i].Kind < out.Items[j].Kind
		}
		return out.Items[i].ID < out.Items[j].ID
	})
	return out
}

// CountsByKind is how many of each kind were found.
func (c ContextContents) CountsByKind() map[string]int {
	out := map[string]int{}
	for _, it := range c.Items {
		out[it.Kind]++
	}
	return out
}

// brushTextureSummary names the textures a brush wears, which is most of what
// "the style of this room" means.
func brushTextureSummary(b *Brush) string {
	seen := map[string]bool{}
	var names []string
	for i := range b.Faces {
		t := b.Faces[i].Texture
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		names = append(names, t)
	}
	sort.Strings(names)
	switch {
	case len(names) == 0:
		return "untextured"
	case len(names) > 4:
		return strings.Join(names[:4], ", ") + fmt.Sprintf(" and %d more", len(names)-4)
	}
	return strings.Join(names, ", ")
}

// volumeRoleSummary says what another volume in the region is for.
func volumeRoleSummary(v *Volume) string {
	var roles []string
	if v.IsContext() {
		roles = append(roles, "context")
	}
	if v.IsRegion() {
		roles = append(roles, "region "+v.Region.Environment)
	}
	if v.IsGrass() {
		roles = append(roles, "grass "+v.Grass.File)
	}
	if v.LoadLevel != "" {
		roles = append(roles, "level exit to "+v.LoadLevel)
	}
	if v.Checkpoint {
		roles = append(roles, "checkpoint")
	}
	if v.Fog != nil {
		roles = append(roles, "fog")
	}
	if v.Cloud != nil {
		roles = append(roles, "cloud")
	}
	if v.Ambient != nil {
		roles = append(roles, "ambient")
	}
	if v.Reflection != nil {
		roles = append(roles, "reflection")
	}
	if v.Scatter != nil {
		roles = append(roles, "scatter")
	}
	if v.Objective != nil {
		roles = append(roles, "objective")
	}
	if v.Doorway != nil {
		roles = append(roles, "doorway")
	}
	if v.Script != "" {
		roles = append(roles, "script "+v.Script)
	}
	if len(roles) == 0 {
		return "trigger"
	}
	return strings.Join(roles, ", ")
}

// aabbOverlap reports whether two world-space boxes intersect.
func aabbOverlap(aMin, aMax, bMin, bMax mgl32.Vec3) bool {
	for i := 0; i < 3; i++ {
		if aMin[i] > bMax[i] || aMax[i] < bMin[i] {
			return false
		}
	}
	return true
}

// Describe writes the contents as text, which is what an assistant is handed.
//
// Prose with numbers in it rather than JSON: it is read by a language model, the
// positions are what matter, and a nested object costs tokens to no benefit.
func (c ContextContents) Describe() string {
	var b strings.Builder
	if c.Volume == nil {
		return "No such context volume."
	}
	fmt.Fprintf(&b, "Context volume @%s\n", c.Volume.ID)
	if c.Note != "" {
		fmt.Fprintf(&b, "The author says: %s\n", c.Note)
	}
	if c.Size == (mgl32.Vec3{}) {
		b.WriteString("It has no extents, so it contains nothing. Give it a size in the editor.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "Shape %s, centred at %s, %s across (world bounds %s to %s).\n",
		VolumeShapeName(c.Volume.Shape), vec(c.Center), vec(c.Size), vec(c.Min), vec(c.Max))

	if len(c.Items) == 0 {
		b.WriteString("\nNothing is inside it.\n")
		return b.String()
	}

	counts := c.CountsByKind()
	var kinds []string
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var summary []string
	for _, k := range kinds {
		summary = append(summary, fmt.Sprintf("%d %s", counts[k], plural(k, counts[k])))
	}
	fmt.Fprintf(&b, "Inside: %s.\n", strings.Join(summary, ", "))
	if c.Partial > 0 {
		fmt.Fprintf(&b, "%d of the brushes straddle the edge — a room's walls usually do.\n", c.Partial)
	}

	lastKind := ""
	for _, it := range c.Items {
		if it.Kind != lastKind {
			heading := pluralKind(it.Kind)
			fmt.Fprintf(&b, "\n%s:\n", strings.ToUpper(heading[:1])+heading[1:])
			lastKind = it.Kind
		}
		id := it.ID
		if id == "" {
			id = "(unnamed)"
		}
		switch {
		case it.HasBounds:
			fmt.Fprintf(&b, "  %s", id)
			if it.Chunk != "" {
				fmt.Fprintf(&b, " [chunk %s]", it.Chunk)
			}
			fmt.Fprintf(&b, " at %s, %s across — %s\n",
				vec(it.Position), vec(it.Max.Sub(it.Min)), it.Detail)
		default:
			fmt.Fprintf(&b, "  %s at %s", id, vec(it.Position))
			if it.Detail != "" {
				fmt.Fprintf(&b, " — %s", it.Detail)
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

// vec formats a position the way a level file writes one, to one decimal: the
// files are authored to the grid and a millimetre of float noise is not
// information.
func vec(v mgl32.Vec3) string {
	return fmt.Sprintf("[%s, %s, %s]", num(v.X()), num(v.Y()), num(v.Z()))
}

func num(f float32) string {
	if math.Abs(float64(f)) < 0.05 {
		return "0"
	}
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", f), "0"), ".")
}

// pluralKind is the plural of one of the kind names above. Small and specific
// on purpose: "entitys" and "brushs" in a description read as a bug in the
// engine, which is a bad first impression for something whose whole job is to
// be read.
func pluralKind(noun string) string {
	switch {
	case strings.HasSuffix(noun, "y"):
		return strings.TrimSuffix(noun, "y") + "ies"
	case strings.HasSuffix(noun, "h"), strings.HasSuffix(noun, "s"),
		strings.HasSuffix(noun, "x"), strings.HasSuffix(noun, "ch"):
		return noun + "es"
	}
	return noun + "s"
}

func plural(noun string, n int) string {
	if n == 1 {
		return noun
	}
	return pluralKind(noun)
}
