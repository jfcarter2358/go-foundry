package ui

// Package ui is the .fui screen document, the layout solve that positions it,
// and the stack of screens a running game has up.
//
// It is pure: no GL, no level, no editor, no imgui. That is what lets the UI
// editor's live preview and the running game drive one solver — the same call
// internal/particle makes so that a preview and the engine share one
// simulation. A screen that looks right in the editor looks right in the game
// because there is only one implementation of what "right" means.
//
// The GPU half is internal/uidraw, which turns a solved screen into vertices.
// The two places this package needs something above it — evaluating a binding
// against the level, and loading a screen's script when it is first shown —
// arrive as a function field and a function variable respectively, rather than
// as imports.
//
// ui_doc.go holds the runtime document: one screen, made of a tree of nodes.

import (
	"foundry/internal/scriptdata"

	"github.com/go-gl/mathgl/mgl32"
)

// Kind is what a node is.
//
// One flat enum with a name table, the way particle.ShapeKind does it: adding a
// widget means adding its name here and nowhere else, and a reordered enum
// cannot silently reinterpret an old document.
type Kind int

const (
	KindPanel   Kind = iota // a rect with a background — the container
	KindImage               // a textured quad
	KindVideo               // a quad showing the current frame of a video file
	KindText                // a string
	KindButton              // a panel with a label and per-state visuals
	KindBar                 // a background and a fill: progress, health, loading
	KindSlider              // a bar with a draggable handle, writing a value back
	KindToggle              // a checkbox or switch
	KindInput               // a single-line text field
	KindInclude             // splices another document's children in
)

// KindNames is the YAML and UI name of each kind, indexed by the kind.
var KindNames = []string{
	"panel",
	"image",
	"video",
	"text",
	"button",
	"bar",
	"slider",
	"toggle",
	"input",
	"include",
}

// KindName returns a kind's name, falling back to the default.
func KindName(k Kind) string {
	if int(k) < 0 || int(k) >= len(KindNames) {
		return KindNames[KindPanel]
	}
	return KindNames[k]
}

// KindFromName is the inverse; an unknown name loads as a panel, which is inert
// and visible, so a document from a newer engine degrades to boxes rather than
// to nothing.
func KindFromName(name string) Kind {
	for i, n := range KindNames {
		if n == name {
			return Kind(i)
		}
	}
	return KindPanel
}

// Interactive reports whether a kind takes pointer or navigation input at all.
func (k Kind) Interactive() bool {
	switch k {
	case KindButton, KindSlider, KindToggle, KindInput:
		return true
	}
	return false
}

// Leaf reports whether a kind may carry a rotation. Rotation is leaf-only
// because a rotated container would need oriented-box hit testing and an
// oriented scissor rect, and GL scissor cannot express one. Leaf-only still
// covers what is actually wanted — a spinning loading indicator, a tilted
// banner — at no cost to the rest of the system.
func (k Kind) Leaf() bool {
	switch k {
	case KindImage, KindVideo, KindText:
		return true
	}
	return false
}

// LeafKindNames lists the rotatable kinds, for the message a document that
// rotates something else is refused with. Derived rather than written out, so
// adding a leaf kind cannot leave the error naming the old list.
func LeafKindNames() []string {
	var out []string
	for i := range KindNames {
		if Kind(i).Leaf() {
			out = append(out, KindNames[i])
		}
	}
	return out
}

// Rect is a solved rectangle in canvas units. The origin is top-left and Y
// increases downwards, matching the ortho projection uidraw builds and the
// scissor rect it converts to.
type Rect struct{ X, Y, W, H float32 }

// Contains reports whether a point is inside, right and bottom edges exclusive
// so that two abutting rects never both claim the same pixel.
func (r Rect) Contains(x, y float32) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Intersect is the overlap of two rects. A result with W or H <= 0 means
// nothing is left, which is how a fully-clipped subtree is detected.
func (r Rect) Intersect(o Rect) Rect {
	x0, y0 := maxF(r.X, o.X), maxF(r.Y, o.Y)
	x1, y1 := minF(r.X+r.W, o.X+o.W), minF(r.Y+r.H, o.Y+o.H)
	return Rect{x0, y0, x1 - x0, y1 - y0}
}

// Empty reports whether a rect has no area.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Center is the rect's midpoint, which is what the navigation search measures
// distances between.
func (r Rect) Center() (x, y float32) { return r.X + r.W/2, r.Y + r.H/2 }

