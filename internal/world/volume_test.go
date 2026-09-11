package world

// volume_test.go — containment, lookup and the YAML round trip.
//
// The containment cases matter most: a volume's whole job is answering "is this
// point inside", and `enter`/`exit`/`interact` are only as correct as that
// answer. The shape math is shared with Entity.ContainsPoint, so a test here
// that disagreed with one there would mean the two had drifted.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func testVolume(shape VolumeShape) Volume {
	return Volume{
		ID:       "trigger",
		Position: mgl32.Vec3{0, 0, 0},
		Shape:    shape,
		HalfX:    2,
		HalfY:    1,
		HalfZ:    3,
		Enabled:  true,
	}
}

func TestVolumeContainsPointByShape(t *testing.T) {
	cases := []struct {
		name  string
		shape VolumeShape
		p     mgl32.Vec3
		want  bool
	}{
		{"box centre", VolumeBox, mgl32.Vec3{0, 0, 0}, true},
		{"box corner inside", VolumeBox, mgl32.Vec3{1.9, 0.9, 2.9}, true},
		{"box just outside X", VolumeBox, mgl32.Vec3{2.1, 0, 0}, false},
		{"box just outside Y", VolumeBox, mgl32.Vec3{0, 1.1, 0}, false},

		{"sphere centre", VolumeSphere, mgl32.Vec3{0, 0, 0}, true},
		// The box corner is outside the inscribed ellipsoid, which is the
		// distinction between the two shapes.
		{"sphere excludes box corner", VolumeSphere, mgl32.Vec3{1.9, 0.9, 2.9}, false},
		{"sphere on X axis", VolumeSphere, mgl32.Vec3{1.9, 0, 0}, true},

		{"cylinder centre", VolumeCylinder, mgl32.Vec3{0, 0, 0}, true},
		{"cylinder flat cap holds full height", VolumeCylinder, mgl32.Vec3{0, 0.99, 0}, true},
		{"cylinder above cap", VolumeCylinder, mgl32.Vec3{0, 1.1, 0}, false},
		// Round in XZ: the box corner in that plane is outside.
		{"cylinder excludes XZ corner", VolumeCylinder, mgl32.Vec3{1.9, 0, 2.9}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := testVolume(tc.shape)
			if got := v.ContainsPoint(tc.p); got != tc.want {
				t.Errorf("ContainsPoint(%v) = %v, want %v", tc.p, got, tc.want)
			}
		})
	}
}

func TestDisabledVolumeContainsNothing(t *testing.T) {
	// The flag is how a script disarms a trigger without moving or deleting it,
	// so "disabled" has to mean "contains nothing" rather than "still fires".
	v := testVolume(VolumeBox)
	v.Enabled = false
	if v.ContainsPoint(mgl32.Vec3{0, 0, 0}) {
		t.Error("a disabled volume reported a point inside it")
	}
}

func TestUnboundedVolumeContainsNothing(t *testing.T) {
	v := testVolume(VolumeBox)
	v.HalfY = 0
	if v.ContainsPoint(mgl32.Vec3{0, 0, 0}) {
		t.Error("a volume with a zero extent reported a point inside it")
	}
	if _, _, ok := v.WorldAABB(nil); ok {
		t.Error("WorldAABB reported bounds for an unauthored volume")
	}
}

func TestRotatedVolumeStaysItsShape(t *testing.T) {
	// A point beyond the unrotated Z extent but within the X one: rotating the
	// volume a quarter turn about Y should swap which is which.
	v := testVolume(VolumeBox)
	p := mgl32.Vec3{2.5, 0, 0}
	if v.ContainsPoint(p) {
		t.Fatalf("unrotated volume already contains %v", p)
	}
	v.Rotation = mgl32.Vec3{0, float32(3.14159265 / 2), 0}
	if !v.ContainsPoint(p) {
		t.Errorf("rotated volume does not contain %v; rotation was not applied", p)
	}
}

func TestVolumeLookupsAndUniqueIDs(t *testing.T) {
	lvl := &Level{Volumes: []Volume{
		{ID: "a", Enabled: true, HalfX: 1, HalfY: 1, HalfZ: 1},
		{ID: "b", Enabled: true, HalfX: 1, HalfY: 1, HalfZ: 1, Position: mgl32.Vec3{10, 0, 0}},
	}}
	lvl.Volumes[0].AddTag("trigger")
	lvl.Volumes[1].AddTag("trigger")

	if got := FindVolume(lvl, "b"); got == nil || got.ID != "b" {
		t.Errorf(`FindVolume("b") did not find it`)
	}
	if FindVolume(lvl, "missing") != nil {
		t.Error("FindVolume invented a volume")
	}
	if got := FindVolumesByTag(lvl, "trigger"); len(got) != 2 {
		t.Errorf("FindVolumesByTag: got %d, want 2", len(got))
	}
	if got := VolumesContaining(lvl, mgl32.Vec3{0, 0, 0}); len(got) != 1 || got[0].ID != "a" {
		t.Errorf("VolumesContaining at the origin: got %v, want just a", got)
	}

	if got := UniqueVolumeID(lvl, "a"); got == "a" {
		t.Error("UniqueVolumeID returned a name already in use")
	}
	// One name means one object across every kind, so a volume cannot take a
	// brush's name either.
	lvl.Chunks = []Chunk{{ID: "c", Brushes: []Brush{{ID: "hall"}}}}
	if got := UniqueVolumeID(lvl, "hall"); got == "hall" {
		t.Error("a volume was allowed to reuse a brush's ID")
	}
	if got := UniqueVolumeID(lvl, "player"); got == "player" {
		t.Error("a volume was allowed to take the reserved player ID")
	}
}

