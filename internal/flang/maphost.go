package flang

// maphost.go — a Host backed by maps.
//
// This exists so the whole language can be exercised with no engine: it is what
// the tests run against, and it is the working proof that the Host boundary is
// real. If something in the interpreter cannot be tested against MapHost, that
// is a sign an engine concept has leaked into the language.
//
// It is also the reference implementation. Anyone writing a second host — a
// different game, a command-line runner — has a short, complete example of what
// each method is supposed to do.

import "fmt"

// MapObject is one object in a MapHost.
type MapObject struct {
	Ref   RefVal
	Tags  []string
	Attrs map[string]Value
	Props map[string]Value
	state *DictVal
}

// MapHost is an in-memory world.
type MapHost struct {
	Objects map[string]*MapObject
	self    RefVal

	// Posted records every message sent, in order, instead of delivering it.
	// Delivery is the runtime's job (Phase 6); until then this is what a test
	// asserts against.
	Posted []PostedMsg

	// Actions records world verbs the script called, so a test can check that
	// `spawn` was reached without needing a world to spawn into.
	Actions []ActionCall

	// Verbs are the world verbs this host implements. A verb not listed reports
	// "this engine does not provide …", which is the honest answer.
	Verbs map[string]func(args []Value, pos Pos) (Value, error)

	// Logged collects everything `log` printed.
	Logged []string

	// Params answer the script's `param` declarations. One map for the whole
	// host rather than one per object: MapHost only ever has one script
	// running, and SetSelf is how a test changes which object that is.
	Params map[string]Value
}

// PostedMsg is one queued message. DelayMS is zero for an immediate send and
// the timer's delay for one sent through `after`.
type PostedMsg struct {
	To      RefVal
	Atom    string
	Args    []Value
	DelayMS float64
	Pos     Pos
}

// ActionCall is one world verb invocation.
type ActionCall struct {
	Name string
	Args []Value
}

// NewMapHost returns an empty world.
func NewMapHost() *MapHost {
	return &MapHost{
		Objects: map[string]*MapObject{},
		Verbs:   map[string]func([]Value, Pos) (Value, error){},
		Params:  map[string]Value{},
	}
}

// AddObject creates an object and returns it.
func (h *MapHost) AddObject(id string, kind Kind_, tags ...string) *MapObject {
	o := &MapObject{
		Ref:   RefVal{Kind: kind, A: id},
		Tags:  append([]string(nil), tags...),
		Attrs: map[string]Value{},
		Props: map[string]Value{},
		state: NewDict(),
	}
	h.Objects[id] = o
	return o
}

// SetSelf makes id the object whose script is running.
func (h *MapHost) SetSelf(id string) {
	if o, ok := h.Objects[id]; ok {
		h.self = o.Ref
		return
	}
	h.self = RefVal{}
}

func (h *MapHost) Resolve(id string) (RefVal, bool) {
	o, ok := h.Objects[id]
	if !ok {
		return RefVal{}, false
	}
	return o.Ref, true
}

// ResolveTag returns matching objects in a stable order. Go's map iteration is
// randomised, and a broadcast whose order changed between runs would make script
// behaviour irreproducible — which is the one thing the single-queue design
// exists to prevent.
func (h *MapHost) ResolveTag(tag string) []RefVal {
	var ids []string
	for id, o := range h.Objects {
		for _, t := range o.Tags {
			if t == tag {
				ids = append(ids, id)
				break
			}
		}
	}
	sortStrings(ids)
	out := make([]RefVal, len(ids))
	for i, id := range ids {
		out[i] = h.Objects[id].Ref
	}
	return out
}

func (h *MapHost) Self() RefVal { return h.self }

func (h *MapHost) Alive(r RefVal) bool {
	_, ok := h.Objects[r.ID()]
	return ok
}

func (h *MapHost) object(r RefVal) *MapObject { return h.Objects[r.ID()] }

func (h *MapHost) Attr(r RefVal, name string) (Value, bool) {
	o := h.object(r)
	if o == nil {
		return nil, false
	}
	v, ok := o.Attrs[name]
	return v, ok
}

func (h *MapHost) SetAttr(r RefVal, name string, v Value) error {
	o := h.object(r)
	if o == nil {
		return fmt.Errorf("%s no longer exists", r)
	}
	o.Attrs[name] = v
	return nil
}

func (h *MapHost) Prop(r RefVal, name string) (Value, bool) {
	o := h.object(r)
	if o == nil {
		return nil, false
	}
	v, ok := o.Props[name]
	return v, ok
}

// SetProp refuses an unknown name. Unlike an attribute, the property set is
// fixed and known, so a name outside it is a typo rather than a new field.
func (h *MapHost) SetProp(r RefVal, name string, v Value) error {
	o := h.object(r)
	if o == nil {
		return fmt.Errorf("%s no longer exists", r)
	}
	if _, known := o.Props[name]; !known {
		return fmt.Errorf("%s has no property %q", r, name)
	}
	o.Props[name] = v
	return nil
}

func (h *MapHost) State(r RefVal) *DictVal {
	o := h.object(r)
	if o == nil {
		// A dangling ref gets a throwaway dict rather than a panic: the write
		// goes nowhere, which is the right outcome for an object that is gone.
		return NewDict()
	}
	if o.state == nil {
		o.state = NewDict()
	}
	return o.state
}

// Param returns what the host has bound to a script parameter. Absent means
// unbound, which the interpreter reads as the declaration's default.
func (h *MapHost) Param(name string) (Value, bool) {
	v, ok := h.Params[name]
	return v, ok
}

// SetParam binds one script parameter. A ref param takes either a RefVal or the
// StrVal naming the object, matching what the real host may return.
func (h *MapHost) SetParam(name string, v Value) {
	if h.Params == nil {
		h.Params = map[string]Value{}
	}
	h.Params[name] = v
}

func (h *MapHost) Member(r RefVal, name string) (Value, bool) { return nil, false }

func (h *MapHost) Post(to RefVal, atom string, args []Value, pos Pos) error {
	h.Posted = append(h.Posted, PostedMsg{To: to, Atom: atom, Args: args, Pos: pos})
	return nil
}

// PostAfter records the delay and nothing else: there is no clock here, so a
// test asserts that the timer was set rather than waiting for it to fire.
func (h *MapHost) PostAfter(to RefVal, atom string, args []Value, delayMS float64, pos Pos) error {
	h.Posted = append(h.Posted, PostedMsg{To: to, Atom: atom, Args: args, DelayMS: delayMS, Pos: pos})
	return nil
}

func (h *MapHost) Action(name string, args []Value, pos Pos) (Value, bool, error) {
	h.Actions = append(h.Actions, ActionCall{Name: name, Args: args})
	fn, ok := h.Verbs[name]
	if !ok {
		return nil, false, nil
	}
	v, err := fn(args, pos)
	return v, true, err
}

func (h *MapHost) Log(msg string) { h.Logged = append(h.Logged, msg) }
