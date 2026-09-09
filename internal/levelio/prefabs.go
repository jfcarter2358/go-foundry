package levelio

// prefabs.go — turning a level's prefab instances into level content.
//
// A prefab file holds a piece of a level authored around its own origin:
// brushes, entities, volumes and point lights (world/prefab.go). A level stores
// only *placements* of it, and this is what makes those real — the brushes
// become one chunk per instance, the rest are stamped onto the level's own
// lists, and everything produced is tagged with the instance it came from so
// that saving writes the placement back rather than a frozen copy of the
// content.
//
// It lives here rather than in internal/world because it reads files, and
// internal/world must not depend on the loader. Every structural decision it
// mirrors is entity_chunks.go's, deliberately: the two are the same idea
// applied to a type and to a file, and an author who has understood one should
// recognise the other.
//
// # Why the IDs are rewritten
//
// Everything a prefab names is unique only *within the prefab*. Twenty street
// lamps from one file would otherwise be twenty lights called "bulb", and `@bulb`
// would mean nothing. Every ID is prefixed with the instance's own, so the
// third lamp's light is `lamp3_bulb`: readable, addressable, and sorted beside
// the rest of what that instance made.
//
// # Why positions are carried rather than left to a parent transform
//
// Only the brushes get a placement they can hold — a chunk has an Origin,
// a Rotation and a Scale, so the instance's transform maps straight onto it.
// An entity, a volume and a light are level-scoped and world-positioned, with
// no parent to inherit from, so their positions are carried through the
// instance's matrix at materialise time.
//
// That means moving an instance re-stamps it rather than updating a transform,
// which is the trade this makes: a prefab is content the level *has*, not an
// indirection every downstream pass has to learn about. See prefab.go's own
// note on why.

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// PrefabsDirName is the directory under the install root holding prefab files,
// and prefabFileExt the extension they carry.
const (
	PrefabsDirName = "prefabs"
	prefabFileExt  = ".yaml"
)

// PrefabsDir is that directory's absolute path.
func PrefabsDir() string { return filepath.Join(fileutil.ExecutablePath, PrefabsDirName) }

// PrefabFilePath resolves a prefab name — with or without its extension — to a
// path under prefabs/.
func PrefabFilePath(name string) string {
	if !strings.EqualFold(filepath.Ext(name), prefabFileExt) {
		name += prefabFileExt
	}
	return filepath.Join(PrefabsDir(), name)
}

// ScanPrefabNames lists the prefab files available to place, extension trimmed
// and sorted — what the editor's picker offers.
func ScanPrefabNames() []string {
	files := fileutil.ScanFilesByExt(PrefabsDir(), prefabFileExt)
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, strings.TrimSuffix(f, prefabFileExt))
	}
	return names
}

// prefabCache holds the prefab files read this session, keyed by resolved
// PATH rather than by name.
//
// Dragging an instance re-stamps it on every motion event (see
// editor_prefab.go on why), and re-stamping re-reads the file — so without
// this a drag is a YAML parse per frame. A prefab file does not change under
// the editor except when the editor itself saves it, which invalidates, or
// when someone edits it on disk, which is what the explicit "Re-stamp from
// file" action is for.
//
// The path rather than the name because the install root moves: a test points
// fileutil.ExecutablePath at a temp directory, and two tests each writing a
// "street_lamp" would otherwise read each other's. Keying on the path makes
// that impossible instead of making it something every caller has to remember
// to clear.
//
// Not concurrency-guarded: every caller is the main thread, the same
// assumption gfx.Textures and world.DetailPresets already make.
var prefabCache = map[string]world.Prefab{}

// InvalidatePrefabCache forgets one prefab file, so the next read comes from
// disk. An empty name forgets all of them — what a level change wants, since
// the cache outlives the level that filled it.
func InvalidatePrefabCache(name string) {
	if name == "" {
		prefabCache = map[string]world.Prefab{}
		return
	}
	delete(prefabCache, PrefabFilePath(name))
}

