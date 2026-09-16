package flang

// builtin.go — the standard library.
//
// Two halves. The pure builtins — maths, strings, lists, vectors — are
// implemented here and need no world, which is what lets them be tested without
// one. The world verbs — play_anim, spawn, destroy — are forwarded to the host,
// because what they mean is the engine's business and not the language's.
//
// The table is a map rather than a switch for the same reason the property
// tables are: three separate things want to *enumerate* it — the editor's
// completion list, the reference panel, and the "no function named X, did you
// mean Y" message — and a switch cannot be enumerated.
//
// A script's own definitions shadow these (see evalCall), so a game that wants
// its own `distance` is not fighting the engine for the name.

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// builtinFn is one library function.
type builtinFn func(in *Interp, args []Value, pos Pos) (Value, error)

// worldVerbs are the builtins the host implements. They are listed here so the
// language knows the names exist — a missing implementation should say "this
// engine does not provide play_anim", not "no function named play_anim".
var worldVerbs = []string{
	"player", "find",
	"play_anim", "stop_anim", "spawn", "destroy", "time",
	"mount", "dismount", "ease_sky",
	"emit", "stop_emit", "burst", "spawn_effect", "explode",
	"hitscan", "bone_position",
	"play", "stop", "play_sound", "play_sound_at", "play_music", "stop_music",
	"attach", "detach",
	"play_waypoints", "stop_waypoints",
	"move_to", "rotate_to", "scale_to",
	"ai_move_to", "ai_stop", "ai_target", "ai_objective", "find_path", "can_see",
	"ai_follow_waypoints", "ai_continue", "ai_follow",
	"show_screen", "hide_screen", "screen", "widget", "focus", "load_level",
	"quit_game",
	"bind_key", "binding_name", "reset_bindings",
}

var builtins map[string]builtinFn

