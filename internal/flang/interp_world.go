package flang

// interp_world.go — the parts of evaluation that reach the world.
//
// Split from interp.go so the boundary is visible: everything here goes through
// Host and nothing else does. With a nil host the language is still complete for
// anything that does not mention an object, which is what lets the arithmetic,
// the dispatcher and the control flow all be tested with no engine at all.
//
// The member chain is the fiddly part. `@self.attr.health` parses as
// MemberExpr{MemberExpr{AddrExpr}}, and what `.health` means depends on `.attr`
// having come before it. Rather than special-casing during parsing — where the
// information is not available — the chain is classified here: a `.attr` or
// `.prop` step puts the walk into a mode, and the next step is read in that
// mode.
//
// `.state` is deliberately *not* a mode. It yields the scratch dict itself, so
// `@self.state["open"]` is an ordinary index and assigning to it an ordinary
// index assignment. Treating it as a mode would have spelled it `.state.open`,
// which is neither what SCRIPTING.md writes nor what a dict wants.

import (
	"sort"
	"strings"
)

// accessMode is what a member step after `.attr` or `.prop` means.
type accessMode int

const (
	modeNone accessMode = iota
	modeAttr
	modeProp
)

// pathBase describes what a member chain is rooted at: one object, or every
// object carrying a tag.
type pathBase struct {
	refs   []RefVal
	single bool // true when the chain began at @id rather than #tag
	// one holds a single object inline, and hasOne says so — rather than a
	// one-element slice, which `@self.attr.x` allocated on every read.
	one    RefVal
	hasOne bool
}

// count is how many objects the path reaches.
func (b *pathBase) count() int {
	if b.hasOne {
		return 1
	}
	return len(b.refs)
}

// at is the i-th of them.
func (b *pathBase) at(i int) RefVal {
	if b.hasOne {
		return b.one
	}
	return b.refs[i]
}

// evalAddr resolves `@id`.
//
// `@self` and `@level` are resolved by the host like any other name, so a test
// host can define them and the language needs no special cases.
func (in *Interp) evalAddr(x *AddrExpr, env *Env) (Value, error) {
	r, found, err := in.addrRef(x)
	if err != nil {
		return nil, err
	}
	if !found {
		return NullVal{}, nil
	}
	return in.boxRef(r), nil
}

// addrRef is evalAddr without the boxing: the ref `@id` names, or found=false
// for a name the level does not have (which evaluates to null).
//
// Kept apart because a RefVal is four words, and converting one to a Value
// allocates: `@self.attr.x` used to box @self only for the member chain to
// unbox it again, on every read.
func (in *Interp) addrRef(x *AddrExpr) (RefVal, bool, error) {
	if in.host == nil {
		return RefVal{}, false, in.errorf(x.Pos_, "@%s: there is no world here", x.Name)
	}
	if x.Name == "self" {
		self := in.host.Self()
		if self.IsNone() {
			return RefVal{}, false, in.errorf(x.Pos_, "@self: nothing is running this script")
		}
		return self, true, nil
	}
	r, ok := in.host.Resolve(x.Name)
	if !ok {
		if _, isParam := in.params[x.Name]; isParam {
			// The sigils used to be shared, so this is the one shape worth a
			// message rather than a null: the name is declared, just not with
			// the sigil that reads it. An object of the same name still wins
			// above, because `@` asks the level for an object and there is one.
			return RefVal{}, false, in.errorf(x.Pos_, "@%s names an object, and nothing in the level is called %q; %s is a script parameter, so write ?%s",
				x.Name, x.Name, x.Name, x.Name)
		}
		// A missing object is null rather than an error: a script that checks
		// `if @door == null` is doing the right thing, and making the check
		// itself throw would leave no way to write it.
		return RefVal{}, false, nil
	}
	return r, true, nil
}

// boxRef is r as a Value, reusing the last one boxed when it is the same ref
// — a handler names @self over and over, and each conversion allocated. A ref
// is a value, so one boxed copy serves every use of it.
func (in *Interp) boxRef(r RefVal) Value {
	if in.refBoxed != nil && in.refBoxKey == r {
		return in.refBoxed
	}
	v := Value(r)
	in.refBoxKey, in.refBoxed = r, v
	return v
}