// LoadPrefab reads a prefab file by name, from the cache when it can.
//
// The returned Prefab's slices are the cached ones. Every consumer copies what
// it needs before mutating (materializePrefabBrushes appends into a fresh
// slice, and the entity/volume/light loops take their raw forms by value), so
// nothing here hands out a shared slice that gets written through — but a new
// consumer has to keep that up.
func LoadPrefab(name string) (world.Prefab, error) {
	if name == "" {
		return world.Prefab{}, fmt.Errorf("prefab has no file")
	}
	path := PrefabFilePath(name)
	if p, ok := prefabCache[path]; ok {
		return p, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return world.Prefab{}, fmt.Errorf("prefab %q: %w", name, err)
	}
	var rp world.RawPrefab
	if err := yaml.Unmarshal(data, &rp); err != nil {
		return world.Prefab{}, fmt.Errorf("prefab %q: %w", name, err)
	}
	p := world.Prefab{
		Brushes:  rp.Brushes,
		Entities: rp.Entities,
		Volumes:  rp.Volumes,
		Lights:   rp.Lights,
	}
	prefabCache[path] = p
	return p, nil
}

// SavePrefab writes a prefab file by name, creating prefabs/ if it is not
// there — the same courtesy SaveChunk extends, and for the same reason: the
// first prefab someone makes is the one that would otherwise fail.
func SavePrefab(name string, p *world.Prefab) error {
	if name == "" {
		return fmt.Errorf("a prefab needs a name")
	}
	data, err := yaml.Marshal(world.RawPrefab{
		Brushes:  p.Brushes,
		Entities: p.Entities,
		Volumes:  p.Volumes,
		Lights:   p.Lights,
	})
	if err != nil {
		return fmt.Errorf("yaml marshal: %w", err)
	}
	if err := os.MkdirAll(PrefabsDir(), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", PrefabsDir(), err)
	}
	if err := os.WriteFile(PrefabFilePath(name), data, 0o644); err != nil {
		return err
	}
	// The one write that has to reach the cache. RestampPrefabsOfFile runs
	// straight after this in the editor, and a stale entry would re-stamp
	// every placement from what the file said before the save.
	InvalidatePrefabCache(name)
	return nil
}

// ---------- materialising ----------

// MaterializePrefab expands one instance into lvl and adds whatever textures it
// needs to texNeeded.
//
// A prefab that will not load costs the level that instance's content, not the
// level: the error is returned for the caller to report. That is the rule a
// missing chunk attachment follows, and for the same reason — a level that
// refuses to open over one missing prefab file is worse than one that opens
// with a hole in it and says so.
func MaterializePrefab(lvl *world.Level, inst *world.PrefabInstance, texNeeded map[string]bool) error {
	if lvl == nil || inst == nil || inst.File == "" {
		return nil // an instance with nothing in it yet
	}
	p, err := LoadPrefab(inst.File)
	if err != nil {
		return fmt.Errorf("prefab instance %q: %w", inst.ID, err)
	}
	if p.IsEmpty() {
		return nil
	}

	if err := materializePrefabBrushes(lvl, inst, &p, texNeeded); err != nil {
		return err
	}
	materializePrefabEntities(lvl, inst, &p, texNeeded)
	materializePrefabVolumes(lvl, inst, &p)
	materializePrefabLights(lvl, inst, &p)
	return nil
}

// materializePrefabBrushes builds the instance's geometry as one chunk.
//
// The chunk carries the instance's transform outright rather than having it
// baked into the verts, so a moved instance is a chunk with a new Origin — no
// re-read of the file, and no drift from repeatedly transforming the same
// numbers.
func materializePrefabBrushes(lvl *world.Level, inst *world.PrefabInstance, p *world.Prefab, texNeeded map[string]bool) error {
	if len(p.Brushes) == 0 {
		return nil
	}
	rc := world.RawChunk{
		ID:      world.PrefabChunkID(inst.ID),
		Brushes: append([]world.RawBrush(nil), p.Brushes...),
		Origin: world.RawChunkOrigin{
			X: inst.Position.X(), Y: inst.Position.Y(), Z: inst.Position.Z(),
		},
		Rotation: inst.Rotation,
		Scale:    inst.EffectiveScale(),
		Frame:    inst.Frame,
	}
	// Brush IDs are level-unique, and twenty lamps from one file would
	// otherwise all carry a brush called "post".
	for i := range rc.Brushes {
		rc.Brushes[i].ID = world.PrefabMemberID(inst.ID, rc.Brushes[i].ID)
	}

	ch, tex, err := BuildChunkFromRaw(rc)
	if err != nil {
		return fmt.Errorf("prefab instance %q: %w", inst.ID, err)
	}
	ch.ID = uniqueChunkID(lvl, ch.ID)
	ch.FromPrefab = inst.ID
	for t := range tex {
		if texNeeded != nil {
			texNeeded[t] = true
		}
	}
	lvl.Chunks = append(lvl.Chunks, ch)
	return nil
}

