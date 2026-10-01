#version 430

// foliage_imp_bake_frag.glsl — what a foliage fragment would be, before it is
// lit or coloured (render/foliage_impostor.go): brightness, how much is
// accent and how much bark, and the normal and thinness the lighting needs.
// The part rules are foliage_vert.glsl's and foliage_frag.glsl's.

in vec3 vNormal;
in vec2 vUV;
flat in int vPart;
in float vTone;

uniform sampler2D atlas;

layout(location = 0) out vec4 outAlbedo; // brightness / 1.5, accent, bark, coverage
layout(location = 1) out vec4 outNormal; // plant-frame normal * 0.5 + 0.5, thin

void main() {
    float lum = vTone;
    if (vPart == 1 || vPart == 3) {
        vec4 t = texture(atlas, vUV);
        if (t.a < 0.5) {
            discard;
        }
        lum *= 0.35 + 0.85 * t.r;
    }
    vec3 n = normalize(vNormal);
    if (vPart == 5) {
        n = normalize(mix(n, vec3(0.0, 1.0, 0.0), 0.6));
    }
    float accent = (vPart == 2 || vPart == 3) ? 1.0 : 0.0;
    float bark = vPart == 4 ? 1.0 : 0.0;
    float thin = (vPart == 1 || vPart == 3 || vPart == 5) ? 1.0 : 0.0;
    outAlbedo = vec4(clamp(lum / 1.5, 0.0, 1.0), accent, bark, 1.0);
    outNormal = vec4(n * 0.5 + 0.5, thin);
}
