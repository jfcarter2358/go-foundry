package editor

// editor_aitree_canvas.go — the node graph: drawing it, and working on it with
// a mouse.
//
// The canvas is one InvisibleButton covering the middle of the window, painted
// into with the window's draw list. Everything on it is hand-drawn, because
// there is nothing in ImGui shaped like a node graph and because the boxes have
// to be in *document* coordinates: a node's position is saved in the file, so
// panning and zooming may not move it.
//
// Two transforms and nothing else:
//
//	screen = (doc + aiPan) * aiZoom + canvasOrigin
//	doc    = (screen - canvasOrigin) / aiZoom - aiPan
//
// Everything that hit-tests works in document units, so a click lands on the
// same node at any zoom, and the only things measured in pixels are the ones
// that must not scale — the port's grab radius, the wire's thickness, the text.
//
// INTERACTION, and what each gesture was chosen to avoid:
//
//	left on a node        select it; drag moves it (and everything else selected)
//	left on a port        drag a wire out; drop on a node links, drop on
//	                      nothing clears that link
//	left on empty         rubber band
//	middle or space+left  pan — never left alone, because left on empty is the
//	                      band, and a canvas where panning and selecting share
//	                      a button gets one of them wrong every time
//	wheel                 zoom about the pointer, so the thing under the mouse
//	                      stays under the mouse
//	right                 the add/act menu, on the node under it or on the
//	                      empty space it was opened over
//
// A node is drawn with its verb as the title and its filled-in fields beneath,
// so the picture says what the behaviour does without anything being selected.
// That is the whole reason for a graph editor over a list, and it is worth the
// text measurement it costs.

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"foundry/internal/aitree"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// View state. None of it is saved: where someone left the canvas is not part of
// the behaviour, and two people opening one file should not disagree about it.
var (
	aiPan        imgui.Vec2
	aiZoom       = float32(1)
	aiShowGrid   = true
	aiSnapToGrid = true
)

// Geometry, in document units.
const (
	aiNodeW       = float32(230)
	aiNodeTitleH  = float32(26)
	aiNodeRowH    = float32(17)
	aiNodeMinH    = float32(46)
	aiNodePadX    = float32(9)
	aiGridStep    = float32(20)
	aiGridMajor   = 5
	aiPortR       = float32(6)
	aiZoomMin     = float32(0.3)
	aiZoomMax     = float32(2.5)
	aiTidyStepX   = float32(270)
	aiTidyStepY   = float32(150)
	aiWireSegs    = 16
	aiMaxRows     = 5 // fields drawn on a box before it says "+n more"
	aiPortGrabPx  = float32(11)
	aiCanvasPadPx = float32(60)
)

// Drag state. One of these is live at a time.
type aiGestureKind int

const (
	aiGestureNone aiGestureKind = iota
	aiGestureNodes
	aiGestureWire
	aiGestureBand
	aiGesturePan
)

var (
	aiGesture   aiGestureKind
	aiDragFrom  string     // the node a wire is being dragged from
	aiDragPort  string     // which port of it
	aiBandStart imgui.Vec2 // in document units
	// aiDragMoved says the current node drag has actually moved something, so a
	// click that did not becomes a plain selection and pushes no undo.
	aiDragMoved bool
)

// Context menu state: where on the canvas it was opened, in document units.
var (
	aiMenuAt       imgui.Vec2
	aiMenuNode     string
	aiShowAddEvent bool
)

// aiResetView puts the canvas back to the origin at 1:1.
func aiResetView() {
	aiPan, aiZoom = imgui.Vec2{X: 60, Y: 60}, 1
}

// ---------- transforms ----------

// aiCanvasRect is where the canvas sits in the window.
func aiCanvasRect() (origin, size imgui.Vec2) {
	w := aiEditorWindow
	if w == nil {
		return imgui.Vec2{}, imgui.Vec2{}
	}
	x := aiListWidth
	width := float32(w.W) - aiListWidth - aiPanelWidth
	if width < 100 {
		width = 100
	}
	return imgui.Vec2{X: x, Y: aiToolbarH}, imgui.Vec2{X: width, Y: float32(w.H) - aiToolbarH}
}

func aiToScreen(origin imgui.Vec2, p imgui.Vec2) imgui.Vec2 {
	return imgui.Vec2{X: origin.X + (p.X+aiPan.X)*aiZoom, Y: origin.Y + (p.Y+aiPan.Y)*aiZoom}
}

func aiToDoc(origin imgui.Vec2, p imgui.Vec2) imgui.Vec2 {
	return imgui.Vec2{X: (p.X-origin.X)/aiZoom - aiPan.X, Y: (p.Y-origin.Y)/aiZoom - aiPan.Y}
}

// aiNodeHeight is how tall a box is, which depends on how much it has to say.
func aiNodeHeight(n *aitree.Node) float32 {
	if n.Kind == aitree.NodeNote {
		lines := float32(strings.Count(n.Note, "\n") + 1)
		return aiMaxF(aiNodeMinH, 14+lines*aiNodeRowH)
	}
	rows := float32(len(aiNodeRows(n)))
	if rows > aiMaxRows {
		rows = aiMaxRows + 1
	}
	return aiMaxF(aiNodeMinH, aiNodeTitleH+rows*aiNodeRowH+8)
}

