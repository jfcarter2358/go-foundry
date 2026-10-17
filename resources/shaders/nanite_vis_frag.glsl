#version 430

// nanite_vis_frag.glsl — the visibility buffer's ID pass (render/
// nanite_vis.go). Linked with vertex.glsl as visVertexSource makes it
// (render/nanite_vis_source.go): the world program's own position, so the
// depth is the forward path's to the bit.
//
// What it writes is not a colour but which triangle won the pixel: the
// command record's number (visCmd, one more than its place in the frame's
// record list) in the top 20 bits and the triangle within that command
// (gl_PrimitiveID: a command is at most 32 clusters of 128 triangles, 4096)
// in the bottom 12. The material pass reads the triangle back from them.
//
// What it must still decide here is COVERAGE — whether this pixel is the
// surface's at all — which is nanite_depth_frag.glsl's question: the albedo's
// alpha (none for an untextured surface), the blend texture's mix of it, the
// per-surface alpha and the cutout ramp; in a TAA frame the ramp is dithered
// into a yes or no (fragment.glsl's coverageNoise), with no samples to spend.

uniform sampler2D tex;
uniform float alpha;
uniform int hasBlend;
uniform sampler2D blendTex;
uniform sampler2D blendMask;
uniform int alphaSharpen;
uniform float alphaCutoffBias;
uniform int normalColor;

// MUST match fragment.glsl's coverageNoise.
uniform int coverageDither;
float coverageNoise(vec2 p, int frame) {
    float ign = fract(52.9829189 * fract(dot(p, vec2(0.06711056, 0.00583715))));
    return fract(ign + float(frame) * 0.125);
}

flat in uint visCmd;
// The instance's draw-distance fade (render/nanite_draw_distance.go): the
// band [x, y) of the noise its pixels keep, and its hash in z, which offsets
// the noise so neighbouring instances do not dissolve in step. A mesh fading
// out keeps the noise below the fade, its impostor fading in the noise at or
// above it — the same number — so between them every pixel is drawn once.
flat in vec3 visFade;
in vec2 fragTexCoord;
in vec3 fragWorldPos;
in vec3 fragNormal;
in float fragViewDepth;
in vec4 fragBakedLight;
in vec3 fragBakedIndirect;

layout(location = 0) out uint visID;

float alphaCutoffLevel() {
    return clamp(0.5 + alphaCutoffBias, 0.01, 0.99);
}

void main() {
    if (visFade.x > 0.0 || visFade.y < 1.0) {
        float n = fract(coverageNoise(gl_FragCoord.xy, coverageDither) + visFade.z);
        if (n < visFade.x || n >= visFade.y) {
            discard;
        }
    }
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
        if (coverageDither != 0 && finalAlpha <= coverageNoise(gl_FragCoord.xy, coverageDither)) {
            discard;
        }
    }
    if (finalAlpha < 0.001) discard;
    visID = (visCmd << 12) | (uint(gl_PrimitiveID) & 4095u);
}
