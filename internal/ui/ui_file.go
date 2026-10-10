package ui

// ui_file.go — the .fui screen document format.
//
// The schema follows particle_file.go's and level_schema.go's conventions:
// pointer fields with omitempty wherever "absent" and "zero" must stay
// distinguishable, name tables rather than raw enum ints so a reordered enum
// cannot silently reinterpret an old file, validation on both read and write so
// a document that would not load back is never written, and WriteFileAtomic so
// a crash mid-save cannot leave half a screen behind.
//
// The pointer-vs-zero rule bites harder here than anywhere else, because most
// of a widget's defaults are *not* zero. Visible and Enabled default to true;
// Alpha and Scale default to one; AnchorMax defaults to {1,1} and Pivot to
// {0.5,0.5}. A plain bool for Visible could not express "hidden", and a plain
// float for Alpha could not express "fully transparent" — both of which an
// author sets deliberately. Where absent really does mean zero — the four
// offsets, a rotation, a nine-slice inset — the field is plain.
//
// Kind is the exception to omitempty that proves the rule: it is always
// written, even for a panel, because it is not a default the author has left
// alone. It is what the thing *is*, and a document where half the nodes do not
// say what they are is unreadable.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/fileutil"
	"foundry/internal/scriptdata"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// UIDirName is where screens live, beside particles/ and models/.
const UIDirName = "ui"

// UIExt is the native document extension.
const UIExt = ".fui"

// maxIncludeDepth bounds how far includes may nest. Deep enough for a button
// inside a row inside a panel inside a menu, shallow enough that a mistake
// stops being a mistake before it stops being recoverable.
const maxIncludeDepth = 8

// Logf reports a problem that was survived rather than failed on: a missing
// include, an unreadable font. internal/script installs it so the message
// reaches the log window. Nil is correct for a headless test, which is why
// nothing here treats a survivable problem as an error return.
var Logf func(format string, args ...any)

func logf(format string, args ...any) {
	if Logf != nil {
		Logf(format, args...)
	}
}

// UIDir is where the editor's open and save dialogs look.
func UIDir() string { return filepath.Join(fileutil.ResourcesPath, UIDirName) }

// UIPath resolves a bare screen name to a full path.
func UIPath(name string) string { return filepath.Join(UIDir(), name+UIExt) }

// ScanScreenFiles returns the sorted screen filenames on disk.
func ScanScreenFiles() []string { return fileutil.ScanFilesByExt(UIDir(), UIExt) }

// ScreenName strips the directory and extension from a path, giving the name a
// level's `ui:` list and an include's `source:` use.
func ScreenName(path string) string {
	return strings.TrimSuffix(filepath.Base(path), UIExt)
}

// ---------- the on-disk shapes ----------

// RawVec2 is a bare {x, y} pair, spelled like world.RawVec3's so the formats
// read alike.
type RawVec2 struct {
	X float32 `yaml:"x"`
	Y float32 `yaml:"y"`
}

// RawColor4 is an {r, g, b, a} quadruple. UI colours carry alpha where a
// particle's tint does not, because a translucent panel is the ordinary case
// rather than the exception.
type RawColor4 struct {
	R float32 `yaml:"r"`
	G float32 `yaml:"g"`
	B float32 `yaml:"b"`
	A float32 `yaml:"a"`
}

// RawUV is the sampled sub-rect: an origin and a span, matching
// render.SpriteInstance.UV's spelling.
type RawUV struct {
	U  float32 `yaml:"u"`
	V  float32 `yaml:"v"`
	DU float32 `yaml:"du"`
	DV float32 `yaml:"dv"`
}

// RawStateStyle is a partial look for one interaction state. Every field is
// optional: an override that sets only a colour keeps the base texture.
type RawStateStyle struct {
	Color   *RawColor4 `yaml:"color,omitempty"`
	Texture string     `yaml:"texture,omitempty"`
	UV      *RawUV     `yaml:"uv,omitempty"`
}

// RawText is a node's string and how it is drawn.
type RawText struct {
	Value  string     `yaml:"value,omitempty"`
	Font   string     `yaml:"font,omitempty"`
	Size   *float32   `yaml:"size,omitempty"`
	Color  *RawColor4 `yaml:"color,omitempty"`
	HAlign string     `yaml:"h_align,omitempty"`
	VAlign string     `yaml:"v_align,omitempty"`
	Wrap   bool       `yaml:"wrap,omitempty"`
	Bind   string     `yaml:"bind,omitempty"`
	Format string     `yaml:"format,omitempty"`
}

