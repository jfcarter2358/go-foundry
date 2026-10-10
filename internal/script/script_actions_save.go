package script

// script_actions_save.go — the verbs save and load screens, the death screen
// and a level's checkpoints are made of.
//
// Everything that changes the game is a request the frame loop acts on between
// frames (hooks/save_hooks.go): a load tears down the runtime making the call,
// and a save taken mid-drain would catch a cascade half-delivered. The verbs
// that only *read* a slot — save_info, latest_save — read the file at the call,
// which a tick may do; that is how a load screen fills in its buttons on
// `:shown:`.

import (
	"errors"

	"foundry/internal/flang"
	"foundry/internal/hooks"
	"foundry/internal/savefile"
)

// saveDateLayout is how save_info's `date` reads: sortable, and short enough
// for a button.
const saveDateLayout = "2006-01-02 15:04"

var saveVerbs = map[string]scriptVerb{
	"save_game": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		slot, err := verbStr(args, 0, "save_game")
		if err != nil {
			return nil, err
		}
		if err := savefile.ValidSlot(slot); err != nil {
			return nil, err
		}
		if rt.lvl == nil || rt.lvl.Name == "" {
			rt.warnAt(pos, "save_game did nothing: the running level has no file to name in a save")
			return flang.BoolVal(false), nil
		}
		hooks.RequestSave(hooks.SaveRequest{Kind: hooks.SaveToSlot, Name: slot})
		return flang.BoolVal(true), nil
	},

	"load_game": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		slot, err := verbStr(args, 0, "load_game")
		if err != nil {
			return nil, err
		}
		// Read the header now, so a Load button on an empty slot answers false
		// at the click rather than doing nothing a frame later.
		if _, err := savefile.ReadHeader(slot); err != nil {
			if !errors.Is(err, savefile.ErrNoSave) {
				rt.warnAt(pos, "load_game(%q): %v", slot, err)
			}
			return flang.BoolVal(false), nil
		}
		hooks.RequestSave(hooks.SaveRequest{Kind: hooks.LoadFromSlot, Name: slot})
		return flang.BoolVal(true), nil
	},

	"save_info": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		slot, err := verbStr(args, 0, "save_info")
		if err != nil {
			return nil, err
		}
		h, err := savefile.ReadHeader(slot)
		if err != nil {
			if !errors.Is(err, savefile.ErrNoSave) {
				rt.warnAt(pos, "save_info(%q): %v", slot, err)
			}
			return flang.NullVal{}, nil
		}
		return saveInfoDict(slot, h), nil
	},

	"latest_save": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 0 {
			return nil, verbArgErr("latest_save", "no arguments")
		}
		slots := savefile.List()
		if len(slots) == 0 {
			return flang.NullVal{}, nil
		}
		return flang.StrVal(slots[0].Name), nil
	},

	"delete_save": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		slot, err := verbStr(args, 0, "delete_save")
		if err != nil {
			return nil, err
		}
		if err := savefile.Delete(slot); err != nil {
			rt.warnAt(pos, "delete_save(%q): %v", slot, err)
			return flang.BoolVal(false), nil
		}
		return flang.BoolVal(true), nil
	},

	"checkpoint": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		name, err := verbStr(args, 0, "checkpoint")
		if err != nil {
			return nil, err
		}
		return flang.BoolVal(reachCheckpoint(rt, name)), nil
	},

	"retry": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 0 {
			return nil, verbArgErr("retry", "no arguments")
		}
		hooks.RequestSave(hooks.SaveRequest{Kind: hooks.Retry})
		return flang.BoolVal(true), nil
	},

	"restart_level": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 0 {
			return nil, verbArgErr("restart_level", "no arguments")
		}
		hooks.RequestSave(hooks.SaveRequest{Kind: hooks.RestartLevel})
		return flang.BoolVal(true), nil
	},
}

// reachCheckpoint records a checkpoint and asks for its save, once per name
// per play-through. It is the whole of both ways of reaching one — a
// checkpoint volume and the `checkpoint` verb — so the two cannot disagree
// about "once".
func reachCheckpoint(rt *ScriptRuntime, name string) bool {
	if name == "" || scriptEvents.checkpoints[name] {
		return false
	}
	if rt.lvl == nil || rt.lvl.Name == "" {
		return false
	}
	// Marked now, not when the save is written: the save is taken after this
	// tick, and it must already count the checkpoint as reached, or retrying
	// from it would reach it again the moment the player stood up.
	scriptEvents.checkpoints[name] = true
	hooks.RequestSave(hooks.SaveRequest{Kind: hooks.Checkpoint, Name: name})
	return true
}

// saveInfoDict is what save_info says about a slot.
func saveInfoDict(slot string, h savefile.Header) *flang.DictVal {
	d := flang.NewDict()
	d.M["slot"] = flang.StrVal(slot)
	d.M["level"] = flang.StrVal(h.Level)
	kind := "manual"
	if int(h.Kind) < len(savefile.KindNames) {
		kind = savefile.KindNames[h.Kind]
	}
	d.M["kind"] = flang.StrVal(kind)
	d.M["checkpoint"] = flang.StrVal(h.Checkpoint)
	d.M["time"] = flang.IntVal(h.Saved.Unix())
	d.M["date"] = flang.StrVal(h.Saved.Local().Format(saveDateLayout))
	d.M["play_time"] = flang.FloatVal(h.PlayTime)
	return d
}
