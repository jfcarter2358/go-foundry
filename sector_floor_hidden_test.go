package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v2"
)

// TestFloorHiddenDefaultsVisible pins the safety invariant the Hidden field
// (rather than a Visible one) was chosen for: every existing Floor{...}
// literal across the codebase that doesn't mention the new field must keep
// rendering exactly as before — i.e. the zero value must mean "visible".
func TestFloorHiddenDefaultsVisible(t *testing.T) {
	f := Floor{Enabled: true, Height: 2}
	if f.Hidden {
		t.Fatal("a freshly zero-valued Floor must default to visible (Hidden=false)")
	}
}

// TestRawFloorHiddenYAMLRoundTrip checks the floor texture toggle survives a
// save/load cycle, and — since existing level files predate this field —
// that Hidden=false (the common case) is omitted from the YAML rather than
// writing "hidden: false" into every floor of every level on next save.
func TestRawFloorHiddenYAMLRoundTrip(t *testing.T) {
	visible := RawFloor{Enabled: true, Height: 1, Texture: "dirt"}
	out, err := yaml.Marshal(visible)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "hidden") {
		t.Fatalf("Hidden=false should be omitted from YAML, got:\n%s", out)
	}

	hidden := RawFloor{Enabled: true, Height: 1, Texture: "dirt", Hidden: true}
	out2, err := yaml.Marshal(hidden)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got RawFloor
	if err := yaml.Unmarshal(out2, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Hidden {
		t.Fatalf("Hidden=true did not survive the YAML round trip:\n%s", out2)
	}
}
