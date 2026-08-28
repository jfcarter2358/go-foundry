package levelio

// sprite_save_test.go — sprites through the YAML boundary.
//
// Two things are being asserted, and the second matters more than the first.
// One: a fully-specified sprite survives a write and a read unchanged. Two: a
// sprite left at its defaults writes *no* optional keys at all, and a level
// authored before sprites existed gains none — which is the contract every
// omitempty field in the schema exists to hold, and the one that silently
// breaks when someone adds a field without a pointer.

import (
	"foundry/internal/gfx"
	"foundry/internal/testenv"
	"foundry/internal/world"
	"reflect"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// withSpriteTextures registers the texture names these tests name, so
// EnsureSpriteTexture finds them without a GL context.
func withSpriteTextures(t *testing.T, names ...string) {
	t.Helper()
	testenv.WithoutGeometryUploads(t)
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	if gfx.TextureNames == nil {
		gfx.TextureNames = map[string]string{}
	}
	for _, n := range append(names, "default") {
		gfx.Textures[n] = 1
		gfx.TextureNames[n] = n
	}
}

func TestSpriteRoundTripsEveryField(t *testing.T) {
	withSpriteTextures(t, "leaf_bush")

	want := world.Sprite{
		ID:          "bush_01",
		Position:    mgl32.Vec3{4, 0.5, -2},
		Rotation:    mgl32.Vec3{0, 1.25, 0},
		Width:       2.5,
		Height:      3,
		Texture:     "leaf_bush",
		Billboard:   gfx.BillboardFixed,
		Blend:       gfx.SpriteBlendAdditive,
		Tint:        mgl32.Vec3{1, 0.5, 0.25},
		Alpha:       0.75,
		Lit:         true,
		CastShadows: true,
		Visible:     false,
		SheetCols:   4,
		SheetRows:   2,
		Frame:       5,
		Attach:      world.Attach{Frame: "ship", Entity: "npc", Model: "body", Bone: "hand_r"},
	}

	got, err := world.SpriteFromRaw(world.SpriteToRaw(&want))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	// ScriptData holds slices and maps, so a Sprite is not comparable with ==
	// and the embedded half is covered by the volume tests already. Zero it on
	// both sides and compare everything this file is actually about.
	got.ScriptData = world.ScriptData{}
	want.ScriptData = world.ScriptData{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sprite changed across a round trip:\n got %+v\nwant %+v", got, want)
	}
}

// TestDefaultSpriteWritesNoOptionalKeys is the omitempty contract. A sprite
// nobody has touched should be three lines in the level file, not thirty.
func TestDefaultSpriteWritesNoOptionalKeys(t *testing.T) {
	withSpriteTextures(t)

	s := world.NewSprite("sprite", mgl32.Vec3{1, 2, 3})
	out, err := yaml.Marshal(world.SpriteToRaw(&s))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(out)
	// NewSprite deliberately picks y-axis over the zero-valued `full`, since a
	// sprite dropped on the ground almost always wants it; everything else must
	// be absent.
	for _, key := range []string{
		"width:", "height:", "blend:", "tint:", "alpha:", "lit:",
		"cast_shadows:", "visible:", "sheet_cols:", "sheet_rows:",
		"frame:", "attach:", "tags:", "attributes:", "script:",
	} {
		if strings.Contains(text, key) {
			t.Errorf("default sprite wrote %q; it should be omitted:\n%s", key, text)
		}
	}
}

// TestSpritelessLevelGainsNoSpritesKey guards the levels that already exist: a
// level with no sprites must round-trip without growing a `sprites:` key.
func TestSpritelessLevelGainsNoSpritesKey(t *testing.T) {
	withSpriteTextures(t)

	var lvl world.Level
	raw, err := LevelToRaw(&lvl)
	if err != nil {
		t.Fatalf("LevelToRaw: %v", err)
	}
	if raw.Sprites != nil {
		t.Fatalf("empty level produced %d sprites", len(raw.Sprites))
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "sprites:") {
		t.Fatalf("empty level wrote a sprites key:\n%s", out)
	}
}

// TestSpriteIDsAreUniqueAcrossKinds — a sprite may not take a name a volume is
// already using, or `@id` becomes ambiguous. LevelIDTaken is what promises
// this, and it has to know about sprites for the promise to hold.
func TestSpriteIDsAreUniqueAcrossKinds(t *testing.T) {
	lvl := world.Level{
		Volumes: []world.Volume{{ID: "trigger"}},
		Sprites: []world.Sprite{{ID: "flame"}},
	}
	if got := world.UniqueSpriteID(&lvl, "trigger"); got == "trigger" {
		t.Error("a sprite was allowed to take a volume's ID")
	}
	if got := world.UniqueVolumeID(&lvl, "flame"); got == "flame" {
		t.Error("a volume was allowed to take a sprite's ID")
	}
	if got := world.UniqueSpriteID(&lvl, "torch"); got != "torch" {
		t.Errorf("an unused ID was renamed: %q", got)
	}
}

// TestSpriteUVRect covers the sheet arithmetic, including the no-sheet case
// that every sprite starts in.
func TestSpriteUVRect(t *testing.T) {
	whole := world.Sprite{}
	if u, v, du, dv := whole.UVRect(); u != 0 || v != 0 || du != 1 || dv != 1 {
		t.Errorf("no sheet should be the whole texture, got %v %v %v %v", u, v, du, dv)
	}
	// A 4x2 sheet: frame 5 is column 1 of row 1.
	s := world.Sprite{SheetCols: 4, SheetRows: 2, Frame: 5}
	u, v, du, dv := s.UVRect()
	if du != 0.25 || dv != 0.5 {
		t.Fatalf("cell size: got %v %v, want 0.25 0.5", du, dv)
	}
	if u != 0.25 || v != 0.5 {
		t.Errorf("frame 5 of a 4x2 sheet: got u=%v v=%v, want 0.25 0.5", u, v)
	}
	// Frames wrap rather than reading off the end of the sheet.
	s.Frame = 8
	if u, v, _, _ := s.UVRect(); u != 0 || v != 0 {
		t.Errorf("frame 8 should wrap to frame 0, got u=%v v=%v", u, v)
	}
}
