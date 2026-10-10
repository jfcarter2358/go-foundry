package script

// script_actions_camera.go — a script driving the camera, and asking where the
// cursor is pointing.
//
// The camera itself is gfx's (camera_rig.go): a pose of its own, a blend on
// each channel, and a switch saying whether it or the player owns the view.
// Everything here is the language boundary — degrees in, radians out, an
// object turned into a target the rig can follow, and the "did you mean"
// messages.
//
// Every verb that places or turns the camera detaches it from the player first,
// so `camera_move_to(@balcony, 2.0)` is the whole of a cutscene's first line.
// `camera_zoom` does not: a lens is not a position, and the player's own
// camera wants it for a scope. `camera_attach` gives the view back.

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/platform"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

var cameraVerbs = map[string]scriptVerb{
	"camera_detach": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("camera_detach takes no arguments")
		}
		gfx.CameraDetach()
		return flang.BoolVal(true), nil
	},

	"camera_attach": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		seconds, ease, err := blendArgs("camera_attach", args, 0)
		if err != nil {
			return nil, err
		}
		if !gfx.CameraDetached() {
			return flang.BoolVal(false), nil
		}
		gfx.CameraAttach(seconds, ease)
		return flang.BoolVal(true), nil
	},

	"camera_move_to": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) < 1 || len(args) > 3 {
			return nil, fmt.Errorf("camera_move_to takes (place), (place, seconds) or (place, seconds, ease)")
		}
		to, ok := rt.positionOf(args[0])
		if !ok {
			return nil, fmt.Errorf("camera_move_to takes a vec or an object with a position, not a %s", flang.TypeOf(args[0]))
		}
		seconds, ease, err := blendArgs("camera_move_to", args, 1)
		if err != nil {
			return nil, err
		}
		gfx.CameraMoveTo(to, seconds, ease)
		return flang.NullVal{}, nil
	},

	"camera_turn_to": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) < 2 || len(args) > 4 {
			return nil, fmt.Errorf("camera_turn_to takes (yaw, pitch), (yaw, pitch, seconds) or (yaw, pitch, seconds, ease), in degrees")
		}
		yaw, ok1 := toFloat32(args[0])
		pitch, ok2 := toFloat32(args[1])
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("camera_turn_to's yaw and pitch are numbers of degrees")
		}
		seconds, ease, err := blendArgs("camera_turn_to", args, 2)
		if err != nil {
			return nil, err
		}
		gfx.CameraTurnTo(radians(yaw), radians(pitch), seconds, ease)
		return flang.NullVal{}, nil
	},

	"camera_roll_to": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) < 1 || len(args) > 3 {
			return nil, fmt.Errorf("camera_roll_to takes (degrees), (degrees, seconds) or (degrees, seconds, ease)")
		}
		roll, ok := toFloat32(args[0])
		if !ok {
			return nil, fmt.Errorf("camera_roll_to's roll is a number of degrees, not a %s", flang.TypeOf(args[0]))
		}
		seconds, ease, err := blendArgs("camera_roll_to", args, 1)
		if err != nil {
			return nil, err
		}
		gfx.CameraRollTo(radians(roll), seconds, ease)
		return flang.NullVal{}, nil
	},

	"camera_look_at": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) < 1 || len(args) > 3 {
			return nil, fmt.Errorf("camera_look_at takes (place), (place, seconds) or (place, seconds, ease): a vec or an object, or null to let go")
		}
		if _, isNull := args[0].(flang.NullVal); isNull {
			gfx.CameraStopLook()
			return flang.BoolVal(true), nil
		}
		t, err := rt.cameraTarget("camera_look_at", args[0])
		if err != nil {
			return nil, err
		}
		seconds, ease, err := blendArgs("camera_look_at", args, 1)
		if err != nil {
			return nil, err
		}
		gfx.CameraLookAt(t, seconds, ease)
		return flang.BoolVal(true), nil
	},

	"camera_orbit": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("camera_orbit takes (target), (target, options) or (options) — or null to stop orbiting")
		}
		var target *gfx.RigTarget
		opts := args[len(args)-1]
		switch a := args[0].(type) {
		case flang.NullVal:
			if len(args) != 1 {
				return nil, fmt.Errorf("camera_orbit(null) stops the orbit and takes nothing else")
			}
			gfx.CameraStopOrbit()
			return flang.BoolVal(true), nil
		case *flang.DictVal:
			if len(args) != 1 {
				return nil, fmt.Errorf("camera_orbit's options go last: (target, options)")
			}
		default:
			t, err := rt.cameraTarget("camera_orbit", a)
			if err != nil {
				return nil, err
			}
			target = &t
			if len(args) == 1 {
				opts = nil
			}
		}
		p, seconds, ease, err := orbitOptions(opts)
		if err != nil {
			return nil, err
		}
		if !gfx.CameraOrbit(target, p, seconds, ease) {
			return nil, fmt.Errorf("camera_orbit: there is no orbit to adjust — give it a target to orbit first")
		}
		return flang.BoolVal(true), nil
	},

	"camera_zoom": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) < 1 || len(args) > 3 {
			return nil, fmt.Errorf("camera_zoom takes (fov), (fov, seconds) or (fov, seconds, ease), in degrees")
		}
		fov, ok := toFloat32(args[0])
		if !ok {
			return nil, fmt.Errorf("camera_zoom's field of view is a number of degrees, not a %s", flang.TypeOf(args[0]))
		}
		seconds, ease, err := blendArgs("camera_zoom", args, 1)
		if err != nil {
			return nil, err
		}
		gfx.CameraZoom(fov, seconds, ease)
		return flang.NullVal{}, nil
	},

	// screen_ray is the ray from the camera through a point on the window, as
	// the options dict hitscan takes: `hitscan(:shot:, screen_ray(p))` casts
	// along it, and a script can walk it by hand.
	"screen_ray": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) > 1 {
			return nil, fmt.Errorf("screen_ray takes nothing (the cursor) or a point on the window in pixels")
		}
		p := cursorPosition()
		if len(args) == 1 {
			v, ok := toVec3(args[0])
			if !ok {
				return nil, fmt.Errorf("screen_ray's point is a vec of window pixels, not a %s", flang.TypeOf(args[0]))
			}
			p = v
		}
		origin, dir, ok := gfx.ScreenRay(p.X(), p.Y())
		if !ok {
			return flang.NullVal{}, nil
		}
		d := flang.NewDict()
		d.M["from"] = vecValue(origin)
		d.M["direction"] = vecValue(dir)
		return d, nil
	},

	// world_to_screen is where a point, or an object, is on the window: x and y
	// in pixels from the top-left and z its distance in front of the camera.
	// Null when it is behind the camera.
	"world_to_screen": func(rt *ScriptRuntime, args []flang.Value, pos flang.Pos) (flang.Value, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("world_to_screen takes a vec or an object")
		}
		p, ok := rt.positionOf(args[0])
		if !ok {
			return nil, fmt.Errorf("world_to_screen takes a vec or an object with a position, not a %s", flang.TypeOf(args[0]))
		}
		x, y, depth, ok := gfx.WorldToScreen(p)
		if !ok {
			return flang.NullVal{}, nil
		}
		return vecValue(mgl32.Vec3{x, y, depth}), nil
	},
}