// aiNodeRows is what a box shows under its title: each field the author filled
// in, and the argument list.
func aiNodeRows(n *aitree.Node) []string {
	call := n.Do
	if n.Kind == aitree.NodeCondition {
		call = n.Check
	}
	v := aitree.VerbOf(call.Verb)
	var out []string
	if v != nil {
		for _, f := range v.Fields {
			text := ""
			if call.Fields != nil {
				text = call.Fields[f.Name]
			}
			if text == "" {
				continue
			}
			// `of: @self` is the default on nearly every verb and saying it on
			// every box would drown what is actually being decided.
			if f.Name == "of" && text == "@self" {
				continue
			}
			out = append(out, f.Name+": "+text)
		}
	} else {
		keys := make([]string, 0, len(call.Fields))
		for k := range call.Fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, k+": "+call.Fields[k])
		}
	}
	if len(call.Args) > 0 {
		out = append(out, "("+strings.Join(call.Args, ", ")+")")
	}
	return out
}

// aiNodeRect is a node's box, in document units.
func aiNodeRect(n *aitree.Node) (min, max imgui.Vec2) {
	min = imgui.Vec2{X: n.X, Y: n.Y}
	max = imgui.Vec2{X: n.X + aiNodeW, Y: n.Y + aiNodeHeight(n)}
	return
}

// aiInPort is where a wire lands on a node: the middle of its top edge.
func aiInPort(n *aitree.Node) imgui.Vec2 {
	return imgui.Vec2{X: n.X + aiNodeW/2, Y: n.Y}
}

// aiOutPort is where a wire leaves. A condition has two, spaced across the
// bottom edge so `then` is always on the left; an action has one in the middle.
func aiOutPort(n *aitree.Node, port string) imgui.Vec2 {
	_, max := aiNodeRect(n)
	switch port {
	case "then":
		return imgui.Vec2{X: n.X + aiNodeW*0.28, Y: max.Y}
	case "else":
		return imgui.Vec2{X: n.X + aiNodeW*0.72, Y: max.Y}
	}
	return imgui.Vec2{X: n.X + aiNodeW/2, Y: max.Y}
}

// ---------- colours ----------

func aiCol(r, g, b, a float32) imgui.PackedColor {
	return imgui.PackedColorFromVec4(imgui.Vec4{X: r, Y: g, Z: b, W: a})
}

var (
	aiColCanvas      = aiCol(0.09, 0.10, 0.12, 1)
	aiColGrid        = aiCol(1, 1, 1, 0.04)
	aiColGridMajor   = aiCol(1, 1, 1, 0.09)
	aiColCondition   = aiCol(0.17, 0.26, 0.38, 1)
	aiColAction      = aiCol(0.19, 0.33, 0.25, 1)
	aiColNote        = aiCol(0.26, 0.24, 0.16, 1)
	aiColTitleCond   = aiCol(0.25, 0.39, 0.57, 1)
	aiColTitleAct    = aiCol(0.27, 0.48, 0.36, 1)
	aiColBorder      = aiCol(0, 0, 0, 0.55)
	aiColSelect      = aiCol(1, 0.62, 0.16, 1)
	aiColRoot        = aiCol(0.55, 0.85, 1, 1)
	aiColText        = aiCol(0.93, 0.94, 0.96, 1)
	aiColTextDim     = aiCol(0.70, 0.73, 0.78, 1)
	aiColWireThen    = aiCol(0.45, 0.80, 0.50, 0.95)
	aiColWireElse    = aiCol(0.85, 0.45, 0.42, 0.95)
	aiColWireNext    = aiCol(0.60, 0.66, 0.78, 0.95)
	aiColError       = aiCol(1, 0.35, 0.30, 1)
	aiColWarn        = aiCol(0.95, 0.78, 0.30, 1)
	aiColBand        = aiCol(1, 0.62, 0.16, 0.20)
	aiColBandEdge    = aiCol(1, 0.62, 0.16, 0.85)
	aiColPort        = aiCol(0.80, 0.84, 0.90, 1)
	aiColPortHover   = aiCol(1, 0.85, 0.35, 1)
	aiColEndOfBranch = aiCol(0.45, 0.48, 0.55, 0.8)
)

func aiWireColor(port string) imgui.PackedColor {
	switch port {
	case "then":
		return aiColWireThen
	case "else":
		return aiColWireElse
	}
	return aiColWireNext
}

// ---------- the frame ----------

