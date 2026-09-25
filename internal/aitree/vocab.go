package aitree

// vocab.go — the verbs a node can be, and the fields each one asks for.
//
// This table is the whole difference between a decision tree and a script laid
// out in boxes. A node is not "some flang"; it is `distance`, with a From, a
// To, an operator and a value, each of which the editor can draw as its own
// labelled control, check, and complete. The author picks a verb from a list
// and fills in a form.
//
// Underneath, nearly every verb is a *template*: its fields are substituted
// into a pattern and the result is parsed by flang's own parser, once, at load.
// So `distance` is, exactly and only, `distance((from), (to)) <= (value)`, and
// there is no second implementation of it to drift from the language's. Every
// substitution is parenthesised, so a field holding `a + b` cannot change the
// shape of the expression it lands in.
//
// The handful that are not templates are the ones a pattern cannot express: the
// writes (`set`, `set_attr`, `set_prop`), the ones that fan out (`send` to a
// tag), and the ones that leave this document (`call`, `run`, `result`).
// Those are the native ops in eval.go, and the Native flag marks them.
//
// ADDING A VERB. Put it in this table, give it a Template or a Native op in
// eval.go, and that is the whole job: the editor's verb list, its form, its
// completion and its validation are all read from here.

import (
	"strconv"
	"strings"
)

// FieldHint tells the editor what kind of control to draw and what to complete
// against. It never changes what a field means — every field is still one flang
// expression, or one bare name — only how it is picked.
type FieldHint string

const (
	// HintExpr is the plain case: a flang expression, with the script editor's
	// completion behind it.
	HintExpr FieldHint = ""
	// HintName is a bare name typed straight into the template rather than
	// evaluated: a blackboard key, an attribute, a property.
	HintName FieldHint = "name"
	// The rest are HintName or HintExpr with something to pick from.
	HintVar      FieldHint = "var"      // a blackboard key
	HintAttr     FieldHint = "attr"     // an attribute name
	HintProp     FieldHint = "prop"     // an engine property name
	HintEvent    FieldHint = "event"    // an atom, without its colons
	HintTag      FieldHint = "tag"      // a tag
	HintFunc     FieldHint = "function" // a function in the entity's script
	HintTree     FieldHint = "tree"     // a helper tree in this document
	HintAnim     FieldHint = "anim"     // an animation name
	HintEffect   FieldHint = "effect"   // a particle effect
	HintSound    FieldHint = "sound"    // a sound
	HintOp       FieldHint = "op"       // a comparison
	HintAssignOp FieldHint = "assign"   // =, +=, -=, *=, /=
	HintStates   FieldHint = "states"   // one or more controller states
	HintBool     FieldHint = "bool"     // true/false, drawn as a checkbox
	HintRef      FieldHint = "ref"      // an object, drawn with an object picker
)

// Field is one control on a node's form.
type Field struct {
	Name string
	Hint FieldHint
	Doc  string
	// Default is what the field means when it is left blank. It is substituted
	// into the template exactly as a typed value would be, so a default is a
	// real answer rather than a hint — `of` defaulting to `@self` is why almost
	// no node in a real tree has to say `@self` at all.
	Default string
	// Optional fields left blank are dropped rather than defaulted: the verb
	// does something different without them (a `send` with no `after` is
	// immediate; a `call` with no `into` throws its answer away).
	Optional bool
	// Quoted marks a field that usually holds the *name* of something — an
	// animation, an effect, a tag — rather than an expression that works one
	// out. A bare word in one is the name itself, so `anim: aim` means what it
	// looks like instead of `anim: '"aim"'`; a leading `=` escapes back into an
	// expression, so `anim: =resume_anim()` calls the script and `tag:
	// =?ally_tag` reads a parameter. That escape is why these are not simply
	// strings: which animation to play is a decision often enough that it has
	// to be sayable.
	Quoted bool
}

// Verb is one entry on the condition or action menu.
type Verb struct {
	Name    string
	Summary string // one line, drawn on the node when it has no label
	Doc     string // the tooltip and the reference entry
	Fields  []Field
	// VarArgs says the verb takes the positional `args:` list as well.
	VarArgs    bool
	VarArgsDoc string

	// Template is the flang source the verb compiles to, with `{field}` where
	// each field's text goes. Empty means the verb is native; see eval.go.
	Template string
	// Native names the op eval.go performs. Empty means Template.
	Native string
	// Terminal says the run ends here whatever the links say — `stop` and
	// `result`.
	Terminal bool
}