// evalParam resolves `?name` — one declared script parameter.
//
// A name with no declaration is an error rather than a null, because unlike an
// object there is nothing else it could be: params are declared in this program
// and the set is closed at link time.
func (in *Interp) evalParam(x *ParamExpr, env *Env) (Value, error) {
	decl, ok := in.params[x.Name]
	if !ok {
		if names := in.paramNames(); len(names) > 0 {
			return nil, in.errorf(x.Pos_, "?%s: no parameter is declared with that name; this script has %s",
				x.Name, strings.Join(names, ", "))
		}
		return nil, in.errorf(x.Pos_, "?%s: this script declares no parameters; add `param %s <type>` at the top level",
			x.Name, x.Name)
	}
	if in.host == nil {
		return nil, in.errorf(x.Pos_, "?%s: there is no world here", x.Name)
	}
	return in.paramValue(decl, x.Pos_)
}

// paramNames lists the declared parameters, for the message a misspelt `?name`
// gets. Sorted so the list does not depend on map order.
func (in *Interp) paramNames() []string {
	out := make([]string, 0, len(in.params))
	for n := range in.params {
		out = append(out, "?"+n)
	}
	sort.Strings(out)
	return out
}

// paramValue reads one declared parameter: what the level bound, else the
// declaration's default, else null.
//
// A `ref` param is stored as the ID of the object it names, because that is what
// a level file can write and what survives an undo — the same reasoning as
// script_ref.go's. Resolving it here rather than at bind time means a param
// pointing at an object that does not exist *yet* starts working the moment it
// does, instead of being frozen wrong at load.
func (in *Interp) paramValue(decl *ParamDecl, pos Pos) (Value, error) {
	v, bound := in.host.Param(decl.Name)
	if !bound || v == nil || v.Type() == TypeNull {
		d, ok := decl.DefaultValue()
		if !ok {
			return NullVal{}, nil
		}
		v = d
	}
	if decl.Type != "ref" {
		if !TypeMatches(v, decl.Type) {
			// The editor writes params through a typed widget, so a mismatch
			// means the level was hand-edited or the declaration's type changed
			// under a level that had already bound one. Either way the author
			// wants to hear about it rather than get a silent zero.
			return nil, in.errorf(pos, "param %s is declared %s but the level bound a %s",
				decl.Name, decl.Type, TypeOf(v))
		}
		return v, nil
	}
	switch t := v.(type) {
	case RefVal:
		return t, nil
	case StrVal:
		if string(t) == "" {
			// Nothing was chosen, which is null rather than a lookup for "".
			return NullVal{}, nil
		}
		r, ok := in.host.Resolve(string(t))
		if !ok {
			return NullVal{}, nil
		}
		return r, nil
	}
	return nil, in.errorf(pos, "param %s is declared ref but the level bound a %s",
		decl.Name, TypeOf(v))
}

// evalTag resolves `#tag` to a list of refs.
func (in *Interp) evalTag(x *TagExpr, env *Env) (Value, error) {
	if in.host == nil {
		return nil, in.errorf(x.Pos_, "#%s: there is no world here", x.Name)
	}
	refs := in.host.ResolveTag(x.Name)
	elems := make([]Value, len(refs))
	for i, r := range refs {
		elems[i] = r
	}
	return NewList(elems...), nil
}

// evalMember reads one step of a member chain.
func (in *Interp) evalMember(x *MemberExpr, env *Env) (Value, error) {
	// A mode word on its own is not a value: `@self.attr` has to be followed by
	// a name. Catching it here gives a message about the missing name rather
	// than one about a property called "attr".
	if isModeWord(x.Name) {
		if _, err := in.evalExpr(x.X, env); err != nil {
			return nil, err
		}
		return nil, in.errorf(x.Pos_, "`.%s` needs a name after it, like .%s.health", x.Name, x.Name)
	}

	// Does an access mode apply? That is decided by the step *before* this one.
	if inner, ok := x.X.(*MemberExpr); ok {
		if mode := modeOf(inner.Name); mode != modeNone {
			base, err := in.evalPathBase(inner.X, env)
			if err != nil {
				return nil, err
			}
			return in.readAccess(base, mode, x, x.Pos_)
		}
	}

	// An ordinary structural step: `@sec.floor`, `ref.id`, `ref.kind`.
	recv, err := in.evalExpr(x.X, env)
	if err != nil {
		return nil, err
	}
	return in.readStructural(recv, x.Name, x.Pos_)
}

