package aitree

// aitree.go — the decision tree document: what an author draws on the canvas.
//
// An AI decision tree (.fai) is the *deciding* half of an agent's behaviour,
// drawn rather than written. The other two halves stay exactly where they were:
//
//   - MOVING is the controller's (internal/ai). `ai:` on the entity type still
//     says how fast it walks, how far it sees and what it does about a target;
//     nothing here steers a body.
//   - ACTING is the script's (.flang). Casting a round, spawning a grenade with
//     the right arc, reading a bone's position — the things that are arithmetic
//     and want a text editor — stay functions in the entity's script, and a
//     tree reaches them with `call`.
//
// What a tree owns is everything in between: the ifs. Which reaction wins, how
// likely it is, how long before it may happen again, what to remember and what
// to tell the rest of the squad. That was the two thirds of enemy_combat.flang
// nobody could see the shape of, and it is the part that is genuinely a tree.
//
// THE SHAPE. A document holds a list of trees, each one rooted at an entry
// point:
//
//	event: damage    a root fired when `:damage:` is delivered to the entity,
//	                 with the message's arguments bound by the names in `args`
//	name: is_foe     a helper, run by another tree's `run` action or asked as a
//	                 question by a `tree` check. Its `result` action is what it
//	                 answers with.
//
// Inside a tree there are two kinds of node and they alternate the way a
// decision tree's do:
//
//	condition   asks one question and goes down `then` or `else`
//	action      does one thing and goes on to `next`
//
// An empty link is the end of the run: that branch is finished. There is no
// third kind, no sequence node and no fallback node — a run of actions is a
// chain of `next`, and a fallback is an `else`. Keeping it to two means a tree
// reads the way its picture looks, top to bottom, with no node whose meaning
// depends on the order of its children.
//
// THE TEXT IN A NODE. Every field holds one flang expression, parsed by the
// language's own parser and evaluated by its own evaluator against the same
// host (flang/embed.go). So `?grenade_chance`, `@self.prop.ai_target`,
// `distance(target, @self)` and `it.attr.health` all mean in a field exactly
// what they mean in a handler — there is no second little expression language
// to learn, and no second one to keep in step.
//
// WHAT IS NOT IN THE FILE. A node's position is, because the drawing is the
// document. Selection, zoom and which tree is open are not: those are the
// editor's, and two people opening the same file should not disagree about it.

// NodeKind is what a node does when the run reaches it.
type NodeKind int

const (
	// NodeCondition asks a question and branches.
	NodeCondition NodeKind = iota
	// NodeAction does something and carries on.
	NodeAction
	// NodeNote is a comment pinned to the canvas. It is never run and has no
	// links; it is there so a tree can say why.
	NodeNote
)

// NodeKindNames is each kind's name in the file, indexed by the kind.
var NodeKindNames = []string{"condition", "action", "note"}

// NodeKindName returns a kind's name, defaulting out-of-range to condition.
func NodeKindName(k NodeKind) string {
	if int(k) < 0 || int(k) >= len(NodeKindNames) {
		return NodeKindNames[NodeCondition]
	}
	return NodeKindNames[k]
}

// NodeKindFromName is the inverse. An unknown name reads as a note, so a
// document written by a newer build still opens and still draws, with the node
// it did not understand sitting there inert rather than silently becoming a
// condition that answers something.
func NodeKindFromName(name string) NodeKind {
	for i, n := range NodeKindNames {
		if n == name {
			return NodeKind(i)
		}
	}
	return NodeNote
}

// Call is a verb and the fields the author filled in for it.
//
// Fields is keyed by the field names the verb declares (vocab.go); anything in
// it the verb does not declare is reported by Compile rather than dropped, so a
// renamed field is a diagnostic instead of a silent behaviour change. Args is
// the positional list the three calling verbs take.
type Call struct {
	Verb   string
	Fields map[string]string
	Args   []string
}

// Field returns a field's text, or the verb's default for it when the author
// left it blank.
func (c Call) Field(name string) string {
	if c.Fields != nil {
		if v, ok := c.Fields[name]; ok && v != "" {
			return v
		}
	}
	if f := FieldOf(c.Verb, name); f != nil {
		return f.Default
	}
	return ""
}

