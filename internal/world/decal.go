package world

// decal.go — Decal, a texture projected onto the surfaces the level already has.
//
// Every other way this engine gets a picture onto a wall changes the wall: a
// texture, a second blended texture, an atlas island painted into the face's own
// UVs. That is right for what a surface IS and wrong for what happens to it. A
// bullet hole, a scorch mark, a blood splatter, a spray-painted arrow and a
// stencilled bay number are all the same idea — a small picture laid ON a
// surface, at a size and an angle that have nothing to do with how that surface
// was textured, and often across the seam between two of them.
//
// So a decal is not a surface property. It is an oriented box placed in the
// world, and what it draws is the level's own geometry CLIPPED to that box and
// re-textured by projecting it along the box's axis — the classic approach, and
// the only one a forward renderer can take, since the screen-space kind needs a
// G-buffer this engine deliberately does not have.
//
// Clipping the real geometry is what makes a decal follow the surface instead of
// hovering in front of it: a bullet hole in the corner where two walls meet bends
// around the corner, one on a displaced terrain face follows the bumps, and one
// on a staircase steps. A quad would not — and a sprite with BillboardFixed,
// which is what `decal.fmat` has been standing in for, is exactly that quad.
//
// The mesh is built per SOURCE BRUSH rather than per decal, in that brush's own
// space, and drawn under the matrix the brush itself is drawn under (see
// decal_project.go). That is the whole of the support for decals on lifts, on
// doors, on a dropship's hull and on chunks that stream in and out: none of it
// needed a line of code here, because a piece of a decal is brush geometry as far
// as every transform in the engine is concerned.
//
// Decals live on the level rather than in a chunk, like sprites and volumes: a
// decal is placed in world space, it can span two chunks, and a chunk boundary is
// not something an author aims at.

import (
	"math"

	"foundry/internal/geom"
	"foundry/internal/gfx"
	"foundry/internal/material"

	"github.com/go-gl/mathgl/mgl32"
)

// Decal is a texture projected onto the level's surfaces.
type Decal struct {
	// ID is unique within a level; a script addresses a decal by it (`@id`).
	ID string
	// ScriptData carries the tags, attributes and attached script, so `#tag`
	// reaches decals on the same terms it reaches sprites and volumes.
	ScriptData

	// Position is the centre of the projection box, in world space.
	Position mgl32.Vec3
	// Rotation is Euler XYZ in radians, composed Y then X then Z as everything
	// placed in this engine is. The decal's local +Z is the direction it faces
	// OUT of the surface — so one lying on a floor has been rotated to point its
	// +Z at the sky — and it projects backwards along -Z through Depth. Local +X
	// runs along U and local +Y up V.
	Rotation mgl32.Vec3

	// Width and Height are the projection's size across the surface, in world
	// units: the whole texture is fitted to them exactly once.
	Width  float32
	Height float32
	// Depth is how far along the projection axis a surface may lie and still be
	// caught, measured either side of Position. It is what decides whether a
	// decal aimed at a wall also prints itself on the floor behind it, so it
	// wants to be the smallest value that still reaches the intended surface —
	// see DefaultDecalDepth.
	Depth float32

	Texture string
	// Material is the .fmat this decal wears, or "" for none. Overlaid on the
	// fields around it rather than replacing them, exactly as a brush face's is:
	// the pieces are ordinary world-program geometry, so every surface effect
	// works on a decal for free.
	Material string
	// Blend is how the decal's texels combine with the surface under them. Alpha
	// by default, which is what a poster, a bullet hole and a wet splatter all
	// want; see gfx.DecalBlend for when the other two are right.
	Blend gfx.DecalBlend

	// Tint multiplies the texture. White is no tint — and a tint earns more on a
	// decal than on a sprite, because one grey scorch texture tinted eight ways
	// is eight marks, where eight copies of it is eight textures.
	Tint mgl32.Vec3
	// Alpha scales the texture's own alpha. 1 is the texture unchanged.
	Alpha float32

	// Unlit draws the texture's own colour, ignoring the scene's lights. Off by
	// default, which is the opposite of a sprite's default and right for the
	// opposite reason: a decal IS part of the surface it lies on, and a bullet
	// hole that stayed bright in a dark room would read as a light source. Turn
	// it on for the emissive few — a glowing rune, a holographic sign.
	Unlit bool

	// MaxAngle is how far, in degrees, a surface's normal may lean from the
	// decal's own facing and still be painted. It is what stops a decal sprayed
	// at a wall from also wrapping onto the floor as a metre-long smear: past
	// this angle the projection is so oblique that the texture is stretched into
	// streaks, which reads as a rendering bug where a decal that stops at the
	// corner reads as a decal. 0 reads as DefaultDecalMaxAngle.
	MaxAngle float32

	// Push moves the built mesh this far out along each surface's own normal.
	// The renderer already pulls decals toward the viewer with a depth-slope
	// offset, which handles every surface facing the camera squarely; this is the
	// extra nudge a surface seen almost edge-on needs, where a slope offset alone
	// cannot separate the two. 0 reads as DefaultDecalPush.
	Push float32

	// Order breaks the tie between decals that overlap: a higher one draws later,
	// so it lies on top. Equal orders draw in level order, which is placement
	// order — so an author who has not thought about it gets "the one I put down
	// last is on top", which is what they expect.
	Order int

	// Visible is what a script toggles to hide a decal without deleting it.
	Visible bool

	// ---------- run time, never authored and never saved ----------

	// Lifetime is how many seconds this decal has left, or 0 for one that never
	// expires. Set by the `decal` verb for the marks a firefight leaves — see
	// DecalFadeTime, which spends the last of it fading out rather than blinking
	// the mark off the wall.
	Lifetime float32
	// Transient marks a decal a script spawned rather than an author placed. It
	// is what keeps a firefight from filling a level: the transient ones are
	// capped (MaxTransientDecals) and the oldest is recycled, and neither the
	// level file nor a save game writes them as level content.
	Transient bool

	// Pieces is the built mesh, one per source space. Rebuilt by SyncDecals; see
	// decal_project.go.
	Pieces []DecalPiece
	// built is the fingerprint of the decal and the world its Pieces were built
	// from, and builtOK that a build has run at all. SyncDecals compares against
	// both — see decalFingerprint.
	built   uint64
	builtOK bool
}

