package editor

// editor_material_draw_test.go — the material editor's panels draw, for every
// kind of material, with no window and no GL.
//
// The parameter panel is generated from the effect schema, so its imgui stack
// discipline is not something a reader can check by eye: it depends on a table
// in another package, and a kind added there draws widgets nobody wrote code
// for. imgui catches an unbalanced group with an assert inside End() that
// aborts the process, so a test that draws every effect of every domain once is
// what stands between a new ParamDef and a crash the moment somebody expands it.
//
// It is also the only coverage the schema-driven widget switch gets: every
// ParamType has to produce a widget, and one that falls through the switch
// silently draws nothing at all.

import (
	"testing"

	"foundry/internal/material"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// drawMaterialPanelsOnce runs one full imgui frame with the parameter panel in
// it. The list and the previews are left out: both need a window for their size
// and the previews need textures, and neither has a branch the panel does not.
func drawMaterialPanelsOnce() {
	io := imgui.CurrentIO()
	io.SetDisplaySize(imgui.Vec2{X: 1280, Y: 800})
	io.SetDeltaTime(1.0 / 60.0)
	imgui.NewFrame()

	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: 0})
	imgui.SetNextWindowSize(imgui.Vec2{X: 360, Y: 800})
	imgui.Begin("materialpanel")
	materialHeaderSection()
	imgui.Separator()
	materialEffectsSection()
	imgui.Separator()
	materialProblemsSection()
	imgui.End()

	imgui.Render()
}

func TestMaterialPanelDrawsEveryEffectOfEveryDomain(t *testing.T) {
	saved, savedPath := materialDoc, materialPath
	t.Cleanup(func() { materialDoc, materialPath = saved, savedPath })

	withImgui(func() {
		for domain := range material.DomainNames {
			d := material.Domain(domain)
			kinds := material.KindsFor(d)
			if len(kinds) == 0 {
				t.Errorf("domain %q has no effects at all", material.DomainName(d))
				continue
			}
			// One document carrying every effect the domain allows, so a single
			// frame reaches every widget the schema can produce.
			doc := &material.Doc{Name: "test", Domain: d, Doc: "a note"}
			for _, k := range kinds {
				e := doc.AddEffect(k.Name)
				// Half the parameters set and half left at their default, so
				// both sides of the "default" marker and its reset button draw.
				for i := range k.Params {
					if i%2 == 0 {
						e.Set(k.Params[i].Name, k.Params[i].Default)
					}
				}
			}
			materialDoc, materialPath = doc, ""
			drawMaterialPanelsOnce()
		}
	})
}

func TestMaterialPanelDrawsAnEmptyAndABrokenDocument(t *testing.T) {
	saved, savedPath := materialDoc, materialPath
	t.Cleanup(func() { materialDoc, materialPath = saved, savedPath })

	withImgui(func() {
		// Nothing on it: the "no effects yet" path.
		materialDoc, materialPath = &material.Doc{Name: "empty", Domain: material.DomainSurface}, ""
		drawMaterialPanelsOnce()

		// An effect from a build that knew more than this one, and one in the
		// wrong domain. Both draw a warning rather than a parameter list, and
		// both are paths the ordinary documents never take.
		broken := &material.Doc{Name: "broken", Domain: material.DomainSprite}
		broken.Effects = append(broken.Effects,
			material.Effect{Kind: "iridescence", Params: map[string]material.Value{
				"strength": material.FloatValue(3),
			}},
			material.Effect{Kind: material.KindWater},
		)
		materialDoc = broken
		drawMaterialPanelsOnce()
	})
}

func TestEveryParamTypeProducesAWidget(t *testing.T) {
	// The schema-driven switch: a type with no case draws nothing, and the
	// parameter silently cannot be edited.
	types := []material.ParamType{
		material.ParamFloat, material.ParamVec2, material.ParamVec3,
		material.ParamColor, material.ParamBool, material.ParamTexture, material.ParamEnum,
	}
	withImgui(func() {
		io := imgui.CurrentIO()
		io.SetDisplaySize(imgui.Vec2{X: 1280, Y: 800})
		io.SetDeltaTime(1.0 / 60.0)
		imgui.NewFrame()
		imgui.Begin("widgets")
		for _, ty := range types {
			p := &material.ParamDef{Name: "p", Type: ty, Label: "P"}
			switch ty {
			case material.ParamEnum:
				p.Choices = []string{"a", "b"}
				p.Default = material.EnumValue("a")
			case material.ParamTexture:
				p.Default = material.TextureValue("")
			case material.ParamBool:
				p.Default = material.BoolValue(false)
			default:
				p.Default = material.Value{Type: ty}
			}
			v := p.Default
			// The return value is "did the user change it", which is false with
			// no input; what is being asserted is that the call does not panic
			// and leaves imgui's stacks even.
			materialParamWidget(p, "P", &v)
		}
		imgui.End()
		imgui.Render()
	})
}

func TestTooltipWrappingKeepsEveryWord(t *testing.T) {
	// The schema's doc strings are one paragraph because `foundry check` and
	// the docs generator read them too; the editor breaks them for a tooltip.
	// Losing a word there would be invisible in the UI and wrong.
	for _, k := range material.AllKinds() {
		got := wrapTooltip(k.Doc)
		if fieldsJoined(got) != fieldsJoined(k.Doc) {
			t.Errorf("kind %q: wrapping changed the words", k.Name)
		}
		for _, line := range splitLines(got) {
			if len(line) > 100 {
				t.Errorf("kind %q: a wrapped line is %d characters", k.Name, len(line))
			}
		}
	}
}

func fieldsJoined(s string) string {
	out := ""
	for _, w := range splitFields(s) {
		out += w + " "
	}
	return out
}

func splitFields(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}
