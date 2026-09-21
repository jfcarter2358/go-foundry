package script

// script_save.go — the script runtime's half of a save game: what it
// remembers between frames, out to a savefile.ScriptState and back.
//
// What is kept is what a handler would notice missing — every object's
// `.state`, the pending `after` timers, the play clock they are due against,
// which objects have already heard `:spawn:`, who is standing in what, and the
// checkpoints reached. What is not: the message queue (a save is taken between
// frames, when the tick has drained it), the parsed programs (they are the
// level's scripts, loaded again with it), and anything belonging to a screen
// or a widget on a screen the save does not put back up — a menu, which is
// the save screen the player was looking at when they saved. A screen the save
// does restore, the HUD, keeps its state and timers and is not `:spawn:`ed
// again, the same as any other object.

import (
	"fmt"
	"sort"

	"foundry/internal/flang"
	"foundry/internal/savefile"
	"foundry/internal/world"
)

// isScreenRef reports whether r names a screen or a widget.
func isScreenRef(r flang.RefVal) bool {
	return r.Kind == flang.RefScreen || r.Kind == flang.RefWidget
}

// CaptureScriptState is the runtime's memory, for a save. screens are the
// screens the save puts back up; what the runtime remembers about any other
// screen or its widgets is left out.
func CaptureScriptState(screens map[string]bool) savefile.ScriptState {
	// A screen ref's A is its screen for a widget as much as for a screen.
	dropped := func(r flang.RefVal) bool { return isScreenRef(r) && !screens[r.A] }
	rt := Scripts
	ss := savefile.ScriptState{
		Elapsed:      scriptEvents.elapsed,
		StackElapsed: scriptEvents.stackElapsed,
	}
	for r, d := range rt.state {
		if dropped(r) || d == nil || len(d.M) == 0 {
			continue
		}
		ss.State = append(ss.State, savefile.RefState{Ref: encodeRef(r), State: EncodeValue(d)})
	}
	sort.Slice(ss.State, func(i, j int) bool { return refLess(ss.State[i].Ref, ss.State[j].Ref) })

	for _, t := range scriptEvents.timers {
		if dropped(t.to) {
			continue
		}
		args := make([]savefile.Value, len(t.args))
		for i, a := range t.args {
			args[i] = EncodeValue(a)
		}
		ss.Timers = append(ss.Timers, savefile.Timer{
			Due: t.due, To: encodeRef(t.to), Atom: t.atom, Args: args,
			File: t.pos.File, Line: t.pos.Line, Col: t.pos.Col, OnStack: t.onStack,
		})
	}

	for r := range scriptEvents.spawned {
		if !dropped(r) {
			ss.Spawned = append(ss.Spawned, encodeRef(r))
		}
	}
	sort.Slice(ss.Spawned, func(i, j int) bool { return refLess(ss.Spawned[i], ss.Spawned[j]) })

	for p := range scriptEvents.overlaps {
		ss.Overlaps = append(ss.Overlaps, [2]savefile.Ref{encodeRef(p.container), encodeRef(p.other)})
	}
	sort.Slice(ss.Overlaps, func(i, j int) bool {
		if ss.Overlaps[i][0] != ss.Overlaps[j][0] {
			return refLess(ss.Overlaps[i][0], ss.Overlaps[j][0])
		}
		return refLess(ss.Overlaps[i][1], ss.Overlaps[j][1])
	})

	for name := range scriptEvents.checkpoints {
		ss.Checkpoints = append(ss.Checkpoints, name)
	}
	sort.Strings(ss.Checkpoints)
	return ss
}

