package editor

// Drawing the UI editor's hierarchy, for real, with no window and no GL.
//
// Every structural edit a row of the tree offers — its context menu and its
// drop target — used to run in the middle of the walk that drew the row. A
// delete shrank the Children slice the walk was ranging over and the next row
// indexed past the end, which took the whole process down. These draw the tree
// with each edit queued the way a row queues it, so it runs where it has to.

import (
	"testing"

	"foundry/internal/ui"
	"foundry/internal/uiwin"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// drawUIHierarchyOnce runs one full imgui frame with the hierarchy in it.
func drawUIHierarchyOnce(w *uiwin.UIWindow) {
	io := imgui.CurrentIO()
	io.SetDisplaySize(imgui.Vec2{X: float32(w.W), Y: float32(w.H)})
	io.SetDeltaTime(1.0 / 60.0)
	imgui.NewFrame()
	uiEditorHierarchy(w)
	imgui.Render()
}

// uiHierarchyTestDoc is a root with three children, the middle one holding a
// grandchild, so a delete or reparent can land at the start, middle and end of
// a sibling list and at depth.
func uiHierarchyTestDoc(t *testing.T) {
	t.Helper()
	uiTestDoc(t)
	t.Cleanup(func() { uiHierarchyPending = nil })
	mid := ui.NewNode("mid", ui.KindPanel)
	mid.Children = []ui.Node{ui.NewNode("inner", ui.KindText)}
	uiDoc.Root.Children = append(uiDoc.Root.Children, mid, ui.NewNode("last", ui.KindButton))
}

func TestUIHierarchyEditsSurviveTheTreeWalk(t *testing.T) {
	w := &uiwin.UIWindow{W: 1440, H: 900}

	cases := []struct {
		name  string
		op    func()
		check func(t *testing.T)
	}{
		{"delete the first child", func() { uiSel = "box"; uiDeleteSelection() }, func(t *testing.T) {
			if uiDoc.Find("box") != nil || len(uiDoc.Root.Children) != 2 {
				t.Errorf("children are %v, want box gone", uiChildIDs(uiDoc.Root.Children))
			}
		}},
		{"delete a middle child with children", func() { uiSel = "mid"; uiDeleteSelection() }, func(t *testing.T) {
			if uiDoc.Find("mid") != nil || uiDoc.Find("inner") != nil {
				t.Error("mid or its child survived the delete")
			}
		}},
		{"delete the last child", func() { uiSel = "last"; uiDeleteSelection() }, func(t *testing.T) {
			if uiDoc.Find("last") != nil {
				t.Error("last survived the delete")
			}
		}},
		{"delete a grandchild", func() { uiSel = "inner"; uiDeleteSelection() }, func(t *testing.T) {
			if uiDoc.Find("inner") != nil {
				t.Error("inner survived the delete")
			}
		}},
		{"duplicate", func() { uiSel = "mid"; uiDuplicateSelection() }, func(t *testing.T) {
			if len(uiDoc.Root.Children) != 4 {
				t.Errorf("children are %v, want a copy of mid", uiChildIDs(uiDoc.Root.Children))
			}
		}},
		{"wrap in panel", func() { uiSel = "box"; uiWrapInPanel() }, func(t *testing.T) {
			if p, _ := uiLocate("box"); p == nil || p.ID == uiDoc.Root.ID {
				t.Error("box is not inside a wrapper")
			}
		}},
		{"reparent out of a sibling list", func() { uiReparent("box", "mid") }, func(t *testing.T) {
			if p, _ := uiLocate("box"); p == nil || p.ID != "mid" {
				t.Error("box did not move under mid")
			}
		}},
		{"add a child", func() { uiSel = "mid"; uiAddNode(ui.KindImage) }, func(t *testing.T) {
			if mid := uiDoc.Find("mid"); mid == nil || len(mid.Children) != 2 {
				t.Error("mid did not gain a child")
			}
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			uiHierarchyTestDoc(t)
			withImgui(func() {
				drawUIHierarchyOnce(w)
				uiHierarchyDefer(c.op)
				drawUIHierarchyOnce(w)
				// And once more, drawing the tree the edit left behind.
				drawUIHierarchyOnce(w)
			})
			if uiHierarchyPending != nil {
				t.Error("the queued edit was never run")
			}
			c.check(t)
		})
	}
}
