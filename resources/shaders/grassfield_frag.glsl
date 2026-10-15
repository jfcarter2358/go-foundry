#version 430

// grassfield_frag.glsl — the grass instancer's fragments (render/grass_field.go).
//
// The clump's albedo is a Poly Haven `_cov` cutout: its alpha is the blade
// shapes, and its mips were built to keep their coverage
// (texcompress/mip_coverage.go), so a distant clump stays a clump rather
// than fading to nothing. With alpha-to-coverage on (the opaque pass turns
// it on under MSAA) the cut is antialiased — the alpha sharpened to a ramp
// about a pixel wide; under TAA, its dither; otherwise a one-half test. An
// instance thinning out is dissolved with an ordered dither, and one handing
// over to its next mesh (tier) keeps the dither cells under its weight
// while the incoming tier takes the rest — foliage_frag.glsl's scheme.

in vec3 vColor;
in vec2 vUV;
in float vFade;
in vec2 vXfade;
in vec3 vWorldPos;

uniform sampler2D grassTex;
uniform int alphaToCoverage;

// TAA's stand-in for alpha-to-coverage (render/taa.go); MUST match
// fragment.glsl's coverageNoise.
uniform int coverageDither;
float coverageNoise(vec2 p, int frame) {
    float ign = fract(52.9829189 * fract(dot(p, vec2(0.06711056, 0.00583715))));
    return fract(ign + float(frame) * 0.125);
}

out vec4 outputColor;

float bayer4(vec2 p) {
    ivec2 i = ivec2(mod(p, 4.0));
    int m[16] = int[16](0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5);
    return (float(m[i.y * 4 + i.x]) + 0.5) / 16.0;
}

void main() {
    if (vFade < 1.0 && vFade < bayer4(gl_FragCoord.xy)) {
        discard;
    }
    float x = bayer4(gl_FragCoord.xy + vec2(1.0, 2.0));
    if (x < 1.0 - vXfade.x || x >= vXfade.y) {
        discard;
    }
    vec4 t = texture(grassTex, vUV);
    float a = t.a;
    if (coverageDither != 0) {
        a = clamp((a - 0.5) / max(fwidth(a), 1e-4) + 0.5, 0.0, 1.0);
        if (a <= coverageNoise(gl_FragCoord.xy, coverageDither)) {
            discard;
        }
        a = 1.0;
    } else if (alphaToCoverage != 0) {
        a = (a - 0.5) / max(fwidth(a), 1e-4) + 0.5;
        if (a <= 0.0) {
            discard;
        }
    } else if (a < 0.5) {
        discard;
    }
    vec3 c = vColor * t.rgb;
    c = applyFog(c, vWorldPos);
    outputColor = vec4(c, clamp(a, 0.0, 1.0));
}
