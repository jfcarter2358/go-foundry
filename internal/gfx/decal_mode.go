package gfx

// decal_mode.go — how a projected decal combines with the surface under it.
//
// A separate enum from SpriteBlend rather than a reuse of it, and the reason is
// the one mode each has that the other cannot use.
//
// SpriteBlend's `cutout` makes no sense for a decal: cutout means "opaque, write
// depth, draw in the opaque pass", and a decal is drawn ON TOP of a surface that
// has already written the same depth — it has nothing to be opaque against and
// nothing to sort. And `multiply` makes no sense for a sprite: multiplying by
// what is behind a free-standing quad multiplies by the sky.
//
// Multiply is what most of the marks a game leaves actually want. Scorch, soot,
// oil, dirt, dried blood and tyre rub are all a DARKENING of the surface, not a
// picture laid over it: an alpha-blended grey blob on a white wall and on a
// black floor is the same grey blob twice, where a multiplied one is a smudge on
// both. One texture, every surface — which is the whole economy of decals.

// DecalBlend is how a decal's texels combine with the surface under them.
type DecalBlend int

const (
	// DecalBlendAlpha lays the texture over the surface, its own alpha deciding
	// how much of each shows. The right default, and what a poster, a sign, a
	// bullet hole and a wet splatter all want.
	DecalBlendAlpha DecalBlend = iota
	// DecalBlendMultiply darkens the surface by the texture instead of covering
	// it, so one mark reads correctly on every colour it lands on. For scorch,
	// soot, dirt and rub — see the file comment.
	DecalBlendMultiply
	// DecalBlendAdditive adds light to the surface and never darkens it. For a
	// glowing rune, a holographic stencil, a hot ember scar.
	DecalBlendAdditive
)

// DecalBlendNames is the YAML/UI name of each mode, indexed by the mode.
var DecalBlendNames = []string{
	"alpha",
	"multiply",
	"additive",
}

// DecalBlendName returns a mode's YAML/UI name, falling back to the default for
// anything out of range.
func DecalBlendName(b DecalBlend) string {
	if int(b) < 0 || int(b) >= len(DecalBlendNames) {
		return DecalBlendNames[DecalBlendAlpha]
	}
	return DecalBlendNames[b]
}

// DecalBlendFromName is the inverse. An unknown name loads as the default
// rather than failing, so a level written by a newer build still opens.
func DecalBlendFromName(name string) DecalBlend {
	for i, n := range DecalBlendNames {
		if n == name {
			return DecalBlend(i)
		}
	}
	return DecalBlendAlpha
}

// ---------- what the shading stage has to know ----------

// SurfaceBlend is how the framebuffer blend will combine a world-program
// fragment, which the SHADER needs told because two of the three answers are not
// a colour at all: an additive fragment only adds light, and a modulate one is a
// factor on what is already there rather than a surface of its own. See
// fragment.glsl's `surfaceBlend`, whose values these are.
//
// It is a separate type from DecalBlend even though a decal is its only user
// today, because it describes a DRAW rather than an authored choice: an additive
// sprite drawn through the world program would want SurfaceBlendAdditive with no
// DecalBlend anywhere in sight.
type SurfaceBlend int32

const (
	// SurfaceBlendNormal is the ordinary case, and the zero value — so every
	// surface in the engine that has never heard of this gets it.
	SurfaceBlendNormal SurfaceBlend = 0
	// SurfaceBlendAdditive is a fragment that only ever adds light.
	SurfaceBlendAdditive SurfaceBlend = 1
	// SurfaceBlendModulate is a fragment that is a factor on the framebuffer.
	SurfaceBlendModulate SurfaceBlend = 2
)

// SurfaceBlendFor is the shading answer for a decal blend mode. The mapping is
// not the identity and must not be assumed to be: DecalBlend's values are the
// order the UI lists them in, and these are the shader's.
func SurfaceBlendFor(b DecalBlend) SurfaceBlend {
	switch b {
	case DecalBlendMultiply:
		return SurfaceBlendModulate
	case DecalBlendAdditive:
		return SurfaceBlendAdditive
	}
	return SurfaceBlendNormal
}
