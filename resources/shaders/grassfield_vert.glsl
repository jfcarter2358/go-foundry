#version 430

// grassfield_vert.glsl — the colour pass of the Poly Haven grass instancer
// (render/grass_field.go). Placement is grassfield_common.glsl; this stage lights.
//
// Lit per vertex through biome_light.glsl, the scatter's and the terrain
// foliage's reasoning: a blade is narrower than a shadow texel, and one
// evaluation per vertex costs a fraction of one per pixel. A clump shades
// half as the ground it stands on (its normal pulled toward up), so a sward
// reads as one lit surface rather than a haze of individually lit cards,
// lets light through toward the viewer when the sun is behind it, and is
// darker toward its base, where the blades shade each other.

uniform mat4 projView;
uniform int receiveShadows;

out vec3 vColor;
out vec2 vUV;
out float vFade;
out vec2 vXfade;
out vec3 vWorldPos;

void main() {
    if (!grassPlace()) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        vColor = vec3(0.0);
        vUV = vec2(0.0);
        vFade = 0.0;
        vXfade = vec2(0.0);
        vWorldPos = vec3(0.0);
        return;
    }
    vec3 up = vec3(0.0, 1.0, 0.0);
    vec3 n = normalize(mix(gNormal, up, 0.55));
    float viewDepth = biomeViewDepth(gWorldPos);
    vec3 lightAt = gWorldPos + up * 0.06;
#ifdef GRASS_FLAT_LIGHT
    vec3 lighting = vec3(0.8);
#else
    vec3 lighting = biomeLighting(lightAt, n, viewDepth, receiveShadows != 0, true);
#endif
    vec3 base = giDebugAlbedo(gTint);
    // Self-shading toward the base of the clump.
    float ao = giDebug == 1 ? 1.0 : mix(0.5, 1.0, smoothstep(0.0, 0.7, gUp));
    vec3 col = base * ao * clamp(lighting, 0.0, 1.2);
    vec3 V = normalize(camPos - gWorldPos);
    for (int i = 0; i < numDirLights; i++) {
        vec3 ld = -normalize(dirLightDir[i]);
        float through = pow(max(dot(-V, ld), 0.0), 4.0);
        col += base * dirLightColor[i] * dirLightBrightness[i] * through * 0.25 * gUp;
    }
    vColor = col;
    vUV = aUV;
    vFade = gFade;
    vXfade = gXfade;
    vWorldPos = gWorldPos;
    gl_Position = projView * vec4(gWorldPos, 1.0);
}
