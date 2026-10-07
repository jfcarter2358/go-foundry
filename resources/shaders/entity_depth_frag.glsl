#version 430

// entity_depth_frag.glsl — the entities' and the viewmodel's depth prepass
// (render/entity_prepass.go). Linked with the world program's own vertex
// stage (vertex.glsl with its skinning, gl_Position invariant), so the depth
// it writes is the depth the colour pass computes, bit for bit; the colour
// pass after it, at LEQUAL, then shades only the nearest surface of a model
// at each pixel instead of every one of its triangles that reached it first.
//
// It writes depth only where the colour pass is CERTAIN to cover the pixel:
// an untextured surface, or a texel the cutout keeps whole. Anywhere the
// colour pass might discard — a cutout's soft edge, which alpha-to-coverage
// or TAA's per-frame coverage dither (fragment.glsl's coverageNoise) decides
// a sample at a time — it writes nothing, and the colour pass decides and
// writes the depth there itself, as it always did. A prepass that covered a
// pixel the colour pass then discarded would leave a hole the depth of a
// model with nothing drawn in it.

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
    float a = 1.0;
    if (normalColor == 0) {
        vec4 texColor = texture(tex, fragTexCoord);
        if (hasBlend != 0) {
            vec4 blendColor = texture(blendTex, fragTexCoord);
            vec3 maskColor = texture(blendMask, fragTexCoord).rgb;
            float maskLum = dot(maskColor, vec3(0.299, 0.587, 0.114));
            texColor = mix(texColor, blendColor, maskLum);
        }
        a = texColor.a;
    }
    float finalAlpha = a * alpha;
    if (alphaSharpen != 0) {
        finalAlpha = clamp((finalAlpha - alphaCutoffLevel()) / max(fwidth(finalAlpha), 1e-5) + 0.5, 0.0, 1.0);
    }
    if (finalAlpha < 1.0) {
        discard;
    }
    outputColor = vec4(0.0);
}