// isModeWord reports whether a member name is one of the access modes.
func isModeWord(name string) bool { return modeOf(name) != modeNone }

func modeOf(name string) accessMode {
	switch name {
	case "attr":
		return modeAttr
	case "prop":
		return modeProp
	}
	return modeNone
}

// evalPathBase evaluates the expression a `.attr` or `.prop` chain hangs off,
// which is either one object or a tag's worth of them.
func (in *Interp) evalPathBase(e Expr, env *Env) (pathBase, error) {
	if tag, ok := e.(*TagExpr); ok {
		if in.host == nil {
			return pathBase{}, in.errorf(tag.Pos_, "#%s: there is no world here", tag.Name)
		}
		return pathBase{refs: in.host.ResolveTag(tag.Name)}, nil
	}
	if addr, ok := e.(*AddrExpr); ok {
		return in.addrPathBase(addr)
	}
	v, err := in.evalExpr(e, env)
	if err != nil {
		return pathBase{}, err
	}
	return in.pathBaseFrom(v, e.Position())
}

// addrPathBase is evalPathBase for an `@id` base, which has no need of a Value.
func (in *Interp) addrPathBase(x *AddrExpr) (pathBase, error) {
	r, found, err := in.addrRef(x)
	if err != nil {
		return pathBase{}, err
	}
	if !found {
		// Reading through a missing object yields null, as a NullVal base does.
		return pathBase{single: true}, nil
	}
	return pathBase{one: r, hasOne: true, single: true}, nil
}

// pathBaseFrom is evalPathBase for a base already evaluated, pos being where
// its expression is.
func (in *Interp) pathBaseFrom(v Value, pos Pos) (pathBase, error) {
	switch t := v.(type) {
	case RefVal:
		return pathBase{one: t, hasOne: true, single: true}, nil
	case NullVal:
		// Reading through a missing object yields null; writing through one is
		// caught by the caller, which has a position to complain at.
		return pathBase{single: true}, nil
	case *ListVal:
		// A list of refs, which is what `#tag` produced — so `find("x")` and a
		// literal list both work as a base.
		refs := make([]RefVal, 0, len(t.Elems))
		for _, el := range t.Elems {
			r, ok := el.(RefVal)
			if !ok {
				return pathBase{}, in.errorf(pos, "a list used as an object path holds refs, not %s",
					typeName(el))
			}
			refs = append(refs, r)
		}
		return pathBase{refs: refs}, nil
	}
	return pathBase{}, in.errorf(pos, "%s is not an object", typeName(v))
}

// readAccess reads an attribute or a property.
//
// Reading through a tag is deliberately not supported: `#lamps.attr.brightness`
// would have to be a list, and then `#lamps.attr.brightness + 1` would silently
// mean something no one intended. Writing through a tag is the useful direction,
// and that is what SCRIPTING.md describes.
func (in *Interp) readAccess(base pathBase, mode accessMode, x *MemberExpr, pos Pos) (Value, error) {
	name := x.Name
	if in.host == nil {
		return nil, in.errorf(pos, "there is no world here")
	}
	if !base.single {
		return nil, in.errorf(pos,
			"a tag can be written through but not read from; read one object at a time, or loop over #tag")
	}
	if base.count() == 0 {
		return NullVal{}, nil
	}
	r := base.at(0)

	switch mode {
	case modeAttr:
		v, ok := in.host.Attr(r, name)
		if !ok {
			// An absent attribute is null: a script can test for one without
			// having to have set it first.
			return NullVal{}, nil
		}
		return v, nil

	default: // modeProp
		var v Value
		var ok bool
		if h := in.propHandle(x, r.Kind); h != nil {
			v, ok = h.GetProp(r)
		} else {
			v, ok = in.host.Prop(r, name)
		}
		if !ok {
			return nil, in.errorf(pos, "%s has no property %q", r, name)
		}
		return v, nil
	}
}

// propSite is a member expression's memory of the host's handle for the
// property it names: for which interpreter, and for which kind of object. A
// `.prop.x` step reaches objects of one kind nearly always; one that alternates
// between kinds resolves again on each change, which costs what every access
// used to.
type propSite struct {
	owner *Interp
	kind  Kind_
	h     PropHandle
}

