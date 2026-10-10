package model

// model_skeleton.go — the armature and its clips, as a document of their own.
//
// A rig is the one part of a model that is genuinely shared. Four characters
// built on the same proportions want the same twenty-five bones and the same
// "walk", and when the walk is wrong it is wrong on all four. Keeping bones and
// clips inside each .fmdl made that a copy-and-paste job: the fix had to be
// retargeted into every file, and the files drifted — the marine picked up
// strafes and dives the mechanic never got, and nothing said so.
//
// So the armature moves out. A SkeletonDoc is bones plus animations, saved as a
// .fskl (model_skeleton_file.go); a ModelDoc names one in ModelDoc.Skeleton and
// carries only the mesh and the weights binding it to that rig.
//
// ---------- what stays the same ----------
//
// In memory nothing changes. A loaded ModelDoc still has its Bones and
// Animations populated, so posing, skinning, the editor's gizmos and the
// runtime player are all untouched — the split is a *file* split, applied on
// load and undone on save. That is deliberate: a rig the renderer had to
// resolve through a second object every frame would be a second way for the
// two to disagree.
//
// ---------- the one thing that changes ----------
//
// Weights. They index into the bone array, and the bone array now belongs to
// somebody else: reordering a skeleton's bones would silently repoint every
// linked model's weights at the wrong limb. So a linked model writes its
// weights by bone *name* (RawVertexBinds.Names), the same reasoning that makes
// an animation track key on a name. Indices are still what memory holds; the
// name form only exists on disk and across a relink.

import "strings"

// SkeletonDoc is one armature and the clips that drive it.
//
// It is deliberately not a ModelDoc with an empty mesh. Every bone and clip
// operation in the editor is a method on ModelDoc — AddBone, ExtrudeBone,
// AddClip, SetKey — and duplicating them here would give the engine two
// armature implementations to keep in step. Instead the model editor goes on
// editing the bones on the open ModelDoc, and this type is what those bones are
// written to and read from.
type SkeletonDoc struct {
	ID         string
	Bones      []ModelBone
	Animations []ModelClip
	// Path is the on-disk .fskl; empty until first save.
	Path string
}

// Clone returns a deep copy.
func (s *SkeletonDoc) Clone() *SkeletonDoc {
	if s == nil {
		return nil
	}
	return &SkeletonDoc{
		ID:         s.ID,
		Bones:      CloneBones(s.Bones),
		Animations: cloneClips(s.Animations),
		Path:       s.Path,
	}
}

// BoneIndexByName returns the index of the named bone, or -1.
func (s *SkeletonDoc) BoneIndexByName(name string) int {
	if s == nil {
		return -1
	}
	for i := range s.Bones {
		if s.Bones[i].Name == name {
			return i
		}
	}
	return -1
}

// BoneNames lists the armature's bones in index order.
func (s *SkeletonDoc) BoneNames() []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s.Bones))
	for i := range s.Bones {
		out[i] = s.Bones[i].Name
	}
	return out
}

// ClipNames lists the skeleton's animations.
func (s *SkeletonDoc) ClipNames() []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s.Animations))
	for i := range s.Animations {
		out[i] = s.Animations[i].Name
	}
	return out
}

// ---------- moving between a model and a skeleton ----------

// SkeletonSnapshot is the skeleton as the document currently holds it: the
// bones and clips in memory, under the name the document links.
//
// This is what SaveModel writes back to the .fskl. A linked document's bones
// *are* the skeleton's — the editor poses and keys them in place — so the
// snapshot is a view, not a conversion, and taking one costs a clone rather
// than a rebuild.
func (m *ModelDoc) SkeletonSnapshot() *SkeletonDoc {
	if m == nil || m.Skeleton == "" {
		return nil
	}
	return &SkeletonDoc{
		ID:         m.Skeleton,
		Bones:      CloneBones(m.Bones),
		Animations: cloneClips(m.Animations),
		Path:       SkeletonPath(m.Skeleton),
	}
}

// ExtractSkeleton lifts the document's armature into a skeleton of its own,
// without linking it. The editor's "New Skeleton" saves the result and then
// links the document to it; a conversion tool saves it next to the models it is
// about to point at it.
func ExtractSkeleton(m *ModelDoc, id string) *SkeletonDoc {
	if m == nil {
		return nil
	}
	return &SkeletonDoc{
		ID:         id,
		Bones:      CloneBones(m.Bones),
		Animations: cloneClips(m.Animations),
		Path:       SkeletonPath(id),
	}
}

// LinkSkeleton points the document at s, adopting its bones and clips in place
// of whatever armature the document had.
//
// The weights come across by bone *name*: the document's current influences are
// read through its current bone array, looked up in s, and rewritten as indices
// into s. A vertex bound to a bone s does not have loses that influence and is
// renormalised over the rest — which is the honest outcome, since there is no
// bone left to move it. Reports how many influences were dropped, so a caller
// can say so rather than let a limb quietly go slack.
func (m *ModelDoc) LinkSkeleton(s *SkeletonDoc) (dropped int) {
	if m == nil || s == nil {
		return 0
	}
	old := m.Bones
	m.Skeleton = s.ID
	m.Bones = CloneBones(s.Bones)
	m.Animations = cloneClips(s.Animations)
	m.EnsureWeights()

	// Old index -> new index, through the name. Built once rather than per
	// influence: a fully rigged character is thousands of lookups.
	remap := make([]int, len(old))
	for i := range old {
		remap[i] = s.BoneIndexByName(old[i].Name)
	}
	for vi, ws := range m.Weights {
		if len(ws) == 0 {
			continue
		}
		out := ws[:0]
		for _, w := range ws {
			if w.Bone < 0 || w.Bone >= len(remap) || remap[w.Bone] < 0 {
				dropped++
				continue
			}
			w.Bone = remap[w.Bone]
			out = append(out, w)
		}
		m.Weights[vi] = normalizeWeights(out)
	}
	m.Invalidate()
	return dropped
}

// UnlinkSkeleton makes the armature the document's own again. The bones and
// clips it is showing stay exactly as they are — they are simply written into
// the .fmdl from now on, and editing them stops reaching the other models that
// share the rig.
func (m *ModelDoc) UnlinkSkeleton() {
	if m == nil || m.Skeleton == "" {
		return
	}
	m.Skeleton = ""
	m.Touch()
}

// LinkedSkeleton reports whether the document's armature belongs to a .fskl.
func (m *ModelDoc) LinkedSkeleton() bool { return m != nil && m.Skeleton != "" }

// skeletonNameFromID sanitises a proposed skeleton name into one that can be a
// filename. Used by the editor's New Skeleton field, which is free text.
func skeletonNameFromID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.ReplaceAll(id, "\\", "/")
	if i := strings.LastIndexByte(id, '/'); i >= 0 {
		id = id[i+1:]
	}
	return strings.TrimSuffix(id, SkeletonExt)
}
