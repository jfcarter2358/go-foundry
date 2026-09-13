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
	"foundry/internal/particle"
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
	"mount", "dismount", "portal_seal", "waypoint", "waypoints_end",
	"click", "hover", "unhover", "change", "submit", "cancel", "shown", "hidden",
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
	// params is every name the script declares with `param`, plus its imports'.
	// It is what `?name` is checked against, and what tells an `@name` that
	// should have been a `?name` from one that is simply missing.
	params map[string]bool

	defs     []string // entity definition names, scanned on first use
	defsRead bool

	effects     []string // particle effect names, scanned on first use
	effectsRead bool

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
// `:intract:` is a typo; `:unlock:` is a design. An atom one of the level's
// waypoints posts is a design too, however close it is to `:waypoint:`.
func (l *linter) handlerAtom(p flang.Param) {
	if containsString(scriptEventAtoms, p.Atom) || containsString(world.WaypointEventAtoms(l.lvl), p.Atom) {
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
		l.assign(t)
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
	case *flang.ParamExpr:
		l.param(t)
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
	if _, ok := l.cache.refFor(x.Name); ok {
		return
	}
	if l.params[x.Name] {
		// Declared, just not with the sigil that reads it. `@` asks the level
		// for an object — and the check above says there is no such object — so
		// this is the wrong sigil rather than a missing binding.
		l.add(x.Pos_, "%s is a script parameter, not an object id; write ?%s", x.Name, x.Name)
		return
	}
	msg := fmt.Sprintf("nothing in this level is called %q, so @%s is null", x.Name, x.Name)
	if near := world.NearestName(x.Name, l.levelNames()); near != "" {
		msg += fmt.Sprintf("; did you mean @%s?", near)
	}
	l.add(x.Pos_, "%s", msg)
}

// param flags `?name` for a parameter the script never declares.
//
// Unlike an object, there is nothing else the name could be: the declarations
// are in this file and its imports, so the set is fully known here. Checked with
// no level, because none is needed.
func (l *linter) param(x *flang.ParamExpr) {
	if l.params == nil || l.params[x.Name] {
		return
	}
	msg := fmt.Sprintf("no `param %s` is declared, so ?%s is an error at runtime", x.Name, x.Name)
	if near := world.NearestName(x.Name, l.paramNames()); near != "" {
		msg += fmt.Sprintf("; did you mean ?%s?", near)
	}
	l.add(x.Pos_, "%s", msg)
}

// paramNames lists the declared parameters in a stable order, for "did you
// mean".
func (l *linter) paramNames() []string {
	out := make([]string, 0, len(l.params))
	for n := range l.params {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
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
	case "spawn":
		l.spawn(c)
	case "spawn_effect":
		l.spawnEffect(c)
	case "attach", "detach":
		l.attach(c)
	case "after":
		l.afterCall(c)
	case "hitscan":
		l.hitscan(c)
	case "play_waypoints", "stop_waypoints":
		l.waypoints(c, name.Name)
	case "move_to", "rotate_to", "scale_to":
		l.tween(c, name.Name)
	}
	l.knownFunc(name)
}

// tween checks the object move_to, rotate_to and scale_to are handed, when it
// is written as a literal: something of a kind the verb can ease, and — for a
// brush — one a script is allowed to move. Each is a refusal that would
// otherwise arrive only when the call runs.
func (l *linter) tween(c *flang.CallExpr, verb string) {
	if l.lvl == nil || len(c.Args) == 0 {
		return
	}
	addr, ok := c.Args[0].(*flang.AddrExpr)
	if !ok {
		return
	}
	r, ok := l.cache.refFor(addr.Name)
	if !ok {
		return // addr() has already said there is no such object
	}
	switch r.Kind {
	case flang.RefBrush:
		if b := l.cache.brushFor(r); b != nil && !b.Dynamic {
			l.add(addr.Pos_, "%s would fail: brush %q is not dynamic; tick Dynamic in its properties panel", verb, addr.Name)
		}
	case flang.RefChunk:
		if verb != "move_to" {
			l.add(addr.Pos_, "%s would fail: a chunk's rotation and scale are read-only, so only move_to works on @%s", verb, addr.Name)
		}
	case flang.RefEntity:
		if e := l.cache.entityFor(r); e != nil && e.IsVehicle() {
			l.add(addr.Pos_, "%s would fail: @%s is a vehicle, and its drive model owns its motion", verb, addr.Name)
		}
	default:
		what := "a brush, a chunk or an entity"
		if verb != "move_to" {
			what = "a brush or an entity"
		}
		l.add(addr.Pos_, "%s eases %s, and @%s is a %s", verb, what, addr.Name, r.Kind)
	}
}

// waypoints checks what play_waypoints and stop_waypoints need from the level,
// for the arguments written as literals: a brush a script is allowed to move,
// and — for play — an animation that exists and has somewhere to send it.
//
// The dynamic check is the one worth making early. Without it the refusal
// arrives only when the call runs, which for a door is the moment somebody
// walks up to it.
func (l *linter) waypoints(c *flang.CallExpr, verb string) {
	if l.lvl == nil || len(c.Args) == 0 {
		return
	}
	if addr, ok := c.Args[0].(*flang.AddrExpr); ok {
		if r, ok := l.cache.refFor(addr.Name); ok {
			switch r.Kind {
			case flang.RefBrush:
				if b := l.cache.brushFor(r); b != nil && !b.Dynamic {
					l.add(addr.Pos_, "%s would fail: brush %q is not dynamic; tick Dynamic in its properties panel", verb, addr.Name)
				}
			case flang.RefEntity:
				if e := l.cache.entityFor(r); e == world.PlayerEntity && e != nil {
					l.add(addr.Pos_, "%s would fail: it cannot move the player", verb)
				} else if e.IsVehicle() {
					l.add(addr.Pos_, "%s would fail: @%s is a vehicle, and its drive model owns its motion", verb, addr.Name)
				}
			case flang.RefLight:
				if d, p := l.cache.lightFor(r); (d != nil && !d.Dynamic) || (p != nil && !p.Dynamic) {
					l.add(addr.Pos_, "%s would fail: light %q is static; tick Dynamic in its properties panel", verb, addr.Name)
				}
			case flang.RefSprite, flang.RefEmitter, flang.RefSound, flang.RefVolume:
			default:
				l.add(addr.Pos_, "%s moves %s, and @%s is a %s", verb, waypointFollowerKinds, addr.Name, r.Kind)
			}
		}
	}
	if verb != "play_waypoints" || len(c.Args) < 2 {
		return
	}

	var anim *world.WaypointAnimation
	switch a := c.Args[1].(type) {
	case *flang.AddrExpr:
		r, ok := l.cache.refFor(a.Name)
		if !ok {
			return // addr() has already said there is no such object
		}
		if r.Kind != flang.RefWaypoints {
			l.add(a.Pos_, "play_waypoints needs a waypoint animation, and @%s is a %s", a.Name, r.Kind)
			return
		}
		anim = l.cache.waypointAnimationFor(r)
	case *flang.StringLit:
		anim = world.FindWaypointAnimation(l.lvl, a.Value)
		if anim == nil {
			// A type's script names its own paths by the name the type gave
			// them, which the verb qualifies by the placement at run time.
			anim = world.FindCarriedWaypointAnimationNamed(l.lvl, a.Value)
		}
		if anim == nil {
			msg := fmt.Sprintf("play_waypoints would fail: this level has no waypoint animation called %q", a.Value)
			names := make([]string, 0, len(l.lvl.WaypointAnimations))
			for i := range l.lvl.WaypointAnimations {
				names = append(names, l.lvl.WaypointAnimations[i].ID)
			}
			if near := world.NearestName(a.Value, names); near != "" {
				msg += fmt.Sprintf("; did you mean %q?", near)
			}
			l.add(c.Pos_, "%s", msg)
			return
		}
	}
	if anim != nil && len(anim.Points) == 0 {
		l.add(c.Pos_, "play_waypoints would do nothing: waypoint animation %q has no points", anim.ID)
	}
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

// assign is the per-property checking of a write, which is what a reskin now
// is: `@guard.prop.texture = "rust"`, or `@guard.model.body.prop.texture` for
// one attachment of several.
//
// The same two questions the `set_texture` verb used to answer at author time —
// is there a .png behind that name, and is there anything here to reskin — with
// the advantage that they are now asked of the *only* way to write a texture
// rather than of one of two.
func (l *linter) assign(a *flang.AssignStmt) {
	target, ok := a.Target.(*flang.MemberExpr)
	if !ok {
		return
	}
	inner, ok := target.X.(*flang.MemberExpr)
	if !ok || inner.Name != "prop" {
		return
	}
	l.staticLightWrite(inner.X, target.Name, a.Pos_)
	if target.Name != "texture" {
		return
	}
	if name, ok := litString(a.Value); ok {
		l.textureExists(name, a.Pos_)
	}
	// Only an entity or one of its attachments can be reskinned into nothing:
	// a wall always has a face to wear the texture. Which attachment is what
	// the ref says, and a ref the source does not pin down is not checked.
	base := inner.X
	model := ""
	if kind, known := l.kindOf(base); known && kind == flang.RefModel {
		m, ok := base.(*flang.MemberExpr)
		if !ok {
			return
		}
		model = m.Name
		base = m.X
		if b, ok := base.(*flang.MemberExpr); ok && b.Name == "model" {
			base = b.X
		}
	}
	e, ok := l.entityExpr(base)
	if !ok {
		return
	}
	if why := e.TextureProblem(model); why != "" {
		l.add(a.Pos_, "writing texture would do nothing: %s", why)
	}
}

// staticLightWrite flags a property write on a light, written as a literal,
// that is not dynamic — a refusal that would otherwise arrive only when the
// write runs, which for a flickering lamp is whenever its trigger fires.
func (l *linter) staticLightWrite(base flang.Expr, prop string, pos flang.Pos) {
	addr, ok := base.(*flang.AddrExpr)
	if !ok || l.lvl == nil {
		return
	}
	r, ok := l.cache.refFor(addr.Name)
	if !ok || r.Kind != flang.RefLight {
		return
	}
	if acc, ok := propTables[flang.RefLight][prop]; !ok || acc.Set == nil {
		return // an unknown or read-only property is someone else's complaint
	}
	if d, p := l.cache.lightFor(r); (d != nil && !d.Dynamic) || (p != nil && !p.Dynamic) {
		l.add(pos, "writing %s would fail: light %q is static; tick Dynamic in its properties panel", prop, addr.Name)
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

// spawnEffect flags a spawn_effect naming an effect that is not in particles/.
// A typo'd effect name is the likeliest mistake with this verb, and at runtime
// it is a log line during combat rather than something anyone reads.
func (l *linter) spawnEffect(c *flang.CallExpr) {
	if len(c.Args) == 0 {
		return
	}
	name, ok := litString(c.Args[0])
	if !ok {
		return
	}
	effects := l.particleEffects()
	if len(effects) == 0 || containsString(effects, name) {
		return
	}
	msg := fmt.Sprintf("there is no particle effect called %q in particles/", name)
	if near := world.NearestName(name, effects); near != "" {
		msg += fmt.Sprintf("; did you mean %q?", near)
	}
	l.add(c.Pos_, "%s", msg)
}

// attach checks what is being bound and what it is being bound to, against
// literal arguments. Both halves are worth catching before the run: attaching an
// entity is a category error the verb can only refuse at runtime, and a
// mistyped bone name binds to nothing in a way that looks exactly like an
// effect that is simply not visible from where the author is standing.
func (l *linter) attach(c *flang.CallExpr) {
	name := "attach"
	if id, ok := c.Fn.(*flang.IdentExpr); ok {
		name = id.Name
	}
	if len(c.Args) == 0 {
		return
	}
	if kind, known := l.kindOf(c.Args[0]); known {
		switch kind {
		case flang.RefSprite, flang.RefEmitter:
		default:
			l.add(c.Pos_, "%s: %s cannot be attached; only sprites and emitters ride things — an entity boards a vehicle with mount", name, aKind(kind))
			return
		}
	}
	if name == "detach" || len(c.Args) < 2 || len(c.Args) > 4 {
		return
	}
	// A str in the carrier slot names a frame, and which frames exist is a
	// property of the running level rather than of the file, so it is left to
	// the runtime. Anything else has to be an entity.
	if _, isStr := litString(c.Args[1]); !isStr {
		if kind, known := l.kindOf(c.Args[1]); known && kind != flang.RefEntity {
			l.add(c.Pos_, "attach: %s cannot be ridden; name an entity, or a frame as a string", aKind(kind))
			return
		}
	}
	if len(c.Args) < 3 {
		return
	}
	e, ok := l.entityArg(c, 1)
	if !ok {
		return
	}
	model, bone := "", ""
	if len(c.Args) == 3 {
		if bone, ok = litString(c.Args[2]); !ok {
			return
		}
	} else {
		if model, ok = litString(c.Args[2]); !ok {
			return
		}
		if bone, ok = litString(c.Args[3]); !ok {
			return
		}
	}
	if why := e.BoneProblem(model, bone); why != "" {
		l.add(c.Pos_, "attach would bind to nothing: %s", why)
	}
}

// particleEffects lists the .fpar documents, extension trimmed, since that is
// how spawn_effect names one. Read once per lint, like entityDefs.
func (l *linter) particleEffects() []string {
	if !l.effectsRead {
		files := particle.ScanParticleFiles()
		l.effects = make([]string, 0, len(files))
		for _, f := range files {
			l.effects = append(l.effects, f[:len(f)-len(particle.ParticleExt)])
		}
		l.effectsRead = true
	}
	return l.effects
}

// afterCall flags an `after` written as a plain call. The parser lifts the form
// the language actually has — `@self <- after(500, :open_done:)` — out of the
// send it belongs to, so a call still standing here is one nobody addressed, and
// scheduling a message with no target cannot mean anything.
func (l *linter) afterCall(c *flang.CallExpr) {
	l.add(c.Pos_, "after schedules a message, so it needs a target: `@self <- after(500, :open_done:)`")
}

// hitscan flags the two mistakes a cast can make that are visible without
// running it: answering with an atom this script has no clause for, and naming
// an option that does not exist.
//
// The options are checked only when they are written as a literal dict, which
// is how every call anyone writes spells them — a dict built at runtime is
// checked when the verb runs and cannot be checked before.
func (l *linter) hitscan(c *flang.CallExpr) {
	if len(c.Args) == 0 {
		return
	}
	l.selfAtom(c.Args[0], c.Pos_)
	if len(c.Args) < 2 {
		return
	}
	d, ok := c.Args[1].(*flang.DictLit)
	if !ok {
		return
	}
	for _, k := range d.Keys {
		if _, known := hitscanOptions[k]; known {
			continue
		}
		msg := fmt.Sprintf("hitscan has no option called %q", k)
		if near := world.NearestName(k, HitscanOptionNames()); near != "" {
			msg += fmt.Sprintf("; did you mean %q?", near)
		}
		l.add(d.Pos_, "%s", msg)
	}
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
	if i >= len(c.Args) {
		return nil, false
	}
	return l.entityExpr(c.Args[i])
}

// entityExpr is entityArg for an expression that is not an argument — the base
// of a property write, which is where a reskin is now written.
func (l *linter) entityExpr(e flang.Expr) (*world.Entity, bool) {
	if l.lvl == nil {
		return nil, false
	}
	addr, ok := e.(*flang.AddrExpr)
	if !ok {
		return nil, false
	}
	// @self is whatever the script is attached to and @player only exists during
	// play; neither can be checked from here.
	if addr.Name == "self" || addr.Name == "player" || addr.Name == "level" {
		return nil, false
	}
	r, ok := l.cache.refFor(addr.Name)
	if !ok || r.Kind != flang.RefEntity {
		// A name that resolves to nothing is already reported by addr(), and one
		// that resolves to a volume is reported by the caller's own kind check.
		return nil, false
	}
	ent := l.cache.entityFor(r)
	return ent, ent != nil
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
		r, ok := l.cache.refFor(t.Name)
		return r.Kind, ok
	case *flang.ParamExpr:
		// Whatever the level bound, which is not knowable from here — and a
		// guess would make the property check reject valid code.
		return flang.RefNone, false
	case *flang.MemberExpr:
		// The structural steps that change the kind: a sector's floor and
		// ceiling, and an entity's model attachments. Everything else is a
		// value, or a step that keeps the kind it started with.
		base, ok := l.kindOf(t.X)
		if !ok {
			return flang.RefNone, false
		}
		switch base {
		case flang.RefEntity:
			if t.Name == "model" {
				// The pending half of `@guard.model.<name>`; the name is the
				// next step, which lands in the RefModel case below.
				return flang.RefModel, true
			}
			if t.Name == "geometry" {
				return flang.RefChunk, true
			}
			// One of the entity's own brushes. Without this the property check
			// would run `@ship.hatch.prop.dynamic` against the *entity* table
			// and flag a write that is perfectly good.
			if addr, ok := t.X.(*flang.AddrExpr); ok {
				id := world.EntityBrushID(addr.Name, t.Name)
				if r, ok := l.cache.refFor(id); ok && r.Kind == flang.RefBrush {
					return flang.RefBrush, true
				}
				// Or one of its emitters, for the same reason: without this
				// `@ship.thruster.prop.emitting` would be checked against the
				// entity's table and flagged.
				emID := world.EntityEmitterID(addr.Name, t.Name)
				if r, ok := l.cache.refFor(emID); ok && r.Kind == flang.RefEmitter {
					return flang.RefEmitter, true
				}
				// Or one of its volumes, likewise.
				volID := world.EntityVolumeID(addr.Name, t.Name)
				if r, ok := l.cache.refFor(volID); ok && r.Kind == flang.RefVolume {
					return flang.RefVolume, true
				}
				// Or one of its paths.
				animID := world.EntityWaypointAnimationID(addr.Name, t.Name)
				if r, ok := l.cache.refFor(animID); ok && r.Kind == flang.RefWaypoints {
					return flang.RefWaypoints, true
				}
			}
		case flang.RefModel:
			// Either the attachment name completing that pending ref, or the
			// step back out to the entity. Nothing else follows a model.
			if t.Name == "entity" {
				return flang.RefEntity, true
			}
			return flang.RefModel, true
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
	// Sprites, emitters and sounds were missing from this list, so `@torch`
	// naming a real emitter still produced "did you mean" against a set that
	// could not contain it. Adding sounds without fixing the other two would
	// have left the same bug in place with one more kind in it.
	for i := range l.lvl.Sprites {
		out = append(out, l.lvl.Sprites[i].ID)
	}
	for i := range l.lvl.Emitters {
		out = append(out, l.lvl.Emitters[i].ID)
	}
	for i := range l.lvl.Sounds {
		out = append(out, l.lvl.Sounds[i].ID)
	}
	for i := range l.lvl.Effects {
		out = append(out, l.lvl.Effects[i].ID)
	}
	for i := range l.lvl.WaypointAnimations {
		out = append(out, l.lvl.WaypointAnimations[i].ID)
	}
	for ci := range l.lvl.Chunks {
		ch := &l.lvl.Chunks[ci]
		out = append(out, ch.ID)
		for bi := range ch.Brushes {
			out = append(out, ch.Brushes[bi].ID)
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
