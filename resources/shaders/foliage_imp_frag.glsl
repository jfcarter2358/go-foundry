#version 430

// foliage_imp_frag.glsl — a foliage impostor's pixels
// (render/foliage_impostor.go): the three nearest baked views blended, then
// coloured with the instance's own colours and lit per pixel from the baked
// normals, the way foliage_vert.glsl lights a mesh vertex, so an impostor
// and the far mesh it replaces agree under any sun. The cut-out, fades and
// fog are foliage_frag.glsl's.

in vec2 vUV0;
in vec2 vUV1;
in vec2 vUV2;
flat in vec2 vCell0;
flat in vec2 vCell1;
flat in vec2 vCell2;
flat in vec3 vWeights;
flat in float vLayer;
flat in vec3 vAxX;
flat in vec3 vAxY;
flat in vec3 vAxZ;
flat in vec3 vColor;
flat in vec3 vAccent;
flat in float vSeed;
in float vFade;
in vec2 vXfade;
in vec3 vWorldPos;

uniform sampler2DArray impAlbedo;
uniform sampler2DArray impNormal;
uniform float impGrid;
uniform int alphaToCoverage;

// TAA's stand-in for alpha-to-coverage (render/taa.go; fragment.glsl has the
// full story): 0 off, else the frame's place in the jitter sequence plus one.
// MUST match fragment.glsl's coverageNoise.
uniform int coverageDither;
float coverageNoise(vec2 p, int frame) {
    float ign = fract(52.9829189 * fract(dot(p, vec2(0.06711056, 0.00583715))));
    return fract(ign + float(frame) * 0.125);
}
uniform int receiveShadows;
uniform vec3 barkColor;
uniform vec3 camPos;

out vec4 outputColor;

float bayer4(vec2 p) {
    ivec2 i = ivec2(mod(p, 4.0));
    int m[16] = int[16](0, 8, 2, 10, 12, 4, 14, 6, 3, 11, 1, 9, 15, 7, 13, 5);
    return (float(m[i.y * 4 + i.x]) + 0.5) / 16.0;
}

// One view's texels: clear outside it, so a neighbour never bleeds in.
void view(vec2 cell, vec2 uv, float w, inout vec4 alb, inout vec4 nrm, inout float used) {
    if (w <= 0.0) {
        return;
    }
    const float inset = 0.5 / 48.0;
    if (any(lessThan(uv, vec2(0.0))) || any(greaterThan(uv, vec2(1.0)))) {
        return;
    }
    vec3 at = vec3(cell + clamp(uv, vec2(inset), vec2(1.0 - inset)) / impGrid, vLayer);
    alb += w * texture(impAlbedo, at);
    nrm += w * texture(impNormal, at);
    used += w;
}

void main() {
    if (vFade < 1.0 && vFade < bayer4(gl_FragCoord.xy)) {
        discard;
    }
    float x = bayer4(gl_FragCoord.xy + vec2(1.0, 2.0));
    if (x < 1.0 - vXfade.x || x >= vXfade.y) {
        discard;
    }
    vec4 alb = vec4(0.0);
    vec4 nrm = vec4(0.0);
    float used = 0.0;
    view(vCell0, vUV0, vWeights.x, alb, nrm, used);
    view(vCell1, vUV1, vWeights.y, alb, nrm, used);
    view(vCell2, vUV2, vWeights.z, alb, nrm, used);
    if (used <= 0.0) {
        discard;
    }
    float a = alb.a;
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
    // A texel's channels hold their own value (the bake bled them into the
    // clear texels too); coverage alone is summed as it is, a view that does
    // not reach this pixel being clear there.
    vec3 ch = alb.rgb / used;

    float lum = ch.r * 1.5;
    vec3 bark = barkColor * (0.8 + 0.4 * vSeed);
    vec3 base = mix(mix(vColor, vAccent, clamp(ch.g, 0.0, 1.0)), bark, clamp(ch.b, 0.0, 1.0)) * lum;
    vec3 nl = nrm.xyz / used * 2.0 - 1.0;
    vec3 n = normalize(vAxX * nl.x + vAxY * nl.y + vAxZ * nl.z + vec3(0.0, 1e-4, 0.0));
    bool thin = nrm.a / used > 0.5;

    float viewDepth = biomeViewDepth(vWorldPos);
    vec3 lighting = biomeLighting(vWorldPos, n, viewDepth, receiveShadows != 0, thin);
    base = giDebugAlbedo(base);
    vec3 col = base * clamp(lighting, 0.0, 1.2);
    if (thin) {
        vec3 V = normalize(camPos - vWorldPos);
        for (int i = 0; i < numDirLights; i++) {
            vec3 ld = -normalize(dirLightDir[i]);
            float through = pow(max(dot(-V, ld), 0.0), 4.0);
            col += base * dirLightColor[i] * dirLightBrightness[i] * through * 0.3;
        }
    }
    col = applyFog(col, vWorldPos);
    outputColor = vec4(col, clamp(a, 0.0, 1.0));
}
