// Package savefile is a save game on disk: what one holds, and the slots they
// are kept in.
//
// # What a save is
//
// A save is not the level. It names the level, and holds what play has changed
// in it: where every object stands and what it is doing, every script's
// scratch and pending timer, the player, the screens up. Restoring one loads
// the level from its own file exactly as load_level would and then lays the
// save over it (internal/savegame). So a save is small, a compiled level and
// an authored one restore the same way, and a level that is edited after the
// save was made still loads — its unchanged objects are put back where they
// were, and anything new is simply where the author put it.
//
// A save is also allowed to hold nothing but the name. That is the start of the
// level — what a `checkpoint`-mode level saves before its first checkpoint,
// and what Retry falls back to with none reached.
//
// # Why gob
//
// For the compiled level's reason (levelio/compiled_level.go): it is only ever
// read by the engine that wrote it, and most of it is float triples. The
// header is its own gob message ahead of the body, so a load screen can list
// ten slots without decoding ten worlds.
//
// Here rather than in internal/savegame so that the script runtime can read a
// slot's header — `save_info` — without importing the code that restores one,
// which imports the script runtime.
package savefile

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"foundry/internal/fileutil"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// Version is bumped whenever the body changes shape in a way an older reader
// would get wrong. A save of another version is refused rather than
// half-restored.
const Version = 1

// DirName is the folder saves live in, under the state directory — beside
// settings.yaml, so a release build's saves are in the player's own data
// directory and a development build's are in the project.
const DirName = "saves"

// Ext is a save file's extension.
const Ext = ".fsav"

// AutosaveSlot is the slot every checkpoint writes.
const AutosaveSlot = "autosave"

var magic = []byte("FSAV")

// Kind is how a save came to be made.
type Kind uint8

const (
	// KindManual is a save the player asked for.
	KindManual Kind = iota
	// KindCheckpoint is the save a checkpoint takes.
	KindCheckpoint
)

// KindNames are what `save_info` reports, indexed by Kind.
var KindNames = []string{"manual", "checkpoint"}

// Header is what a slot says about itself without being loaded.
type Header struct {
	Version int
	// Level is the level's name, as load_level takes it.
	Level string
	Kind  Kind
	// Checkpoint is the checkpoint this save restores to: the one it was taken
	// at, for a checkpoint's own save and for a `checkpoint`-mode level's; ""
	// for an exact save, and for the start of a level.
	Checkpoint string
	Saved      time.Time
	// PlayTime is the level's play clock at the save, in seconds.
	PlayTime float64
}

// Snapshot is a whole save.
type Snapshot struct {
	Header
	// Fresh is a save holding nothing but the level's name: loading it is a
	// restart. Everything below is empty.
	Fresh bool

	World  WorldState
	Player PlayerState
	Script ScriptState
	UI     []ScreenState

	// LastCheckpoint is the checkpoint the player had reached when this save
	// was taken, so that dying after loading a save retries from the checkpoint
	// rather than from the save. nil when there had been none — and on a
	// checkpoint's own save, which is itself the checkpoint.
	LastCheckpoint *Snapshot
}

// ---------- world ----------

// WorldState is the level's objects as play has left them.
//
// Where a world type is nothing but data it is stored whole: then a field
// added to it later is saved without anyone remembering to. Where a type also
// holds GPU buffers, voices, particle systems or its type's templates, the
// state has its own record of the parts play changes.
type WorldState struct {
	Entities    []EntityState
	Chunks      []ChunkState
	Volumes     []VolumeState
	PointLights []world.PointLight
	DirLights   []world.DirectionalLight
	GlobalLight world.GlobalLight
	Sprites     []world.Sprite
	Emitters    []EmitterState
	Sounds      []SoundState
	Effects     []world.Effect
	// Decals are the level's projected marks, each with its built mesh cleared
	// (savegame.bareDecal): the mesh is clipped out of the level's geometry and
	// holds GPU buffers, so it is rebuilt on load rather than stored. Only the
	// lasting ones are here — the bullet holes a firefight left are Transient and
	// are no more level state than a spent one-shot sound is.
	Decals     []world.Decal
	Sky        *world.Sky
	Music      *MusicState
	StepHeight float32
	// Camera is the player's camera block, which a script may switch during
	// play (`@level.prop.camera_mode`, `camera_distance`). Gob leaves it zero
	// in a save written before it existed, and zero is "the level said
	// nothing" — first person.
	Camera world.PlayerCamera
	// Gravity is the level's ambient pull, which a script may have changed
	// during play (`@level.prop.gravity`). A pointer, as on the level itself:
	// nil is a level that never set one, and gob writes it as absent rather
	// than as a zero that would restore every level into free fall.
	Gravity *float32
	// ViewDistance is the level's far plane, which a script may have changed
	// during play (`@level.prop.view_distance`); 0 is the engine default.
	ViewDistance float32
}