// Clone deep-copies a call.
func (c Call) Clone() Call {
	out := Call{Verb: c.Verb, Args: append([]string(nil), c.Args...)}
	if c.Fields != nil {
		out.Fields = make(map[string]string, len(c.Fields))
		for k, v := range c.Fields {
			out.Fields[k] = v
		}
	}
	return out
}

// Set writes a field, creating the map on first use and deleting the entry when
// the text is blank — so a field left empty does not appear in the file at all
// and two documents that say the same thing fingerprint the same.
func (c *Call) Set(name, text string) {
	if text == "" {
		delete(c.Fields, name)
		return
	}
	if c.Fields == nil {
		c.Fields = map[string]string{}
	}
	c.Fields[name] = text
}

// Node is one box on the canvas.
type Node struct {
	ID   string
	Kind NodeKind
	// X and Y are where the box sits, in document units. The canvas pans and
	// zooms over these; they are never window pixels.
	X, Y float32
	// Label is the author's own name for this node, drawn on the box in place
	// of the verb's own summary. Empty uses the summary.
	Label string
	// Note is the text of a NodeNote, and a tooltip on the other two.
	Note string

	// Check and its two links are a condition's; Do and Next are an action's.
	// Both are kept whatever the kind is, so flipping a node between them and
	// back does not lose what was typed.
	Check Call
	Then  string
	Else  string

	Do   Call
	Next string
}

// Clone deep-copies a node.
func (n *Node) Clone() *Node {
	c := *n
	c.Check = n.Check.Clone()
	c.Do = n.Do.Clone()
	return &c
}

// Links returns the node ids this one can go to, with the name of each link.
// The order is the order the editor draws the ports in.
func (n *Node) Links() []Link {
	switch n.Kind {
	case NodeCondition:
		return []Link{{"then", n.Then}, {"else", n.Else}}
	case NodeAction:
		return []Link{{"next", n.Next}}
	}
	return nil
}

// Link is one outgoing port: what it is called, and where it goes ("" for an
// end of the run).
type Link struct {
	Port string
	To   string
}

// SetLink points one of a node's ports at a node id.
func (n *Node) SetLink(port, to string) {
	switch port {
	case "then":
		n.Then = to
	case "else":
		n.Else = to
	case "next":
		n.Next = to
	}
}

// LinkTo returns where a port goes.
func (n *Node) LinkTo(port string) string {
	switch port {
	case "then":
		return n.Then
	case "else":
		return n.Else
	case "next":
		return n.Next
	}
	return ""
}

// Tree is one entry point and the nodes reachable from it.
//
// Exactly one of Event and Name is set. The file format is what enforces that
// — a tree entry carries either an `event:` key or a `name:` key — and Compile
// reports one that carries both or neither.
type Tree struct {
	// Event is the atom this root answers, spelled without its colons:
	// `damage` for `:damage:`. The message's arguments are bound to Args.
	Event string
	// Name is a helper tree's name, called by `run` and asked by a `tree`
	// check. Its arguments are bound to Args.
	Name string
	// Args names what gets bound in the tree's environment. For an event root
	// these are the message's arguments after the atom, in order; a root
	// naming fewer than the message carries simply ignores the rest, and one
	// naming more sees null for the ones that are not there.
	Args []string
	Doc  string
	// Root is the id of the node the run starts at. Empty is a tree that does
	// nothing, which is what a new one is until something is drawn on it.
	Root  string
	Nodes []*Node
}

// Title is how the tree reads in a list: `:damage:` or `is_foe()`.
func (t *Tree) Title() string {
	if t.Event != "" {
		return ":" + t.Event + ":"
	}
	if t.Name != "" {
		return t.Name + "()"
	}
	return "(unnamed)"
}

// IsHelper reports whether this tree is called rather than fired.
func (t *Tree) IsHelper() bool { return t.Event == "" && t.Name != "" }