// RestoreScriptState puts a saved memory back into a runtime that has just
// loaded the save's level — after ScriptRuntimeLoad and ScriptEventsReset,
// before the first tick.
func RestoreScriptState(ss savefile.ScriptState) {
	rt := Scripts
	scriptEvents.elapsed = ss.Elapsed
	scriptEvents.stackElapsed = ss.StackElapsed
	scriptEvents.paused = false

	rt.state = map[flang.RefVal]*flang.DictVal{}
	for _, rs := range ss.State {
		d, ok := DecodeValue(rs.State).(*flang.DictVal)
		if !ok {
			continue
		}
		rt.state[decodeRef(rs.Ref)] = d
	}

	scriptEvents.timers = scriptEvents.timers[:0]
	for _, t := range ss.Timers {
		args := make([]flang.Value, len(t.Args))
		for i, a := range t.Args {
			args[i] = DecodeValue(a)
		}
		scriptEvents.timers = append(scriptEvents.timers, scriptTimer{
			due: t.Due, to: decodeRef(t.To), atom: t.Atom, args: args,
			pos: flang.Pos{File: t.File, Line: t.Line, Col: t.Col}, onStack: t.OnStack,
		})
	}

	scriptEvents.spawned = map[flang.RefVal]bool{}
	for _, r := range ss.Spawned {
		scriptEvents.spawned[decodeRef(r)] = true
	}
	scriptEvents.overlaps = map[overlapPair]bool{}
	for _, p := range ss.Overlaps {
		scriptEvents.overlaps[overlapPair{container: decodeRef(p[0]), other: decodeRef(p[1])}] = true
	}
	scriptEvents.checkpoints = map[string]bool{}
	for _, name := range ss.Checkpoints {
		scriptEvents.checkpoints[name] = true
	}
	rt.queue, rt.spill = nil, nil
	if rt.lvl != nil {
		rt.cache = buildRefCache(rt.lvl)
	}
}

// QueueTextureLoad asks for a texture to be loaded where the GL context is
// current, as a script's write to a texture property does.
func QueueTextureLoad(name string) {
	if name != "" {
		Scripts.markTextureDirty(name)
	}
}

// PlayTime is the play clock `time()` reads.
func PlayTime() float64 { return scriptEvents.elapsed }

// PostCheckpointSaved tells the player's script, and every screen up, that a
// checkpoint has been saved — `(:checkpoint:, name)` — so a HUD can say so.
// Sent after the save is on disk rather than when the player walks in, so
// nothing claims a save that failed.
func PostCheckpointSaved(name string) {
	rt := Scripts
	if rt.lvl == nil {
		return
	}
	args := []flang.Value{flang.StrVal(name)}
	if world.PlayerEntity != nil && world.PlayerEntity.Script != "" {
		_ = rt.post(flang.RefVal{Kind: flang.RefEntity, A: "player"}, "checkpoint", args, flang.Pos{})
	}
	if uiStack != nil {
		for _, sc := range uiStack.Screens() {
			if sc.Script != "" {
				_ = rt.post(flang.RefVal{Kind: flang.RefScreen, A: sc.ID}, "checkpoint", args, flang.Pos{})
			}
		}
	}
}

// ---------- objects a restore makes and unmakes ----------

// SpawnForRestore makes an entity a script had spawned, under the ID it had —
// `spawn` less the name-picking, the position and the `:spawn:`, since the
// save supplies all three. The caller writes the rest of its state.
func SpawnForRestore(lvl *world.Level, typeName, id string) (*world.Entity, error) {
	rt := Scripts
	if lvl == nil {
		return nil, fmt.Errorf("no level")
	}
	def, err := world.LoadEntityDef(typeName)
	if err != nil {
		return nil, err
	}
	e := def.Instantiate(id)
	if err := e.Load(); err != nil {
		rt.logLine(fmt.Sprintf("restoring %s: %v", id, err))
	}
	lvl.Entities = append(lvl.Entities, e)
	rt.loadUnit(e.Script)
	placed := &lvl.Entities[len(lvl.Entities)-1]
	tex := map[string]bool{}
	if err := world.MaterializeEntityEmitters(lvl, placed, tex); err != nil {
		rt.logLine(fmt.Sprintf("restoring %s: %v", id, err))
	}
	for t := range tex {
		rt.markTextureDirty(t)
	}
	world.MaterializeEntityVolumes(lvl, placed)
	world.MaterializeEntityWaypointAnimations(lvl, placed)
	rt.cache = buildRefCache(lvl)
	return placed, nil
}

// RemoveForRestore takes out an entity or a volume a script had destroyed,
// without the `:destroy:` — it was heard the first time.
func RemoveForRestore(lvl *world.Level, kind flang.Kind_, id string) bool {
	rt := Scripts
	if lvl == nil || rt.lvl != lvl {
		return false
	}
	ok := rt.removeObject(flang.RefVal{Kind: kind, A: id})
	rt.cache = buildRefCache(lvl)
	return ok
}

