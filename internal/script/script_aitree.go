package script

// script_aitree.go — decision trees, loaded and delivered to beside the
// scripts.
//
// A tree (internal/aitree) is not a second scripting system bolted on. It is a
// second *front end* onto the one that is already here: it decides through the
// same host, remembers on the same scratch state, sends through the same queue,
// reads the same `?param`, and rolls on the same random stream. An entity may
// carry a tree, a script, or both, and when it carries both they are one
// behaviour with two halves.
//
// THREE THINGS MAKE THAT TRUE, and each is a decision worth naming.
//
// **The tree runs on the entity's own interpreter.** Not one of its own. That
// is what makes `call` reach the entity's functions with no marshalling, makes
// `?grenade_chance` in a node mean what it means in the script beside it, and
// makes a seeded run reproducible across both halves. An entity with a tree and
// no script gets a bare interpreter holding nothing but the tree's parameters,
// so the uncommon case costs one empty program rather than a special path.
//
// **The tree's parameters are merged into the script's program.** A `params:`
// block in a .fai is declared to flang as though the script had written
// `param grenade_chance float = 0.35` itself, which is what makes a level's
// `params:` override work on a tree's numbers with no second mechanism — and
// what makes a name declared by both a reported conflict rather than a coin
// toss.
//
// **The tree hears an event before the script does.** Deciding comes first: a
// tree that sets `dead` on `:damage:` has done so before the script's own
// `:damage:` clause runs, which is the order that lets a script's handler be a
// consequence rather than a race. Either half handling a message counts as
// handled, so the "nothing answered this" warning stays honest.

import (
	"fmt"
	"sort"
	"strings"

	"foundry/internal/aitree"
	"foundry/internal/fileutil"
	"foundry/internal/flang"
	"foundry/internal/world"
)

// treeMaxErrors is how many failed runs one document gets before it is switched
// off, on the scripts' rule and for the scripts' reason: a node erroring every
// frame fills the log and slows the game without ever saying anything new.
const treeMaxErrors = 20

// treeUnit is one loaded .fai.
type treeUnit struct {
	name string
	path string
	doc  *aitree.Doc
	prog *aitree.Program
	// interp is the fallback for an entity with a tree and no script: an empty
	// program carrying only this document's parameters.
	interp   *flang.Interp
	errs     []aitree.Diagnostic
	failures int
	disabled bool
}

// loadTrees reads every decision tree the level names, before any script is
// parsed — the scripts need the trees' parameter declarations to link against.
func (rt *ScriptRuntime) loadTrees() {
	rt.trees = map[string]*treeUnit{}
	rt.treeParams = map[string][]string{}
	if rt.lvl == nil {
		return
	}
	// Sorted, so loading order and the diagnostics that come out of it do not
	// shuffle between runs.
	names := map[string]bool{}
	scriptsFor := map[string]map[string]bool{}
	note := func(tree, script string) {
		if tree == "" {
			return
		}
		names[tree] = true
		if script == "" {
			return
		}
		if scriptsFor[tree] == nil {
			scriptsFor[tree] = map[string]bool{}
		}
		scriptsFor[tree][script] = true
	}
	if world.PlayerEntity != nil {
		note(world.PlayerEntity.AITree, world.PlayerEntity.Script)
	}
	for i := range rt.lvl.Entities {
		note(rt.lvl.Entities[i].AITree, rt.lvl.Entities[i].Script)
	}
	// And every entity *type* that carries one, not only the ones already
	// placed — see aiTreeScriptPairs.
	for _, p := range aiTreeScriptPairs() {
		note(p.tree, p.script)
	}

	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		tu := rt.loadTree(n)
		if tu == nil || tu.prog == nil || tu.prog.ParamSource == "" {
			continue
		}
		paths := make([]string, 0, len(scriptsFor[n]))
		for p := range scriptsFor[n] {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			rt.treeParams[p] = append(rt.treeParams[p], tu.prog.ParamSource)
		}
	}
}

