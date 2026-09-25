package aitree

// compile.go — a drawn document turned into something that can be run, and
// everything wrong with it said out loud.
//
// Compiling happens twice: once in the editor, every time the document changes,
// so the canvas can mark the bad nodes while they are being drawn; and once at
// load, so the runtime has parsed expressions rather than strings. It is the
// same function both times, and it never fails — a document with mistakes in it
// still compiles, with the broken nodes carrying their diagnostics and the rest
// of the tree runnable. That is deliberate: half a behaviour that works while
// you fix the other half is the whole reason for a live editor.
//
// Every expression in the document is parsed exactly once, here. Nothing is
// parsed at run time, and nothing is built out of strings while an agent is
// deciding.

import (
	"fmt"
	"sort"
	"strings"

	"foundry/internal/flang"
)

// Diagnostic is one thing wrong, placed precisely enough for the editor to draw
// a marker on the node it belongs to.
type Diagnostic struct {
	// Tree is the tree's title (`:damage:`, `is_foe()`), Node the node's id and
	// Field the field's name. Any of them may be empty for something that is
	// wrong about the document as a whole.
	Tree, Node, Field string
	Msg               string
	// Warning marks something worth saying that still runs: an unreachable
	// node, a helper nothing calls.
	Warning bool
}

// Error is how a diagnostic reads in a log line.
func (d Diagnostic) Error() string {
	var where []string
	if d.Tree != "" {
		where = append(where, d.Tree)
	}
	if d.Node != "" {
		where = append(where, d.Node)
	}
	if d.Field != "" {
		where = append(where, d.Field)
	}
	if len(where) == 0 {
		return d.Msg
	}
	return strings.Join(where, " ") + ": " + d.Msg
}

// runNode is one compiled node.
type runNode struct {
	Src  *Node
	Verb *Verb
	// Expr is the compiled template, for a verb that has one; nil for a native
	// verb and for a node whose fields would not parse.
	Expr flang.Expr
	// Fields are the individually evaluated fields a native verb reads, by
	// field name, and Args the positional list.
	Fields map[string]flang.Expr
	Args   []flang.Expr
	// Into is the blackboard key an answer is stored under, or "".
	Into string

	Then, Else, Next *runNode
	// Broken nodes end the run rather than being guessed at.
	Broken bool
}

// runTree is one compiled entry point.
type runTree struct {
	Src  *Tree
	Root *runNode
	Args []string
}

// Program is a compiled document.
type Program struct {
	Doc     *Doc
	Events  map[string]*runTree
	Helpers map[string]*runTree
	Diags   []Diagnostic
	// ParamSource is a flang file declaring the document's params, for the host
	// to merge into the entity's script program so that `?name` and the level's
	// `params:` override work with no second mechanism. Empty when the document
	// declares none.
	ParamSource string
}

// Errors returns the diagnostics that are not warnings.
func (p *Program) Errors() []Diagnostic {
	var out []Diagnostic
	for _, d := range p.Diags {
		if !d.Warning {
			out = append(out, d)
		}
	}
	return out
}

// DiagsFor returns everything said about one node.
func (p *Program) DiagsFor(tree *Tree, nodeID string) []Diagnostic {
	if p == nil || tree == nil {
		return nil
	}
	title := tree.Title()
	var out []Diagnostic
	for _, d := range p.Diags {
		if d.Tree == title && d.Node == nodeID {
			out = append(out, d)
		}
	}
	return out
}

// Compile turns a document into a program.
func Compile(d *Doc) *Program {
	p := &Program{Doc: d, Events: map[string]*runTree{}, Helpers: map[string]*runTree{}}
	if d == nil {
		return p
	}
	c := compiler{p: p}
	c.params()

	// Entry points first, so a `run` or a `tree` check can be told whether the
	// helper it names exists before any node is compiled.
	seen := map[string]*Tree{}
	for _, t := range d.Trees {
		switch {
		case t.Event != "" && t.Name != "":
			c.doc(t, "", "", "a tree answers an event or has a name, not both; clear one of them")
			continue
		case t.Event == "" && t.Name == "":
			c.doc(t, "", "", "this tree has neither an event nor a name, so nothing can ever reach it")
			continue
		}
		key := t.Title()
		if prev, dup := seen[key]; dup {
			_ = prev
			c.doc(t, "", "", fmt.Sprintf("%s is declared twice; one document answers an event once", key))
			continue
		}
		seen[key] = t
		rt := &runTree{Src: t, Args: t.Args}
		if t.Event != "" {
			p.Events[t.Event] = rt
		} else {
			p.Helpers[t.Name] = rt
		}
	}

	for _, t := range d.Trees {
		rt := p.Events[t.Event]
		if t.Event == "" {
			rt = p.Helpers[t.Name]
		}
		if rt == nil || rt.Src != t {
			continue
		}
		c.tree(rt)
	}
	sort.SliceStable(p.Diags, func(i, j int) bool {
		if p.Diags[i].Warning != p.Diags[j].Warning {
			return !p.Diags[i].Warning
		}
		return false
	})
	return p
}

