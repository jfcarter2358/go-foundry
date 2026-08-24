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

import "strings"

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
}

// evalAddr resolves `@id`.
//
// `@self` and `@level` are resolved by the host like any other name, so a test
// host can define them and the language needs no special cases.
func (in *Interp) evalAddr(x *AddrExpr, env *Env) (Value, error) {
	if in.host == nil {
		return nil, in.errorf(x.Pos_, "@%s: there is no world here", x.Name)
	}
	if x.Name == "self" {
		self := in.host.Self()
		if self.IsNone() {
			return nil, in.errorf(x.Pos_, "@self: nothing is running this script")
		}
		return self, nil
	}
	if decl, ok := in.params[x.Name]; ok {
		return in.paramValue(decl, x.Pos_)
	}
	r, ok := in.host.Resolve(x.Name)
	if !ok {
		// A missing object is null rather than an error: a script that checks
		// `if @door == null` is doing the right thing, and making the check
		// itself throw would leave no way to write it.
		return NullVal{}, nil
	}
	return r, nil
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
			return in.readAccess(base, mode, x.Name, x.Pos_)
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
	v, err := in.evalExpr(e, env)
	if err != nil {
		return pathBase{}, err
	}
	switch t := v.(type) {
	case RefVal:
		return pathBase{refs: []RefVal{t}, single: true}, nil
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
				return pathBase{}, in.errorf(e.Position(), "a list used as an object path holds refs, not %s",
					typeName(el))
			}
			refs = append(refs, r)
		}
		return pathBase{refs: refs}, nil
	}
	return pathBase{}, in.errorf(e.Position(), "%s is not an object", typeName(v))
}

// readAccess reads an attribute or a property.
//
// Reading through a tag is deliberately not supported: `#lamps.attr.brightness`
// would have to be a list, and then `#lamps.attr.brightness + 1` would silently
// mean something no one intended. Writing through a tag is the useful direction,
// and that is what SCRIPTING.md describes.
func (in *Interp) readAccess(base pathBase, mode accessMode, name string, pos Pos) (Value, error) {
	if in.host == nil {
		return nil, in.errorf(pos, "there is no world here")
	}
	if !base.single {
		return nil, in.errorf(pos,
			"a tag can be written through but not read from; read one object at a time, or loop over #tag")
	}
	if len(base.refs) == 0 {
		return NullVal{}, nil
	}
	r := base.refs[0]

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
		v, ok := in.host.Prop(r, name)
		if !ok {
			return nil, in.errorf(pos, "%s has no property %q", r, name)
		}
		return v, nil
	}
}

// readStructural reads a step that is part of an object's shape rather than its
// data: `ref.id`, `ref.kind`, `@sec.floor`, `@sec.edge`.
func (in *Interp) readStructural(recv Value, name string, pos Pos) (Value, error) {
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
		return AtomVal(r.Kind.String()), nil
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
	if base.single && len(base.refs) == 0 {
		return in.errorf(x.Pos_, "cannot write to an object that does not exist")
	}

	// Writing through a tag reaches every member. One that rejects the write
	// does not stop the others: a broadcast where a single incompatible object
	// aborted the rest would be far harder to reason about than one that reports
	// what it could not do.
	var firstErr error
	for _, r := range base.refs {
		var werr error
		switch mode {
		case modeAttr:
			werr = in.host.SetAttr(r, x.Name, val)
		default: // modeProp
			werr = in.host.SetProp(r, x.Name, val)
		}
		if werr != nil && firstErr == nil {
			firstErr = in.errorf(x.Pos_, "%s", werr.Error())
		}
	}
	return firstErr
}

// ---------- sending ----------

// execSend posts a message. It does not deliver it: `<-` is asynchronous, and
// the runtime drains the queue — see SCRIPTING.md's execution model.
func (in *Interp) execSend(st *SendStmt, env *Env) error {
	if in.host == nil {
		return in.errorf(st.Pos_, "there is no world here to send to")
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
		if err := in.host.Post(r, string(atom), args, st.Pos_); err != nil {
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
