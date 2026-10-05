package script

// script_actions_song.go — steering a tracker song while it plays.
//
// play_music plays a song like any track; these verbs are what a song can do
// that a recording cannot. Every change is quantised — it waits for the next
// beat, bar, pattern or the end of the section — so a script that switches to
// the combat section the instant a fight starts still lands on the downbeat.
// The quantise word is a str or an atom: "beat" (the default), "bar",
// "pattern", "section" or "now".
//
// The song talks back through events posted to the level's scripts and the
// player's: `:song_marker:` from a pattern's E command, `:song_section:` when a
// section starts, `:song_end:` when a song that does not loop runs out, and —
// for a song with beat_events on — `:song_beat:` and `:song_bar:`.

import (
	"fmt"

	"foundry/internal/flang"
	"foundry/internal/tracker"
	"foundry/internal/world"
)

// quantArg reads an optional quantise word.
func quantArg(args []flang.Value, i int) tracker.Quantize {
	if s, ok := verbStrAt(args, i); ok {
		return tracker.ParseQuantize(s)
	}
	return tracker.QuantBeat
}

// optionalSecs reads an optional duration, defaulting to def.
func optionalSecs(args []flang.Value, i int, def float32) float32 {
	if f, ok := verbNum(args, i); ok {
		return float32(f)
	}
	return def
}

var songVerbs = map[string]scriptVerb{
	"play_song": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		name, err := verbStr(args, 0, "play_song")
		if err != nil {
			return nil, err
		}
		if rt.lvl == nil {
			return flang.BoolVal(false), nil
		}
		if !tracker.IsSong(name) {
			rt.warnAt(pos, "play_song(%q): there is no song sounds/%s%s", name, name, tracker.FileExt)
			return flang.BoolVal(false), nil
		}
		section, _ := verbStrAt(args, 1)
		rt.markDirty()
		return flang.BoolVal(world.PlaySong(rt.lvl, name, section, optionalSecs(args, 2, 1))), nil
	},

	"song_section": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		name, err := verbStr(args, 0, "song_section")
		if err != nil {
			return nil, err
		}
		if world.SongPlayer(rt.lvl) == nil {
			return flang.BoolVal(false), nil
		}
		if !world.SetSongSection(rt.lvl, name, quantArg(args, 1)) {
			rt.warnAt(pos, "song_section(%q): the playing song has no section by that name", name)
			return flang.BoolVal(false), nil
		}
		rt.markDirty()
		return flang.BoolVal(true), nil
	},

	"song_param": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		name, err := verbStr(args, 0, "song_param")
		if err != nil {
			return nil, err
		}
		v, ok := verbNum(args, 1)
		if !ok {
			return nil, verbArgErr("song_param", "a param name and a value from 0 to 1")
		}
		if world.SongPlayer(rt.lvl) == nil {
			return flang.BoolVal(false), nil
		}
		if !world.SetSongParam(rt.lvl, name, float32(v), optionalSecs(args, 2, 0)) {
			rt.warnAt(pos, "song_param(%q): the playing song has no param by that name", name)
			return flang.BoolVal(false), nil
		}
		rt.markDirty()
		return flang.BoolVal(true), nil
	},

	"song_mute": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		ch, ok := verbStrAt(args, 0)
		if !ok {
			if n, isNum := verbNum(args, 0); isNum {
				ch, ok = fmt.Sprint(int(n)), true
			}
		}
		if !ok || len(args) < 2 {
			return nil, verbArgErr("song_mute", "a channel name (or number) and true or false")
		}
		on, isBool := args[1].(flang.BoolVal)
		if !isBool {
			return nil, verbArgErr("song_mute", "true to mute or false to unmute as the second argument")
		}
		if world.SongPlayer(rt.lvl) == nil {
			return flang.BoolVal(false), nil
		}
		if !world.SetSongMute(rt.lvl, ch, bool(on), optionalSecs(args, 2, 0.5)) {
			rt.warnAt(pos, "song_mute(%q): the playing song has no channel by that name", ch)
			return flang.BoolVal(false), nil
		}
		rt.markDirty()
		return flang.BoolVal(true), nil
	},

	"song_tempo": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		bpm, ok := verbNum(args, 0)
		if !ok {
			return nil, verbArgErr("song_tempo", "a tempo in beats per minute")
		}
		p := world.SongPlayer(rt.lvl)
		if p == nil {
			return flang.BoolVal(false), nil
		}
		return flang.BoolVal(p.Tempo(float32(bpm), optionalSecs(args, 1, 0))), nil
	},

	"song_stinger": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		name, err := verbStr(args, 0, "song_stinger")
		if err != nil {
			return nil, err
		}
		p := world.SongPlayer(rt.lvl)
		if p == nil {
			return flang.BoolVal(false), nil
		}
		s, err := tracker.Load(name)
		if err != nil {
			rt.warnAt(pos, "song_stinger(%q): %v", name, err)
			return flang.BoolVal(false), nil
		}
		return flang.BoolVal(p.Stinger(s, quantArg(args, 1))), nil
	},

	"play_note": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) < 2 {
			return nil, verbArgErr("play_note", "an instrument (name or number) and a note such as \"C-4\"")
		}
		p := world.SongPlayer(rt.lvl)
		if p == nil {
			return flang.BoolVal(false), nil
		}
		s := p.Song()
		var instr uint8
		if n, ok := verbNum(args, 0); ok {
			instr = uint8(n)
		} else if name, ok := verbStrAt(args, 0); ok {
			for i, in := range s.Instruments {
				if in.Name == name {
					instr = uint8(i + 1)
				}
			}
		}
		if s.Instrument(instr) == nil {
			rt.warnAt(pos, "play_note: the playing song has no instrument %v", args[0])
			return flang.BoolVal(false), nil
		}
		noteName, ok := verbStrAt(args, 1)
		if !ok {
			return nil, verbArgErr("play_note", "a note such as \"C-4\" as the second argument")
		}
		note, err := tracker.ParseNote(noteName)
		if err != nil || note < tracker.NoteMin || note > tracker.NoteMax {
			rt.warnAt(pos, "play_note: %q is not a note", noteName)
			return flang.BoolVal(false), nil
		}
		vol := float32(1)
		if v, ok := verbNum(args, 2); ok {
			vol = float32(v)
		}
		return flang.BoolVal(p.NoteFor(instr, note, vol, optionalSecs(args, 3, 0.5))), nil
	},

	"song_position": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		d := flang.NewDict()
		p := world.SongPlayer(rt.lvl)
		if p == nil {
			d.M["playing"] = flang.BoolVal(false)
			return d, nil
		}
		at := p.Position()
		s := p.Song()
		d.M["playing"] = flang.BoolVal(at.Playing)
		d.M["section"] = flang.StrVal(world.MusicSection(rt.lvl))
		d.M["order"] = flang.IntVal(at.Order)
		d.M["row"] = flang.IntVal(at.Row)
		d.M["beat"] = flang.IntVal(at.Row / s.LPB)
		d.M["song"] = flang.StrVal(rt.lvl.Music.Track)
		return d, nil
	},
}

