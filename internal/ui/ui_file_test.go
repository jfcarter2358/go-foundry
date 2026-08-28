package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/scriptdata"

	"github.com/go-gl/mathgl/mgl32"
)

// uiTempDir points the asset paths at a scratch directory for the duration of a
// test, and captures whatever gets logged so a survivable failure can be
// asserted on rather than merely survived.
func uiTempDir(t *testing.T) (dir string, logs *[]string) {
	t.Helper()
	root := t.TempDir()
	prevExec := fileutil.ExecutablePath
	fileutil.ExecutablePath = root
	t.Cleanup(func() { fileutil.ExecutablePath = prevExec })

	if err := os.MkdirAll(UIDir(), 0o755); err != nil {
		t.Fatalf("mkdir ui: %v", err)
	}

	var captured []string
	prevLog := Logf
	Logf = func(format string, args ...any) { captured = append(captured, format) }
	t.Cleanup(func() { Logf = prevLog })

	return root, &captured
}

func writeYAML(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(UIPath(name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// maximalScreen touches every field the format has, so a round trip that keeps
// it has kept everything.
func maximalScreen() *Screen {
	s := NewScreen("kitchen-sink")
	s.Canvas = Canvas{Mode: ScaleConstantPixel, RefW: 1280, RefH: 720, Match: 0.25, PixelSnap: false}
	s.Modal, s.Pauses, s.ShowsCursor = true, true, true
	s.InitialFocus = "btn"
	s.Root.Style.Color = mgl32.Vec4{0.1, 0.2, 0.3, 0.4}

	btn := NewNode("btn", KindButton)
	btn.Rect = RectXf{
		AnchorMin: mgl32.Vec2{0.25, 0.5}, AnchorMax: mgl32.Vec2{0.75, 0.5},
		Offset: [4]float32{1, 2, 3, 4},
		Pivot:  mgl32.Vec2{0, 1}, Scale: mgl32.Vec2{1.5, 2},
	}
	btn.Visible, btn.Enabled, btn.Alpha = false, false, 0.25
	btn.Clip, btn.Blocks = true, true
	btn.Style.Color = mgl32.Vec4{0.9, 0.8, 0.7, 0.6}
	btn.Style.Texture = "ui_frame"
	btn.Style.UV = mgl32.Vec4{0.1, 0.2, 0.3, 0.4}
	btn.Style.Slice = [4]float32{5, 6, 7, 8}
	btn.Style.Fill = FillTile
	hover := mgl32.Vec4{1, 0, 0, 1}
	press := mgl32.Vec4{0, 1, 0, 1}
	uv := mgl32.Vec4{0.5, 0.5, 0.5, 0.5}
	btn.Style.Hover = &StateStyle{Color: &hover, Texture: "ui_hover", UV: &uv}
	btn.Style.Press = &StateStyle{Color: &press}
	btn.Style.Disabled = &StateStyle{Texture: "ui_off"}
	btn.Text = TextSpec{
		Value: "Resume", Font: "inter", Size: 28,
		Color:  mgl32.Vec4{0.2, 0.3, 0.4, 0.5},
		HAlign: AlignRight, VAlign: AlignBottom, Wrap: true,
		Bind: "@player.attr.name", Format: "%s!",
	}
	btn.Action = "resume"
	btn.Nav = Nav{Up: "a", Down: "b", Left: "c", Right: "d"}

	bar := NewNode("bar", KindBar)
	bar.Value = ValueSpec{
		Value: 3, Min: 1, Max: 9, Step: 0.5, Dir: FillTopBottom, On: true,
		Bind: "@player.attr.health", BindMax: "@player.attr.health_max",
	}

	inc := NewNode("inc", KindInclude)
	inc.Include = IncludeSpec{Source: "prefab", Params: map[string]string{"label": "Quit"}}

	s.Root.Children = []Node{btn, bar, inc}
	return s
}

func TestScreenRoundTripsEveryField(t *testing.T) {
	uiTempDir(t)
	want := maximalScreen()
	path := UIPath("sink")

	if err := SaveScreen(want, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadScreenBare(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Compare the Raw forms: they are exactly the serialisable state, so a
	// field that survives here is a field the format actually carries.
	if a, b := screenToRaw(want), screenToRaw(got); !reflect.DeepEqual(a, b) {
		t.Errorf("round trip lost something.\n saved: %#v\nloaded: %#v", a, b)
	}
}

// TestDefaultsAreNotWritten is what catches a pointer that should have been
// plain and a plain field that should have been a pointer — the failure mode
// that turns a three-line document into a forty-line one, or silently rewrites
// an authored zero.
func TestDefaultsAreNotWritten(t *testing.T) {
	uiTempDir(t)

	s := NewScreen("plain")
	s.Root.Style.Color = mgl32.Vec4{1, 1, 1, 1} // the actual default
	s.Root.Children = []Node{NewNode("kid", KindPanel)}
	path := UIPath("plain")
	if err := SaveScreen(s, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	body := string(data)

	for _, unwanted := range []string{
		"visible:", "enabled:", "alpha:", "pivot:", "scale:",
		"anchor_min:", "anchor_max:", "offset:", "rotation:",
		"color:", "uv:", "slice:", "fill:", "clip:", "blocks:",
		"text:", "value:", "nav:", "include:", "canvas:",
		"modal:", "pauses:", "shows_cursor:", "initial_focus:",
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("a document at its defaults wrote %q:\n%s", unwanted, body)
		}
	}
	// Kind is the deliberate exception: it is not a default the author left
	// alone, it is what the thing is.
	if !strings.Contains(body, "kind: panel") {
		t.Errorf("kind was omitted; a document where nodes do not say what they are is unreadable:\n%s", body)
	}
	if !strings.Contains(body, "id: kid") {
		t.Errorf("the child went missing:\n%s", body)
	}
}

func TestAuthoredZerosSurviveTheRoundTrip(t *testing.T) {
	// The other half of the pointer rule: a deliberately hidden, fully
	// transparent, unscaled-to-nothing node must come back exactly as authored
	// rather than snapping to the defaults it happens to differ from.
	uiTempDir(t)

	s := NewScreen("zeros")
	n := NewNode("n", KindPanel)
	n.Visible, n.Enabled = false, false
	n.Alpha = 0
	n.Rect.AnchorMax = mgl32.Vec2{0, 0}
	n.Rect.Scale = mgl32.Vec2{0, 0}
	n.Value.Max = 0
	n.Value.Bind = "x" // so the value block is written at all
	s.Root.Children = []Node{n}

	path := UIPath("zeros")
	if err := SaveScreen(s, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadScreenBare(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	k := got.Find("n")
	if k.Visible || k.Enabled {
		t.Error("an explicitly hidden/disabled node came back true")
	}
	if k.Alpha != 0 {
		t.Errorf("alpha came back %v, want the authored 0", k.Alpha)
	}
	if k.Rect.AnchorMax != (mgl32.Vec2{0, 0}) {
		t.Errorf("anchor_max came back %v, want the authored {0,0}", k.Rect.AnchorMax)
	}
	if k.Value.Max != 0 {
		t.Errorf("value max came back %v, want the authored 0", k.Value.Max)
	}
}

func TestValidateRejectsDuplicateNodeIDs(t *testing.T) {
	uiTempDir(t)
	s := NewScreen("dup")
	s.Root.Children = []Node{NewNode("same", KindPanel), NewNode("same", KindPanel)}
	err := SaveScreen(s, UIPath("dup"))
	if err == nil || !strings.Contains(err.Error(), "same") {
		t.Errorf("saved a screen with duplicate ids: %v", err)
	}
}

func TestValidateRejectsRotationOnAContainer(t *testing.T) {
	uiTempDir(t)
	s := NewScreen("rot")
	n := NewNode("panel", KindPanel)
	n.Rect.Rotation = 0.5
	s.Root.Children = []Node{n}
	if err := SaveScreen(s, UIPath("rot")); err == nil {
		t.Error("saved a rotated panel; GL scissor cannot express an oriented clip")
	}

	// A leaf is fine.
	s2 := NewScreen("rot2")
	img := NewNode("img", KindImage)
	img.Rect.Rotation = 0.5
	s2.Root.Children = []Node{img}
	if err := SaveScreen(s2, UIPath("rot2")); err != nil {
		t.Errorf("refused a rotated image: %v", err)
	}
}

func TestValidateRejectsAMalformedOffsetList(t *testing.T) {
	uiTempDir(t)
	writeYAML(t, "bad", `
id: bad
root:
  id: root
  kind: panel
  children:
    - id: n
      kind: panel
      offset: [1, 2, 3]
`)
	if _, err := loadScreenBare(UIPath("bad")); err == nil || !strings.Contains(err.Error(), "offset") {
		t.Errorf("accepted a 3-element offset: %v", err)
	}
}

func TestUnknownKindIsRefusedRatherThanSilentlyAPanel(t *testing.T) {
	uiTempDir(t)
	writeYAML(t, "future", `
id: future
root:
  id: root
  kind: panel
  children:
    - id: n
      kind: hologram
`)
	if _, err := loadScreenBare(UIPath("future")); err == nil {
		t.Error("a document naming an unknown kind loaded silently as a panel")
	}
}

func TestCloneIsDeep(t *testing.T) {
	orig := maximalScreen()
	clone := orig.Clone()

	clone.Root.Children[0].ID = "changed"
	clone.Root.Children[0].Text.Value = "changed"
	*clone.Root.Children[0].Style.Hover.Color = mgl32.Vec4{9, 9, 9, 9}
	clone.Root.Children[2].Include.Params["label"] = "changed"
	clone.Root.Children = append(clone.Root.Children, NewNode("extra", KindPanel))

	if orig.Root.Children[0].ID != "btn" {
		t.Error("clone shared a node")
	}
	if orig.Root.Children[0].Text.Value != "Resume" {
		t.Error("clone shared a text spec")
	}
	if (*orig.Root.Children[0].Style.Hover.Color) != (mgl32.Vec4{1, 0, 0, 1}) {
		t.Error("clone shared a state style's colour pointer")
	}
	if orig.Root.Children[2].Include.Params["label"] != "Quit" {
		t.Error("clone shared an include's param map")
	}
	if len(orig.Root.Children) != 3 {
		t.Error("clone shared the children slice")
	}
}

func TestUniqueNodeID(t *testing.T) {
	s := NewScreen("t")
	s.Root.Children = []Node{NewNode("btn", KindButton), NewNode("btn_2", KindButton)}

	if got := s.UniqueNodeID("label"); got != "label" {
		t.Errorf("an unused stem came back as %q", got)
	}
	if got := s.UniqueNodeID("btn"); got != "btn_3" {
		t.Errorf("got %q, want btn_3 — btn and btn_2 are taken", got)
	}
	if got := s.UniqueNodeID(""); got != "node" {
		t.Errorf("an empty stem came back as %q", got)
	}
}

// ---------- includes ----------

func writePrefab(t *testing.T) {
	t.Helper()
	writeYAML(t, "prefab", `
id: prefab
root:
  id: root
  kind: panel
  children:
    - id: bg
      kind: panel
      texture: "{{skin}}"
      children:
        - id: label
          kind: text
          text: {value: "{{caption}}"}
          nav: {up: bg}
      action: "{{act}}"
`)
}

func TestIncludeSplicesAndPrefixesIDs(t *testing.T) {
	uiTempDir(t)
	writePrefab(t)
	writeYAML(t, "host", `
id: host
root:
  id: root
  kind: panel
  children:
    - id: quit
      kind: include
      include: {source: prefab}
    - id: play
      kind: include
      include: {source: prefab}
`)

	s, err := LoadScreen(UIPath("host"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// Two copies of one prefab must not both answer to "label", or a script
	// addressing one gets whichever the walk reaches first.
	// The prefix is the include node's id, applied flat to every descendant:
	// ids are already unique inside the prefab, so one level is enough. Nested
	// includes compose, because the inner splice has already prefixed its own.
	for _, id := range []string{"quit/bg", "quit/label", "play/bg", "play/label"} {
		if s.Find(id) == nil {
			t.Errorf("spliced node %q is missing", id)
		}
	}
	if s.Find("label") != nil {
		t.Error("a spliced id was not prefixed")
	}
	// A wired nav link inside the prefab must follow the prefix, or it points
	// at the other copy.
	if got := s.Find("quit/label").Nav.Up; got != "quit/bg" {
		t.Errorf("spliced nav link is %q, want quit/bg", got)
	}
	if !s.Find("quit/bg").FromInclude() {
		t.Error("a spliced node is not marked as coming from an include")
	}
}

func TestNestedIncludesCompose(t *testing.T) {
	// The flat one-level prefix is only correct if it composes: an inner splice
	// has already prefixed its own nodes by the time the outer one runs.
	uiTempDir(t)
	writeYAML(t, "leaf", `
id: leaf
root:
  id: root
  kind: panel
  children: [{id: x, kind: text}]
`)
	writeYAML(t, "mid", `
id: mid
root:
  id: root
  kind: panel
  children: [{id: inner, kind: include, include: {source: leaf}}]
`)
	writeYAML(t, "host", `
id: host
root:
  id: root
  kind: panel
  children:
    - {id: a, kind: include, include: {source: mid}}
    - {id: b, kind: include, include: {source: mid}}
`)

	s, err := LoadScreen(UIPath("host"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, id := range []string{"a/inner", "a/inner/x", "b/inner", "b/inner/x"} {
		if s.Find(id) == nil {
			t.Errorf("nested splice is missing %q", id)
		}
	}
}

func TestIncludeParamsSubstitute(t *testing.T) {
	uiTempDir(t)
	writePrefab(t)
	writeYAML(t, "host", `
id: host
root:
  id: root
  kind: panel
  children:
    - id: quit
      kind: include
      include:
        source: prefab
        params: {caption: "Quit", skin: "ui_red", act: "quit_game"}
`)
	s, err := LoadScreen(UIPath("host"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := s.Find("quit/label").Text.Value; got != "Quit" {
		t.Errorf("text param gave %q", got)
	}
	if got := s.Find("quit/bg").Style.Texture; got != "ui_red" {
		t.Errorf("texture param gave %q", got)
	}
	if got := s.Find("quit/bg").Action; got != "quit_game" {
		t.Errorf("action param gave %q", got)
	}
}

func TestIncludeCycleIsRefused(t *testing.T) {
	_, logs := uiTempDir(t)
	writeYAML(t, "a", `
id: a
root:
  id: root
  kind: panel
  children: [{id: toB, kind: include, include: {source: b}}]
`)
	writeYAML(t, "b", `
id: b
root:
  id: root
  kind: panel
  children: [{id: toA, kind: include, include: {source: a}}]
`)

	done := make(chan *Screen, 1)
	go func() {
		s, _ := LoadScreen(UIPath("a"))
		done <- s
	}()
	s := <-done // a cycle that was not caught would never reach here

	if s == nil {
		t.Fatal("a cyclic include failed the whole load; it should be logged and kept")
	}
	if len(*logs) == 0 {
		t.Error("a cycle was broken silently")
	}
	if n := s.Find("toB/toA"); n != nil && len(n.Children) > 0 {
		t.Error("the cycle spliced a second time round")
	}
}

func TestIncludeDepthIsBounded(t *testing.T) {
	_, logs := uiTempDir(t)
	// A chain longer than the limit, with no cycle in it.
	for i := 0; i <= maxIncludeDepth+2; i++ {
		next := "d" + itoa(i+1)
		writeYAML(t, "d"+itoa(i), "id: d"+itoa(i)+"\nroot:\n  id: root\n  kind: panel\n"+
			"  children: [{id: n, kind: include, include: {source: "+next+"}}]\n")
	}
	if _, err := LoadScreen(UIPath("d0")); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(*logs) == 0 {
		t.Error("a chain past the depth limit was cut silently")
	}
}

func TestBrokenIncludeLogsAndKeeps(t *testing.T) {
	_, logs := uiTempDir(t)
	writeYAML(t, "host", `
id: host
root:
  id: root
  kind: panel
  children:
    - id: gone
      kind: include
      include: {source: not-a-file}
    - id: survivor
      kind: text
      text: {value: still here}
`)
	s, err := LoadScreen(UIPath("host"))
	if err != nil {
		t.Fatalf("a missing include failed the load: %v", err)
	}
	if s.Find("survivor") == nil {
		t.Error("the rest of the screen was lost with the broken include")
	}
	if len(*logs) == 0 {
		t.Error("a missing include was swallowed without a word")
	}
}

func TestSaveDoesNotWriteSplicedChildren(t *testing.T) {
	// Writing them would fork the prefab the moment anyone saved a screen that
	// used it.
	uiTempDir(t)
	writePrefab(t)
	writeYAML(t, "host", `
id: host
root:
  id: root
  kind: panel
  children: [{id: quit, kind: include, include: {source: prefab}}]
`)
	s, err := LoadScreen(UIPath("host"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(s.Find("quit").Children) == 0 {
		t.Fatal("nothing was spliced, so the test proves nothing")
	}

	out := filepath.Join(UIDir(), "host2"+UIExt)
	if err := SaveScreen(s, out); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, _ := os.ReadFile(out)
	if strings.Contains(string(data), "quit/bg") {
		t.Errorf("spliced children were written into the host document:\n%s", data)
	}
	if !strings.Contains(string(data), "source: prefab") {
		t.Errorf("the include itself was not written:\n%s", data)
	}
}

func TestScreenNameAndPaths(t *testing.T) {
	uiTempDir(t)
	if got := ScreenName(UIPath("hud")); got != "hud" {
		t.Errorf("ScreenName gave %q", got)
	}
	if got := filepath.Base(UIPath("hud")); got != "hud"+UIExt {
		t.Errorf("UIPath gave %q", got)
	}
}

func TestScanScreenFiles(t *testing.T) {
	uiTempDir(t)
	writeYAML(t, "b", "id: b\nroot: {id: root, kind: panel}\n")
	writeYAML(t, "a", "id: a\nroot: {id: root, kind: panel}\n")
	if err := os.WriteFile(filepath.Join(UIDir(), "notes.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := ScanScreenFiles()
	want := []string{"a" + UIExt, "b" + UIExt}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scan gave %v, want %v", got, want)
	}
}

func TestLoadFallsBackToTheFilenameForAnUnnamedScreen(t *testing.T) {
	uiTempDir(t)
	writeYAML(t, "unnamed", "root: {id: root, kind: panel}\n")
	s, err := loadScreenBare(UIPath("unnamed"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.ID != "unnamed" {
		t.Errorf("id is %q, want the filename", s.ID)
	}
}

func TestRootIsNormalisedOnLoad(t *testing.T) {
	// The root is the canvas. A document that says otherwise — an older one, or
	// a hand-edited one — comes up working rather than being refused.
	uiTempDir(t)
	writeYAML(t, "oddroot", `
id: oddroot
root:
  id: root
  kind: button
  visible: false
  anchor_max: {x: 0.5, y: 0.5}
  clip: true
`)
	s, err := loadScreenBare(UIPath("oddroot"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if s.Root.Kind != KindPanel || !s.Root.Visible || s.Root.Clip {
		t.Errorf("root came up as %+v", s.Root)
	}
	if s.Root.Rect != DefaultRectXf() {
		t.Errorf("root rect is %+v, want the full canvas", s.Root.Rect)
	}
}

func TestScriptDataRoundTrips(t *testing.T) {
	uiTempDir(t)

	s := NewScreen("scripted")
	s.Tags = []string{"menu", "ui"}
	s.Script = "menus/pause.flang"
	s.Attributes = scriptdata.AttrSet{"opened": scriptdata.IntAttr(3)}
	s.ScriptParams = scriptdata.AttrSet{"target": scriptdata.StringAttr("door_1")}

	btn := NewNode("btn", KindButton)
	btn.Tags = []string{"clickable"}
	btn.Script = "menus/button.flang"
	btn.Attributes = scriptdata.AttrSet{"cost": scriptdata.FloatAttr(2.5)}
	btn.ScriptParams = scriptdata.AttrSet{"locked": scriptdata.BoolAttr(true)}
	s.Root.Children = []Node{btn}

	path := UIPath("scripted")
	if err := SaveScreen(s, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadScreenBare(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !reflect.DeepEqual(got.ScriptData, s.ScriptData) {
		t.Errorf("the screen's script data changed:\n got %+v\nwant %+v", got.ScriptData, s.ScriptData)
	}
	gotBtn := got.Find("btn")
	if gotBtn == nil {
		t.Fatal("the button went missing")
	}
	if !reflect.DeepEqual(gotBtn.ScriptData, btn.ScriptData) {
		t.Errorf("the button's script data changed:\n got %+v\nwant %+v", gotBtn.ScriptData, btn.ScriptData)
	}
}

func TestUnscriptedDocumentWritesNoScriptKeys(t *testing.T) {
	uiTempDir(t)
	s := NewScreen("plain")
	s.Root.Style.Color = mgl32.Vec4{1, 1, 1, 1}
	s.Root.Children = []Node{NewNode("kid", KindPanel)}
	if err := SaveScreen(s, UIPath("plain2")); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, _ := os.ReadFile(UIPath("plain2"))
	for _, unwanted := range []string{"tags:", "attributes:", "script:", "params:"} {
		if strings.Contains(string(data), unwanted) {
			t.Errorf("an unscripted document wrote %q:\n%s", unwanted, data)
		}
	}
}

func TestAMalformedAttributeRefusesTheLoad(t *testing.T) {
	// Silently dropping a value an author wrote is worse than refusing to open
	// the screen: the script that reads it would just find nothing there.
	uiTempDir(t)
	writeYAML(t, "badattr", `
id: badattr
root:
  id: root
  kind: panel
  children:
    - id: n
      kind: panel
      attributes:
        broken: {type: sasquatch, value: 3}
`)
	if _, err := loadScreenBare(UIPath("badattr")); err == nil {
		t.Error("a screen with an unknown attribute type loaded anyway")
	}
}

func TestCloneCopiesScriptData(t *testing.T) {
	s := NewScreen("t")
	n := NewNode("n", KindPanel)
	n.Tags = []string{"a"}
	n.Attributes = scriptdata.AttrSet{"x": scriptdata.IntAttr(1)}
	s.Root.Children = []Node{n}

	clone := s.Clone()
	clone.Find("n").Tags[0] = "changed"
	clone.Find("n").Attributes["x"] = scriptdata.IntAttr(99)

	if s.Find("n").Tags[0] != "a" {
		t.Error("clone shared the tag slice")
	}
	if v, _ := s.Find("n").Attributes["x"].Int(); v != 1 {
		t.Error("clone shared the attribute map")
	}
}

// TestALabelHasNoBackgroundUntilAsked is the fix for a label that arrived with
// an opaque white box behind it — which reads as a renderer bug rather than as
// a colour anyone can change, and which the shipped HUD had on both its text
// nodes.
func TestALabelHasNoBackgroundUntilAsked(t *testing.T) {
	uiTempDir(t)

	if got := NewNode("l", KindText).Style.Color; got[3] != 0 {
		t.Errorf("a new label's background is %v, want transparent", got)
	}
	if got := NewNode("p", KindPanel).Style.Color; got[3] == 0 {
		t.Errorf("a new panel's background is %v; a panel is a rectangle you can see", got)
	}

	// And a document that says nothing about it agrees.
	writeYAML(t, "bare", `
id: bare
root:
  id: root
  kind: panel
  children:
    - {id: label, kind: text, text: {value: hi}}
    - {id: panel, kind: panel}
`)
	s, err := loadScreenBare(UIPath("bare"))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Find("label").Style.Color; got[3] != 0 {
		t.Errorf("a label with no color key loaded as %v, want transparent", got)
	}
	if got := s.Find("panel").Style.Color; got[3] == 0 {
		t.Errorf("a panel with no color key loaded as %v", got)
	}
}

func TestAnAuthoredLabelBackgroundSurvives(t *testing.T) {
	// The other direction: a background someone did ask for has to round-trip,
	// including one that happens to be the old default.
	uiTempDir(t)

	s := NewScreen("bg")
	n := NewNode("l", KindText)
	n.Style.Color = mgl32.Vec4{1, 1, 1, 1}
	s.Root.Children = []Node{n}

	path := UIPath("bg")
	if err := SaveScreen(s, path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "color:") {
		t.Errorf("an authored label background was not written:\n%s", data)
	}

	got, err := loadScreenBare(path)
	if err != nil {
		t.Fatal(err)
	}
	if c := got.Find("l").Style.Color; c[3] != 1 {
		t.Errorf("it came back as %v", c)
	}
}
