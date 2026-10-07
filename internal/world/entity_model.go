package world

// entity_model.go — an entity's model attachments.
//
// An entity holds a *list* of attachments rather than one model, because the
// things a game wants to place are rarely one rigid mesh: a tank is a hull and
// a turret that turn independently, a character is a body and whatever it is
// holding. Each attachment carries its own offset, rotation and scale relative
// to the entity, so moving the entity moves all of them together while each
// keeps its own placement on the entity.
//
// Each attachment also carries its own set of *named* animations. A .fmdl holds
// clips (model_anim.go); an attachment's animation list is what names them for
// the game — "walk" can mean the clip `walk_cycle_02` on one model and
// `locomotion.walk` on another, and a script says `walk` either way. That
// indirection is the whole point: the animation set is per-model because the
// clips are per-model, but the names scripts use are not.

import (
	"fmt"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/model"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

// EntityAnimation is one named, playable animation on an attachment.
type EntityAnimation struct {
	// Name is what scripts and the editor call this animation.
	Name string
	// Clip is the clip in the model document it plays. Empty means "the clip
	// with the same name as this animation", which is what makes the common
	// case — a model whose clips are already called what the game calls them —
	// need no mapping at all.
	Clip string
	// Speed multiplies playback rate; 0 means 1, so an unmentioned speed plays
	// at the authored rate.
	Speed float32
	// Loop overrides the clip's own loop flag. Nil follows the clip, which is
	// where looping is authored — an override exists for the case where one
	// game wants a one-shot out of a clip authored to cycle.
	Loop *bool
	// BlendTime is how many seconds this animation crossfades in from whatever
	// pose is showing when it starts. Nil follows DefaultAnimBlend; an explicit
	// 0 forces a hard cut, for the rare transition — a ragdoll drop, a teleport
	// — that should not soften.
	BlendTime *float32
	// Autoplay starts this animation when the entity loads. At most one per
	// attachment is honoured (the first), since a model can only be in one pose.
	Autoplay bool
}

// DefaultAnimBlend is the crossfade an animation gets when its own BlendTime
// does not say otherwise — long enough to hide the pop of a hard cut, short
// enough not to read as its own transition animation.
const DefaultAnimBlend = float32(0.15)

// effectiveClip returns the clip name this animation plays.
func (a EntityAnimation) EffectiveClip() string {
	if a.Clip != "" {
		return a.Clip
	}
	return a.Name
}

// effectiveSpeed treats 0 as 1 — see Speed.
func (a EntityAnimation) EffectiveSpeed() float32 {
	if a.Speed == 0 {
		return 1
	}
	return a.Speed
}

// EffectiveBlend returns the crossfade this animation starts with — see
// BlendTime.
func (a EntityAnimation) EffectiveBlend() float32 {
	if a.BlendTime == nil {
		return DefaultAnimBlend
	}
	return *a.BlendTime
}

// EntityModel is one model attached to an entity.
type EntityModel struct {
	// Name identifies the attachment within its entity — what a script names to
	// play an animation on this model rather than another, and what an instance
	// names to override this attachment's transform. Unique per entity.
	Name string
	// Path is the model file, stored relative to resources/ (see
	// RelAssetPath). Both .fmdl and .obj are accepted.
	Path string
	// Material is the .fmat every submesh of this attachment wears, or "" for
	// none. Overlaid on the fields around it rather than replacing them; see
	// gfx.SurfaceState.
	Material string

	// Texture overrides the model document's own texture. Empty keeps whatever
	// the model was authored with, which is the usual case — the override is
	// for reskinning one shared mesh per entity type.
	Texture string

	// Parent names another of the entity's model attachments for this one to
	// ride, and Bone a bone inside it — a sword in a hand, a turret on a hull's
	// mount point. Empty Parent rides the entity itself. A Parent with no Bone
	// rides that attachment's own origin. See Entity.AttachmentMatrix.
	Parent string
	Bone   string

	// The attachment's transform relative to what it rides: the entity, or
	// the Parent attachment's Bone when one is set. Composed under that, so the
	// carrier's rotation and scale carry these with them.
	Offset   mgl32.Vec3
	Rotation mgl32.Vec3 // Euler angles (X, Y, Z) in radians
	Scale    mgl32.Vec3 // per-axis; zero value treated as {1,1,1}

	// Alpha is the attachment's opacity; 0 means "unset" and loads as 1, so an
	// attachment that never mentioned it is opaque rather than invisible.
	Alpha float32
	// Visible hides this attachment without removing it.
	Visible bool
	// Unlit draws the attachment at its texture's own colour, with no light or
	// shadow on it and none cast by it — for something that gives off light,
	// like a beam. It draws in the sorted transparent pass, so a texture with
	// an alpha channel fades off at its edges as a glow rather than a solid.
	Unlit bool
	// UVScroll slides the attachment's texture across it, in UV units a
	// second: a tank's track belt running round its wheels, a conveyor, a
	// waterfall. The mesh wants a texture that tiles along the direction it
	// scrolls. Zero holds it still.
	UVScroll mgl32.Vec2

	// Animations are the named clips this attachment can play.
	Animations []EntityAnimation

	// ---------- runtime, never authored and never saved ----------

	// Submeshes is the uploaded mesh. A document compiles to one submesh today
	// (model_doc.go), but the slice is kept so a model that later splits by
	// material needs no change here.
	Submeshes []gfx.Geometry
	// Anim is live playback state, nil on a model with no motion to play.
	Anim *EntityAnim
	// Doc is kept past Load only when the model has an armature — to pose
	// against, and for its bones to serve as sockets even when nothing animates
	// them. Re-reading the file every frame is not an option. Nil for models with
	// no bones, which is what keeps a level of props from holding every mesh
	// twice.
	Doc *model.ModelDoc
	// AnimBlocked is why Anim is nil, in words, or "" when it is not nil.
	// Recorded at Load because that is the only moment the document is in hand:
	// afterwards a static model has dropped it, and "no armature" and "no skin
	// weights" are indistinguishable from an EntityModel alone.
	AnimBlocked string
	// UVOffset is how far UVScroll has slid the texture, kept inside [0, 1)
	// so a track that runs all game never loses precision. Handed to every
	// submesh as it draws. Saved, so a restored tank's tracks do not jump.
	UVOffset mgl32.Vec2
}

// effectiveScale treats the zero value as identity.
func (m *EntityModel) EffectiveScale() mgl32.Vec3 {
	if m.Scale == (mgl32.Vec3{}) {
		return mgl32.Vec3{1, 1, 1}
	}
	return m.Scale
}

// effectiveAlpha treats 0 as fully opaque — see Alpha.
func (m *EntityModel) EffectiveAlpha() float32 {
	if m.Alpha == 0 {
		return 1
	}
	return m.Alpha
}

// LocalMatrix is the attachment's own offset, rotation and scale — relative to
// whatever it rides. For where it is in the entity, which is what anything
// drawing or picking it wants, use Entity.AttachmentMatrix.
func (m *EntityModel) LocalMatrix() mgl32.Mat4 {
	s := m.EffectiveScale()
	T := mgl32.Translate3D(m.Offset.X(), m.Offset.Y(), m.Offset.Z())
	S := mgl32.Scale3D(s.X(), s.Y(), s.Z())
	return T.Mul4(EulerMatrixYXZ(m.Rotation)).Mul4(S)
}

// WorldMatrix is the attachment's full transform: the entity's matrix with the
// attachment's placement in the entity composed under it.
func (m *EntityModel) WorldMatrix(e *Entity) mgl32.Mat4 {
	if e == nil {
		return m.LocalMatrix()
	}
	return e.ModelMatrix().Mul4(e.AttachmentMatrix(m))
}

// AttachmentMatrix is where attachment m sits in the entity's own space: its
// LocalMatrix, carried by the posed bone of the attachment it rides, and that
// attachment by whatever it rides in turn.
//
// This is the one resolution every pass shares — colour, both shadow passes,
// picking and the entity editor's preview — so a sword and the hand holding it
// cannot disagree about where the hand is. An unresolvable mount (a parent that
// has been renamed, a bone its model does not have, a model not yet loaded)
// reads as riding the next thing out rather than as an error, the rule Attach
// follows for the same reason.
func (e *Entity) AttachmentMatrix(m *EntityModel) mgl32.Mat4 {
	return entityAttachmentMatrix(e, m, 0)
}

// entityAttachmentMatrix is AttachmentMatrix with a depth count. The editor
// refuses a cycle (ModelMountCycle), but a hand-edited file can still hold one,
// and the cap turns that into a wrong placement rather than a hang.
func entityAttachmentMatrix(e *Entity, m *EntityModel, depth int) mgl32.Mat4 {
	local := m.LocalMatrix()
	if e == nil || m.Parent == "" || depth >= len(e.Models) {
		return local
	}
	p := e.Model(m.Parent)
	if p == nil || p == m {
		return local
	}
	if m.Bone != "" {
		if bm, ok := entityBoneMatrix(e, p, m.Bone, depth+1); ok {
			return bm.Mul4(local)
		}
	}
	return entityAttachmentMatrix(e, p, depth+1).Mul4(local)
}

// RenameModelMounts points every attachment riding the one called from at its
// new name, so renaming a hand's model does not drop the sword off it.
func RenameModelMounts(models []EntityModel, from, to string) {
	for i := range models {
		if models[i].Parent == from {
			models[i].Parent = to
		}
	}
}

// DropModelMounts takes every attachment riding the one called name off it,
// for when that attachment is removed. They go back to riding the entity,
// where the mount would have left them anyway, rather than naming nothing.
func DropModelMounts(models []EntityModel, name string) {
	for i := range models {
		if models[i].Parent == name {
			models[i].Parent, models[i].Bone = "", ""
		}
	}
}

// ModelMountCycle reports whether mounting the attachment named child on the
// one named parent would make child ride itself, directly or through others.
func (e *Entity) ModelMountCycle(child, parent string) bool {
	if e == nil || parent == "" {
		return false
	}
	for steps, p := 0, parent; p != "" && steps <= len(e.Models); steps++ {
		if p == child {
			return true
		}
		m := e.Model(p)
		if m == nil {
			return false
		}
		p = m.Parent
	}
	return false
}

// ---------- loading ----------

// Load uploads the attachment's mesh to the GPU. ownerID only appears in log
// messages, naming the entity a texture failure belongs to.
//
// Safe to call repeatedly: the previous upload is released first, which is what
// makes it usable as "rebuild after the model path changed in the editor".
func (m *EntityModel) Load(ownerID string) error {
	m.Delete()
	if m.Path == "" {
		return nil
	}
	path := fileutil.ResolveAssetPath(m.Path)
	if shareModelMeshes {
		if err := m.loadShared(ownerID, path); err != nil {
			return err
		}
		m.bindPalette()
		m.startAutoplay()
		return nil
	}
	doc, err := loadModelDocument(path)
	if err != nil {
		return fmt.Errorf("entity %q model %q: %w", ownerID, m.Name, err)
	}

	// A texture override replaces the document's, so the mesh compiles already
	// wearing it rather than being patched per-submesh afterwards.
	if m.Texture != "" {
		doc.Texture = m.Texture
	}

	m.initAnimation(doc)

	for _, sub := range doc.ModelSubmeshes() {
		if len(sub.Indices) == 0 {
			continue
		}
		// Every texture the model reaches for is ensured here: nothing else
		// loads it. The level loader only walks chunk geometry, so a model
		// placed with a texture no wall happens to use would otherwise draw
		// untextured.
		if err := gfx.EnsureTextureLoaded(sub.Texture); err != nil {
			log.Printf("entity %s: model %s: texture %q: %v", ownerID, m.Name, sub.Texture, err)
		}
		// Indexed: a model loads as a shared vertex pool and an index list
		// (gfx.IndexedModels; docs/nanite/PHASE_0.md).
		g := gfx.Geometry{Smooth: sub.Smooth, SmoothAngle: doc.SmoothAngle, AutoSmooth: true, Indexed: true}
		if m.Anim != nil {
			// Set before Load, which de-indexes these alongside the positions.
			g.SkinIdx, g.SkinWt = sub.SkinIdx, sub.SkinWt
		}
		// Position is the geometry's own origin for the transparent sort; the
		// mesh is model-space, so that origin is the attachment's offset.
		if err := g.Load(sub.Vertices, sub.Indices, sub.Texture, m.Offset); err != nil {
			return fmt.Errorf("entity %q model %q: %w", ownerID, m.Name, err)
		}
		g.Alpha = m.EffectiveAlpha()
		g.Unlit = m.Unlit
		g.MaterialName = m.Material
		m.Submeshes = append(m.Submeshes, g)
	}

	m.bindPalette()
	m.startAutoplay()
	return nil
}

// loadShared is Load's body with the mesh shared between every instance of the
// model (model_mesh_cache.go): the built submeshes are copied from the cache
// and their buffers shared, and only what is the instance's own is set here.
// What comes out is the attachment the unshared path builds, field for field
// (TestASharedModelMeshIsTheUnsharedOne).
func (m *EntityModel) loadShared(ownerID, path string) error {
	doc, err := pristineModelDocument(path)
	if err != nil {
		return fmt.Errorf("entity %q model %q: %w", ownerID, m.Name, err)
	}
	// The pristine document is shared and read-only. An instance that keeps
	// one — a rigged model, which it poses — keeps its own copy, wearing its
	// texture override as the unshared path's does.
	if len(doc.Bones) > 0 {
		doc = doc.Clone()
		if m.Texture != "" {
			doc.Texture = m.Texture
		}
	}
	texture := doc.Texture
	if m.Texture != "" {
		texture = m.Texture
	}
	m.initAnimation(doc)

	skinned := m.Anim != nil
	var mm *modelMesh
	if key, ok := modelMeshKeyFor(path, doc, skinned); ok {
		mm, err = sharedModelMesh(key, doc)
	} else {
		mm, err = buildModelMesh(doc, skinned)
	}
	if err != nil {
		return fmt.Errorf("entity %q model %q: %w", ownerID, m.Name, err)
	}
	for i := range mm.subs {
		// Every texture the model reaches for is ensured here: nothing else
		// loads it. The level loader only walks chunk geometry, so a model
		// placed with a texture no wall happens to use would otherwise draw
		// untextured.
		if err := gfx.EnsureTextureLoaded(texture); err != nil {
			log.Printf("entity %s: model %s: texture %q: %v", ownerID, m.Name, texture, err)
		}
		g := mm.subs[i]
		g.Texture = texture
		// Position is the geometry's own origin for the transparent sort; the
		// mesh is model-space, so that origin is the attachment's offset.
		g.Position = m.Offset
		g.UploadShared(&mm.shares[i])
		g.Alpha = m.EffectiveAlpha()
		g.Unlit = m.Unlit
		g.MaterialName = m.Material
		m.Submeshes = append(m.Submeshes, g)
	}
	return nil
}

// SetTexture changes what the attachment draws with, both for the next reload
// (the override) and right now (the uploaded submeshes).
//
// Patching the submeshes is the whole of the live change: every draw call binds
// Textures[g.Texture] afresh, so a name swap needs no re-upload of the mesh and
// no reload of the model — see the note above pendingUpload in go.
func (m *EntityModel) SetTexture(texture string) {
	m.Texture = texture
	for i := range m.Submeshes {
		m.Submeshes[i].Texture = texture
	}
}

// Delete releases the attachment's GPU buffers and drops its playback state.
// Reloading without this would leak the previous upload's VAO/VBO.
func (m *EntityModel) Delete() {
	for i := range m.Submeshes {
		m.Submeshes[i].Delete()
	}
	m.Submeshes = nil
	m.Anim = nil
	m.Doc = nil
	m.AnimBlocked = ""
}

// HasGeometry reports whether this attachment has anything uploaded to draw.
func (m *EntityModel) HasGeometry() bool {
	for i := range m.Submeshes {
		if len(m.Submeshes[i].Vertices) > 0 {
			return true
		}
	}
	return false
}

// SetAlpha applies a to every submesh: transparency is a per-Geometry property,
// so it has to reach all of them or half a model would stop blending.
func (m *EntityModel) SetAlpha(a float32) {
	m.Alpha = a
	for i := range m.Submeshes {
		m.Submeshes[i].Alpha = m.EffectiveAlpha()
	}
}

// initAnimation prepares playback state, and keeps the document only when there
// is something to pose. A static model's document is dropped here.
func (m *EntityModel) initAnimation(doc *model.ModelDoc) {
	m.Anim = NewEntityAnim(doc)
	m.AnimBlocked = AnimUnavailable(doc)
	// Bones without motion are still sockets: a turret's muzzle bone is a place
	// to hang a flash and a model, whether or not the turret ever animates.
	if m.Anim != nil || (doc != nil && len(doc.Bones) > 0) {
		m.Doc = doc
	}
}

// bindPalette hands every submesh a slice header on the shared bone palette.
// The backing array is never reallocated after NewEntityAnim, which is what
// makes sharing it safe: the animation rewrites it in place each frame and the
// colour pass and both shadow passes read it from there.
func (m *EntityModel) bindPalette() {
	if m.Anim == nil {
		return
	}
	for i := range m.Submeshes {
		m.Submeshes[i].BonePalette = m.Anim.Palette
	}
}

// startAutoplay starts the first animation marked Autoplay, if any.
func (m *EntityModel) startAutoplay() {
	for _, a := range m.Animations {
		if a.Autoplay {
			m.Play(a.Name)
			return
		}
	}
}

// ---------- animation control ----------

// HasAnimation reports whether this attachment has motion it can play — both a
// rigged document and at least one animation naming a clip in it.
func (m *EntityModel) HasAnimation() bool {
	return m.Anim != nil && len(m.Animations) > 0
}

// Animation returns the named animation, or nil.
func (m *EntityModel) Animation(name string) *EntityAnimation {
	for i := range m.Animations {
		if m.Animations[i].Name == name {
			return &m.Animations[i]
		}
	}
	return nil
}

// Play starts the named animation from the top and reports whether it resolved.
// An empty name stops playback and returns the model to its rest pose.
//
// It fails rather than falling back to a default when the name is unknown: a
// script asking for an animation that is not there is a bug worth seeing, and
// silently playing "idle" instead would hide it.
func (m *EntityModel) Play(name string) bool {
	if m.Anim == nil {
		return false
	}
	if name == "" {
		m.Anim.SetClip("", 0)
		return true
	}
	a := m.Animation(name)
	if a == nil {
		return false
	}
	if !m.Anim.SetClip(a.EffectiveClip(), a.EffectiveBlend()) {
		return false
	}
	m.Anim.Speed = a.EffectiveSpeed()
	if a.Loop != nil {
		m.Anim.Loop = *a.Loop
	}
	return true
}

// Stop halts playback and returns the model to its rest pose.
func (m *EntityModel) Stop() {
	if m.Anim != nil {
		m.Anim.SetClip("", 0)
	}
}

// Playing returns the name of the animation currently playing, or "".
//
// It maps the live clip back through the animation list rather than remembering
// what Play was told, so a clip started any other way still reports under the
// name the game knows it by.
func (m *EntityModel) Playing() string {
	if m.Anim == nil || !m.Anim.Playing {
		return ""
	}
	c := m.Anim.clip()
	if c == nil {
		return ""
	}
	for _, a := range m.Animations {
		if a.EffectiveClip() == c.Name {
			return a.Name
		}
	}
	return ""
}

// Update advances this attachment's animation clock and reports any clip
// transition it crossed, named the way the game knows the animation rather than
// the way the .fmdl does.
//
// entityID is what the event is addressed to. It is threaded down rather than
// looked up because an attachment has no way back to its entity, and adding one
// would be a cycle for the sake of one string.
func (m *EntityModel) Update(dt float32, entityID string) {
	m.UpdatePose(dt, entityID, true)
}

// UpdatePose is Update that rebuilds the pose only when pose is set; the
// clock, and every event it posts, move either way. See AnimPoseDue.
func (m *EntityModel) UpdatePose(dt float32, entityID string, pose bool) {
	if m.UVScroll != (mgl32.Vec2{}) {
		m.UVOffset = wrapUV(m.UVOffset.Add(m.UVScroll.Mul(dt)))
	}
	if m.Anim == nil {
		return
	}
	name := m.Playing()
	m.Anim.UpdatePose(dt, pose)

	if m.Anim.wrapped {
		m.Anim.wrapped = false
		// A looping clip emits end-then-start per cycle, in that order: the
		// cycle that just closed, then the one that opened.
		hooks.RecordAnimEvent(hooks.AnimEvent{Entity: entityID, Model: m.Name, Animation: name, Ended: true})
		hooks.RecordAnimEvent(hooks.AnimEvent{Entity: entityID, Model: m.Name, Animation: name})
	}
	if m.Anim.finished {
		m.Anim.finished = false
		hooks.RecordAnimEvent(hooks.AnimEvent{Entity: entityID, Model: m.Name, Animation: name, Ended: true})
	}
}

// ---------- helpers ----------

// wrapUV keeps each component of an offset in [0, 1): a whole tile of scroll
// is no scroll at all on a texture that repeats.
func wrapUV(v mgl32.Vec2) mgl32.Vec2 {
	for i := range v {
		v[i] -= float32(math.Floor(float64(v[i])))
	}
	return v
}

// loadModelDocument reads a model file into the editable document form, picking
// the reader by extension.
//
// Parsed documents are cached by path, and every caller gets its own Clone: a
// level full of soldiers is dozens of entities wearing the same three models,
// and each of those is most of a megabyte of YAML. Parsing marine.fmdl once per
// marine was most of level1's load. The cache entry is checked against the
// file's size and modification time, so a model saved from the editor is read
// again rather than served stale.
//
// A rigged model is two files — the .fmdl and the .fskl it links — so the stamp
// covers both. Without the skeleton's, re-saving a walk cycle would leave every
// character playing the old one until something happened to touch their meshes.
func loadModelDocument(path string) (*model.ModelDoc, error) {
	doc, err := pristineModelDocument(path)
	if err != nil || doc == nil {
		return doc, err
	}
	return doc.Clone(), nil
}

// pristineModelDocument is path's parsed document as the cache holds it —
// shared, and so READ-ONLY to the caller, which is what lets the entity loop
// skip an 800 KB clone per static instance. loadModelDocument is the same with
// a copy the caller may change. Safe on any goroutine: the parse runs outside
// the lock, and two goroutines parsing one file at once both store the same
// thing.
func pristineModelDocument(path string) (*model.ModelDoc, error) {
	info, statErr := os.Stat(path)
	if statErr == nil {
		if doc := cachedModelDocument(path, info); doc != nil {
			return doc, nil
		}
	}
	var doc *model.ModelDoc
	var err error
	if strings.EqualFold(filepath.Ext(path), model.ModelExt) {
		doc, err = model.LoadModel(path)
	} else {
		doc, err = model.LoadOBJMesh(path)
	}
	if err != nil || statErr != nil {
		return doc, err
	}
	modelDocCache.Lock()
	modelDocCache.docs[path] = modelDocEntry{
		doc:      doc,
		size:     info.Size(),
		mod:      info.ModTime(),
		skeleton: skeletonStamp(path, doc.Skeleton),
	}
	modelDocCache.Unlock()
	return doc, nil
}

// skeletonStamp is a linked skeleton file's size and modification time, folded
// into one comparable value. Zero for an unlinked model, and for a skeleton
// that cannot be stat'd — which is a file that will fail to load anyway, and
// the load is where that gets reported.
//
// Resolved through the *model's* path, not the bare name, for the same reason
// LoadModel does: a project's skeletons sit beside its models, and the
// configured resources directory is only the fallback. Stamping the wrong file
// would read as "unchanged" forever.
func skeletonStamp(modelPath, name string) int64 {
	if name == "" {
		return 0
	}
	info, err := os.Stat(model.SkeletonPathFor(modelPath, name))
	if err != nil {
		return 0
	}
	return info.Size() ^ info.ModTime().UnixNano()
}

// modelDocCache holds the pristine parsed documents loadModelDocument clones
// and pristineModelDocument shares.
// Locked because an entity can be spawned from a script as well as loaded.
var modelDocCache = struct {
	sync.Mutex
	docs map[string]modelDocEntry
}{docs: map[string]modelDocEntry{}}

type modelDocEntry struct {
	doc  *model.ModelDoc
	size int64
	mod  time.Time
	// skeleton stamps the .fskl this document links, or 0 when it links none.
	skeleton int64
}

// cachedModelDocument is path's cached (pristine, shared) document, or nil
// when there is none or the file has changed since it was parsed.
func cachedModelDocument(path string, info os.FileInfo) *model.ModelDoc {
	modelDocCache.Lock()
	defer modelDocCache.Unlock()
	e, ok := modelDocCache.docs[path]
	if !ok || e.size != info.Size() || !e.mod.Equal(info.ModTime()) {
		return nil
	}
	if e.skeleton != skeletonStamp(path, e.doc.Skeleton) {
		return nil
	}
	return e.doc
}

// UniqueModelName returns base, or base with a numeric suffix no attachment on
// the entity is using. Attachment names address a model from a script, so two
// sharing one would make that address ambiguous.
func UniqueModelName(e *Entity, base string) string {
	if base == "" {
		base = "model"
	}
	if e == nil || e.Model(base) == nil {
		return base
	}
	for n := 2; ; n++ {
		name := fmt.Sprintf("%s_%d", base, n)
		if e.Model(name) == nil {
			return name
		}
	}
}

// ModelNameForPath is the attachment name a newly picked mesh suggests: the
// file's own name, so an entity wearing models/barrel.fmdl has an attachment
// called "barrel" rather than "model_2".
func ModelNameForPath(path string) string {
	base := filepath.Base(filepath.ToSlash(path))
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "" || base == "." {
		return "model"
	}
	return base
}

// UniqueAnimationName is UniqueModelName for an attachment's animation list.
func UniqueAnimationName(m *EntityModel, base string) string {
	if base == "" {
		base = "animation"
	}
	if m == nil || m.Animation(base) == nil {
		return base
	}
	for n := 2; ; n++ {
		name := fmt.Sprintf("%s_%d", base, n)
		if m.Animation(name) == nil {
			return name
		}
	}
}
