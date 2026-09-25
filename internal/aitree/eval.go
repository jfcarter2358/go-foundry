package aitree

// eval.go — running a compiled tree for one agent.
//
// A run is a walk: start at the root, ask each condition and take the branch it
// answers, do each action and go on to the next, and stop at the first empty
// link. It is a loop rather than a recursion, so a long chain of actions costs
// nothing but iterations, and the step budget is what stops a tree that a
// `next` wired back into itself.
//
// Everything a node reads or writes goes through the flang host and the entity's
// own interpreter. That is not a convenience — it is the reason a tree and a
// script can share an agent at all: the blackboard a `set` writes is literally
// the dict the script reads as `@self.state[...]`, the message a `send` posts
// goes into the same queue and arrives under the same rules, and a `call`
// enters the script the way any other call does.
//
// A run never touches the world directly. Search this file for `world.` and
// there is nothing to find.

import (
	"fmt"

	"foundry/internal/flang"
)

// DefaultMaxSteps is how many nodes one run may visit. Generous for any tree
// anyone would draw, and low enough that a `next` wired into a loop costs a
// frame rather than the session.
const DefaultMaxSteps = 512

// Runner runs a program for one agent.
//
// One of these is built per object per loaded document. It holds no state
// between runs: everything an agent remembers is on its blackboard, where the
// script can see it too.
type Runner struct {
	Prog *Program
	// Interp is the entity's own script interpreter — what a `call` enters,
	// what `?param` reads, and whose random stream a `chance` rolls on. It is
	// shared with the .flang script on the same object, which is what makes the
	// two halves one behaviour rather than two.
	Interp *flang.Interp
	Host   flang.Host
	// Self is the agent. The host's own Self() is the object currently being
	// delivered to, which is this one while a tree runs; this is kept anyway so
	// a Runner can be driven directly by a test.
	Self flang.RefVal
	// MaxSteps overrides DefaultMaxSteps.
	MaxSteps int
}

// TreeError is a failure during a run, placed at the node it happened on.
type TreeError struct {
	Tree, Node, Verb string
	Err              error
}

func (e *TreeError) Error() string {
	return fmt.Sprintf("%s %s (%s): %v", e.Tree, e.Node, e.Verb, e.Err)
}

func (e *TreeError) Unwrap() error { return e.Err }

// Answers reports whether the program has a root for an event.
func (r *Runner) Answers(event string) bool {
	return r != nil && r.Prog != nil && r.Prog.Events[event] != nil
}

// Fire runs the root for an event. handled is false when the document has no
// root for it, which is the ordinary case and not an error — a tree answers the
// handful of things it cares about out of everything the engine posts.
func (r *Runner) Fire(event string, args []flang.Value) (handled bool, err error) {
	if r == nil || r.Prog == nil {
		return false, nil
	}
	t := r.Prog.Events[event]
	if t == nil {
		return false, nil
	}
	if r.Interp != nil {
		// One budget for the whole event, not one per field: a tree that spends
		// it is one doing too much work per message, whichever node it was in.
		r.Interp.ResetSteps()
	}
	run := &run{r: r, max: r.maxSteps()}
	if _, err := run.tree(t, args); err != nil {
		return true, err
	}
	return true, nil
}

// CallHelper runs a helper tree by name and returns what it answered.
func (r *Runner) CallHelper(name string, args []flang.Value) (flang.Value, error) {
	if r == nil || r.Prog == nil {
		return flang.NullVal{}, nil
	}
	t := r.Prog.Helpers[name]
	if t == nil {
		return flang.NullVal{}, fmt.Errorf("no helper tree called %q", name)
	}
	run := &run{r: r, max: r.maxSteps()}
	return run.tree(t, args)
}

func (r *Runner) maxSteps() int {
	if r.MaxSteps > 0 {
		return r.MaxSteps
	}
	return DefaultMaxSteps
}

// run is one walk, and the budget it is spending.
type run struct {
	r     *Runner
	steps int
	max   int
}

// tree binds a tree's arguments and walks it from its root.
func (x *run) tree(t *runTree, args []flang.Value) (flang.Value, error) {
	if t == nil || t.Root == nil {
		return flang.NullVal{}, nil
	}
	env := x.r.Interp.NewChildEnv()
	for i, name := range t.Args {
		var v flang.Value = flang.NullVal{}
		if i < len(args) {
			v = args[i]
		}
		env.Declare(name, v)
	}
	return x.walk(t, t.Root, env)
}

