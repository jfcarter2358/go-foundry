package editor

// editor_aitree_panel.go — the two side panels: the document on the left, the
// selected node on the right.
//
// The right panel is the one that matters, and it is generated rather than
// written. A node is a verb from aitree's table, and that table declares what
// fields the verb has, what each is for and what to complete it against — so
// this file draws a form *from the table*, and adding a verb to the vocabulary
// adds its form here with no code.
//
// The alternative — a hand-written panel per verb — was what made the first
// sketch of this editor unmaintainable: twenty verbs is twenty near-identical
// blocks, and every one of them is a place for the form and the runtime to
// disagree about what a field is called.

import (
	"fmt"
	"sort"
	"strings"

	"foundry/internal/aitree"
	"foundry/internal/imguibackend"
	"foundry/internal/script"
	"foundry/internal/world"

	imgui "github.com/inkyblackness/imgui-go/v4"
)

// aiShowAddEventMenu opens the "which event?" dialog from the Tree menu.
var aiShowAddEventMenu bool

// aiNewEventName is what that dialog has typed in it.
var aiNewEventName string

// ---------- the left panel: trees, parameters, problems ----------

func aiEditorListPanel() {
	w := aiEditorWindow
	if w == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: 0, Y: aiToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: aiListWidth, Y: float32(w.H) - aiToolbarH})
	flags := imgui.WindowFlagsNoTitleBar | imgui.WindowFlagsNoResize | imgui.WindowFlagsNoMove |
		imgui.WindowFlagsNoSavedSettings | imgui.WindowFlagsNoBringToFrontOnFocus
	if imgui.BeginV("##ailist", nil, flags) {
		aiTreeList()
		imgui.Separator()
		aiParamsSection()
		imgui.Separator()
		aiProblemList()
	}
	imgui.End()
}

// aiTreeList is the document's entry points: the event roots first, then the
// helpers, because that is the order someone reads a behaviour in.
func aiTreeList() {
	imgui.Text("Trees")
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Each one is an entry point. An event root runs when that\n" +
			"message reaches the entity; a helper runs when another tree\n" +
			"calls it.")
	}
	if aiDoc == nil {
		return
	}
	imgui.BeginChildV("##aitreelist", imgui.Vec2{X: 0, Y: 190}, true, 0)
	for pass := 0; pass < 2; pass++ {
		for i, t := range aiDoc.Trees {
			if (pass == 0) == t.IsHelper() {
				continue
			}
			label := t.Title()
			if len(t.Args) > 0 && !t.IsHelper() {
				label += "  (" + strings.Join(t.Args, ", ") + ")"
			}
			// Latched before the widget: clicking it changes which tree is
			// open, and re-asking would pop a colour that was never pushed.
			bad := aiTreeHasErrors(t)
			if bad {
				imgui.PushStyleColor(imgui.StyleColorText, imgui.Vec4{X: 1, Y: 0.45, Z: 0.4, W: 1})
			}
			if imguibackend.SelectableV(label+"##ait"+fmt.Sprint(i), i == aiTreeIndex, 0, imgui.Vec2{}) {
				aiTreeIndex = i
				aiSelection = map[string]bool{}
				aiFrameAll()
			}
			if bad {
				imgui.PopStyleColor()
			}
		}
	}
	imgui.EndChild()

	if t := aiCurrentTree(); t != nil {
		aiTreeHeaderFields(t)
	}
}

// aiTreeHasErrors reports whether the compiler said anything non-warning about
// a tree, so the list can mark it.
func aiTreeHasErrors(t *aitree.Tree) bool {
	if aiProg == nil {
		return false
	}
	title := t.Title()
	for _, d := range aiProg.Diags {
		if d.Tree == title && !d.Warning {
			return true
		}
	}
	return false
}