func TestVolumeRoundTrip(t *testing.T) {
	v := testVolume(VolumeCylinder)
	v.Rotation = mgl32.Vec3{0, 1.5, 0}
	v.Script = "scripts/door.flang"
	v.AddTag("trigger")
	v.SetAttr("locked", BoolAttr(true))
	v.SetAttr("uses", IntAttr(3))
	v.Enabled = false

	got, err := VolumeFromRaw(VolumeToRaw(&v))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}

	if got.ID != v.ID || got.Shape != v.Shape || got.Enabled != v.Enabled {
		t.Errorf("identity/shape/enabled changed: %+v", got)
	}
	if got.Position != v.Position || got.Rotation != v.Rotation {
		t.Errorf("transform changed: pos %v rot %v", got.Position, got.Rotation)
	}
	if got.HalfX != v.HalfX || got.HalfY != v.HalfY || got.HalfZ != v.HalfZ {
		t.Errorf("extents changed: %v %v %v", got.HalfX, got.HalfY, got.HalfZ)
	}
	if got.Script != v.Script {
		t.Errorf("script changed: got %q, want %q", got.Script, v.Script)
	}
	if !got.HasTag("trigger") {
		t.Error("tag did not survive the round trip")
	}
	if a, ok := got.Attr("locked"); !ok || !a.B {
		t.Error("bool attribute did not survive the round trip")
	}
	if a, ok := got.Attr("uses"); !ok || a.I != 3 {
		t.Error("int attribute did not survive the round trip")
	}
}

func TestVolumeShapeNameRoundTrip(t *testing.T) {
	for i, name := range VolumeShapeNames {
		if got := VolumeShapeFromName(name); got != VolumeShape(i) {
			t.Errorf("%q parsed as %d, want %d", name, got, i)
		}
	}
	// An unknown name loads as a box rather than failing, so a level written by
	// a newer build still opens.
	if got := VolumeShapeFromName("tetrahedron"); got != VolumeBox {
		t.Errorf("unknown shape parsed as %d, want VolumeBox", got)
	}
}

func TestScriptDataTagsAndAttributes(t *testing.T) {
	var d ScriptData
	d.AddTag("b")
	d.AddTag("a")
	d.AddTag("a") // duplicate is a no-op
	if len(d.Tags) != 2 || d.Tags[0] != "a" || d.Tags[1] != "b" {
		t.Errorf("tags are not normalised and deduped: %v", d.Tags)
	}
	if !d.HasTag("a") || d.HasTag("z") {
		t.Error("HasTag disagrees with the list")
	}
	if !d.RemoveTag("a") || d.RemoveTag("a") {
		t.Error("RemoveTag did not report what it did")
	}

	d.SetAttr("health", IntAttr(25))
	if v, ok := d.Attr("health"); !ok || v.I != 25 {
		t.Error("SetAttr/Attr did not round-trip")
	}
	if !d.DeleteAttr("health") || d.DeleteAttr("health") {
		t.Error("DeleteAttr did not report what it did")
	}
}

func TestVolumeIsLevelTrigger(t *testing.T) {
	// All three conditions are load-bearing: a disarmed trigger, or one with no
	// extents, contains nothing, and one with no target is an ordinary volume.
	base := testVolume(VolumeBox)
	base.LoadLevel = "canyon"

	if !base.IsLevelTrigger() {
		t.Error("an armed, bounded volume with a target is not a level trigger")
	}

	off := base
	off.Enabled = false
	if off.IsLevelTrigger() {
		t.Error("a disabled volume is still a level trigger")
	}

	flat := base
	flat.HalfY = 0
	if flat.IsLevelTrigger() {
		t.Error("an unauthored volume is still a level trigger")
	}

	plain := testVolume(VolumeBox)
	if plain.IsLevelTrigger() {
		t.Error("a volume with no target is a level trigger")
	}
}

func TestVolumeLoadLevelRoundTrips(t *testing.T) {
	v := testVolume(VolumeBox)
	v.LoadLevel = "canyon"

	back, err := VolumeFromRaw(VolumeToRaw(&v))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if back.LoadLevel != "canyon" {
		t.Errorf("load_level came back %q, want %q", back.LoadLevel, "canyon")
	}

	// And an ordinary volume writes no key at all, so adding this changed no
	// existing level file.
	plain := testVolume(VolumeBox)
	if raw := VolumeToRaw(&plain); raw.LoadLevel != "" {
		t.Errorf("a volume with no target wrote load_level %q", raw.LoadLevel)
	}
}