// loadTree reads and compiles one document, or returns the one already loaded.
func (rt *ScriptRuntime) loadTree(name string) *treeUnit {
	if name == "" {
		return nil
	}
	if rt.trees == nil {
		rt.trees = map[string]*treeUnit{}
	}
	if tu, ok := rt.trees[name]; ok {
		return tu
	}
	tu := &treeUnit{name: name, path: aitree.AIPath(name)}
	rt.trees[name] = tu

	doc, err := aitree.LoadAI(tu.path)
	if err != nil {
		tu.disabled = true
		ScriptLog.Errorf(treeLogPath(name), "%v", err)
		return tu
	}
	tu.doc = doc
	tu.prog = aitree.Compile(doc)
	tu.errs = tu.prog.Diags
	for _, d := range tu.prog.Diags {
		if d.Warning {
			ScriptLog.Warnf(treeLogPath(name), "%s", d.Error())
		} else {
			ScriptLog.Errorf(treeLogPath(name), "%s", d.Error())
		}
	}
	// Unlike a script, a document with mistakes in it still runs. A tree is
	// drawn a node at a time and half of one working while the other half is
	// finished is the point of a live editor; the broken nodes end their own
	// branches and say why (aitree/eval.go).
	if len(tu.prog.Errors()) > 0 {
		ScriptLog.Warnf(treeLogPath(name),
			"%d node(s) will do nothing until they are fixed; the rest of the tree runs",
			len(tu.prog.Errors()))
	}

	// The fallback interpreter, for an entity carrying this tree and no script.
	prog := flang.NewProgram()
	if tu.prog.ParamSource != "" {
		f, errs := flang.Parse(treeLogPath(name), []byte(tu.prog.ParamSource))
		if len(errs) == 0 {
			errs = prog.Add(f)
		}
		for _, e := range errs {
			ScriptLog.ErrorAt(treeLogPath(name), e.Pos, "%s", e.Msg)
		}
	}
	in, ierrs := flang.NewInterp(prog, rt.host, flang.Limits{})
	for _, e := range ierrs {
		ScriptLog.ErrorAt(treeLogPath(name), e.Pos, "%s", e.Msg)
	}
	in.SetOutput(func(s string) { rt.logLine(s) })
	tu.interp = in
	return tu
}

// treeLogPath is how a document is named in the log, so the window can tell one
// from a script with the same stem.
func treeLogPath(name string) string {
	return fileutil.RelAssetPath(aitree.AIPath(name))
}

// aiTreeFor returns the decision tree an object carries, by name. Only an
// entity can: the tree's whole vocabulary is an agent's.
func (rt *ScriptRuntime) aiTreeFor(r flang.RefVal) string {
	if r.Kind != flang.RefEntity {
		return ""
	}
	if e := rt.cache.entityFor(r); e != nil {
		return e.AITree
	}
	return ""
}

// runnerFor returns what will run an object's tree for one message, or nil when
// it has none.
//
// Built per delivery rather than held: it is four fields, and the alternative
// is a cache that has to be invalidated every time an entity is re-stamped,
// spawned, or replaced by an undo.
func (rt *ScriptRuntime) runnerFor(r flang.RefVal) (*aitree.Runner, *treeUnit) {
	name := rt.aiTreeFor(r)
	if name == "" {
		return nil, nil
	}
	tu := rt.loadTree(name)
	if tu == nil || tu.disabled || tu.prog == nil {
		return nil, nil
	}
	in := tu.interp
	backing := (*scriptUnit)(nil)
	if path := rt.scriptFor(r); path != "" {
		if u := rt.units[path]; u != nil && !u.disabled && u.interp != nil {
			in, backing = u.interp, u
		}
	}
	if in == nil {
		return nil, nil
	}
	_ = backing
	return &aitree.Runner{Prog: tu.prog, Interp: in, Host: rt.host, Self: r}, tu
}

// backingUnit is the script unit whose interpreter a tree on this object runs
// on, or nil. It is what rt.running is set to while the tree runs, so a verb
// that draws at random draws from the same generator the script would.
func (rt *ScriptRuntime) backingUnit(r flang.RefVal) *scriptUnit {
	if path := rt.scriptFor(r); path != "" {
		if u := rt.units[path]; u != nil && !u.disabled && u.interp != nil {
			return u
		}
	}
	return nil
}