// RectXf places a node inside its parent: anchors in 0..1 of the parent's
// solved rect, then four pixel offsets inward from the anchor rect's own edges.
//
// One representation for both the pinned and the stretched case. Unity keeps
// anchoredPosition+sizeDelta for one and offsetMin/offsetMax for the other, and
// most of the confusion RectTransform is famous for comes from the two
// disagreeing. The pinned spelling an author actually wants — a position and a
// size — is a derived accessor below, not a second stored field.
type RectXf struct {
	AnchorMin mgl32.Vec2 // {0,0} is the parent's top-left
	AnchorMax mgl32.Vec2 // {1,1} is the parent's bottom-right
	Offset    [4]float32 // left, top, right, bottom, all inward
	Pivot     mgl32.Vec2 // 0..1 within the node's own rect; scale's origin
	Rotation  float32    // radians, leaf kinds only
	Scale     mgl32.Vec2 // about the pivot
}

// Offset indices, named so a drag handler reads as what it moves.
const (
	OffLeft = iota
	OffTop
	OffRight
	OffBottom
)

// DefaultRectXf fills the parent: stretched on both axes with no inset, a
// centred pivot, unscaled.
//
// Stretch rather than a corner pin, because the pinned default with no offsets
// solves to a zero-size rect — a newly added widget would be invisible, and the
// first thing anyone did with one would be to give it a size before they could
// even see it. "The author said nothing about where this goes" is much better
// answered by "it fills its parent" than by "it does not exist".
func DefaultRectXf() RectXf {
	return RectXf{
		AnchorMin: mgl32.Vec2{0, 0},
		AnchorMax: mgl32.Vec2{1, 1},
		Pivot:     mgl32.Vec2{0.5, 0.5},
		Scale:     mgl32.Vec2{1, 1},
	}
}

// StretchedX reports whether the node's width follows its parent's. The
// properties panel shows a different pair of numbers for each case, and a drag
// moves one edge or two depending on the answer.
func (r RectXf) StretchedX() bool { return r.AnchorMin[0] != r.AnchorMax[0] }

// StretchedY is StretchedX for the vertical axis.
func (r RectXf) StretchedY() bool { return r.AnchorMin[1] != r.AnchorMax[1] }

// PinnedX is the position-and-width spelling of a non-stretched horizontal
// axis, measured from the anchor point through the pivot. Meaningless on a
// stretched axis, where left and right offsets are the honest description.
func (r RectXf) PinnedX() (pos, width float32) {
	width = -r.Offset[OffLeft] - r.Offset[OffRight]
	return r.Offset[OffLeft] + r.Pivot[0]*width, width
}

// SetPinnedX writes back what PinnedX reads.
func (r *RectXf) SetPinnedX(pos, width float32) {
	r.Offset[OffLeft] = pos - r.Pivot[0]*width
	r.Offset[OffRight] = -(r.Offset[OffLeft] + width)
}

// PinnedY is PinnedX for the vertical axis.
func (r RectXf) PinnedY() (pos, height float32) {
	height = -r.Offset[OffTop] - r.Offset[OffBottom]
	return r.Offset[OffTop] + r.Pivot[1]*height, height
}

// SetPinnedY writes back what PinnedY reads.
func (r *RectXf) SetPinnedY(pos, height float32) {
	r.Offset[OffTop] = pos - r.Pivot[1]*height
	r.Offset[OffBottom] = -(r.Offset[OffTop] + height)
}

// FillMode is how an image fits the rect it is given.
type FillMode int

const (
	FillStretch FillMode = iota // distort to fill
	FillFit                     // letterbox, preserving aspect
	FillFill                    // crop, preserving aspect
	FillTile                    // repeat at texel size
)

// FillModeNames is each fill mode's name.
var FillModeNames = []string{"stretch", "fit", "fill", "tile"}

// HAlign is horizontal text alignment within its rect.
type HAlign int

const (
	AlignLeft HAlign = iota
	AlignCenter
	AlignRight
)

// HAlignNames is each horizontal alignment's name.
var HAlignNames = []string{"left", "center", "right"}

// VAlign is vertical text alignment within its rect.
type VAlign int

const (
	AlignTop VAlign = iota
	AlignMiddle
	AlignBottom
)

// VAlignNames is each vertical alignment's name.
var VAlignNames = []string{"top", "middle", "bottom"}