// materializePrefabEntities stamps the instance's entities onto the level,
// carrying each position and yaw through the instance's placement.
//
// An entity that will not load is reported and skipped rather than failing —
// the opposite of a level-authored entity, which fails the load. The difference
// is what a reader can do about it: a level file plainly listing an entity that
// vanished is a mystery, while a prefab that half-materialised names the file
// to go and fix.
func materializePrefabEntities(lvl *world.Level, inst *world.PrefabInstance, p *world.Prefab, texNeeded map[string]bool) {
	for _, re := range p.Entities {
		re.ID = world.PrefabMemberID(inst.ID, re.ID)
		e, err := world.EntityFromRaw(re, world.LoadEntityDef)
		if err != nil {
			prefabWarn("prefab instance %q: entity %q: %v", inst.ID, re.ID, err)
			continue
		}
		e.Position = prefabPoint(inst, e.Position)
		e.Rotation[1] += inst.Rotation
		e.Scale = e.Scale.Mul(inst.EffectiveScale())
		if inst.Frame != "" {
			e.Frame = inst.Frame
		}
		e.ID = world.UniqueEntityID(lvl, e.ID)
		e.FromPrefab = inst.ID
		if err := e.Load(); err != nil {
			prefabWarn("prefab instance %q: entity %q load: %v", inst.ID, e.ID, err)
		}
		lvl.Entities = append(lvl.Entities, e)

		// The decks that entity carries, on the same terms every other
		// materialised entity's are. They are tagged with the prefab too, so
		// dropping the instance takes them with it.
		for _, err := range MaterializeEntityChunks(lvl, &lvl.Entities[len(lvl.Entities)-1], texNeeded) {
			prefabWarn("%v", err)
		}
		for i := range lvl.Chunks {
			if lvl.Chunks[i].FromEntity == e.ID {
				lvl.Chunks[i].FromPrefab = inst.ID
			}
		}
	}
}

func materializePrefabVolumes(lvl *world.Level, inst *world.PrefabInstance, p *world.Prefab) {
	for _, rv := range p.Volumes {
		rv.ID = world.PrefabMemberID(inst.ID, rv.ID)
		v, err := world.VolumeFromRaw(rv)
		if err != nil {
			prefabWarn("prefab instance %q: volume %q: %v", inst.ID, rv.ID, err)
			continue
		}
		v.Position = prefabPoint(inst, v.Position)
		v.Rotation[1] += inst.Rotation
		// A volume's extents are half-sizes, so the instance's scale reaches
		// them — unlike its position, which is a point. A prefab scaled to half
		// size must not keep a full-size trigger.
		s := inst.EffectiveScale()
		v.HalfX, v.HalfY, v.HalfZ = v.HalfX*s, v.HalfY*s, v.HalfZ*s
		if inst.Frame != "" {
			v.Frame = inst.Frame
		}
		v.ID = world.UniqueVolumeID(lvl, v.ID)
		v.FromPrefab = inst.ID
		if err := v.Load(); err != nil {
			prefabWarn("prefab instance %q: volume %q: marker load: %v", inst.ID, v.ID, err)
		}
		lvl.Volumes = append(lvl.Volumes, v)
	}
}

func materializePrefabLights(lvl *world.Level, inst *world.PrefabInstance, p *world.Prefab) {
	for _, rl := range p.Lights {
		rl.ID = world.PrefabMemberID(inst.ID, rl.ID)
		pl := PointLightFromRaw(rl)
		pl.Position = prefabPoint(inst, pl.Position)
		// A radius is a distance, so it scales with the instance the way a
		// volume's extents do. A lamp shrunk to half size lighting the same
		// room would be the surprise.
		pl.Radius *= inst.EffectiveScale()
		pl.FromPrefab = inst.ID
		lvl.Lights.Points = append(lvl.Lights.Points, pl)
	}
}

