#version 430

// foliage_frag.glsl — terrain foliage's fragments (render/foliage.go).
//
// Cards are cut out of the atlas: its alpha is the leaf's shape and its grey
// the leaf's veins and edges, multiplied into the colour lit per vertex.
// With alpha-to-coverage on (the opaque pass turns it on under MSAA), the
// cut is antialiased — the alpha is sharpened to a ramp about a pixel wide,
// the width coverage can resolve; without it, it is a plain one-half test.
// An instance fading out at the end of its range is dissolved with an
// ordered dither rather than blended, so nothing needs sorting. So is one
// handing over to its next tier (near mesh, far mesh, clump card or
// impostor): the outgoing tier keeps the dither cells under its weight and
// the incoming one the rest, so the two together cover every pixel once.

in vec3 vColor;
in vec2 vUV;
flat in int vPart;
in float vFade;
in vec2 vXfade;
in vec3 vWorldPos;

uniform sampler2D atlas;
uniform int alphaToCoverage;

// TAA's stand-in for alpha-to-coverage (render/taa.go; fragment.glsl has the
// full story): 0 off, else the frame's place in the jitter sequence plus one.
// MUST match fragment.glsl's coverageNoise.
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
    vec3 c = vColor;
    float a = 1.0;
    if (vPart == 1 || vPart == 3) {
        vec4 t = texture(atlas, vUV);
        c *= 0.35 + 0.85 * t.r;
        a = t.a;
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
    }
    // Lit in scene units in the vertex stage (gfx/hdr.go).
    c = applyFogScene(c, vWorldPos);
    outputColor = vec4(c, clamp(a, 0.0, 1.0));
}