func (x *run) walk(t *runTree, n *runNode, env *flang.Env) (flang.Value, error) {
	for n != nil {
		x.steps++
		if x.steps > x.max {
			return flang.NullVal{}, x.fail(t, n, fmt.Errorf(
				"this run visited %d nodes without stopping; a next or a branch is wired back round into itself", x.max))
		}
		// A node the compiler could not make sense of ends the branch. Running
		// on past it would be acting on a guess about what the author meant.
		if n.Broken {
			return flang.NullVal{}, nil
		}
		switch n.Src.Kind {
		case NodeCondition:
			ok, err := x.test(t, n, env)
			if err != nil {
				return flang.NullVal{}, err
			}
			if ok {
				n = n.Then
			} else {
				n = n.Else
			}
		case NodeAction:
			v, stop, err := x.act(t, n, env)
			if err != nil {
				return flang.NullVal{}, err
			}
			if stop {
				return v, nil
			}
			n = n.Next
		default:
			return flang.NullVal{}, nil
		}
	}
	return flang.NullVal{}, nil
}

func (x *run) fail(t *runTree, n *runNode, err error) error {
	verb := ""
	if n.Verb != nil {
		verb = n.Verb.Name
	}
	return &TreeError{Tree: t.Src.Title(), Node: n.Src.ID, Verb: verb, Err: err}
}

// test answers one condition.
func (x *run) test(t *runTree, n *runNode, env *flang.Env) (bool, error) {
	if n.Verb != nil && n.Verb.Native == "tree" {
		v, err := x.helper(t, n, env)
		if err != nil {
			return false, err
		}
		return flang.Truthy(v), nil
	}
	v, err := x.r.Interp.Eval(n.Expr, env)
	if err != nil {
		return false, x.fail(t, n, err)
	}
	return flang.Truthy(v), nil
}

// act performs one action. stop is true when the run ends here.
func (x *run) act(t *runTree, n *runNode, env *flang.Env) (v flang.Value, stop bool, err error) {
	native := ""
	if n.Verb != nil {
		native = n.Verb.Native
	}
	switch native {
	case "":
		// A templated verb: one expression, evaluated for its effect, and its
		// answer kept when the node asked for it.
		val, e := x.r.Interp.Eval(n.Expr, env)
		if e != nil {
			return nil, false, x.fail(t, n, e)
		}
		x.store(n, val)
		return val, false, nil

	case "stop":
		return flang.NullVal{}, true, nil

	case "result":
		val, e := x.field(n, "value", env, flang.BoolVal(true))
		if e != nil {
			return nil, false, x.fail(t, n, e)
		}
		return val, true, nil

	case "set":
		return x.doSet(t, n, env)

	case "set_attr":
		return x.doSetAttr(t, n, env)

	case "set_prop":
		return x.doSetProp(t, n, env)

	case "send":
		return x.doSend(t, n, env)

	case "call":
		name := n.Src.Do.Field("function")
		args, e := x.args(n, env)
		if e != nil {
			return nil, false, x.fail(t, n, e)
		}
		val, e := x.r.Interp.Call(name, args, flang.Pos{File: n.Src.ID})
		if e != nil {
			return nil, false, x.fail(t, n, e)
		}
		x.store(n, val)
		return val, false, nil

	case "run":
		val, e := x.helper(t, n, env)
		if e != nil {
			return nil, false, e
		}
		x.store(n, val)
		return val, false, nil
	}
	return nil, false, x.fail(t, n, fmt.Errorf("no implementation for %q", native))
}

// helper runs the tree a `run` action or a `tree` check names, inside the same
// budget: a helper called from a helper cannot buy itself more steps.
func (x *run) helper(t *runTree, n *runNode, env *flang.Env) (flang.Value, error) {
	call := n.Src.Do
	if n.Src.Kind == NodeCondition {
		call = n.Src.Check
	}
	h := x.r.Prog.Helpers[call.Field("tree")]
	if h == nil {
		return flang.NullVal{}, x.fail(t, n, fmt.Errorf("no helper tree called %q", call.Field("tree")))
	}
	args, err := x.args(n, env)
	if err != nil {
		return flang.NullVal{}, x.fail(t, n, err)
	}
	inner := x.r.Interp.NewChildEnv()
	for i, name := range h.Args {
		var v flang.Value = flang.NullVal{}
		if i < len(args) {
			v = args[i]
		}
		inner.Declare(name, v)
	}
	return x.walk(h, h.Root, inner)
}

// ---------- the writes ----------

