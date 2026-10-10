package world

// entity_model_mount_test.go — model attachments riding each other's bones, and
// point lights riding carriers.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/model"
)

// riggedDoc is a model with one bone running from head to tail, and no
// animation — the shape a turret with a muzzle bone has.
func riggedDoc(t *testing.T, head, tail mgl32.Vec3, name string) *model.ModelDoc {
	t.Helper()
	d := model.NewCubeDoc()
	bi := d.AddBone(head, tail)
	if bi < 0 {
		t.Fatal("AddBone refused")
	}
	d.RenameBone(bi, name)
	return d
}

// mountedEntity is a knight with a body model whose hand bone points up the
// +Y axis from (1, 2, 0), and a sword mounted on it.
func mountedEntity(t *testing.T) *Entity {
	t.Helper()
	body := EntityModel{Name: "body", Visible: true,
		Doc: riggedDoc(t, mgl32.Vec3{1, 2, 0}, mgl32.Vec3{1, 3, 0}, "hand")}
	sword := EntityModel{Name: "sword", Visible: true, Parent: "body", Bone: "hand",
		Offset: mgl32.Vec3{0, 0.5, 0}}
	return &Entity{ID: "knight", Position: mgl32.Vec3{10, 0, 0}, Scale: mgl32.Vec3{1, 1, 1},
		Models: []EntityModel{body, sword}}
}

func TestAMountedModelSitsOnItsParentsBone(t *testing.T) {
	e := mountedEntity(t)
	got := translationOf(e.AttachmentMatrix(e.Model("sword")))
	// The bone's head, then half a unit along the bone (+Y in the socket).
	if want := (mgl32.Vec3{1, 2.5, 0}); !vec3Near(got, want, 1e-5) {
		t.Errorf("sword at %v in the entity, want %v", got, want)
	}
	world := translationOf(e.Model("sword").WorldMatrix(e))
	if want := (mgl32.Vec3{11, 2.5, 0}); !vec3Near(world, want, 1e-5) {
		t.Errorf("sword at %v in the world, want %v", world, want)
	}
}

func TestAMountFollowsItsParentsOwnPlacement(t *testing.T) {
	e := mountedEntity(t)
	e.Model("body").Offset = mgl32.Vec3{0, 0, 5}
	got := translationOf(e.AttachmentMatrix(e.Model("sword")))
	if want := (mgl32.Vec3{1, 2.5, 5}); !vec3Near(got, want, 1e-5) {
		t.Errorf("sword at %v after moving the body, want %v", got, want)
	}
}

func TestAMountWithNoBoneRidesTheParentsOrigin(t *testing.T) {
	e := mountedEntity(t)
	e.Model("body").Offset = mgl32.Vec3{3, 0, 0}
	e.Model("sword").Bone = ""
	got := translationOf(e.AttachmentMatrix(e.Model("sword")))
	if want := (mgl32.Vec3{3, 0.5, 0}); !vec3Near(got, want, 1e-5) {
		t.Errorf("sword at %v, want the body's origin plus its offset %v", got, want)
	}
}

func TestAnUnresolvableMountFallsBack(t *testing.T) {
	e := mountedEntity(t)
	sword := e.Model("sword")

	sword.Bone = "tail_of_nothing"
	if got := translationOf(e.AttachmentMatrix(sword)); !vec3Near(got, mgl32.Vec3{0, 0.5, 0}, 1e-5) {
		t.Errorf("a missing bone put the sword at %v, want the body's origin", got)
	}
	sword.Parent = "gone"
	if got := translationOf(e.AttachmentMatrix(sword)); !vec3Near(got, mgl32.Vec3{0, 0.5, 0}, 1e-5) {
		t.Errorf("a missing parent put the sword at %v, want the entity's origin", got)
	}
}