// aiEditorCanvasWindow draws the middle of the window.
func aiEditorCanvasWindow() {
	origin, size := aiCanvasRect()
	imgui.SetNextWindowPos(origin)
	imgui.SetNextWindowSize(size)
	flags := imgui.WindowFlagsNoTitleBar | imgui.WindowFlagsNoResize | imgui.WindowFlagsNoMove |
		imgui.WindowFlagsNoScrollbar | imgui.WindowFlagsNoScrollWithMouse | imgui.WindowFlagsNoSavedSettings |
		imgui.WindowFlagsNoBringToFrontOnFocus
	imgui.PushStyleVarVec2(imgui.StyleVarWindowPadding, imgui.Vec2{})
	if imgui.BeginV("##aicanvas", nil, flags) {
		aiDrawCanvas(origin, size)
	}
	imgui.End()
	imgui.PopStyleVar()
}

func aiDrawCanvas(origin, size imgui.Vec2) {
	dl := imgui.WindowDrawList()
	dl.AddRectFilled(origin, imgui.Vec2{X: origin.X + size.X, Y: origin.Y + size.Y}, aiColCanvas)

	// The hit area. Everything below asks this button whether it is hovered or
	// held, so ImGui arbitrates between the canvas and anything drawn over it.
	imgui.SetCursorScreenPos(origin)
	imgui.InvisibleButtonV("##aicanvasbtn", size, imgui.ButtonFlagsMouseButtonLeft|
		imgui.ButtonFlagsMouseButtonRight|imgui.ButtonFlagsMouseButtonMiddle)
	hovered := imgui.IsItemHovered()

	if aiShowGrid {
		aiDrawGrid(dl, origin, size)
	}

	t := aiCurrentTree()
	if t == nil {
		dl.AddText(imgui.Vec2{X: origin.X + 20, Y: origin.Y + 20}, aiColTextDim,
			"No tree. Add one from the Tree menu.")
		return
	}

	mouseDoc := aiToDoc(origin, imgui.MousePos())
	hotNode, hotPort := aiHitTest(t, mouseDoc)

	aiHandleInput(t, origin, size, hovered, mouseDoc, hotNode, hotPort)

	aiDrawWires(dl, origin, t)
	aiDrawNodes(dl, origin, t, hotNode, hotPort)
	aiDrawOverlays(dl, origin, t)

	aiCanvasContextMenu(t)
}

// aiDrawGrid paints the backdrop. Drawn in screen space from the document
// origin, so the lines slide with the pan rather than with the window.
func aiDrawGrid(dl imgui.DrawList, origin, size imgui.Vec2) {
	step := aiGridStep * aiZoom
	if step < 6 {
		// Past this the grid is a grey wash and costs hundreds of lines.
		return
	}
	offX := float32(math.Mod(float64(aiPan.X*aiZoom), float64(step*aiGridMajor)))
	offY := float32(math.Mod(float64(aiPan.Y*aiZoom), float64(step*aiGridMajor)))
	n := 0
	for x := offX; x < size.X; x += step {
		col := aiColGrid
		if n%aiGridMajor == 0 {
			col = aiColGridMajor
		}
		n++
		dl.AddLine(imgui.Vec2{X: origin.X + x, Y: origin.Y},
			imgui.Vec2{X: origin.X + x, Y: origin.Y + size.Y}, col)
	}
	n = 0
	for y := offY; y < size.Y; y += step {
		col := aiColGrid
		if n%aiGridMajor == 0 {
			col = aiColGridMajor
		}
		n++
		dl.AddLine(imgui.Vec2{X: origin.X, Y: origin.Y + y},
			imgui.Vec2{X: origin.X + size.X, Y: origin.Y + y}, col)
	}
}

// aiDrawWires draws every link, under the boxes so a wire never covers text.
func aiDrawWires(dl imgui.DrawList, origin imgui.Vec2, t *aitree.Tree) {
	for _, n := range t.Nodes {
		if n.Kind == aitree.NodeNote {
			continue
		}
		for _, l := range n.Links() {
			from := aiToScreen(origin, aiOutPort(n, l.Port))
			if l.To == "" {
				// An empty link is the end of the run, and saying so on the
				// canvas is worth a stub: a branch that stops on purpose looks
				// the same as one nobody finished otherwise.
				aiDrawStub(dl, from, aiWireColor(l.Port))
				continue
			}
			to := t.Node(l.To)
			if to == nil {
				aiDrawStub(dl, from, aiColError)
				continue
			}
			aiDrawWire(dl, from, aiToScreen(origin, aiInPort(to)), aiWireColor(l.Port))
		}
	}
}

// aiDrawWire draws one link as a cubic bezier, flattened here because the draw
// list has no curve of its own. The control points pull straight down out of
// the source and straight up into the target, so a wire reads as flowing
// downward even when it has to come back up the canvas.
func aiDrawWire(dl imgui.DrawList, a, b imgui.Vec2, col imgui.PackedColor) {
	lift := aiMaxF(30*aiZoom, aiAbsF(b.Y-a.Y)*0.4)
	c1 := imgui.Vec2{X: a.X, Y: a.Y + lift}
	c2 := imgui.Vec2{X: b.X, Y: b.Y - lift}
	prev := a
	for i := 1; i <= aiWireSegs; i++ {
		p := aiBezier(a, c1, c2, b, float32(i)/aiWireSegs)
		dl.AddLineV(prev, p, col, aiMaxF(1.5, 2*aiZoom))
		prev = p
	}
	// An arrowhead at the target, so which way a wire runs is readable when two
	// nodes sit side by side.
	aiDrawArrow(dl, prev, b, col)
}

