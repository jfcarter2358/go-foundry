package world

// sprite.go — Sprite, a camera-facing textured quad placed in a level.
//
// Everything else this engine draws is a surface of the world or a mesh standing
// in it. A sprite is neither: it is one quad that turns to face the viewer, so a
// bush, a sign, a torch flame or a pickup costs four vertices and one texture
// instead of a model and a rig.
//
// It is the smaller idea next to an entity, in the same way a volume is, so it
// gets the smaller type: no bounding volume, no collision flags, no velocity, no
// model attachments, no definition it was instanced from. What it does have is
// the three things a billboard needs and nothing else does — which way it faces,
// how it blends, and what it rides.
//
// Sprites live on the level rather than in a chunk, for the reason entities and
// volumes do: one attached to a moving entity crosses chunks, and its authored
// position is world-space unless Attach says otherwise.

import (
	"foundry/internal/gfx"

	"github.com/go-gl/mathgl/mgl32"
)

// Sprite is a camera-facing textured quad placed in a level.
type Sprite struct {
	// ID is unique within a level; a script addresses a sprite by it (`@id`).
	ID string
	// ScriptData carries the tags, attributes and attached script, so `#tag`
	// reaches sprites on the same terms it reaches entities and volumes.
	ScriptData

	// Position is world-space unless Attach names a carrier, in which case it
	// is that carrier's local space.
	Position mgl32.Vec3
	// Rotation is Euler XYZ in radians. In the two billboarded modes only Z is
	// read, as a roll about the view axis; gfx.BillboardFixed reads all three.
	Rotation mgl32.Vec3

	// Width and Height are the quad's authored size in world units.
	Width  float32
	Height float32

	// Scale multiplies the authored size: X the width, Y the height, Z nothing
	// (a quad has no depth). The zero value reads as {1,1,1} — see
	// EffectiveScale. Runtime-only, like Brush.Scale: it is what a waypoint
	// animation or a script grows the sprite by, never what a level file says
	// its size is. DrawnSize is what everything that draws or bounds the quad
	// reads.
	Scale mgl32.Vec3

	Texture string
	// Material is the .fmat this sprite wears, or "" for none. What it says is
	// overlaid on the fields around it rather than replacing them — see
	// sprite_material.go, and the Draw* accessors there, which are what the
	// renderer reads.
	Material  string
	Billboard gfx.BillboardMode
	Blend     gfx.SpriteBlend

	// Tint multiplies the texture. White is no tint.
	Tint mgl32.Vec3
	// Alpha scales the texture's own alpha. 1 is the texture unchanged. A
	// cutout sprite below 1 draws in the alpha pass until it is back at 1, so
	// it fades rather than vanishing at the cutout threshold.
	Alpha float32

	// Lit takes the full scene lighting and shadows. Off by default, which is
	// right for anything emissive — a flame lit by the sun looks wrong.
	Lit bool
	// CastShadows puts the quad in the depth passes, where it faces the light
	// rather than the camera. Off by default: most sprites are emissive, and a
	// billboard's shadow is only convincing for the solid ones.
	CastShadows bool
	// Visible is what a script toggles to hide a sprite without deleting it.
	Visible bool

	// SheetCols and SheetRows divide the texture into a grid of cells, and
	// Frame picks one. Zero on either axis means "use the whole texture", which
	// is the common case and costs nothing.
	SheetCols int
	SheetRows int
	Frame     int

	// Attach names the frame, entity or bone this sprite rides. Zero is the
	// world.
	Attach Attach

	// Waypoints is the waypoint animation this sprite is travelling, or nil.
	// Runtime-only, never serialized; see waypoint_follow.go.
	Waypoints *WaypointPlayback
}

// DefaultSpriteTexture is what a freshly placed sprite gets, so it is visible
// the moment it lands rather than a black quad.
const DefaultSpriteTexture = "default"

// NewSprite returns a sprite with the defaults a freshly placed one should
// have: a one-unit quad, cutout, upright, visible, untinted.
func NewSprite(id string, pos mgl32.Vec3) Sprite {
	return Sprite{
		ID:        id,
		Position:  pos,
		Width:     1,
		Height:    1,
		Texture:   DefaultSpriteTexture,
		Billboard: gfx.BillboardYAxis,
		Blend:     gfx.SpriteBlendCutout,
		Tint:      mgl32.Vec3{1, 1, 1},
		Alpha:     1,
		Visible:   true,
	}
}

// HasSize reports whether the sprite describes a drawable quad. A zero on
// either axis is an unauthored sprite rather than a degenerate one.
func (s *Sprite) HasSize() bool { return s.Width > 0 && s.Height > 0 }