// Source is a field as flang source: a Quoted field's bare name becomes a
// string literal, an `=` escape drops the `=`, and everything else is the text
// exactly as it was typed.
func (c Call) Source(name string) string {
	text := c.Field(name)
	f := FieldOf(c.Verb, name)
	if f == nil || !f.Quoted || text == "" {
		return text
	}
	if text[0] == '=' {
		return text[1:]
	}
	return strconv.Quote(text)
}

// FieldOf returns a verb's declaration of one field, or nil. Checks first, then
// actions: no name is in both tables.
func FieldOf(verb, field string) *Field {
	v := VerbOf(verb)
	if v == nil {
		return nil
	}
	for i := range v.Fields {
		if v.Fields[i].Name == field {
			return &v.Fields[i]
		}
	}
	return nil
}

// VerbOf returns a verb by name from either table, or nil.
func VerbOf(name string) *Verb {
	if v := CheckVerb(name); v != nil {
		return v
	}
	return ActionVerb(name)
}

// CheckVerb returns a condition verb by name, or nil.
func CheckVerb(name string) *Verb {
	for i := range Checks {
		if Checks[i].Name == name {
			return &Checks[i]
		}
	}
	return nil
}

// ActionVerb returns an action verb by name, or nil.
func ActionVerb(name string) *Verb {
	for i := range Actions {
		if Actions[i].Name == name {
			return &Actions[i]
		}
	}
	return nil
}

// VerbsFor returns the table a node kind picks from.
func VerbsFor(k NodeKind) []Verb {
	if k == NodeAction {
		return Actions
	}
	return Checks
}

// Ops are the comparisons a check's `op` field may hold, in menu order. The
// four at the end take no right-hand side: they are the questions about a value
// that has no comparison in it, and writing them as operators keeps every check
// one shape instead of doubling the table.
var Ops = []string{"==", "!=", "<", "<=", ">", ">=", "is true", "is false", "is null", "is not null"}

// OpNeedsValue reports whether an operator has a right-hand side.
func OpNeedsValue(op string) bool {
	switch op {
	case "is true", "is false", "is null", "is not null":
		return false
	}
	return true
}

// AssignOps are what `set`, `set_attr` and `set_prop` may do to what is there.
var AssignOps = []string{"=", "+=", "-=", "*=", "/="}

// compare builds the flang source for one comparison. Both sides are
// parenthesised; an operator with no right-hand side ignores rhs.
//
// `is false` is `!= true` rather than `not (...)` on purpose: a blackboard key
// that was never written reads as null, and "not yet set" must count as false
// rather than as an error or as a third answer. The same reading makes
// `is true` exact — only a real true is true, not a non-empty string.
func compare(lhs, op, rhs string) string {
	switch op {
	case "is true":
		return "(" + lhs + ") == true"
	case "is false":
		return "(" + lhs + ") != true"
	case "is null":
		return "(" + lhs + ") == null"
	case "is not null":
		return "(" + lhs + ") != null"
	case "":
		op = "=="
	}
	return "(" + lhs + ") " + op + " (" + rhs + ")"
}

// ---------- conditions ----------