// postSongEvents drains the level song's events and follows the hard pause.
// Posted to the level's scripts and the player's, the listeners key events
// have, since music belongs to the level rather than to any one object.
func postSongEvents(rt *ScriptRuntime, lvl *world.Level, paused bool) {
	world.MusicPause(lvl, paused)
	p := world.SongPlayer(lvl)
	if p == nil {
		return
	}
	rt.songEvents = p.Events(rt.songEvents[:0])
	if paused {
		return
	}
	for _, e := range rt.songEvents {
		switch e.Kind {
		case tracker.EventMarker:
			postPointer(rt, lvl, "song_marker", flang.RefVal{}, []flang.Value{flang.StrVal(e.Name), flang.IntVal(e.Value)})
		case tracker.EventSection:
			postPointer(rt, lvl, "song_section", flang.RefVal{}, []flang.Value{flang.StrVal(e.Name)})
		case tracker.EventBeat:
			postPointer(rt, lvl, "song_beat", flang.RefVal{}, []flang.Value{flang.IntVal(e.Row / max(p.Song().LPB, 1)), flang.StrVal(e.Section)})
		case tracker.EventBar:
			// A bar is a beat too; a handler counting beats should not skip it.
			postPointer(rt, lvl, "song_beat", flang.RefVal{}, []flang.Value{flang.IntVal(e.Row / max(p.Song().LPB, 1)), flang.StrVal(e.Section)})
			postPointer(rt, lvl, "song_bar", flang.RefVal{}, []flang.Value{flang.StrVal(e.Section)})
		case tracker.EventEnd:
			postPointer(rt, lvl, "song_end", flang.RefVal{}, []flang.Value{flang.StrVal(lvl.Music.Track)})
		}
	}
}