// recordTreeFailure logs a failed run and switches the document off if it keeps
// happening.
func (rt *ScriptRuntime) recordTreeFailure(tu *treeUnit, err error, m queuedMsg) {
	rt.stats.Errors++
	tu.failures++
	var te *aitree.TreeError
	if e, ok := err.(*aitree.TreeError); ok {
		te = e
	}
	if te != nil {
		ScriptLog.Errorf(treeLogPath(tu.name),
			"%s stopped at node %s (%s): %v; nothing further down that branch ran",
			te.Tree, te.Node, te.Verb, te.Err)
	} else {
		ScriptLog.Errorf(treeLogPath(tu.name), "running :%s: on %s: %v", m.atom, m.to, err)
	}
	if tu.failures >= treeMaxErrors {
		tu.disabled = true
		ScriptLog.Warnf(treeLogPath(tu.name), "disabled after %d errors; fix it and reload", tu.failures)
	}
}

// ---------- what the editor asks ----------

// AITreeDiagnostics returns what compiling one document said, for the editor.
func AITreeDiagnostics(name string) []aitree.Diagnostic {
	if tu, ok := Scripts.trees[name]; ok {
		return tu.errs
	}
	return nil
}

// AITreeLoaded reports whether a document is loaded and running.
func AITreeLoaded(name string) bool {
	tu, ok := Scripts.trees[name]
	return ok && !tu.disabled
}

// AITreeUsage says which entity types and placements name a document, which is
// worth knowing before renaming or deleting one.
func AITreeUsage(lvl *world.Level, name string) []string {
	if lvl == nil || name == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for i := range lvl.Entities {
		e := &lvl.Entities[i]
		if e.AITree == name && !seen[e.ID] {
			seen[e.ID] = true
			out = append(out, e.ID)
		}
	}
	sort.Strings(out)
	return out
}

