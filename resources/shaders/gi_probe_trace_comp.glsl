// gi_probe_trace_comp.glsl — tracing and updating the GI probes (render/gi.go).
//
// One workgroup per probe, one invocation per ray. Each ray is sphere-traced
// through the distance field; where it lands it picks up what that surface
// sends back — its albedo times the lights reaching it (each shadowed by a
// ray of its own through the same field) plus last frame's probes there,
// which is how the bounces pile up a frame at a time — and where it leaves
// the field it sees the sky. The rays then fold into the probe's two
// octahedral tiles: irradiance, cosine-weighted, and the mean and mean-square
// distance to what was hit, sharply weighted, for the shading's visibility
// test. A tile's border texels repeat its edge, wrapped as the octahedron
// folds, so bilinear filtering at an edge reads the right neighbour.
//
// GI_RAYS (a define), gi_sdf_common.glsl and light_model.glsl (the probe
// layout and the lighting terms) are spliced in ahead of this file.

layout(local_size_x = GI_RAYS) in;

layout(r11f_g11f_b10f, binding = 0) uniform image2D irradianceImage;
layout(rg16f, binding = 1) uniform image2D visibilityImage;
layout(rgba16f, binding = 2) uniform image2D probeImage;

uniform int   cascade;
uniform int   frameIndex;
uniform int   period;       // update one probe in `period` each frame
uniform int   restPeriod;   // and one resting probe in `restPeriod`
uniform mat3  rayRotation;  // a fresh random turn of the ray set each frame
uniform float maxDistance;  // how far a ray looks before it calls it sky
uniform float hysteresis;   // how much of the old value a texel keeps

// The scene's lights, under the world program's names so world.UploadLightsTo
// fills them. Their GI switches (Light.NoGI) are GI's own.
#define MAX_DIR_LIGHTS 4
#define MAX_POINT_LIGHTS 8
uniform vec3  globalLightColor;
uniform float globalLightBrightness;
uniform int   numDirLights;
uniform vec3  dirLightDir[MAX_DIR_LIGHTS];
uniform vec3  dirLightColor[MAX_DIR_LIGHTS];
uniform float dirLightBrightness[MAX_DIR_LIGHTS];
uniform bool  dirLightCastShadows[MAX_DIR_LIGHTS];
uniform int   numPointLights;
uniform vec3  pointLightPos[MAX_POINT_LIGHTS];
uniform vec3  pointLightColor[MAX_POINT_LIGHTS];
uniform float pointLightBrightness[MAX_POINT_LIGHTS];
uniform float pointLightRadius[MAX_POINT_LIGHTS];
uniform bool  pointLightCastShadows[MAX_POINT_LIGHTS];
uniform int   giDirLightOn[MAX_DIR_LIGHTS];
uniform int   giPointLightOn[MAX_POINT_LIGHTS];

#define MAX_STEPS 32
#define STATE_ACTIVE 1.0
#define STATE_FRESH 2.0
#define STATE_RESTING 4.0

shared vec3  rayRadiance[GI_RAYS];
shared vec3  rayDir[GI_RAYS];
shared float rayDist[GI_RAYS];

// Spherical Fibonacci: n directions spread evenly over the sphere.
vec3 fibonacciDir(int i, int n) {
    const float golden = 2.39996323; // pi * (3 - sqrt(5))
    float y = 1.0 - (float(i) + 0.5) / float(n) * 2.0;
    float r = sqrt(max(1.0 - y * y, 0.0));
    float phi = float(i) * golden;
    return vec3(cos(phi) * r, y, sin(phi) * r);
}

