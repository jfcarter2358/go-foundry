package main

// model_anim.go — keyframed animation clips.
//
// A clip is a set of per-bone tracks, not a list of whole-armature poses. Both
// can express the same motion, but only tracks let one bone be keyed without
// keying every other: an animator who nails a foot plant at frame 12 and is
// still fiddling with the arm should not have the arm's mistakes baked into the
// foot's keyframe. The panel's "Key Pose" button keys every posed bone at once,
// so the simpler whole-pose workflow is still one click — it is just not the
// only one the data can represent.
//
// Interpolation is slerp on the rotation and lerp on the other two channels.
// Rotation is the one that cannot be lerped: the endpoints of a swinging limb
// trace an arc, and interpolating them linearly cuts the chord, so a bone would
// visibly shorten halfway between two keys. That is also why the keys hold
// bone-space TRS rather than posed head/tail placements (model_pose.go).
//
// ---------- two invariants ----------
//
// Tracks index into ModelDoc.Bones, so they are subject to the same rule as
// ModelDoc.Weights, and break the same way if it is missed:
//
//   - Anything that renumbers the bone array must remap the tracks.
//     DeleteBones calls remapAnimBones alongside remapWeightBones; a track left
//     pointing at a deleted bone would drive the wrong limb rather than fail
//     loudly.
//   - Merge (model_primitives.go) shifts the incoming document's bone indices by
//     boneBase. Its animation tracks must be shifted with them, and the incoming
//     clip names uniquified, or the merged model plays another model's motion.
//
// On disk the tracks key on bone *name*, which is why uniqueBoneName exists.

