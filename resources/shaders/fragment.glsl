#version 410

uniform sampler2D tex;
uniform float alpha; // per-geometry alpha multiplier (default 1.0)

// Alpha-cutout anti-aliasing, set for the opaque pass only (see
// AlphaSharpenUniform in main.go). Off (0) by default so any program that
// links this shader without knowing about it — and the sorted transparent
// pass, where alpha means blending, not coverage — behaves exactly as before.
uniform int alphaSharpen;

// Where a cutout's edge sits once mip filtering has turned its hard 0/255
// alpha into a ramp: the level set that best reproduces the original
// silhouette is the halfway point, not "any alpha at all".
const float alphaCutoff = 0.5;

// Water surface. isWater gates the whole path.
uniform int   isWater;
uniform float time;
uniform float waveAmplitude;
uniform float waveLength;
uniform float waveSpeed;
uniform float reflectivity;
uniform vec3  reflectColor;
uniform vec3  camPos;

// Global ambient light
uniform vec3  globalLightColor;
uniform float globalLightBrightness;

// Directional lights (e.g. sun)
#define MAX_DIR_LIGHTS 4
uniform int   numDirLights;
uniform vec3  dirLightDir[MAX_DIR_LIGHTS];
uniform vec3  dirLightColor[MAX_DIR_LIGHTS];
uniform float dirLightBrightness[MAX_DIR_LIGHTS];

// Directional cascaded shadow maps.
//
// SHADOW_CASCADES, SHADOW_CASCADE_BLEND and SHADOW_FADE_FRACTION must match
// ShadowCascades / ShadowCascadeBlend / shadowFadeFraction in shadow.go.
#define SHADOW_CASCADES       3
#define SHADOW_CASCADE_BLEND  0.12
#define SHADOW_FADE_FRACTION  0.15

// One array texture holds every cascade of every casting light — MAX_DIR_LIGHTS
// * SHADOW_CASCADES separate sampler2Ds would blow the 16 fragment texture
// units GL 4.1 guarantees.
// A comparison sampler: each lookup passes the fragment's own depth as a
// reference and gets back the fraction of the four filtered texels it is in
// front of, so a tap is a smooth 0..1 rather than a per-texel yes/no. The
// compare mode lives on the texture (EnsureCascadeArray in shadow.go).
uniform sampler2DArrayShadow shadowMapArray;
uniform mat4  cascadeMatrix[MAX_DIR_LIGHTS * SHADOW_CASCADES];
uniform float cascadeBias[MAX_DIR_LIGHTS * SHADOW_CASCADES];
uniform float cascadeSplit[SHADOW_CASCADES]; // view-space distance each cascade ends at
uniform int   dirLightShadowLayer[MAX_DIR_LIGHTS]; // first array layer, -1 = light doesn't cast
uniform bool  dirLightCastShadows[MAX_DIR_LIGHTS];
uniform float dirLightShadowStrength[MAX_DIR_LIGHTS];

// Point lights
#define MAX_POINT_LIGHTS 8
uniform int   numPointLights;
uniform vec3  pointLightPos[MAX_POINT_LIGHTS];
uniform vec3  pointLightColor[MAX_POINT_LIGHTS];
uniform float pointLightBrightness[MAX_POINT_LIGHTS];
uniform float pointLightRadius[MAX_POINT_LIGHTS];

// Point light cubemap shadow maps
uniform samplerCube pointShadowMap[MAX_POINT_LIGHTS];
uniform bool        pointLightCastShadows[MAX_POINT_LIGHTS];
uniform float       pointLightShadowStrength[MAX_POINT_LIGHTS];
uniform float       pointLightFarPlane[MAX_POINT_LIGHTS];

// Smooth shading. When set, the surface normal is the interpolated per-vertex
// normal supplied by the vertex/tessellation stage instead of the flat facet
// the derivatives below recover — see smooth.go.
uniform int smoothShading;

in vec2 fragTexCoord;
in vec3 fragWorldPos;
in vec3 fragNormal;
// Distance along the view axis, used to pick a cascade. The light-space
// positions themselves are derived here from fragWorldPos rather than passed as
// varyings — a vec4 per light per cascade would be 48 float varyings against a
// guaranteed budget of 60.
in float fragViewDepth;

out vec4 outputColor;