// propHandle returns the host's handle for x's property on objects of this
// kind, or nil when the host has none to give.
func (in *Interp) propHandle(x *MemberExpr, kind Kind_) PropHandle {
	site := &x.prop
	if site.owner == in && site.kind == kind {
		return site.h
	}
	var h PropHandle
	if pr, ok := in.host.(PropResolver); ok {
		h = pr.PropHandle(kind, x.Name)
	}
	*site = propSite{owner: in, kind: kind, h: h}
	return h
}

// refKindAtoms are `ref.kind`'s answers, boxed once.
var refKindAtoms = func() [256]Value {
	var out [256]Value
	for i := range out {
		out[i] = AtomVal(Kind_(i).String())
	}
	return out
}()

// readStructural reads a step that is part of an object's shape rather than its
// data: `ref.id`, `ref.kind`, `@sec.floor`, `@sec.edge`, `v.x`.
func (in *Interp) readStructural(recv Value, name string, pos Pos) (Value, error) {
	if v, ok := recv.(VecVal); ok {
		return vecComponent(in, v, name, pos)
	}
	r, ok := recv.(RefVal)
	if !ok {
		if _, isNull := recv.(NullVal); isNull {
			return NullVal{}, nil
		}
		return nil, in.errorf(pos, "%s has no member %q", typeName(recv), name)
	}
	// Members every ref answers, whatever it points at.
	switch name {
	case "id":
		return StrVal(r.ID()), nil
	case "kind":
		return refKindAtoms[r.Kind], nil
	case "state":
		// The scratch dict itself, so `@self.state["open"]` is an ordinary
		// index and `@self.state["open"] = v` an ordinary index assignment.
		// Making it a mode like `.attr` would have meant `.state.open`, which
		// is not what SCRIPTING.md writes and not what a dict wants.
		if in.host == nil {
			return nil, in.errorf(pos, "there is no world here")
		}
		return in.host.State(r), nil
	}
	if in.host == nil {
		return nil, in.errorf(pos, "there is no world here")
	}
	v, ok := in.host.Member(r, name)
	if !ok {
		return nil, in.errorf(pos, "%s has no member %q", r, name)
	}
	return v, nil
}

// assignMember writes through a member chain.
func (in *Interp) assignMember(x *MemberExpr, val Value, env *Env) error {
	inner, ok := x.X.(*MemberExpr)
	if !ok || modeOf(inner.Name) == modeNone {
		if err := in.assignVecComponent(x, env); err != nil {
			return err
		}
		return in.errorf(x.Pos_,
			"only attributes and properties can be assigned; write .attr.%s, .prop.%s, or .state[%q]",
			x.Name, x.Name, x.Name)
	}
	mode := modeOf(inner.Name)

	base, err := in.evalPathBase(inner.X, env)
	if err != nil {
		return err
	}
	if in.host == nil {
		return in.errorf(x.Pos_, "there is no world here")
	}
	if base.single && base.count() == 0 {
		return in.errorf(x.Pos_, "cannot write to an object that does not exist")
	}

	// Writing through a tag reaches every member. One that rejects the write
	// does not stop the others: a broadcast where a single incompatible object
	// aborted the rest would be far harder to reason about than one that reports
	// what it could not do.
	var firstErr error
	for i, n := 0, base.count(); i < n; i++ {
		r := base.at(i)
		var werr error
		switch mode {
		case modeAttr:
			werr = in.host.SetAttr(r, x.Name, val)
		default: // modeProp
			if h := in.propHandle(x, r.Kind); h != nil {
				werr = h.SetProp(r, val)
			} else {
				werr = in.host.SetProp(r, x.Name, val)
			}
		}
		if werr != nil && firstErr == nil {
			firstErr = in.errorf(x.Pos_, "%s", werr.Error())
		}
	}
	return firstErr
}

// assignVecComponent returns the error for `v.y = 1.0`, or nil when the target
// is not a vec component and the caller should report its own.
//
// A vec is a value: the one a property hands back is a copy, so a write to a
// component of it would land on the copy and change nothing on the object. That
// is the failure worth catching by name — the generic "only attributes and
// properties can be assigned" would send the author looking for a property
// called `y`.
func (in *Interp) assignVecComponent(x *MemberExpr, env *Env) error {
	if vecAxis(x.Name) < 0 {
		return nil
	}
	// The base is re-evaluated only to classify the error, and only on a path
	// that is already failing.
	v, err := in.evalExpr(x.X, env)
	if err != nil {
		return nil
	}
	if _, isVec := v.(VecVal); !isVec {
		return nil
	}
	return in.errorf(x.Pos_,
		"a vec component cannot be assigned — a vec is a value, so the one you have is a copy. "+
			"Build a new one instead: v = <v.x, 1.0, v.z>")
}

