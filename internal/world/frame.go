package world

// frame.go — the coordinate systems a level has.
//
// Until now there was exactly one: the world, with every chunk's geometry baked
// into it at load and every entity's position expressed in it. A frame is a
// second one — a rigid transform that geometry and entities can live in, carried
// by an entity that moves.
//
// The point is not the transform, which is trivial. It is that a body inside a
// frame stores its position *in that frame's coordinates*, so the whole existing
// collision path — which takes a chunk, a sector and a position and knows
// nothing about which space they are in — resolves a player walking around a
// moving ship with no change at all. Local gravity is local −Y. A rider is
// carried exactly, because nothing had to carry it: it never moved.
//
// A frame may sit inside another, which is how a shuttle in a carrier's hangar
// works. Nothing downstream sees the nesting — render and collision only ever
// get a final matrix and a local space — but building the transforms does, and
// three rules keep that honest: parents are always resolved before children, a
// frame may not end up inside itself, and the chain is depth-limited.

import (
	"fmt"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// MaxFrameDepth bounds how deeply frames may nest. Four is world → carrier →
// shuttle → cargo pod, which is further than anything has needed to go; the cap
// exists so a malformed level cannot make transform composition unbounded.
const MaxFrameDepth = 4

// WorldFrameName is how a portal names the world as its destination.
//
// A frame is normally named by the entity carrying it, and the world is carried
// by nothing — but "" already means "this portal does not cross frames at all",
// so the world needs a name of its own to be a target. It is reserved: an
// entity called "world" cannot own a frame, which the level loader reports.
const WorldFrameName = "world"

// FrameID normalises a frame name to the ID an entity stores: the reserved
// world name becomes the empty string every world-space entity already carries.
func FrameID(name string) string {
	if name == WorldFrameName {
		return ""
	}
	return name
}

// Frame is one coordinate system. The world is the frame with no owner.
//
// ChunkIdx indexes into Level.Chunks rather than holding chunks, because a
// Chunk carries live GPU buffer handles that must not be copied and the level
// is rebuilt wholesale by undo and by reload — an index survives both, and a
// pointer survives neither.
type Frame struct {
	// OwnerID is the entity whose local space this is; "" for the world.
	OwnerID string
	// Parent is the frame OwnerID's own position is expressed in.
	Parent string
	// Depth is 0 for the world, 1 for a frame carried by a world-space entity,
	// and so on.
	Depth int

	// World takes a point from this frame into world space; Inv is its inverse.
	// Rot is the rotation alone, for carrying directions rather than points.
	World mgl32.Mat4
	Inv   mgl32.Mat4
	Rot   mgl32.Quat

	// ChunkIdx are the chunks whose geometry is authored in this frame.
	ChunkIdx []int
}

// IsWorld reports whether this is the world frame. A nil Frame reads as the
// world, so every caller that does not care about frames can pass nil.
func (f *Frame) IsWorld() bool { return f == nil || f.OwnerID == "" }

// ToWorld carries a point out of this frame.
func (f *Frame) ToWorld(p mgl32.Vec3) mgl32.Vec3 {
	if f.IsWorld() {
		return p
	}
	r := f.World.Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// FromWorld carries a point into this frame.
func (f *Frame) FromWorld(p mgl32.Vec3) mgl32.Vec3 {
	if f.IsWorld() {
		return p
	}
	r := f.Inv.Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r.X(), r.Y(), r.Z()}
}

// DirToWorld and DirFromWorld carry a direction, which is a rotation only —
// translating a direction would be meaningless.
func (f *Frame) DirToWorld(d mgl32.Vec3) mgl32.Vec3 {
	if f.IsWorld() {
		return d
	}
	return f.Rot.Rotate(d)
}

func (f *Frame) DirFromWorld(d mgl32.Vec3) mgl32.Vec3 {
	if f.IsWorld() {
		return d
	}
	return f.Rot.Inverse().Rotate(d)
}

// FrameSet is a level's frames, indexed by owner and ordered parents-first.
//
// It is built when the level's structure changes and refreshed every frame.
// Splitting those apart is the point: which frames exist and how they nest is a
// property of the level and changes rarely, while where they *are* changes
// sixty times a second.
type FrameSet struct {
	world  Frame
	byID   map[string]*Frame
	order  []string // topological: a parent always precedes its children
	frames []*Frame // same order, for iteration without a map lookup
}

// World is the identity frame. It is always present, and is what an entity with
// no frame lives in.
func (fs *FrameSet) World() *Frame {
	if fs == nil {
		return nil
	}
	return &fs.world
}

// Frame returns the frame with the given owner ID, or the world frame for "".
// An unknown ID yields nil, which callers must read as "this entity's frame has
// gone" rather than as the world — an entity whose vehicle was deleted is not
// suddenly standing in the level.
func (fs *FrameSet) Frame(id string) *Frame {
	if fs == nil {
		return nil
	}
	if id == "" || id == WorldFrameName {
		return &fs.world
	}
	return fs.byID[id]
}

// Order lists every non-world frame, parents before children. Anything that
// updates transforms must walk it in this order, or a child composes against
// its parent's position from the previous frame — which is exactly the bug that
// shoves a rider through the deck the moment the ship pitches.
func (fs *FrameSet) Order() []*Frame {
	if fs == nil {
		return nil
	}
	return fs.frames
}

// FrameFor returns the frame an entity's coordinates are expressed in.
func (fs *FrameSet) FrameFor(e *Entity) *Frame {
	if fs == nil || e == nil {
		return nil
	}
	return fs.Frame(e.Frame)
}

// WorldPosition is an entity's position in world space, whatever frame it is
// stored in. Anything comparing two entities that might be in different frames
// has to go through this — a local position and a world one are not numbers
// that can be subtracted.
func (fs *FrameSet) WorldPosition(e *Entity) mgl32.Vec3 {
	if e == nil {
		return mgl32.Vec3{}
	}
	return fs.FrameFor(e).ToWorld(e.Position)
}

// WorldBoundCenter is the same for the centre of an entity's bounding volume.
func (fs *FrameSet) WorldBoundCenter(e *Entity) mgl32.Vec3 {
	if e == nil {
		return mgl32.Vec3{}
	}
	return fs.FrameFor(e).ToWorld(e.BoundCenter())
}

// BuildFrames works out which frames a level has and how they nest.
//
// A frame exists for every entity that owns one — which for now means every
// entity named as a chunk's FrameOwner. Its parent is that entity's own frame,
// so nesting falls out of the same field that puts an entity in a frame at all.
//
// It returns an error for a chunk naming an entity that does not exist, for a
// cycle, and for nesting past MaxFrameDepth. All three are authoring mistakes
// that would otherwise show up as a hang or as geometry in the wrong place, and
// none of them can be resolved by guessing.
func BuildFrames(lvl *Level) (*FrameSet, error) {
	fs := &FrameSet{byID: map[string]*Frame{}}
	fs.world.World = mgl32.Ident4()
	fs.world.Inv = mgl32.Ident4()
	fs.world.Rot = mgl32.QuatIdent()

	if lvl == nil {
		return fs, nil
	}

	// Which entities own a frame, and which chunks belong to each.
	for ci := range lvl.Chunks {
		owner := lvl.Chunks[ci].FrameOwner
		if owner == "" {
			continue
		}
		if owner == WorldFrameName {
			// Reserved, because a portal has to be able to name the world as a
			// destination and "" already means "does not cross frames".
			continue
		}
		f := fs.byID[owner]
		if f == nil {
			f = &Frame{OwnerID: owner, World: mgl32.Ident4(), Inv: mgl32.Ident4(), Rot: mgl32.QuatIdent()}
			fs.byID[owner] = f
		}
		f.ChunkIdx = append(f.ChunkIdx, ci)
	}
	if len(fs.byID) == 0 {
		return fs, nil
	}

	// Each frame's parent is its owner entity's own frame.
	for id, f := range fs.byID {
		owner := FindEntity(lvl, id)
		if owner == nil {
			return nil, fmt.Errorf("chunk frame owner %q: no entity in this level has that ID", id)
		}
		f.Parent = owner.Frame
	}

	order, err := frameOrder(fs.byID)
	if err != nil {
		return nil, err
	}
	fs.order = order
	fs.frames = make([]*Frame, 0, len(order))
	for _, id := range order {
		fs.frames = append(fs.frames, fs.byID[id])
	}
	return fs, nil
}

// frameOrder sorts frames parents-first, and is where cycles and over-deep
// nesting are caught: a frame whose parent chain cannot be walked to the world
// within MaxFrameDepth is one or the other.
func frameOrder(byID map[string]*Frame) ([]string, error) {
	depth := map[string]int{}

	var resolve func(id string, seen map[string]bool) (int, error)
	resolve = func(id string, seen map[string]bool) (int, error) {
		if d, ok := depth[id]; ok {
			return d, nil
		}
		if seen[id] {
			return 0, fmt.Errorf("frame %q is inside itself", id)
		}
		seen[id] = true
		defer delete(seen, id)

		f := byID[id]
		d := 1
		if f.Parent != "" {
			parent, ok := byID[f.Parent]
			if !ok || parent == nil {
				// The parent entity is in the world, or in a frame nothing owns
				// geometry in. Either way this frame sits one level up from the
				// world as far as composition is concerned.
				d = 1
			} else {
				pd, err := resolve(f.Parent, seen)
				if err != nil {
					return 0, err
				}
				d = pd + 1
			}
		}
		if d > MaxFrameDepth {
			return 0, fmt.Errorf("frame %q nests %d deep; the limit is %d", id, d, MaxFrameDepth)
		}
		depth[id] = d
		f.Depth = d
		return d, nil
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic, so a level's frames order the same every load
	for _, id := range ids {
		if _, err := resolve(id, map[string]bool{}); err != nil {
			return nil, err
		}
	}

	// Stable sort by depth: equal depths keep the ID order above, so the result
	// is the same on every load.
	out := make([]string, 0, len(ids))
	for d := 1; d <= MaxFrameDepth; d++ {
		for _, id := range ids {
			if depth[id] == d {
				out = append(out, id)
			}
		}
	}
	return out, nil
}

// RefreshFrames recomputes every frame's transform from its owner entity, and
// republishes it onto the chunks that frame owns.
//
// Walked parents-first, so a nested frame composes against a parent that has
// already been brought up to date this tick.
func RefreshFrames(lvl *Level, fs *FrameSet) {
	if lvl == nil || fs == nil {
		return
	}
	for _, f := range fs.frames {
		owner := FindEntity(lvl, f.OwnerID)
		if owner == nil {
			// The owning entity was destroyed mid-play. Leave the frame where it
			// last was rather than snapping its contents to the origin; the level
			// is rebuilt on the next structural change and the frame goes with it.
			continue
		}
		local := FrameLocalMatrix(owner)
		f.World = fs.parentTransform(lvl, f.Parent, 0).Mul4(local)
		f.Inv = f.World.Inv()
		f.Rot = mgl32.Mat4ToQuat(rotationOnly(f.World))

		for _, ci := range f.ChunkIdx {
			lvl.Chunks[ci].Xform = f.World
		}
	}
}

// parentTransform is the transform a frame's parent contributes.
//
// Usually that is simply the parent frame's own, already refreshed because the
// walk is parents-first. But an entity can be *in* a frame without *owning* one
// — a shuttle riding in a carrier that has no interior of its own — and then
// there is no parent Frame to read. Composing the parent entity's local matrix
// directly is what keeps the chain unbroken in that case, rather than silently
// dropping a level of nesting and drawing the shuttle at the world origin.
func (fs *FrameSet) parentTransform(lvl *Level, parentID string, depth int) mgl32.Mat4 {
	if parentID == "" || depth > MaxFrameDepth {
		return mgl32.Ident4()
	}
	if f := fs.byID[parentID]; f != nil {
		return f.World
	}
	e := FindEntity(lvl, parentID)
	if e == nil {
		return mgl32.Ident4()
	}
	return fs.parentTransform(lvl, e.Frame, depth+1).Mul4(FrameLocalMatrix(e))
}

// WorldMatrix is a frame's transform, and the identity for the world frame or a
// nil one — so a caller holding a frame that may not exist can compose without
// a nil check at every site.
func (f *Frame) WorldMatrix() mgl32.Mat4 {
	if f == nil {
		return mgl32.Ident4()
	}
	return f.World
}

// FrameLocalMatrix is the transform an entity contributes as a frame carrier:
// its translation and rotation, and deliberately not its scale.
//
// Scale is left out because a frame is a place to stand, not a model. Scaling
// one would scale the geometry a body walks on without scaling the body, so a
// half-size ship would have a player's head through its ceiling — and the
// bounding volumes the resolvers work in are not expressed in the frame's units
// at all.
func FrameLocalMatrix(e *Entity) mgl32.Mat4 {
	T := mgl32.Translate3D(e.Position.X(), e.Position.Y(), e.Position.Z())
	return T.Mul4(e.RotationMatrix())
}

// rotationOnly strips the translation from a rigid transform, leaving the basis
// a direction should be carried through.
func rotationOnly(m mgl32.Mat4) mgl32.Mat4 {
	m[12], m[13], m[14] = 0, 0, 0
	return m
}

// ChunkAndSectorAt locates a position expressed in this frame's coordinates.
//
// The world frame goes through the spatial hash it has always used; a carried
// frame scans its own handful of chunks. Both answer in the same coordinates
// they were asked in, which is the whole reason the resolvers downstream need no
// changes.
func (f *Frame) ChunkAndSectorAt(lvl *Level, pos mgl32.Vec3) (*Chunk, *Sector) {
	if lvl == nil {
		return nil, nil
	}
	if f.IsWorld() {
		return FindChunkAndSectorSH(lvl.Spatial, lvl.Chunks, pos)
	}
	return FindChunkAndSectorAmong(lvl.Chunks, f.ChunkIdx, pos)
}

// RebuildIndexes rebuilds a level's derived lookup structures: the world
// spatial hash and the frame set.
//
// They are rebuilt together because they come from the same structural fact —
// which chunks are in world space and which are in a frame. Rebuilding one
// without the other leaves a chunk indexed as world geometry while being drawn
// through a transform, or the reverse, and both are silent.
//
// An error means the level's frames are malformed (a chunk owned by an entity
// that does not exist, a cycle, or nesting too deep). The spatial hash is still
// rebuilt, so the world half of the level stays usable and the caller can
// decide whether to refuse the level or carry on without frames.
func RebuildIndexes(lvl *Level) error {
	if lvl == nil {
		return nil
	}
	lvl.Spatial = BuildSpatialHash(lvl.Chunks, 8.0)
	fs, err := BuildFrames(lvl)
	if err != nil {
		lvl.Frames = &FrameSet{byID: map[string]*Frame{}}
		lvl.Frames.world.World = mgl32.Ident4()
		lvl.Frames.world.Inv = mgl32.Ident4()
		lvl.Frames.world.Rot = mgl32.QuatIdent()
		return err
	}
	lvl.Frames = fs
	RefreshFrames(lvl, fs)
	return nil
}