// Checks is the condition menu, in the order the editor lists it: the few that
// answer the questions an agent actually asks, then the general comparisons,
// then the escape hatch.
var Checks = []Verb{
	{
		Name:    "has_target",
		Summary: "has a target",
		Doc: "True while the controller is holding a target — something on the\n" +
			"entity's `ai.targets` it has noticed, or one a script or a tree\n" +
			"gave it with ai_target. This is the question nearly every combat\n" +
			"tree starts with.",
		Fields:   []Field{{Name: "of", Doc: "whose target", Default: "@self", Hint: HintRef}},
		Template: "({of}).prop.ai_target != null",
	},
	{
		Name:    "distance",
		Summary: "distance",
		Doc: "How far apart two things are, compared with a number. Either side\n" +
			"may be an object or a point, so `to` left at its default asks the\n" +
			"distance to whatever is being fought.",
		Fields: []Field{
			{Name: "from", Doc: "measured from", Default: "@self", Hint: HintRef},
			{Name: "to", Doc: "measured to", Default: "@self.prop.ai_target", Hint: HintRef},
			{Name: "op", Hint: HintOp, Default: "<="},
			{Name: "value", Doc: "metres"},
		},
		Template: "", // built by checkSource; the operator moves
	},
	{
		Name:    "chance",
		Summary: "chance",
		Doc: "Rolls once, here, now. `then` is taken with this probability, from\n" +
			"0 (never) to 1 (always). It uses the script runtime's own random\n" +
			"stream, so a seeded test plays out the same way twice.",
		Fields:   []Field{{Name: "chance", Doc: "0 to 1", Default: "0.5"}},
		Template: "random() < ({chance})",
	},
	{
		Name:    "cooldown",
		Summary: "cooldown ready",
		Doc: "True when at least `seconds` have passed since the blackboard key\n" +
			"was last stamped with the time — and true when it has never been\n" +
			"stamped, so the first attempt is never blocked. Stamp it with a\n" +
			"`set` action whose value is `time()`.",
		Fields: []Field{
			{Name: "of", Default: "@self", Hint: HintRef},
			{Name: "var", Hint: HintVar, Doc: "the key holding the last time"},
			{Name: "seconds", Doc: "how long it has to have been", Default: "1.0"},
		},
		Template: "({of}).state[\"{var}\"] == null or time() - ({of}).state[\"{var}\"] >= ({seconds})",
	},
	{
		Name:    "ai_state",
		Summary: "controller is",
		Doc: "What the movement controller is doing this tick: idle, moving,\n" +
			"chasing, maneuvering, flanking, dodging, in_cover and the rest. True\n" +
			"when it is any one of the states ticked. This is how a tree keeps\n" +
			"out of the controller's way — nobody crouches mid-stride.",
		Fields: []Field{
			{Name: "of", Default: "@self", Hint: HintRef},
			{Name: "states", Hint: HintStates, Doc: "any one of these"},
		},
		Template: "", // built by checkSource; one clause per state
	},
	{
		Name:    "tagged",
		Summary: "carries tag",
		Doc:     "Whether an object is one of the things carrying a tag — the usual way to ask which side something is on.",
		Fields: []Field{
			{Name: "who", Default: "@self", Hint: HintRef},
			{Name: "tag", Hint: HintTag, Quoted: true},
		},
		Template: "contains(find({tag}), ({who}))",
	},
	{
		Name:    "var",
		Summary: "blackboard",
		Doc: "A key on the agent's own scratch state — what the tree remembers\n" +
			"between events. The same dict a script reads as @self.state[\"...\"];\n" +
			"a key never written reads as null, so `is false` is true for a flag\n" +
			"nothing has set yet.",
		Fields: []Field{
			{Name: "of", Default: "@self", Hint: HintRef},
			{Name: "var", Hint: HintVar},
			{Name: "op", Hint: HintOp, Default: "is true"},
			{Name: "value"},
		},
	},
	{
		Name:    "attr",
		Summary: "attribute",
		Doc:     "A user-defined attribute — the entity type's `attributes:` block. Health, above all.",
		Fields: []Field{
			{Name: "of", Default: "@self", Hint: HintRef},
			{Name: "attr", Hint: HintAttr},
			{Name: "op", Hint: HintOp, Default: "<="},
			{Name: "value", Default: "0"},
		},
	},
	{
		Name:    "prop",
		Summary: "property",
		Doc:     "An engine property — position, velocity, ai_enabled, ai_state, visible, and the rest of the fixed set.",
		Fields: []Field{
			{Name: "of", Default: "@self", Hint: HintRef},
			{Name: "prop", Hint: HintProp},
			{Name: "op", Hint: HintOp, Default: "=="},
			{Name: "value"},
		},
	},
	{
		Name:    "value",
		Summary: "compare",
		Doc:     "Two expressions, compared. The general form the four above are shorthands for.",
		Fields: []Field{
			{Name: "a"},
			{Name: "op", Hint: HintOp, Default: "=="},
			{Name: "b"},
		},
	},
	{
		Name:    "tree",
		Summary: "ask",
		Doc: "Runs a helper tree in this document and branches on what it\n" +
			"answers. A helper is how a question with a shape of its own — is\n" +
			"this a foe? is it worth a grenade? — gets drawn once and asked from\n" +
			"everywhere.",
		Fields:     []Field{{Name: "tree", Hint: HintTree}},
		VarArgs:    true,
		VarArgsDoc: "bound to the helper's own argument names, in order",
		Native:     "tree",
	},
	{
		Name:    "expr",
		Summary: "expression",
		Doc: "One flang expression, true when it is truthy. The escape hatch: if\n" +
			"a question does not fit any node above, it fits here, and nothing is\n" +
			"lost by using it — the other checks are all this one with a form in\n" +
			"front.",
		Fields:   []Field{{Name: "test", Default: "true"}},
		Template: "({test})",
	},
}