// aiTreeHeaderFields edits the selected tree itself: what fires it, what its
// arguments are called, and the note on it.
func aiTreeHeaderFields(t *aitree.Tree) {
	imgui.PushItemWidth(-1)
	defer imgui.PopItemWidth()

	if t.IsHelper() {
		name := t.Name
		if aiInputText("Name##aitname", &name) {
			AIPushUndo()
			t.Name = name
			aiDoc.Touch()
		}
		aiHelp("What `run` and the `ask` check name it by.")
	} else {
		ev := t.Event
		if aiInputText("Event##aitev", &ev) {
			AIPushUndo()
			t.Event = ev
			aiDoc.Touch()
		}
		aiHelp("The message this root answers, without its colons.\n" +
			"`damage`, `tick`, `target_acquired`, `anim_end` — anything the\n" +
			"engine posts or another script sends.")
	}

	args := strings.Join(t.Args, ", ")
	if aiInputText("Arguments##aitargs", &args) {
		AIPushUndo()
		t.Args = nil
		for _, a := range strings.Split(args, ",") {
			if a = strings.TrimSpace(a); a != "" {
				t.Args = append(t.Args, a)
			}
		}
		aiDoc.Touch()
	}
	aiHelp("Names for what the message carries, in order — these are what a\n" +
		"node's fields can mention. `:damage:` carries (src, amount);\n" +
		"`:anim_end:` carries (src, model, anim).")

	doc := t.Doc
	if aiInputText("Note##aitdoc", &doc) {
		AIPushUndo()
		t.Doc = doc
		aiDoc.Touch()
	}
}

// aiParamsSection is the document's declared tunables: the numbers an entity
// type or a placement overrides in its own `params:` block.
func aiParamsSection() {
	if aiDoc == nil {
		return
	}
	if !imgui.CollapsingHeader(fmt.Sprintf("Parameters (%d)##aiparams", len(aiDoc.Params))) {
		return
	}
	editorTextMutedWrapped("Declared here, read in any field as ?name, and set per entity type " +
		"or per placement by the same params: block a script's are.")
	remove := -1
	for i := range aiDoc.Params {
		p := &aiDoc.Params[i]
		imgui.PushID(fmt.Sprintf("aiparam%d", i))
		imgui.PushItemWidth(-1)
		name := p.Name
		if aiInputText("Name", &name) {
			AIPushUndo()
			p.Name = name
			aiDoc.Touch()
		}
		if imguibackend.BeginCombo("Type", p.Type) {
			for _, ty := range aitree.ParamTypes {
				if imguibackend.SelectableV(ty, ty == p.Type, 0, imgui.Vec2{}) {
					AIPushUndo()
					p.Type = ty
					aiDoc.Touch()
				}
			}
			imgui.EndCombo()
		}
		val := p.Value
		if aiInputText("Default", &val) {
			AIPushUndo()
			p.Value = val
			aiDoc.Touch()
		}
		doc := p.Doc
		if aiInputText("Note", &doc) {
			AIPushUndo()
			p.Doc = doc
			aiDoc.Touch()
		}
		imgui.PopItemWidth()
		if imgui.Button("Remove") {
			remove = i
		}
		imgui.PopID()
		imgui.Separator()
	}
	if remove >= 0 {
		AIPushUndo()
		aiDoc.Params = append(aiDoc.Params[:remove], aiDoc.Params[remove+1:]...)
		aiDoc.Touch()
	}
	if imgui.Button("Add Parameter##aiaddparam") {
		AIPushUndo()
		aiDoc.Params = append(aiDoc.Params, aitree.Param{Name: "value", Type: "float", Value: "0.0"})
		aiDoc.Touch()
	}
}

// aiProblemList names everything wrong with the document, and takes you there.
func aiProblemList() {
	if aiProg == nil {
		return
	}
	if !imgui.CollapsingHeaderV(fmt.Sprintf("Problems (%d)##aiproblems", len(aiProg.Diags)),
		imgui.TreeNodeFlagsDefaultOpen) {
		return
	}
	if len(aiProg.Diags) == 0 {
		editorTextMuted("Nothing to report.")
		return
	}
	imgui.BeginChildV("##aiproblemlist", imgui.Vec2{X: 0, Y: 200}, true, 0)
	for i, d := range aiProg.Diags {
		col := imgui.Vec4{X: 1, Y: 0.45, Z: 0.4, W: 1}
		if d.Warning {
			col = imgui.Vec4{X: 0.95, Y: 0.8, Z: 0.35, W: 1}
		}
		imgui.PushStyleColor(imgui.StyleColorText, col)
		label := d.Msg
		if d.Node != "" {
			label = d.Node + ": " + d.Msg
		}
		if imguibackend.SelectableV(aiOneLine(label)+"##aipb"+fmt.Sprint(i), false, 0, imgui.Vec2{}) {
			aiGoTo(d)
		}
		imgui.PopStyleColor()
		if imgui.IsItemHovered() {
			imgui.SetTooltip(d.Error())
		}
	}
	imgui.EndChild()
}