// RawValue is a node's number.
type RawValue struct {
	Value float32  `yaml:"value,omitempty"`
	Min   float32  `yaml:"min,omitempty"`
	Max   *float32 `yaml:"max,omitempty"`
	Step  float32  `yaml:"step,omitempty"`
	Dir   string   `yaml:"dir,omitempty"`
	On    bool     `yaml:"on,omitempty"`

	FillColor   *RawColor4 `yaml:"fill_color,omitempty"`
	FillTexture string     `yaml:"fill_texture,omitempty"`

	Bind    string `yaml:"bind,omitempty"`
	BindMax string `yaml:"bind_max,omitempty"`
}

// RawVideo is a video node's clip and how it plays.
//
// Volume is a pointer because its default is one: a plain float could not
// express a video authored silent, which is a thing somebody sets deliberately
// on a background loop. Muted is the switch for "silent for now"; volume zero
// is "this clip has no soundtrack worth hearing".
type RawVideo struct {
	Source   string   `yaml:"source,omitempty"`
	AutoPlay bool     `yaml:"autoplay,omitempty"`
	Loop     bool     `yaml:"loop,omitempty"`
	Muted    bool     `yaml:"muted,omitempty"`
	Volume   *float32 `yaml:"volume,omitempty"`
}

// RawNav is the four hand-wired focus links.
type RawNav struct {
	Up    string `yaml:"up,omitempty"`
	Down  string `yaml:"down,omitempty"`
	Left  string `yaml:"left,omitempty"`
	Right string `yaml:"right,omitempty"`
}

// RawInclude points at another document.
type RawInclude struct {
	Source string            `yaml:"source,omitempty"`
	Params map[string]string `yaml:"params,omitempty"`
}

// RawCanvas is how a screen maps onto a framebuffer.
type RawCanvas struct {
	Mode      string   `yaml:"mode,omitempty"`
	RefW      *float32 `yaml:"ref_w,omitempty"`
	RefH      *float32 `yaml:"ref_h,omitempty"`
	Match     *float32 `yaml:"match,omitempty"`
	PixelSnap *bool    `yaml:"pixel_snap,omitempty"`
}

// RawNode is one widget on disk.
type RawNode struct {
	ID   string `yaml:"id"`
	Kind string `yaml:"kind"`

	AnchorMin *RawVec2  `yaml:"anchor_min,omitempty"`
	AnchorMax *RawVec2  `yaml:"anchor_max,omitempty"`
	Offset    []float32 `yaml:"offset,omitempty"` // left, top, right, bottom
	Pivot     *RawVec2  `yaml:"pivot,omitempty"`
	Rotation  float32   `yaml:"rotation,omitempty"`
	Scale     *RawVec2  `yaml:"scale,omitempty"`

	Visible *bool    `yaml:"visible,omitempty"`
	Enabled *bool    `yaml:"enabled,omitempty"`
	Alpha   *float32 `yaml:"alpha,omitempty"`
	Clip    bool     `yaml:"clip,omitempty"`
	Blocks  bool     `yaml:"blocks,omitempty"`
	// Draggable: see Node.Draggable.
	Draggable bool `yaml:"draggable,omitempty"`

	Color    *RawColor4     `yaml:"color,omitempty"`
	Texture  string         `yaml:"texture,omitempty"`
	UV       *RawUV         `yaml:"uv,omitempty"`
	Slice    []float32      `yaml:"slice,omitempty"` // left, top, right, bottom
	Fill     string         `yaml:"fill,omitempty"`
	Hover    *RawStateStyle `yaml:"hover,omitempty"`
	Press    *RawStateStyle `yaml:"press,omitempty"`
	Disabled *RawStateStyle `yaml:"disabled,omitempty"`

	Text  *RawText  `yaml:"text,omitempty"`
	Value *RawValue `yaml:"value,omitempty"`
	Video *RawVideo `yaml:"video,omitempty"`

	Action  string      `yaml:"action,omitempty"`
	Nav     *RawNav     `yaml:"nav,omitempty"`
	Include *RawInclude `yaml:"include,omitempty"`

	RawScriptData `yaml:",inline"`

	Children []RawNode `yaml:"children,omitempty"`
}