// EntityState is one entity. Entity is the entity itself with everything that
// is a template or a GPU resource cleared — models, brushes, emitters, volumes,
// waypoint animations — since those come from its type and the level file.
// Its Type is what a spawned one is made again from.
type EntityState struct {
	Entity world.Entity
	Models []ModelState
	// HasAI and HasVehicle say the entity had an AI block and a vehicle spec.
	// gob writes a pointer to a zero struct as nil, and an `ai:` block
	// switched off with every other field at its default is exactly that.
	HasAI      bool
	HasVehicle bool
}

// ModelState is what play can change about one model attachment.
type ModelState struct {
	Name     string
	Path     string
	Texture  string
	Offset   mgl32.Vec3
	Rotation mgl32.Vec3
	Scale    mgl32.Vec3
	Alpha    float32
	Visible  bool
	// UVScroll and UVOffset are the texture's scroll rate and how far it has
	// slid (world.EntityModel).
	UVScroll mgl32.Vec2
	UVOffset mgl32.Vec2
	// Clip is the animation playing, by name, or "".
	Clip    string
	Time    float32
	Speed   float32
	Playing bool
	Loop    bool
}

// ChunkState is one chunk. Chunk is the chunk with its brushes cleared; the
// brushes play can move are in Brushes.
type ChunkState struct {
	Chunk   world.Chunk
	Brushes []BrushState
}

// BrushState is the run-time half of one brush — see world.Brush's note on
// Offset. Index is where it sat in its chunk, with ID there to check it.
type BrushState struct {
	Index           int
	ID              string
	Tags            []string
	Attributes      world.AttrSet
	Solid           bool
	Dynamic         bool
	Offset          mgl32.Vec3
	Rotation        mgl32.Vec3
	Scale           mgl32.Vec3
	Velocity        mgl32.Vec3
	AngularVelocity mgl32.Vec3
	Waypoints       *world.WaypointPlayback
	Tweens          world.Tweens
}

// VolumeState is what play can change about a volume. Its scatter, doorway
// and objective are authored and stay as the level file has them.
type VolumeState struct {
	ID           string
	FromEntity   string
	Tags         []string
	Attributes   world.AttrSet
	Script       string
	Attach       world.Attach
	Position     mgl32.Vec3
	Rotation     mgl32.Vec3
	HalfX        float32
	HalfY        float32
	HalfZ        float32
	Enabled      bool
	LoadLevel    string
	Checkpoint   bool
	Fog          *world.VolumeFog
	Cloud        *world.VolumeCloud
	ClearsClouds bool
	// Gravity is the region's pull override, nil when it has none.
	Gravity   *float32
	Waypoints *world.WaypointPlayback
}

// EmitterState is an emitter less its live particle systems, which are
// cosmetic and start over.
type EmitterState struct {
	ID         string
	Tags       []string
	Attributes world.AttrSet
	Effect     string
	Position   mgl32.Vec3
	Rotation   mgl32.Vec3
	Enabled    bool
	Emitting   bool
	RateScale  float32
	Tint       mgl32.Vec3
	Attach     world.Attach
	Waypoints  *world.WaypointPlayback
}

// SoundState is a sound less its voice. A sound that was playing starts again
// from the top: the mixer has no way to start a clip part-way through.
type SoundState struct {
	ID         string
	Tags       []string
	Attributes world.AttrSet
	Clip       string
	Position   mgl32.Vec3
	Rotation   mgl32.Vec3
	Enabled    bool
	Playing    bool
	Loop       bool
	Gain       float32
	Pitch      float32
	Attach     world.Attach
	Waypoints  *world.WaypointPlayback
}

// MusicState is the level's track, if it has one.
type MusicState struct {
	Track string
	Gain  float32
	Loop  bool
	Fade  float32
	// A tracker song also keeps where it was and what scripts set: the
	// section it was in, the order position and row to resume at, and the
	// params and channel mutes last asked for. Gob reads an older save
	// without them as zero, which is "from the top, as authored".
	Section    string
	Order, Row int
	Playing    bool
	Params     map[string]float32
	Mutes      map[string]bool
	// Tick is where a score was (internal/score/play's 960 a quarter, in
	// playing order); its Section, Playing, Params and Mutes (lines) are
	// the song's.
	Tick int64
}