// prefabWarn reports something a prefab could not stamp.
//
// A log line rather than a returned error for the same reason the level loader
// logs a bad emitter: the instance is real level content and the author needs
// to see the rest of it in order to fix the part that broke.
func prefabWarn(format string, args ...any) { log.Printf(format, args...) }

// prefabPoint carries a prefab-local point into the level's space.
func prefabPoint(inst *world.PrefabInstance, p mgl32.Vec3) mgl32.Vec3 {
	r := inst.Matrix().Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// ---------- dropping ----------

// DropPrefabContent removes and releases everything materialised from the named
// instance, and reports how many objects went.
//
// Called when the instance is deleted, moved, renamed, or re-stamped from a
// file that has since changed. Releasing the geometry matters: a materialised
// chunk holds GL buffers like any other, and dropping the slice entry alone
// would leak them once per edit.
func DropPrefabContent(lvl *world.Level, instanceID string) int {
	if lvl == nil || instanceID == "" {
		return 0
	}
	n := 0

	keptChunks := lvl.Chunks[:0]
	for i := range lvl.Chunks {
		if lvl.Chunks[i].FromPrefab == instanceID {
			lvl.Chunks[i].ReleaseGeometry()
			n++
			continue
		}
		keptChunks = append(keptChunks, lvl.Chunks[i])
	}
	lvl.Chunks = keptChunks

	keptEntities := lvl.Entities[:0]
	for i := range lvl.Entities {
		if lvl.Entities[i].FromPrefab == instanceID {
			lvl.Entities[i].Delete()
			n++
			continue
		}
		keptEntities = append(keptEntities, lvl.Entities[i])
	}
	lvl.Entities = keptEntities

	keptVolumes := lvl.Volumes[:0]
	for i := range lvl.Volumes {
		if lvl.Volumes[i].FromPrefab == instanceID {
			lvl.Volumes[i].Geometry.Delete()
			n++
			continue
		}
		keptVolumes = append(keptVolumes, lvl.Volumes[i])
	}
	lvl.Volumes = keptVolumes

	keptLights := lvl.Lights.Points[:0]
	for i := range lvl.Lights.Points {
		if lvl.Lights.Points[i].FromPrefab == instanceID {
			n++
			continue
		}
		keptLights = append(keptLights, lvl.Lights.Points[i])
	}
	lvl.Lights.Points = keptLights

	return n
}

// RestampPrefab drops an instance's content and materialises it again — what an
// edit to the instance's transform, or to the file it names, has to do.
//
// Whole rather than incremental because a prefab file can have gained and lost
// content since the last stamp, and reconciling two lists of objects that have
// been renamed and moved is more machinery than re-reading a small file.
func RestampPrefab(lvl *world.Level, instanceID string) error {
	inst := world.FindPrefabInstance(lvl, instanceID)
	if inst == nil {
		DropPrefabContent(lvl, instanceID)
		return nil
	}
	DropPrefabContent(lvl, instanceID)
	texNeeded := map[string]bool{}
	if err := MaterializePrefab(lvl, inst, texNeeded); err != nil {
		return err
	}
	// A stamp adds and removes chunks and entities, and both of the level's
	// indexes address those by position — the spatial hash's buckets and each
	// frame's ChunkIdx. Rebuilt here rather than in MaterializePrefab because
	// the load path materialises every instance before finishLevel does this
	// once for the whole level; this is the entry point that runs afterwards.
	if err := world.RebuildIndexes(lvl); err != nil {
		return err
	}
	return EnsureChunkTextures(texNeeded)
}

// RestampPrefabsOfFile re-materialises every instance naming a file, and
// reports how many it touched. This is the live link: the editor calls it after
// saving a prefab, and every placement of it updates.
func RestampPrefabsOfFile(lvl *world.Level, file string) (int, []error) {
	if lvl == nil || file == "" {
		return 0, nil
	}
	// The IDs are collected first because restamping rewrites lvl.Chunks and
	// the other lists — not lvl.Prefabs, but walking one while another is
	// rebuilt is the kind of thing that only stays true by accident.
	var ids []string
	for i := range lvl.Prefabs {
		if lvl.Prefabs[i].File == file {
			ids = append(ids, lvl.Prefabs[i].ID)
		}
	}
	var errs []error
	for _, id := range ids {
		if err := RestampPrefab(lvl, id); err != nil {
			errs = append(errs, err)
		}
	}
	return len(ids), errs
}