// aiGoTo opens the tree a diagnostic is about and selects its node.
func aiGoTo(d aitree.Diagnostic) {
	for i, t := range aiDoc.Trees {
		if t.Title() != d.Tree {
			continue
		}
		aiTreeIndex = i
		aiSelectOnly(d.Node)
		aiCentreOn(d.Node)
		return
	}
}

// aiCentreOn pans the canvas so a node is in the middle of it.
func aiCentreOn(id string) {
	t := aiCurrentTree()
	if t == nil {
		return
	}
	n := t.Node(id)
	if n == nil {
		return
	}
	_, size := aiCanvasRect()
	aiPan = imgui.Vec2{
		X: size.X/(2*aiZoom) - (n.X + aiNodeW/2),
		Y: size.Y/(2*aiZoom) - (n.Y + aiNodeHeight(n)/2),
	}
}

// ---------- the right panel: the selected node ----------

func aiEditorInspector() {
	w := aiEditorWindow
	if w == nil {
		return
	}
	imgui.SetNextWindowPos(imgui.Vec2{X: float32(w.W) - aiPanelWidth, Y: aiToolbarH})
	imgui.SetNextWindowSize(imgui.Vec2{X: aiPanelWidth, Y: float32(w.H) - aiToolbarH})
	flags := imgui.WindowFlagsNoTitleBar | imgui.WindowFlagsNoResize | imgui.WindowFlagsNoMove |
		imgui.WindowFlagsNoSavedSettings | imgui.WindowFlagsNoBringToFrontOnFocus
	if imgui.BeginV("##aiinspector", nil, flags) {
		aiInspectorBody()
	}
	imgui.End()
}

func aiInspectorBody() {
	t := aiCurrentTree()
	n := aiSelectedNode()
	switch {
	case t == nil:
		editorTextMutedWrapped("No tree open. Add one from the Tree menu.")
		return
	case len(aiSelection) > 1:
		imgui.Text(fmt.Sprintf("%d nodes selected", len(aiSelection)))
		editorTextMutedWrapped("Drag to move them together, Ctrl+D to duplicate, Del to delete. " +
			"Select one on its own to edit it.")
		return
	case n == nil:
		aiInspectorDocument()
		return
	}
	aiNodeForm(t, n)
}

// aiInspectorDocument is what the panel shows with nothing selected: the
// document's own identity, and how to reach it from a level.
func aiInspectorDocument() {
	imgui.Text("Behaviour")
	imgui.Separator()
	imgui.PushItemWidth(-1)
	id := aiDoc.ID
	if aiInputText("Id##aidocid", &id) {
		AIPushUndo()
		aiDoc.ID = id
		aiDoc.Touch()
	}
	desc := aiDoc.Description
	if aiInputTextMulti("Description##aidocdesc", &desc, 70) {
		AIPushUndo()
		aiDoc.Description = desc
		aiDoc.Touch()
	}
	imgui.PopItemWidth()
	imgui.Separator()
	editorTextMuted(aiDescribeDoc(aiDoc))
	if name := aiDocName(); name != "" {
		editorTextMutedWrapped("An entity type carries it with:\n\n    ai_tree: " + name)
		if lvl := Editor.level; lvl != nil {
			if users := script.AITreeUsage(lvl, name); len(users) > 0 {
				editorTextMutedWrapped("In this level: " + strings.Join(users, ", "))
			}
		}
	}
	imgui.Separator()
	editorTextMutedWrapped("Click a node to edit it. Right-click the canvas to add one, " +
		"drag from a node's bottom socket to wire it to the next.")
}