// FillDir is which way a bar or slider fills as its value rises.
type FillDir int

const (
	FillLeftRight FillDir = iota
	FillRightLeft
	FillBottomTop
	FillTopBottom
)

// FillDirNames is each fill direction's name.
var FillDirNames = []string{"lr", "rl", "bt", "tb"}

// StateStyle overrides part of a widget's look while it is hovered, pressed or
// disabled. Every field is optional: an override that sets only a colour keeps
// the base texture.
type StateStyle struct {
	Color   *mgl32.Vec4
	Texture string
	UV      *mgl32.Vec4
}

// Style is how a node is painted.
type Style struct {
	// Color tints the whole node. With no texture it is the fill.
	Color mgl32.Vec4
	// Texture names a gfx texture; empty means a solid colour.
	Texture string
	// UV is the sub-rect sampled from it: u0, v0, du, dv. The spelling matches
	// render.SpriteInstance.UV so an author who has done one has done both.
	UV mgl32.Vec4
	// Slice is the 9-slice inset in texels: left, top, right, bottom. All zero
	// draws one quad.
	Slice [4]float32
	Fill  FillMode

	// Hover, Press and Disabled are optional per-state looks. Nil means "derive
	// by tinting", so a button with no authored hover state still lights up —
	// an unstyled widget that gave no feedback at all would read as broken.
	Hover, Press, Disabled *StateStyle
}

// DefaultStyle is opaque white with no texture, covering the whole UV range.
func DefaultStyle() Style { return DefaultStyleFor(KindPanel) }

// DefaultStyleFor is the look a node of a kind has before anyone styles it.
func DefaultStyleFor(k Kind) Style {
	return Style{Color: DefaultBackground(k), UV: mgl32.Vec4{0, 0, 1, 1}, Fill: DefaultFill(k)}
}

// DefaultFill is how a node of a kind fits its picture into its rect before
// anyone chooses.
//
// Letterbox for a video, stretch for everything else. A video has an aspect
// ratio it was shot at and a widget has one the layout gave it, and the two are
// only ever equal by accident; stretching is the one answer that is visibly
// wrong every time. An image is usually authored to fit the box it goes in, so
// the same reasoning does not apply to it.
func DefaultFill(k Kind) FillMode {
	if k == KindVideo {
		return FillFit
	}
	return FillStretch
}

// DefaultBackground is the fill a node of a kind starts with.
//
// Transparent for a label and for an include, opaque white for everything else.
// The difference is what the widget *is*: a panel is a rectangle you can see —
// that is its whole job — but a label is a string, and putting a white box
// behind every one is not what anyone means by "add some text". A label with an
// opaque background it never asked for is also close to unfixable from the
// panel, because the control that clears it does not look like it is about
// backgrounds at all.
//
// An author who wants a background on a label sets one, and it is written to
// the document precisely because it differs from this.
func DefaultBackground(k Kind) mgl32.Vec4 {
	switch k {
	case KindText, KindInclude:
		return mgl32.Vec4{1, 1, 1, 0}
	case KindVideo:
		// Black, because on a video the background is what the letterbox bars
		// are made of. White bars down the sides of a clip read as the widget
		// being broken, and they are the first thing anyone would see, since a
		// widget's rect matching its film's aspect ratio is a coincidence.
		return mgl32.Vec4{0, 0, 0, 1}
	}
	return mgl32.Vec4{1, 1, 1, 1}
}

// TextSpec is the string a node shows and how.
type TextSpec struct {
	Value  string
	Font   string  // a name under resources/fonts; empty takes the theme's
	Size   float32 // canvas units; zero takes the theme's
	Color  mgl32.Vec4
	HAlign HAlign
	VAlign VAlign
	Wrap   bool

	// Bind, when set, replaces Value every tick from the stack's resolver. A
	// script writing this widget's text is then refused with a warning naming
	// the binding, rather than silently losing the write on the next frame.
	Bind string
	// Format is the fmt verb a bound number is spelled with, e.g. "%.0f%%".
	Format string

	// bound is the last value the binding produced. Kept off Value so that
	// clearing a binding in the editor puts the authored string back rather
	// than leaving whatever the level last said.
	bound    string
	hasBound bool
}

// DefaultTextSpec is left-aligned, middle-aligned, opaque white, theme-sized.
func DefaultTextSpec() TextSpec {
	return TextSpec{Color: mgl32.Vec4{1, 1, 1, 1}, VAlign: AlignMiddle}
}

