package script

// script_lint.go — the checks that can be made before a script is ever run.
//
// A flang script parses cleanly and still does nothing, because parsing is the
// wrong question. `play_anim(@button, "button", "button_press")` is a perfectly
// well-formed line whether or not the level contains a `button`, whether or not
// that entity has an attachment called `button`, and whether or not the
// attachment declares an animation of that name. All three are wiring, and all
// three used to be discovered by walking into the volume and pressing E.
//
// Everything here is a check against the level, so it belongs to the engine
// rather than to internal/flang: the language knows there is a host, not what a
// host contains.
//
// Three rules keep it honest.
//
//   - **Literals only.** A check runs when every argument it needs is written in
//     the source. `play_anim(@self, name)` is skipped entirely, because the
//     values are not knowable until it runs. A linter that guesses is a linter
//     people learn to ignore.
//   - **Warnings, never errors.** These are checked against whichever level
//     happens to be open. A script shared between levels will legitimately name
//     objects this one does not have, so an issue never blocks a run — it is
//     shown, and the author decides.
//   - **The same words as the runtime.** The animation and texture reasons come
//     from entity_diagnose.go, which is also what the verbs log. Being told one
//     thing by the editor and a different thing by the log window is worse than
//     being told nothing twice.

import (
	"fmt"
	"sort"
	"strings"

	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/world"
)

// ScriptIssue is one thing a script asks for that the level cannot deliver.
type ScriptIssue struct {
	Pos flang.Pos
	Msg string
}

// scriptEventAtoms is every atom the engine posts by itself. Used only to catch
// a near-miss on one — a custom atom needs no declaration, so an unrecognised
// name is not by itself a problem.
var scriptEventAtoms = []string{
	"spawn", "destroy", "tick", "enter", "exit",
	"collide", "interact", "anim_start", "anim_end",
	"mount", "dismount", "portal_seal",
}

// linter carries what the checks share: the level, its index, and the lazily
// built asset lists.
type linter struct {
	lvl   *world.Level
	cache *refCache
	// inScope is every function name the script can call — its own plus its
	// imports'. Nil disables the unknown-call check, which is what a caller that
	// could not resolve the imports should pass rather than a partial set.
	inScope map[string]bool
	// handled is the set of atoms this script has a clause for, for `after`.
	handled map[string]bool
	// params is every name the script declares with `param`. `@name` for one of
	// those is bound by the level rather than by an object called `name`, so it
	// is exempt from the does-this-exist check — the linter is reading the file,
	// and the file is deliberately silent about which object it will be given.
	params map[string]bool

	defs     []string // entity definition names, scanned on first use
	defsRead bool

	issues []ScriptIssue
}

// LintScript checks one parsed file against a level, returning what it found in
// source order.
//
// lvl may be nil — the script editor can be open with no level loaded — in which
// case only the checks that need no world are made.
func LintScript(lvl *world.Level, f *flang.File, inScope, params map[string]bool) []ScriptIssue {
	if f == nil {
		return nil
	}
	l := &linter{
		lvl:     lvl,
		cache:   buildRefCache(lvl),
		inScope: inScope,
		handled: handledAtoms(f),
		params:  params,
	}
	for _, g := range f.Funcs {
		for _, c := range g.Clauses {
			l.clause(c)
		}
	}
	sort.SliceStable(l.issues, func(i, j int) bool {
		if l.issues[i].Pos.Line != l.issues[j].Pos.Line {
			return l.issues[i].Pos.Line < l.issues[j].Pos.Line
		}
		return l.issues[i].Pos.Col < l.issues[j].Pos.Col
	})
	return l.issues
}

// handledAtoms is every atom the file has a clause for — the first parameter of
// a clause, which is what makes it an event handler.
func handledAtoms(f *flang.File) map[string]bool {
	out := map[string]bool{}
	for _, g := range f.Funcs {
		for _, c := range g.Clauses {
			if len(c.Params) > 0 && c.Params[0].Kind == flang.ParamAtom {
				out[c.Params[0].Atom] = true
			}
		}
	}
	return out
}