func init() {
	builtins = map[string]builtinFn{
		// ---- output ----
		"log": func(in *Interp, args []Value, pos Pos) (Value, error) {
			parts := make([]string, len(args))
			for i, a := range args {
				parts[i] = a.String()
			}
			in.logLine(parts)
			return NullVal{}, nil
		},

		// ---- maths ----
		"abs":   numeric1("abs", math.Abs, func(i int64) int64 { return absInt(i) }),
		"floor": floatToInt("floor", math.Floor),
		"ceil":  floatToInt("ceil", math.Ceil),
		"round": floatToInt("round", math.Round),
		"sqrt":  float1("sqrt", math.Sqrt),
		"sin":   float1("sin", math.Sin),
		"cos":   float1("cos", math.Cos),
		"tan":   float1("tan", math.Tan),
		"pi": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "pi", args, 0, pos); err != nil {
				return nil, err
			}
			return FloatVal(math.Pi), nil
		},
		"atan2": func(in *Interp, args []Value, pos Pos) (Value, error) {
			y, x, err := two(in, "atan2", args, pos)
			if err != nil {
				return nil, err
			}
			return FloatVal(math.Atan2(y, x)), nil
		},
		"random": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "random", args, 0, pos); err != nil {
				return nil, err
			}
			return FloatVal(in.randomUnit()), nil
		},
		// random_range keeps the type it was given, the way min, max and clamp
		// do: two ints are a die roll and want an int back, and a float bound
		// anywhere means the answer is continuous. Both ends are included, so
		// random_range(1, 6) is the six faces and not five of them.
		"random_range": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "random_range", args, 2, pos); err != nil {
				return nil, err
			}
			loI, loInt := args[0].(IntVal)
			hiI, hiInt := args[1].(IntVal)
			if loInt && hiInt {
				if hiI <= loI {
					return loI, nil
				}
				return IntVal(in.randomIntBetween(int64(loI), int64(hiI))), nil
			}
			lo, hi, err := two(in, "random_range", args, pos)
			if err != nil {
				return nil, err
			}
			// An inverted or empty range is the author who typed one number
			// into both places, not an error worth stopping a frame for.
			if hi <= lo {
				return FloatVal(lo), nil
			}
			return FloatVal(lo + (hi-lo)*in.randomUnit()), nil
		},
		"min": minmax("min", false),
		"max": minmax("max", true),
		"clamp": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "clamp", args, 3, pos); err != nil {
				return nil, err
			}
			v, err := num(in, "clamp", args[0], pos)
			if err != nil {
				return nil, err
			}
			lo, err := num(in, "clamp", args[1], pos)
			if err != nil {
				return nil, err
			}
			hi, err := num(in, "clamp", args[2], pos)
			if err != nil {
				return nil, err
			}
			if lo > hi {
				lo, hi = hi, lo
			}
			out := math.Max(lo, math.Min(hi, v))
			// Clamping ints keeps them ints, so a clamped index is still an index.
			if allInts(args) {
				return IntVal(int64(out)), nil
			}
			return FloatVal(out), nil
		},

		// ---- strings ----
		"upper": str1("upper", strings.ToUpper),
		"lower": str1("lower", strings.ToLower),
		"trim":  str1("trim", strings.TrimSpace),
		"split": func(in *Interp, args []Value, pos Pos) (Value, error) {
			s, sep, err := twoStrings(in, "split", args, pos)
			if err != nil {
				return nil, err
			}
			parts := strings.Split(s, sep)
			elems := make([]Value, len(parts))
			for i, p := range parts {
				elems[i] = StrVal(p)
			}
			return NewList(elems...), nil
		},
		"join": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "join", args, 2, pos); err != nil {
				return nil, err
			}
			list, ok := args[0].(*ListVal)
			if !ok {
				return nil, in.errorf(pos, "join takes a list, not %s", typeName(args[0]))
			}
			sep, ok := args[1].(StrVal)
			if !ok {
				return nil, in.errorf(pos, "join's separator is a str, not %s", typeName(args[1]))
			}
			parts := make([]string, len(list.Elems))
			for i, e := range list.Elems {
				parts[i] = e.String()
			}
			return StrVal(strings.Join(parts, string(sep))), nil
		},
		"replace": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "replace", args, 3, pos); err != nil {
				return nil, err
			}
			s, ok1 := args[0].(StrVal)
			old, ok2 := args[1].(StrVal)
			nw, ok3 := args[2].(StrVal)
			if !ok1 || !ok2 || !ok3 {
				return nil, in.errorf(pos, "replace takes three strings")
			}
			return StrVal(strings.ReplaceAll(string(s), string(old), string(nw))), nil
		},
		"starts_with": strPred("starts_with", strings.HasPrefix),
		"ends_with":   strPred("ends_with", strings.HasSuffix),
		"format": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if len(args) == 0 {
				return StrVal(""), nil
			}
			// Deliberately not printf: a format verb that does not match its
			// argument is a runtime error nobody wants in a gameplay script.
			// `{}` is replaced left to right, and a leftover is left alone.
			tmpl, ok := args[0].(StrVal)
			if !ok {
				return nil, in.errorf(pos, "format's first argument is a str, not %s", typeName(args[0]))
			}
			out := string(tmpl)
			for _, a := range args[1:] {
				i := strings.Index(out, "{}")
				if i < 0 {
					break
				}
				out = out[:i] + a.String() + out[i+2:]
			}
			return StrVal(out), nil
		},

		// ---- containers ----
		"len": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "len", args, 1, pos); err != nil {
				return nil, err
			}
			switch v := args[0].(type) {
			case StrVal:
				return IntVal(len(v)), nil
			case *ListVal:
				return IntVal(len(v.Elems)), nil
			case TupleVal:
				return IntVal(len(v.Elems)), nil
			case *DictVal:
				return IntVal(len(v.M)), nil
			}
			return nil, in.errorf(pos, "len does not apply to %s", typeName(args[0]))
		},
		"append": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if len(args) < 2 {
				return nil, in.errorf(pos, "append takes a list and at least one value")
			}
			list, ok := args[0].(*ListVal)
			if !ok {
				return nil, in.errorf(pos, "append takes a list, not %s", typeName(args[0]))
			}
			// In place, and the list is returned too — a list is a reference
			// value, so both readings are the same object.
			list.Elems = append(list.Elems, args[1:]...)
			return list, nil
		},
		"remove": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "remove", args, 2, pos); err != nil {
				return nil, err
			}
			switch c := args[0].(type) {
			case *ListVal:
				for i, e := range c.Elems {
					if Equal(e, args[1]) {
						c.Elems = append(c.Elems[:i], c.Elems[i+1:]...)
						return BoolVal(true), nil
					}
				}
				return BoolVal(false), nil
			case *DictVal:
				k, ok := args[1].(StrVal)
				if !ok {
					return nil, in.errorf(pos, "a dict key is a str, not %s", typeName(args[1]))
				}
				if _, had := c.M[string(k)]; !had {
					return BoolVal(false), nil
				}
				delete(c.M, string(k))
				return BoolVal(true), nil
			}
			return nil, in.errorf(pos, "remove does not apply to %s", typeName(args[0]))
		},
		"contains": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "contains", args, 2, pos); err != nil {
				return nil, err
			}
			switch c := args[0].(type) {
			case StrVal:
				sub, ok := args[1].(StrVal)
				if !ok {
					return nil, in.errorf(pos, "contains on a str takes a str, not %s", typeName(args[1]))
				}
				return BoolVal(strings.Contains(string(c), string(sub))), nil
			case *ListVal:
				for _, e := range c.Elems {
					if Equal(e, args[1]) {
						return BoolVal(true), nil
					}
				}
				return BoolVal(false), nil
			case TupleVal:
				for _, e := range c.Elems {
					if Equal(e, args[1]) {
						return BoolVal(true), nil
					}
				}
				return BoolVal(false), nil
			}
			return nil, in.errorf(pos, "contains does not apply to %s", typeName(args[0]))
		},
		"has": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "has", args, 2, pos); err != nil {
				return nil, err
			}
			d, ok := args[0].(*DictVal)
			if !ok {
				return nil, in.errorf(pos, "has takes a dict, not %s", typeName(args[0]))
			}
			k, ok := args[1].(StrVal)
			if !ok {
				return nil, in.errorf(pos, "a dict key is a str, not %s", typeName(args[1]))
			}
			_, present := d.M[string(k)]
			return BoolVal(present), nil
		},
		"get": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if len(args) < 2 || len(args) > 3 {
				return nil, in.errorf(pos, "get takes a dict, a key, and optionally a fallback")
			}
			d, ok := args[0].(*DictVal)
			if !ok {
				return nil, in.errorf(pos, "get takes a dict, not %s", typeName(args[0]))
			}
			k, ok := args[1].(StrVal)
			if !ok {
				return nil, in.errorf(pos, "a dict key is a str, not %s", typeName(args[1]))
			}
			if v, present := d.M[string(k)]; present {
				return v, nil
			}
			if len(args) == 3 {
				return args[2], nil
			}
			return NullVal{}, nil
		},
		"keys": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "keys", args, 1, pos); err != nil {
				return nil, err
			}
			d, ok := args[0].(*DictVal)
			if !ok {
				return nil, in.errorf(pos, "keys takes a dict, not %s", typeName(args[0]))
			}
			// Sorted, so iterating a dict twice gives the same order twice.
			names := sortedKeys(d)
			elems := make([]Value, len(names))
			for i, n := range names {
				elems[i] = StrVal(n)
			}
			return NewList(elems...), nil
		},
		"values": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "values", args, 1, pos); err != nil {
				return nil, err
			}
			d, ok := args[0].(*DictVal)
			if !ok {
				return nil, in.errorf(pos, "values takes a dict, not %s", typeName(args[0]))
			}
			names := sortedKeys(d)
			elems := make([]Value, len(names))
			for i, n := range names {
				elems[i] = d.M[n]
			}
			return NewList(elems...), nil
		},
		"sort": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "sort", args, 1, pos); err != nil {
				return nil, err
			}
			list, ok := args[0].(*ListVal)
			if !ok {
				return nil, in.errorf(pos, "sort takes a list, not %s", typeName(args[0]))
			}
			var sortErr error
			sort.SliceStable(list.Elems, func(i, j int) bool {
				if a, ok := numeric(list.Elems[i]); ok {
					if b, ok := numeric(list.Elems[j]); ok {
						return a < b
					}
				}
				if a, ok := list.Elems[i].(StrVal); ok {
					if b, ok := list.Elems[j].(StrVal); ok {
						return a < b
					}
				}
				if sortErr == nil {
					sortErr = in.errorf(pos, "sort needs a list of numbers or a list of strings")
				}
				return false
			})
			if sortErr != nil {
				return nil, sortErr
			}
			return list, nil
		},

		// ---- vectors ----
		"dot":   vec2("dot", func(a, b VecVal) Value { return FloatVal(a.Dot(b)) }),
		"cross": vec2("cross", func(a, b VecVal) Value { return a.Cross(b) }),
		"distance": vec2("distance", func(a, b VecVal) Value {
			return FloatVal(a.Sub(b).Len())
		}),
		"length": vec1("length", func(v VecVal) Value { return FloatVal(v.Len()) }),
		"normalize": vec1("normalize", func(v VecVal) Value {
			return v.Normalized()
		}),

		// ---- conversion ----
		"str": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "str", args, 1, pos); err != nil {
				return nil, err
			}
			return StrVal(args[0].String()), nil
		},
		"int": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "int", args, 1, pos); err != nil {
				return nil, err
			}
			switch v := args[0].(type) {
			case IntVal:
				return v, nil
			case FloatVal:
				return IntVal(int64(v)), nil
			case BoolVal:
				if v {
					return IntVal(1), nil
				}
				return IntVal(0), nil
			case StrVal:
				n, err := strconv.ParseInt(strings.TrimSpace(string(v)), 10, 64)
				if err != nil {
					return nil, in.errorf(pos, "%q is not an int", string(v))
				}
				return IntVal(n), nil
			}
			return nil, in.errorf(pos, "cannot make an int from %s", typeName(args[0]))
		},
		"float": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "float", args, 1, pos); err != nil {
				return nil, err
			}
			if f, ok := numeric(args[0]); ok {
				return FloatVal(f), nil
			}
			if s, ok := args[0].(StrVal); ok {
				f, err := strconv.ParseFloat(strings.TrimSpace(string(s)), 64)
				if err != nil {
					return nil, in.errorf(pos, "%q is not a float", string(s))
				}
				return FloatVal(f), nil
			}
			return nil, in.errorf(pos, "cannot make a float from %s", typeName(args[0]))
		},
		"type": func(in *Interp, args []Value, pos Pos) (Value, error) {
			if err := wantArgs(in, "type", args, 1, pos); err != nil {
				return nil, err
			}
			return AtomVal(typeName(args[0])), nil
		},
	}

	// `after` is the one builtin that cannot be called. It is a send —
	// `@self <- after(500, :open_done:)` — and the parser takes that form apart
	// before anything else sees it, so a surviving call is one written without
	// a target. It stays in the table so that the name is known: the reference
	// panel and completion list read this map, and "did you mean after?" is a
	// better answer than "nothing defines a function called after".
	builtins["after"] = func(in *Interp, args []Value, pos Pos) (Value, error) {
		return nil, in.errorf(pos,
			"after schedules a message and so has to be sent to whoever should receive it: `@self <- after(500, :open_done:)`")
	}

	// The remaining world verbs all forward to the host, so they share one body.
	for _, name := range worldVerbs {
		verb := name
		builtins[verb] = func(in *Interp, args []Value, pos Pos) (Value, error) {
			return in.callAction(verb, args, pos)
		}
	}
}

