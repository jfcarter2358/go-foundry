#version 430

// nanite_depth_frag.glsl — the GPU-driven cull's depth prepass
// (render/nanite_prepass.go). Linked with the world program's own vertex
// stage (vertex.glsl, gl_Position invariant), so the depth it writes is the
// depth the colour pass will compute, bit for bit; the colour pass after it
// then shades each covered sample once, at LEQUAL, instead of once per
// surface that reaches it in draw order.
//
// What it must reproduce from fragment.glsl is only what decides COVERAGE:
// the albedo's alpha (or none, for an untextured surface), the blend
// texture's mix of it, the per-surface alpha, and the opaque pass's cutout
// sharpening that alpha-to-coverage turns into samples. Everything that
// decides colour is the colour pass's.

uniform sampler2D tex;
uniform float alpha;
uniform int hasBlend;
uniform sampler2D blendTex;
uniform sampler2D blendMask;
uniform int alphaSharpen;
uniform float alphaCutoffBias;
uniform int normalColor;

in vec2 fragTexCoord;
in vec3 fragWorldPos;
in vec3 fragNormal;
in float fragViewDepth;
in vec4 fragBakedLight;
in vec3 fragBakedIndirect;

out vec4 outputColor;

float alphaCutoffLevel() {
    return clamp(0.5 + alphaCutoffBias, 0.01, 0.99);
}

void main() {
    vec2 uv = fragTexCoord;
    float a = 1.0;
    if (normalColor == 0) {
        vec4 texColor = texture(tex, uv);
        if (hasBlend != 0) {
            vec4 blendColor = texture(blendTex, uv);
            vec3 maskColor = texture(blendMask, uv).rgb;
            float maskLum = dot(maskColor, vec3(0.299, 0.587, 0.114));
            texColor = mix(texColor, blendColor, maskLum);
        }
        a = texColor.a;
    }
    float finalAlpha = a * alpha;
    if (alphaSharpen != 0) {
        finalAlpha = clamp((finalAlpha - alphaCutoffLevel()) / max(fwidth(finalAlpha), 1e-5) + 0.5, 0.0, 1.0);
    }
    if (finalAlpha < 0.001) discard;
    outputColor = vec4(0.0, 0.0, 0.0, finalAlpha);
}