// aiNodeForm is the generated form: one control per field the node's verb
// declares.
func aiNodeForm(t *aitree.Tree, n *aitree.Node) {
	imgui.Text(n.ID)
	imgui.SameLine()
	editorTextMuted("— " + aitree.NodeKindName(n.Kind))
	if t.Root == n.ID {
		imgui.SameLine()
		editorTextColored(imgui.Vec4{X: 0.55, Y: 0.85, Z: 1, W: 1}, "(root)")
	}
	imgui.Separator()

	aiNodeDiagnostics(t, n)

	imgui.PushItemWidth(-1)
	defer imgui.PopItemWidth()

	label := n.Label
	if aiInputText("Label##ainlabel", &label) {
		AIPushUndo()
		n.Label = label
		aiDoc.Touch()
	}
	aiHelp("What the box says on the canvas. Empty uses the verb's own summary.")

	if n.Kind == aitree.NodeNote {
		note := n.Note
		if aiInputTextMulti("Note##ainnote", &note, 120) {
			AIPushUndo()
			n.Note = note
			aiDoc.Touch()
		}
		return
	}

	call := &n.Do
	if n.Kind == aitree.NodeCondition {
		call = &n.Check
	}
	aiVerbCombo(n, call)
	imgui.Separator()

	v := aitree.VerbOf(call.Verb)
	if v == nil {
		editorTextWrappedColored(imgui.Vec4{X: 1, Y: 0.45, Z: 0.4, W: 1},
			"This build has no verb called "+call.Verb+". Pick one above; what is here will be kept until you do.")
		return
	}
	editorTextMutedWrapped(v.Doc)
	imgui.Separator()

	for i := range v.Fields {
		aiFieldControl(t, n, call, &v.Fields[i])
	}
	if v.VarArgs {
		aiArgsControl(n, call, v)
	}

	imgui.Separator()
	aiLinkControls(t, n)
	imgui.Separator()
	aiCompiledPreview(t, n)
}

// aiNodeDiagnostics shows what the compiler said about this node, at the top of
// its form where it cannot be missed.
func aiNodeDiagnostics(t *aitree.Tree, n *aitree.Node) {
	diags := aiProg.DiagsFor(t, n.ID)
	if len(diags) == 0 {
		return
	}
	for _, d := range diags {
		col := imgui.Vec4{X: 1, Y: 0.45, Z: 0.4, W: 1}
		if d.Warning {
			col = imgui.Vec4{X: 0.95, Y: 0.8, Z: 0.35, W: 1}
		}
		msg := d.Msg
		if d.Field != "" {
			msg = d.Field + ": " + d.Msg
		}
		editorTextWrappedColored(col, msg)
	}
	imgui.Separator()
}

// aiVerbCombo is the verb picker, filtered as you type (imguibackend's combo).
func aiVerbCombo(n *aitree.Node, call *aitree.Call) {
	preview := call.Verb
	if v := aitree.VerbOf(call.Verb); v != nil {
		preview = v.Summary + "  (" + v.Name + ")"
	}
	if !imguibackend.BeginCombo("Verb##ainverb", preview) {
		return
	}
	for i, v := range aitree.VerbsFor(n.Kind) {
		_ = i
		if imguibackend.SelectableV(v.Summary+"  ("+v.Name+")", v.Name == call.Verb, 0, imgui.Vec2{}) {
			if v.Name != call.Verb {
				AIPushUndo()
				// The fields are left alone. Two verbs often share `of` and
				// `value`, and throwing them away on every change of mind is
				// how an editor loses work; the compiler names any field the
				// new verb does not have, and the panel's "unused fields"
				// section below is where they are cleared.
				call.Verb = v.Name
				aiDoc.Touch()
			}
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip(v.Doc)
		}
	}
	imgui.EndCombo()
}

// aiFieldControl draws one field. Every field is text holding an expression;
// what changes with the hint is what is offered beside it.
func aiFieldControl(t *aitree.Tree, n *aitree.Node, call *aitree.Call, f *aitree.Field) {
	// A right-hand side that the operator beside it does not take is not drawn:
	// `health is null` has no value, and a box for one would invite filling in.
	if (f.Name == "value" || f.Name == "b") && aiOpTakesNoValue(call) {
		return
	}
	id := "##aif" + n.ID + f.Name
	text := ""
	if call.Fields != nil {
		text = call.Fields[f.Name]
	}

	switch f.Hint {
	case aitree.HintOp:
		aiChoiceField(call, f, id, aitree.Ops)
		return
	case aitree.HintAssignOp:
		aiChoiceField(call, f, id, aitree.AssignOps)
		return
	case aitree.HintBool:
		aiBoolField(call, f, id)
		return
	case aitree.HintStates:
		aiStatesField(call, f, id)
		return
	}

	before := text
	if aiInputText(aiFieldLabel(f)+id, &text) && text != before {
		AIPushUndo()
		call.Set(f.Name, text)
		aiDoc.Touch()
	}
	aiFieldPicker(t, n, call, f, id)
	aiFieldHelp(f)
}