func (x *run) doSet(t *runTree, n *runNode, env *flang.Env) (flang.Value, bool, error) {
	ref, err := x.ref(n, "of", env)
	if err != nil {
		return nil, false, x.fail(t, n, err)
	}
	val, err := x.field(n, "value", env, flang.BoolVal(true))
	if err != nil {
		return nil, false, x.fail(t, n, err)
	}
	key := n.Src.Do.Field("var")
	st := x.r.Host.State(ref)
	if st == nil {
		return nil, false, x.fail(t, n, fmt.Errorf("%s has no state to write", ref))
	}
	if st.M == nil {
		st.M = map[string]flang.Value{}
	}
	out, err := applyOp(st.M[key], n.Src.Do.Field("op"), val)
	if err != nil {
		return nil, false, x.fail(t, n, err)
	}
	st.M[key] = out
	return out, false, nil
}

func (x *run) doSetAttr(t *runTree, n *runNode, env *flang.Env) (flang.Value, bool, error) {
	ref, err := x.ref(n, "of", env)
	if err != nil {
		return nil, false, x.fail(t, n, err)
	}
	val, err := x.field(n, "value", env, flang.NullVal{})
	if err != nil {
		return nil, false, x.fail(t, n, err)
	}
	name := n.Src.Do.Field("attr")
	cur, _ := x.r.Host.Attr(ref, name)
	out, err := applyOp(cur, n.Src.Do.Field("op"), val)
	if err != nil {
		return nil, false, x.fail(t, n, err)
	}
	if err := x.r.Host.SetAttr(ref, name, out); err != nil {
		return nil, false, x.fail(t, n, err)
	}
	return out, false, nil
}

func (x *run) doSetProp(t *runTree, n *runNode, env *flang.Env) (flang.Value, bool, error) {
	ref, err := x.ref(n, "of", env)
	if err != nil {
		return nil, false, x.fail(t, n, err)
	}
	val, err := x.field(n, "value", env, flang.NullVal{})
	if err != nil {
		return nil, false, x.fail(t, n, err)
	}
	if err := x.r.Host.SetProp(ref, n.Src.Do.Field("prop"), val); err != nil {
		return nil, false, x.fail(t, n, err)
	}
	return val, false, nil
}

// doSend posts one message, or fans one out across a tag.
func (x *run) doSend(t *runTree, n *runNode, env *flang.Env) (flang.Value, bool, error) {
	do := n.Src.Do
	args, err := x.args(n, env)
	if err != nil {
		return nil, false, x.fail(t, n, err)
	}
	delay := float64(0)
	if e := n.Fields["after"]; e != nil {
		v, err := x.r.Interp.Eval(e, env)
		if err != nil {
			return nil, false, x.fail(t, n, err)
		}
		delay = asFloat(v)
	}
	post := func(to flang.RefVal) error {
		if delay > 0 {
			return x.r.Host.PostAfter(to, do.Field("event"), args, delay, flang.Pos{File: n.Src.ID})
		}
		return x.r.Host.Post(to, do.Field("event"), args, flang.Pos{File: n.Src.ID})
	}

	if do.Field("tag") != "" {
		tagv, err := x.field(n, "tag", env, flang.StrVal(""))
		if err != nil {
			return nil, false, x.fail(t, n, err)
		}
		tag, ok := tagv.(flang.StrVal)
		if !ok {
			return nil, false, x.fail(t, n, fmt.Errorf("tag is %s, and it has to be text", flang.TypeOf(tagv)))
		}
		here, haveHere := x.position(x.r.Self)
		within := float64(0)
		if e := n.Fields["within"]; e != nil {
			v, err := x.r.Interp.Eval(e, env)
			if err != nil {
				return nil, false, x.fail(t, n, err)
			}
			within = asFloat(v)
		}
		sent := 0
		for _, to := range x.r.Host.ResolveTag(string(tag)) {
			if within > 0 && haveHere {
				p, ok := x.position(to)
				if !ok || p.Sub(here).Len() > within {
					continue
				}
			}
			if e := n.Fields["where"]; e != nil {
				// `it` is the candidate. Declared on a child env so one
				// member's binding cannot leak into the next test, or out into
				// the rest of the tree.
				sub := flang.NewEnv(env)
				sub.Declare("it", to)
				v, err := x.r.Interp.Eval(e, sub)
				if err != nil {
					return nil, false, x.fail(t, n, err)
				}
				if !flang.Truthy(v) {
					continue
				}
			}
			if err := post(to); err != nil {
				return nil, false, x.fail(t, n, err)
			}
			sent++
		}
		return flang.IntVal(sent), false, nil
	}

	to, err := x.ref(n, "to", env)
	if err != nil {
		return nil, false, x.fail(t, n, err)
	}
	if err := post(to); err != nil {
		return nil, false, x.fail(t, n, err)
	}
	return flang.IntVal(1), false, nil
}