// MUST stay identical to waterHeight() in vertex.glsl — see the note there.
float waterHeight(vec2 p) {
    float k = 6.2831853 / max(waveLength, 0.001);
    float t = time * waveSpeed * 6.2831853;

    float h = 0.0;
    h += sin(p.x * k + t) * 0.50;
    h += sin((p.x * 0.7 + p.y * 0.7) * k * 1.3 - t * 1.1) * 0.30;
    h += sin(p.y * k * 0.8 + t * 0.8) * 0.20;
    return h * waveAmplitude;
}

// Central-difference gradient of the wave field. Done per-pixel rather than
// per-vertex so the swell stays smooth regardless of mesh density, and so no
// extra varying is needed — this fragment shader is also linked against the
// detail tessellation and scatter vertex stages, which know nothing of water.
vec3 waterNormal(vec2 p) {
    float e  = max(waveLength * 0.02, 0.01);
    float hL = waterHeight(p - vec2(e, 0.0));
    float hR = waterHeight(p + vec2(e, 0.0));
    float hD = waterHeight(p - vec2(0.0, e));
    float hU = waterHeight(p + vec2(0.0, e));
    return normalize(vec3(hL - hR, 2.0 * e, hD - hU));
}

// 5x5 PCF sample of a single cascade layer. Returns occlusion in 0..1, or -1.0
// when the fragment falls outside this cascade's box — the caller then falls
// through to a wider cascade rather than reporting the fragment as lit.
float sampleCascade(int matIdx, int layer, vec3 normal, vec3 lightDir) {
    vec4 posLS = cascadeMatrix[matIdx] * vec4(fragWorldPos, 1.0);
    vec3 projCoords = posLS.xyz / posLS.w;
    projCoords = projCoords * 0.5 + 0.5;

    if (projCoords.x < 0.0 || projCoords.x > 1.0 ||
        projCoords.y < 0.0 || projCoords.y > 1.0 ||
        projCoords.z > 1.0) {
        return -1.0;
    }

    // Quadratic slope-scaled bias — tighter than linear at seams. The base bias
    // is per-cascade because each cascade normalizes the same world-space bias
    // over its own depth range.
    float base = cascadeBias[matIdx];
    float cosTheta = abs(dot(normal, lightDir));
    float bias = base * (1.0 - cosTheta) * (1.0 - cosTheta) + base * 0.05;
    float currentDepth = projCoords.z;

    // 5x5 PCF for softer edges. Every tap is itself bilinearly filtered by the
    // comparison sampler, so the result is continuous across a texel rather
    // than stepping from one whole texel to the next — which is what kept
    // shadow edges on nearby geometry looking like a staircase of blocks, a
    // cascade-0 texel being centimetres wide in the world but tens of pixels
    // wide on screen when the surface is a couple of metres from the camera.
    float ref = currentDepth - bias;
    float lit = 0.0;
    vec2 texelSize = 1.0 / vec2(textureSize(shadowMapArray, 0).xy);
    for (int x = -2; x <= 2; x++) {
        for (int y = -2; y <= 2; y++) {
            lit += texture(shadowMapArray,
                vec4(projCoords.xy + vec2(x, y) * texelSize, float(layer), ref));
        }
    }
    return 1.0 - lit / 25.0;
}

