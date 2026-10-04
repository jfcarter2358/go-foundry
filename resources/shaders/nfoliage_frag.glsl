#version 430

// nfoliage_frag.glsl — the Nanite foliage's triangle and part pixels
// (render/nfoliage.go): foliage_frag.glsl's cut-out, rank dissolve and fog,
// without its tier cross-fades — this path has no tiers to fade between; the
// cluster DAG's cut changes level where the change is under a pixel — and
// with each pixel's own motion for TAA, written to the scene target's motion
// attachment (render/scene_target.go) where the vertex stage worked it out.

in vec3 vColor;
in vec2 vUV;
flat in int vPart;
in float vFade;
in vec3 vWorldPos;
in vec4 vClip;
in vec4 vPrevClip;

uniform sampler2D atlas;
uniform int alphaToCoverage;
uniform int writeMotion;

// TAA's stand-in for alpha-to-coverage (render/taa.go; fragment.glsl has the
// full story): 0 off, else the frame's place in the jitter sequence plus one.
// MUST match fragment.glsl's coverageNoise.
uniform int coverageDither;
float coverageNoise(vec2 p, int frame) {
    float ign = fract(52.9829189 * fract(dot(p, vec2(0.06711056, 0.00583715))));
    return fract(ign + float(frame) * 0.125);
}

layout(location = 0) out vec4 outputColor;
layout(location = 2) out vec2 outMotion;

float bayer4(vec2 p) {
    ivec2 i = ivec2(mod(p, 4.0));
    int m[16] = int[16](0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5);
    return (float(m[i.y * 4 + i.x]) + 0.5) / 16.0;
}

void main() {
    if (vFade < 1.0 && vFade < bayer4(gl_FragCoord.xy)) {
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
    c = applyFog(c, vWorldPos);
    outputColor = vec4(c, clamp(a, 0.0, 1.0));
    outMotion = vec2(16384.0);
    if (writeMotion != 0) {
        outMotion = (vClip.xy / vClip.w - vPrevClip.xy / vPrevClip.w) * 0.5;
    }
}