// ---------- reading a node's fields ----------

// field evaluates one of a native verb's fields, or returns fallback when the
// author left it blank.
func (x *run) field(n *runNode, name string, env *flang.Env, fallback flang.Value) (flang.Value, error) {
	e := n.Fields[name]
	if e == nil {
		return fallback, nil
	}
	return x.r.Interp.Eval(e, env)
}

// ref evaluates a field that has to name an object.
func (x *run) ref(n *runNode, name string, env *flang.Env) (flang.RefVal, error) {
	v, err := x.field(n, name, env, x.r.Self)
	if err != nil {
		return flang.RefVal{}, err
	}
	r, ok := v.(flang.RefVal)
	if !ok {
		return flang.RefVal{}, fmt.Errorf("%s is %s, and it has to be an object", name, flang.TypeOf(v))
	}
	return r, nil
}

// args evaluates the positional list.
func (x *run) args(n *runNode, env *flang.Env) ([]flang.Value, error) {
	if len(n.Args) == 0 {
		return nil, nil
	}
	out := make([]flang.Value, len(n.Args))
	for i, e := range n.Args {
		v, err := x.r.Interp.Eval(e, env)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// store puts an action's answer on the blackboard, when the node asked for it.
func (x *run) store(n *runNode, v flang.Value) {
	if n.Into == "" {
		return
	}
	st := x.r.Host.State(x.r.Self)
	if st == nil {
		return
	}
	if st.M == nil {
		st.M = map[string]flang.Value{}
	}
	st.M[n.Into] = v
}

// position is where something is, for a fan-out's radius.
func (x *run) position(r flang.RefVal) (flang.VecVal, bool) {
	v, ok := x.r.Host.Prop(r, "position")
	if !ok {
		return flang.VecVal{}, false
	}
	p, ok := v.(flang.VecVal)
	return p, ok
}

// ---------- values ----------

// applyOp is what a `set` does to what is already there.
//
// A compound op on a key that has never been written treats the absent value as
// zero, which is what makes a counter work without having to be initialised
// first: `set dash += 1` on a fresh agent is 1.
func applyOp(cur flang.Value, op string, v flang.Value) (flang.Value, error) {
	switch op {
	case "", "=":
		return v, nil
	}
	if cur == nil {
		cur = flang.NullVal{}
	}
	a, aok := number(cur)
	b, bok := number(v)
	if !aok || !bok {
		if op == "+=" {
			// Strings and lists concatenate with `+` in the language, and a
			// blackboard key holding either is a reasonable thing to append to.
			if as, ok := cur.(flang.StrVal); ok {
				return flang.StrVal(string(as) + v.String()), nil
			}
		}
		return nil, fmt.Errorf("%s needs numbers; this is %s and %s", op, flang.TypeOf(cur), flang.TypeOf(v))
	}
	var out float64
	switch op {
	case "+=":
		out = a + b
	case "-=":
		out = a - b
	case "*=":
		out = a * b
	case "/=":
		if b == 0 {
			return nil, fmt.Errorf("dividing by zero")
		}
		out = a / b
	default:
		return nil, fmt.Errorf("%q is not an assignment", op)
	}
	// An int op an int stays an int, the way the language's own arithmetic
	// does: a counter must not turn into 3.0 the first time it is added to.
	_, ai := cur.(flang.IntVal)
	_, bi := v.(flang.IntVal)
	if (ai || isNull(cur)) && bi && op != "/=" {
		return flang.IntVal(int64(out)), nil
	}
	return flang.FloatVal(out), nil
}

func isNull(v flang.Value) bool {
	_, ok := v.(flang.NullVal)
	return ok || v == nil
}

// number reads a value as a float, counting an unset key as zero.
func number(v flang.Value) (float64, bool) {
	switch t := v.(type) {
	case flang.IntVal:
		return float64(t), true
	case flang.FloatVal:
		return float64(t), true
	case flang.NullVal:
		return 0, true
	case nil:
		return 0, true
	}
	return 0, false
}

// asFloat reads a number, or zero.
func asFloat(v flang.Value) float64 {
	f, _ := number(v)
	return f
}
