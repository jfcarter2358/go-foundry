package world

// entity_restamp_test.go — re-stamping a placed entity from a changed type,
// which is what saving a type in the entity editor does to the open level.

import (
	"slices"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func restampTypes() (prev, next *EntityDef) {
	prev = &EntityDef{
		Name:       "button",
		Script:     "scripts/button.flang",
		Params:     AttrSet{"drop": FloatAttr(6)},
		Tags:       []string{"switch"},
		Visible:    true,
		BoundHalfX: 1, BoundHalfY: 1, BoundHalfZ: 1,
	}
	next = prev.Clone()
	next.Script = "scripts/button_v2.flang"
	next.Params["drop"] = FloatAttr(8)
	next.BoundHalfX = 2
	return prev, next
}

func TestRestampKeepsTheParametersAPlacementBound(t *testing.T) {
	prev, next := restampTypes()
	e := prev.Instantiate("button_1")
	e.Type = prev.Name
	e.Position = mgl32.Vec3{3, 0, 4}
	e.ScriptParams["target"] = StringAttr("door_north")
	e.Tags = append(e.Tags, "north")
	e.Frame, e.Seat, e.FromPrefab = "ship", "gunner", "lobby"

	got, err := RestampEntity(&e, prev, next)
	if err != nil {
		t.Fatalf("RestampEntity: %v", err)
	}
	// What the placement made its own survives the type changing script.
	if a, ok := got.ScriptParams.Attr("target"); !ok || a.S != "door_north" {
		t.Errorf("the bound parameter went: %+v (bound %v)", a, ok)
	}
	if !slices.Contains(got.Tags, "north") {
		t.Errorf("the placement's own tag went: %v", got.Tags)
	}
	if got.Position != (mgl32.Vec3{3, 0, 4}) {
		t.Errorf("position %v", got.Position)
	}
	if got.Frame != "ship" || got.Seat != "gunner" || got.FromPrefab != "lobby" {
		t.Errorf("runtime placement not carried: frame %q seat %q prefab %q", got.Frame, got.Seat, got.FromPrefab)
	}
	// And what it left alone follows the new type.
	if got.Script != next.Script {
		t.Errorf("script %q, want the type's new %q", got.Script, next.Script)
	}
	if a, _ := got.ScriptParams.Attr("drop"); a.F != 8 {
		t.Errorf("an unbound default did not follow the type: drop = %v", a.F)
	}
	if got.BoundHalfX != 2 || !slices.Contains(got.Tags, "switch") {
		t.Errorf("the type's own change did not land: half %v tags %v", got.BoundHalfX, got.Tags)
	}
}

func TestRestampKeepsAnOverriddenDefault(t *testing.T) {
	prev, next := restampTypes()
	e := prev.Instantiate("button_1")
	e.Type = prev.Name
	e.ScriptParams["drop"] = FloatAttr(2)

	got, err := RestampEntity(&e, prev, next)
	if err != nil {
		t.Fatalf("RestampEntity: %v", err)
	}
	if a, _ := got.ScriptParams.Attr("drop"); a.F != 2 {
		t.Errorf("drop = %v, want the placement's own 2 rather than the type's new default", a.F)
	}
}

func TestRestampWithNoPreviousTypeKeepsThePlacement(t *testing.T) {
	_, next := restampTypes()
	e := Entity{ID: "button_1", Type: "button", Position: mgl32.Vec3{1, 2, 3}, Scale: mgl32.Vec3{1, 1, 1}}
	got, err := RestampEntity(&e, nil, next)
	if err != nil {
		t.Fatalf("RestampEntity: %v", err)
	}
	if got.Position != e.Position || got.Script != next.Script || got.Type != "button" {
		t.Errorf("got %+v", got)
	}
}