// Shown is the string a node actually draws: the binding's value if it has one,
// otherwise what the author typed.
func (t TextSpec) Shown() string {
	if t.hasBound {
		return t.bound
	}
	return t.Value
}

// ValueSpec is the number a bar, slider or toggle carries, and how the filled
// part of it is painted.
//
// The fill needs its own colour because Style is the *background* — the empty
// half of a health bar. Making the fill a child node instead would let all the
// existing styling machinery apply to it, but the child's rect would then be
// driven by the value rather than by its anchors, which is a second and
// contradictory layout rule for one node. Two extra fields is the cheaper lie.
type ValueSpec struct {
	Value, Min, Max float32
	Step            float32 // slider quantisation; 0 is continuous
	Dir             FillDir
	On              bool // toggle state

	// FillColor and FillTexture paint the filled portion. The fill is a plain
	// quad, never nine-sliced: a stretching border on something whose whole job
	// is to change width every frame reads as jitter.
	FillColor   mgl32.Vec4
	FillTexture string

	Bind    string
	BindMax string

	bound, boundMax       float32
	hasBound, hasBoundMax bool
}

// DefaultValueSpec runs 0..1, filling left to right in the theme's bar colour.
func DefaultValueSpec() ValueSpec {
	return ValueSpec{Max: 1, FillColor: DefaultTheme().BarFill}
}

// Current is the value a node actually draws, preferring its binding.
func (v ValueSpec) Current() float32 {
	if v.hasBound {
		return v.bound
	}
	return v.Value
}

// Ceiling is the value a full bar means, preferring its binding. Never zero, so
// that Fraction cannot divide by one.
func (v ValueSpec) Ceiling() float32 {
	m := v.Max
	if v.hasBoundMax {
		m = v.boundMax
	}
	if m == v.Min {
		return v.Min + 1
	}
	return m
}

// Fraction is how full a bar is, clamped to 0..1.
func (v ValueSpec) Fraction() float32 {
	f := (v.Current() - v.Min) / (v.Ceiling() - v.Min)
	return clamp01(f)
}

// VideoSpec is the clip a video node shows, and the transport it plays under.
//
// The authored half — which file, whether it starts by itself, whether it loops
// — is four fields and is what the .fui carries. The rest is where playback has
// got to, and it is unexported for TextSpec's reason: a document is cloned
// every time a screen is shown, and a clone that came with somebody else's
// playhead in it would be a cutscene that started half way through the second
// time it was watched.
//
// Nothing here decodes. internal/video owns the file and the clock, and the two
// are joined by VideoDriver — the same shape as the resolver a binding is
// evaluated through, and for the same reason: a package that has never heard of
// a GPU cannot be the one holding a decoded frame.
type VideoSpec struct {
	// Source is a name under resources/video, without the extension.
	Source string
	// AutoPlay starts the clip the first time the widget is ticked. Off, the
	// widget sits on the first frame until something says `playing: true` —
	// which is what a cutscene triggered by a script wants.
	AutoPlay bool
	Loop     bool
	Muted    bool
	// Volume scales the soundtrack, 0 to 1, on top of the music slider.
	Volume float32

	// ---- transport, never serialised ----

	// playing is what the widget wants; the driver makes it so and reports
	// back. Separate from the player's own state because the widget exists
	// before the player does — a screen ticked once before its file is opened
	// still has to remember that it was told to play.
	playing bool
	// started records that AutoPlay has been acted on, so pausing a clip that
	// autoplayed does not have it start again on the next tick.
	started bool

	seekTo  float32
	seeking bool

	// tex is the name the driver registered this widget's current frame under.
	// It lives on the node because the draw walk has a node and nothing else:
	// the key it was built from belongs to the stack, which by then is three
	// call frames away and in another package.
	tex string

	time     float32
	duration float32
	ended    bool
	// justEnded is the edge, held for the stack to turn into an event on the
	// same tick. A flag rather than a return value because the driver reports
	// the level — "this clip is at its end" — and only the stack knows which
	// screen the widget is on and therefore who to tell.
	justEnded bool
}

// DefaultVideoSpec is a stopped, unmuted clip at full volume playing once.
func DefaultVideoSpec() VideoSpec { return VideoSpec{Volume: 1} }