// Defaults for a freshly placed decal. A quarter-unit square is about the size
// of a bullet hole or a small stencil, which is the commonest thing placed; the
// depth is deliberately much smaller than that, so a decal put on a wall does
// not also print itself on whatever stands a metre behind it.
const (
	DefaultDecalSize     = float32(0.25)
	DefaultDecalDepth    = float32(0.25)
	DefaultDecalMaxAngle = float32(75)
	DefaultDecalPush     = float32(0.002)
	// DefaultDecalTexture is what a freshly placed decal gets, so it is visible
	// the moment it lands rather than an invisible box.
	DefaultDecalTexture = "default"
)

// MaxTransientDecals is how many script-spawned decals a level keeps. Past it
// the oldest is recycled, which is the whole of the policy: a decal costs a mesh
// clipped out of the world's triangles, and a level that kept every one a long
// fight produced would grow without bound in memory and in draw calls.
//
// Chosen as a couple of magazines' worth of impacts, which is long enough that a
// player never sees a hole vanish from the wall in front of them.
const MaxTransientDecals = 192

// DecalFadeTime is how long a decal with a Lifetime spends fading out at the end
// of it, in seconds. A mark that blinked out of existence draws the eye far more
// than one that was never there.
const DecalFadeTime = float32(1.5)

// NewDecal returns a decal at pos facing along normal, with the defaults a
// freshly placed one should have.
//
// The roll about the normal is left at zero. A caller that wants a varied one —
// the `decal` verb, placing the tenth bullet hole in the same wall — passes its
// own; see DecalRotationFor.
func NewDecal(id string, pos, normal mgl32.Vec3) Decal {
	return Decal{
		ID:       id,
		Position: pos,
		Rotation: DecalRotationFor(normal, 0),
		Width:    DefaultDecalSize,
		Height:   DefaultDecalSize,
		Depth:    DefaultDecalDepth,
		Texture:  DefaultDecalTexture,
		Blend:    gfx.DecalBlendAlpha,
		Tint:     mgl32.Vec3{1, 1, 1},
		Alpha:    1,
		MaxAngle: DefaultDecalMaxAngle,
		Push:     DefaultDecalPush,
		Visible:  true,
	}
}

// DecalRotationFor is the Euler rotation that points a decal's +Z along normal,
// rolled by roll radians about it.
//
// It exists so that placing a decal reads as "face this way" everywhere — the
// editor's click on a surface, the `decal` verb's impact normal — rather than
// each caller deriving Euler angles from a vector and picking a different
// convention. Composed to match EulerMatrixYXZ, which is what reads the result:
// yaw about Y, then pitch about X, then the roll.
func DecalRotationFor(normal mgl32.Vec3, roll float32) mgl32.Vec3 {
	if normal.LenSqr() < 1e-12 {
		return mgl32.Vec3{0, 0, roll}
	}
	n := normal.Normalize()
	// Yaw turns +Z onto the normal's horizontal heading; pitch then lifts it to
	// the normal's own elevation. Atan2 of (x, z) rather than (z, x) because +Z
	// is the facing axis — and a floor decal, whose normal is straight up and has
	// no heading at all, falls out as yaw 0, pitch -90.
	yaw := float32(math.Atan2(float64(n.X()), float64(n.Z())))
	horiz := float32(math.Sqrt(float64(n.X()*n.X() + n.Z()*n.Z())))
	pitch := float32(math.Atan2(float64(-n.Y()), float64(horiz)))
	return mgl32.Vec3{pitch, yaw, roll}
}