// RawScreen is one .fui document.
type RawScreen struct {
	ID           string     `yaml:"id"`
	Canvas       *RawCanvas `yaml:"canvas,omitempty"`
	Modal        bool       `yaml:"modal,omitempty"`
	Pauses       bool       `yaml:"pauses,omitempty"`
	ShowsCursor  bool       `yaml:"shows_cursor,omitempty"`
	ClickThrough bool       `yaml:"click_through,omitempty"`
	InitialFocus string     `yaml:"initial_focus,omitempty"`

	RawScriptData `yaml:",inline"`

	Root RawNode `yaml:"root"`
}

// RawScriptData is the tags/attributes/script/params block, spelled exactly as
// a level object writes it — the same keys mean the same things, so an author
// who has scripted a door has already scripted a button.
//
// Embedded inline rather than nested under a "script:" key so that a widget's
// three lines sit beside its other properties rather than in a sub-block of
// their own.
type RawScriptData struct {
	Tags         []string                           `yaml:"tags,omitempty"`
	Attributes   map[string]scriptdata.RawAttribute `yaml:"attributes,omitempty"`
	Script       string                             `yaml:"script,omitempty"`
	ScriptParams map[string]scriptdata.RawAttribute `yaml:"params,omitempty"`
}

// rawToScriptData converts the block, reporting a malformed attribute rather
// than dropping it: silently losing a value an author wrote is worse than
// refusing to open the screen.
func rawToScriptData(raw RawScriptData, what string) (scriptdata.ScriptData, error) {
	attrs, err := scriptdata.AttrsFromRaw(raw.Attributes)
	if err != nil {
		return scriptdata.ScriptData{}, fmt.Errorf("%s: %w", what, err)
	}
	params, err := scriptdata.AttrsFromRaw(raw.ScriptParams)
	if err != nil {
		return scriptdata.ScriptData{}, fmt.Errorf("%s: %w", what, err)
	}
	return scriptdata.ScriptData{
		Tags:         scriptdata.NormalizeTags(raw.Tags),
		Attributes:   attrs,
		Script:       raw.Script,
		ScriptParams: params,
	}, nil
}

func scriptDataToRaw(d scriptdata.ScriptData) RawScriptData {
	return RawScriptData{
		Tags:         scriptdata.NormalizeTags(d.Tags),
		Attributes:   scriptdata.AttrsToRaw(d.Attributes),
		Script:       d.Script,
		ScriptParams: scriptdata.AttrsToRaw(d.ScriptParams),
	}
}

// ---------- reading ----------

// LoadScreen reads a document and splices its includes in.
//
// A broken include is logged and kept rather than failed on, the rule
// levelio.rawToLevel already follows for a broken asset reference: losing one
// prefab should not take a whole menu down, and an author needs to see the rest
// of the screen in order to fix it.
func LoadScreen(path string) (*Screen, error) {
	s, err := loadScreenBare(path)
	if err != nil {
		return nil, err
	}
	s.includes = resolveIncludes(&s.Root, []string{path}, 0)
	return s, nil
}

// ---------- the loaded-screen cache ----------

// screenCache holds one parsed copy of each screen. Show hands out a Clone of
// it rather than the cached instance itself: two stacks showing the same screen
// — the game's and the editor's preview — must not share solved rects, bound
// values or a toggle's state.
var screenCache = map[string]*Screen{}

func cachedScreenClone(name string) (*Screen, error) {
	s, ok := screenCache[name]
	if !ok {
		var err error
		s, err = LoadScreen(UIPath(name))
		if err != nil {
			return nil, err
		}
		screenCache[name] = s
	}
	return s.Clone(), nil
}

// InvalidateScreen drops a screen from the cache, along with every cached
// screen that includes it — saving a prefab has to reach the menus using it,
// not just the prefab's own entry.
func InvalidateScreen(name string) {
	delete(screenCache, name)
	for cached, s := range screenCache {
		for _, inc := range s.includes {
			if inc == name {
				delete(screenCache, cached)
				break
			}
		}
	}
}

// InvalidateAllScreens empties the cache.
func InvalidateAllScreens() { screenCache = map[string]*Screen{} }