// aiFieldLabel is the field's name with a marker for the ones that may be left
// blank, so a form says which of its boxes have to be filled in.
func aiFieldLabel(f *aitree.Field) string {
	if f.Optional {
		return f.Name + " (optional)"
	}
	return f.Name
}

// aiFieldHelp is the one-line note under a field, and the default it falls back
// to when it is empty — which is the thing an author most wants to know and the
// thing a plain text box cannot say.
func aiFieldHelp(f *aitree.Field) {
	var parts []string
	if f.Doc != "" {
		parts = append(parts, f.Doc)
	}
	if f.Default != "" {
		parts = append(parts, "empty means "+f.Default)
	}
	if f.Quoted {
		parts = append(parts, "a bare name is the name itself; start with = for an expression")
	}
	if len(parts) > 0 {
		editorTextMuted(strings.Join(parts, " · "))
	}
}

// aiFieldPicker is the "..." beside a field that has something to pick from:
// an animation the entity has, a tag the level uses, a function the script
// defines, a helper tree in this document.
func aiFieldPicker(t *aitree.Tree, n *aitree.Node, call *aitree.Call, f *aitree.Field, id string) {
	options := aiFieldOptions(f)
	if len(options) == 0 {
		return
	}
	if !imguibackend.BeginCombo("pick "+f.Name+id+"pick", "") {
		return
	}
	for _, o := range options {
		if imguibackend.SelectableV(o, false, 0, imgui.Vec2{}) {
			AIPushUndo()
			call.Set(f.Name, o)
			aiDoc.Touch()
		}
	}
	imgui.EndCombo()
	_ = t
	_ = n
}

// aiFieldOptions is what a field can be picked from, or nil.
func aiFieldOptions(f *aitree.Field) []string {
	switch f.Hint {
	case aitree.HintTree:
		var out []string
		if aiDoc != nil {
			for _, t := range aiDoc.Trees {
				if t.IsHelper() {
					out = append(out, t.Name)
				}
			}
		}
		return out
	case aitree.HintTag:
		return aiLevelTags()
	case aitree.HintFunc:
		return aiScriptFunctions()
	case aitree.HintProp:
		return aiEntityPropNames()
	case aitree.HintEvent:
		return aiKnownEventNames()
	case aitree.HintAnim:
		return aiAnimationNames()
	}
	return nil
}

// aiChoiceField is a field whose value comes from a fixed list.
func aiChoiceField(call *aitree.Call, f *aitree.Field, id string, options []string) {
	cur := call.Field(f.Name)
	if !imguibackend.BeginCombo(f.Name+id, cur) {
		aiFieldHelp(f)
		return
	}
	for _, o := range options {
		if imguibackend.SelectableV(o, o == cur, 0, imgui.Vec2{}) && o != cur {
			AIPushUndo()
			call.Set(f.Name, o)
			aiDoc.Touch()
		}
	}
	imgui.EndCombo()
	aiFieldHelp(f)
}

// aiBoolField is a checkbox over a field that holds `true` or `false`. It is
// still an expression underneath — an author who needs one types it in the
// text box the checkbox sits beside.
func aiBoolField(call *aitree.Call, f *aitree.Field, id string) {
	text := call.Field(f.Name)
	on := text == "true"
	plain := text == "true" || text == "false" || text == ""
	if plain {
		if imgui.Checkbox(f.Name+id, &on) {
			AIPushUndo()
			if on {
				call.Set(f.Name, "true")
			} else {
				call.Set(f.Name, "false")
			}
			aiDoc.Touch()
		}
		aiFieldHelp(f)
		return
	}
	before := text
	if aiInputText(f.Name+id, &text) && text != before {
		AIPushUndo()
		call.Set(f.Name, text)
		aiDoc.Touch()
	}
	aiFieldHelp(f)
}

// aiStatesField is the controller-state picker: a checkbox per state, written
// back as the comma-separated list the check reads.
func aiStatesField(call *aitree.Call, f *aitree.Field, id string) {
	have := map[string]bool{}
	for _, s := range strings.Split(call.Field(f.Name), ",") {
		if s = strings.TrimSpace(s); s != "" {
			have[s] = true
		}
	}
	imgui.Text(f.Name)
	changed := false
	for i, name := range world.AIModeNames {
		on := have[name]
		if imgui.Checkbox(name+id+fmt.Sprint(i), &on) {
			have[name] = on
			changed = true
		}
		if i%2 == 0 && i+1 < len(world.AIModeNames) {
			imgui.SameLine()
		}
	}
	if changed {
		var out []string
		for _, name := range world.AIModeNames {
			if have[name] {
				out = append(out, name)
			}
		}
		AIPushUndo()
		call.Set(f.Name, strings.Join(out, ", "))
		aiDoc.Touch()
	}
	aiFieldHelp(f)
}