// ---------- player ----------

// PlayerState is the engine's player, which outlives every level and so is not
// among the level's entities.
type PlayerState struct {
	Entity world.Entity
	Models []ModelState
	// Yaw and Pitch are where the player is looking.
	Yaw, Pitch float32
	// Control, Gravity and Fly are `@level.prop.player_control`,
	// `player_gravity` and `player_fly`.
	//
	// Fly is the newest of the three and is absent from every save written
	// before it existed, where it reads back false — which is the right
	// answer, since a save taken then was taken by a player on their feet.
	Control, Gravity, Fly bool
}

// ---------- scripts ----------

// ScriptState is the script runtime's memory.
type ScriptState struct {
	// Elapsed is the play clock `time()` reads and gameplay timers are due
	// against; StackElapsed is the screen stack's.
	Elapsed      float64
	StackElapsed float64
	State        []RefState
	Timers       []Timer
	// Spawned are the objects that have already heard `:spawn:`, so it is not
	// posted to them a second time.
	Spawned []Ref
	// Overlaps are who was inside what, so walking out of a volume after a
	// restore is an `:exit:` and standing in one is not a fresh `:enter:`.
	Overlaps [][2]Ref
	// Checkpoints are the checkpoint volumes the player has reached.
	Checkpoints []string
	// View is the camera and the pointer as scripts left them. Nil is a save
	// from before there was one — and reads back as the player's own camera
	// with the cursor captured, which is what every such save was taken with.
	View *ViewState
}

// ViewState is the script camera (gfx/camera_rig.go) and `@level.prop.cursor`.
// Kept because a level that detaches the camera or frees the cursor does it
// once, in `:spawn:`, and `:spawn:` is not heard again after a restore.
//
// A blend in flight is not: the camera is saved where it is, and a move that
// was halfway somewhere is simply there. Orbit and look are, with what they
// follow, since a script adjusting an orbit every frame would otherwise find
// none to adjust.
type ViewState struct {
	Cursor   bool
	Detached bool
	// Position, Yaw, Pitch and Roll are the camera's pose, angles in radians.
	Position         [3]float32
	Yaw, Pitch, Roll float32
	Orbit            *OrbitState
	Look             *ViewTarget
}

// OrbitState is an orbit the camera was holding, angles in radians.
type OrbitState struct {
	Target               ViewTarget
	Distance, Yaw, Pitch float32
}

// ViewTarget is what the camera orbits or looks at: a fixed point, or an
// object it follows (Ref set) that was last seen at Point.
type ViewTarget struct {
	Point [3]float32
	Ref   *Ref
}

// RefState is one object's `@x.state` dict.
type RefState struct {
	Ref   Ref
	State Value
}

// Timer is one pending `after(...)`.
type Timer struct {
	Due  float64
	To   Ref
	Atom string
	Args []Value
	File string
	Line int
	Col  int
	// OnStack says it counts against the screen stack's clock.
	OnStack bool
}

// Ref is a flang ref.
type Ref struct {
	Kind    uint8
	A, B, C string
}

// Value is a flang value. T is its flang.Type; the one field that type uses
// is set. A list, a tuple and a dict all keep Elems, the dict with its keys
// beside them.
//
// Two state dicts that shared one list come back holding a copy each — the
// only thing a tree cannot say. No shipped script relies on that sharing.
type Value struct {
	T     uint8
	I     int64
	F     float64
	S     string
	B     bool
	V     [3]float64
	Ref   Ref
	Elems []Value
	Keys  []string
}

// ---------- screens ----------

// ScreenState is one screen that was up, and what scripts had written to its
// widgets.
type ScreenState struct {
	ID    string
	Nodes []NodeState
}

// NodeState is the script-writable part of one widget.
type NodeState struct {
	ID         string
	Visible    bool
	Enabled    bool
	Alpha      float32
	Text       string
	TextColor  mgl32.Vec4
	Color      mgl32.Vec4
	Texture    string
	Value      float32
	Min, Max   float32
	On         bool
	FillColor  mgl32.Vec4
	Attributes world.AttrSet
}

// ---------- slots ----------

// Dir is the saves folder.
func Dir() string { return fileutil.StatePath(DirName) }

// ErrNoSave is a slot that has nothing in it.
var ErrNoSave = errors.New("no save in that slot")