// loadScreenBare reads one document without touching its includes.
func loadScreenBare(path string) (*Screen, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw RawScreen
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	if err := validateRawScreen(raw); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	s := rawToScreen(raw)
	s.Path = path
	if s.ID == "" {
		s.ID = ScreenName(path)
	}
	return s, nil
}

func rawToScreen(raw RawScreen) *Screen {
	s := &Screen{
		ID:           raw.ID,
		Canvas:       rawToCanvas(raw.Canvas),
		Modal:        raw.Modal,
		Pauses:       raw.Pauses,
		ShowsCursor:  raw.ShowsCursor,
		ClickThrough: raw.ClickThrough,
		InitialFocus: raw.InitialFocus,
		Root:         rawToNode(raw.Root),
	}
	// A malformed attribute has already failed validation by the time this
	// runs, so the error here cannot fire; taking the value and dropping it
	// keeps the conversion total.
	s.ScriptData, _ = rawToScriptData(raw.RawScriptData, "screen "+raw.ID)
	normalizeRoot(&s.Root)
	return s
}

func rawToCanvas(raw *RawCanvas) Canvas {
	def := DefaultCanvas()
	if raw == nil {
		return def
	}
	return Canvas{
		Mode:      ScaleMode(nameIndex(ScaleModeNames, raw.Mode, int(def.Mode))),
		RefW:      f32Default(raw.RefW, def.RefW),
		RefH:      f32Default(raw.RefH, def.RefH),
		Match:     f32Default(raw.Match, def.Match),
		PixelSnap: boolDefault(raw.PixelSnap, def.PixelSnap),
	}
}

func rawToNode(raw RawNode) Node {
	n := NewNode(raw.ID, KindFromName(raw.Kind))

	n.Rect.AnchorMin = vec2Default(raw.AnchorMin, mgl32.Vec2{0, 0})
	n.Rect.AnchorMax = vec2Default(raw.AnchorMax, mgl32.Vec2{1, 1})
	n.Rect.Offset = quadDefault(raw.Offset)
	n.Rect.Pivot = vec2Default(raw.Pivot, mgl32.Vec2{0.5, 0.5})
	n.Rect.Rotation = raw.Rotation
	n.Rect.Scale = vec2Default(raw.Scale, mgl32.Vec2{1, 1})
	clampAnchors(&n.Rect)

	n.Visible = boolDefault(raw.Visible, true)
	n.Enabled = boolDefault(raw.Enabled, true)
	n.Alpha = f32Default(raw.Alpha, 1)
	n.Clip = raw.Clip
	n.Blocks = raw.Blocks
	n.Draggable = raw.Draggable

	n.Style.Color = color4Default(raw.Color, DefaultBackground(n.Kind))
	n.Style.Texture = raw.Texture
	n.Style.UV = uvDefault(raw.UV)
	n.Style.Slice = quadDefault(raw.Slice)
	n.Style.Fill = FillMode(nameIndex(FillModeNames, raw.Fill, int(DefaultFill(n.Kind))))
	n.Style.Hover = rawToState(raw.Hover)
	n.Style.Press = rawToState(raw.Press)
	n.Style.Disabled = rawToState(raw.Disabled)

	if t := raw.Text; t != nil {
		n.Text = TextSpec{
			Value:  t.Value,
			Font:   t.Font,
			Size:   f32Default(t.Size, 0),
			Color:  color4Default(t.Color, mgl32.Vec4{1, 1, 1, 1}),
			HAlign: HAlign(nameIndex(HAlignNames, t.HAlign, int(AlignLeft))),
			VAlign: VAlign(nameIndex(VAlignNames, t.VAlign, int(AlignMiddle))),
			Wrap:   t.Wrap,
			Bind:   t.Bind,
			Format: t.Format,
		}
	}
	if vd := raw.Video; vd != nil {
		n.Video = VideoSpec{
			Source:   vd.Source,
			AutoPlay: vd.AutoPlay,
			Loop:     vd.Loop,
			Muted:    vd.Muted,
			Volume:   clamp01(f32Default(vd.Volume, 1)),
		}
	}
	if v := raw.Value; v != nil {
		n.Value = ValueSpec{
			Value:       v.Value,
			Min:         v.Min,
			Max:         f32Default(v.Max, 1),
			Step:        v.Step,
			Dir:         FillDir(nameIndex(FillDirNames, v.Dir, int(FillLeftRight))),
			On:          v.On,
			FillColor:   color4Default(v.FillColor, DefaultTheme().BarFill),
			FillTexture: v.FillTexture,
			Bind:        v.Bind,
			BindMax:     v.BindMax,
		}
	}

	n.ScriptData, _ = rawToScriptData(raw.RawScriptData, "node "+raw.ID)

	n.Action = raw.Action
	if nv := raw.Nav; nv != nil {
		n.Nav = Nav{Up: nv.Up, Down: nv.Down, Left: nv.Left, Right: nv.Right}
	}
	if inc := raw.Include; inc != nil {
		n.Include = IncludeSpec{Source: inc.Source, Params: cloneStrMap(inc.Params)}
	}

	if len(raw.Children) > 0 {
		n.Children = make([]Node, len(raw.Children))
		for i, c := range raw.Children {
			n.Children[i] = rawToNode(c)
		}
	}
	return n
}