type compiler struct {
	p    *Program
	tr   *Tree
	node *Node
}

func (c *compiler) doc(t *Tree, node, field, msg string) {
	title := ""
	if t != nil {
		title = t.Title()
	}
	c.p.Diags = append(c.p.Diags, Diagnostic{Tree: title, Node: node, Field: field, Msg: msg})
}

func (c *compiler) errf(field, format string, a ...any) {
	id := ""
	if c.node != nil {
		id = c.node.ID
	}
	c.doc(c.tr, id, field, fmt.Sprintf(format, a...))
}

func (c *compiler) warnf(field, format string, a ...any) {
	id := ""
	if c.node != nil {
		id = c.node.ID
	}
	title := ""
	if c.tr != nil {
		title = c.tr.Title()
	}
	c.p.Diags = append(c.p.Diags, Diagnostic{
		Tree: title, Node: id, Field: field, Msg: fmt.Sprintf(format, a...), Warning: true,
	})
}

// params builds the flang source for the document's `param` declarations, and
// checks each one is something flang would accept.
func (c *compiler) params() {
	d := c.p.Doc
	if len(d.Params) == 0 {
		return
	}
	var b strings.Builder
	seen := map[string]bool{}
	for _, pm := range d.Params {
		switch {
		case pm.Name == "":
			c.doc(nil, "", "params", "a parameter with no name")
			continue
		case seen[pm.Name]:
			c.doc(nil, "", "params", fmt.Sprintf("parameter %q is declared twice", pm.Name))
			continue
		}
		seen[pm.Name] = true
		if !validParamType(pm.Type) {
			c.doc(nil, "", "params", fmt.Sprintf("parameter %q has type %q; it must be one of %s",
				pm.Name, pm.Type, strings.Join(ParamTypes, ", ")))
			continue
		}
		b.WriteString("param " + pm.Name + " " + pm.Type)
		if pm.Value != "" {
			b.WriteString(" = " + pm.Value)
		}
		b.WriteString("\n")
	}
	src := b.String()
	if src == "" {
		return
	}
	// Parsed here rather than only where it is merged, so a bad default is a
	// mark in the editor's params panel instead of a line in the game's log.
	if _, errs := flang.Parse("params", []byte(src)); len(errs) > 0 {
		for _, e := range errs {
			c.doc(nil, "", "params", e.Msg)
		}
		return
	}
	c.p.ParamSource = src
}

// ParamTypes are the types a declared parameter may have, as flang spells them.
var ParamTypes = []string{"int", "float", "bool", "str", "vec", "ref"}

func validParamType(t string) bool {
	for _, v := range ParamTypes {
		if v == t {
			return true
		}
	}
	return false
}

