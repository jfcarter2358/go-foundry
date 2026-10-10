package material

// Package material is the .fmat material document: a named bundle of shader
// effects and their parameters, which a surface, sprite, particle layer or sky
// can wear in place of naming a bare texture.
//
// material_doc.go holds the document itself. The engine's effects used to be
// scattered: water lived in a sector's `water:` block, unlit on a brush face's
// `unlit:` flag, the second-texture splat on `blend_texture:`/`blend_mask:`,
// the scroll on an entity attachment's `uv_scroll:`, a sprite's blending in
// its `blend:` enum, a particle layer's in the .fpar. Each was authored in a
// different panel, spelled differently, and could not be reused: painting two
// walls the same shade of animated water meant filling in six numbers twice.
//
// A material is the one place those now live. It names them once, under a name,
// in a file of its own, and anything that can wear a texture can wear it
// instead. Nothing is thrown away to get there — see material_surface.go: a
// material RESOLVES into exactly the per-object fields the renderer already
// read, so the draw path is untouched and a level that names no material
// behaves as it always did.
//
// The package is pure: no GL, no level, no editor, no gfx. That is what lets
// the level compiler and every test here read materials headlessly, and what
// lets internal/gfx import it rather than the other way round.

import (
	"fmt"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// Domain is what a material may be worn by. It decides which effect kinds are
// legal in the document and which panel of the material editor offers it: a
// sprite has no waves, a wall has no billboard, and catching that when the
// document is written beats discovering it as a silently ignored parameter.
type Domain int

const (
	// DomainSurface is a brush face, a compiled surface, a sector floor or
	// ceiling, a model or an entity attachment — anything drawn through the
	// world program (fragment.glsl).
	DomainSurface Domain = iota
	// DomainSprite is a billboard: a level sprite or a scenery quad.
	DomainSprite
	// DomainParticle is one layer of a .fpar effect.
	DomainParticle
	// DomainSky is the sky dome and the cloud deck above it.
	DomainSky
)

// DomainNames is the YAML/UI spelling of each domain, indexed by the domain.
// Adding one means adding its name here and nowhere else, so an unknown name
// cannot quietly load as the wrong domain through some per-file switch that was
// never updated — the convention gfx.SpriteBlendNames set.
var DomainNames = []string{
	"surface",
	"sprite",
	"particle",
	"sky",
}

// DomainName returns a domain's name, falling back to the default.
func DomainName(d Domain) string {
	if int(d) < 0 || int(d) >= len(DomainNames) {
		return DomainNames[DomainSurface]
	}
	return DomainNames[d]
}

// DomainFromName is the inverse. An unknown name loads as a surface rather
// than failing, so a document written by a newer build still opens.
func DomainFromName(name string) Domain {
	for i, n := range DomainNames {
		if n == name {
			return Domain(i)
		}
	}
	return DomainSurface
}

// Doc is one .fmat document: a material.
type Doc struct {
	// Name is the basename with no extension — what a level, a face or a
	// sprite writes to wear it, exactly as a texture is named without its
	// .png. Set by the loader from the filename rather than read from the
	// file, so a renamed file is a renamed material with nothing to keep in
	// step.
	Name string

	// Domain is what may wear it; see Domain.
	Domain Domain

	// Texture is the albedo the material draws with — a texture name, no
	// extension, the same spelling a face's `texture:` uses. Empty means the
	// material supplies no texture of its own and whatever wears it keeps the
	// one it already names, which is what lets a single `scrolling.fmat` slide
	// any wall's own art.
	Texture string

	// Effects are applied in order. Order matters only where two write the same
	// resolved field (the last one wins); the built-in kinds are disjoint, so
	// in practice a document reads as a set.
	Effects []Effect

	// Doc is a free-text note shown in the material editor and in the picker's
	// tooltip. Purely for whoever opens it next.
	Doc string
}

// Effect is one effect on a material: which kind, and what its parameters are
// set to.
type Effect struct {
	// Kind is a registered kind's name (see material_kinds.go). An effect whose
	// kind this build does not know is kept, not dropped — see Unknown.
	Kind string

	// Disabled switches the effect off without deleting it, which is how you
	// compare a wall with and without its scroll. Spelled negatively so that
	// the zero value — and therefore an effect written with no `enabled:` key
	// at all — is on.
	Disabled bool

	// Params are the parameters the author set, by ParamDef.Name. A parameter
	// that is absent reads as its kind's default: that is what lets a new
	// parameter be added to a kind without touching a single document on disk,
	// and what keeps a .fmat down to the handful of numbers that were actually
	// changed.
	Params map[string]Value

	// Unknown holds keys this build did not recognise, so that saving a
	// document written by a NEWER build does not quietly delete the parameters
	// (or the whole effect) it did not understand. Nothing reads it but the
	// writer.
	Unknown map[string]interface{}
}

// ParamType is what one parameter holds.
type ParamType int

const (
	// ParamFloat is a single number.
	ParamFloat ParamType = iota
	// ParamVec2 is an {x, y} pair — a scroll rate, a tiling factor.
	ParamVec2
	// ParamVec3 is an {x, y, z} triple that is not a colour.
	ParamVec3
	// ParamColor is a triple the editor shows as a colour swatch rather than
	// three drag fields. Carried as a distinct type rather than a flag on
	// ParamVec3 so the UI needs no table of its own to know which is which.
	ParamColor
	// ParamBool is a checkbox.
	ParamBool
	// ParamTexture is a texture name, offered through the texture picker.
	ParamTexture
	// ParamEnum is one of ParamDef.Choices, written as its name.
	ParamEnum
)

// ParamTypeNames is the YAML/UI spelling of each type, for the editor and for
// error messages.
var ParamTypeNames = []string{
	"float",
	"vec2",
	"vec3",
	"color",
	"bool",
	"texture",
	"enum",
}

// ParamTypeName returns a type's name.
func ParamTypeName(t ParamType) string {
	if int(t) < 0 || int(t) >= len(ParamTypeNames) {
		return ParamTypeNames[ParamFloat]
	}
	return ParamTypeNames[t]
}

// Value is one parameter's value: a tagged union, so that Effect.Params can be
// one map rather than five.
//
// The numeric payload is a Vec3 whatever the type, with a float in [0], a vec2
// in [0..1] and a colour in [0..2]. One field rather than three saves the
// editor and the YAML writer a switch apiece at the cost of eight bytes per
// parameter, on documents that hold tens of parameters in total.
type Value struct {
	Type ParamType
	Num  mgl32.Vec3
	Bool bool
	Str  string // texture name, or an enum choice
}

// FloatValue, Vec2Value, Vec3Value, ColorValue, BoolValue, TextureValue and
// EnumValue build a Value of each type. Used by the kind table for defaults and
// by the editor when a widget is dragged.
func FloatValue(f float32) Value    { return Value{Type: ParamFloat, Num: mgl32.Vec3{f}} }
func Vec2Value(v mgl32.Vec2) Value  { return Value{Type: ParamVec2, Num: mgl32.Vec3{v.X(), v.Y()}} }
func Vec3Value(v mgl32.Vec3) Value  { return Value{Type: ParamVec3, Num: v} }
func ColorValue(v mgl32.Vec3) Value { return Value{Type: ParamColor, Num: v} }
func BoolValue(b bool) Value        { return Value{Type: ParamBool, Bool: b} }
func TextureValue(s string) Value   { return Value{Type: ParamTexture, Str: s} }
func EnumValue(s string) Value      { return Value{Type: ParamEnum, Str: s} }

// Float, Vec2, Vec3 and Str read a value's payload regardless of its declared
// type, so that a document whose parameter was written as the wrong type reads
// as something sane rather than as zero.
func (v Value) Float() float32   { return v.Num[0] }
func (v Value) Vec2() mgl32.Vec2 { return mgl32.Vec2{v.Num[0], v.Num[1]} }
func (v Value) Vec3() mgl32.Vec3 { return v.Num }

// ---------- reading an effect's parameters ----------

// Param returns e's value for the named parameter, or the kind's default when
// the document did not set it (and the zero Value when neither the parameter
// nor the kind is known). Every typed accessor below goes through it, so the
// fallback rule lives in exactly one place.
func (e *Effect) Param(name string) Value {
	if v, ok := e.Params[name]; ok {
		return v
	}
	if k := KindByName(e.Kind); k != nil {
		if d := k.Param(name); d != nil {
			return d.Default
		}
	}
	return Value{}
}

// Float, Vec2, Vec3, Color, Bool, Texture and Enum are Param with the payload
// taken out — what the resolvers in material_surface.go and its siblings
// actually call.
func (e *Effect) Float(name string) float32    { return e.Param(name).Float() }
func (e *Effect) Vec2(name string) mgl32.Vec2  { return e.Param(name).Vec2() }
func (e *Effect) Vec3(name string) mgl32.Vec3  { return e.Param(name).Vec3() }
func (e *Effect) Color(name string) mgl32.Vec3 { return e.Param(name).Vec3() }
func (e *Effect) Bool(name string) bool        { return e.Param(name).Bool }
func (e *Effect) Texture(name string) string   { return e.Param(name).Str }
func (e *Effect) Enum(name string) string      { return e.Param(name).Str }

// Set writes a parameter, allocating the map on first use. The editor's only
// way in.
func (e *Effect) Set(name string, v Value) {
	if e.Params == nil {
		e.Params = map[string]Value{}
	}
	e.Params[name] = v
}

// Clear takes a parameter back to its kind's default by removing it, which is
// what keeps a .fmat to the numbers that were actually changed.
func (e *Effect) Clear(name string) { delete(e.Params, name) }

// ---------- reading a document ----------

// Effect returns the document's first enabled effect of the named kind, or nil.
// The resolvers ask for effects by name and get nil for "not present" and for
// "present but switched off" alike, which is the distinction they do not need
// and the one place it would otherwise have to be repeated.
func (d *Doc) Effect(kind string) *Effect {
	if d == nil {
		return nil
	}
	for i := range d.Effects {
		if d.Effects[i].Kind == kind && !d.Effects[i].Disabled {
			return &d.Effects[i]
		}
	}
	return nil
}

// Has reports whether the document carries an enabled effect of that kind.
func (d *Doc) Has(kind string) bool { return d.Effect(kind) != nil }

// Find returns the document's effect of that kind whether or not it is
// enabled, for the editor — which has to be able to show a switched-off
// effect in order to switch it back on.
func (d *Doc) Find(kind string) *Effect {
	if d == nil {
		return nil
	}
	for i := range d.Effects {
		if d.Effects[i].Kind == kind {
			return &d.Effects[i]
		}
	}
	return nil
}

// AddEffect appends an effect of the named kind with no parameters set — so
// every one of them reads as the kind's default — and returns it. A kind the
// document already carries is returned as it stands rather than duplicated,
// since a second `water:` on one material has no meaning.
func (d *Doc) AddEffect(kind string) *Effect {
	if e := d.Find(kind); e != nil {
		return e
	}
	d.Effects = append(d.Effects, Effect{Kind: kind})
	return &d.Effects[len(d.Effects)-1]
}

// RemoveEffect drops the effect of that kind, if any.
func (d *Doc) RemoveEffect(kind string) {
	for i := range d.Effects {
		if d.Effects[i].Kind == kind {
			d.Effects = append(d.Effects[:i], d.Effects[i+1:]...)
			return
		}
	}
}

// Clone is a deep copy — what the material editor's undo stack keeps and what
// a caller who means to edit a library document takes first, since the library
// hands out the live pointer.
func (d *Doc) Clone() *Doc {
	if d == nil {
		return nil
	}
	c := *d
	c.Effects = make([]Effect, len(d.Effects))
	for i, e := range d.Effects {
		c.Effects[i] = e
		if e.Params != nil {
			c.Effects[i].Params = make(map[string]Value, len(e.Params))
			for k, v := range e.Params {
				c.Effects[i].Params[k] = v
			}
		}
		if e.Unknown != nil {
			c.Effects[i].Unknown = make(map[string]interface{}, len(e.Unknown))
			for k, v := range e.Unknown {
				c.Effects[i].Unknown[k] = v
			}
		}
	}
	return &c
}

// Validate reports every problem with the document that is worth telling an
// author about: an effect whose kind does not exist, one that does not belong
// in this domain, and a parameter its kind does not declare. It is advisory —
// nothing refuses to load over it — and it is what the material editor's
// status line and `foundry check` both print.
func (d *Doc) Validate() []string {
	var problems []string
	seen := map[string]bool{}
	for i := range d.Effects {
		e := &d.Effects[i]
		k := KindByName(e.Kind)
		if k == nil {
			problems = append(problems, fmt.Sprintf("effect %q: no such effect kind", e.Kind))
			continue
		}
		if !k.InDomain(d.Domain) {
			problems = append(problems, fmt.Sprintf("effect %q: not available on a %s material",
				e.Kind, DomainName(d.Domain)))
		}
		if seen[e.Kind] {
			problems = append(problems, fmt.Sprintf("effect %q: listed twice; only the first is used", e.Kind))
		}
		seen[e.Kind] = true

		names := make([]string, 0, len(e.Params))
		for n := range e.Params {
			names = append(names, n)
		}
		sort.Strings(names) // a map's order would shuffle the report between runs
		for _, n := range names {
			if k.Param(n) == nil {
				problems = append(problems, fmt.Sprintf("effect %q: no parameter %q", e.Kind, n))
			}
		}
	}
	return problems
}