// cameraTarget turns a vec or an object into something the rig can aim at. An
// object is followed — its position re-read every frame — so an orbit round a
// walking unit stays centred on it.
func (rt *ScriptRuntime) cameraTarget(name string, v flang.Value) (gfx.RigTarget, error) {
	if p, ok := toVec3(v); ok {
		return gfx.RigTarget{Point: p}, nil
	}
	r, ok := v.(flang.RefVal)
	if !ok {
		return gfx.RigTarget{}, fmt.Errorf("%s takes a vec or an object, not a %s", name, flang.TypeOf(v))
	}
	p, ok := rt.host.refPosition(r)
	if !ok {
		return gfx.RigTarget{}, fmt.Errorf("%s: %s has no position to follow", name, r)
	}
	return gfx.RigTarget{Point: p, Ref: r}, nil
}

// RefreshCameraTargets re-reads where every object the camera is following is
// now. Called by the frame between the script tick and the camera's own step,
// against the cache that tick built — so a unit that moved this frame is
// followed to where it moved to, not to where it was.
//
// A target that has gone keeps the last place it was seen: a camera orbiting a
// unit that has just been destroyed looks at the smoke, not at the origin.
func RefreshCameraTargets(lvl *world.Level) {
	rt := Scripts
	if rt == nil || rt.cache == nil || rt.lvl != lvl {
		return
	}
	for _, t := range gfx.CameraTargets() {
		r, ok := t.Ref.(flang.RefVal)
		if !ok {
			continue
		}
		p, ok := rt.host.refPosition(r)
		t.Lost = !ok
		if ok {
			t.Point = p
		}
	}
}