func rawToState(raw *RawStateStyle) *StateStyle {
	if raw == nil {
		return nil
	}
	out := &StateStyle{Texture: raw.Texture}
	if raw.Color != nil {
		c := mgl32.Vec4{raw.Color.R, raw.Color.G, raw.Color.B, raw.Color.A}
		out.Color = &c
	}
	if raw.UV != nil {
		uv := mgl32.Vec4{raw.UV.U, raw.UV.V, raw.UV.DU, raw.UV.DV}
		out.UV = &uv
	}
	return out
}

// ---------- writing ----------

// Fingerprint identifies the screen's content as SaveScreen would write it,
// for comparing against what was last saved. See fileutil.Fingerprint.
func Fingerprint(s *Screen) uint64 { return fileutil.Fingerprint(screenToRaw(s)) }

// SaveScreen validates and writes a document.
//
// Spliced include children are not written: only the include node itself is,
// and the splice is re-derived on the next load. Writing them would fork the
// prefab the moment anyone saved a screen that used it.
func SaveScreen(s *Screen, path string) error {
	raw := screenToRaw(s)
	if err := validateRawScreen(raw); err != nil {
		return err
	}
	data, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomic(path, data); err != nil {
		return err
	}
	s.Path = path
	s.Clean()
	return nil
}

func screenToRaw(s *Screen) RawScreen {
	return RawScreen{
		ID:           s.ID,
		Canvas:       canvasToRaw(s.Canvas),
		Modal:        s.Modal,
		Pauses:       s.Pauses,
		ShowsCursor:  s.ShowsCursor,
		ClickThrough: s.ClickThrough,
		InitialFocus: s.InitialFocus,

		RawScriptData: scriptDataToRaw(s.ScriptData),

		Root: nodeToRaw(&s.Root),
	}
}

func canvasToRaw(c Canvas) *RawCanvas {
	def := DefaultCanvas()
	raw := RawCanvas{
		Mode:      nameIfNot(ScaleModeNames[c.Mode], ScaleModeNames[def.Mode]),
		RefW:      f32PtrIfNot(c.RefW, def.RefW),
		RefH:      f32PtrIfNot(c.RefH, def.RefH),
		Match:     f32PtrIfNot(c.Match, def.Match),
		PixelSnap: boolPtrIfNot(c.PixelSnap, def.PixelSnap),
	}
	if raw == (RawCanvas{}) {
		return nil
	}
	return &raw
}