// ---------- actions ----------

// Actions is the action menu.
var Actions = []Verb{
	{
		Name:    "set",
		Summary: "remember",
		Doc: "Writes a key on the agent's blackboard — the scratch state a script\n" +
			"reads as @self.state[\"...\"]. This is the tree's memory: flags, the\n" +
			"time something last happened, a spot it picked, a counter.",
		Fields: []Field{
			{Name: "of", Default: "@self", Hint: HintRef},
			{Name: "var", Hint: HintVar},
			{Name: "op", Hint: HintAssignOp, Default: "="},
			{Name: "value", Default: "true"},
		},
		Native: "set",
	},
	{
		Name:    "set_attr",
		Summary: "set attribute",
		Doc:     "Writes a user-defined attribute — taking health off something, above all.",
		Fields: []Field{
			{Name: "of", Default: "@self", Hint: HintRef},
			{Name: "attr", Hint: HintAttr},
			{Name: "op", Hint: HintAssignOp, Default: "="},
			{Name: "value"},
		},
		Native: "set_attr",
	},
	{
		Name:    "set_prop",
		Summary: "set property",
		Doc:     "Writes an engine property: ai_enabled, hit_height, visible, velocity, and the rest.",
		Fields: []Field{
			{Name: "of", Default: "@self", Hint: HintRef},
			{Name: "prop", Hint: HintProp},
			{Name: "value"},
		},
		Native: "set_prop",
	},
	{
		Name:     "play_anim",
		Summary:  "play animation",
		Doc:      "Plays a clip by the name the entity type gave it. A one-shot holds its last frame when it ends and posts :anim_end:.",
		Fields:   []Field{{Name: "of", Default: "@self", Hint: HintRef}, {Name: "anim", Hint: HintAnim, Quoted: true}},
		Template: "play_anim({of}, {anim})",
	},
	{
		Name:     "stop_anim",
		Summary:  "stop animation",
		Doc:      "Stops a clip that is playing.",
		Fields:   []Field{{Name: "of", Default: "@self", Hint: HintRef}, {Name: "anim", Hint: HintAnim, Quoted: true}},
		Template: "stop_anim({of}, {anim})",
	},
	{
		Name:     "spawn_effect",
		Summary:  "spawn effect",
		Doc:      "Fires a particle effect (.fpar) once at a point.",
		Fields:   []Field{{Name: "effect", Hint: HintEffect, Quoted: true}, {Name: "at", Default: "@self.prop.position"}},
		Template: "spawn_effect({effect}, {at})",
	},
	{
		Name:     "play_sound",
		Summary:  "play sound",
		Doc:      "Plays a sound at a point in the world.",
		Fields:   []Field{{Name: "sound", Hint: HintSound, Quoted: true}, {Name: "at", Default: "@self.prop.position"}, {Name: "volume", Optional: true}},
		Template: "", // built by actionSource; volume is optional
	},
	{
		Name:    "ai_target",
		Summary: "set target",
		Doc: "Gives the controller a target, whether or not it can see one. A\n" +
			"target set this way is kept until it goes down, which is what makes\n" +
			"being shot from behind turn an agent round for good rather than for\n" +
			"lose_time.",
		Fields:   []Field{{Name: "of", Default: "@self", Hint: HintRef}, {Name: "target", Hint: HintRef}},
		Template: "ai_target({of}, {target})",
	},
	{
		Name:    "ai_move_to",
		Summary: "go to",
		Doc: "Sends the agent to a point, outranking everything else it was\n" +
			"doing. It posts :ai_arrived: when it gets there and\n" +
			":ai_path_failed: when it cannot, and both are worth a root of their\n" +
			"own. `run` sends it at its run speed, playing its run clip.",
		Fields: []Field{
			{Name: "of", Default: "@self", Hint: HintRef},
			{Name: "to"},
			{Name: "run", Hint: HintBool, Default: "false"},
		},
		Template: "ai_move_to({of}, {to}, {run})",
	},
	{
		Name:     "ai_stop",
		Summary:  "stop going",
		Doc:      "Cancels a move the tree or a script ordered. The agent goes back to whatever it would be doing anyway.",
		Fields:   []Field{{Name: "of", Default: "@self", Hint: HintRef}},
		Template: "ai_stop({of})",
	},
	{
		Name:    "ai_dodge",
		Summary: "dive aside",
		Doc: "Throws the body out of the way of a point — a grenade at its feet.\n" +
			"`away` dives directly away rather than sideways. It answers whether\n" +
			"there was anywhere to dive to, which `into` catches so the tree can\n" +
			"do something else when there was not.",
		Fields: []Field{
			{Name: "of", Default: "@self", Hint: HintRef},
			{Name: "from"},
			{Name: "away", Hint: HintBool, Default: "true"},
			{Name: "into", Hint: HintVar, Optional: true, Doc: "blackboard key for the answer"},
		},
		Template: "ai_dodge({of}, {from}, {away})",
	},
	{
		Name:    "find_cover",
		Summary: "find cover",
		Doc: "Looks for somewhere within `radius` hidden from a threat, and puts\n" +
			"the spot on the blackboard — or null when there is nowhere. `opts` is\n" +
			"the same dict the script verb takes: from, duck, peek, near.",
		Fields: []Field{
			{Name: "of", Default: "@self", Hint: HintRef},
			{Name: "radius", Default: "12.0"},
			{Name: "opts", Optional: true},
			{Name: "into", Hint: HintVar, Doc: "blackboard key for the spot"},
		},
		Template: "", // built by actionSource; opts is optional
	},
	{
		Name:    "send",
		Summary: "send event",
		Doc: "Posts a message. To one object, or — with a tag — to everything\n" +
			"carrying it, which is how one agent's sighting wakes a squad. `within`\n" +
			"keeps a fan-out to what is near enough to have noticed, and `where`\n" +
			"filters it with `it` bound to each candidate. `after` delays it by\n" +
			"that many milliseconds of play time, which is how a tree schedules\n" +
			"itself: a chain that sends itself its own event is a loop with a\n" +
			"clock on it.",
		Fields: []Field{
			{Name: "to", Default: "@self", Hint: HintRef},
			{Name: "tag", Hint: HintTag, Optional: true, Quoted: true, Doc: "send to everything carrying this instead"},
			{Name: "event", Hint: HintEvent},
			{Name: "after", Optional: true, Doc: "milliseconds"},
			{Name: "within", Optional: true, Doc: "metres, with a tag"},
			{Name: "where", Optional: true, Doc: "a test, with `it` bound to each"},
		},
		VarArgs:    true,
		VarArgsDoc: "the message's arguments",
		Native:     "send",
	},
	{
		Name:    "call",
		Summary: "call script",
		Doc: "Calls a function in the entity's own .flang script. This is the door\n" +
			"to the half of a behaviour that is arithmetic rather than decision:\n" +
			"casting a round, solving a throw's arc, reading a bone. `into` puts\n" +
			"what it answers on the blackboard.",
		Fields: []Field{
			{Name: "function", Hint: HintFunc},
			{Name: "into", Hint: HintVar, Optional: true},
		},
		VarArgs:    true,
		VarArgsDoc: "the function's arguments",
		Native:     "call",
	},
	{
		Name:       "run",
		Summary:    "run tree",
		Doc:        "Runs a helper tree in this document, and carries on. `into` catches what it answers.",
		Fields:     []Field{{Name: "tree", Hint: HintTree}, {Name: "into", Hint: HintVar, Optional: true}},
		VarArgs:    true,
		VarArgsDoc: "bound to the helper's argument names, in order",
		Native:     "run",
	},
	{
		Name:     "result",
		Summary:  "answer",
		Doc:      "What a helper tree answers with. The run ends here.",
		Fields:   []Field{{Name: "value", Default: "true"}},
		Native:   "result",
		Terminal: true,
	},
	{
		Name:     "stop",
		Summary:  "stop",
		Doc:      "Ends the run. The same as leaving a link empty, drawn so a branch can say it means to do nothing.",
		Native:   "stop",
		Terminal: true,
	},
	{
		Name:    "exec",
		Summary: "expression",
		Doc: "Evaluates one flang expression for its effect — any engine verb,\n" +
			"spelled the way a script spells it. The escape hatch, and the way to\n" +
			"reach a verb this table has no node for.",
		Fields:   []Field{{Name: "do"}},
		Template: "{do}",
	},
	{
		Name:     "log",
		Summary:  "log",
		Doc:      "Writes a line to the log window. For finding out why a branch was or was not taken.",
		Fields:   []Field{{Name: "message", Default: "\"\""}},
		Template: "log({message})",
	},
}

