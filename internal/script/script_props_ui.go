package script

// script_props_ui.go — the `.prop` surface for screens and widgets.
//
// Thin, like the sprite and emitter tables: what a script plausibly changes at
// runtime — show a widget, grey it out, set its text — rather than everything
// the UI editor can author. Layout is authored; a script that repositioned a
// button by writing anchors would be doing the editor's job badly.
//
// Dirty is false throughout: a widget owns no sector geometry. Tex is set on
// the texture write, so the name is loaded during DrainScriptWork rather than
// mid-tick, where GL must not be touched.
//
// The refusals matter as much as the writes. A widget whose document declares a
// binding is showing what the level says; a script writing to it would be
// overwritten by the next poll, and which of the two won would depend on frame
// timing. Refusing with a message that names the binding is the only version of
// that anyone can debug.

import (
	"foundry/internal/flang"
	"foundry/internal/ui"

	"github.com/go-gl/mathgl/mgl32"
)

// widgetProps is the property table for flang.RefWidget.
func widgetProps() map[string]propAccess {
	get := func(fn func(n *ui.Node) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			n := c.widgetFor(r)
			if n == nil {
				return nil, false
			}
			return fn(n), true
		}
	}
	set := func(fn func(n *ui.Node, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			n := c.widgetFor(r)
			if n == nil {
				return &scriptMissingError{r.String()}
			}
			return fn(n, v)
		}
	}

	return map[string]propAccess{
		"visible": {
			Get: get(func(n *ui.Node) flang.Value { return flang.BoolVal(n.Visible) }),
			Set: set(func(n *ui.Node, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("visible", "bool", v)
				}
				n.Visible = b
				return nil
			}),
			Doc: "whether the widget and everything under it is drawn",
		},
		"enabled": {
			Get: get(func(n *ui.Node) flang.Value { return flang.BoolVal(n.Enabled) }),
			Set: set(func(n *ui.Node, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("enabled", "bool", v)
				}
				n.Enabled = b
				return nil
			}),
			Doc: "off greys the widget and refuses input; it is still drawn",
		},
		"text": {
			Get: get(func(n *ui.Node) flang.Value { return flang.StrVal(n.Text.Shown()) }),
			Set: set(func(n *ui.Node, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("text", "str", v)
				}
				if !n.SetText(s) {
					return boundErr("text", n.ID, n.Text.Bind)
				}
				return nil
			}),
			Doc: "the string shown. Refused on a widget whose document binds it",
		},
		"value": {
			Get: get(func(n *ui.Node) flang.Value { return flang.FloatVal(float64(n.Value.Current())) }),
			Set: set(func(n *ui.Node, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("value", "float", v)
				}
				if !n.SetValue(f) {
					return boundErr("value", n.ID, n.Value.Bind)
				}
				return nil
			}),
			Doc: "a bar's or slider's number. Refused on a widget whose document binds it",
		},
		"max": {
			Get: get(func(n *ui.Node) flang.Value { return flang.FloatVal(float64(n.Value.Ceiling())) }),
			Set: set(func(n *ui.Node, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("max", "float", v)
				}
				if n.Value.BindMax != "" {
					return boundErr("max", n.ID, n.Value.BindMax)
				}
				n.Value.Max = f
				return nil
			}),
			Doc: "the value a full bar means",
		},
		"on": {
			Get: get(func(n *ui.Node) flang.Value { return flang.BoolVal(n.Value.On) }),
			Set: set(func(n *ui.Node, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("on", "bool", v)
				}
				n.Value.On = b
				return nil
			}),
			Doc: "a toggle's state",
		},
		"alpha": {
			Get: get(func(n *ui.Node) flang.Value { return flang.FloatVal(float64(n.Alpha)) }),
			Set: set(func(n *ui.Node, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("alpha", "float", v)
				}
				n.Alpha = clamp01f(f)
				return nil
			}),
			Doc: "opacity from 0 to 1, multiplied down the whole subtree",
		},
		"color": {
			Get: get(func(n *ui.Node) flang.Value {
				return vecValue(mgl32.Vec3{n.Style.Color[0], n.Style.Color[1], n.Style.Color[2]})
			}),
			Set: set(func(n *ui.Node, v flang.Value) error {
				c, ok := toVec3(v)
				if !ok {
					return propErr("color", "vec", v)
				}
				// Alpha is `alpha`, deliberately: a tint and an opacity are
				// different things to want, and folding them into one vec4
				// would mean every retint also had to restate the opacity.
				n.Style.Color = mgl32.Vec4{c[0], c[1], c[2], n.Style.Color[3]}
				return nil
			}),
			Doc: "tint, as a vec of r, g and b. Opacity is `alpha`",
		},
		"texture": {
			Get: get(func(n *ui.Node) flang.Value { return flang.StrVal(n.Style.Texture) }),
			Set: set(func(n *ui.Node, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("texture", "str", v)
				}
				n.Style.Texture = s
				return nil
			}),
			Tex: true,
			Doc: "texture name, or empty for a flat colour",
		},
		"video": {
			Get: get(func(n *ui.Node) flang.Value { return flang.StrVal(n.Video.Source) }),
			Set: set(func(n *ui.Node, v flang.Value) error {
				s, ok := toStr(v)
				if !ok {
					return propErr("video", "str", v)
				}
				// Pointing a widget at a different clip rewinds it. The
				// alternative — keeping the playhead across a change of film —
				// is not a thing anyone means, and it lands on whatever frame
				// the new clip happens to have at that second.
				n.Video.Source = s
				n.SeekVideo(0)
				return nil
			}),
			Doc: "the clip a video widget plays, named as it is under video/",
		},
		"playing": {
			Get: get(func(n *ui.Node) flang.Value { return flang.BoolVal(n.VideoPlaying()) }),
			Set: set(func(n *ui.Node, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("playing", "bool", v)
				}
				n.SetVideoPlaying(b)
				return nil
			}),
			Doc: "a video's transport. Writing true to a finished clip starts it again",
		},
		"time": {
			Get: get(func(n *ui.Node) flang.Value { return flang.FloatVal(float64(n.VideoTime())) }),
			Set: set(func(n *ui.Node, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("time", "float", v)
				}
				n.SeekVideo(f)
				return nil
			}),
			Doc: "how far into a video playback has got, in seconds. Writing it seeks",
		},
		"duration": {
			Get: get(func(n *ui.Node) flang.Value { return flang.FloatVal(float64(n.VideoDuration())) }),
			Doc: "how long a video runs, in seconds; zero until it has been opened",
		},
		"loop": {
			Get: get(func(n *ui.Node) flang.Value { return flang.BoolVal(n.Video.Loop) }),
			Set: set(func(n *ui.Node, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("loop", "bool", v)
				}
				n.Video.Loop = b
				return nil
			}),
			Doc: "whether a video starts again when it reaches the end",
		},
		"muted": {
			Get: get(func(n *ui.Node) flang.Value { return flang.BoolVal(n.Video.Muted) }),
			Set: set(func(n *ui.Node, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("muted", "bool", v)
				}
				n.Video.Muted = b
				return nil
			}),
			Doc: "silences a video's soundtrack without stopping it",
		},
		"volume": {
			Get: get(func(n *ui.Node) flang.Value { return flang.FloatVal(float64(n.Video.Volume)) }),
			Set: set(func(n *ui.Node, v flang.Value) error {
				f, ok := toFloat32(v)
				if !ok {
					return propErr("volume", "float", v)
				}
				// Named volume rather than gain, unlike everything in
				// internal/audio: this is the widget's own level, the thing a
				// player would call a volume, and it is multiplied by the music
				// slider rather than replacing it.
				n.Video.Volume = clamp01f(f)
				return nil
			}),
			Doc: "a video's own level from 0 to 1, on top of the music slider",
		},
		"kind": {
			Get: get(func(n *ui.Node) flang.Value { return flang.AtomVal(ui.KindName(n.Kind)) }),
			Doc: "panel, image, video, text, button, bar, slider, toggle, input or include",
		},
		"screen": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				if c.widgetFor(r) == nil {
					return nil, false
				}
				return flang.RefVal{Kind: flang.RefScreen, A: r.A}, true
			},
			Doc: "the screen this widget belongs to",
		},
		"world_anchor": {
			Get: get(func(n *ui.Node) flang.Value {
				if p, ok := n.WorldAnchor(); ok {
					return vecValue(p)
				}
				return flang.NullVal{}
			}),
			Set: set(func(n *ui.Node, v flang.Value) error {
				if _, isNull := v.(flang.NullVal); isNull {
					n.ClearWorldAnchor()
					return nil
				}
				p, ok := toVec3(v)
				if !ok {
					return propErr("world_anchor", "vec or null", v)
				}
				n.SetWorldAnchor(p)
				return nil
			}),
			Doc: "a point in the world the widget rides on: its pivot is kept where that point is on screen, " +
				"and it is hidden while the point is behind the camera. null puts it back where it was authored",
		},
		"pos": {
			Get: get(func(n *ui.Node) flang.Value {
				r := n.Solved()
				return vecValue(mgl32.Vec3{r.X, r.Y, 0})
			}),
			Doc: "solved top-left in canvas units; the z component is unused",
		},
		"size": {
			Get: get(func(n *ui.Node) flang.Value {
				r := n.Solved()
				return vecValue(mgl32.Vec3{r.W, r.H, 0})
			}),
			Doc: "solved width and height in canvas units",
		},
		"focused": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				if c.widgetFor(r) == nil || c.ui == nil {
					return nil, false
				}
				return flang.BoolVal(c.ui.Interaction().Focus.Is(r.A, r.B)), true
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				n := c.widgetFor(r)
				if n == nil || c.ui == nil {
					return &scriptMissingError{r.String()}
				}
				b, ok := toBool(v)
				if !ok {
					return propErr("focused", "bool", v)
				}
				if b {
					c.ui.Focus(r.A, r.B)
				} else if c.ui.Interaction().Focus.Is(r.A, r.B) {
					c.ui.ClearFocus()
				}
				return nil
			},
			Doc: "writing true moves keyboard and gamepad focus here",
		},
	}
}

