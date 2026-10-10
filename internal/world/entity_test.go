package world

import (
	"foundry/internal/geom"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func vecNear(a, b mgl32.Vec3, tol float32) bool {
	return geom.Abs32(a.X()-b.X()) <= tol && geom.Abs32(a.Y()-b.Y()) <= tol && geom.Abs32(a.Z()-b.Z()) <= tol
}

// ---------- transform ----------

func TestEntityModelMatrixTreatsZeroScaleAsIdentity(t *testing.T) {
	// An entity that never mentioned scale must behave as one scaled {1,1,1},
	// not collapse to a point.
	e := Entity{Position: mgl32.Vec3{1, 2, 3}}
	p := e.ModelMatrix().Mul4x1(mgl32.Vec4{1, 0, 0, 1})
	if got := (mgl32.Vec3{p.X(), p.Y(), p.Z()}); !vecNear(got, mgl32.Vec3{2, 2, 3}, 1e-5) {
		t.Errorf("got %v, want {2 2 3}", got)
	}
}

func TestEntityModelYawMatchesTheEngineConvention(t *testing.T) {
	// A Y rotation carries +X toward -Z, the same sense the spawn marker and
	// DirectionFromAngles use. The 2D gizmo's sign flip depends on this.
	e := Entity{Rotation: mgl32.Vec3{0, math.Pi / 2, 0}, Scale: mgl32.Vec3{1, 1, 1}}
	p := e.ModelMatrix().Mul4x1(mgl32.Vec4{1, 0, 0, 1})
	if got := (mgl32.Vec3{p.X(), p.Y(), p.Z()}); !vecNear(got, mgl32.Vec3{0, 0, -1}, 1e-5) {
		t.Errorf("got %v, want {0 0 -1}", got)
	}
}

func TestEntityModelAttachmentComposesUnderTheEntity(t *testing.T) {
	// The whole point of an attachment offset: the entity's transform carries
	// it, so turning the entity swings the attachment around it.
	e := Entity{Position: mgl32.Vec3{10, 0, 0}, Rotation: mgl32.Vec3{0, math.Pi / 2, 0}}
	m := EntityModel{Offset: mgl32.Vec3{2, 0, 0}}

	p := m.WorldMatrix(&e).Mul4x1(mgl32.Vec4{0, 0, 0, 1})
	// The offset points along +X locally; a 90° yaw sends it to -Z.
	if got := (mgl32.Vec3{p.X(), p.Y(), p.Z()}); !vecNear(got, mgl32.Vec3{10, 0, -2}, 1e-5) {
		t.Errorf("got %v, want {10 0 -2}", got)
	}
}

func TestEntityModelScaleAndAlphaDefaults(t *testing.T) {
	var m EntityModel
	if got := m.EffectiveScale(); got != (mgl32.Vec3{1, 1, 1}) {
		t.Errorf("scale: got %v, want identity", got)
	}
	// 0 means "unset", so an attachment that never mentioned alpha is opaque
	// rather than invisible.
	if got := m.EffectiveAlpha(); got != 1 {
		t.Errorf("alpha: got %v, want 1", got)
	}
}

// ---------- bounds ----------

func TestEntityBoundCenterFollowsRotationAndScale(t *testing.T) {
	// A bound offset is authored against the model, so it has to ride the
	// entity's own transform or it slides off whatever it was centred on.
	e := Entity{
		Position:    mgl32.Vec3{5, 0, 0},
		Rotation:    mgl32.Vec3{0, math.Pi / 2, 0},
		Scale:       mgl32.Vec3{2, 2, 2},
		BoundOffset: mgl32.Vec3{1, 0, 0},
	}
	if got := e.BoundCenter(); !vecNear(got, mgl32.Vec3{5, 0, -2}, 1e-5) {
		t.Errorf("got %v, want {5 0 -2}", got)
	}
}

func TestEntityWorldAABBRequiresAuthoredExtents(t *testing.T) {
	// An unbounded entity is not a zero-sized one: culling must not hide it.
	e := Entity{Position: mgl32.Vec3{1, 1, 1}}
	if _, _, ok := e.WorldAABB(); ok {
		t.Error("an entity with no extents reported bounds")
	}
	e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ = 1, 2, 3
	min, max, ok := e.WorldAABB()
	if !ok {
		t.Fatal("bounds not reported")
	}
	if !vecNear(min, mgl32.Vec3{0, -1, -2}, 1e-5) || !vecNear(max, mgl32.Vec3{2, 3, 4}, 1e-5) {
		t.Errorf("got %v..%v", min, max)
	}
}

func TestEntityWorldAABBIsConservativeWhenRotated(t *testing.T) {
	// A rotated box uses the extents' length as a uniform radius, so the AABB
	// stays valid at any orientation rather than clipping as the entity turns.
	e := Entity{BoundHalfX: 1, BoundHalfY: 1, BoundHalfZ: 1, Rotation: mgl32.Vec3{0, 0.4, 0}}
	min, max, ok := e.WorldAABB()
	if !ok {
		t.Fatal("bounds not reported")
	}
	r := mgl32.Vec3{1, 1, 1}.Len()
	if !vecNear(min, mgl32.Vec3{-r, -r, -r}, 1e-5) || !vecNear(max, mgl32.Vec3{r, r, r}, 1e-5) {
		t.Errorf("got %v..%v, want ±%v on each axis", min, max, r)
	}
}

func TestEntityContainsPointPerShape(t *testing.T) {
	base := Entity{BoundHalfX: 1, BoundHalfY: 1, BoundHalfZ: 1}

	box := base
	box.BoundingKind = BoundingAABB
	if !box.ContainsPoint(mgl32.Vec3{0.9, 0.9, 0.9}) {
		t.Error("aabb: corner point should be inside")
	}
	if box.ContainsPoint(mgl32.Vec3{1.1, 0, 0}) {
		t.Error("aabb: point beyond the extent should be outside")
	}

	sphere := base
	sphere.BoundingKind = BoundingSphere
	// Inside the box but outside the inscribed sphere — the case that tells the
	// two shapes apart.
	if sphere.ContainsPoint(mgl32.Vec3{0.9, 0.9, 0.9}) {
		t.Error("sphere: box corner should be outside")
	}
	if !sphere.ContainsPoint(mgl32.Vec3{0.9, 0, 0}) {
		t.Error("sphere: on-axis point should be inside")
	}

	cyl := base
	cyl.BoundingKind = BoundingCylinder
	// Circular in XZ, flat-capped in Y.
	if !cyl.ContainsPoint(mgl32.Vec3{0, 0.99, 0}) {
		t.Error("cylinder: point below the cap should be inside")
	}
	if cyl.ContainsPoint(mgl32.Vec3{0, 1.01, 0}) {
		t.Error("cylinder: point above the cap should be outside")
	}
	if cyl.ContainsPoint(mgl32.Vec3{0.9, 0, 0.9}) {
		t.Error("cylinder: XZ box corner should be outside the circle")
	}
}

func TestEntityContainsPointRespectsRotation(t *testing.T) {
	// The point is moved into the entity's space, so a rotated non-cubic box
	// stays a box rather than degenerating into the conservative sphere.
	e := Entity{BoundHalfX: 2, BoundHalfY: 1, BoundHalfZ: 0.5, Rotation: mgl32.Vec3{0, math.Pi / 2, 0}}
	// The long axis now runs along Z.
	if !e.ContainsPoint(mgl32.Vec3{0, 0, 1.8}) {
		t.Error("point along the rotated long axis should be inside")
	}
	if e.ContainsPoint(mgl32.Vec3{1.8, 0, 0}) {
		t.Error("point along the rotated short axis should be outside")
	}
}

func TestEntityContainsPointNeedsBounds(t *testing.T) {
	e := Entity{}
	if e.ContainsPoint(e.Position) {
		t.Error("an unbounded entity contained a point")
	}
}

// ---------- motion ----------

func TestIntegrateAdvancesPositionAndRotation(t *testing.T) {
	e := Entity{
		Position:        mgl32.Vec3{0, 0, 0},
		Velocity:        mgl32.Vec3{2, 0, -1},
		AngularVelocity: mgl32.Vec3{0, 1, 0},
	}
	e.Integrate(0.5)
	if !vecNear(e.Position, mgl32.Vec3{1, 0, -0.5}, 1e-5) {
		t.Errorf("position: got %v, want {1 0 -0.5}", e.Position)
	}
	if !vecNear(e.Rotation, mgl32.Vec3{0, 0.5, 0}, 1e-5) {
		t.Errorf("rotation: got %v, want {0 0.5 0}", e.Rotation)
	}
}

func TestIntegrateWrapsRotation(t *testing.T) {
	// An entity spinning for a long time must not accumulate an angle whose
	// float32 resolution is coarser than the spin itself.
	e := Entity{AngularVelocity: mgl32.Vec3{0, 1, 0}}
	for i := 0; i < 1000; i++ {
		e.Integrate(0.1)
	}
	if e.Rotation.Y() < -math.Pi || e.Rotation.Y() > math.Pi {
		t.Errorf("rotation escaped [-pi, pi]: %v", e.Rotation.Y())
	}
}

func TestWrapAngle(t *testing.T) {
	cases := []struct{ in, want float32 }{
		{0, 0},
		{1, 1},
		{math.Pi, math.Pi},
		{-math.Pi, -math.Pi},
		{float32(2*math.Pi + 0.5), 0.5},
		{float32(-2*math.Pi - 0.5), -0.5},
		{float32(4*math.Pi + 1), 1},
	}
	for _, c := range cases {
		if got := WrapAngle(c.in); geom.Abs32(got-c.want) > 1e-4 {
			t.Errorf("WrapAngle(%v) = %v, want %v", c.in, got, c.want)
		}
	}

	// The half-turns land on one of the two equivalent representations; which
	// one does not matter, only that the result is in range and still names the
	// same angle.
	for _, in := range []float32{float32(3 * math.Pi), float32(-3 * math.Pi)} {
		got := WrapAngle(in)
		if got < -math.Pi-1e-4 || got > math.Pi+1e-4 {
			t.Errorf("WrapAngle(%v) = %v, out of [-pi, pi]", in, got)
		}
		if geom.Abs32(geom.Abs32(got)-math.Pi) > 1e-4 {
			t.Errorf("WrapAngle(%v) = %v, want ±pi", in, got)
		}
	}
}

func TestIntegrateIsAStillNoOpWithoutMotion(t *testing.T) {
	// A static prop must not drift, and must not be rewritten frame after
	// frame with almost-equal floats.
	e := Entity{Position: mgl32.Vec3{1, 2, 3}, Rotation: mgl32.Vec3{0.1, 0.2, 0.3}}
	before := e
	e.Integrate(0.016)
	if e.Position != before.Position || e.Rotation != before.Rotation {
		t.Errorf("a motionless entity moved: %v %v", e.Position, e.Rotation)
	}
}

// ---------- lookup ----------

func TestFindEntityAndTagLookup(t *testing.T) {
	lvl := &Level{Entities: []Entity{
		{ID: "a", Tags: []string{"prop", "breakable"}},
		{ID: "b", Tags: []string{"prop"}},
		{ID: "c"},
	}}

	if got := FindEntity(lvl, "b"); got == nil || got.ID != "b" {
		t.Errorf("FindEntity: got %+v", got)
	}
	if got := FindEntity(lvl, "missing"); got != nil {
		t.Errorf("FindEntity of a missing id: got %+v", got)
	}
	// `#prop` addresses every entity carrying the tag, in level order.
	got := FindEntitiesByTag(lvl, "prop")
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Errorf("FindEntitiesByTag: got %v", entityIDs(got))
	}
	if got := FindEntitiesByTag(lvl, "breakable"); len(got) != 1 || got[0].ID != "a" {
		t.Errorf("FindEntitiesByTag(breakable): got %v", entityIDs(got))
	}
}