// blendArgs reads the optional seconds and ease that close most camera verbs,
// starting at args[i].
func blendArgs(name string, args []flang.Value, i int) (float32, gfx.Ease, error) {
	seconds := float32(0)
	ease := gfx.EaseInOut
	if len(args) > i+2 {
		return 0, ease, fmt.Errorf("%s takes at most a number of seconds and an ease after its other arguments", name)
	}
	if len(args) > i {
		s, ok := toFloat32(args[i])
		if !ok {
			return 0, ease, fmt.Errorf("%s's seconds is a number, not a %s", name, flang.TypeOf(args[i]))
		}
		seconds = s
	}
	if len(args) > i+1 {
		e, err := easeValue(name, args[i+1])
		if err != nil {
			return 0, ease, err
		}
		ease = e
	}
	return seconds, ease, nil
}

// easeValue reads an ease, written as an atom (`:ease_out:`) or a string.
func easeValue(name string, v flang.Value) (gfx.Ease, error) {
	var s string
	switch t := v.(type) {
	case flang.AtomVal:
		s = string(t)
	case flang.StrVal:
		s = string(t)
	default:
		return gfx.EaseInOut, fmt.Errorf("%s's ease is one of %s, not a %s", name, easeList(), flang.TypeOf(v))
	}
	e, ok := gfx.ParseEase(s)
	if !ok {
		return gfx.EaseInOut, fmt.Errorf("%s has no ease called %q; it is one of %s", name, s, easeList())
	}
	return e, nil
}

func easeList() string {
	names := gfx.EaseNames()
	for i, n := range names {
		names[i] = ":" + n + ":"
	}
	return strings.Join(names, ", ")
}

// orbitOptionNames are the keys camera_orbit's options dict takes.
var orbitOptionNames = []string{"distance", "yaw", "pitch", "seconds", "ease"}

// orbitOptions reads camera_orbit's options dict. Nil is no options.
func orbitOptions(v flang.Value) (gfx.OrbitParams, float32, gfx.Ease, error) {
	var p gfx.OrbitParams
	seconds, ease := float32(0), gfx.EaseInOut
	if v == nil {
		return p, seconds, ease, nil
	}
	if _, isNull := v.(flang.NullVal); isNull {
		return p, seconds, ease, nil
	}
	d, ok := v.(*flang.DictVal)
	if !ok {
		return p, seconds, ease, fmt.Errorf("camera_orbit's options are a dict, not a %s", flang.TypeOf(v))
	}
	keys := make([]string, 0, len(d.M))
	for k := range d.M {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	num := func(k string) (*float32, error) {
		f, ok := toFloat32(d.M[k])
		if !ok {
			return nil, fmt.Errorf("camera_orbit's %q is a number, not a %s", k, flang.TypeOf(d.M[k]))
		}
		return &f, nil
	}
	for _, k := range keys {
		var err error
		switch k {
		case "distance":
			p.Distance, err = num(k)
		case "yaw", "pitch":
			var f *float32
			if f, err = num(k); err == nil {
				r := radians(*f)
				if k == "yaw" {
					p.Yaw = &r
				} else {
					p.Pitch = &r
				}
			}
		case "seconds":
			var f *float32
			if f, err = num(k); err == nil {
				seconds = *f
			}
		case "ease":
			ease, err = easeValue("camera_orbit", d.M[k])
		default:
			hint := ""
			if best := world.NearestName(k, orbitOptionNames); best != "" {
				hint = fmt.Sprintf("; did you mean %q?", best)
			}
			return p, seconds, ease, fmt.Errorf("camera_orbit has no option called %q%s", k, hint)
		}
		if err != nil {
			return p, seconds, ease, err
		}
	}
	return p, seconds, ease, nil
}

// cursorPosition is where the pointer is, in window pixels from the top-left.
// A captured cursor points at the middle of the view — the reticle — so `pick`
// with the mouse captured is a pick through the crosshair.
func cursorPosition() mgl32.Vec3 {
	c := hooks.Cursor()
	if !c.Free {
		return mgl32.Vec3{float32(platform.WinWidth) / 2, float32(platform.WinHeight) / 2, 0}
	}
	return mgl32.Vec3{c.X, c.Y, 0}
}

func radians(deg float32) float32 { return deg * math.Pi / 180 }
func degrees(rad float32) float32 { return rad * 180 / math.Pi }