func aiBezier(p0, p1, p2, p3 imgui.Vec2, t float32) imgui.Vec2 {
	u := 1 - t
	a, b, c, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
	return imgui.Vec2{
		X: a*p0.X + b*p1.X + c*p2.X + d*p3.X,
		Y: a*p0.Y + b*p1.Y + c*p2.Y + d*p3.Y,
	}
}

func aiDrawArrow(dl imgui.DrawList, from, at imgui.Vec2, col imgui.PackedColor) {
	dx, dy := at.X-from.X, at.Y-from.Y
	l := float32(math.Hypot(float64(dx), float64(dy)))
	if l < 0.001 {
		dx, dy, l = 0, 1, 1
	}
	dx, dy = dx/l, dy/l
	s := aiMaxF(5, 7*aiZoom)
	dl.AddTriangleFilled(
		at,
		imgui.Vec2{X: at.X - dx*s - dy*s*0.55, Y: at.Y - dy*s + dx*s*0.55},
		imgui.Vec2{X: at.X - dx*s + dy*s*0.55, Y: at.Y - dy*s - dx*s*0.55},
		col)
}

// aiDrawStub is the short tail on a link that goes nowhere.
func aiDrawStub(dl imgui.DrawList, from imgui.Vec2, col imgui.PackedColor) {
	end := imgui.Vec2{X: from.X, Y: from.Y + 16*aiZoom}
	dl.AddLineV(from, end, aiColEndOfBranch, aiMaxF(1, 1.5*aiZoom))
	dl.AddCircleV(end, aiMaxF(2.5, 3.5*aiZoom), aiColEndOfBranch, 8, 1)
}

// aiDrawNodes paints every box.
func aiDrawNodes(dl imgui.DrawList, origin imgui.Vec2, t *aitree.Tree, hotNode, hotPort string) {
	for _, n := range t.Nodes {
		aiDrawNode(dl, origin, t, n, hotNode, hotPort)
	}
}

func aiDrawNode(dl imgui.DrawList, origin imgui.Vec2, t *aitree.Tree, n *aitree.Node, hotNode, hotPort string) {
	dmin, dmax := aiNodeRect(n)
	min, max := aiToScreen(origin, dmin), aiToScreen(origin, dmax)
	// Off screen: skip. A big behaviour is a few hundred nodes and most of them
	// are not in view.
	_, size := aiCanvasRect()
	if max.X < origin.X || min.X > origin.X+size.X || max.Y < origin.Y || min.Y > origin.Y+size.Y {
		return
	}

	body, title := aiColAction, aiColTitleAct
	switch n.Kind {
	case aitree.NodeCondition:
		body, title = aiColCondition, aiColTitleCond
	case aitree.NodeNote:
		body, title = aiColNote, aiColNote
	}
	round := 5 * aiZoom
	dl.AddRectFilledV(min, max, body, round, imgui.DrawFlagsRoundCornersAll)
	if n.Kind != aitree.NodeNote {
		dl.AddRectFilledV(min, imgui.Vec2{X: max.X, Y: min.Y + aiNodeTitleH*aiZoom}, title,
			round, imgui.DrawFlagsRoundCornersTop)
	}

	// Border: the selection first, then the root marker, then plain.
	switch {
	case aiSelection[n.ID]:
		dl.AddRectV(min, max, aiColSelect, round, imgui.DrawFlagsRoundCornersAll, aiMaxF(2, 2.5*aiZoom))
	case t.Root == n.ID:
		dl.AddRectV(min, max, aiColRoot, round, imgui.DrawFlagsRoundCornersAll, aiMaxF(1.5, 2*aiZoom))
	default:
		dl.AddRectV(min, max, aiColBorder, round, imgui.DrawFlagsRoundCornersAll, 1)
	}

	aiDrawNodeText(dl, t, n, min, max)
	if n.Kind != aitree.NodeNote {
		aiDrawPorts(dl, origin, n, hotNode, hotPort)
	}
	aiDrawNodeMarker(dl, t, n, min)
}

// aiDrawNodeText writes the title and the rows. Text is drawn at the font's own
// size whatever the zoom — a scaled bitmap font is unreadable — so past a zoom
// out it is simply left off and the boxes become a map.
func aiDrawNodeText(dl imgui.DrawList, t *aitree.Tree, n *aitree.Node, min, max imgui.Vec2) {
	if aiZoom < 0.55 {
		return
	}
	pad := aiNodePadX * aiZoom
	if n.Kind == aitree.NodeNote {
		aiClippedText(dl, imgui.Vec2{X: min.X + pad, Y: min.Y + 6}, max.X-pad, aiColText, n.Note)
		return
	}

	title := n.Label
	if title == "" {
		call := n.Do
		if n.Kind == aitree.NodeCondition {
			call = n.Check
		}
		if v := aitree.VerbOf(call.Verb); v != nil {
			title = v.Summary
		} else if call.Verb != "" {
			title = call.Verb + " (unknown)"
		} else {
			title = "(no verb)"
		}
	}
	if t.Root == n.ID {
		title = "▶ " + title
	}
	aiClippedText(dl, imgui.Vec2{X: min.X + pad, Y: min.Y + 5*aiZoom}, max.X-pad, aiColText, title)

	rows := aiNodeRows(n)
	y := min.Y + aiNodeTitleH*aiZoom + 3
	for i, r := range rows {
		if i >= aiMaxRows {
			aiClippedText(dl, imgui.Vec2{X: min.X + pad, Y: y}, max.X-pad, aiColTextDim,
				fmt.Sprintf("+%d more", len(rows)-aiMaxRows))
			break
		}
		aiClippedText(dl, imgui.Vec2{X: min.X + pad, Y: y}, max.X-pad, aiColTextDim, r)
		y += aiNodeRowH * aiZoom
	}
}