// directAt is the light from the scene's lights reaching a surface at p facing
// n — fragment.glsl's dirLightContrib and pointLightContrib, with the shadow
// maps' answer replaced by a ray through the field, which unlike a shadow map
// also knows about what is behind the camera.
vec3 directAt(vec3 p, vec3 n) {
    vec3 sum = vec3(0.0);
    for (int i = 0; i < numDirLights && i < MAX_DIR_LIGHTS; i++) {
        if (giDirLightOn[i] == 0) {
            continue;
        }
        vec3 l = -normalize(dirLightDir[i]);
        float diff = lightWrapTerm(dot(n, l));
        if (diff <= 0.0) {
            continue;
        }
        vec3 c = dirLightColor[i] * dirLightBrightness[i] * diff;
        if (dirLightCastShadows[i]) {
            c *= giSdfShadow(p, l, maxDistance * 2.0);
        }
        sum += c;
    }
    for (int i = 0; i < numPointLights && i < MAX_POINT_LIGHTS; i++) {
        if (giPointLightOn[i] == 0) {
            continue;
        }
        vec3 toLight = pointLightPos[i] - p;
        float dist = length(toLight);
        if (dist >= pointLightRadius[i] || dist < 1e-4) {
            continue;
        }
        vec3 l = toLight / dist;
        float t = 1.0 - dist / pointLightRadius[i];
        float diff = lightWrapTerm(dot(n, l));
        if (diff <= 0.0) {
            continue;
        }
        vec3 c = pointLightColor[i] * pointLightBrightness[i] * diff * t * t;
        if (pointLightCastShadows[i]) {
            c *= giSdfShadow(p, l, dist);
        }
        sum += c;
    }
    return sum;
}

// bounceAt is last frame's probe light at a ray's hit, for the next bounce:
// the finest cascade that holds p, its eight probes weighted by distance and
// facing only. The shading's visibility test is left out — a bounce of a
// bounce is a small term, and this runs once per ray.
vec3 bounceAt(vec3 p, vec3 n) {
    for (int c = 0; c < giCascades && c < GI_MAX_CASCADES; c++) {
        if (giCascadeCover(c, p) <= 0.0) {
            continue;
        }
        float s = giCascade[c].w;
        vec3 q = (p + n * 0.3 * s) / s;
        ivec3 base = ivec3(floor(q));
        vec3 a = q - vec3(base);
        vec2 irrSize = vec2(textureSize(giIrradianceAtlas, 0));
        vec3 sum = vec3(0.0);
        float wsum = 0.0;
        for (int i = 0; i < 8; i++) {
            ivec3 off = ivec3(i, i >> 1, i >> 2) & ivec3(1);
            ivec3 wi = base + off;
            ivec2 tile = giTile(giSlot(wi), c);
            vec3 tri = mix(1.0 - a, a, vec3(off));
            vec3 toProbe = normalize(vec3(wi) * s - p);
            float wrap = (dot(toProbe, n) + 1.0) * 0.5;
            float w = tri.x * tri.y * tri.z * (wrap * wrap + 0.2);
            sum += textureLod(giIrradianceAtlas, giAtlasUV(tile, n, GI_IRR_TILE, irrSize), 0.0).rgb * w;
            wsum += w;
        }
        return wsum > 0.0 ? sum / wsum : vec3(0.0);
    }
    vec3 ambient = globalLightColor * globalLightBrightness;
    return ambientAt(ambient, n);
}

// borderSource is the interior texel a tile texel copies: itself inside, and
// on the border the edge texel the octahedron folds onto it.
ivec2 borderSource(ivec2 t, int size) {
    int n = size - 2; // interior texels on a side, at 1..n
    bool bx = t.x == 0 || t.x == n + 1;
    bool by = t.y == 0 || t.y == n + 1;
    if (bx && by) {
        return ivec2(t.x == 0 ? n : 1, t.y == 0 ? n : 1);
    }
    if (by) {
        return ivec2(n + 1 - t.x, t.y == 0 ? 1 : n);
    }
    if (bx) {
        return ivec2(t.x == 0 ? 1 : n, n + 1 - t.y);
    }
    return t;
}

vec3 texelDir(ivec2 src, int size) {
    float n = float(size - 2);
    vec2 oct = (vec2(src - 1) + 0.5) / n * 2.0 - 1.0;
    return giOctDecode(oct);
}

float luminance(vec3 c) { return dot(c, vec3(0.2126, 0.7152, 0.0722)); }