import (
	"fmt"
	"foundry/internal/geom"
	"math"
	"sort"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

// modelDefaultClipFPS is the frame rate a new clip is authored at. Nothing at
// runtime reads it — sampling is in seconds — but the timeline needs a grid to
// snap keys to, and 24 is the one most animation reference is quoted in.
const modelDefaultClipFPS = float32(24)

// modelDefaultClipDuration is how long a new clip runs, in seconds.
const modelDefaultClipDuration = float32(1)

// modelKeyEpsilon is how close two key times must be to count as the same key.
// Half a frame at 120fps: fine enough never to merge keys an animator placed on
// adjacent frames, coarse enough that re-keying the frame you are parked on
// replaces the key instead of stacking a second one a float wobble away.
const modelKeyEpsilon = float32(1.0 / 240.0)

// ModelBoneKey is one bone's pose at one instant. The embedded ModelBonePose
// carries the same zero-value tolerance the pose type documents, so a key
// deserialized with omitted channels reads as rest rather than as a zero
// quaternion.
type ModelBoneKey struct {
	Time float32
	ModelBonePose
}

// ModelBoneTrack is one bone's channel through a clip. Keys are kept ascending
// by Time with no two within modelKeyEpsilon, which is what lets sample binary
// search rather than scan.
type ModelBoneTrack struct {
	Bone int
	Keys []ModelBoneKey
}

// ModelClip is one named animation.
type ModelClip struct {
	Name     string
	Duration float32
	FPS      float32
	Loop     bool
	Tracks   []ModelBoneTrack
}

// ---------- sampling ----------

// duration returns the clip's length, falling back to its last key so a clip
// whose duration was never set still plays rather than collapsing to frame 0.
func (c *ModelClip) duration() float32 {
	if c == nil {
		return 0
	}
	if c.Duration > 0 {
		return c.Duration
	}
	var last float32
	for i := range c.Tracks {
		if n := len(c.Tracks[i].Keys); n > 0 {
			if t := c.Tracks[i].Keys[n-1].Time; t > last {
				last = t
			}
		}
	}
	return last
}

// fps returns the authoring frame rate, defaulted.
func (c *ModelClip) fps() float32 {
	if c == nil || c.FPS <= 0 {
		return modelDefaultClipFPS
	}
	return c.FPS
}

// frameCount is how many whole frames the timeline shows.
func (c *ModelClip) frameCount() int {
	n := int(c.duration()*c.fps() + 0.5)
	if n < 1 {
		return 1
	}
	return n
}

// SampleInto writes the clip's pose at time t into p, which is resized to
// boneCount and filled with identities first — a bone with no track in this clip
// stays at rest rather than keeping whatever the previous sample left there.
func (c *ModelClip) SampleInto(p *ModelPose, boneCount int, t float32) {
	p.resize(boneCount)
	for i := range p.Bones {
		p.Bones[i] = identBonePose()
	}
	if c == nil {
		return
	}
	for i := range c.Tracks {
		tr := &c.Tracks[i]
		if tr.Bone < 0 || tr.Bone >= boneCount || len(tr.Keys) == 0 {
			continue
		}
		p.Bones[tr.Bone] = tr.sample(t)
	}
}

// sample interpolates the track at time t, holding the first and last key
// outside the range rather than extrapolating — a clip sampled a hair past its
// end should settle on its final pose, not keep swinging.
func (tr *ModelBoneTrack) sample(t float32) ModelBonePose {
	n := len(tr.Keys)
	switch {
	case n == 0:
		return identBonePose()
	case t <= tr.Keys[0].Time:
		return tr.Keys[0].normalized()
	case t >= tr.Keys[n-1].Time:
		return tr.Keys[n-1].normalized()
	}
	hi := sort.Search(n, func(i int) bool { return tr.Keys[i].Time > t })
	a, b := tr.Keys[hi-1], tr.Keys[hi]
	span := b.Time - a.Time
	if span <= 0 {
		return b.normalized()
	}
	u := (t - a.Time) / span
	sa, sb := a.scale(), b.scale()
	return ModelBonePose{
		Trans: a.Trans.Add(b.Trans.Sub(a.Trans).Mul(u)),
		// QuatSlerp normalises its inputs and takes the shortest path, so a key
		// stored as the negated-but-equivalent quaternion cannot send the bone
		// the long way round.
		Rot:   mgl32.QuatSlerp(a.rot(), b.rot(), u),
		Scale: sa.Add(sb.Sub(sa).Mul(u)),
	}
}

// wrapClipTime folds t into the clip, looping or clamping.
func (c *ModelClip) wrapClipTime(t float32) float32 {
	d := c.duration()
	if d <= 0 {
		return 0
	}
	if !c.Loop {
		return geom.ClampValue(t, 0, d)
	}
	t = float32(math.Mod(float64(t), float64(d)))
	if t < 0 {
		t += d
	}
	return t
}

// ---------- editing ----------

// trackFor returns bone bi's track, or nil.
func (c *ModelClip) trackFor(bi int) *ModelBoneTrack {
	for i := range c.Tracks {
		if c.Tracks[i].Bone == bi {
			return &c.Tracks[i]
		}
	}
	return nil
}

// ensureTrack returns bone bi's track, creating an empty one if needed.
func (c *ModelClip) ensureTrack(bi int) *ModelBoneTrack {
	if tr := c.trackFor(bi); tr != nil {
		return tr
	}
	c.Tracks = append(c.Tracks, ModelBoneTrack{Bone: bi})
	return &c.Tracks[len(c.Tracks)-1]
}

// SetKey keys bone bi at time t, replacing any key already within
// modelKeyEpsilon so re-keying the frame the playhead is parked on overwrites
// rather than stacking.
func (c *ModelClip) SetKey(bi int, t float32, bp ModelBonePose) {
	if bi < 0 {
		return
	}
	if t < 0 {
		t = 0
	}
	tr := c.ensureTrack(bi)
	key := ModelBoneKey{Time: t, ModelBonePose: bp.normalized()}
	for i := range tr.Keys {
		if geom.Abs32(tr.Keys[i].Time-t) <= modelKeyEpsilon {
			tr.Keys[i] = key
			return
		}
	}
	tr.Keys = append(tr.Keys, key)
	sort.SliceStable(tr.Keys, func(a, b int) bool { return tr.Keys[a].Time < tr.Keys[b].Time })
}

// DeleteKey removes bone bi's key at time t. An emptied track is dropped, so a
// bone the animator un-keyed stops appearing in the timeline.
func (c *ModelClip) DeleteKey(bi int, t float32) bool {
	for i := range c.Tracks {
		if c.Tracks[i].Bone != bi {
			continue
		}
		tr := &c.Tracks[i]
		for k := range tr.Keys {
			if geom.Abs32(tr.Keys[k].Time-t) > modelKeyEpsilon {
				continue
			}
			tr.Keys = append(tr.Keys[:k], tr.Keys[k+1:]...)
			if len(tr.Keys) == 0 {
				c.Tracks = append(c.Tracks[:i], c.Tracks[i+1:]...)
			}
			return true
		}
		return false
	}
	return false
}

// ClearBone drops bone bi's whole track.
func (c *ModelClip) ClearBone(bi int) bool {
	for i := range c.Tracks {
		if c.Tracks[i].Bone == bi {
			c.Tracks = append(c.Tracks[:i], c.Tracks[i+1:]...)
			return true
		}
	}
	return false
}

// HasKeyAt reports whether bone bi is keyed at t.
func (c *ModelClip) HasKeyAt(bi int, t float32) bool {
	tr := c.trackFor(bi)
	if tr == nil {
		return false
	}
	for _, k := range tr.Keys {
		if geom.Abs32(k.Time-t) <= modelKeyEpsilon {
			return true
		}
	}
	return false
}

// KeyTimes returns every instant any bone is keyed at, ascending — the summary
// lane at the top of the timeline.
func (c *ModelClip) KeyTimes() []float32 {
	var out []float32
	for i := range c.Tracks {
		for _, k := range c.Tracks[i].Keys {
			dup := false
			for _, t := range out {
				if geom.Abs32(t-k.Time) <= modelKeyEpsilon {
					dup = true
					break
				}
			}
			if !dup {
				out = append(out, k.Time)
			}
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

// clone deep-copies the clip; the key slices must not be shared or editing one
// snapshot would rewrite another.
func (c ModelClip) clone() ModelClip {
	out := c
	out.Tracks = make([]ModelBoneTrack, len(c.Tracks))
	for i, tr := range c.Tracks {
		out.Tracks[i] = ModelBoneTrack{Bone: tr.Bone, Keys: append([]ModelBoneKey(nil), tr.Keys...)}
	}
	return out
}

func cloneClips(src []ModelClip) []ModelClip {
	if src == nil {
		return nil
	}
	out := make([]ModelClip, len(src))
	for i, c := range src {
		out[i] = c.clone()
	}
	return out
}

// ---------- the document's clip list ----------

// ClipIndexByName returns the clip with the given name, or -1.
func (m *ModelDoc) ClipIndexByName(name string) int {
	for i := range m.Animations {
		if m.Animations[i].Name == name {
			return i
		}
	}
	return -1
}

// Clip returns clip i, or nil.
func (m *ModelDoc) Clip(i int) *ModelClip {
	if i < 0 || i >= len(m.Animations) {
		return nil
	}
	return &m.Animations[i]
}

// ClipNames lists the clips in order, for the editor's combos and the entity
// panel's picker.
func (m *ModelDoc) ClipNames() []string {
	out := make([]string, len(m.Animations))
	for i := range m.Animations {
		out[i] = m.Animations[i].Name
	}
	return out
}

// uniqueClipName returns stem, or the first free "stem.NNN" after it — the same
// scheme uniqueBoneName uses, for the same reason: a clip is addressed by name
// from the level file.
func (m *ModelDoc) uniqueClipName(stem string) string {
	stem = strings.TrimSpace(stem)
	if stem == "" {
		stem = "Clip"
	}
	stem = boneNameStem(stem)
	if m.ClipIndexByName(stem) < 0 {
		return stem
	}
	for n := 1; n < 1000; n++ {
		candidate := fmt.Sprintf("%s.%03d", stem, n)
		if m.ClipIndexByName(candidate) < 0 {
			return candidate
		}
	}
	return fmt.Sprintf("%s.%d", stem, len(m.Animations))
}

// AddClip appends an empty clip and returns its index.
func (m *ModelDoc) AddClip(name string) int {
	m.Animations = append(m.Animations, ModelClip{
		Name:     m.uniqueClipName(name),
		Duration: modelDefaultClipDuration,
		FPS:      modelDefaultClipFPS,
		Loop:     true,
	})
	return len(m.Animations) - 1
}

// DuplicateClip copies clip i and returns the copy's index, or -1.
func (m *ModelDoc) DuplicateClip(i int) int {
	if i < 0 || i >= len(m.Animations) {
		return -1
	}
	dup := m.Animations[i].clone()
	dup.Name = m.uniqueClipName(m.Animations[i].Name)
	m.Animations = append(m.Animations, dup)
	return len(m.Animations) - 1
}

// DeleteClip removes clip i.
func (m *ModelDoc) DeleteClip(i int) {
	if i < 0 || i >= len(m.Animations) {
		return
	}
	m.Animations = append(m.Animations[:i], m.Animations[i+1:]...)
}

// RenameClip gives clip i a unique name.
func (m *ModelDoc) RenameClip(i int, name string) {
	if i < 0 || i >= len(m.Animations) {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" || name == m.Animations[i].Name {
		return
	}
	// Clear the old name first so a case-only rename does not collide with
	// itself — the same guard RenameBone uses.
	m.Animations[i].Name = ""
	m.Animations[i].Name = m.uniqueClipName(name)
}

// hasAnimation reports whether the document carries any keyed motion. Used to
// skip the runtime's whole skinning path on a static model.
func (m *ModelDoc) hasAnimation() bool {
	for i := range m.Animations {
		for j := range m.Animations[i].Tracks {
			if len(m.Animations[i].Tracks[j].Keys) > 0 {
				return true
			}
		}
	}
	return false
}

// remapAnimBones rewrites track bone indices after the bone array has been
// compacted. remap[old] is the new index, or -1 for a bone that was deleted —
// the same contract as remapWeightBones, and it must be called from the same
// places. See the invariants in this file's header.
func (m *ModelDoc) remapAnimBones(remap []int) {
	for ci := range m.Animations {
		c := &m.Animations[ci]
		kept := c.Tracks[:0]
		for _, tr := range c.Tracks {
			if tr.Bone < 0 || tr.Bone >= len(remap) || remap[tr.Bone] < 0 {
				continue
			}
			tr.Bone = remap[tr.Bone]
			kept = append(kept, tr)
		}
		c.Tracks = kept
	}
}

// shiftAnimBones offsets every track's bone index, for Merge folding another
// document's armature in after this one's.
func (m *ModelDoc) shiftAnimBones(by int) {
	for ci := range m.Animations {
		for ti := range m.Animations[ci].Tracks {
			m.Animations[ci].Tracks[ti].Bone += by
		}
	}
}