// Node returns the node with an id, or nil.
func (t *Tree) Node(id string) *Node {
	for _, n := range t.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

// IndexOf returns a node's position in the list, or -1.
func (t *Tree) IndexOf(id string) int {
	for i, n := range t.Nodes {
		if n.ID == id {
			return i
		}
	}
	return -1
}

// AddNode appends a node, giving it a free id when it has none, and makes it
// the root when the tree had none.
func (t *Tree) AddNode(n *Node) *Node {
	if n.ID == "" {
		n.ID = t.FreeID("n")
	}
	t.Nodes = append(t.Nodes, n)
	if t.Root == "" && n.Kind != NodeNote {
		t.Root = n.ID
	}
	return n
}

// FreeID returns an id of the form prefix+number that no node has.
func (t *Tree) FreeID(prefix string) string {
	for i := 1; ; i++ {
		id := prefix + itoa(i)
		if t.Node(id) == nil {
			return id
		}
	}
}

// RemoveNode deletes a node and unhooks every link into it, so a delete cannot
// leave a wire pointing at nothing.
func (t *Tree) RemoveNode(id string) {
	i := t.IndexOf(id)
	if i < 0 {
		return
	}
	t.Nodes = append(t.Nodes[:i], t.Nodes[i+1:]...)
	for _, n := range t.Nodes {
		for _, l := range n.Links() {
			if l.To == id {
				n.SetLink(l.Port, "")
			}
		}
	}
	if t.Root == id {
		t.Root = ""
		// The tree still has to start somewhere. The first node that is not a
		// note is as good a guess as any, and it keeps a tree runnable after
		// deleting its head rather than quietly switching it off.
		for _, n := range t.Nodes {
			if n.Kind != NodeNote {
				t.Root = n.ID
				break
			}
		}
	}
}

// Clone deep-copies a tree.
func (t *Tree) Clone() *Tree {
	c := *t
	c.Args = append([]string(nil), t.Args...)
	c.Nodes = make([]*Node, len(t.Nodes))
	for i, n := range t.Nodes {
		c.Nodes[i] = n.Clone()
	}
	return &c
}

// Param is a tunable the document declares, in every way a script's `param` is:
// same types, same defaults, same `?name` spelling in an expression, and set
// per entity type or per placement by the same `params:` block in YAML.
//
// It is declared here rather than left to the script because a tree is often
// the only half an author touches, and a number they cannot reach from the
// canvas is a number they will edit in a text file instead. At load the
// runtime hands these to the entity's script program as though the script had
// declared them, which is what makes `?name` and the YAML override work with no
// second mechanism.
type Param struct {
	Name string
	Type string // int | float | bool | str | vec | ref, as flang spells them
	// Value is the default, as flang source. Empty is no default, which reads
	// as null.
	Value string
	Doc   string
}

// Doc is one .fai file.
type Doc struct {
	ID          string
	Description string
	Params      []Param
	Trees       []*Tree

	// Path is where it was loaded from or last saved to; empty for a document
	// that has never been written.
	Path string

	// Rev counts edits. It is the editor's change signal — the key
	// editor_save_state.go fingerprints against — and is not saved.
	Rev uint64
}

// NewDoc returns an empty document with one tree on it, so a new file opens on
// something to draw rather than on a blank list.
func NewDoc(id string) *Doc {
	d := &Doc{ID: id}
	d.Trees = append(d.Trees, &Tree{Event: "spawn"})
	return d
}

// Tree returns the tree for an event, or nil.
func (d *Doc) Tree(event string) *Tree {
	for _, t := range d.Trees {
		if t.Event == event && event != "" {
			return t
		}
	}
	return nil
}

// Helper returns the helper tree with a name, or nil.
func (d *Doc) Helper(name string) *Tree {
	for _, t := range d.Trees {
		if t.Name == name && name != "" {
			return t
		}
	}
	return nil
}

// Events lists the atoms this document answers, in file order.
func (d *Doc) Events() []string {
	var out []string
	for _, t := range d.Trees {
		if t.Event != "" {
			out = append(out, t.Event)
		}
	}
	return out
}

// Clone deep-copies a document. Undo snapshots one of these.
func (d *Doc) Clone() *Doc {
	c := *d
	c.Params = append([]Param(nil), d.Params...)
	c.Trees = make([]*Tree, len(d.Trees))
	for i, t := range d.Trees {
		c.Trees[i] = t.Clone()
	}
	return &c
}

// Touch marks the document edited.
func (d *Doc) Touch() { d.Rev++ }

// itoa is strconv.Itoa without the import, kept here because this file is the
// only one in the package that needs it and the package is otherwise free of
// formatting.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}