// Cascaded directional shadow lookup: pick the tightest cascade covering this
// fragment, cross-fade into the next at the split, and fade out entirely as the
// shadow range runs out.
float calcDirShadow(int lightIdx, vec3 normal, vec3 lightDir) {
    int layerBase = dirLightShadowLayer[lightIdx];
    if (layerBase < 0) {
        return 0.0;
    }

    int c = SHADOW_CASCADES;
    for (int i = 0; i < SHADOW_CASCADES; i++) {
        if (fragViewDepth <= cascadeSplit[i]) {
            c = i;
            break;
        }
    }
    if (c >= SHADOW_CASCADES) {
        return 0.0; // beyond ShadowMaxDistance — unshadowed
    }

    int matBase = lightIdx * SHADOW_CASCADES;
    float shadow = sampleCascade(matBase + c, layerBase + c, normal, lightDir);
    if (shadow < 0.0) {
        // A fragment at the corner of a slice can sit just outside its own
        // cascade's box; the next one out always contains it.
        if (c + 1 < SHADOW_CASCADES) {
            c += 1;
            shadow = sampleCascade(matBase + c, layerBase + c, normal, lightDir);
        }
        if (shadow < 0.0) {
            return 0.0;
        }
    }

    // Cross-fade over the tail of this cascade so the jump in texel density
    // reads as a softening rather than a seam.
    float prev = (c == 0) ? 0.0 : cascadeSplit[c - 1];
    float span = max(cascadeSplit[c] - prev, 1e-4);
    float t = (fragViewDepth - prev) / span;
    if (c + 1 < SHADOW_CASCADES && t > 1.0 - SHADOW_CASCADE_BLEND) {
        float next = sampleCascade(matBase + c + 1, layerBase + c + 1, normal, lightDir);
        if (next >= 0.0) {
            shadow = mix(shadow, next, (t - (1.0 - SHADOW_CASCADE_BLEND)) / SHADOW_CASCADE_BLEND);
        }
    }

    // Fade the shadow term out approaching the range limit, so the boundary
    // isn't a hard line across the ground.
    float maxDist = cascadeSplit[SHADOW_CASCADES - 1];
    float fadeBegin = maxDist * (1.0 - SHADOW_FADE_FRACTION);
    shadow *= 1.0 - clamp((fragViewDepth - fadeBegin) / max(maxDist - fadeBegin, 1e-4), 0.0, 1.0);

    return shadow;
}

// Sample offsets for soft point light shadows (20 directions).
const int NUM_POINT_SAMPLES = 20;
const vec3 sampleOffsets[20] = vec3[](
    vec3( 1,  1,  1), vec3( 1, -1,  1), vec3(-1, -1,  1), vec3(-1,  1,  1),
    vec3( 1,  1, -1), vec3( 1, -1, -1), vec3(-1, -1, -1), vec3(-1,  1, -1),
    vec3( 1,  1,  0), vec3( 1, -1,  0), vec3(-1, -1,  0), vec3(-1,  1,  0),
    vec3( 1,  0,  1), vec3(-1,  0,  1), vec3( 1,  0, -1), vec3(-1,  0, -1),
    vec3( 0,  1,  1), vec3( 0, -1,  1), vec3( 0, -1, -1), vec3( 0,  1, -1)
);

// Point light cubemap shadow sampling with PCF-like disk.
float calcPointShadow(int lightIdx, vec3 fragPos) {
    vec3 fragToLight = fragPos - pointLightPos[lightIdx];
    float currentDist = length(fragToLight);
    float farPlane = pointLightFarPlane[lightIdx];

    // Disk radius scales with distance for consistent softness
    float diskRadius = (1.0 + currentDist / farPlane) * 0.02;
    float bias = 0.05;

    float shadow = 0.0;
    for (int i = 0; i < NUM_POINT_SAMPLES; i++) {
        float closestDist = texture(pointShadowMap[lightIdx], fragToLight + sampleOffsets[i] * diskRadius).r;
        closestDist *= farPlane; // stored as dist/farPlane, convert back
        if (currentDist - bias > closestDist) {
            shadow += 1.0;
        }
    }
    shadow /= float(NUM_POINT_SAMPLES);

    return shadow;
}