// aiClippedText draws one line, cut to fit the box with an ellipsis. The draw
// list will happily paint text out past a node's edge and over its neighbour,
// which at a glance reads as the neighbour's content.
func aiClippedText(dl imgui.DrawList, at imgui.Vec2, right float32, col imgui.PackedColor, s string) {
	if s == "" {
		return
	}
	s = strings.ReplaceAll(s, "\n", " ")
	avail := right - at.X
	if avail <= 4 {
		return
	}
	if imgui.CalcTextSize(s, false, 0).X > avail {
		for len(s) > 1 && imgui.CalcTextSize(s+"…", false, 0).X > avail {
			s = s[:len(s)-1]
		}
		s += "…"
	}
	dl.AddText(at, col, s)
}

// aiDrawPorts paints the sockets a wire leaves and arrives at.
func aiDrawPorts(dl imgui.DrawList, origin imgui.Vec2, n *aitree.Node, hotNode, hotPort string) {
	r := aiMaxF(3.5, aiPortR*aiZoom)
	dl.AddCircleFilled(aiToScreen(origin, aiInPort(n)), r*0.8, aiColPort)
	for _, l := range n.Links() {
		col := aiColPort
		if hotNode == n.ID && hotPort == l.Port {
			col = aiColPortHover
		}
		dl.AddCircleFilled(aiToScreen(origin, aiOutPort(n, l.Port)), r, col)
	}
	// `then` and `else` are worth labelling: which side is which is the one
	// thing a reader has to be sure of, and colour alone does not survive a
	// screenshot or a colour-blind reader.
	if n.Kind == aitree.NodeCondition && aiZoom >= 0.7 {
		y := aiToScreen(origin, aiOutPort(n, "then")).Y - 15*aiZoom
		dl.AddText(imgui.Vec2{X: aiToScreen(origin, aiOutPort(n, "then")).X - 12, Y: y}, aiColWireThen, "yes")
		dl.AddText(imgui.Vec2{X: aiToScreen(origin, aiOutPort(n, "else")).X - 8, Y: y}, aiColWireElse, "no")
	}
}

// aiDrawNodeMarker puts a dot on a node the compiler complained about.
func aiDrawNodeMarker(dl imgui.DrawList, t *aitree.Tree, n *aitree.Node, min imgui.Vec2) {
	diags := aiProg.DiagsFor(t, n.ID)
	if len(diags) == 0 {
		return
	}
	col := aiColWarn
	for _, d := range diags {
		if !d.Warning {
			col = aiColError
		}
	}
	dl.AddCircleFilled(imgui.Vec2{X: min.X + 6, Y: min.Y + 6}, aiMaxF(3, 4*aiZoom), col)
}

// aiDrawOverlays paints what is in flight: the wire being dragged, and the
// rubber band.
func aiDrawOverlays(dl imgui.DrawList, origin imgui.Vec2, t *aitree.Tree) {
	switch aiGesture {
	case aiGestureWire:
		if n := t.Node(aiDragFrom); n != nil {
			aiDrawWire(dl, aiToScreen(origin, aiOutPort(n, aiDragPort)), imgui.MousePos(), aiWireColor(aiDragPort))
		}
	case aiGestureBand:
		a := aiToScreen(origin, aiBandStart)
		b := imgui.MousePos()
		min := imgui.Vec2{X: aiMinF(a.X, b.X), Y: aiMinF(a.Y, b.Y)}
		max := imgui.Vec2{X: aiMaxF(a.X, b.X), Y: aiMaxF(a.Y, b.Y)}
		dl.AddRectFilled(min, max, aiColBand)
		dl.AddRectV(min, max, aiColBandEdge, 0, imgui.DrawFlagsRoundCornersNone, 1)
	}
}

// ---------- hit testing ----------

// aiHitTest returns the node under a document point and, when the point is on
// one of its output ports, which port. Walked backwards so the node drawn last
// — the one on top — wins.
func aiHitTest(t *aitree.Tree, p imgui.Vec2) (node, port string) {
	grab := aiPortGrabPx / aiZoom
	for i := len(t.Nodes) - 1; i >= 0; i-- {
		n := t.Nodes[i]
		if n.Kind != aitree.NodeNote {
			for _, l := range n.Links() {
				if aiNear(p, aiOutPort(n, l.Port), grab) {
					return n.ID, l.Port
				}
			}
		}
		min, max := aiNodeRect(n)
		if p.X >= min.X && p.X <= max.X && p.Y >= min.Y && p.Y <= max.Y {
			return n.ID, ""
		}
	}
	return "", ""
}