// ---------- templating ----------

// fill substitutes a call's fields into a template. Every substitution is
// wrapped in the template itself, not here, so a pattern can put a field
// somewhere parentheses would be wrong — inside a string literal, as a bare
// name between quotes.
func fill(tmpl string, get func(string) string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(tmpl, '{')
		if i < 0 {
			b.WriteString(tmpl)
			return b.String()
		}
		j := strings.IndexByte(tmpl[i:], '}')
		if j < 0 {
			b.WriteString(tmpl)
			return b.String()
		}
		b.WriteString(tmpl[:i])
		b.WriteString(get(tmpl[i+1 : i+j]))
		tmpl = tmpl[i+j+1:]
	}
}

// checkSource is the flang source one condition compiles to, or "" for a native
// one.
func checkSource(c Call) string {
	v := CheckVerb(c.Verb)
	if v == nil || v.Native != "" {
		return ""
	}
	f := func(name string) string { return c.Source(name) }
	switch c.Verb {
	case "distance":
		return compare("distance("+c.Field("from")+", "+c.Field("to")+")", c.Field("op"), c.Field("value"))
	case "ai_state":
		states := splitList(c.Field("states"))
		if len(states) == 0 {
			return "false"
		}
		var parts []string
		for _, s := range states {
			parts = append(parts, "("+c.Field("of")+").prop.ai_state == :"+s+":")
		}
		return strings.Join(parts, " or ")
	case "var":
		return compare("("+c.Field("of")+").state[\""+c.Field("var")+"\"]", c.Field("op"), c.Field("value"))
	case "attr":
		return compare("("+c.Field("of")+").attr."+c.Field("attr"), c.Field("op"), c.Field("value"))
	case "prop":
		return compare("("+c.Field("of")+").prop."+c.Field("prop"), c.Field("op"), c.Field("value"))
	case "value":
		return compare(c.Field("a"), c.Field("op"), c.Field("b"))
	}
	return fill(v.Template, f)
}

