package script

// script_pointer.go — the mouse pointing at the world, as messages.
//
// The input handler records a press or a release that no widget took, with
// where the cursor was (hooks/pointer_hooks.go). This picks through each one —
// the same ray `pick` casts — and posts what it found. Hover and dragging are
// not events anyone recorded: like `:enter:`, they are the difference between
// what is under the cursor now and what was last frame, so they are derived
// here, once a frame.
//
// # Who hears it
//
// Every pointer event goes to the level's scripts and to the player's, which
// is where a game's controller lives — the thing that selects units, moves the
// build cursor, or orbits the camera on a right-drag — and also to the object
// it is about, when that object has a script of its own: the crate that was
// clicked, the unit the drag started on. The two are separate listeners with
// separate jobs. A button on a console handles its own :pointer_click:; the
// level handles "the player clicked the ground here".
//
// Only while the cursor is free. A captured cursor is the player's head, and
// its buttons are the fire actions.

import (
	"foundry/internal/collision"
	"foundry/internal/flang"
	"foundry/internal/gfx"
	"foundry/internal/hooks"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// dragThreshold is how far, in pixels, the pointer has to travel with a button
// down before the press is a drag rather than a click. A hand holding a mouse
// still moves it a pixel or two between press and release.
const dragThreshold = 4

// pointerPress is one button held down on the world.
type pointerPress struct {
	down         bool
	x0, y0       float32
	hit          flang.RefVal
	at           mgl32.Vec3
	hasAt        bool
	dragging     bool
	lastX, lastY float32
}

// pointerTrack is what deriving pointer events needs to remember between
// frames.
type pointerTrack struct {
	press [4]pointerPress
	// hover is what the cursor was over last frame.
	hover flang.RefVal
}

var pointer pointerTrack

// resetPointerTrack forgets presses and hover, with the rest of the derived
// state (ScriptEventsReset).
func resetPointerTrack() {
	pointer = pointerTrack{}
	hooks.ResetPointer()
}

// pickResult is what a ray through a point on the screen met.
type pickResult struct {
	hit   flang.RefVal
	at    mgl32.Vec3
	hasAt bool
}

// hitValue and atValue are the result as message arguments: a ref or null, a
// vec or null.
func (p pickResult) hitValue() flang.Value {
	if p.hit.Kind == flang.RefNone {
		return flang.NullVal{}
	}
	return p.hit
}

func (p pickResult) atValue() flang.Value {
	if !p.hasAt {
		return flang.NullVal{}
	}
	return vecValue(p.at)
}

// pickFilter is what a pick meets when nothing says otherwise: hitscan's
// defaults, with one change. A detached camera is not inside the player, so
// the player is something it can point at.
func pickFilter() collision.RayFilter {
	f := collision.DefaultRayFilter(hitscanDefaultRange)
	if gfx.CameraDetached() {
		f.Player = world.PlayerEntity
	}
	return f
}

// pickScreen casts through a point on the screen with a filter.
func pickScreen(lvl *world.Level, x, y float32, f collision.RayFilter) (collision.RayResult, bool) {
	origin, dir, ok := gfx.ScreenRay(x, y)
	if !ok || lvl == nil {
		return collision.RayResult{}, false
	}
	return collision.Raycast(lvl, origin, dir, f), true
}

// pickAt is the nearest thing under a point on the screen, and where the ray
// met it — or the level's own surface, for a click on the floor.
func pickAt(lvl *world.Level, x, y float32) pickResult {
	res, ok := pickScreen(lvl, x, y, pickFilter())
	if !ok {
		return pickResult{}
	}
	if h, ok := res.First(); ok {
		return pickResult{hit: hitscanRef(h), at: h.Point, hasAt: true}
	}
	if res.Blocked {
		return pickResult{at: res.Stop, hasAt: true}
	}
	return pickResult{}
}

// postPointerEvents derives and posts one frame's pointer events.
func postPointerEvents(rt *ScriptRuntime, lvl *world.Level) {
	events := hooks.TakePointerEvents()
	cur := hooks.Cursor()
	if !cur.Free || lvl == nil {
		// Captured: nothing is being pointed at. A button still held from
		// before the capture ends without an event — the release that would
		// have ended it went to the fire action instead.
		setHover(rt, lvl, flang.RefVal{})
		pointer.press = [4]pointerPress{}
		return
	}

	// One pick a frame for what is under the cursor, shared by hover and the
	// drag's `at`. Over a widget, nothing in the world is.
	var under pickResult
	if !cur.OverUI {
		under = pickAt(lvl, cur.X, cur.Y)
	}

	for _, ev := range events {
		switch ev.Kind {
		case hooks.PointerWheel:
			p := under
			if ev.X != cur.X || ev.Y != cur.Y {
				p = pickAt(lvl, ev.X, ev.Y)
			}
			postPointer(rt, lvl, "pointer_wheel", p.hit,
				[]flang.Value{flang.FloatVal(ev.Wheel), p.hitValue(), p.atValue()})
		case hooks.PointerDown:
			pointerDown(rt, lvl, ev)
		case hooks.PointerUp:
			pointerUp(rt, lvl, ev)
		}
	}

	for b := range pointer.press {
		pointerDrag(rt, lvl, hooks.PointerButton(b), cur, under)
	}
	setHover(rt, lvl, under.hit)
}

func pointerDown(rt *ScriptRuntime, lvl *world.Level, ev hooks.PointerEvent) {
	if int(ev.Button) >= len(pointer.press) {
		return
	}
	p := pickAt(lvl, ev.X, ev.Y)
	pointer.press[ev.Button] = pointerPress{
		down: true, x0: ev.X, y0: ev.Y, lastX: ev.X, lastY: ev.Y,
		hit: p.hit, at: p.at, hasAt: p.hasAt,
	}
	postPointer(rt, lvl, "pointer_down", p.hit, pointerArgs(ev.Button, p))
}

func pointerUp(rt *ScriptRuntime, lvl *world.Level, ev hooks.PointerEvent) {
	if int(ev.Button) >= len(pointer.press) {
		return
	}
	pr := pointer.press[ev.Button]
	pointer.press[ev.Button] = pointerPress{}
	p := pickAt(lvl, ev.X, ev.Y)
	if pr.dragging {
		// The drop: what it was let go over, heard by the thing that was
		// dragged as well as by whatever it landed on.
		postPointer(rt, lvl, "pointer_drag_end", pr.hit, pointerArgs(ev.Button, p))
		if p.hit != pr.hit && p.hit.Kind != flang.RefNone && rt.listens(p.hit) {
			_ = rt.post(p.hit, "pointer_drop", []flang.Value{atomOf(ev.Button), refOrNull(pr.hit), p.atValue()}, flang.Pos{})
		}
	}
	postPointer(rt, lvl, "pointer_up", p.hit, pointerArgs(ev.Button, p))
	// A click is a press and a release on the same thing, without a drag in
	// between. A press that began before the cursor was free — or on a widget
	// — has nothing to be the same as, and is no click.
	if pr.down && !pr.dragging && p.hit == pr.hit {
		postPointer(rt, lvl, "pointer_click", p.hit, pointerArgs(ev.Button, p))
	}
}

// pointerDrag moves one held button along: past the threshold it becomes a
// drag, and a drag reports every frame the pointer moved.
func pointerDrag(rt *ScriptRuntime, lvl *world.Level, b hooks.PointerButton, cur hooks.CursorState, under pickResult) {
	pr := &pointer.press[b]
	if !pr.down {
		return
	}
	if !pr.dragging {
		dx, dy := cur.X-pr.x0, cur.Y-pr.y0
		if dx*dx+dy*dy < dragThreshold*dragThreshold {
			return
		}
		pr.dragging = true
		start := pickResult{hit: pr.hit, at: pr.at, hasAt: pr.hasAt}
		postPointer(rt, lvl, "pointer_drag_start", pr.hit, pointerArgs(b, start))
	}
	if cur.X == pr.lastX && cur.Y == pr.lastY {
		return
	}
	delta := mgl32.Vec3{cur.X - pr.lastX, cur.Y - pr.lastY, 0}
	pr.lastX, pr.lastY = cur.X, cur.Y
	postPointer(rt, lvl, "pointer_drag", pr.hit, []flang.Value{atomOf(b), vecValue(delta), under.atValue()})
}

// setHover posts :pointer_exit: and :pointer_enter: when what is under the
// cursor changes.
func setHover(rt *ScriptRuntime, lvl *world.Level, now flang.RefVal) {
	was := pointer.hover
	if now == was {
		return
	}
	pointer.hover = now
	if was.Kind != flang.RefNone {
		postPointer(rt, lvl, "pointer_exit", was, []flang.Value{was})
	}
	if now.Kind != flang.RefNone {
		postPointer(rt, lvl, "pointer_enter", now, []flang.Value{now})
	}
}

// postPointer sends one pointer event to the level's scripts, the player's and
// the object it concerns.
func postPointer(rt *ScriptRuntime, lvl *world.Level, atom string, obj flang.RefVal, args []flang.Value) {
	player := flang.RefVal{Kind: flang.RefEntity, A: "player"}
	if lvl != nil && len(lvl.Scripts) > 0 {
		_ = rt.post(flang.RefVal{Kind: flang.RefLevel}, atom, args, flang.Pos{})
	}
	if pe := world.PlayerEntity; pe != nil && (pe.Script != "" || pe.AITree != "") {
		_ = rt.post(player, atom, args, flang.Pos{})
	}
	if obj.Kind == flang.RefNone || obj == player || (obj.Kind == flang.RefEntity && world.PlayerEntity != nil && obj.A == world.PlayerEntity.ID) {
		return
	}
	if rt.listens(obj) {
		_ = rt.post(obj, atom, args, flang.Pos{})
	}
}

// listens reports whether an object in the world has anything to deliver a
// pointer event to: a script, or — for an entity — a decision tree. A wall
// with no script is pointed at all the time, and posting to it would be a
// "no handler" warning on every frame the cursor rested there.
func (rt *ScriptRuntime) listens(r flang.RefVal) bool {
	return rt.scriptFor(r) != "" || rt.aiTreeFor(r) != ""
}

func pointerArgs(b hooks.PointerButton, p pickResult) []flang.Value {
	return []flang.Value{atomOf(b), p.hitValue(), p.atValue()}
}

func atomOf(b hooks.PointerButton) flang.Value { return flang.AtomVal(b.Atom()) }

func refOrNull(r flang.RefVal) flang.Value {
	if r.Kind == flang.RefNone {
		return flang.NullVal{}
	}
	return r
}
