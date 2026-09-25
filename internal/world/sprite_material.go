package world

// sprite_material.go — a sprite wearing a .fmat.
//
// The Draw* accessors here are what the renderer reads instead of the fields
// themselves, exactly as BrushFace.DrawTexture and DrawUVTransform are what it
// reads instead of a face's Texture and UV fields. A sprite with no material
// gets its own values back unchanged, so nothing about a level authored before
// materials existed moves a pixel.
//
// The split between "the material's" and "the sprite's" follows the rule
// material_resolve.go sets out: a material overrides only the effects it
// actually carries. `ember.fmat` saying nothing but "additive" leaves each
// sprite wearing it its own texture, size, tint and frame.
//
// Frame is deliberately NOT a material parameter even though the sheet layout
// is: how a sheet is cut up is a property of the art, and which cell a
// particular sprite is showing is a property of that sprite.

import (
	"foundry/internal/gfx"
	"foundry/internal/material"

	"github.com/go-gl/mathgl/mgl32"
)

// quad resolves the sprite's material, or reports that it has none. Every
// accessor below starts here; material.Get returns nil for the empty name that
// nearly every sprite carries, so the cost on those is one comparison.
func (s *Sprite) quad() (material.Quad, bool) {
	return material.ResolveQuad(s.Material)
}

// DrawTexture is the texture the sprite is drawn with: its material's, or its
// own.
func (s *Sprite) DrawTexture() string {
	if q, ok := s.quad(); ok && q.Texture != "" {
		return q.Texture
	}
	return s.Texture
}

// DrawBlend is the blend mode it draws in.
func (s *Sprite) DrawBlend() gfx.SpriteBlend {
	if q, ok := s.quad(); ok && q.HasBlendMode {
		return gfx.SpriteBlendFromName(q.BlendMode)
	}
	return s.Blend
}

// DrawBillboard is how it faces the viewer.
func (s *Sprite) DrawBillboard() gfx.BillboardMode {
	if q, ok := s.quad(); ok && q.HasBillboard {
		return gfx.BillboardModeFromName(q.Billboard)
	}
	return s.Billboard
}

// DrawTint multiplies the texture. A material's tint multiplies the sprite's
// rather than replacing it, so a material that warms a row of lamps does not
// also flatten the one lamp an author made red.
func (s *Sprite) DrawTint() mgl32.Vec3 {
	if q, ok := s.quad(); ok && q.HasTint {
		return mgl32.Vec3{
			s.Tint.X() * q.Tint.X(),
			s.Tint.Y() * q.Tint.Y(),
			s.Tint.Z() * q.Tint.Z(),
		}
	}
	return s.Tint
}

// DrawAlpha is its alpha, the material's multiplying the sprite's for the
// reason DrawTint's does — and so that a script fading a sprite out still
// reaches zero however its material is set.
func (s *Sprite) DrawAlpha() float32 {
	if q, ok := s.quad(); ok && q.HasAlpha {
		return s.Alpha * q.Alpha
	}
	return s.Alpha
}

// DrawLit is whether it takes the scene's lighting. A material can only turn
// it ON: `lit` is the presence of an effect, and there is no effect meaning
// "not lit" to override a sprite that asked for it.
func (s *Sprite) DrawLit() bool {
	if q, ok := s.quad(); ok && q.Lit {
		return true
	}
	return s.Lit
}

// DrawCastShadows is whether it goes in the depth passes, on the same terms.
func (s *Sprite) DrawCastShadows() bool {
	if q, ok := s.quad(); ok && q.CastShadows {
		return true
	}
	return s.CastShadows
}

// DrawSheet is the sheet grid it is cut into: the material's when it declares
// one, since how the art divides up belongs to the art.
func (s *Sprite) DrawSheet() (cols, rows int) {
	if q, ok := s.quad(); ok && q.HasSheet {
		return q.SheetCols, q.SheetRows
	}
	return s.SheetCols, s.SheetRows
}