// screenProps is the property table for flang.RefScreen.
func screenProps() map[string]propAccess {
	get := func(fn func(s *ui.Screen) flang.Value) func(*refCache, flang.RefVal) (flang.Value, bool) {
		return func(c *refCache, r flang.RefVal) (flang.Value, bool) {
			s := c.screenFor(r)
			if s == nil {
				return nil, false
			}
			return fn(s), true
		}
	}
	set := func(fn func(s *ui.Screen, v flang.Value) error) func(*refCache, flang.RefVal, flang.Value) error {
		return func(c *refCache, r flang.RefVal, v flang.Value) error {
			s := c.screenFor(r)
			if s == nil {
				return &scriptMissingError{r.String()}
			}
			return fn(s, v)
		}
	}

	return map[string]propAccess{
		"visible": {
			Get: get(func(s *ui.Screen) flang.Value { return flang.BoolVal(true) }),
			Doc: "whether the screen is up. Read-only: use show_screen and hide_screen",
		},
		"top": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				s := c.screenFor(r)
				if s == nil || c.ui == nil {
					return nil, false
				}
				return flang.BoolVal(c.ui.Top() == s), true
			},
			Doc: "whether this is the topmost screen on the stack",
		},
		"modal": {
			Get: get(func(s *ui.Screen) flang.Value { return flang.BoolVal(s.Modal) }),
			Set: set(func(s *ui.Screen, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("modal", "bool", v)
				}
				s.Modal = b
				return nil
			}),
			Doc: "on, nothing below this screen sees any input",
		},
		"pauses": {
			Get: get(func(s *ui.Screen) flang.Value { return flang.BoolVal(s.Pauses) }),
			Set: set(func(s *ui.Screen, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("pauses", "bool", v)
				}
				s.Pauses = b
				return nil
			}),
			Doc: "on, the player, vehicles and entities stop while this screen is up",
		},
		"cursor": {
			Get: get(func(s *ui.Screen) flang.Value { return flang.BoolVal(s.ShowsCursor) }),
			Set: set(func(s *ui.Screen, v flang.Value) error {
				b, ok := toBool(v)
				if !ok {
					return propErr("cursor", "bool", v)
				}
				s.ShowsCursor = b
				return nil
			}),
			Doc: "on, mouse-look is released while this screen is up",
		},
		"focus": {
			Get: func(c *refCache, r flang.RefVal) (flang.Value, bool) {
				s := c.screenFor(r)
				if s == nil || c.ui == nil {
					return nil, false
				}
				f := c.ui.Interaction().Focus
				if f.Screen != s.ID || f.Node == "" {
					return flang.NullVal{}, true
				}
				return flang.RefVal{Kind: flang.RefWidget, A: f.Screen, B: f.Node}, true
			},
			Set: func(c *refCache, r flang.RefVal, v flang.Value) error {
				s := c.screenFor(r)
				if s == nil || c.ui == nil {
					return &scriptMissingError{r.String()}
				}
				w, ok := v.(flang.RefVal)
				if !ok || w.Kind != flang.RefWidget {
					return propErr("focus", "widget ref", v)
				}
				c.ui.Focus(w.A, w.B)
				return nil
			},
			Doc: "the focused widget on this screen, or null",
		},
	}
}

// boundErr is the refusal a script gets for writing a widget the document
// binds. It names the binding, because the fix is to write *that* instead.
func boundErr(prop, node, bind string) error {
	return &scriptMissingError{
		"@" + node + "." + prop + " is bound to " + bind +
			"; write that instead, or clear the binding in the UI editor",
	}
}

func clamp01f(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