// aiArgsControl edits the positional argument list of the three calling verbs.
func aiArgsControl(n *aitree.Node, call *aitree.Call, v *aitree.Verb) {
	imgui.Separator()
	imgui.Text("Arguments")
	if v.VarArgsDoc != "" {
		editorTextMuted(v.VarArgsDoc)
	}
	remove := -1
	for i := range call.Args {
		text := call.Args[i]
		before := text
		if aiInputText(fmt.Sprintf("%d##aiarg%s%d", i+1, n.ID, i), &text) && text != before {
			AIPushUndo()
			call.Args[i] = text
			aiDoc.Touch()
		}
		imgui.SameLine()
		if imgui.Button(fmt.Sprintf("x##aiargdel%s%d", n.ID, i)) {
			remove = i
		}
	}
	if remove >= 0 {
		AIPushUndo()
		call.Args = append(call.Args[:remove], call.Args[remove+1:]...)
		aiDoc.Touch()
	}
	if imgui.Button("Add Argument##aiargadd" + n.ID) {
		AIPushUndo()
		call.Args = append(call.Args, "")
		aiDoc.Touch()
	}
	// A helper's arity is fixed by its own argument names, and getting it wrong
	// is the commonest mistake here, so the form says what it wants.
	if name := call.Field("tree"); name != "" && aiDoc != nil {
		if h := aiDoc.Helper(name); h != nil {
			editorTextMuted(name + " takes: " + strings.Join(h.Args, ", "))
		}
	}
}

// aiLinkControls edits a node's outgoing links as a dropdown per port, which is
// the way to wire a node to one that is off screen.
func aiLinkControls(t *aitree.Tree, n *aitree.Node) {
	imgui.Text("Goes to")
	for _, l := range n.Links() {
		label := l.To
		if label == "" {
			label = "(ends the run)"
		}
		if !imguibackend.BeginCombo(l.Port+"##ailink"+n.ID+l.Port, label) {
			continue
		}
		if imguibackend.SelectableV("(ends the run)", l.To == "", 0, imgui.Vec2{}) {
			AIPushUndo()
			n.SetLink(l.Port, "")
			aiDoc.Touch()
		}
		for _, other := range t.Nodes {
			if other.ID == n.ID || other.Kind == aitree.NodeNote {
				continue
			}
			if imguibackend.SelectableV(aiNodeMenuLabel(other), other.ID == l.To, 0, imgui.Vec2{}) {
				AIPushUndo()
				n.SetLink(l.Port, other.ID)
				aiDoc.Touch()
			}
		}
		imgui.EndCombo()
	}
	if imgui.Button("Make Root##aimkroot" + n.ID) {
		AIPushUndo()
		t.Root = n.ID
		aiDoc.Touch()
	}
	imgui.SameLine()
	if imgui.Button("Centre##aicentre" + n.ID) {
		aiCentreOn(n.ID)
	}
}

// aiNodeMenuLabel is how a node reads in a list: its id and what it does.
func aiNodeMenuLabel(n *aitree.Node) string {
	call := n.Do
	if n.Kind == aitree.NodeCondition {
		call = n.Check
	}
	what := call.Verb
	if n.Label != "" {
		what = n.Label
	} else if v := aitree.VerbOf(call.Verb); v != nil {
		what = v.Summary
	}
	return n.ID + " — " + what
}

// aiCompiledPreview shows the flang a templated node compiles to.
//
// It is the one thing in this editor that explains the whole design: a node is
// not a mystery box, it is a line of the same language the script beside it is
// written in, and anyone who wants to know exactly what a check means can read
// it here.
func aiCompiledPreview(t *aitree.Tree, n *aitree.Node) {
	if !imgui.CollapsingHeader("As an expression##aipreview") {
		return
	}
	src := aitree.NodeSource(n)
	if src == "" {
		editorTextMutedWrapped("This verb is not a template — the engine performs it directly. " +
			"Its fields are each evaluated on their own.")
		return
	}
	editorTextMutedWrapped(src)
	_ = t
}

