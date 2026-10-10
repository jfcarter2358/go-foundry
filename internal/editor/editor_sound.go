package editor

// editor_sound.go — placing, selecting and picking sound sources.
//
// The emitter workflow, near enough exactly: a sound has no geometry of its
// own either, so the picker tests a small camera-facing disc at its origin and
// the viewport draws that disc.
//
// The one thing this adds over the emitter is the range rings. A positional
// sound is authored almost entirely by its two distances, and neither of them
// is visible in any other way — a torch you can hear across the level is
// indistinguishable, on screen, from one you can hear across the room. Being
// able to see where the sound reaches is most of what makes it placeable by
// eye rather than by walking around listening.

import (
	"math"

	"foundry/internal/audio"
	"foundry/internal/gizmo"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// soundMarkerRadius is the clickable disc's size in world units, matching the
// emitter's so the two feel the same to click.
const soundMarkerRadius = 0.25

const soundRingSegments = 24

var (
	soundMarkerColor = mgl32.Vec3{0.5, 1, 0.5}
	soundSelColor    = mgl32.Vec3{1, 1, 0}
	// The two ranges are drawn in the same hue at different strengths, so
	// which is which is readable without a legend: the inner is where the
	// sound is at full gain, the outer where it falls to nothing.
	soundRefRingColor = mgl32.Vec3{0.35, 0.85, 0.45}
	soundMaxRingColor = mgl32.Vec3{0.2, 0.45, 0.28}
	soundConeColor    = mgl32.Vec3{0.9, 0.75, 0.3}
)

// ---------- selection ----------

// activeSound returns the selected sound, or nil.
func activeSound() *world.Sound {
	lvl := Editor.level
	if lvl == nil || Editor.ActiveSoundIdx < 0 || Editor.ActiveSoundIdx >= len(lvl.Sounds) {
		return nil
	}
	return &lvl.Sounds[Editor.ActiveSoundIdx]
}

// editorSelectSound makes the sound at idx the selection.
func editorSelectSound(idx int) {
	lvl := Editor.level
	if lvl == nil || idx < 0 || idx >= len(lvl.Sounds) {
		return
	}
	editorClearObjectSelections()
	Editor.ActiveSoundIdx = idx
	Editor.GizmoKind = gizmoKindSound
	Editor.PanelFocus = panelFocusSound
}

// editorClearSoundSelection drops the sound from the selection, taking the
// gizmo with it.
func editorClearSoundSelection() {
	Editor.ActiveSoundIdx = -1
	if Editor.GizmoKind == gizmoKindSound {
		Editor.GizmoKind = gizmoKindNone
	}
}

// activeGizmoSound returns the sound the gizmo is attached to, or nil. It
// checks the gizmo kind as well as the index, so a stale selection left by
// another tool cannot silently be dragged.
func activeGizmoSound() *world.Sound {
	if Editor.GizmoKind != gizmoKindSound {
		return nil
	}
	return activeSound()
}

// ---------- placement ----------

// editorArmSoundPlacement makes the next left-click drop a sound.
func editorArmSoundPlacement() {
	Editor.SoundPlacementMode = true
	Editor.EmitterPlacementMode = false
	Editor.SpritePlacementMode = false
	Editor.DecalPlacementMode = false
	Editor.VolumePlacementMode = false
	Editor.EntityPlacementMode = false
	Editor.SpawnPlacementMode = false
}

// placeSoundAt creates a sound at pos, selects it, and leaves placement mode.
func placeSoundAt(pos mgl32.Vec3) bool {
	Editor.SoundPlacementMode = false
	lvl := Editor.level
	if lvl == nil {
		return false
	}

	EditorPushUndo()
	base := Editor.NewSoundID
	if base == "" {
		base = "sound"
	}
	s := world.NewSound(world.UniqueSoundID(lvl, base), pos)
	s.Clip = Editor.NewSoundClip
	if s.Clip != "" {
		// A missing clip is a marker to fix, not a refusal: the placement is
		// real level content either way.
		if _, err := audio.ClipFor(s.Clip); err != nil {
			editorSetStatus("Sound %s: %v", s.ID, err)
		}
	}
	lvl.Sounds = append(lvl.Sounds, s)
	editorSelectSound(len(lvl.Sounds) - 1)
	editorSetStatus("Placed sound %q", s.ID)
	return true
}

// ---------- duplicate / delete ----------

// duplicateActiveSound copies the selected sound beside itself.
func duplicateActiveSound() {
	lvl := Editor.level
	src := activeSound()
	if lvl == nil || src == nil {
		return
	}
	EditorPushUndo()
	cp := *src
	cp.ScriptData = world.CloneScriptData(src.ScriptData)
	cp.ID = world.UniqueSoundID(lvl, src.ID)
	cp.Position = src.Position.Add(mgl32.Vec3{1, 0, 0})
	// The copy starts with no voice of its own. Sharing the original's handle
	// would mean stopping one silenced both, and the survivor would then never
	// start again because it still believes it is playing.
	cp.Voice = 0
	lvl.Sounds = append(lvl.Sounds, cp)
	editorSelectSound(len(lvl.Sounds) - 1)
	editorSetStatus("Duplicated sound %q", cp.ID)
}

// deleteActiveSound removes the selected sound.
func deleteActiveSound() {
	lvl := Editor.level
	idx := Editor.ActiveSoundIdx
	if lvl == nil || idx < 0 || idx >= len(lvl.Sounds) {
		return
	}
	EditorPushUndo()
	id := lvl.Sounds[idx].ID
	// Silence it before it stops existing, or the voice plays on with nothing
	// left to switch it off.
	audio.Stop(lvl.Sounds[idx].Voice)
	lvl.Sounds = append(lvl.Sounds[:idx], lvl.Sounds[idx+1:]...)
	editorClearSoundSelection()
	editorSetStatus("Deleted sound %q", id)
}

// auditionActiveSound plays the selected sound once at the editor camera, so
// it can be heard at full gain without walking to it.
func auditionActiveSound() {
	s := activeSound()
	if s == nil || s.Clip == "" {
		return
	}
	if !audio.Available() {
		editorSetStatus("No audio device")
		return
	}
	if id := audio.Play(audio.PlayOptions{
		Clip:  s.Clip,
		Bus:   s.AudioBus(),
		Gain:  s.Gain,
		Pitch: s.Pitch,
		// Not spatial and not looping: an audition is "what does this clip
		// sound like", not "what does it sound like from here".
	}); id.IsZero() {
		editorSetStatus("Could not play %q", s.Clip)
		return
	}
	editorSetStatus("Auditioning %q", s.Clip)
}

// ---------- picking ----------

// pickSoundRayT returns the distance at which a picking ray crosses the
// sound's marker disc, or +Inf. A camera-facing disc rather than a sphere, for
// pickEmitterRayT's reason: testing what is drawn is what makes it land where
// it looks like it should.
func pickSoundRayT(origin, dir mgl32.Vec3, lvl *world.Level, s *world.Sound, camPos mgl32.Vec3) float32 {
	return pickMarkerDiscRayT(origin, dir, s.WorldPosition(lvl), camPos, soundMarkerRadius)
}

// pickMarkerDiscRayT is the shared camera-facing-disc test. Extracted from
// pickEmitterRayT when the sound marker needed the identical maths.
func pickMarkerDiscRayT(origin, dir, c, camPos mgl32.Vec3, radius float32) float32 {
	toCam := camPos.Sub(c)
	if toCam.LenSqr() < 1e-8 {
		return float32(math.MaxFloat32)
	}
	n := toCam.Normalize()
	denom := dir.Dot(n)
	if denom > -1e-6 && denom < 1e-6 {
		return float32(math.MaxFloat32)
	}
	t := c.Sub(origin).Dot(n) / denom
	if t <= 0 {
		return float32(math.MaxFloat32)
	}
	if origin.Add(dir.Mul(t)).Sub(c).LenSqr() > radius*radius {
		return float32(math.MaxFloat32)
	}
	return t
}

// ---------- drawing ----------

// RenderSoundMarkers draws every sound's marker, and the selected one's
// ranges.
func RenderSoundMarkers(lvl *world.Level, camPos mgl32.Vec3) {
	if lvl == nil {
		return
	}
	for i := range lvl.Sounds {
		s := &lvl.Sounds[i]
		col := soundMarkerColor
		if i == Editor.ActiveSoundIdx {
			col = soundSelColor
		}
		if !s.Enabled || !s.Playing {
			// Dimmed rather than hidden, on the emitter's rule: a switched-off
			// sound is still there and still has to be found.
			col = col.Mul(0.45)
		}
		p := s.WorldPosition(lvl)
		drawSoundMarker(p, camPos, col)
		if i == Editor.ActiveSoundIdx && Editor.ShowSoundRanges {
			drawSoundRanges(lvl, s, p)
		}
	}
}

// drawSoundMarker draws the clickable disc: a ring with a cross through it,
// matching the emitter marker so the two read as the same kind of thing.
func drawSoundMarker(c, camPos, col mgl32.Vec3) {
	toCam := camPos.Sub(c)
	if toCam.LenSqr() < 1e-8 {
		return
	}
	n := toCam.Normalize()
	ref := mgl32.Vec3{0, 1, 0}
	if absF32(n.Y()) > 0.999 {
		ref = mgl32.Vec3{1, 0, 0}
	}
	right := ref.Cross(n).Normalize().Mul(soundMarkerRadius)
	up := n.Cross(right.Normalize()).Mul(soundMarkerRadius)

	prev := c.Add(right)
	for i := 1; i <= soundRingSegments; i++ {
		a := float64(i) / soundRingSegments * 2 * math.Pi
		p := c.Add(right.Mul(float32(math.Cos(a)))).Add(up.Mul(float32(math.Sin(a))))
		gizmo.GizmoLine(prev, p, col)
		prev = p
	}
	gizmo.GizmoLine(c.Sub(right), c.Add(right), col)
	gizmo.GizmoLine(c.Sub(up), c.Add(up), col)
}

// drawSoundRanges draws the reference and maximum distances as horizontal
// rings, plus the cone if there is one.
//
// Horizontal rather than three-axis spheres: a sphere at level-sized radii is
// a ball of lines that hides the geometry behind it, and the distance that
// actually matters to a player is the one along the floor they walk on.
func drawSoundRanges(lvl *world.Level, s *world.Sound, at mgl32.Vec3) {
	drawHorizontalRing(at, s.RefDistance, soundRefRingColor)
	drawHorizontalRing(at, s.MaxDistance, soundMaxRingColor)

	if s.ConeOuter > 0 {
		drawSoundCone(at, s.WorldDirection(lvl), s.ConeOuter, s.RefDistance, soundConeColor)
	}
}

func drawHorizontalRing(c mgl32.Vec3, r float32, col mgl32.Vec3) {
	if r <= 0 {
		return
	}
	const seg = 48
	prev := c.Add(mgl32.Vec3{r, 0, 0})
	for i := 1; i <= seg; i++ {
		a := float64(i) / seg * 2 * math.Pi
		p := c.Add(mgl32.Vec3{r * float32(math.Cos(a)), 0, r * float32(math.Sin(a))})
		gizmo.GizmoLine(prev, p, col)
		prev = p
	}
}

// drawSoundCone sketches the outer cone as four ribs at the reference
// distance, which is enough to read the aim and the width without drawing a
// full mesh over the level.
func drawSoundCone(at, dir mgl32.Vec3, totalAngle, length float32, col mgl32.Vec3) {
	if length <= 0 {
		length = 1
	}
	half := float64(totalAngle) / 2
	if half > math.Pi {
		half = math.Pi
	}

	// Any two vectors perpendicular to the axis will do for the ribs.
	ref := mgl32.Vec3{0, 1, 0}
	if absF32(dir.Y()) > 0.999 {
		ref = mgl32.Vec3{1, 0, 0}
	}
	u := ref.Cross(dir).Normalize()
	v := dir.Cross(u).Normalize()

	axis := dir.Mul(length * float32(math.Cos(half)))
	radius := length * float32(math.Sin(half))
	for i := 0; i < 4; i++ {
		a := float64(i) / 4 * 2 * math.Pi
		off := u.Mul(radius * float32(math.Cos(a))).Add(v.Mul(radius * float32(math.Sin(a))))
		gizmo.GizmoLine(at, at.Add(axis).Add(off), col)
	}
	gizmo.GizmoLine(at, at.Add(dir.Mul(length)), col)
}

// ---------- context-menu helpers ----------

// editor3DMoveSoundTo drops the selected sound at a clicked point.
func editor3DMoveSoundTo(pos mgl32.Vec3) {
	s := activeSound()
	if s == nil {
		return
	}
	EditorPushUndo()
	s.Position = pos
	editorSetStatus("Moved sound %q", s.ID)
}

// editor3DAddSoundAt places one directly, without arming placement mode.
func editor3DAddSoundAt(pos mgl32.Vec3) { placeSoundAt(pos) }