// VideoTransport is everything a driver needs in order to make a player match a
// widget: what to play, and how it should be running right now.
type VideoTransport struct {
	Source  string
	Loop    bool
	Muted   bool
	Volume  float32
	Playing bool

	// Seek is a position the widget has been told to jump to since the last
	// tick, valid only when Seeking. A one-shot rather than a level, because
	// "go to 0" repeated every frame would pin a playing clip to its first
	// frame forever.
	Seek    float32
	Seeking bool
}

// TakeVideoTransport reads a video node's wanted state, applying AutoPlay the
// first time and consuming any pending seek.
//
// Named Take because it consumes: calling it twice in one frame loses the seek,
// which is why only a driver calls it.
func (n *Node) TakeVideoTransport() VideoTransport {
	v := &n.Video
	if !v.started {
		v.started = true
		v.playing = v.AutoPlay
	}
	t := VideoTransport{
		Source:  v.Source,
		Loop:    v.Loop,
		Muted:   v.Muted,
		Volume:  v.Volume,
		Playing: v.playing,
		Seek:    v.seekTo,
		Seeking: v.seeking,
	}
	v.seeking = false
	return t
}

// NoteVideo records where the driver's player actually got to.
//
// The rising edge of `ended` is latched rather than reported, because the
// driver does not know which screen the node is on and so cannot post the
// `:finished:` event itself. The stack reads the latch on the same tick.
func (n *Node) NoteVideo(time, duration float32, playing, ended bool) {
	v := &n.Video
	v.time, v.duration = time, duration
	if ended && !v.ended {
		v.justEnded = true
	}
	v.ended = ended
	v.playing = playing
}

// VideoTexture is the name this widget's current frame is registered under, or
// empty before a driver has produced one.
func (n *Node) VideoTexture() string { return n.Video.tex }

// NoteVideoTexture records that name. Called by the driver, alongside
// NoteVideo.
func (n *Node) NoteVideoTexture(name string) { n.Video.tex = name }

// VideoPlaying reports whether the clip is running.
func (n *Node) VideoPlaying() bool { return n.Video.playing }

// SetVideoPlaying starts or stops the clip. Starting one that has run out
// rewinds it, which is the only reading of "play this" that does anything.
func (n *Node) SetVideoPlaying(on bool) {
	n.Video.started = true
	if on && n.Video.ended {
		n.SeekVideo(0)
	}
	n.Video.playing = on
}

// VideoJustEnded reports and clears the "reached the end on this tick" latch.
func (n *Node) VideoJustEnded() bool {
	if !n.Video.justEnded {
		return false
	}
	n.Video.justEnded = false
	return true
}

// VideoTime is where playback has got to, in seconds.
func (n *Node) VideoTime() float32 { return n.Video.time }

// SeekVideo queues a jump to a position in seconds, applied on the next tick.
// The reported time moves immediately so that a script reading it back in the
// same tick sees where it asked to be rather than where it was.
func (n *Node) SeekVideo(sec float32) {
	if sec < 0 {
		sec = 0
	}
	n.Video.seekTo, n.Video.seeking = sec, true
	n.Video.time, n.Video.ended, n.Video.justEnded = sec, false, false
}

// VideoDuration is how long the clip runs, or zero before it has been opened.
func (n *Node) VideoDuration() float32 { return n.Video.duration }

// VideoEnded reports whether a non-looping clip has run out.
func (n *Node) VideoEnded() bool { return n.Video.ended }

// Nav names the node focus moves to in each direction. An empty entry falls
// back to the geometric search in ui_nav.go.
type Nav struct{ Up, Down, Left, Right string }

// IncludeSpec points a KindInclude node at another document.
type IncludeSpec struct {
	// Source is a screen name under resources/ui, without the extension.
	Source string
	// Params are substituted into {{name}} in the spliced subtree's text,
	// textures and actions. Deliberately not a general override list: per-node
	// overrides need a serialised diff and a three-way merge, which is a
	// separate feature rather than a bigger version of this one.
	Params map[string]string
}