// EffectiveScale is Scale with its zero value read as no scale at all.
func (s *Sprite) EffectiveScale() mgl32.Vec3 { return effectiveScale3(s.Scale) }

// DrawnSize is the quad's size as drawn: the authored Width and Height, each
// multiplied by its axis of Scale.
func (s *Sprite) DrawnSize() mgl32.Vec2 {
	sc := s.EffectiveScale()
	return mgl32.Vec2{s.Width * sc.X(), s.Height * sc.Y()}
}

// Drawable reports whether the sprite should be submitted this frame at all.
func (s *Sprite) Drawable() bool { return s.Visible && s.HasSize() && s.DrawTexture() != "" }

// UVRect returns the sub-rectangle of the texture this sprite draws, as
// (u0, v0, du, dv). The whole texture when no sheet is authored.
func (s *Sprite) UVRect() (u0, v0, du, dv float32) {
	cols, rows := s.DrawSheet()
	if cols <= 0 || rows <= 0 {
		return 0, 0, 1, 1
	}
	n := cols * rows
	f := s.Frame % n
	if f < 0 {
		f += n
	}
	du = 1 / float32(cols)
	dv = 1 / float32(rows)
	return float32(f%cols) * du, float32(f/cols) * dv, du, dv
}

// ModelMatrix builds the sprite's authored transform, before billboarding. The
// renderer uses its translation for every mode and its rotation only for
// gfx.BillboardFixed; rotation composes Y, then X, then Z, as everything placed in
// this engine does.
func (s *Sprite) ModelMatrix() mgl32.Mat4 {
	T := mgl32.Translate3D(s.Position.X(), s.Position.Y(), s.Position.Z())
	return T.Mul4(EulerMatrixYXZ(s.Rotation))
}

// WorldPosition resolves the sprite's position through its attachment. This is
// what culling, sorting and the draw itself all read.
func (s *Sprite) WorldPosition(lvl *Level) mgl32.Vec3 {
	if s.Attach.IsZero() {
		return s.Position
	}
	p := s.Attach.Matrix(lvl).Mul4x1(s.Position.Vec4(1))
	return mgl32.Vec3{p.X(), p.Y(), p.Z()}
}

