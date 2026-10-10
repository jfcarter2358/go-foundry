package particle

// particle_material.go — a layer wearing a .fmat.
//
// The same arrangement world/sprite_material.go makes for a level sprite: the
// Draw* accessors are what the renderer and the volumetric march read, a layer
// with no material gets its own fields back unchanged, and a material
// overrides only the effects it actually carries.
//
// What this buys an author is that the look of an effect stops being trapped
// inside one .fpar. `ember.fmat` — additive, full billboard, a warm tint — can
// be worn by the sparks layer of six different explosions, and changing it
// changes all six. It is also how the volumetric look of a fireball becomes
// something the material editor can drag a slider on while the effect plays.
//
// The two RAMPS stay on the layer. A ramp is a curve over a particle's life
// rather than a constant, and a material's parameter schema has nowhere to put
// one — see material.KindVolume.

import (
	"foundry/internal/gfx"
	"foundry/internal/material"

	"github.com/go-gl/mathgl/mgl32"
)

func (l *Layer) quad() (material.Quad, bool) { return material.ResolveQuad(l.Material) }

// DrawTexture is the texture its particles are drawn with.
func (l *Layer) DrawTexture() string {
	if q, ok := l.quad(); ok && q.Texture != "" {
		return q.Texture
	}
	return l.Texture
}

// DrawBlend is the blend mode they draw in.
func (l *Layer) DrawBlend() gfx.SpriteBlend {
	if q, ok := l.quad(); ok && q.HasBlendMode {
		return gfx.SpriteBlendFromName(q.BlendMode)
	}
	return l.Blend
}

// DrawBillboard is how they face the viewer.
func (l *Layer) DrawBillboard() gfx.BillboardMode {
	if q, ok := l.quad(); ok && q.HasBillboard {
		return gfx.BillboardModeFromName(q.Billboard)
	}
	return l.Billboard
}

// DrawTint multiplies the layer's own tint rather than replacing it, for the
// reason Sprite.DrawTint does.
func (l *Layer) DrawTint() mgl32.Vec3 {
	if q, ok := l.quad(); ok && q.HasTint {
		return mgl32.Vec3{
			l.Tint.X() * q.Tint.X(),
			l.Tint.Y() * q.Tint.Y(),
			l.Tint.Z() * q.Tint.Z(),
		}
	}
	return l.Tint
}

// DrawLit and DrawCastShadows can only be turned ON by a material: both are
// the presence of an effect, and there is no effect meaning "not lit".
func (l *Layer) DrawLit() bool {
	if q, ok := l.quad(); ok && q.Lit {
		return true
	}
	return l.Lit
}

func (l *Layer) DrawCastShadows() bool {
	if q, ok := l.quad(); ok && q.CastShadows {
		return true
	}
	return l.CastShadows
}

// DrawSheet is the sheet grid the texture is cut into.
func (l *Layer) DrawSheet() (cols, rows int) {
	if q, ok := l.quad(); ok && q.HasSheet {
		return q.SheetCols, q.SheetRows
	}
	return l.SheetCols, l.SheetRows
}

// DrawVolume is the volumetric look the march should use: the material's
// constants over the layer's, with the layer's two ramps kept either way.
func (l *Layer) DrawVolume() VolumeLook {
	v := l.Volume
	q, ok := l.quad()
	if !ok || !q.HasVolume {
		return v
	}
	v.Density = q.Volume.Density
	v.NoiseScale = q.Volume.NoiseScale
	v.Displacement = q.Volume.Displacement
	v.Billow = q.Volume.Billow
	v.Erosion = q.Volume.Erosion
	v.Albedo = q.Volume.Albedo
	v.Absorption = q.Volume.Absorption
	v.Emission = q.Volume.Emission
	return v
}