// Node is one widget.
//
// One flat struct with per-kind sub-structs rather than an interface per kind:
// the editor's panel switches on Kind anyway, Clone stays a shallow copy plus a
// few slices, and the Raw form maps one-to-one onto it. particle.Layer makes
// the same call for the same reasons.
type Node struct {
	ID   string
	Kind Kind
	Rect RectXf

	Visible bool
	Enabled bool    // interactive kinds: false greys out and refuses input
	Alpha   float32 // multiplies down the whole subtree
	Clip    bool    // scissor children to this rect
	Blocks  bool    // swallow a click landing here even if this kind is inert

	Style   Style
	Text    TextSpec
	Value   ValueSpec
	Video   VideoSpec
	Action  string // the atom posted on activation; empty uses the kind's default
	Nav     Nav
	Include IncludeSpec

	// The same tags/attributes/script/params a level object carries, so a
	// widget is addressable from flang on exactly the same terms as an entity
	// — and so the editor's Scripting panel works on one without a second
	// implementation of it.
	scriptdata.ScriptData

	Children []Node

	// ---- solved each frame, never serialised ----
	solved      Rect
	solvedClip  Rect
	culled      bool
	fromInclude bool
}

// NewNode is a node of a kind with the defaults an author expects: visible,
// enabled, opaque, filling its parent's top-left corner.
func NewNode(id string, kind Kind) Node {
	return Node{
		ID:      id,
		Kind:    kind,
		Rect:    DefaultRectXf(),
		Visible: true,
		Enabled: true,
		Alpha:   1,
		Style:   DefaultStyleFor(kind),
		Text:    DefaultTextSpec(),
		Value:   DefaultValueSpec(),
		Video:   DefaultVideoSpec(),
	}
}

// Solved is the node's rectangle as of the last Solve, in canvas units.
func (n *Node) Solved() Rect { return n.solved }

// SolvedClip is the scissor rect in force for this node, already intersected
// with every clipping ancestor.
func (n *Node) SolvedClip() Rect { return n.solvedClip }

// Culled reports whether the node fell entirely outside its clip.
func (n *Node) Culled() bool { return n.culled }

// FromInclude reports whether the node was spliced in from another document.
// The editor draws these read-only: editing one would write changes into a file
// that is not the one being edited.
func (n *Node) FromInclude() bool { return n.fromInclude }

// Clone is a deep copy. Solved state comes along because a snapshot taken
// mid-frame and restored later should not read as unsolved.
func (n *Node) Clone() Node {
	out := *n
	out.ScriptData = scriptdata.CloneScriptData(n.ScriptData)
	out.Include.Params = cloneStrMap(n.Include.Params)
	out.Style.Hover = cloneState(n.Style.Hover)
	out.Style.Press = cloneState(n.Style.Press)
	out.Style.Disabled = cloneState(n.Style.Disabled)
	if n.Children != nil {
		out.Children = make([]Node, len(n.Children))
		for i := range n.Children {
			out.Children[i] = n.Children[i].Clone()
		}
	}
	return out
}

// Find returns the node with an ID anywhere in this subtree, or nil.
func (n *Node) Find(id string) *Node {
	if n.ID == id {
		return n
	}
	for i := range n.Children {
		if got := n.Children[i].Find(id); got != nil {
			return got
		}
	}
	return nil
}

// Walk visits this node and its descendants, parents first and siblings in
// array order — which is draw order. Returning false from fn skips that node's
// children. The parent passed with the root is nil.
func (n *Node) Walk(fn func(parent, node *Node) bool) { n.walk(nil, fn) }

func (n *Node) walk(parent *Node, fn func(parent, node *Node) bool) {
	if !fn(parent, n) {
		return
	}
	for i := range n.Children {
		n.Children[i].walk(n, fn)
	}
}

// ScaleMode decides what a document's numbers mean at a resolution other than
// the one it was authored at.
type ScaleMode int

const (
	// ScaleWithScreen treats the numbers as being in reference-resolution units
	// and scales them, so an authored HUD is the same shape at 720p and 4K.
	ScaleWithScreen ScaleMode = iota
	// ScaleConstantPixel treats one canvas unit as one device pixel, for a
	// pixel-art HUD where a fractional scale would blur the sprite.
	ScaleConstantPixel
)

// ScaleModeNames is each scale mode's name.
var ScaleModeNames = []string{"screen", "pixel"}

// Canvas is how a screen maps onto whatever it is being drawn into.
type Canvas struct {
	Mode       ScaleMode
	RefW, RefH float32
	// Match blends the width-driven and height-driven scale: 0 matches width,
	// 1 matches height, 0.5 splits the difference. Blending geometrically
	// rather than linearly is what keeps a square element square.
	Match float32
	// PixelSnap rounds solved rects to whole device pixels at draw time.
	// Without it a 1px border and a text baseline shimmer as a window resizes.
	PixelSnap bool
}

