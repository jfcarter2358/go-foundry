package editor

// Drawing the AI editor, for real, with no window and no GL.
//
// The same guard editor_panel_draw_test.go stands: the canvas and the two
// panels are nearly all imgui stack discipline — every Begin, child, combo,
// popup and style push has to be closed on every path, including the early
// returns a body takes when there is nothing to draw — and imgui catches an
// imbalance with an assert inside End() that aborts the process. Drawing each
// state once here is what stands between a mistyped Begin and a crash the
// moment somebody opens the window.
//
// It is a smoke test, not a rendering test: what it asserts is that a frame
// completes. What each node *looks* like is aiNodeRows' and aiNodeHeight's, and
// those are tested directly in editor_aitree_test.go.

import (
	"testing"

	"foundry/internal/aitree"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// drawAIOnce runs one full imgui frame with the AI editor's three regions in
// it, at the window size the real editor uses.
//
// The window is faked rather than opened: aiEditorWindow only supplies W and H
// to the layout, so a bare one is enough to exercise every path without an OS
// window or a GL context.
func drawAIOnce() {
	io := imgui.CurrentIO()
	io.SetDisplaySize(imgui.Vec2{X: float32(aiWindowW), Y: float32(aiWindowH)})
	io.SetDeltaTime(1.0 / 60.0)
	imgui.NewFrame()

	aiRefreshProgram()
	aiEditorToolbar()
	aiEditorListPanel()
	aiEditorCanvasWindow()
	aiEditorInspector()
	aiEditorDialogs()

	imgui.Render()
}

// aiDrawTestDoc is one of everything the editor can be pointed at: a condition
// with a picker field, every native action, a helper, a note, a parameter, and
// a node with something wrong with it.
func aiDrawTestDoc() *aitree.Doc {
	d := &aitree.Doc{
		ID:          "drawtest",
		Description: "every shape of node",
		Params: []aitree.Param{
			{Name: "chance", Type: "float", Value: "0.5", Doc: "a note"},
			{Name: "broken", Type: "notatype", Value: "0"},
		},
	}
	t := &aitree.Tree{Event: "damage", Args: []string{"src", "amount"}, Root: "c1"}
	// One node per verb in both tables, so every branch of aiFieldControl —
	// operator combo, checkbox, state list, picker, argument list — is drawn.
	prev := ""
	for _, v := range aitree.Checks {
		n := &aitree.Node{ID: "c_" + v.Name, Kind: aitree.NodeCondition,
			Check: aitree.Call{Verb: v.Name}}
		if v.VarArgs {
			n.Check.Args = []string{"src"}
		}
		t.Nodes = append(t.Nodes, n)
		if prev != "" {
			t.Node(prev).Then = n.ID
		}
		prev = n.ID
	}
	for _, v := range aitree.Actions {
		n := &aitree.Node{ID: "a_" + v.Name, Kind: aitree.NodeAction,
			Do: aitree.Call{Verb: v.Name}}
		if v.VarArgs {
			n.Do.Args = []string{"src", "amount"}
		}
		t.Nodes = append(t.Nodes, n)
	}
	t.Nodes = append(t.Nodes,
		&aitree.Node{ID: "c1", Kind: aitree.NodeCondition, Label: "start",
			Check: aitree.Call{Verb: "var", Fields: map[string]string{"var": "dead", "op": "is true"}},
			Then:  "note1", Else: "c_has_target"},
		&aitree.Node{ID: "note1", Kind: aitree.NodeNote, Note: "why\nthis\nhappens"},
		// A verb this build does not have, and a link to a node that is not
		// there: both have their own branch in the form and on the canvas.
		&aitree.Node{ID: "bad", Kind: aitree.NodeAction,
			Do: aitree.Call{Verb: "no_such_verb", Fields: map[string]string{"mystery": "1"}}, Next: "nowhere"},
	)
	d.Trees = append(d.Trees, t,
		&aitree.Tree{Name: "helper", Args: []string{"x"}, Root: "h1", Nodes: []*aitree.Node{
			{ID: "h1", Kind: aitree.NodeAction, Do: aitree.Call{Verb: "result",
				Fields: map[string]string{"value": "x"}}},
		}},
		// A tree with no root at all: the canvas has its own message for it.
		&aitree.Tree{Event: "tick", Args: []string{"dt"}},
	)
	return d
}

func TestTheAIEditorDrawsInEveryState(t *testing.T) {
	prevWin := aiEditorWindow
	aiEditorWindow = &aiEditorWindowStub
	t.Cleanup(func() { aiEditorWindow = prevWin })

	doc := aiDrawTestDoc()
	withAIDoc(t, doc)

	withImgui(func() {
		// Nothing selected: the inspector shows the document.
		drawAIOnce()

		// Each node in turn, so every verb's generated form is drawn once.
		for _, tr := range doc.Trees {
			idx := -1
			for i, cand := range doc.Trees {
				if cand == tr {
					idx = i
				}
			}
			aiTreeIndex = idx
			for _, n := range tr.Nodes {
				aiSelectOnly(n.ID)
				drawAIOnce()
			}
		}

		// A multiple selection, and an empty document.
		aiTreeIndex = 0
		aiSelection = map[string]bool{"c1": true, "note1": true}
		drawAIOnce()

		aiSelection = map[string]bool{}
		withAIDoc(t, aitree.NewDoc("empty"))
		drawAIOnce()

		// The dialogs, each in its open state.
		aiShowOpenDialog = true
		drawAIOnce()
		aiShowOpenDialog = false
		aiShowSaveAsDialog = true
		drawAIOnce()
		aiShowSaveAsDialog = false
		aiShowAddEventMenu = true
		drawAIOnce()
		aiShowAddEventMenu = false
	})
}

func TestTheAIEditorDrawsAtEveryZoom(t *testing.T) {
	prevWin := aiEditorWindow
	aiEditorWindow = &aiEditorWindowStub
	t.Cleanup(func() { aiEditorWindow = prevWin })

	withAIDoc(t, aiDrawTestDoc())
	withImgui(func() {
		// Text is left off below 0.55 and the `yes`/`no` labels below 0.7, and
		// the grid gives up when its step is under six pixels: three branches
		// that are only reached by zooming.
		for _, z := range []float32{aiZoomMin, 0.5, 0.65, 1, aiZoomMax} {
			aiZoom = z
			drawAIOnce()
		}
		aiZoom = 1
		aiShowGrid = false
		drawAIOnce()
		aiShowGrid = true
	})
}

// aiEditorWindowStub stands in for the OS window. The layout reads only W and
// H off it, so there is nothing else to fake — and nothing here opens a window
// or touches GL.
var aiEditorWindowStub = uiwin.UIWindow{W: aiWindowW, H: aiWindowH}