// BuiltinNames returns every library function, sorted. The script editor's
// completion list and the reference panel both read this rather than keeping
// their own copy.
func BuiltinNames() []string {
	out := make([]string, 0, len(builtins))
	for n := range builtins {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// IsBuiltin reports whether name is a library function.
func IsBuiltin(name string) bool { _, ok := builtins[name]; return ok }

// ---------- argument helpers ----------

func wantArgs(in *Interp, name string, args []Value, n int, pos Pos) error {
	if len(args) != n {
		return in.errorf(pos, "%s takes %d argument(s), got %d", name, n, len(args))
	}
	return nil
}

func num(in *Interp, name string, v Value, pos Pos) (float64, error) {
	f, ok := numeric(v)
	if !ok {
		return 0, in.errorf(pos, "%s takes a number, not %s", name, typeName(v))
	}
	return f, nil
}

func two(in *Interp, name string, args []Value, pos Pos) (float64, float64, error) {
	if err := wantArgs(in, name, args, 2, pos); err != nil {
		return 0, 0, err
	}
	a, err := num(in, name, args[0], pos)
	if err != nil {
		return 0, 0, err
	}
	b, err := num(in, name, args[1], pos)
	if err != nil {
		return 0, 0, err
	}
	return a, b, nil
}

func twoStrings(in *Interp, name string, args []Value, pos Pos) (string, string, error) {
	if err := wantArgs(in, name, args, 2, pos); err != nil {
		return "", "", err
	}
	a, ok1 := args[0].(StrVal)
	b, ok2 := args[1].(StrVal)
	if !ok1 || !ok2 {
		return "", "", in.errorf(pos, "%s takes two strings", name)
	}
	return string(a), string(b), nil
}

func allInts(args []Value) bool {
	for _, a := range args {
		if _, ok := a.(IntVal); !ok {
			return false
		}
	}
	return true
}

func absInt(i int64) int64 {
	if i < 0 {
		return -i
	}
	return i
}

// numeric1 builds a builtin that preserves int-ness where it can, so abs(-3) is
// an int and abs(-3.0) is a float.
func numeric1(name string, ff func(float64) float64, fi func(int64) int64) builtinFn {
	return func(in *Interp, args []Value, pos Pos) (Value, error) {
		if err := wantArgs(in, name, args, 1, pos); err != nil {
			return nil, err
		}
		if i, ok := args[0].(IntVal); ok {
			return IntVal(fi(int64(i))), nil
		}
		f, err := num(in, name, args[0], pos)
		if err != nil {
			return nil, err
		}
		return FloatVal(ff(f)), nil
	}
}

func float1(name string, fn func(float64) float64) builtinFn {
	return func(in *Interp, args []Value, pos Pos) (Value, error) {
		if err := wantArgs(in, name, args, 1, pos); err != nil {
			return nil, err
		}
		f, err := num(in, name, args[0], pos)
		if err != nil {
			return nil, err
		}
		return FloatVal(fn(f)), nil
	}
}

// floatToInt builds floor/ceil/round, which return an int — rounding a float
// and getting a float back is never what the caller wanted.
func floatToInt(name string, fn func(float64) float64) builtinFn {
	return func(in *Interp, args []Value, pos Pos) (Value, error) {
		if err := wantArgs(in, name, args, 1, pos); err != nil {
			return nil, err
		}
		if i, ok := args[0].(IntVal); ok {
			return i, nil
		}
		f, err := num(in, name, args[0], pos)
		if err != nil {
			return nil, err
		}
		return IntVal(int64(fn(f))), nil
	}
}

func minmax(name string, wantMax bool) builtinFn {
	return func(in *Interp, args []Value, pos Pos) (Value, error) {
		if len(args) < 1 {
			return nil, in.errorf(pos, "%s takes at least one number", name)
		}
		best := args[0]
		bestF, err := num(in, name, args[0], pos)
		if err != nil {
			return nil, err
		}
		for _, a := range args[1:] {
			f, err := num(in, name, a, pos)
			if err != nil {
				return nil, err
			}
			if (wantMax && f > bestF) || (!wantMax && f < bestF) {
				best, bestF = a, f
			}
		}
		return best, nil
	}
}

func str1(name string, fn func(string) string) builtinFn {
	return func(in *Interp, args []Value, pos Pos) (Value, error) {
		if err := wantArgs(in, name, args, 1, pos); err != nil {
			return nil, err
		}
		s, ok := args[0].(StrVal)
		if !ok {
			return nil, in.errorf(pos, "%s takes a str, not %s", name, typeName(args[0]))
		}
		return StrVal(fn(string(s))), nil
	}
}

func strPred(name string, fn func(string, string) bool) builtinFn {
	return func(in *Interp, args []Value, pos Pos) (Value, error) {
		a, b, err := twoStrings(in, name, args, pos)
		if err != nil {
			return nil, err
		}
		return BoolVal(fn(a, b)), nil
	}
}

func vec1(name string, fn func(VecVal) Value) builtinFn {
	return func(in *Interp, args []Value, pos Pos) (Value, error) {
		if err := wantArgs(in, name, args, 1, pos); err != nil {
			return nil, err
		}
		v, ok := args[0].(VecVal)
		if !ok {
			return nil, in.errorf(pos, "%s takes a vec, not %s", name, typeName(args[0]))
		}
		return fn(v), nil
	}
}

// vec2 builds a two-vector operation. `distance` also accepts two refs, which
// the host resolves to positions — that lives in the host so this stays pure.
func vec2(name string, fn func(a, b VecVal) Value) builtinFn {
	return func(in *Interp, args []Value, pos Pos) (Value, error) {
		if err := wantArgs(in, name, args, 2, pos); err != nil {
			return nil, err
		}
		a, aok := args[0].(VecVal)
		b, bok := args[1].(VecVal)
		if aok && bok {
			return fn(a, b), nil
		}
		if name == "distance" {
			// Two objects is the far commoner reading of distance in a game
			// script, so forward it rather than making the author dig out
			// positions first.
			return in.callAction("distance", args, pos)
		}
		return nil, in.errorf(pos, "%s takes two vecs", name)
	}
}