// DefaultCanvas is 1920x1080 reference, split-difference match, snapped.
func DefaultCanvas() Canvas {
	return Canvas{RefW: 1920, RefH: 1080, Match: 0.5, PixelSnap: true}
}

// Screen is one .fui document: a canvas, some input semantics, and a tree.
type Screen struct {
	ID     string
	Canvas Canvas

	// The three input semantics. A HUD sets none of them; a pause menu sets all
	// three; a loading screen sets Modal and Pauses. One mechanism, three uses,
	// which is why there is no application-state enum anywhere in the engine.
	Modal       bool // nothing below this screen sees input
	Pauses      bool // gameplay does not tick while this screen is up
	ShowsCursor bool // mouse-look is released while this screen is up

	// InitialFocus is the node keyboard focus starts on. Empty picks the first
	// interactive node in tree order.
	InitialFocus string

	// Root is always a panel filling the canvas, and is not deletable — the
	// root is the canvas.
	Root Node

	// A screen carries its own script, where @self is the screen. Menu-wide
	// behaviour — which button closes it, what Escape does — belongs to the
	// screen rather than to any one widget on it.
	scriptdata.ScriptData

	// Path is where this was loaded from, empty until the first Save As. Not
	// serialised: a document that has been copied elsewhere should not insist
	// on its old home.
	Path string

	// includes are the screen names this document splices in, at any depth.
	// Recorded so that saving a prefab can drop every cached screen that used
	// it, rather than leaving stale copies of it on the next Show.
	includes []string

	dirty bool
	rev   uint64
}

// Includes are the screen names this document splices in, at any depth.
func (s *Screen) Includes() []string { return s.includes }

// NewScreen is an empty screen with the default canvas and a bare root.
func NewScreen(id string) *Screen {
	s := &Screen{ID: id, Canvas: DefaultCanvas(), Root: NewNode("root", KindPanel)}
	s.Root.Style.Color = mgl32.Vec4{0, 0, 0, 0} // a menu should not start opaque
	normalizeRoot(&s.Root)
	return s
}

// Clone is a deep copy, used as the editor's undo snapshot.
func (s *Screen) Clone() *Screen {
	out := *s
	out.ScriptData = scriptdata.CloneScriptData(s.ScriptData)
	out.Root = s.Root.Clone()
	return &out
}

// Dirty reports whether the document has changed since it was last saved or
// rebuilt. The editor rebuilds derived state once a frame on this, rather than
// once per edit.
func (s *Screen) Dirty() bool { return s.dirty }

// Invalidate marks the document changed and bumps the revision.
func (s *Screen) Invalidate() {
	s.dirty = true
	s.rev++
}

// Clean clears the dirty flag without touching the revision — the document is
// the same document, it has just been consumed.
func (s *Screen) Clean() { s.dirty = false }

// Rev is an edit counter, for caches that key off "has this changed".
func (s *Screen) Rev() uint64 { return s.rev }

// Find returns a node by ID, or nil.
func (s *Screen) Find(id string) *Node { return s.Root.Find(id) }

// UniqueNodeID returns stem, or stem with the lowest suffix that is not taken.
// Mirrors world.UniqueLevelID, because a duplicated widget needs a name for the
// same reason a duplicated entity does.
func (s *Screen) UniqueNodeID(stem string) string {
	if stem == "" {
		stem = "node"
	}
	if s.Find(stem) == nil {
		return stem
	}
	for i := 2; ; i++ {
		candidate := stem + "_" + itoa(i)
		if s.Find(candidate) == nil {
			return candidate
		}
	}
}

// normalizeRoot forces the root to be a full-canvas panel. It is called on load
// and on New rather than validated against, because a document written by an
// older engine should come up working rather than be refused over a field the
// author never sees.
func normalizeRoot(root *Node) {
	root.Kind = KindPanel
	root.Rect = DefaultRectXf()
	root.Visible = true
	root.Clip = false
}

func cloneStrMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneState(s *StateStyle) *StateStyle {
	if s == nil {
		return nil
	}
	out := *s
	if s.Color != nil {
		c := *s.Color
		out.Color = &c
	}
	if s.UV != nil {
		uv := *s.UV
		out.UV = &uv
	}
	return &out
}

func minF(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func maxF(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// itoa is strconv.Itoa for small positive values, kept local so a pure document
// package does not pull strconv in for one call.
func itoa(v int) string {
	if v <= 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