// HasSize reports whether the decal describes a projection with volume. A zero
// on any axis is an unauthored decal rather than a flat one.
func (d *Decal) HasSize() bool { return d.Width > 0 && d.Height > 0 && d.Depth > 0 }

// Facing is the decal's projection axis in world space: the direction it looks
// OUT of the surface it lies on, which is its local +Z.
func (d *Decal) Facing() mgl32.Vec3 {
	_, _, n := d.Basis()
	return n
}

// Basis is the decal's three world-space axes: U across the texture, V up it,
// and N out of the surface. Each is unit length; the box's half-extents are
// applied by the caller, because the projection wants the axes and the clipper
// wants them scaled.
func (d *Decal) Basis() (u, v, n mgl32.Vec3) {
	m := EulerMatrixYXZ(d.Rotation)
	col := func(i int) mgl32.Vec3 {
		c := m.Col(i)
		return mgl32.Vec3{c.X(), c.Y(), c.Z()}
	}
	u, v, n = col(0), col(1), col(2)
	if u.LenSqr() < 1e-12 || v.LenSqr() < 1e-12 || n.LenSqr() < 1e-12 {
		return mgl32.Vec3{1, 0, 0}, mgl32.Vec3{0, 1, 0}, mgl32.Vec3{0, 0, 1}
	}
	return u.Normalize(), v.Normalize(), n.Normalize()
}

// EffectiveMaxAngle is MaxAngle with its zero read as the default, and clamped
// short of the 180 degrees at which a decal would paint the far side of every
// solid it reached.
func (d *Decal) EffectiveMaxAngle() float32 {
	if d.MaxAngle <= 0 {
		return DefaultDecalMaxAngle
	}
	if d.MaxAngle > 179 {
		return 179
	}
	return d.MaxAngle
}

// EffectivePush is Push with its zero read as the default. A negative one is
// honoured: an author sinking a decal INTO a surface is doing it on purpose.
func (d *Decal) EffectivePush() float32 {
	if d.Push == 0 {
		return DefaultDecalPush
	}
	return d.Push
}

// DrawAlpha is the alpha the decal is drawn at: its own, faded down over the
// last DecalFadeTime seconds of a Lifetime.
//
// The fade is applied here rather than written into Alpha so that nothing
// destroys the authored value — a script reading `@hole.prop.alpha` back gets
// what it set, and a decal whose lifetime is cancelled returns to full strength
// instead of staying wherever the fade had reached.
func (d *Decal) DrawAlpha() float32 {
	if d.Lifetime <= 0 || d.Lifetime >= DecalFadeTime {
		return d.Alpha
	}
	return d.Alpha * (d.Lifetime / DecalFadeTime)
}

// Drawable reports whether the decal should be submitted this frame at all.
func (d *Decal) Drawable() bool {
	return d.Visible && d.HasSize() && d.DrawAlpha() > 0 && len(d.Pieces) > 0
}

// DrawTexture is the texture the decal draws with: its material's, where the
// material names one, and its own otherwise.
//
// A decal's pieces are ordinary world-program geometry, so the material is
// carried on the Geometry and resolved by gfx.SurfaceState at draw time exactly
// as a brush face's is. This is the same answer asked early — by the texture
// preload and by the editor's swatch, both of which need the name before there
// is any geometry to ask.
func (d *Decal) DrawTexture() string {
	if m, ok := material.ResolveSurface(d.Material); ok && m.Texture != "" {
		return m.Texture
	}
	return d.Texture
}

// WorldAABB is the projection box's axis-aligned bounds — what culling and the
// clipper's broad phase both read. ok is false when nothing is authored.
func (d *Decal) WorldAABB() (min, max mgl32.Vec3, ok bool) {
	if !d.HasSize() {
		return mgl32.Vec3{}, mgl32.Vec3{}, false
	}
	u, v, n := d.Basis()
	hu, hv, hn := u.Mul(d.Width*0.5), v.Mul(d.Height*0.5), n.Mul(d.Depth*0.5)
	min, max = d.Position, d.Position
	// The eight corners, which is the honest answer for a box that may be
	// rotated on all three axes.
	for _, su := range [2]float32{-1, 1} {
		for _, sv := range [2]float32{-1, 1} {
			for _, sn := range [2]float32{-1, 1} {
				p := d.Position.Add(hu.Mul(su)).Add(hv.Mul(sv)).Add(hn.Mul(sn))
				min, max = geom.VecMin(min, p), geom.VecMax(max, p)
			}
		}
	}
	return min, max, true
}

// ---------- level lookup ----------

