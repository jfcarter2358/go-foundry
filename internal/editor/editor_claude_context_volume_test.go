package editor

// Context volumes, from the panel's side: resolving what the user typed, and
// answering the tool.

import (
	"strings"
	"testing"

	"foundry/internal/claude"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// contextVolumeLevel is a room with a crate in it and a region named around it.
func contextVolumeLevel() *world.Level {
	return &world.Level{
		Chunks: []world.Chunk{{ID: "deck", Brushes: []world.Brush{{
			ID: "mess_floor",
			BrushMesh: world.BrushMesh{
				Verts: []mgl32.Vec3{
					{-4, 0, -4}, {4, 0, -4}, {4, 0, 4}, {-4, 0, 4},
					{-4, 1, -4}, {4, 1, -4}, {4, 1, 4}, {-4, 1, 4},
				},
				Faces: []world.BrushFace{{Indices: []int{0, 1, 2, 3}, Texture: "sf_deck"}},
			},
		}}}},
		Entities: []world.Entity{{ID: "crate_01", Type: "crate", Position: mgl32.Vec3{1, 1, 1}}},
		Volumes: []world.Volume{
			{
				ID: "mess_hall", Enabled: true, Position: mgl32.Vec3{0, 2, 0},
				HalfX: 8, HalfY: 6, HalfZ: 8,
				Context: &world.VolumeContext{Note: "the crew's mess"},
			},
			{ID: "plain_trigger", Enabled: true, Position: mgl32.Vec3{0, 1, 0}, HalfX: 1, HalfY: 1, HalfZ: 1},
		},
	}
}

func TestContextVolumeToolDescribesTheRegion(t *testing.T) {
	defer saveEditorState()()
	Editor.level = contextVolumeLevel()

	got := claudeContextVolumeText("mess_hall")
	for _, want := range []string{"mess_hall", "the crew's mess", "mess_floor", "crate_01", "sf_deck", "world bounds"} {
		if !strings.Contains(got, want) {
			t.Errorf("the description is missing %q:\n%s", want, got)
		}
	}
	// The @ is how it is written in a prompt, so it has to be accepted.
	if claudeContextVolumeText("@mess_hall") != got {
		t.Error("a leading @ changed the answer")
	}
}

func TestContextVolumeToolListsAndExplainsMisses(t *testing.T) {
	defer saveEditorState()()
	Editor.level = contextVolumeLevel()

	list := claudeContextVolumeText("")
	if !strings.Contains(list, "@mess_hall") || strings.Contains(list, "@plain_trigger") {
		t.Errorf("the listing is wrong:\n%s", list)
	}

	// An ordinary volume is the likely mistake, and deserves its own answer.
	got := claudeContextVolumeText("plain_trigger")
	if !strings.Contains(got, "not a context volume") {
		t.Errorf("naming an ordinary volume said %q", got)
	}
	// A name that is nothing at all falls back to the list, so the next call
	// can be right.
	got = claudeContextVolumeText("no_such_place")
	if !strings.Contains(got, "no context volume called") || !strings.Contains(got, "@mess_hall") {
		t.Errorf("naming nothing said %q", got)
	}

	Editor.level = &world.Level{}
	if got := claudeContextVolumeText(""); !strings.Contains(got, "no context volumes") {
		t.Errorf("a level with none said %q", got)
	}
	Editor.level = nil
	if got := claudeContextVolumeText("mess_hall"); !strings.Contains(got, "No level is open") {
		t.Errorf("with no level open, said %q", got)
	}
}

func TestMentionedContextVolumesAreResolved(t *testing.T) {
	defer saveEditorState()()
	Editor.level = contextVolumeLevel()

	cases := []struct {
		text string
		want []string
	}{
		{"take a look at @mess_hall and restyle it", []string{"mess_hall"}},
		{"@mess_hall, please", []string{"mess_hall"}},
		{"compare @mess_hall with @mess_hall", []string{"mess_hall"}}, // once only
		{"nothing here", nil},
		{"@plain_trigger is not one", nil},
		{"@no_such_thing", nil},
		{"an email@address is not a mention", nil},
	}
	for _, c := range cases {
		got := claudeMentionedContextVolumes(c.text)
		if len(got) != len(c.want) {
			t.Errorf("mentions in %q = %v, want %v", c.text, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("mentions in %q = %v, want %v", c.text, got, c.want)
			}
		}
	}
}

func TestAMentionIsSentEvenWithEditorContextOff(t *testing.T) {
	withClaudePanel(t)
	defer saveEditorState()()
	Editor.level = contextVolumeLevel()
	Editor.SavePath = "/p/resources/levels/level1.yaml"

	got := claudeCompose("restyle @mess_hall")
	if !strings.Contains(got, "context_volume") || !strings.Contains(got, "@mess_hall") {
		t.Errorf("composed = %q, want the mention pointed at the tool", got)
	}

	// The user named the place in the message itself; switching off the
	// selection note must not take that with it.
	claudeState.noContext = true
	got = claudeCompose("restyle @mess_hall")
	if !strings.Contains(got, "context_volume") {
		t.Errorf("with context off, composed = %q, want the mention still resolved", got)
	}
	if strings.Contains(got, "Foundry editor: open on") {
		t.Errorf("composed = %q, want the editor note suppressed", got)
	}
	// A message with no mention gains nothing.
	if got := claudeCompose("hello"); strings.Contains(got, "context_volume") {
		t.Errorf("composed = %q, want no hint when nothing was mentioned", got)
	}
}

func TestPanelListsNamedPlacesWhenEmpty(t *testing.T) {
	withClaudePanel(t)
	defer saveEditorState()()
	Editor.level = contextVolumeLevel()
	// The empty state is where someone decides what to ask, so that is where
	// the names have to appear.
	withImgui(func() { drawClaudeFrame(claude.Snapshot{Status: claude.StatusIdle}) })
}

func TestLookingAtARegionMovesTheCameraAndNotTheSelection(t *testing.T) {
	defer saveEditorState()()
	Editor.level = contextVolumeLevel()
	// Something is selected, and must still be after Claude looks elsewhere:
	// the person at the keyboard was in the middle of it.
	Editor.SelectedBrushes = []brushSel{{chunkIdx: 0, brushIdx: 0}}
	before := editorCam3D.Target

	got := claudeLookAtContextVolume("mess_hall")
	if !strings.Contains(got, "camera") {
		t.Errorf("look said %q", got)
	}
	if editorCam3D.Target == before {
		t.Error("the camera did not move")
	}
	if len(Editor.SelectedBrushes) != 1 {
		t.Error("looking at a region took the selection away")
	}
	if editorCam3D.Fly {
		t.Error("framing should land in orbit mode, looking at the thing from outside")
	}

	// A region with no size says so rather than flinging the camera somewhere.
	v := world.FindContextVolume(Editor.level, "mess_hall")
	v.HalfX, v.HalfY, v.HalfZ = 0, 0, 0
	if got := claudeLookAtContextVolume("mess_hall"); !strings.Contains(got, "no extents") {
		t.Errorf("an unbounded region said %q", got)
	}
	if got := claudeLookAtContextVolume("no_such_place"); got != "" {
		t.Errorf("looking at nothing said %q", got)
	}
}