// tree compiles one entry point: every node, then the links between them.
func (c *compiler) tree(rt *runTree) {
	c.tr = rt.Src
	defer func() { c.tr, c.node = nil, nil }()

	byID := map[string]*runNode{}
	ids := map[string]bool{}
	for _, n := range rt.Src.Nodes {
		if n.ID == "" {
			c.node = n
			c.errf("", "a node with no id; every node needs one, since that is what a link points at")
			continue
		}
		if ids[n.ID] {
			c.node = n
			c.errf("", "two nodes share the id %q; a link to it would be ambiguous", n.ID)
			continue
		}
		ids[n.ID] = true
		if n.Kind == NodeNote {
			continue
		}
		byID[n.ID] = c.compileNode(n)
	}

	// Links, once every node exists.
	for _, n := range rt.Src.Nodes {
		rn := byID[n.ID]
		if rn == nil {
			continue
		}
		c.node = n
		for _, l := range n.Links() {
			if l.To == "" {
				continue
			}
			to := byID[l.To]
			if to == nil {
				c.errf(l.Port, "%s points at %q, which is not a node in this tree", l.Port, l.To)
				continue
			}
			switch l.Port {
			case "then":
				rn.Then = to
			case "else":
				rn.Else = to
			case "next":
				rn.Next = to
			}
		}
		if rn.Verb != nil && rn.Verb.Terminal {
			// A terminal node's link is not wrong, it is simply never taken.
			// Saying so is worth a line: an author who wired something after a
			// `stop` meant it to run.
			for _, l := range n.Links() {
				if l.To != "" {
					c.warnf(l.Port, "%s ends the run, so %s is never taken", rn.Verb.Name, l.Port)
				}
			}
		}
	}

	c.node = nil
	if rt.Src.Root == "" {
		c.errf("", "%s has no root, so nothing runs when it fires", rt.Src.Title())
		return
	}
	rt.Root = byID[rt.Src.Root]
	if rt.Root == nil {
		c.errf("", "the root is %q, which is not a node in this tree", rt.Src.Root)
		return
	}

	// Anything the root cannot reach is drawn but dead. A warning rather than
	// an error: a branch parked off to one side mid-edit is normal.
	live := map[*runNode]bool{}
	var walk func(*runNode)
	walk = func(n *runNode) {
		if n == nil || live[n] {
			return
		}
		live[n] = true
		walk(n.Then)
		walk(n.Else)
		walk(n.Next)
	}
	walk(rt.Root)
	for _, n := range rt.Src.Nodes {
		if rn := byID[n.ID]; rn != nil && !live[rn] {
			c.node = n
			c.warnf("", "nothing links to this node, so it never runs")
		}
	}
	c.node = nil
}

// compileNode parses everything in one node.
func (c *compiler) compileNode(n *Node) *runNode {
	c.node = n
	rn := &runNode{Src: n}

	call := n.Do
	table := "action"
	if n.Kind == NodeCondition {
		call, table = n.Check, "condition"
	}
	if call.Verb == "" {
		c.errf("", "this %s node has no verb; pick one", table)
		rn.Broken = true
		return rn
	}
	if n.Kind == NodeCondition {
		rn.Verb = CheckVerb(call.Verb)
	} else {
		rn.Verb = ActionVerb(call.Verb)
	}
	if rn.Verb == nil {
		c.errf("", "%q is not a %s; %s", call.Verb, table, didYouMean(call.Verb, n.Kind))
		rn.Broken = true
		return rn
	}

	// Fields the verb does not declare are reported rather than ignored: a
	// document written against an older table would otherwise lose behaviour
	// without saying so.
	for name := range call.Fields {
		if FieldOf(call.Verb, name) == nil {
			c.errf(name, "%s has no field called %q", call.Verb, name)
		}
	}
	for _, f := range rn.Verb.Fields {
		if f.Optional || f.Default != "" || !needed(rn.Verb, call, f.Name) {
			continue
		}
		if call.Field(f.Name) == "" {
			c.errf(f.Name, "%s needs %s", call.Verb, f.Name)
			rn.Broken = true
		}
	}
	if !rn.Verb.VarArgs && len(call.Args) > 0 {
		c.errf("args", "%s takes no argument list", call.Verb)
	}
	rn.Into = call.Field("into")

	// A templated verb is one expression; a native one is its fields, each on
	// its own.
	if src := verbSource(n.Kind, call); src != "" {
		rn.Expr = c.parse("", src)
		if rn.Expr == nil {
			rn.Broken = true
		}
	} else if rn.Verb.Native == "" {
		c.errf("", "%q has neither a template nor an implementation; this is an engine bug", call.Verb)
		rn.Broken = true
	} else {
		rn.Fields = map[string]flang.Expr{}
		for _, f := range rn.Verb.Fields {
			if !evaluated(rn.Verb.Native, f.Name) {
				continue
			}
			text := call.Source(f.Name)
			if text == "" {
				continue
			}
			if e := c.parse(f.Name, text); e != nil {
				rn.Fields[f.Name] = e
			} else {
				rn.Broken = true
			}
		}
		c.checkNative(rn, call)
	}

	for i, a := range call.Args {
		e := c.parse(fmt.Sprintf("args[%d]", i), a)
		if e == nil {
			rn.Broken = true
		}
		rn.Args = append(rn.Args, e)
	}
	return rn
}

