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
        if (alphaToCoverage != 0) {
            a = (a - 0.5) / max(fwidth(a), 1e-4) + 0.5;
            if (a <= 0.0) {
                discard;
            }
        } else if (a < 0.5) {
            discard;
        }
    }
    c = applyFog(c, vWorldPos);
    outputColor = vec4(c, clamp(a, 0.0, 1.0));
}
