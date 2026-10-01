#version 430

// sprite_frag.glsl — colour-pass fragment stage for the instanced sprite pass.
//
// Three blend modes reach this shader, and they differ in what `alpha` means,
// which is why the mode is a uniform rather than something the blend state
// alone could express:
//
//   cutout    alpha is COVERAGE. The pass runs with GL_SAMPLE_ALPHA_TO_COVERAGE
//             on, so the sharpen ramp below hands MSAA a one-pixel edge and the
//             silhouette antialiases instead of stair-stepping. Depth is
//             written; nothing sorts. This is fragment.glsl's alphaSharpen,
//             reproduced here because this program does not include it.
//   alpha     alpha is BLEND WEIGHT, sorted back to front by the caller.
//   additive  alpha scales what is added; nothing is ever occluded.
//
// A fourth value is never authored: a cutout sprite whose own alpha is below 1
// is drawn in the alpha pass as "cutout, fading" — cut against the texture,
// blended by the sprite. See spriteBatch.FadingCutout in sprite_render.go.
//
// Lighting is opt-in and, when on, is the same biome_light.glsl block the
// scatter and ground passes use — identical uniform names to fragment.glsl, so
// UploadLightsTo and the two shadow uploads drive this program unchanged.

in vec2 fragTexCoord;
in vec3 fragWorldPos;
in vec3 fragNormal;
in vec4 fragColor;

uniform sampler2D tex;

// 0 = cutout, 1 = alpha, 2 = additive — gfx.SpriteBlend — and 3 = cutout,
// fading (spriteModeFadingCutout).
uniform int blendMode;

// alphaCutoff is where a cutout sprite stops being drawn at all. Matched to
// fragment.glsl's own so a sprite and a cutout wall dissolve alike.
uniform float alphaCutoff;

// lit gates the whole lighting block. Off is the default and is right for
// anything emissive — a flame lit by the sun looks wrong.
uniform int lit;
uniform int receiveShadows;

out vec4 outColor;

void main() {
    vec4 texel = texture(tex, fragTexCoord);
    vec3 rgb   = texel.rgb * fragColor.rgb;
    float a    = texel.a * fragColor.a;

    if (lit != 0) {
        rgb = giDebugAlbedo(rgb);
        rgb *= biomeLighting(fragWorldPos, normalize(fragNormal),
                             biomeViewDepth(fragWorldPos), receiveShadows != 0, true);
    }

    // Fog, before the blend-mode branch so every mode is fogged, but by the
    // rule that mode needs: an additive sprite draws over a framebuffer that
    // already holds the fog behind it, so it is swallowed by the fog rather
    // than mixed toward it. See applyFogAdditive in fog.glsl.
    if (blendMode == 2) {
        rgb = applyFogAdditive(rgb, fragWorldPos);
    } else {
        rgb = applyFog(rgb, fragWorldPos);
    }

    if (blendMode == 0) {
        // Cutout. Discard first so the depth buffer never records a hole, then
        // convert what survives into a coverage ramp one pixel wide. fwidth is
        // the screen-space rate of change, so the ramp stays a pixel however
        // far away the sprite is — the trick fragment.glsl uses, and the reason
        // a cutout sprite needs no sorting to look right.
        if (a < alphaCutoff) {
            discard;
        }
        a = clamp((a - alphaCutoff) / max(fwidth(a), 1e-5) + 0.5, 0.0, 1.0);
    } else if (blendMode == 3) {
        // Cutout mid-fade, drawn in the alpha pass. The cut is taken against
        // the TEXTURE's alpha alone, so the silhouette stays where it is while
        // the sprite's own alpha fades it; testing the product instead is what
        // left a fading cutout invisible until the threshold and then whole.
        // The same one-pixel ramp as above, blended rather than covered.
        if (texel.a < alphaCutoff) {
            discard;
        }
        a = clamp((texel.a - alphaCutoff) / max(fwidth(texel.a), 1e-5) + 0.5, 0.0, 1.0)
          * fragColor.a;
        if (a <= 0.0) {
            discard;
        }
    } else if (a <= 0.0) {
        // Nothing to add and nothing to blend; skipping keeps a fully
        // transparent particle from touching the framebuffer at all.
        discard;
    }

    // Every mode writes the same thing; what differs is the blend func the
    // caller set. Additive runs (SRC_ALPHA, ONE), so premultiplying alpha here
    // would apply it twice.
    outColor = vec4(rgb, a);
}