func aiNear(a, b imgui.Vec2, r float32) bool {
	dx, dy := a.X-b.X, a.Y-b.Y
	return dx*dx+dy*dy <= r*r
}

// ---------- input ----------

func aiHandleInput(t *aitree.Tree, origin, size imgui.Vec2, hovered bool, mouseDoc imgui.Vec2, hotNode, hotPort string) {
	io := imgui.CurrentIO()

	// Zoom about the pointer: the document point under the mouse before the
	// zoom is put back under it after, which is the only zoom that does not
	// feel like the canvas is running away.
	if _, wheel := io.MouseWheel(); hovered && wheel != 0 {
		before := aiToDoc(origin, imgui.MousePos())
		aiZoom = clampF(aiZoom*float32(math.Pow(1.12, float64(wheel))), aiZoomMin, aiZoomMax)
		after := aiToDoc(origin, imgui.MousePos())
		aiPan.X += after.X - before.X
		aiPan.Y += after.Y - before.Y
	}

	// Pan: middle drag, or space with the left button — never left alone, since
	// left on empty is the rubber band.
	panning := imgui.IsMouseDragging(1, 2) || imgui.IsMouseDragging(2, 2) ||
		(imgui.IsMouseDragging(0, 2) && imgui.IsKeyPressed(int(imgui.KeySpace)))
	if aiGesture == aiGestureNone && hovered && panning {
		aiGesture = aiGesturePan
	}
	if aiGesture == aiGesturePan {
		d := io.MouseDelta()
		aiPan.X += d.X / aiZoom
		aiPan.Y += d.Y / aiZoom
		if !imgui.IsMouseDown(1) && !imgui.IsMouseDown(2) && !imgui.IsMouseDown(0) {
			aiGesture = aiGestureNone
		}
		return
	}

	// Press.
	if hovered && imgui.IsMouseClicked(0) {
		switch {
		case hotPort != "":
			aiGesture, aiDragFrom, aiDragPort = aiGestureWire, hotNode, hotPort
		case hotNode != "":
			if !aiSelection[hotNode] {
				if io.KeyShiftPressed() || io.KeyCtrlPressed() {
					aiSelection[hotNode] = true
				} else {
					aiSelectOnly(hotNode)
				}
			} else if io.KeyCtrlPressed() {
				delete(aiSelection, hotNode)
			}
			aiGesture, aiDragMoved = aiGestureNodes, false
		default:
			if !io.KeyShiftPressed() && !io.KeyCtrlPressed() {
				aiSelection = map[string]bool{}
			}
			aiGesture, aiBandStart = aiGestureBand, mouseDoc
		}
	}

	// Drag.
	switch aiGesture {
	case aiGestureNodes:
		d := io.MouseDelta()
		if d.X != 0 || d.Y != 0 {
			if !aiDragMoved {
				// Pushed once per drag, at the first movement, so a click that
				// only selected does not fill the undo stack.
				AIPushUndo()
				aiDragMoved = true
			}
			for id := range aiSelection {
				if n := t.Node(id); n != nil {
					n.X += d.X / aiZoom
					n.Y += d.Y / aiZoom
				}
			}
			aiDoc.Touch()
		}
	}

	// Release.
	if imgui.IsMouseReleased(0) {
		switch aiGesture {
		case aiGestureWire:
			aiFinishWire(t, hotNode)
		case aiGestureBand:
			aiFinishBand(t, mouseDoc, io.KeyShiftPressed() || io.KeyCtrlPressed())
		case aiGestureNodes:
			if aiDragMoved && aiSnapToGrid {
				for id := range aiSelection {
					if n := t.Node(id); n != nil {
						n.X = aiSnapF(n.X, aiGridStep)
						n.Y = aiSnapF(n.Y, aiGridStep)
					}
				}
				aiDoc.Touch()
			}
		}
		aiGesture = aiGestureNone
	}

	// A double-click on empty canvas is the quickest way to add a node, and the
	// one gesture a graph editor is expected to have.
	if hovered && hotNode == "" && imgui.IsMouseDoubleClicked(0) {
		aiMenuAt, aiMenuNode = mouseDoc, ""
		imgui.OpenPopup("##aicanvasmenu")
	}

	if hovered && imgui.IsMouseClicked(1) {
		aiMenuAt, aiMenuNode = mouseDoc, hotNode
		if hotNode != "" && !aiSelection[hotNode] {
			aiSelectOnly(hotNode)
		}
		imgui.OpenPopup("##aicanvasmenu")
	}
}

