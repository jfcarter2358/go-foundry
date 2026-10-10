package world

import "testing"

// `unlit:` survives a definition's load and save, and reaches the uploaded
// geometry's copy through TemplateCopy like every other attachment field.
func TestEntityModelUnlitRoundTrips(t *testing.T) {
	m := entityModelFromRaw(RawEntityModel{Name: "beam", Unlit: true})
	if !m.Unlit {
		t.Fatal("unlit lost on load")
	}
	if !entityModelToRaw(&m).Unlit {
		t.Fatal("unlit lost on save")
	}
	if !m.TemplateCopy().Unlit {
		t.Fatal("unlit lost on instancing")
	}
	if entityModelToRaw(&EntityModel{Name: "body"}).Unlit {
		t.Fatal("a lit attachment saved as unlit")
	}
}
