package ui

// ui_interact.go — what the pointer and the focus are currently on.
//
// Everything here addresses a widget by a pair of stable names rather than by a
// pointer or an index, for exactly the reason internal/script's refs do: the UI
// editor rebuilds a screen wholesale on undo, and a Show reloads one from disk,
// so any pointer held across a frame boundary is pointing at a node that no
// longer exists. A name survives both.

// Target names one widget, or nothing at all when it is the zero value.
type Target struct{ Screen, Node string }

// Valid reports whether a target names anything.
func (t Target) Valid() bool { return t.Node != "" || t.Screen != "" }

// Is reports whether a target names this node on this screen.
func (t Target) Is(screen, node string) bool { return t.Screen == screen && t.Node == node }

// Interaction is the pointer and focus state one stack carries between frames.
type Interaction struct {
	Hover Target
	Press Target
	Focus Target

	// NavDriven is set when focus last moved by a key or a stick rather than by
	// the pointer. It selects a focus ring over a hover tint, which is what
	// stops a menu showing two highlighted buttons at once — the one the mouse
	// happens to rest on and the one the stick moved to.
	NavDriven bool

	// Caret and Sel are the text field's cursor and selection anchor, as byte
	// offsets into the focused input's string.
	Caret, Sel int
}

// VisualState is which of a widget's looks applies right now.
type VisualState int

const (
	StateNormal VisualState = iota
	StateHover
	StatePress
	StateDisabled
)

// StateOf is the look a node should be drawn in.
//
// Disabled wins over everything: a greyed-out button that lit up under the
// pointer would be telling the player it can be pressed.
func (in *Interaction) StateOf(screen string, n *Node) VisualState {
	if !n.Kind.Interactive() {
		return StateNormal
	}
	if !n.Enabled {
		return StateDisabled
	}
	switch {
	case in.Press.Is(screen, n.ID):
		return StatePress
	case in.Hover.Is(screen, n.ID), in.NavDriven && in.Focus.Is(screen, n.ID):
		return StateHover
	}
	return StateNormal
}

// Resolve produces the colour, texture and UV a node is drawn with in a state,
// folding in an inherited alpha.
//
// A state with no authored style is derived by scaling the base colour rather
// than left alone, so a button an author never styled still visibly responds.
// A widget that gives no feedback at all reads as broken, and "it was never
// configured" is not a distinction a player can make.
func (s Style) Resolve(state VisualState, alpha float32) (col [4]float32, tex string, uv [4]float32) {
	c := s.Color
	tex, uvv := s.Texture, s.UV

	apply := func(o *StateStyle, tint float32) {
		if o == nil {
			c = [4]float32{c[0] * tint, c[1] * tint, c[2] * tint, c[3]}
			return
		}
		if o.Color != nil {
			c = *o.Color
		}
		if o.Texture != "" {
			tex = o.Texture
		}
		if o.UV != nil {
			uvv = *o.UV
		}
	}

	switch state {
	case StateHover:
		apply(s.Hover, 1.25)
	case StatePress:
		apply(s.Press, 0.75)
	case StateDisabled:
		if s.Disabled == nil {
			// Fade rather than darken: a disabled control should recede, and
			// darkening alone is indistinguishable from a pressed one.
			c = [4]float32{c[0] * 0.6, c[1] * 0.6, c[2] * 0.6, c[3] * 0.5}
		} else {
			apply(s.Disabled, 1)
		}
	}

	c[3] *= alpha
	return c, tex, uvv
}