func nodeToRaw(n *Node) RawNode {
	raw := RawNode{
		ID:   n.ID,
		Kind: KindName(n.Kind),

		AnchorMin: vec2PtrIfNot(n.Rect.AnchorMin, mgl32.Vec2{0, 0}),
		AnchorMax: vec2PtrIfNot(n.Rect.AnchorMax, mgl32.Vec2{1, 1}),
		Offset:    quadIfAny(n.Rect.Offset),
		Pivot:     vec2PtrIfNot(n.Rect.Pivot, mgl32.Vec2{0.5, 0.5}),
		Rotation:  n.Rect.Rotation,
		Scale:     vec2PtrIfNot(n.Rect.Scale, mgl32.Vec2{1, 1}),

		Visible: boolPtrIfNot(n.Visible, true),
		Enabled: boolPtrIfNot(n.Enabled, true),
		Alpha:   f32PtrIfNot(n.Alpha, 1),
		Clip:    n.Clip,
		Blocks:  n.Blocks,

		Draggable: n.Draggable,

		Color:    color4PtrIfNot(n.Style.Color, DefaultBackground(n.Kind)),
		Texture:  n.Style.Texture,
		UV:       uvPtrIfNotFull(n.Style.UV),
		Slice:    quadIfAny(n.Style.Slice),
		Fill:     nameIfNot(FillModeNames[n.Style.Fill], FillModeNames[DefaultFill(n.Kind)]),
		Hover:    stateToRaw(n.Style.Hover),
		Press:    stateToRaw(n.Style.Press),
		Disabled: stateToRaw(n.Style.Disabled),

		Text:  textToRaw(n.Text),
		Value: valueToRaw(n.Value),
		Video: videoToRaw(n.Video),

		Action:  n.Action,
		Nav:     navToRaw(n.Nav),
		Include: includeToRaw(n.Include),

		RawScriptData: scriptDataToRaw(n.ScriptData),
	}

	// An include's children belong to the document it came from.
	if n.Kind == KindInclude {
		return raw
	}
	for i := range n.Children {
		if n.Children[i].fromInclude {
			continue
		}
		raw.Children = append(raw.Children, nodeToRaw(&n.Children[i]))
	}
	return raw
}

func stateToRaw(s *StateStyle) *RawStateStyle {
	if s == nil {
		return nil
	}
	out := &RawStateStyle{Texture: s.Texture}
	if s.Color != nil {
		out.Color = &RawColor4{R: s.Color[0], G: s.Color[1], B: s.Color[2], A: s.Color[3]}
	}
	if s.UV != nil {
		out.UV = &RawUV{U: s.UV[0], V: s.UV[1], DU: s.UV[2], DV: s.UV[3]}
	}
	return out
}

func textToRaw(t TextSpec) *RawText {
	raw := RawText{
		Value:  t.Value,
		Font:   t.Font,
		Size:   f32PtrIfNot(t.Size, 0),
		Color:  color4PtrIfNot(t.Color, mgl32.Vec4{1, 1, 1, 1}),
		HAlign: nameIfNot(HAlignNames[t.HAlign], HAlignNames[AlignLeft]),
		VAlign: nameIfNot(VAlignNames[t.VAlign], VAlignNames[AlignMiddle]),
		Wrap:   t.Wrap,
		Bind:   t.Bind,
		Format: t.Format,
	}
	if raw == (RawText{}) {
		return nil
	}
	return &raw
}

func valueToRaw(v ValueSpec) *RawValue {
	raw := RawValue{
		Value:       v.Value,
		Min:         v.Min,
		Max:         f32PtrIfNot(v.Max, 1),
		Step:        v.Step,
		Dir:         nameIfNot(FillDirNames[v.Dir], FillDirNames[FillLeftRight]),
		On:          v.On,
		FillColor:   color4PtrIfNot(v.FillColor, DefaultTheme().BarFill),
		FillTexture: v.FillTexture,
		Bind:        v.Bind,
		BindMax:     v.BindMax,
	}
	if raw == (RawValue{}) {
		return nil
	}
	return &raw
}

func videoToRaw(v VideoSpec) *RawVideo {
	raw := RawVideo{
		Source:   v.Source,
		AutoPlay: v.AutoPlay,
		Loop:     v.Loop,
		Muted:    v.Muted,
		Volume:   f32PtrIfNot(v.Volume, 1),
	}
	if raw == (RawVideo{}) {
		return nil
	}
	return &raw
}

func navToRaw(nv Nav) *RawNav {
	if nv == (Nav{}) {
		return nil
	}
	return &RawNav{Up: nv.Up, Down: nv.Down, Left: nv.Left, Right: nv.Right}
}

func includeToRaw(inc IncludeSpec) *RawInclude {
	if inc.Source == "" && len(inc.Params) == 0 {
		return nil
	}
	return &RawInclude{Source: inc.Source, Params: cloneStrMap(inc.Params)}
}

// ---------- validation ----------

// validateRawScreen refuses a document that would not survive a round trip, on
// both read and write. Running it on the way out is what makes "saved" mean
// "will load", rather than "was written".
func validateRawScreen(raw RawScreen) error {
	if raw.Root.ID == "" {
		return fmt.Errorf("root node has no id")
	}
	if _, err := rawToScriptData(raw.RawScriptData, "screen "+raw.ID); err != nil {
		return err
	}
	seen := map[string]bool{}
	return validateRawNode(raw.Root, seen)
}