void main() {
    // Flat facet normal from screen-space position derivatives. Correct for a
    // flat surface and the fallback whenever no better normal is available.
    vec3 faceNormal = normalize(cross(dFdx(fragWorldPos), dFdy(fragWorldPos)));

    vec3 normal;
    if (isWater != 0) {
        // Re-derive from the wave field; the facet normal would only recover the
        // flat triangle and lose the swell entirely.
        normal = waterNormal(fragWorldPos.xz);
    } else if (smoothShading != 0 && dot(fragNormal, fragNormal) > 1e-12) {
        // Interpolated across the triangle, so lighting varies continuously
        // instead of stepping at every shared edge.
        normal = normalize(fragNormal);
    } else {
        normal = faceNormal;
    }

    // Flip normal for back faces so double-sided surfaces are lit correctly.
    if (!gl_FrontFacing) {
        normal = -normal;
    }

    vec2 uv = fragTexCoord;
    if (isWater != 0) {
        // Two layers scrolling at different speeds/headings so the texture
        // never reads as a rigid sheet sliding across the surface.
        float t = time * waveSpeed;
        uv += vec2(t * 0.03, t * 0.02);
    }

    vec4 texColor = texture(tex, uv);
    if (isWater != 0) {
        vec2 uv2 = fragTexCoord * 1.7 - vec2(time * waveSpeed * 0.021, time * waveSpeed * 0.034);
        texColor = mix(texColor, texture(tex, uv2), 0.5);
    }

    // Start with global ambient contribution
    vec3 lighting = globalLightColor * globalLightBrightness;

    // Directional lights with soft shadows
    for (int i = 0; i < numDirLights; i++) {
        vec3 ldir = -normalize(dirLightDir[i]);
        float diff = abs(dot(normal, ldir));
        vec3 contrib = dirLightColor[i] * dirLightBrightness[i] * diff;

        if (dirLightCastShadows[i]) {
            float shadow = calcDirShadow(i, normal, ldir);
            // Shadow strength: 0 = no darkening, 1 = full shadow
            contrib *= (1.0 - shadow * dirLightShadowStrength[i]);
        }

        lighting += contrib;
    }

    // Point lights with quadratic falloff and cubemap shadows
    for (int i = 0; i < numPointLights; i++) {
        vec3  toLight = pointLightPos[i] - fragWorldPos;
        float dist    = length(toLight);
        if (dist < pointLightRadius[i]) {
            float t           = 1.0 - (dist / pointLightRadius[i]);
            float attenuation = t * t;
            float diff        = abs(dot(normal, normalize(toLight)));
            vec3 contrib = pointLightColor[i] * pointLightBrightness[i] * diff * attenuation;

            if (pointLightCastShadows[i]) {
                float shadow = calcPointShadow(i, fragWorldPos);
                contrib *= (1.0 - shadow * pointLightShadowStrength[i]);
            }

            lighting += contrib;
        }
    }

    float finalAlpha = texColor.a * alpha;

    if (alphaSharpen != 0) {
        // Re-derive a crisp, roughly one-pixel-wide edge from the soft alpha
        // ramp mip filtering leaves behind, and hand that to
        // alpha-to-coverage (SetAlphaToCoverage, antialias.go) as the
        // fraction of the pixel the cutout actually covers.
        //
        // Without this, mipmapping and cutouts fight each other: distant mip
        // levels average a foliage texture's alpha down below the cutoff over
        // more and more of the shape, so it thins and eventually dissolves —
        // the classic regression from turning mipmaps on over alpha-tested
        // art. Rescaling by fwidth is what makes the edge's *screen-space*
        // width constant instead of its texture-space width: distance is
        // divided back out, so the silhouette stays put and only the last
        // pixel of it is partially covered.
        //
        // On a fully opaque texel fwidth is 0, the division saturates, and
        // the clamp pins coverage at 1 — ordinary opaque geometry is
        // untouched by this path, which is why it can stay on for the whole
        // opaque pass rather than being switched per surface.
        finalAlpha = clamp((finalAlpha - alphaCutoff) / max(fwidth(finalAlpha), 1e-5) + 0.5, 0.0, 1.0);
    }

    if (finalAlpha < 0.001) discard;

    vec3 surface = texColor.rgb * clamp(lighting, 0.0, 1.0);

    if (isWater != 0) {
        vec3 viewDir = normalize(camPos - fragWorldPos);

        // Schlick fresnel: water is barely reflective looking straight down and
        // near-mirror at grazing angles, which is what sells it as a surface.
        float cosTheta = clamp(dot(normal, viewDir), 0.0, 1.0);
        float fresnel  = reflectivity + (1.0 - reflectivity) * pow(1.0 - cosTheta, 5.0);

        surface = mix(surface, reflectColor * clamp(lighting, 0.0, 1.0), fresnel);

        // Blinn-Phong glint from each directional light — the moving specular
        // highlight that reads as sun on water.
        for (int i = 0; i < numDirLights; i++) {
            vec3  ldir = -normalize(dirLightDir[i]);
            vec3  halfVec = normalize(ldir + viewDir);
            float spec = pow(max(dot(normal, halfVec), 0.0), 128.0);
            surface += dirLightColor[i] * dirLightBrightness[i] * spec * fresnel;
        }

        // Grazing angles also read as more opaque, matching the reflection.
        finalAlpha = clamp(finalAlpha + fresnel * (1.0 - finalAlpha) * 0.5, 0.0, 1.0);
    }

    outputColor = vec4(surface, finalAlpha);
}