// ValidSlot keeps a slot name a file name: a script is choosing it, and "../x"
// is not a slot.
func ValidSlot(slot string) error {
	if slot == "" {
		return fmt.Errorf("a save slot needs a name")
	}
	if strings.ContainsAny(slot, `/\:*?"<>|`) || strings.HasPrefix(slot, ".") {
		return fmt.Errorf("save slot %q: a slot name cannot hold a path", slot)
	}
	return nil
}

// Path is a slot's file.
func Path(slot string) string { return filepath.Join(Dir(), slot+Ext) }

// Encode is a snapshot as the bytes Write puts on disk.
func Encode(s *Snapshot) ([]byte, error) {
	var buf bytes.Buffer
	buf.Write(magic)
	enc := gob.NewEncoder(&buf)
	h := s.Header
	h.Version = Version
	if err := enc.Encode(&h); err != nil {
		return nil, fmt.Errorf("encoding save header: %w", err)
	}
	body := *s
	body.Header = h
	if err := enc.Encode(&body); err != nil {
		return nil, fmt.Errorf("encoding save: %w", err)
	}
	return buf.Bytes(), nil
}

// Decode is Encode's inverse.
func Decode(data []byte) (*Snapshot, error) {
	dec, err := decoder(data)
	if err != nil {
		return nil, err
	}
	var h Header
	if err := dec.Decode(&h); err != nil {
		return nil, fmt.Errorf("reading save header: %w", err)
	}
	if h.Version != Version {
		return nil, fmt.Errorf("save is version %d; this build reads version %d", h.Version, Version)
	}
	var s Snapshot
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("reading save: %w", err)
	}
	return &s, nil
}

func decoder(data []byte) (*gob.Decoder, error) {
	if !bytes.HasPrefix(data, magic) {
		return nil, fmt.Errorf("not a save file")
	}
	return gob.NewDecoder(bytes.NewReader(data[len(magic):])), nil
}

// Write saves a snapshot to a slot, replacing what was there.
func Write(slot string, s *Snapshot) error {
	data, err := Encode(s)
	if err != nil {
		return err
	}
	return WriteData(slot, data)
}

// WriteData writes an encoded snapshot to a slot. The write is atomic, so a
// crash part-way leaves the old save rather than half of the new.
func WriteData(slot string, data []byte) error {
	if err := ValidSlot(slot); err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(Path(slot), data)
}

// Read loads a slot.
func Read(slot string) (*Snapshot, error) {
	data, err := ReadData(slot)
	if err != nil {
		return nil, err
	}
	return Decode(data)
}

// ReadData reads a slot's bytes, undecoded.
func ReadData(slot string) ([]byte, error) {
	if err := ValidSlot(slot); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(Path(slot))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoSave
	}
	return data, err
}

// ReadHeader reads only what a slot says about itself.
func ReadHeader(slot string) (Header, error) {
	if err := ValidSlot(slot); err != nil {
		return Header{}, err
	}
	f, err := os.Open(Path(slot))
	if errors.Is(err, os.ErrNotExist) {
		return Header{}, ErrNoSave
	}
	if err != nil {
		return Header{}, err
	}
	defer f.Close()
	head := make([]byte, len(magic))
	if _, err := io.ReadFull(f, head); err != nil || !bytes.Equal(head, magic) {
		return Header{}, fmt.Errorf("not a save file")
	}
	var h Header
	if err := gob.NewDecoder(f).Decode(&h); err != nil {
		return Header{}, fmt.Errorf("reading save header: %w", err)
	}
	if h.Version != Version {
		return Header{}, fmt.Errorf("save is version %d; this build reads version %d", h.Version, Version)
	}
	return h, nil
}

// Delete empties a slot. An empty slot is not an error.
func Delete(slot string) error {
	if err := ValidSlot(slot); err != nil {
		return err
	}
	err := os.Remove(Path(slot))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Slot is one filled slot, as List reports it.
type Slot struct {
	Name string
	Header
}

// List is every readable save, newest first. A file that will not read — an
// older version, a stray — is left out rather than failing the list.
func List() []Slot {
	entries, err := os.ReadDir(Dir())
	if err != nil {
		return nil
	}
	var out []Slot
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), Ext)
		if !ok || e.IsDir() {
			continue
		}
		h, err := ReadHeader(name)
		if err != nil {
			continue
		}
		out = append(out, Slot{Name: name, Header: h})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Saved.After(out[j].Saved) })
	return out
}
