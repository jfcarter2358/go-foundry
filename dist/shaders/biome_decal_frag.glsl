#version 410

// biome_decal_frag.glsl — fake ground-contact shadow halo, paired with
// biome_decal_vert.glsl. No lighting, no shadow lookup, no texture: the whole
// point is that this is the cheap approximation, not a second real shading
// pass. It multiply-blends a flat darkening over whatever the opaque pass
// already drew — the structural floor beyond the sector's own ground patch, or
// the patch itself, whichever is underneath.
//
// Drawn with glBlendFunc(GL_ZERO, GL_SRC_COLOR) (see RenderBiomeShadowDecal):
// under that equation the fragment's OWN output is the multiplier applied to
// the destination color, so outputColor.rgb = 1-w reads directly as "darken by
// w", with alpha unused by the blend equation and set to 1 for cleanliness.

in float vWeight;

uniform float strength; // BiomeBrush.FakeShadowStrength, 0..1

out vec4 outputColor;

void main() {
    float w = clamp(vWeight * strength, 0.0, 1.0);
    outputColor = vec4(vec3(1.0 - w), 1.0);
}