// actionSource is the flang source one action compiles to, or "" for a native
// one.
func actionSource(c Call) string {
	v := ActionVerb(c.Verb)
	if v == nil || v.Native != "" {
		return ""
	}
	if c.Verb == "find_cover" {
		if opts := c.Field("opts"); opts != "" {
			return "find_cover(" + c.Field("of") + ", " + c.Field("radius") + ", " + opts + ")"
		}
		return "find_cover(" + c.Field("of") + ", " + c.Field("radius") + ")"
	}
	if c.Verb == "play_sound" {
		if vol := c.Field("volume"); vol != "" {
			return "play_sound_at(" + c.Source("sound") + ", " + c.Field("at") + ", " + vol + ")"
		}
		return "play_sound_at(" + c.Source("sound") + ", " + c.Field("at") + ")"
	}
	return fill(v.Template, func(name string) string { return c.Source(name) })
}

// splitList reads a comma-separated field — the only field kind that holds more
// than one thing — trimming the spaces an author leaves after the commas.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// NodeSource is the flang one node compiles to, or "" for a native verb.
//
// Exported for the editor's "As an expression" panel, which is the one place
// this design explains itself: a node is not a mystery box, it is a line of the
// same language the script beside it is written in.
func NodeSource(n *Node) string {
	if n == nil || n.Kind == NodeNote {
		return ""
	}
	if n.Kind == NodeCondition {
		return checkSource(n.Check)
	}
	return actionSource(n.Do)
}