func (l *linter) add(pos flang.Pos, format string, args ...any) {
	l.issues = append(l.issues, ScriptIssue{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// ---------- walking ----------

func (l *linter) clause(c *flang.Clause) {
	if len(c.Params) > 0 && c.Params[0].Kind == flang.ParamAtom {
		l.handlerAtom(c.Params[0])
	}
	if c.Guard != nil {
		l.expr(c.Guard)
	}
	l.stmts(c.Body)
}

// handlerAtom flags a first parameter that is *nearly* an engine event.
//
// Only a near miss, never an unrecognised name: custom atoms are ordinary and
// need no declaration, so complaining about every one would make this useless.
// `:intract:` is a typo; `:unlock:` is a design.
func (l *linter) handlerAtom(p flang.Param) {
	if containsString(scriptEventAtoms, p.Atom) {
		return
	}
	if near := world.NearestName(p.Atom, scriptEventAtoms); near != "" {
		l.add(p.Pos_, "this handler is for :%s:, which the engine never posts — did you mean :%s:? "+
			"(a custom atom needs no declaration, so ignore this if :%s: is your own message)",
			p.Atom, near, p.Atom)
	}
}

func (l *linter) stmts(body []flang.Stmt) {
	for _, s := range body {
		l.stmt(s)
	}
}

func (l *linter) stmt(s flang.Stmt) {
	switch t := s.(type) {
	case *flang.AssignStmt:
		l.expr(t.Target)
		l.expr(t.Value)
	case *flang.SendStmt:
		l.expr(t.Target)
		l.expr(t.Msg)
		l.send(t)
	case *flang.IfStmt:
		for _, c := range t.Conds {
			l.expr(c)
		}
		for _, b := range t.Blocks {
			l.stmts(b)
		}
		l.stmts(t.Else)
	case *flang.ForStmt:
		l.expr(t.Seq)
		l.stmts(t.Body)
	case *flang.WhileStmt:
		l.expr(t.Cond)
		l.stmts(t.Body)
	case *flang.ReturnStmt:
		l.expr(t.Value)
	case *flang.ExprStmt:
		l.expr(t.X)
	}
}

func (l *linter) expr(e flang.Expr) {
	switch t := e.(type) {
	case nil:
		return
	case *flang.VecLit:
		l.expr(t.X)
		l.expr(t.Y)
		l.expr(t.Z)
	case *flang.ListLit:
		for _, el := range t.Elems {
			l.expr(el)
		}
	case *flang.DictLit:
		for _, v := range t.Values {
			l.expr(v)
		}
	case *flang.TupleLit:
		for _, el := range t.Elems {
			l.expr(el)
		}
	case *flang.UnaryExpr:
		l.expr(t.X)
	case *flang.BinaryExpr:
		l.expr(t.L)
		l.expr(t.R)
	case *flang.IndexExpr:
		l.expr(t.X)
		l.expr(t.Index)
	case *flang.MemberExpr:
		l.expr(t.X)
		l.member(t)
	case *flang.CallExpr:
		for _, a := range t.Args {
			l.expr(a)
		}
		l.call(t)
	case *flang.AddrExpr:
		l.addr(t)
	case *flang.TagExpr:
		l.tag(t)
	}
}

// ---------- the checks ----------

// addr flags `@name` for a name the level does not have.
func (l *linter) addr(x *flang.AddrExpr) {
	if l.lvl == nil {
		return
	}
	switch x.Name {
	case "self", "level", "player":
		// Engine-provided: @self depends on what the script is attached to,
		// @level always resolves, and @player exists during play.
		return
	}
	if l.params[x.Name] {
		// A script parameter: which object it names is the level's answer, not
		// this file's, and it may differ for every object running the script.
		return
	}
	if _, ok := l.cache.refFor(x.Name); ok {
		return
	}
	msg := fmt.Sprintf("nothing in this level is called %q, so @%s is null", x.Name, x.Name)
	if near := world.NearestName(x.Name, l.levelNames()); near != "" {
		msg += fmt.Sprintf("; did you mean @%s?", near)
	}
	l.add(x.Pos_, "%s", msg)
}

// tag flags `#tag` that nothing carries.
func (l *linter) tag(x *flang.TagExpr) {
	if l.lvl == nil {
		return
	}
	if len(l.cache.refsWithTag(x.Name)) > 0 {
		return
	}
	msg := fmt.Sprintf("nothing in this level carries the tag %q, so #%s is an empty list", x.Name, x.Name)
	if near := world.NearestName(x.Name, l.cache.allTags()); near != "" {
		msg += fmt.Sprintf("; did you mean #%s?", near)
	}
	l.add(x.Pos_, "%s", msg)
}

// member flags an unknown property name on an object whose kind is knowable.
//
// Only `@name.prop.x` — the kind has to be known for the check to mean anything,
// and it is only known when the base is a literal address that resolves.
func (l *linter) member(x *flang.MemberExpr) {
	inner, ok := x.X.(*flang.MemberExpr)
	if !ok || inner.Name != "prop" {
		return
	}
	kind, ok := l.kindOf(inner.X)
	if !ok {
		return
	}
	names := PropertyNames(kind)
	if len(names) == 0 || containsString(names, x.Name) {
		return
	}
	msg := fmt.Sprintf("a %s has no property called %q", kind, x.Name)
	if near := world.NearestName(x.Name, names); near != "" {
		msg += fmt.Sprintf("; did you mean %q?", near)
	}
	l.add(x.Pos_, "%s", msg)
}

// send flags a message addressed to `@self` that this script cannot handle. Only
// `@self`, because that is the one target whose script is certainly this one.
func (l *linter) send(s *flang.SendStmt) {
	addr, ok := s.Target.(*flang.AddrExpr)
	if !ok || addr.Name != "self" {
		return
	}
	l.selfAtom(s.Msg, s.Position())
}

// call is the per-verb argument checking.
func (l *linter) call(c *flang.CallExpr) {
	name, ok := c.Fn.(*flang.IdentExpr)
	if !ok {
		return
	}
	switch name.Name {
	case "play_anim":
		l.playAnim(c)
	case "set_texture":
		l.setTexture(c)
	case "spawn":
		l.spawn(c)
	case "after":
		l.after(c)
	}
	l.knownFunc(name)
}

// knownFunc flags a call to something neither defined nor built in.
func (l *linter) knownFunc(name *flang.IdentExpr) {
	if l.inScope == nil || l.inScope[name.Name] || flang.IsBuiltin(name.Name) {
		return
	}
	msg := fmt.Sprintf("nothing defines a function called %q", name.Name)
	if near := world.NearestName(name.Name, l.scopeNames()); near != "" {
		msg += fmt.Sprintf("; did you mean %q?", near)
	}
	l.add(name.Pos_, "%s", msg)
}

// playAnim runs the runtime's own animation check against literal arguments.
func (l *linter) playAnim(c *flang.CallExpr) {
	e, ok := l.entityArg(c, 0)
	if !ok {
		return
	}
	var model, anim string
	switch len(c.Args) {
	case 2:
		if anim, ok = litString(c.Args[1]); !ok {
			return
		}
	case 3:
		if model, ok = litString(c.Args[1]); !ok {
			return
		}
		if anim, ok = litString(c.Args[2]); !ok {
			return
		}
	default:
		return
	}
	if why := e.AnimationProblem(model, anim); why != "" {
		l.add(c.Pos_, "play_anim would do nothing: %s", why)
	}
}

// setTexture checks the attachment exists and the texture is on disk.
func (l *linter) setTexture(c *flang.CallExpr) {
	if len(c.Args) < 2 || len(c.Args) > 3 {
		return
	}
	if name, ok := litString(c.Args[len(c.Args)-1]); ok {
		l.textureExists(name, c.Pos_)
	}
	kind, known := l.kindOf(c.Args[0])
	if known && len(c.Args) == 2 {
		switch kind {
		case flang.RefEntity, flang.RefEdge, flang.RefFloor, flang.RefCeiling:
		default:
			l.add(c.Pos_, "set_texture: a %s has no texture; name an entity, an edge, a floor or a ceiling", kind)
			return
		}
	}
	e, ok := l.entityArg(c, 0)
	if !ok {
		return
	}
	model := ""
	if len(c.Args) == 3 {
		if model, ok = litString(c.Args[1]); !ok {
			return
		}
	}
	if why := e.TextureProblem(model); why != "" {
		l.add(c.Pos_, "set_texture would do nothing: %s", why)
	}
}

// textureExists flags a texture name with no .png behind it. Skipped when the
// texture list is empty, which means it was never scanned — in a test, or before
// startup finished — rather than that the folder is empty.
func (l *linter) textureExists(name string, pos flang.Pos) {
	if len(gfx.TextureNames) == 0 || name == "" {
		return
	}
	if _, ok := gfx.TextureNames[name]; ok {
		return
	}
	msg := fmt.Sprintf("there is no texture called %q in textures/", name)
	if near := world.NearestName(name, textureKeys()); near != "" {
		msg += fmt.Sprintf("; did you mean %q?", near)
	}
	l.add(pos, "%s", msg)
}

// spawn flags a type name with no definition file.
func (l *linter) spawn(c *flang.CallExpr) {
	if len(c.Args) == 0 {
		return
	}
	name, ok := litString(c.Args[0])
	if !ok {
		return
	}
	defs := l.entityDefs()
	if len(defs) == 0 || containsString(defs, name) {
		return
	}
	msg := fmt.Sprintf("there is no entity definition called %q in resources/entities/", name)
	if near := world.NearestName(name, defs); near != "" {
		msg += fmt.Sprintf("; did you mean %q?", near)
	}
	l.add(c.Pos_, "%s", msg)
}

// after flags a timer whose atom this script never handles. `after` always
// addresses @self, so the handler can only be here.
func (l *linter) after(c *flang.CallExpr) {
	if len(c.Args) < 2 {
		return
	}
	l.selfAtom(c.Args[1], c.Pos_)
}

// selfAtom flags an atom sent to @self that no clause in this file matches. The
// message may be a bare atom or the first element of a tuple.
func (l *linter) selfAtom(msg flang.Expr, pos flang.Pos) {
	if tup, ok := msg.(*flang.TupleLit); ok && len(tup.Elems) > 0 {
		msg = tup.Elems[0]
	}
	atom, ok := msg.(*flang.AtomLit)
	if !ok || l.handled[atom.Name] {
		return
	}
	l.add(pos, "this script sends :%s: to itself but defines no clause for it, so nothing will happen", atom.Name)
}

// ---------- resolution helpers ----------

// entityArg resolves argument i to an entity in the level, when it is written as
// a literal address that names one.
func (l *linter) entityArg(c *flang.CallExpr, i int) (*world.Entity, bool) {
	if l.lvl == nil || i >= len(c.Args) {
		return nil, false
	}
	addr, ok := c.Args[i].(*flang.AddrExpr)
	if !ok {
		return nil, false
	}
	// @self is whatever the script is attached to and @player only exists during
	// play; neither can be checked from here.
	if addr.Name == "self" || addr.Name == "player" || addr.Name == "level" {
		return nil, false
	}
	if l.params[addr.Name] {
		// The level supplies this one. Checked explicitly rather than left to
		// the lookup below: a param that happens to share a name with an object
		// in this level would otherwise be checked against the wrong entity.
		return nil, false
	}
	r, ok := l.cache.refFor(addr.Name)
	if !ok || r.Kind != flang.RefEntity {
		// A name that resolves to nothing is already reported by addr(), and one
		// that resolves to a volume is reported by the verb's own kind check.
		return nil, false
	}
	e := l.cache.entityFor(r)
	return e, e != nil
}

// kindOf returns the kind of object an expression names, when that is knowable
// from the source alone.
func (l *linter) kindOf(e flang.Expr) (flang.Kind_, bool) {
	if l.lvl == nil {
		return flang.RefNone, false
	}
	switch t := e.(type) {
	case *flang.AddrExpr:
		switch t.Name {
		case "level":
			return flang.RefLevel, true
		case "player":
			return flang.RefEntity, true
		case "self":
			return flang.RefNone, false
		}
		if l.params[t.Name] {
			// Whatever the level bound, which is not knowable from here — and a
			// guess would make the property check reject valid code.
			return flang.RefNone, false
		}
		r, ok := l.cache.refFor(t.Name)
		return r.Kind, ok
	case *flang.MemberExpr:
		// `@sec.floor` and `@sec.ceil` are the two structural steps that change
		// the kind; everything else is a value.
		base, ok := l.kindOf(t.X)
		if !ok || base != flang.RefSector {
			return flang.RefNone, false
		}
		switch t.Name {
		case "floor":
			return flang.RefFloor, true
		case "ceil", "ceiling":
			return flang.RefCeiling, true
		}
	}
	return flang.RefNone, false
}

// levelNames is every ID a script could address, for "did you mean".
func (l *linter) levelNames() []string {
	if l.lvl == nil {
		return nil
	}
	var out []string
	for i := range l.lvl.Entities {
		out = append(out, l.lvl.Entities[i].ID)
	}
	for i := range l.lvl.Volumes {
		out = append(out, l.lvl.Volumes[i].ID)
	}
	for ci := range l.lvl.Chunks {
		ch := &l.lvl.Chunks[ci]
		out = append(out, ch.ID)
		for si := range ch.Sectors {
			out = append(out, ch.Sectors[si].ID)
		}
	}
	for i := range l.lvl.Lights.Directional {
		out = append(out, l.lvl.Lights.Directional[i].ID)
	}
	for i := range l.lvl.Lights.Points {
		out = append(out, l.lvl.Lights.Points[i].ID)
	}
	return out
}

// scopeNames is every callable name, for "did you mean".
func (l *linter) scopeNames() []string {
	out := flang.BuiltinNames()
	for n := range l.inScope {
		out = append(out, n)
	}
	return out
}

// entityDefs scans resources/entities once per lint run.
func (l *linter) entityDefs() []string {
	if !l.defsRead {
		l.defs = world.ScanEntityDefs()
		l.defsRead = true
	}
	return l.defs
}

// textureKeys lists the logical texture names, for "did you mean".
func textureKeys() []string {
	out := make([]string, 0, len(gfx.TextureNames))
	for n := range gfx.TextureNames {
		out = append(out, n)
	}
	return out
}

// litString returns a string literal's value.
func litString(e flang.Expr) (string, bool) {
	s, ok := e.(*flang.StringLit)
	if !ok {
		return "", false
	}
	return s.Value, true
}

func containsString(list []string, s string) bool {
	for _, c := range list {
		if c == s {
			return true
		}
	}
	return false
}

// ---------- rendering ----------

// ScriptIssuesText renders a list for a tooltip or a log line.
func ScriptIssuesText(issues []ScriptIssue) string {
	parts := make([]string, len(issues))
	for i, s := range issues {
		parts[i] = fmt.Sprintf("%d:%d  %s", s.Pos.Line, s.Pos.Col, s.Msg)
	}
	return strings.Join(parts, "\n")
}