// ---------- sending ----------

// execSend posts a message. It does not deliver it: `<-` is asynchronous, and
// the runtime drains the queue — see SCRIPTING.md's execution model.
//
// A send carrying a delay is posted on a timer instead, which is the same
// asynchrony with a longer wait in it.
func (in *Interp) execSend(st *SendStmt, env *Env) error {
	if in.host == nil {
		return in.errorf(st.Pos_, "there is no world here to send to")
	}

	// The delay is written first inside `after(...)`, so it is evaluated first.
	delayMS := 0.0
	if st.Delay != nil {
		d, err := in.evalExpr(st.Delay, env)
		if err != nil {
			return err
		}
		f, ok := numeric(d)
		if !ok {
			return in.errorf(st.Delay.Position(),
				"after's delay is a number of milliseconds, not %s", typeName(d))
		}
		delayMS = f
	}

	msg, err := in.evalExpr(st.Msg, env)
	if err != nil {
		return err
	}
	tup, ok := msg.(TupleVal)
	if !ok || len(tup.Elems) == 0 {
		return in.errorf(st.Pos_, "a message is a tuple beginning with an atom")
	}
	atom, ok := tup.Elems[0].(AtomVal)
	if !ok {
		return in.errorf(st.Pos_, "a message begins with an atom, not %s", typeName(tup.Elems[0]))
	}
	args := tup.Elems[1:]

	targets, err := in.sendTargets(st.Target, env)
	if err != nil {
		return err
	}
	for _, r := range targets {
		var err error
		if st.Delay != nil {
			err = in.host.PostAfter(r, string(atom), args, delayMS, st.Pos_)
		} else {
			err = in.host.Post(r, string(atom), args, st.Pos_)
		}
		if err != nil {
			return in.errorf(st.Pos_, "%s", err.Error())
		}
	}
	return nil
}

// sendTargets resolves what a send is addressed to: one object, or a tag's
// worth, snapshotted now.
//
// The snapshot matters: an object that gains the tag inside a handler must not
// receive the message already in flight, or a handler that tags things would
// deliver to a set that changed underneath it.
func (in *Interp) sendTargets(e Expr, env *Env) ([]RefVal, error) {
	if tag, ok := e.(*TagExpr); ok {
		return in.host.ResolveTag(tag.Name), nil
	}
	v, err := in.evalExpr(e, env)
	if err != nil {
		return nil, err
	}
	switch t := v.(type) {
	case RefVal:
		return []RefVal{t}, nil
	case NullVal:
		// Sending to nothing is dropped rather than fatal: an object may
		// legitimately have been destroyed since the sender last looked.
		return nil, nil
	case *ListVal:
		refs := make([]RefVal, 0, len(t.Elems))
		for _, el := range t.Elems {
			r, ok := el.(RefVal)
			if !ok {
				return nil, in.errorf(e.Position(), "cannot send to %s", typeName(el))
			}
			refs = append(refs, r)
		}
		return refs, nil
	}
	return nil, in.errorf(e.Position(), "cannot send to %s", typeName(v))
}

// ---------- host-backed builtins ----------

// callAction forwards a world verb to the host, so builtin.go can stay free of
// engine concerns.
func (in *Interp) callAction(name string, args []Value, pos Pos) (Value, error) {
	if in.host == nil {
		return nil, in.errorf(pos, "%s needs a world to act on", name)
	}
	v, ok, err := in.host.Action(name, args, pos)
	if err != nil {
		return nil, in.errorf(pos, "%s", err.Error())
	}
	if !ok {
		return nil, in.errorf(pos, "this engine does not provide %q", name)
	}
	if v == nil {
		return NullVal{}, nil
	}
	return v, nil
}

// logLine sends text to whichever sink is configured, preferring an explicit
// output override so a test can capture it without a host.
func (in *Interp) logLine(parts []string) {
	msg := strings.Join(parts, " ")
	if in.out != nil {
		in.out(msg)
		return
	}
	if in.host != nil {
		in.host.Log(msg)
	}
}