func validateRawNode(raw RawNode, seen map[string]bool) error {
	if raw.ID == "" {
		return fmt.Errorf("a node has no id")
	}
	if seen[raw.ID] {
		// IDs are how a script and the editor address a widget. Two nodes
		// sharing one means every reference to it is a coin flip.
		return fmt.Errorf("two nodes share the id %q", raw.ID)
	}
	seen[raw.ID] = true

	if _, err := rawToScriptData(raw.RawScriptData, "node "+raw.ID); err != nil {
		return err
	}

	kind := KindFromName(raw.Kind)
	if raw.Kind != "" && KindName(kind) != raw.Kind {
		return fmt.Errorf("node %q has unknown kind %q", raw.ID, raw.Kind)
	}
	if raw.Rotation != 0 && !kind.Leaf() {
		// A rotated container would need oriented-box hit testing and an
		// oriented scissor rect, and GL scissor cannot express one.
		return fmt.Errorf("node %q is a %s: only %s may be rotated",
			raw.ID, KindName(kind), strings.Join(LeafKindNames(), ", "))
	}
	if raw.Rotation != 0 && raw.Clip {
		return fmt.Errorf("node %q cannot both clip and rotate", raw.ID)
	}
	if n := len(raw.Offset); n != 0 && n != 4 {
		return fmt.Errorf("node %q has %d offsets, want 4 (left, top, right, bottom)", raw.ID, n)
	}
	if n := len(raw.Slice); n != 0 && n != 4 {
		return fmt.Errorf("node %q has %d slice insets, want 4 (left, top, right, bottom)", raw.ID, n)
	}

	for _, c := range raw.Children {
		if err := validateRawNode(c, seen); err != nil {
			return err
		}
	}
	return nil
}

// ---------- includes ----------

// resolveIncludes splices every include node's source in, depth-first, and
// reports every source name it reached.
//
// chain is the stack of document paths currently open, which is what detects a
// cycle: a prefab that includes itself, directly or through two others, would
// otherwise recurse until the stack ran out.
func resolveIncludes(n *Node, chain []string, depth int) []string {
	if n.Kind == KindInclude {
		var used []string
		n.Children, used = spliceInclude(n, chain, depth)
		return used
	}
	var used []string
	for i := range n.Children {
		used = append(used, resolveIncludes(&n.Children[i], chain, depth)...)
	}
	return used
}

func spliceInclude(n *Node, chain []string, depth int) ([]Node, []string) {
	if n.Include.Source == "" {
		return nil, nil
	}
	path := UIPath(n.Include.Source)

	if depth >= maxIncludeDepth {
		logf("ui: %q: includes nested more than %d deep, stopping", n.ID, maxIncludeDepth)
		return nil, nil
	}
	for _, open := range chain {
		if open == path {
			logf("ui: %q includes %q, which is already open — cycle ignored", n.ID, n.Include.Source)
			return nil, nil
		}
	}

	src, err := loadScreenBare(path)
	if err != nil {
		logf("ui: %q includes %q: %v", n.ID, n.Include.Source, err)
		return nil, nil
	}

	used := append([]string{n.Include.Source}, resolveIncludes(&src.Root, append(chain, path), depth+1)...)

	out := make([]Node, 0, len(src.Root.Children))
	for i := range src.Root.Children {
		c := src.Root.Children[i].Clone()
		// IDs are prefixed with the include node's own, so twenty copies of one
		// prefab do not all answer to "label". It also means a script can
		// address a spliced widget at all, which it could not if the ids
		// collided.
		prefixIDs(&c, n.ID+"/")
		substituteParams(&c, n.Include.Params)
		markFromInclude(&c)
		out = append(out, c)
	}
	return out, used
}

func prefixIDs(n *Node, prefix string) {
	n.Walk(func(_, c *Node) bool {
		c.ID = prefix + c.ID
		if c.Nav.Up != "" {
			c.Nav.Up = prefix + c.Nav.Up
		}
		if c.Nav.Down != "" {
			c.Nav.Down = prefix + c.Nav.Down
		}
		if c.Nav.Left != "" {
			c.Nav.Left = prefix + c.Nav.Left
		}
		if c.Nav.Right != "" {
			c.Nav.Right = prefix + c.Nav.Right
		}
		return true
	})
}