void main() {
    ivec3 slot = ivec3(gl_WorkGroupID.x % uint(giDims.x), gl_WorkGroupID.y, gl_WorkGroupID.x / uint(giDims.x));
    int linear = (slot.z * giDims.y + slot.y) * giDims.x + slot.x;
    ivec2 tile = giTile(slot, cascade);
    vec4 pd = imageLoad(probeImage, tile);
    if (mod(pd.w, 2.0) < 0.5) {
        return; // buried: switched off, and nothing to trace from
    }
    bool fresh = mod(pd.w, 4.0) >= STATE_FRESH;
    // One read of the probe's own state decides for the whole workgroup.
    int every = (pd.w >= STATE_RESTING && !fresh) ? restPeriod : period;
    if ((linear + frameIndex) % every != 0) {
        return;
    }
    ivec3 lo = ivec3(giCascade[cascade].xyz);
    float s = giCascade[cascade].w;
    ivec3 wi = lo + giSlot(slot - lo);
    vec3 origin = vec3(wi) * s + pd.xyz;

    // --- trace: one ray per invocation ---
    int r = int(gl_LocalInvocationIndex);
    vec3 dir = rayRotation * fibonacciDir(r, GI_RAYS);
    vec3 ambient = globalLightColor * globalLightBrightness;
    float t;
    vec3 radiance;
    if (giSdfTrace(origin, dir, maxDistance, MAX_STEPS, t)) {
        vec3 hp = origin + dir * t;
        float voxel;
        giSdf(hp, voxel);
        vec3 n = giSdfNormal(hp, voxel);
        vec4 surf = giSdfSurface(hp);
        vec3 lp = hp + n * voxel; // off the surface, for its own shadow rays
        radiance = surf.rgb * (directAt(lp, n) + bounceAt(lp, n)) + surf.rgb * surf.a;
    } else {
        // The sky, as the flat ambient has always stood for it.
        radiance = ambientAt(ambient, dir);
        t = maxDistance;
    }
    rayRadiance[r] = radiance;
    rayDir[r] = dir;
    rayDist[r] = t;
    barrier();

    // --- fold the rays into the probe's two tiles ---
    const int irrTexels = GI_IRR_TILE * GI_IRR_TILE;
    const int visTexels = GI_VIS_TILE * GI_VIS_TILE;
    for (int k = r; k < irrTexels + visTexels; k += GI_RAYS) {
        bool irr = k < irrTexels;
        int size = irr ? GI_IRR_TILE : GI_VIS_TILE;
        int idx = irr ? k : k - irrTexels;
        ivec2 tt = ivec2(idx % size, idx / size);
        vec3 tdir = texelDir(borderSource(tt, size), size);
        ivec2 texel = tile * size + tt;

        if (irr) {
            vec3 sum = vec3(0.0);
            float wsum = 0.0;
            for (int j = 0; j < GI_RAYS; j++) {
                float w = max(dot(tdir, rayDir[j]), 0.0);
                sum += rayRadiance[j] * w;
                wsum += w;
            }
            vec3 value = wsum > 0.0 ? sum / wsum : vec3(0.0);
            vec3 old = imageLoad(irradianceImage, texel).rgb;
            float h = fresh ? 0.0 : hysteresis;
            // A big change — a flash, a door — is let through quickly rather
            // than faded in over a second.
            float lo2 = luminance(old), ln = luminance(value);
            if (abs(ln - lo2) / max(lo2, 0.05) > 0.5) {
                h = min(h, 0.8);
            }
            imageStore(irradianceImage, texel, vec4(mix(value, old, h), 1.0));
        } else {
            vec2 sum = vec2(0.0);
            float wsum = 0.0;
            for (int j = 0; j < GI_RAYS; j++) {
                // cos^32 by squaring: the sharp lobe DDGI weights distances
                // by, without a pow per ray per texel.
                float w = max(dot(tdir, rayDir[j]), 0.0);
                w *= w; w *= w; w *= w; w *= w; w *= w;
                float d = rayDist[j];
                sum += vec2(d, d * d) * w;
                wsum += w;
            }
            vec2 value = wsum > 0.0 ? sum / wsum : vec2(maxDistance, maxDistance * maxDistance);
            vec2 old = imageLoad(visibilityImage, texel).rg;
            float h = fresh ? 0.0 : hysteresis;
            imageStore(visibilityImage, texel, vec4(mix(value, old, h), 0.0, 0.0));
        }
    }

    // Traced once: no longer fresh.
    if (r == 0 && fresh) {
        imageStore(probeImage, tile, vec4(pd.xyz, pd.w - STATE_FRESH));
    }
}