func entityIDs(es []*Entity) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

func TestEntitiesContainingUsesBoundingVolume(t *testing.T) {
	// The query behind the scripting engine's enter/exit events.
	lvl := &Level{Entities: []Entity{
		{ID: "near", Position: mgl32.Vec3{0, 0, 0}, BoundHalfX: 1, BoundHalfY: 1, BoundHalfZ: 1},
		{ID: "far", Position: mgl32.Vec3{10, 0, 0}, BoundHalfX: 1, BoundHalfY: 1, BoundHalfZ: 1},
		{ID: "unbounded", Position: mgl32.Vec3{0, 0, 0}},
	}}
	got := EntitiesContaining(lvl, mgl32.Vec3{0.5, 0, 0})
	if len(got) != 1 || got[0].ID != "near" {
		t.Errorf("got %v, want [near]", entityIDs(got))
	}
}

// ---------- tags ----------

func TestEntityTagAddRemove(t *testing.T) {
	e := Entity{}
	e.AddTag("prop")
	e.AddTag("breakable")
	e.AddTag("prop") // a duplicate must not appear twice
	if len(e.Tags) != 2 {
		t.Fatalf("got %v, want two tags", e.Tags)
	}
	// Kept sorted, so membership and saved files never depend on add order.
	if e.Tags[0] != "breakable" || e.Tags[1] != "prop" {
		t.Errorf("not sorted: %v", e.Tags)
	}
	if !e.RemoveTag("prop") || e.HasTag("prop") {
		t.Errorf("remove failed: %v", e.Tags)
	}
	if e.RemoveTag("never_there") {
		t.Error("removing an absent tag reported success")
	}
}

func TestMergeTagsUnions(t *testing.T) {
	got := mergeTags([]string{"prop", "breakable"}, []string{"quest_item", "prop"})
	want := []string{"breakable", "prop", "quest_item"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// ---------- bounding kind names ----------

func TestBoundingTypeNameRoundTrip(t *testing.T) {
	for i, name := range BoundingTypeNames {
		k := BoundingType(i)
		if got := BoundingTypeName(k); got != name {
			t.Errorf("%v: got %q, want %q", k, got, name)
		}
		if got := BoundingKindFromName(name); got != k {
			t.Errorf("%q: got %v, want %v", name, got, k)
		}
	}
	// An unknown name loads as an AABB rather than failing, so a level written
	// by a newer build still opens.
	if got := BoundingKindFromName("capsule"); got != BoundingAABB {
		t.Errorf("unknown name: got %v, want aabb", got)
	}
	if got := BoundingTypeName(BoundingType(99)); got != "aabb" {
		t.Errorf("out-of-range kind: got %q", got)
	}
}