// ---------- values ----------

// EncodeValue turns a flang value into the tree a save holds.
func EncodeValue(v flang.Value) savefile.Value {
	switch x := v.(type) {
	case nil, flang.NullVal:
		return savefile.Value{T: uint8(flang.TypeNull)}
	case flang.IntVal:
		return savefile.Value{T: uint8(flang.TypeInt), I: int64(x)}
	case flang.FloatVal:
		return savefile.Value{T: uint8(flang.TypeFloat), F: float64(x)}
	case flang.StrVal:
		return savefile.Value{T: uint8(flang.TypeStr), S: string(x)}
	case flang.BoolVal:
		return savefile.Value{T: uint8(flang.TypeBool), B: bool(x)}
	case flang.AtomVal:
		return savefile.Value{T: uint8(flang.TypeAtom), S: string(x)}
	case flang.VecVal:
		return savefile.Value{T: uint8(flang.TypeVec), V: [3]float64(x)}
	case flang.RefVal:
		return savefile.Value{T: uint8(flang.TypeRef), Ref: encodeRef(x)}
	case flang.TupleVal:
		return savefile.Value{T: uint8(flang.TypeTuple), Elems: encodeValues(x.Elems)}
	case *flang.ListVal:
		if x == nil {
			return savefile.Value{T: uint8(flang.TypeList)}
		}
		return savefile.Value{T: uint8(flang.TypeList), Elems: encodeValues(x.Elems)}
	case *flang.DictVal:
		out := savefile.Value{T: uint8(flang.TypeDict)}
		if x == nil {
			return out
		}
		keys := make([]string, 0, len(x.M))
		for k := range x.M {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out.Keys = keys
		out.Elems = make([]savefile.Value, len(keys))
		for i, k := range keys {
			out.Elems[i] = EncodeValue(x.M[k])
		}
		return out
	}
	return savefile.Value{T: uint8(flang.TypeNull)}
}

func encodeValues(vs []flang.Value) []savefile.Value {
	out := make([]savefile.Value, len(vs))
	for i, v := range vs {
		out[i] = EncodeValue(v)
	}
	return out
}

// DecodeValue is EncodeValue's inverse.
func DecodeValue(v savefile.Value) flang.Value {
	switch flang.Type(v.T) {
	case flang.TypeInt:
		return flang.IntVal(v.I)
	case flang.TypeFloat:
		return flang.FloatVal(v.F)
	case flang.TypeStr:
		return flang.StrVal(v.S)
	case flang.TypeBool:
		return flang.BoolVal(v.B)
	case flang.TypeAtom:
		return flang.AtomVal(v.S)
	case flang.TypeVec:
		return flang.VecVal(v.V)
	case flang.TypeRef:
		return decodeRef(v.Ref)
	case flang.TypeTuple:
		return flang.TupleVal{Elems: decodeValues(v.Elems)}
	case flang.TypeList:
		return &flang.ListVal{Elems: decodeValues(v.Elems)}
	case flang.TypeDict:
		d := flang.NewDict()
		for i, k := range v.Keys {
			if i < len(v.Elems) {
				d.M[k] = DecodeValue(v.Elems[i])
			}
		}
		return d
	}
	return flang.NullVal{}
}

func decodeValues(vs []savefile.Value) []flang.Value {
	out := make([]flang.Value, len(vs))
	for i, v := range vs {
		out[i] = DecodeValue(v)
	}
	return out
}

func encodeRef(r flang.RefVal) savefile.Ref {
	return savefile.Ref{Kind: uint8(r.Kind), A: r.A, B: r.B, C: r.C}
}

func decodeRef(r savefile.Ref) flang.RefVal {
	return flang.RefVal{Kind: flang.Kind_(r.Kind), A: r.A, B: r.B, C: r.C}
}

func refLess(a, b savefile.Ref) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	if a.A != b.A {
		return a.A < b.A
	}
	if a.B != b.B {
		return a.B < b.B
	}
	return a.C < b.C
}