func TestAMountCycleDoesNotHang(t *testing.T) {
	e := &Entity{Models: []EntityModel{
		{Name: "a", Parent: "b", Offset: mgl32.Vec3{1, 0, 0}},
		{Name: "b", Parent: "a", Offset: mgl32.Vec3{0, 1, 0}},
	}}
	_ = e.AttachmentMatrix(&e.Models[0]) // returning at all is the test

	if !e.ModelMountCycle("a", "b") {
		t.Error("ModelMountCycle missed a two-model loop")
	}
	chain := &Entity{Models: []EntityModel{{Name: "a"}, {Name: "b", Parent: "a"}, {Name: "c", Parent: "b"}}}
	if !chain.ModelMountCycle("a", "c") {
		t.Error("mounting a on its grandchild is not reported as a cycle")
	}
	if chain.ModelMountCycle("c", "a") {
		t.Error("mounting c on its grandparent is reported as a cycle")
	}
}

func TestBonesOfAMountedModelAreSocketsToo(t *testing.T) {
	e := mountedEntity(t)
	e.Model("sword").Doc = riggedDoc(t, mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, 2, 0}, "tip")

	m, ok := EntityBoneMatrix(e, "sword", "tip")
	if !ok {
		t.Fatal("the sword's own bone did not resolve")
	}
	// Sword origin (1, 2.5, 0), then its tip bone's head one unit up.
	if got, want := translationOf(m), (mgl32.Vec3{1, 3.5, 0}); !vec3Near(got, want, 1e-5) {
		t.Errorf("sword tip at %v, want %v", got, want)
	}
}

// A socket is a rotation, never a reflection: a left-handed basis would mirror
// whatever hangs there — a rifle's ejection port swapping sides, its normals
// turned inside out.
func TestASocketBasisIsRightHanded(t *testing.T) {
	for _, tail := range []mgl32.Vec3{{1, 2, 1}, {1, 3, 0}, {2, 2, 0}, {1, 1.2, 0.3}} {
		e := mountedEntity(t)
		e.Model("body").Doc = riggedDoc(t, mgl32.Vec3{1, 2, 0}, tail, "hand")
		m, ok := EntityBoneMatrix(e, "body", "hand")
		if !ok {
			t.Fatal("the hand did not resolve")
		}
		x, y, z := m.Col(0).Vec3(), m.Col(1).Vec3(), m.Col(2).Vec3()
		if d := m.Mat3().Det(); d < 0.999 || d > 1.001 {
			t.Errorf("bone toward %v: socket determinant %v, want 1", tail, d)
		}
		if !vec3Near(x.Cross(y), z, 1e-5) {
			t.Errorf("bone toward %v: x × y = %v, but z is %v", tail, x.Cross(y), z)
		}
		if want := tail.Sub(mgl32.Vec3{1, 2, 0}).Normalize(); !vec3Near(y, want, 1e-5) {
			t.Errorf("bone toward %v: socket +Y %v, want along the bone %v", tail, y, want)
		}
	}
}

func TestAStaticRigStillHasSockets(t *testing.T) {
	e := mountedEntity(t)
	if e.Model("body").Anim != nil {
		t.Fatal("fixture is supposed to be unanimated")
	}
	if _, ok := EntityBoneMatrix(e, "body", "hand"); !ok {
		t.Error("a bone on a model with no animation did not resolve")
	}
}

func TestInitAnimationKeepsARigWithoutClips(t *testing.T) {
	var m EntityModel
	m.initAnimation(riggedDoc(t, mgl32.Vec3{}, mgl32.Vec3{0, 1, 0}, "muzzle"))
	if m.Anim != nil {
		t.Error("an unanimated rig got playback state")
	}
	if m.Doc == nil {
		t.Error("an unanimated rig dropped its document, so its bones cannot be sockets")
	}

	var plain EntityModel
	plain.initAnimation(model.NewCubeDoc())
	if plain.Doc != nil {
		t.Error("a model with no bones kept its document")
	}
}