// aiOpTakesNoValue reports whether the call's operator is one of the four that
// has no right-hand side.
func aiOpTakesNoValue(call *aitree.Call) bool {
	v := aitree.VerbOf(call.Verb)
	if v == nil {
		return false
	}
	for _, f := range v.Fields {
		if f.Name == "op" && f.Hint == aitree.HintOp {
			return !aitree.OpNeedsValue(call.Field("op"))
		}
	}
	return false
}

// ---------- adding a tree ----------

// aiAddEventDialog asks which message a new root answers, offering the ones the
// engine posts.
func aiAddEventDialog() {
	imguibackend.WithModalPopup("Add Event Root##aiaddevent", &aiShowAddEventMenu, func() {
		imgui.InputTextV("Event##aieventname", &aiNewEventName, 0, nil)
		editorTextMutedWrapped("Without its colons. Pick a common one below, or type any message " +
			"another script sends this entity.")
		imgui.BeginChildV("##aieventlist", imgui.Vec2{X: 380, Y: 260}, true, 0)
		for _, e := range script.AIEventDocs() {
			label := e.Name
			if e.Args != "" {
				label += "  (" + e.Args + ")"
			}
			if imguibackend.Selectable(label) {
				aiNewEventName = e.Name
			}
			if imgui.IsItemHovered() {
				imgui.SetTooltip(e.Doc)
			}
		}
		imgui.EndChild()
		if imgui.Button("Add##aieventgo") && aiNewEventName != "" {
			args := ""
			for _, e := range script.AIEventDocs() {
				if e.Name == aiNewEventName {
					args = e.Args
				}
			}
			t := &aitree.Tree{Event: aiNewEventName}
			for _, a := range strings.Split(args, ",") {
				if a = strings.TrimSpace(a); a != "" {
					t.Args = append(t.Args, a)
				}
			}
			aiAddTree(t)
			aiNewEventName = ""
			imgui.CloseCurrentPopup()
		}
		imgui.SameLine()
		if imgui.Button("Cancel##aieventcancel") {
			imgui.CloseCurrentPopup()
		}
	})
}

// aiAddTree appends a tree and opens it.
func aiAddTree(t *aitree.Tree) {
	if aiDoc == nil {
		return
	}
	AIPushUndo()
	aiDoc.Trees = append(aiDoc.Trees, t)
	aiTreeIndex = len(aiDoc.Trees) - 1
	aiSelection = map[string]bool{}
	aiDoc.Touch()
	aiResetView()
}

// aiDeleteCurrentTree removes the open tree.
func aiDeleteCurrentTree() {
	t := aiCurrentTree()
	if t == nil {
		return
	}
	AIPushUndo()
	aiDoc.Trees = append(aiDoc.Trees[:aiTreeIndex], aiDoc.Trees[aiTreeIndex+1:]...)
	if aiTreeIndex >= len(aiDoc.Trees) {
		aiTreeIndex = len(aiDoc.Trees) - 1
	}
	if aiTreeIndex < 0 {
		aiTreeIndex = 0
	}
	aiSelection = map[string]bool{}
	aiDoc.Touch()
}

// aiFreeHelperName is a helper name the document does not already use.
func aiFreeHelperName() string {
	for i := 1; ; i++ {
		name := fmt.Sprintf("helper_%d", i)
		if aiDoc == nil || aiDoc.Helper(name) == nil {
			return name
		}
	}
}

// ---------- what the pickers offer ----------