// ScriptFunctionNames lists the functions an entity's script defines, for the
// tree editor's `call` field. Empty for an entity with no script, which is the
// honest answer: there is nothing to call.
func ScriptFunctionNames(scriptPath string) []string {
	u, ok := Scripts.units[scriptPath]
	if !ok || u.prog == nil {
		return nil
	}
	out := make([]string, 0, len(u.prog.Funcs))
	for n := range u.prog.Funcs {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// treeParamConflict is the message a name declared by both a tree and the
// script beside it produces. Spelled out here rather than left to flang's own
// duplicate-param error, which would name two files without saying that one of
// them is a drawing.
func treeParamConflict(tree, script, name string) string {
	return fmt.Sprintf("%s declares the parameter %q and so does %s; one of them has to give it up — "+
		"a tree and the script it runs beside share one set of parameters, which is how a level's params: block reaches both",
		tree, name, script)
}

// handlerSourceOf names what was asked to answer a message, for the warning
// deliver files when nothing did. An object with both halves has to say both,
// or the author goes looking in the one they did not write the behaviour in.
func handlerSourceOf(rt *ScriptRuntime, to flang.RefVal) string {
	script, tree := rt.scriptFor(to), rt.aiTreeFor(to)
	switch {
	case script != "" && tree != "":
		return fmt.Sprintf("neither its decision tree (%s) nor its script (%s) has", tree, script)
	case tree != "":
		return fmt.Sprintf("its decision tree (%s) has no root for it, and it has no script with", tree)
	}
	return fmt.Sprintf("its script (%s) defines", script)
}

// treeRootHint adds the one sentence worth adding when an object has a tree
// that simply has no root for this event: that is a root away from working.
func treeRootHint(rt *ScriptRuntime, to flang.RefVal, atom string) string {
	if rt.aiTreeFor(to) == "" {
		return ""
	}
	return fmt.Sprintf(" — add a tree rooted at :%s: in the AI editor, or a clause for it in the script", atom)
}

// EntityPropertyNames is the fixed set of engine properties an entity has, for
// the tree editor's `prop` field. Read from the runtime's own table rather than
// listed again here, so a property added to the engine appears in the picker.
func EntityPropertyNames() []string {
	tbl := entityProps()
	out := make([]string, 0, len(tbl))
	for name := range tbl {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// AIEventDoc is one message a tree can be rooted at, as the editor's "add root"
// dialog lists it.
type AIEventDoc struct {
	// Name is the atom without its colons, which is how a tree spells it.
	Name string
	// Args are the names to bind, comma-separated and ready to be read straight
	// into the tree's `args`.
	Args string
	Doc  string
}

// AIEventDocs is what the editor offers when adding an event root: every event
// the engine posts, with the AI controller's own first — those are the ones a
// behaviour is built out of, and hunting for them in an alphabetical list of
// forty is how the dialog stopped being useful.
func AIEventDocs() []AIEventDoc {
	first := map[string]bool{
		":spawn:": true, ":tick:": true, ":damage:": true, ":anim_end:": true,
		":target_acquired:": true, ":target_lost:": true, ":target_in_range:": true,
		":ai_arrived:": true, ":ai_path_failed:": true, ":ai_maneuver:": true,
		":ai_in_position:": true, ":ai_dodge:": true, ":cover_taken:": true,
	}
	var lead, rest []AIEventDoc
	for _, d := range EventDocs() {
		e := AIEventDoc{
			Name: strings.Trim(d.Atom, ":"),
			Args: argNamesOf(d.Args),
			Doc:  d.When + "\n\nPosted to: " + d.PostedTo,
		}
		if first[d.Atom] {
			lead = append(lead, e)
		} else {
			rest = append(rest, e)
		}
	}
	return append(lead, rest...)
}

// argNamesOf turns an event doc's parameter list ("src ref, amount int") into
// the bare names a tree binds ("src, amount"). An event that carries nothing is
// written "—" in the docs and becomes the empty list.
func argNamesOf(params string) string {
	if params == "" || params == "—" {
		return ""
	}
	var out []string
	for _, p := range strings.Split(params, ",") {
		fields := strings.Fields(strings.TrimSpace(p))
		if len(fields) > 0 {
			out = append(out, fields[0])
		}
	}
	return strings.Join(out, ", ")
}

// ---------- which trees run beside which scripts ----------

// treeScriptPair is one entity type's pairing of a decision tree with a script.
type treeScriptPair struct{ tree, script string }

// aiTreeScriptPairs reads the entity definitions for every type that carries a
// decision tree, and says which script it runs beside.
//
// A type a script spawns mid-level has to have had its tree's parameters linked
// into that script *before* the script was linked, and there is no honest way to
// do that after the fact — so the definitions are read up front. They are small
// YAML files and LoadEntityDef caches them; nothing here touches a model or a
// texture.
func aiTreeScriptPairs() []treeScriptPair {
	var out []treeScriptPair
	for _, name := range world.ScanEntityDefs() {
		def, err := world.LoadEntityDef(name)
		if err != nil || def == nil || def.AITree == "" {
			continue
		}
		out = append(out, treeScriptPair{tree: def.AITree, script: def.Script})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].tree != out[j].tree {
			return out[i].tree < out[j].tree
		}
		return out[i].script < out[j].script
	})
	return out
}

// AITreeParamNames returns the parameters every decision tree that runs beside
// this script declares.
//
// It exists because a tree's `params:` are declared *to* the script it pairs
// with (see this file's header), so a linter that only reads the script's own
// text would report every `?name` the tree declared as undeclared — seventeen
// false errors on a script that is perfectly correct. Every place that lints a
// script has to ask this: `foundry check`, the script editor's live lint, and
// the runtime's own load.
//
// The answer comes off disk rather than out of the runtime, because two of
// those three callers have no runtime and no level.
func AITreeParamNames(scriptPath string) map[string]bool {
	if scriptPath == "" {
		return nil
	}
	want := fileutil.ScriptPath(scriptPath)
	out := map[string]bool{}
	seen := map[string]bool{}
	for _, p := range aiTreeScriptPairs() {
		if p.script == "" || fileutil.ScriptPath(p.script) != want || seen[p.tree] {
			continue
		}
		seen[p.tree] = true
		doc, err := aitree.LoadAI(aitree.AIPath(p.tree))
		if err != nil {
			continue
		}
		for _, pm := range doc.Params {
			if pm.Name != "" {
				out[pm.Name] = true
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AddAITreeParams merges AITreeParamNames into a linter's parameter set. Nil-safe
// on both sides, and returns the set to use.
func AddAITreeParams(params map[string]bool, scriptPath string) map[string]bool {
	extra := AITreeParamNames(scriptPath)
	if len(extra) == 0 {
		return params
	}
	if params == nil {
		params = map[string]bool{}
	}
	for n := range extra {
		params[n] = true
	}
	return params
}