// substituteParams replaces {{name}} in the fields a prefab is worth
// parameterising: what it says, what it is drawn with, what it plays, and what
// it does.
//
// Deliberately not every field. A general override mechanism needs a serialised
// diff and a three-way merge against a prefab that has since changed, which is
// a different feature rather than a bigger version of this one.
func substituteParams(n *Node, params map[string]string) {
	if len(params) == 0 {
		return
	}
	n.Walk(func(_, c *Node) bool {
		c.Text.Value = expand(c.Text.Value, params)
		c.Style.Texture = expand(c.Style.Texture, params)
		c.Video.Source = expand(c.Video.Source, params)
		c.Action = expand(c.Action, params)
		return true
	})
}

func expand(s string, params map[string]string) string {
	if s == "" || !strings.Contains(s, "{{") {
		return s
	}
	for k, v := range params {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}

func markFromInclude(n *Node) {
	n.Walk(func(_, c *Node) bool {
		c.fromInclude = true
		return true
	})
}

// ---------- the pointer-vs-zero helpers ----------

func f32Default(p *float32, def float32) float32 {
	if p != nil {
		return *p
	}
	return def
}

func f32PtrIfNot(v, def float32) *float32 {
	if v == def {
		return nil
	}
	return &v
}

func boolDefault(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}

func boolPtrIfNot(v, def bool) *bool {
	if v == def {
		return nil
	}
	return &v
}

func nameIfNot(name, def string) string {
	if name == def {
		return ""
	}
	return name
}

// nameIndex is the inverse of a name table: an unknown name takes the default
// rather than erroring, so a document from a newer engine degrades instead of
// refusing to open.
func nameIndex(names []string, name string, def int) int {
	for i, n := range names {
		if n == name {
			return i
		}
	}
	return def
}

func vec2Default(p *RawVec2, def mgl32.Vec2) mgl32.Vec2 {
	if p == nil {
		return def
	}
	return mgl32.Vec2{p.X, p.Y}
}

func vec2PtrIfNot(v, def mgl32.Vec2) *RawVec2 {
	if v == def {
		return nil
	}
	return &RawVec2{X: v[0], Y: v[1]}
}

func color4Default(c *RawColor4, def mgl32.Vec4) mgl32.Vec4 {
	if c == nil {
		return def
	}
	return mgl32.Vec4{c.R, c.G, c.B, c.A}
}

func color4PtrIfNot(v, def mgl32.Vec4) *RawColor4 {
	if v == def {
		return nil
	}
	return &RawColor4{R: v[0], G: v[1], B: v[2], A: v[3]}
}

// uvDefault treats an absent uv as the whole texture, which is what an author
// who has not thought about it means.
func uvDefault(p *RawUV) mgl32.Vec4 {
	if p == nil {
		return mgl32.Vec4{0, 0, 1, 1}
	}
	return mgl32.Vec4{p.U, p.V, p.DU, p.DV}
}

func uvPtrIfNotFull(v mgl32.Vec4) *RawUV {
	if v == (mgl32.Vec4{0, 0, 1, 1}) {
		return nil
	}
	return &RawUV{U: v[0], V: v[1], DU: v[2], DV: v[3]}
}

// quadDefault reads the four-float lists — offsets and slice insets — treating
// a missing or malformed one as all zeros. Length is validated separately, so
// this only has to be safe rather than strict.
func quadDefault(v []float32) [4]float32 {
	var out [4]float32
	if len(v) != 4 {
		return out
	}
	copy(out[:], v)
	return out
}

func quadIfAny(v [4]float32) []float32 {
	if v == ([4]float32{}) {
		return nil
	}
	return []float32{v[0], v[1], v[2], v[3]}
}

func clampAnchors(r *RectXf) {
	r.AnchorMin = mgl32.Vec2{clamp01(r.AnchorMin[0]), clamp01(r.AnchorMin[1])}
	r.AnchorMax = mgl32.Vec2{clamp01(r.AnchorMax[0]), clamp01(r.AnchorMax[1])}
	if r.AnchorMax[0] < r.AnchorMin[0] {
		r.AnchorMax[0] = r.AnchorMin[0]
	}
	if r.AnchorMax[1] < r.AnchorMin[1] {
		r.AnchorMax[1] = r.AnchorMin[1]
	}
}