// WorldAABB returns bounds wide enough to hold the quad at any orientation,
// which is what culling a thing that turns with the camera requires. ok is
// false when nothing is authored.
func (s *Sprite) WorldAABB(lvl *Level) (min, max mgl32.Vec3, ok bool) {
	if !s.HasSize() {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	// Half the diagonal, on every axis: a quad spun about its centre sweeps a
	// sphere of that radius, and a box around the sphere is what stays correct
	// however the camera moves.
	r := s.DrawnSize().Len() * 0.5
	h := mgl32.Vec3{r, r, r}
	p := s.WorldPosition(lvl)
	return p.Sub(h), p.Add(h), true
}

// ---------- level lookup ----------

// FindSprite returns the level sprite with the given ID, or nil. This is what a
// script's `@id` resolves through for sprites.
func FindSprite(lvl *Level, id string) *Sprite {
	if lvl == nil || id == "" {
		return nil
	}
	for i := range lvl.Sprites {
		if lvl.Sprites[i].ID == id {
			return &lvl.Sprites[i]
		}
	}
	return nil
}

// FindSpritesByTag returns every level sprite carrying tag, in level order.
func FindSpritesByTag(lvl *Level, tag string) []*Sprite {
	if lvl == nil || tag == "" {
		return nil
	}
	var out []*Sprite
	for i := range lvl.Sprites {
		if lvl.Sprites[i].HasTag(tag) {
			out = append(out, &lvl.Sprites[i])
		}
	}
	return out
}

// UniqueSpriteID returns base, or base with a numeric suffix that nothing in
// lvl is already using — the same guarantee UniqueVolumeID gives.
func UniqueSpriteID(lvl *Level, base string) string {
	if base == "" {
		base = "sprite"
	}
	return uniqueLevelID(lvl, base)
}

// ---------- serialization ----------

// SpriteFromRaw converts the YAML form. Every default is chosen so that the
// smallest useful entry in a level file is an ID, a position and a texture.
func SpriteFromRaw(r RawSprite) (Sprite, error) {
	data, err := ScriptDataFromRaw(r.RawScriptData, "sprite "+r.ID)
	if err != nil {
		return Sprite{}, err
	}
	s := Sprite{
		ID:          r.ID,
		ScriptData:  data,
		Position:    mgl32.Vec3{r.Position.X, r.Position.Y, r.Position.Z},
		Rotation:    mgl32.Vec3{r.Rotation.X, r.Rotation.Y, r.Rotation.Z},
		Width:       float32Default(r.Width, 1),
		Height:      float32Default(r.Height, 1),
		Texture:     r.Texture,
		Material:    r.Material,
		Billboard:   gfx.BillboardModeFromName(r.Billboard),
		Blend:       gfx.SpriteBlendFromName(r.Blend),
		Tint:        colorDefault(r.Tint),
		Alpha:       float32Default(r.Alpha, 1),
		Lit:         r.Lit,
		CastShadows: r.CastShadows,
		Visible:     BoolDefault(r.Visible, true),
		SheetCols:   r.SheetCols,
		SheetRows:   r.SheetRows,
		Frame:       r.Frame,
		Attach:      AttachFromRaw(r.Attach),
	}
	if s.Texture == "" {
		s.Texture = DefaultSpriteTexture
	}
	return s, nil
}

// SpriteToRaw is the inverse. Defaults are written as absent so a sprite that
// has not been fiddled with stays a three-line entry.
func SpriteToRaw(s *Sprite) RawSprite {
	return RawSprite{
		ID:            s.ID,
		RawScriptData: ScriptDataToRaw(s.ScriptData),
		Position:      RawVec3{X: s.Position.X(), Y: s.Position.Y(), Z: s.Position.Z()},
		Rotation:      RawVec3{X: s.Rotation.X(), Y: s.Rotation.Y(), Z: s.Rotation.Z()},
		Width:         float32PtrIfNot(s.Width, 1),
		Height:        float32PtrIfNot(s.Height, 1),
		Texture:       s.Texture,
		Material:      s.Material,
		Billboard:     billboardNameIfNot(s.Billboard, gfx.BillboardFull),
		Blend:         blendNameIfNot(s.Blend, gfx.SpriteBlendCutout),
		Tint:          colorPtrIfNotWhite(s.Tint),
		Alpha:         float32PtrIfNot(s.Alpha, 1),
		Lit:           s.Lit,
		CastShadows:   s.CastShadows,
		Visible:       BoolPtrIfFalse(s.Visible),
		SheetCols:     s.SheetCols,
		SheetRows:     s.SheetRows,
		Frame:         s.Frame,
		Attach:        AttachToRaw(s.Attach),
	}
}

// billboardNameIfNot writes a mode's name only when it is not the default, so
// the common case leaves the key out.
func billboardNameIfNot(m, def gfx.BillboardMode) string {
	if m == def {
		return ""
	}
	return gfx.BillboardModeName(m)
}

// blendNameIfNot is the same for blend modes.
func blendNameIfNot(b, def gfx.SpriteBlend) string {
	if b == def {
		return ""
	}
	return gfx.SpriteBlendName(b)
}

// ---------- shared Raw* value helpers ----------
//
// These live here rather than in yamlutil.go alongside the bool pair because
// they are the float and colour halves of the same idiom and sprites are their
// first user; move them down if a second one appears.

// float32Default returns v, or def when v is zero. Used where zero is never a
// meaningful authored value — a sprite with zero width is unauthored, not flat.
func float32Default(p *float32, def float32) float32 {
	if p != nil {
		return *p
	}
	return def
}

// float32PtrIfNot returns a pointer only when v differs from the default.
func float32PtrIfNot(v, def float32) *float32 {
	if v == def {
		return nil
	}
	return &v
}

// colorDefault reads an optional colour, defaulting to white.
func colorDefault(c *RawColor) mgl32.Vec3 {
	if c == nil {
		return mgl32.Vec3{1, 1, 1}
	}
	return mgl32.Vec3{c.R, c.G, c.B}
}

// colorPtrIfNotWhite writes a tint only when it is one.
func colorPtrIfNotWhite(v mgl32.Vec3) *RawColor {
	if v == (mgl32.Vec3{1, 1, 1}) {
		return nil
	}
	return &RawColor{R: v.X(), G: v.Y(), B: v.Z()}
}

// SpriteMarkerTexture is what an unauthored sprite falls back to, so a sprite
// whose texture went missing is still selectable rather than invisible.
const SpriteMarkerTexture = "default"

// EnsureSpriteTexture loads the sprite's texture, falling back to the marker so
// a level naming a texture that no longer exists still opens.
func (s *Sprite) EnsureSpriteTexture() error {
	// A material may supply the texture, in which case the sprite's own is
	// free to be empty — so the fallback is applied to what will actually be
	// drawn, not to the field.
	if s.DrawTexture() == "" {
		s.Texture = SpriteMarkerTexture
	}
	if err := gfx.EnsureTextureLoaded(s.DrawTexture()); err != nil {
		s.Texture, s.Material = SpriteMarkerTexture, ""
		return gfx.EnsureTextureLoaded(s.Texture)
	}
	return nil
}
