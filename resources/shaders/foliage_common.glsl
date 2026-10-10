// foliage_common.glsl — placing one foliage instance's vertex
// (render/foliage.go). Spliced into every foliage vertex stage — the colour
// pass, the cascade depth pass and the impostors — after foliage_params.glsl,
// so a tree casts the shadow of the tree on screen, sway included, and every
// stage agrees about which instances are drawn.
//
// A template vertex is in the plant's own frame: y up its growth axis, unit
// scale. The instance carries where it stands (chunk space), its growth axis,
// its turn about that axis, its size, its colours and a random rank. The
// vertex is carried into the chunk, then out to the world by `model`, and the
// wind bends it there, in world space, so every plant in a gust bends the same
// way whatever its chunk's rotation.
//
// Instances arrive one of two ways. Drawn by the CPU (the default), they are
// instanced attributes and the draw says which bucket (shape and variant) and
// which distance window it is. Drawn by the GPU cull (FOLIAGE_GPU,
// render/foliage_gpu.go), each is a reference the cull wrote — which instance
// of the shared pool, which bucket, which tier — and the instance itself is
// read from the pool through a buffer texture.

layout(location = 0) in vec3 aPos;
layout(location = 1) in vec3 aNormal;
layout(location = 2) in vec2 aUV;
// sway (0 root .. 1 tip), part (render/foliage_mesh.go part*), tone.
layout(location = 3) in vec3 aParam;

#ifdef FOLIAGE_GPU
// x: the instance's index in the pool; y: bucket | tier << 8.
layout(location = 4) in uvec2 iRef;
uniform samplerBuffer instances;
vec4 iPosScale;   // chunk-space position, scale
vec4 iUpYaw;      // growth axis, turn about it
vec4 iColorRank;
vec4 iAccentSeed;
#else
layout(location = 4) in vec4 iPosScale;
layout(location = 5) in vec4 iUpYaw;
layout(location = 6) in vec4 iColorRank;
layout(location = 7) in vec4 iAccentSeed;
// The bucket this draw is, and the distance window it draws over.
uniform int drawBucket;
uniform vec2 drawWindow;
#endif

uniform mat4 model;
// The inverse-transpose of model's upper 3x3, uploaded with it.
uniform mat3 normalMatrix;
uniform vec3 camPos;
uniform float time;
uniform vec2 windDir;
uniform float windStrength;

int fBucket;
int fShape;
vec2 fWindow;
// The plant's frame in chunk space: across, up its growth axis, and the third.
vec3 fX;
vec3 fUpL;
vec3 fZ;
vec3 fAnchor;
float fDist;
vec3 fWorldPos;
vec3 fNormal;
vec3 fUp;
// Rank fade, and the tier cross-fade (in, out) — foliage_params.glsl.
float fFade;
vec2 fXfade;
// How much the instance grows to cover for thinned neighbours.
float fComp;

void foliageFetch() {
#ifdef FOLIAGE_GPU
    int b = int(iRef.x) * 4;
    iPosScale = texelFetch(instances, b);
    iUpYaw = texelFetch(instances, b + 1);
    iColorRank = texelFetch(instances, b + 2);
    iAccentSeed = texelFetch(instances, b + 3);
    fBucket = int(iRef.y & 0xffu);
    fShape = bucketInfo[fBucket].x;
    fWindow = foliageTierWindow(fShape, int(iRef.y >> 8u));
#else
    fBucket = drawBucket;
    fShape = bucketInfo[fBucket].x;
    fWindow = drawWindow;
#endif
}

// foliageBegin reads the instance and decides whether it is drawn at all:
// false when its rank is thinned out at its distance or it is outside this
// tier's window. On true the plant's frame, fade and growth are set.
bool foliageBegin() {
    foliageFetch();
    vec3 up = normalize(iUpYaw.xyz);
    vec3 ref = abs(up.y) < 0.95 ? vec3(0.0, 1.0, 0.0) : vec3(1.0, 0.0, 0.0);
    vec3 t0 = normalize(cross(ref, up));
    vec3 b0 = cross(up, t0);
    float c = cos(iUpYaw.w), s = sin(iUpYaw.w);
    fX = t0 * c + b0 * s;
    fZ = cross(fX, up);
    fUpL = up;

    fAnchor = (model * vec4(iPosScale.xyz, 1.0)).xyz;
    fDist = length(fAnchor - camPos);
    vec4 R = shapeRange[fShape];
    float fade = foliageRankFade(foliageKeep(R, fDist), iColorRank.w);
    if (fade <= 0.0) {
        return false;
    }
    vec2 w = foliageTierWeights(fWindow, fDist);
    if (w.x <= 0.0 || w.y <= 0.0) {
        return false;
    }
    fFade = fade;
    fXfade = w;
    fComp = foliageComp(R, fDist);
    return true;
}

bool foliagePlace() {
    if (!foliageBegin()) {
        return false;
    }
    vec3 up = fUpL;
    vec3 X = fX;
    vec3 Z = fZ;
    vec3 anchor = fAnchor;

    float scale = iPosScale.w;
    if (shapeMore[fShape].y > 0.5) {
        scale *= mix(0.25, 1.0, fFade);
    }
    // Survivors of the thinning grow, more across than up.
    vec3 grow = vec3(fComp, pow(fComp, shapeMore[fShape].x), fComp);
    vec3 lp = aPos * scale * grow;
    vec3 local = iPosScale.xyz + X * lp.x + up * lp.y + Z * lp.z;
    vec3 n = X * aNormal.x + up * aNormal.y + Z * aNormal.z;
    vec4 w = model * vec4(local, 1.0);
    fNormal = normalize(normalMatrix * n);
    fUp = normalize(mat3(model) * up);

    // Wind: a slow travelling gust — waves you can watch cross a meadow —
    // and a quick flutter, both growing toward the tip.
    float sway = aParam.x;
    if (sway > 0.0 && windStrength > 0.0) {
        vec3 wd = vec3(windDir.x, 0.0, windDir.y);
        float phase = dot(anchor.xz, windDir) * 0.09 - time * 1.3;
        float gust = 0.5 + 0.5 * sin(phase + 1.7 * sin(phase * 0.37 + anchor.x * 0.05));
        float flutter = sin(time * 6.5 + anchor.x * 1.7 + anchor.z * 1.3 + iAccentSeed.w * 40.0 + aPos.y * 3.0);
        float bend = windStrength * sway * sway * (0.2 + 0.8 * gust);
        vec3 side = normalize(mat3(model) * X);
        float h = scale * grow.y;
        vec3 disp = wd * bend * h * 0.35 + side * flutter * sway * 0.03 * h * windStrength;
        w.xyz += disp;
        // Keep the blade's length: what bends over comes down.
        w.xyz -= fUp * dot(disp, disp) * 0.5 / max(h * max(aPos.y, 0.05), 0.05);
    }
    fWorldPos = w.xyz;
    return true;
}