// aiLevelTags is every tag in the level being edited beside this window, so a
// `send` to a tag can be picked rather than remembered.
func aiLevelTags() []string {
	lvl := Editor.level
	if lvl == nil {
		return nil
	}
	seen := map[string]bool{}
	for i := range lvl.Entities {
		for _, t := range lvl.Entities[i].Tags {
			seen[t] = true
		}
	}
	for i := range lvl.Volumes {
		for _, t := range lvl.Volumes[i].Tags {
			seen[t] = true
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// aiScriptFunctions is what a `call` node can reach: the functions defined by
// the script of an entity in this level that carries this document.
func aiScriptFunctions() []string {
	lvl := Editor.level
	name := aiDocName()
	if lvl == nil || name == "" {
		return nil
	}
	seen := map[string]bool{}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.AITree != name || e.Script == "" {
			continue
		}
		for _, f := range script.ScriptFunctionNames(e.Script) {
			seen[f] = true
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// aiAnimationNames is every animation name an entity carrying this document
// declares, so `play_anim` offers the clips that actually exist.
func aiAnimationNames() []string {
	lvl := Editor.level
	name := aiDocName()
	if lvl == nil || name == "" {
		return nil
	}
	seen := map[string]bool{}
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.AITree != name {
			continue
		}
		for mi := range e.Models {
			for _, a := range e.Models[mi].Animations {
				seen[a.Name] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// aiEntityPropNames is the fixed set of engine properties an entity has.
func aiEntityPropNames() []string {
	return script.EntityPropertyNames()
}

// aiKnownEventNames is every event the engine posts, for a `send` node's event
// field. A send may name anything — a tree and a script talk to each other with
// messages of their own — so this is a list to pick from, never a restriction.
func aiKnownEventNames() []string {
	docs := script.AIEventDocs()
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.Name)
	}
	return out
}

// aiOneLine flattens a message for a list row.
func aiOneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 90 {
		s = s[:88] + "…"
	}
	return s
}

// ---------- small wrappers ----------

// aiInputText is imgui's text box with the editor's own conventions: returns
// true on a change, and takes the label with its ## id already in it.
func aiInputText(label string, s *string) bool {
	return imgui.InputTextV(label, s, 0, nil)
}

func aiInputTextMulti(label string, s *string, height float32) bool {
	return imgui.InputTextMultilineV(label, s, imgui.Vec2{X: -1, Y: height}, 0, nil)
}

// aiHelp is the muted note under a control.
func aiHelp(s string) { editorTextMutedWrapped(s) }

// ---------- the picker the level editor shows ----------

// editorAITreeUI is the "AI Tree" row on an object's Scripting group: a
// type-to-filter picker over resources/ai/, a button to open the AI editor on
// what is chosen, and a summary of what the behaviour answers.
//
// It lives here rather than in editor_entity_panel.go so that everything that
// knows about .fai documents is in one place — and so that the summary can be
// read out of the compiled document rather than guessed at from the filename.
func editorAITreeUI(field *string, idPrefix string) {
	names := AITreeNamesList()
	preview := *field
	if preview == "" {
		preview = "(none)"
	}
	if imguibackend.BeginCombo("AI Tree##"+idPrefix+"aitree", preview) {
		if imguibackend.SelectableV("(none)", *field == "", 0, imgui.Vec2{}) {
			EditorPushUndo()
			*field = ""
		}
		for _, n := range names {
			if imguibackend.SelectableV(n, n == *field, 0, imgui.Vec2{}) && n != *field {
				EditorPushUndo()
				*field = n
			}
		}
		imgui.EndCombo()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("A decision tree (" + aitree.AIExt + ") deciding what this does about what\n" +
			"happens to it. Drawn in the AI editor. The `ai:` block above is a\n" +
			"different thing: that is how it moves.")
	}

	imgui.SameLine()
	if imgui.Button("Edit##" + idPrefix + "aitreeedit") {
		if *field == "" {
			OpenAIEditorWindow()
		} else {
			OpenAIEditorOn(*field)
		}
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Open it in the AI editor.")
	}
	imgui.SameLine()
	if imgui.Button("Rescan##" + idPrefix + "aitreerescan") {
		Editor.AITreeNames, aiSummaryCache = nil, nil
	}

	if *field != "" {
		editorTextMuted(aiSummaryOf(*field))
	}
}

// aiSummaryOf is the one-line description of a document named by a picker,
// loaded from disk and cached so the row does not re-read a file every frame.
func aiSummaryOf(name string) string {
	if s, ok := aiSummaryCache[name]; ok {
		return s
	}
	if aiSummaryCache == nil {
		aiSummaryCache = map[string]string{}
	}
	d, err := aitree.LoadAI(aitree.AIPath(name))
	if err != nil {
		aiSummaryCache[name] = "could not be read: " + err.Error()
		return aiSummaryCache[name]
	}
	s := aiDescribeDoc(d)
	if len(d.Events()) > 0 {
		s += " — answers :" + strings.Join(d.Events(), ": :") + ":"
	}
	aiSummaryCache[name] = s
	return s
}

// aiSummaryCache is cleared whenever the name list is, which is what a save or
// a Rescan does.
var aiSummaryCache map[string]string