// checkNative is the handful of extra things only a native verb can be wrong
// about: a name that has to exist, a field that only means something beside
// another one.
func (c *compiler) checkNative(rn *runNode, call Call) {
	switch rn.Verb.Native {
	case "send":
		if call.Field("event") == "" {
			c.errf("event", "send needs an event to send")
			rn.Broken = true
		}
		if call.Field("within") != "" && call.Field("tag") == "" {
			c.errf("within", "within only means something with a tag: it keeps a fan-out to what is near enough")
		}
	case "call":
		if call.Field("function") == "" {
			c.errf("function", "call needs the name of a function in the entity's script")
			rn.Broken = true
		}
	case "run", "tree":
		name := call.Field("tree")
		if name == "" {
			c.errf("tree", "%s needs the name of a helper tree", rn.Verb.Name)
			rn.Broken = true
			return
		}
		h := c.p.Helpers[name]
		if h == nil {
			c.errf("tree", "this document has no helper tree called %q", name)
			rn.Broken = true
			return
		}
		if h.Src == c.tr {
			c.errf("tree", "a tree cannot run itself; use a send with an after to come back round")
			rn.Broken = true
			return
		}
		if len(call.Args) != len(h.Args) {
			c.errf("args", "%s takes %d argument(s) (%s), and %d were given",
				name, len(h.Args), strings.Join(h.Args, ", "), len(call.Args))
		}
	case "set", "set_attr", "set_prop":
		key := "var"
		if rn.Verb.Native == "set_attr" {
			key = "attr"
		} else if rn.Verb.Native == "set_prop" {
			key = "prop"
		}
		if call.Field(key) == "" {
			c.errf(key, "%s needs a %s to write", rn.Verb.Name, key)
			rn.Broken = true
		}
	}
}

// needed reports whether a field has to be filled in for this particular call.
// The only thing that makes one optional is the operator beside it: `is true`,
// `is false`, `is null` and `is not null` are questions about one value, so the
// right-hand side is not merely blank, it is not there.
func needed(v *Verb, c Call, field string) bool {
	if field != "value" && field != "b" {
		return true
	}
	for _, f := range v.Fields {
		if f.Name == "op" && f.Hint == HintOp {
			return OpNeedsValue(c.Field("op"))
		}
	}
	return true
}

// evaluated reports whether a native verb's field is an expression to compile
// or a bare name to read straight out of the call. The bare ones are the names
// of things — a blackboard key, an event, a tag, a function — which are written
// as themselves rather than quoted.
func evaluated(native, field string) bool {
	switch field {
	case "var", "attr", "prop", "op", "into", "event", "function", "tree", "states":
		return false
	}
	return true
}

// verbSource is the flang source a node compiles to, or "" for a native verb.
func verbSource(k NodeKind, c Call) string {
	if k == NodeCondition {
		return checkSource(c)
	}
	return actionSource(c)
}

// parse compiles one field, filing any diagnostic against it.
func (c *compiler) parse(field, src string) flang.Expr {
	id := "expression"
	if c.node != nil {
		id = c.node.ID
	}
	e, errs := flang.ParseExpr(id, []byte(src))
	for _, err := range errs {
		c.errf(field, "%s", err.Msg)
	}
	if len(errs) > 0 {
		return nil
	}
	if e == nil {
		c.errf(field, "this is empty, and something has to go here")
		return nil
	}
	return e
}

// didYouMean names the nearest verb in the right table, for a typo.
func didYouMean(name string, k NodeKind) string {
	best, bestD := "", 1<<30
	for _, v := range VerbsFor(k) {
		if d := editDistance(name, v.Name); d < bestD {
			best, bestD = v.Name, d
		}
	}
	if best != "" && bestD <= 3 {
		return "did you mean " + best + "?"
	}
	var names []string
	for _, v := range VerbsFor(k) {
		names = append(names, v.Name)
	}
	return "the ones there are: " + strings.Join(names, ", ")
}

// editDistance is Levenshtein, for the suggestion above and nothing else.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
