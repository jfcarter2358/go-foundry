#version 410

uniform sampler2D tex;
uniform float alpha; // per-geometry alpha multiplier (default 1.0)

// Global ambient light
uniform vec3  globalLightColor;
uniform float globalLightBrightness;

// Directional lights (e.g. sun)
#define MAX_DIR_LIGHTS 4
uniform int   numDirLights;
uniform vec3  dirLightDir[MAX_DIR_LIGHTS];
uniform vec3  dirLightColor[MAX_DIR_LIGHTS];
uniform float dirLightBrightness[MAX_DIR_LIGHTS];

// Directional shadow maps
uniform sampler2D shadowMap[MAX_DIR_LIGHTS];
uniform bool      dirLightCastShadows[MAX_DIR_LIGHTS];
uniform float     dirLightShadowStrength[MAX_DIR_LIGHTS];
uniform float     shadowBias;

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

in vec2 fragTexCoord;
in vec3 fragWorldPos;
in vec4 fragPosLightSpace[MAX_DIR_LIGHTS];

out vec4 outputColor;

// 5x5 PCF shadow sampling for soft directional shadows.
float calcDirShadow(int lightIdx, vec3 normal, vec3 lightDir) {
    vec4 posLS = fragPosLightSpace[lightIdx];
    vec3 projCoords = posLS.xyz / posLS.w;
    projCoords = projCoords * 0.5 + 0.5;

    // Fragments outside the shadow map are lit
    if (projCoords.x < 0.0 || projCoords.x > 1.0 ||
        projCoords.y < 0.0 || projCoords.y > 1.0 ||
        projCoords.z > 1.0) {
        return 0.0;
    }

    // Quadratic slope-scaled bias — tighter than linear at seams
    float cosTheta = abs(dot(normal, lightDir));
    float bias = shadowBias * (1.0 - cosTheta) * (1.0 - cosTheta) + shadowBias * 0.05;
    float currentDepth = projCoords.z;

    // 5x5 PCF for softer edges
    float shadow = 0.0;
    vec2 texelSize = 1.0 / textureSize(shadowMap[lightIdx], 0);
    for (int x = -2; x <= 2; x++) {
        for (int y = -2; y <= 2; y++) {
            float pcfDepth = texture(shadowMap[lightIdx], projCoords.xy + vec2(x, y) * texelSize).r;
            shadow += (currentDepth - bias > pcfDepth) ? 1.0 : 0.0;
        }
    }
    shadow /= 25.0;

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
    // Derive the surface normal from screen-space position derivatives.
    vec3 dX = dFdx(fragWorldPos);
    vec3 dY = dFdy(fragWorldPos);
    vec3 normal = normalize(cross(dX, dY));

    // Flip normal for back faces so double-sided surfaces are lit correctly.
    if (!gl_FrontFacing) {
        normal = -normal;
    }

    vec4 texColor = texture(tex, fragTexCoord);

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
    if (finalAlpha < 0.001) discard;
    outputColor = vec4(texColor.rgb * clamp(lighting, 0.0, 1.0), finalAlpha);
}