// aiFinishWire links, or unlinks, whatever the wire was dropped on.
func aiFinishWire(t *aitree.Tree, onto string) {
	from := t.Node(aiDragFrom)
	if from == nil {
		return
	}
	// Dropping a wire back on its own node would make a one-node loop, which
	// the runtime would stop and the author did not mean.
	if onto == aiDragFrom {
		return
	}
	if from.LinkTo(aiDragPort) == onto {
		return
	}
	AIPushUndo()
	from.SetLink(aiDragPort, onto)
	aiDoc.Touch()
	if onto == "" {
		aiStatus = fmt.Sprintf("%s: %s now ends the run", from.ID, aiDragPort)
	}
}

// aiFinishBand selects everything the rubber band touched.
func aiFinishBand(t *aitree.Tree, to imgui.Vec2, add bool) {
	min := imgui.Vec2{X: aiMinF(aiBandStart.X, to.X), Y: aiMinF(aiBandStart.Y, to.Y)}
	max := imgui.Vec2{X: aiMaxF(aiBandStart.X, to.X), Y: aiMaxF(aiBandStart.Y, to.Y)}
	if !add {
		aiSelection = map[string]bool{}
	}
	for _, n := range t.Nodes {
		nmin, nmax := aiNodeRect(n)
		if nmax.X >= min.X && nmin.X <= max.X && nmax.Y >= min.Y && nmin.Y <= max.Y {
			aiSelection[n.ID] = true
		}
	}
}

// ---------- the context menu ----------

func aiCanvasContextMenu(t *aitree.Tree) {
	if !imgui.BeginPopup("##aicanvasmenu") {
		return
	}
	if aiMenuNode != "" {
		n := t.Node(aiMenuNode)
		if n != nil {
			imgui.Text(aiMenuNode)
			imgui.Separator()
			if imgui.MenuItemV("Make Root", "", t.Root == n.ID, n.Kind != aitree.NodeNote) {
				AIPushUndo()
				t.Root = n.ID
				aiDoc.Touch()
			}
			editorMenuTooltip("Where this tree starts when it fires.")
			if imgui.MenuItem("Duplicate") {
				aiDuplicateSelection()
			}
			if imgui.MenuItem("Delete") {
				aiDeleteSelection()
			}
			imgui.Separator()
			if imgui.MenuItem("Clear Links") {
				AIPushUndo()
				for _, l := range n.Links() {
					n.SetLink(l.Port, "")
				}
				aiDoc.Touch()
			}
			imgui.Separator()
		}
	}
	if imgui.BeginMenu("Add Condition") {
		for i := range aitree.Checks {
			v := &aitree.Checks[i]
			if imgui.MenuItem(v.Summary + "##ck" + v.Name) {
				aiAddNode(t, aitree.NodeCondition, v.Name)
			}
			editorMenuTooltip(v.Doc)
		}
		imgui.EndMenu()
	}
	if imgui.BeginMenu("Add Action") {
		for i := range aitree.Actions {
			v := &aitree.Actions[i]
			if imgui.MenuItem(v.Summary + "##ac" + v.Name) {
				aiAddNode(t, aitree.NodeAction, v.Name)
			}
			editorMenuTooltip(v.Doc)
		}
		imgui.EndMenu()
	}
	if imgui.MenuItem("Add Note") {
		aiAddNode(t, aitree.NodeNote, "")
	}
	editorMenuTooltip("A box that is never run: somewhere to say why.")
	imgui.EndPopup()
}

// aiAddNode drops a new node where the menu was opened, wired into the
// selection when exactly one node was selected with a free port — which is what
// makes building a chain a sequence of right-clicks rather than of drags.
func aiAddNode(t *aitree.Tree, kind aitree.NodeKind, verb string) {
	AIPushUndo()
	n := &aitree.Node{Kind: kind, X: aiMenuAt.X, Y: aiMenuAt.Y}
	if aiSnapToGrid {
		n.X, n.Y = aiSnapF(n.X, aiGridStep), aiSnapF(n.Y, aiGridStep)
	}
	switch kind {
	case aitree.NodeCondition:
		n.Check = aitree.Call{Verb: verb}
	case aitree.NodeAction:
		n.Do = aitree.Call{Verb: verb}
	case aitree.NodeNote:
		n.Note = "note"
	}
	t.AddNode(n)

	if kind != aitree.NodeNote && len(aiSelection) == 1 {
		for id := range aiSelection {
			if prev := t.Node(id); prev != nil {
				for _, l := range prev.Links() {
					if l.To == "" {
						prev.SetLink(l.Port, n.ID)
						break
					}
				}
			}
		}
	}
	aiSelectOnly(n.ID)
	aiDoc.Touch()
}

// ---------- operations on the selection ----------

func aiSelectAll() {
	t := aiCurrentTree()
	if t == nil {
		return
	}
	aiSelection = map[string]bool{}
	for _, n := range t.Nodes {
		aiSelection[n.ID] = true
	}
}

func aiDeleteSelection() {
	t := aiCurrentTree()
	if t == nil || len(aiSelection) == 0 {
		return
	}
	AIPushUndo()
	for id := range aiSelection {
		t.RemoveNode(id)
	}
	aiSelection = map[string]bool{}
	aiDoc.Touch()
}