func TestModelMountRoundTripsAndOverrides(t *testing.T) {
	e := mountedEntity(t)
	raw := entityModelToRaw(e.Model("sword"))
	if raw.Parent != "body" || raw.Bone != "hand" {
		t.Fatalf("raw mount = %q/%q", raw.Parent, raw.Bone)
	}
	back := entityModelFromRaw(raw)
	if back.Parent != "body" || back.Bone != "hand" {
		t.Errorf("mount came back as %q/%q", back.Parent, back.Bone)
	}
	if c := e.Model("sword").TemplateCopy(); c.Parent != "body" || c.Bone != "hand" {
		t.Errorf("TemplateCopy lost the mount: %q/%q", c.Parent, c.Bone)
	}

	def := &EntityDef{Models: []EntityModel{e.Models[0].TemplateCopy(), e.Models[1].TemplateCopy()}}
	inst := e.Models[1].TemplateCopy()
	if _, changed := modelOverride(def, &inst); changed {
		t.Error("an untouched mount wrote an override")
	}
	inst.Bone = "thumb"
	o, changed := modelOverride(def, &inst)
	if !changed || o.Bone != "thumb" || o.Parent != "" {
		t.Errorf("override = %+v, changed %v; want just the bone", o, changed)
	}
	applyModelOverride(&def.Models[1], o)
	if def.Models[1].Bone != "thumb" || def.Models[1].Parent != "body" {
		t.Errorf("applying the override gave %q/%q", def.Models[1].Parent, def.Models[1].Bone)
	}
}

func TestRenameAndDropModelMounts(t *testing.T) {
	models := []EntityModel{{Name: "body"}, {Name: "sword", Parent: "body", Bone: "hand"}, {Name: "cape"}}
	RenameModelMounts(models, "body", "torso")
	if models[1].Parent != "torso" || models[1].Bone != "hand" {
		t.Errorf("rename left the mount at %q/%q", models[1].Parent, models[1].Bone)
	}
	DropModelMounts(models, "torso")
	if models[1].Parent != "" || models[1].Bone != "" {
		t.Errorf("drop left the mount at %q/%q", models[1].Parent, models[1].Bone)
	}
}

// ---------- lights ----------

func TestSceneLightsCarriesAnAttachedLight(t *testing.T) {
	lvl := &Level{
		Entities: []Entity{{ID: "cart", Position: mgl32.Vec3{5, 0, 0}, Scale: mgl32.Vec3{1, 1, 1}}},
	}
	lvl.Lights.Points = []PointLight{
		{ID: "fixed", Position: mgl32.Vec3{1, 1, 1}, Radius: 4},
		{ID: "lantern", Position: mgl32.Vec3{0, 2, 0}, Radius: 4, Dynamic: true, Attach: Attach{Entity: "cart"}},
	}
	if !HasAttachedLights(lvl) {
		t.Fatal("HasAttachedLights missed the lantern")
	}

	ls := SceneLights(lvl)
	if got := ls.Points[1].Position; !vec3Near(got, mgl32.Vec3{5, 2, 0}, 1e-5) {
		t.Errorf("the scene's lantern is at %v, want it carried to the cart", got)
	}
	if got := ls.Points[0].Position; got != (mgl32.Vec3{1, 1, 1}) {
		t.Errorf("an unattached light moved to %v", got)
	}
	if got := lvl.Lights.Points[1].Position; got != (mgl32.Vec3{0, 2, 0}) {
		t.Errorf("SceneLights overwrote the authored offset: %v", got)
	}

	lvl.Entities[0].Position = mgl32.Vec3{-3, 0, 0}
	if got := SceneLights(lvl).Points[1].Position; !vec3Near(got, mgl32.Vec3{-3, 2, 0}, 1e-5) {
		t.Errorf("after the cart moved the lantern is at %v", got)
	}

	lvl.Lights.Points[1].SetWorldPosition(lvl, mgl32.Vec3{0, 0, 0})
	if got := lvl.Lights.Points[1].Position; !vec3Near(got, mgl32.Vec3{3, 0, 0}, 1e-5) {
		t.Errorf("SetWorldPosition stored %v, want the cart-local offset", got)
	}
}

func TestALightOnABoneFollowsTheMount(t *testing.T) {
	e := mountedEntity(t)
	lvl := &Level{Entities: []Entity{*e}}
	lvl.Lights.Points = []PointLight{{ID: "glow", Dynamic: true,
		Attach: Attach{Entity: "knight", Model: "body", Bone: "hand"}}}
	got := lvl.Lights.Points[0].WorldPosition(lvl)
	if want := (mgl32.Vec3{11, 2, 0}); !vec3Near(got, want, 1e-5) {
		t.Errorf("light on the hand at %v, want %v", got, want)
	}
}

func vec3Near(a, b mgl32.Vec3, eps float32) bool {
	return a.Sub(b).Len() <= eps
}
