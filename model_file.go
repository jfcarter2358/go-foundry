package main

// model_file.go — the .fmdl model document format.
//
// .fmdl is the model editor's source of truth, and OBJ is an interchange format
// it converts to and from (model_obj.go). OBJ cannot hold what the editor needs
// to reopen a model losslessly: it has no per-face smoothing flag, its material
// system is a separate file of surface parameters rather than the logical
// texture names the engine uses, and round-tripping through it would fan every
// n-gon into triangles the moment anything else read it.
//
// The schema follows level_schema.go's conventions: pointer fields with
// omitempty where "absent" and "false/zero" must stay distinguishable, and
// fileutil.WriteFileAtomic (fileutil.go) for the write so a crash mid-save cannot leave
// a half-written model behind.

import (
	"fmt"
	"foundry/internal/fileutil"
	"foundry/internal/geom"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

const modelsDirName = "models"

// modelExt is the native document extension; modelsDir is where the editor's
// open/save dialogs look.
const modelExt = ".fmdl"

func modelsDir() string { return filepath.Join(fileutil.ExecutablePath, modelsDirName) }

func modelPath(name string) string { return filepath.Join(modelsDir(), name+modelExt) }

// RawModel is the on-disk form of a ModelDoc.
type RawModel struct {
	ID string `yaml:"id"`
	// Texture is the one image the model is painted with. Omitted when the
	// document never chose one, which loads back as defaultModelTexture.
	Texture     string           `yaml:"texture,omitempty"`
	SmoothAngle float32          `yaml:"smooth_angle,omitempty"`
	Vertices    [][]float32      `yaml:"vertices"`
	Faces       []RawModelFace   `yaml:"faces"`
	Bones       []RawModelBone   `yaml:"bones,omitempty"`
	Weights     []RawVertexBinds `yaml:"weights,omitempty"`
	Animations  []RawModelClip   `yaml:"animations,omitempty"`
}

// RawModelBone is one bone of the armature. Parent is a pointer so a root bone
// omits the field entirely rather than writing "parent: -1" on every root.
type RawModelBone struct {
	Name      string    `yaml:"name"`
	Parent    *int      `yaml:"parent,omitempty"`
	Head      []float32 `yaml:"head"`
	Tail      []float32 `yaml:"tail"`
	Roll      float32   `yaml:"roll,omitempty"`
	Connected *bool     `yaml:"connected,omitempty"`
}

// RawVertexBinds is one vertex's bone influences. The list is sparse — only
// bound vertices appear — because an unbound vertex is the common case on a
// model that is only partly rigged, and writing an empty entry for each would
// double the file for nothing.
//
// Bones and Weights are index-parallel rather than a list of pairs so the YAML
// stays one short line per vertex instead of a nested block per influence.
type RawVertexBinds struct {
	Vert    int       `yaml:"v"`
	Bones   []int     `yaml:"b"`
	Weights []float32 `yaml:"w"`
}

// RawModelClip is one animation. Tracks key on bone *name* rather than index:
// a name survives the bone array being reordered by an edit, and it is what
// makes a hand-written clip readable. rawToModelDoc resolves them to indices.
type RawModelClip struct {
	Name     string          `yaml:"name"`
	Duration float32         `yaml:"duration"`
	FPS      float32         `yaml:"fps,omitempty"`
	Loop     *bool           `yaml:"loop,omitempty"`
	Tracks   []RawModelTrack `yaml:"tracks,omitempty"`
}

// RawModelTrack is one bone's channel through a clip.
type RawModelTrack struct {
	Bone string        `yaml:"bone"`
	Keys []RawModelKey `yaml:"keys"`
}

// RawModelKey is one keyframe. Each channel is omitted when it is at rest, so a
// clip that only rotates writes one short line per key instead of three vectors
// of zeroes and ones. That is also why Rot is a pointer-free slice tested for
// length: an absent rotation must read as the identity quaternion, not as the
// zero one (see model_pose.go on mgl32.Quat's zero value).
type RawModelKey struct {
	Time  float32   `yaml:"t"`
	Trans []float32 `yaml:"p,omitempty"`
	Rot   []float32 `yaml:"r,omitempty"` // x, y, z, w
	Scale []float32 `yaml:"s,omitempty"`
}

// RawModelFace is one n-gon. Verts indexes RawModel.Vertices; UVs is
// index-parallel to it.
type RawModelFace struct {
	Verts []int       `yaml:"v"`
	UVs   [][]float32 `yaml:"uv,omitempty"`
	// Texture is read, never written: models authored before the move to one
	// texture per document named a texture per face. rawToModelDoc promotes the
	// first one it finds to RawModel.Texture so those files still open with the
	// material they were saved with, instead of silently reverting to the
	// default. Once such a file is saved again the field is gone.
	Texture string `yaml:"texture,omitempty"`
	Smooth  *bool  `yaml:"smooth,omitempty"`
}

// modelDocToRaw converts a document for serialization.
func modelDocToRaw(m *ModelDoc) RawModel {
	raw := RawModel{
		ID:          m.ID,
		Texture:     m.Texture,
		SmoothAngle: m.SmoothAngle,
		Vertices:    make([][]float32, len(m.Verts)),
		Faces:       make([]RawModelFace, len(m.Faces)),
	}
	for i, p := range m.Verts {
		raw.Vertices[i] = []float32{p.X(), p.Y(), p.Z()}
	}
	for i := range m.Faces {
		f := &m.Faces[i]
		rf := RawModelFace{
			Verts:  append([]int(nil), f.Verts...),
			Smooth: boolPtrIfTrue(f.Smooth),
		}
		// UVs are omitted wholesale when every one is zero — a model imported
		// from an untextured OBJ would otherwise carry a screenful of [0, 0].
		if anyNonZeroUV(f.UVs) {
			rf.UVs = make([][]float32, len(f.UVs))
			for j, uv := range f.UVs {
				rf.UVs[j] = []float32{uv.X(), uv.Y()}
			}
		}
		raw.Faces[i] = rf
	}

	for i := range m.Bones {
		b := &m.Bones[i]
		rb := RawModelBone{
			Name:      b.Name,
			Head:      []float32{b.Head.X(), b.Head.Y(), b.Head.Z()},
			Tail:      []float32{b.Tail.X(), b.Tail.Y(), b.Tail.Z()},
			Roll:      b.Roll,
			Connected: boolPtrIfTrue(b.Connected),
		}
		if b.Parent >= 0 {
			parent := b.Parent
			rb.Parent = &parent
		}
		raw.Bones = append(raw.Bones, rb)
	}

	for vi, ws := range m.Weights {
		if len(ws) == 0 || vi >= len(m.Verts) {
			continue
		}
		bind := RawVertexBinds{Vert: vi}
		for _, w := range ws {
			bind.Bones = append(bind.Bones, w.Bone)
			bind.Weights = append(bind.Weights, w.Weight)
		}
		raw.Weights = append(raw.Weights, bind)
	}

	for ci := range m.Animations {
		c := &m.Animations[ci]
		rc := RawModelClip{
			Name:     c.Name,
			Duration: c.duration(),
			FPS:      c.FPS,
			Loop:     boolPtrIfTrue(c.Loop),
		}
		for ti := range c.Tracks {
			tr := &c.Tracks[ti]
			if tr.Bone < 0 || tr.Bone >= len(m.Bones) || len(tr.Keys) == 0 {
				continue
			}
			rt := RawModelTrack{Bone: m.Bones[tr.Bone].Name}
			for _, k := range tr.Keys {
				rt.Keys = append(rt.Keys, rawKeyFrom(k))
			}
			rc.Tracks = append(rc.Tracks, rt)
		}
		raw.Animations = append(raw.Animations, rc)
	}
	return raw
}

// rawKeyFrom serialises one keyframe, omitting whichever channels are at rest.
func rawKeyFrom(k ModelBoneKey) RawModelKey {
	out := RawModelKey{Time: k.Time}
	const eps = 1e-6
	if k.Trans.Len() > eps {
		out.Trans = []float32{k.Trans.X(), k.Trans.Y(), k.Trans.Z()}
	}
	if q := k.rot(); geom.Abs32(geom.Abs32(q.W)-1) > eps {
		out.Rot = []float32{q.V.X(), q.V.Y(), q.V.Z(), q.W}
	}
	if sc := k.scale(); geom.Abs32(sc.X()-1) > eps || geom.Abs32(sc.Y()-1) > eps || geom.Abs32(sc.Z()-1) > eps {
		out.Scale = []float32{sc.X(), sc.Y(), sc.Z()}
	}
	return out
}

// boolPtrIfTrue returns a pointer for true and nil for false, so the field is
// omitted from the YAML rather than written as "smooth: false" on every face.
func boolPtrIfTrue(b bool) *bool {
	if !b {
		return nil
	}
	v := true
	return &v
}

func anyNonZeroUV(uvs []mgl32.Vec2) bool {
	for _, uv := range uvs {
		if uv.X() != 0 || uv.Y() != 0 {
			return true
		}
	}
	return false
}

// rawToModelDoc rebuilds a document from its on-disk form. It assumes the raw
// data has already passed validateRawModel.
func rawToModelDoc(raw RawModel) *ModelDoc {
	m := &ModelDoc{
		ID:          raw.ID,
		Texture:     raw.Texture,
		SmoothAngle: raw.SmoothAngle,
		Verts:       make([]mgl32.Vec3, len(raw.Vertices)),
		Faces:       make([]ModelFace, len(raw.Faces)),
	}
	for i, v := range raw.Vertices {
		m.Verts[i] = mgl32.Vec3{v[0], v[1], v[2]}
	}
	for i, rf := range raw.Faces {
		f := ModelFace{
			Verts:  append([]int(nil), rf.Verts...),
			Smooth: rf.Smooth != nil && *rf.Smooth,
			// UVs are always materialised at full length even when the file
			// omitted them, so uvAt and every UV-interpolating op can index
			// them without a length check of their own.
			UVs: make([]mgl32.Vec2, len(rf.Verts)),
		}
		for j := range rf.UVs {
			if j >= len(f.UVs) {
				break
			}
			if len(rf.UVs[j]) >= 2 {
				f.UVs[j] = mgl32.Vec2{rf.UVs[j][0], rf.UVs[j][1]}
			}
		}
		// Legacy per-face texture: the first face that names one decides the
		// document's texture. A file that used several is flattened to the
		// first, which is lossy — but the alternative is refusing to open it,
		// and the UVs it carries are still the ones that were authored.
		if raw.Texture == "" && m.Texture == "" && rf.Texture != "" && rf.Texture != defaultModelTexture {
			m.Texture = rf.Texture
		}
		m.Faces[i] = f
	}

	m.Bones = make([]ModelBone, len(raw.Bones))
	for i, rb := range raw.Bones {
		b := ModelBone{
			Name:      rb.Name,
			Parent:    modelBoneNoParent,
			Head:      mgl32.Vec3{rb.Head[0], rb.Head[1], rb.Head[2]},
			Tail:      mgl32.Vec3{rb.Tail[0], rb.Tail[1], rb.Tail[2]},
			Roll:      rb.Roll,
			Connected: rb.Connected != nil && *rb.Connected,
		}
		if rb.Parent != nil {
			b.Parent = *rb.Parent
		}
		if b.Name == "" {
			b.Name = fmt.Sprintf("%s.%03d", defaultBoneName, i)
		}
		m.Bones[i] = b
	}

	// Materialised at full length whatever the file held, so every caller can
	// index Weights by vertex without a length check of its own.
	m.ensureWeights()
	for _, bind := range raw.Weights {
		if bind.Vert < 0 || bind.Vert >= len(m.Weights) {
			continue
		}
		ws := make([]ModelVertexWeight, 0, len(bind.Bones))
		for j, bi := range bind.Bones {
			if j >= len(bind.Weights) {
				break
			}
			ws = append(ws, ModelVertexWeight{Bone: bi, Weight: bind.Weights[j]})
		}
		m.Weights[bind.Vert] = normalizeWeights(ws)
	}

	for _, rc := range raw.Animations {
		c := ModelClip{
			Name:     rc.Name,
			Duration: rc.Duration,
			FPS:      rc.FPS,
			Loop:     rc.Loop != nil && *rc.Loop,
		}
		for _, rt := range rc.Tracks {
			bi := m.BoneIndexByName(rt.Bone)
			if bi < 0 {
				// validateRawModel has already refused this, so reaching here
				// means a caller built the raw form by hand; drop the track
				// rather than key a bone that does not exist.
				continue
			}
			tr := ModelBoneTrack{Bone: bi}
			for _, rk := range rt.Keys {
				tr.Keys = append(tr.Keys, modelKeyFromRaw(rk))
			}
			if len(tr.Keys) > 0 {
				c.Tracks = append(c.Tracks, tr)
			}
		}
		m.Animations = append(m.Animations, c)
	}

	m.invalidate()
	return m
}

// modelKeyFromRaw rebuilds one keyframe, reading an omitted channel as rest and
// renormalising the rotation — a hand-written quaternion is rarely unit-length,
// and a non-unit one would scale the bone it drives.
func modelKeyFromRaw(rk RawModelKey) ModelBoneKey {
	k := ModelBoneKey{Time: rk.Time, ModelBonePose: identBonePose()}
	if len(rk.Trans) >= 3 {
		k.Trans = mgl32.Vec3{rk.Trans[0], rk.Trans[1], rk.Trans[2]}
	}
	if len(rk.Rot) >= 4 {
		q := mgl32.Quat{V: mgl32.Vec3{rk.Rot[0], rk.Rot[1], rk.Rot[2]}, W: rk.Rot[3]}
		if q.Len() > 1e-6 {
			k.Rot = q.Normalize()
		}
	}
	if len(rk.Scale) >= 3 {
		k.Scale = mgl32.Vec3{rk.Scale[0], rk.Scale[1], rk.Scale[2]}
	}
	return k
}

// validateRawModel rejects a file that would produce an out-of-bounds index or
// a degenerate face. Loading is the one place a hand-edited or foreign file
// reaches the mesh code, and every operation downstream indexes without
// checking.
func validateRawModel(raw RawModel) error {
	nv := len(raw.Vertices)
	if nv == 0 {
		return fmt.Errorf("model %q has no vertices", raw.ID)
	}
	for i, v := range raw.Vertices {
		if len(v) != 3 {
			return fmt.Errorf("model %q vertex %d has %d components, want 3", raw.ID, i, len(v))
		}
	}
	for i, f := range raw.Faces {
		if len(f.Verts) < 3 {
			return fmt.Errorf("model %q face %d has %d vertices, want at least 3", raw.ID, i, len(f.Verts))
		}
		for _, idx := range f.Verts {
			if idx < 0 || idx >= nv {
				return fmt.Errorf("model %q face %d references vertex %d (only %d exist)", raw.ID, i, idx, nv)
			}
		}
		if len(f.UVs) != 0 && len(f.UVs) != len(f.Verts) {
			return fmt.Errorf("model %q face %d has %d UVs for %d vertices", raw.ID, i, len(f.UVs), len(f.Verts))
		}
	}
	if err := validateRawArmature(raw); err != nil {
		return err
	}
	return validateRawAnimations(raw)
}

// validateRawAnimations rejects a clip the player could not run: a nameless or
// duplicated clip, a track naming a bone that is not in the file, keys out of
// order, or a non-positive duration.
//
// The bone-name check is the one that matters most. A track whose bone has been
// renamed out from under it would otherwise load as silence — the limb simply
// stops moving — which is far harder to diagnose than a refused file.
func validateRawAnimations(raw RawModel) error {
	if len(raw.Animations) == 0 {
		return nil
	}
	if len(raw.Bones) > maxSkinBones {
		return fmt.Errorf("model %q has %d bones; the skinning shader carries at most %d",
			raw.ID, len(raw.Bones), maxSkinBones)
	}
	bones := make(map[string]bool, len(raw.Bones))
	for _, b := range raw.Bones {
		bones[b.Name] = true
	}
	seen := make(map[string]int, len(raw.Animations))
	for i, c := range raw.Animations {
		if strings.TrimSpace(c.Name) == "" {
			return fmt.Errorf("model %q animation %d has no name", raw.ID, i)
		}
		if prev, dup := seen[c.Name]; dup {
			return fmt.Errorf("model %q animations %d and %d are both named %q", raw.ID, prev, i, c.Name)
		}
		seen[c.Name] = i
		if c.Duration <= 0 {
			return fmt.Errorf("model %q animation %q has duration %v, want a positive length", raw.ID, c.Name, c.Duration)
		}
		for _, tr := range c.Tracks {
			if !bones[tr.Bone] {
				return fmt.Errorf("model %q animation %q drives bone %q, which the armature does not contain",
					raw.ID, c.Name, tr.Bone)
			}
			last := float32(-1)
			for k, key := range tr.Keys {
				if key.Time < 0 {
					return fmt.Errorf("model %q animation %q bone %q key %d is at negative time %v",
						raw.ID, c.Name, tr.Bone, k, key.Time)
				}
				if key.Time < last {
					return fmt.Errorf("model %q animation %q bone %q keys are not in time order (%v after %v)",
						raw.ID, c.Name, tr.Bone, key.Time, last)
				}
				last = key.Time
			}
		}
	}
	return nil
}

// validateRawArmature rejects a skeleton the mesh code could not walk: an
// out-of-range or self-referencing parent, a cycle, a zero-length bone with no
// direction to build a deform basis from, or a duplicate name.
//
// Names matter as much as indices here. The animation data this is heading
// towards keys its channels on bone names, so two bones called "Arm" would make
// a saved pose ambiguous — better to refuse the file than to load it and lose
// half the animation later.
func validateRawArmature(raw RawModel) error {
	nb := len(raw.Bones)
	seen := make(map[string]int, nb)
	for i, b := range raw.Bones {
		if len(b.Head) != 3 || len(b.Tail) != 3 {
			return fmt.Errorf("model %q bone %d needs a 3-component head and tail", raw.ID, i)
		}
		head := mgl32.Vec3{b.Head[0], b.Head[1], b.Head[2]}
		tail := mgl32.Vec3{b.Tail[0], b.Tail[1], b.Tail[2]}
		if tail.Sub(head).Len() < 1e-6 {
			return fmt.Errorf("model %q bone %d (%q) has zero length", raw.ID, i, b.Name)
		}
		if b.Name != "" {
			if prev, dup := seen[b.Name]; dup {
				return fmt.Errorf("model %q bones %d and %d share the name %q", raw.ID, prev, i, b.Name)
			}
			seen[b.Name] = i
		}
		if b.Parent == nil {
			continue
		}
		if *b.Parent < 0 || *b.Parent >= nb {
			return fmt.Errorf("model %q bone %d has parent %d (only %d exist)", raw.ID, i, *b.Parent, nb)
		}
		if *b.Parent == i {
			return fmt.Errorf("model %q bone %d is its own parent", raw.ID, i)
		}
	}
	// Cycles, walked per bone with a step cap: a chain longer than the whole
	// armature can only mean it loops.
	for i := range raw.Bones {
		p, steps := raw.Bones[i].Parent, 0
		for p != nil {
			if steps > nb {
				return fmt.Errorf("model %q bone %d sits in a parent cycle", raw.ID, i)
			}
			p = raw.Bones[*p].Parent
			steps++
		}
	}

	nv := len(raw.Vertices)
	for i, bind := range raw.Weights {
		if bind.Vert < 0 || bind.Vert >= nv {
			return fmt.Errorf("model %q weight %d binds vertex %d (only %d exist)", raw.ID, i, bind.Vert, nv)
		}
		if len(bind.Bones) != len(bind.Weights) {
			return fmt.Errorf("model %q weight %d has %d bones for %d weights", raw.ID, i, len(bind.Bones), len(bind.Weights))
		}
		for _, bi := range bind.Bones {
			if bi < 0 || bi >= nb {
				return fmt.Errorf("model %q weight %d references bone %d (only %d exist)", raw.ID, i, bi, nb)
			}
		}
	}
	return nil
}

// SaveModel writes the document to path as YAML.
func SaveModel(m *ModelDoc, path string) error {
	raw := modelDocToRaw(m)
	if err := validateRawModel(raw); err != nil {
		return err
	}
	data, err := yaml.Marshal(&raw)
	if err != nil {
		return fmt.Errorf("marshal model %q: %w", m.ID, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create model directory: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// LoadModel reads a .fmdl document.
func LoadModel(path string) (*ModelDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var raw RawModel
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validateRawModel(raw); err != nil {
		return nil, err
	}
	m := rawToModelDoc(raw)
	m.Path = path
	if m.ID == "" {
		m.ID = trimModelExt(filepath.Base(path))
	}
	return m, nil
}

// ScanModelFiles lists the .fmdl documents available to the open dialog.
func ScanModelFiles() []string { return fileutil.ScanFilesByExt(modelsDir(), modelExt) }

func trimModelExt(name string) string {
	return name[:len(name)-len(filepath.Ext(name))]
}