// aiDuplicateSelection copies the selected nodes, keeping the links *between*
// them and dropping the ones that led out of the selection. Copying a branch
// and having it still wired into the original's children would be a surprise
// every time.
func aiDuplicateSelection() {
	t := aiCurrentTree()
	if t == nil || len(aiSelection) == 0 {
		return
	}
	AIPushUndo()
	remap := map[string]string{}
	var added []*aitree.Node
	// Sorted, so the new ids are handed out in a stable order and a duplicate
	// is reproducible.
	ids := make([]string, 0, len(aiSelection))
	for id := range aiSelection {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		src := t.Node(id)
		if src == nil {
			continue
		}
		c := src.Clone()
		c.ID = t.FreeID("n")
		c.X += aiGridStep * 2
		c.Y += aiGridStep * 2
		remap[id] = c.ID
		t.Nodes = append(t.Nodes, c)
		added = append(added, c)
	}
	for _, c := range added {
		for _, l := range c.Links() {
			c.SetLink(l.Port, remap[l.To])
		}
	}
	aiSelection = map[string]bool{}
	for _, c := range added {
		aiSelection[c.ID] = true
	}
	aiDoc.Touch()
}

// ---------- layout ----------

// aiFrameAll pans and zooms so the whole tree fits.
func aiFrameAll() {
	t := aiCurrentTree()
	origin, size := aiCanvasRect()
	if t == nil || len(t.Nodes) == 0 || size.X <= 0 {
		aiResetView()
		return
	}
	_ = origin
	min := imgui.Vec2{X: math.MaxFloat32, Y: math.MaxFloat32}
	max := imgui.Vec2{X: -math.MaxFloat32, Y: -math.MaxFloat32}
	for _, n := range t.Nodes {
		a, b := aiNodeRect(n)
		min.X, min.Y = aiMinF(min.X, a.X), aiMinF(min.Y, a.Y)
		max.X, max.Y = aiMaxF(max.X, b.X), aiMaxF(max.Y, b.Y)
	}
	w, h := max.X-min.X, max.Y-min.Y
	if w <= 0 || h <= 0 {
		aiResetView()
		return
	}
	aiZoom = clampF(aiMinF((size.X-2*aiCanvasPadPx)/w, (size.Y-2*aiCanvasPadPx)/h), aiZoomMin, aiZoomMax)
	aiPan = imgui.Vec2{
		X: (size.X/aiZoom-w)/2 - min.X,
		Y: (size.Y/aiZoom-h)/2 - min.Y,
	}
}

// aiTidyTree lays the current tree out from its root: each node below the one
// it came from, `then` to the left and `else` to the right, and anything
// unreachable parked in a row underneath.
//
// It only moves boxes. What a node links to is the document and this cannot
// touch it, which is what makes Tidy safe to press on a behaviour someone else
// drew.
func aiTidyTree() {
	t := aiCurrentTree()
	if t == nil {
		return
	}
	AIPushUndo()
	placed := map[string]bool{}
	// nextX is the left edge of the next free column, so two branches that
	// happen to be the same depth cannot land on top of each other.
	nextX := float32(0)
	var place func(id string, depth int) float32
	place = func(id string, depth int) float32 {
		n := t.Node(id)
		if n == nil || placed[id] {
			return nextX
		}
		placed[id] = true
		var kids []string
		for _, l := range n.Links() {
			if l.To != "" && !placed[l.To] {
				kids = append(kids, l.To)
			}
		}
		if len(kids) == 0 {
			x := nextX
			nextX += aiTidyStepX
			n.X, n.Y = x, float32(depth)*aiTidyStepY
			return x
		}
		first, last := float32(0), float32(0)
		for i, k := range kids {
			cx := place(k, depth+1)
			if i == 0 {
				first = cx
			}
			last = cx
		}
		// Centred over its children, which is what makes a tree look like one.
		n.X, n.Y = (first+last)/2, float32(depth)*aiTidyStepY
		return n.X
	}
	if t.Root != "" {
		place(t.Root, 0)
	}
	// Whatever the root could not reach, in a row below everything.
	deepest := float32(0)
	for _, n := range t.Nodes {
		if placed[n.ID] {
			deepest = aiMaxF(deepest, n.Y)
		}
	}
	orphanX := float32(0)
	for _, n := range t.Nodes {
		if placed[n.ID] {
			continue
		}
		n.X, n.Y = orphanX, deepest+aiTidyStepY*1.5
		orphanX += aiTidyStepX
	}
	if aiSnapToGrid {
		for _, n := range t.Nodes {
			n.X, n.Y = aiSnapF(n.X, aiGridStep), aiSnapF(n.Y, aiGridStep)
		}
	}
	aiDoc.Touch()
	aiFrameAll()
	aiStatus = "Tidied " + t.Title()
}

// ---------- little maths ----------

func aiMaxF(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func aiMinF(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func aiAbsF(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func aiSnapF(v, step float32) float32 {
	if step <= 0 {
		return v
	}
	return float32(math.Round(float64(v/step))) * step
}
