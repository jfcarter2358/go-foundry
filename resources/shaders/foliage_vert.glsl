#version 430

// foliage_vert.glsl — colour-pass vertex stage for terrain foliage
// (render/foliage.go). Placement is foliage_common.glsl; this stage lights.
//
// Lit per vertex through biome_light.glsl, the scatter's reasoning: a blade
// or a leaf card is too small for per-pixel light to show, and one
// 3x3-averaged cascade tap per vertex costs a fraction of one per pixel.
//
// Leaves and blades take light from both sides, and pass some of it through
// toward the viewer when the sun is behind them — a backlit canopy glows,
// which is most of what makes foliage read as leaves rather than paper. Grass
// blades shade half as the ground under them, so a meadow is one lit surface
// with texture, not a haze of individually lit strips.

uniform mat4 projView;
uniform int receiveShadows;

out vec3 vColor;
out vec2 vUV;
flat out int vPart;
out float vFade;
out vec2 vXfade;
out vec3 vWorldPos;

void main() {
    if (!foliagePlace()) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        vColor = vec3(0.0);
        vUV = vec2(0.0);
        vPart = 0;
        vFade = 0.0;
        vXfade = vec2(0.0);
        vWorldPos = vec3(0.0);
        return;
    }
    int part = int(aParam.y + 0.5);
    float tone = aParam.z;
    vec3 base = iColorRank.rgb;
    if (part == 2 || part == 3) {
        base = iAccentSeed.rgb;
    } else if (part == 4) {
        base = shapeBark[fShape] * (0.8 + 0.4 * iAccentSeed.w);
    }
    bool thin = part == 1 || part == 3 || part == 5;
    vec3 n = fNormal;
    if (part == 5) {
        n = normalize(mix(n, fUp, 0.6));
    }

    float viewDepth = biomeViewDepth(fWorldPos);
    // Sampled a little up the plant's axis, so a blade in a casting sward
    // does not read its own footprint in the depth map.
    vec3 lightAt = fWorldPos + fUp * 0.06;
    vec3 lighting = biomeLighting(lightAt, n, viewDepth, receiveShadows != 0, thin);
    base = giDebugAlbedo(base);
    tone = giDebug == 1 ? 1.0 : tone;
    // Scene units (gfx/hdr.go): hdrLightMax is 1 in LDR.
    vec3 col = base * tone * clamp(lighting, 0.0, 1.2 * hdrLightMax());

    if (thin) {
        vec3 V = normalize(camPos - fWorldPos);
        for (int i = 0; i < numDirLights; i++) {
            vec3 ld = -normalize(dirLightDir[i]);
            float through = pow(max(dot(-V, ld), 0.0), 4.0);
            col += base * dirLightColor[i] * dirLightBrightness[i] * through * 0.3 * tone;
        }
    }

    vColor = col;
    vUV = aUV;
    vPart = part;
    vFade = fFade;
    vXfade = fXfade;
    vWorldPos = fWorldPos;
    gl_Position = projView * vec4(fWorldPos, 1.0);
}