// FindDecal returns the level decal with the given ID, or nil. This is what a
// script's `@id` resolves through for decals.
func FindDecal(lvl *Level, id string) *Decal {
	if lvl == nil || id == "" {
		return nil
	}
	for i := range lvl.Decals {
		if lvl.Decals[i].ID == id {
			return &lvl.Decals[i]
		}
	}
	return nil
}

// FindDecalsByTag returns every level decal carrying tag, in level order.
func FindDecalsByTag(lvl *Level, tag string) []*Decal {
	if lvl == nil || tag == "" {
		return nil
	}
	var out []*Decal
	for i := range lvl.Decals {
		if lvl.Decals[i].HasTag(tag) {
			out = append(out, &lvl.Decals[i])
		}
	}
	return out
}

// UniqueDecalID returns base, or base with a numeric suffix that nothing in lvl
// is already using — the same guarantee UniqueSpriteID gives.
func UniqueDecalID(lvl *Level, base string) string {
	if base == "" {
		base = "decal"
	}
	return uniqueLevelID(lvl, base)
}

// ---------- serialization ----------

// DecalFromRaw converts the YAML form. Every default is chosen so that the
// smallest useful entry is an ID, a position, a rotation and a texture.
func DecalFromRaw(r RawDecal) (Decal, error) {
	data, err := ScriptDataFromRaw(r.RawScriptData, "decal "+r.ID)
	if err != nil {
		return Decal{}, err
	}
	d := Decal{
		ID:         r.ID,
		ScriptData: data,
		Position:   mgl32.Vec3{r.Position.X, r.Position.Y, r.Position.Z},
		Rotation:   mgl32.Vec3{r.Rotation.X, r.Rotation.Y, r.Rotation.Z},
		Width:      float32Default(r.Width, DefaultDecalSize),
		Height:     float32Default(r.Height, DefaultDecalSize),
		Depth:      float32Default(r.Depth, DefaultDecalDepth),
		Texture:    r.Texture,
		Material:   r.Material,
		Blend:      gfx.DecalBlendFromName(r.Blend),
		Tint:       colorDefault(r.Tint),
		Alpha:      float32Default(r.Alpha, 1),
		Unlit:      r.Unlit,
		MaxAngle:   float32Default(r.MaxAngle, DefaultDecalMaxAngle),
		Push:       float32Default(r.Push, DefaultDecalPush),
		Order:      r.Order,
		Visible:    BoolDefault(r.Visible, true),
	}
	if d.Texture == "" && d.Material == "" {
		d.Texture = DefaultDecalTexture
	}
	return d, nil
}

// DecalToRaw is the inverse. Defaults are written as absent, so a decal that has
// not been fiddled with stays a four-line entry.
func DecalToRaw(d *Decal) RawDecal {
	return RawDecal{
		ID:            d.ID,
		RawScriptData: ScriptDataToRaw(d.ScriptData),
		Position:      RawVec3{X: d.Position.X(), Y: d.Position.Y(), Z: d.Position.Z()},
		Rotation:      RawVec3{X: d.Rotation.X(), Y: d.Rotation.Y(), Z: d.Rotation.Z()},
		Width:         float32PtrIfNot(d.Width, DefaultDecalSize),
		Height:        float32PtrIfNot(d.Height, DefaultDecalSize),
		Depth:         float32PtrIfNot(d.Depth, DefaultDecalDepth),
		Texture:       d.Texture,
		Material:      d.Material,
		Blend:         decalBlendNameIfNot(d.Blend, gfx.DecalBlendAlpha),
		Tint:          colorPtrIfNotWhite(d.Tint),
		Alpha:         float32PtrIfNot(d.Alpha, 1),
		Unlit:         d.Unlit,
		MaxAngle:      float32PtrIfNot(d.MaxAngle, DefaultDecalMaxAngle),
		Push:          float32PtrIfNot(d.Push, DefaultDecalPush),
		Order:         d.Order,
		Visible:       BoolPtrIfFalse(d.Visible),
	}
}

// decalBlendNameIfNot writes a mode's name only when it is not the default.
func decalBlendNameIfNot(b, def gfx.DecalBlend) string {
	if b == def {
		return ""
	}
	return gfx.DecalBlendName(b)
}

// EnsureDecalTexture loads the decal's texture, falling back to the default so a
// level naming a texture that no longer exists still opens — the rule
// EnsureSpriteTexture follows, for the same reason.
func (d *Decal) EnsureDecalTexture() error {
	if d.DrawTexture() == "" {
		d.Texture = DefaultDecalTexture
	}
	if err := gfx.EnsureTextureLoaded(d.DrawTexture()); err != nil {
		d.Texture, d.Material = DefaultDecalTexture, ""
		return gfx.EnsureTextureLoaded(d.Texture)
	}
	return nil
}
