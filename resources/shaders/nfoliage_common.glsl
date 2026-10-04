// nfoliage_common.glsl — placing a Nanite foliage vertex
// (render/nfoliage.go). Spliced into every vertex stage of the Nanite
// foliage — triangles and parts, voxel boxes, their shadow casters — after
// foliage_params.glsl, so all of them place, thin and bend a plant alike.
//
// Each instance of a draw is a REFERENCE (item, which: the vertex stages'
// instanced attribute 4): the item is one
// plant's one cluster (nfoliage_cull_comp.glsl), `which` the part of a part
// cluster or the brick of a voxel cluster. The plant itself is read from
// the shared instance pool, as foliage_common.glsl reads it, and so are its
// frame, its thinning fade and the growth that covers for thinned
// neighbours.
//
// # Bending
//
// The classic path moves each vertex along the wind by its sway weight.
// Here a vertex rides a BONE (render/foliage_assembly.go): its position in
// the plant's frame goes through the bone's base and own poses, mixed by its
// place along the bone. The poses (nfoliage_bones_comp.glsl) are for the
// variant at NF_GUSTS gust levels and NF_FLUTTERS flutter phases, with the
// wind along the plant's +X or +Z. An instance takes its own gust — the
// classic path's travelling wave, from where it stands — between two levels,
// its flutter phase from its seed, and mixes the two directions by how its
// frame faces the world's wind. Last frame's poses and time give where the
// vertex WAS, for the motion vectors TAA reprojects with (render/taa.go).

uniform samplerBuffer instances;
// Items: pool index, terrain, cluster.
uniform usamplerBuffer nfItems;
// Per cluster two texels: (kind, bucket, first part or brick, first bone)
// and (voxel size bits, references, 0, 0).
uniform usamplerBuffer nfClusters;
// Per part four texels: right·w0, up·w1, normal·tone, base·bone.
uniform samplerBuffer nfParts;
// Per brick two texels: corner·size bits, (mask lo, mask hi, first voxel,
// bone | weight << 16).
uniform usamplerBuffer nfBricks;
// This frame's poses and last frame's (nfPoseIndex).
uniform samplerBuffer nfPose;
uniform samplerBuffer nfPosePrev;
// Per terrain four texels: its model matrix's columns.
uniform samplerBuffer nfTerrains;

uniform vec3 camPos;
uniform float time;
uniform float prevTime;
uniform vec2 windDir;
uniform float windStrength;

int nfPoseIndex(int bone, int slot, int dir, int which) {
    return (((bone * NF_SLOTS + slot) * 2 + dir) * 2 + which) * 3;
}

// The reference's plant.
uint nItem;
uint nWhich;
int nKind;
int nBucket;
int nShape;
int nFirst;      // the cluster's first part or brick
int nBoneBase;   // the variant's first bone
float nVoxelSize;
mat4 nModel;
vec4 iPosScale;
vec4 iUpYaw;
vec4 iColorRank;
vec4 iAccentSeed;
vec3 nX, nUp, nZ;
vec3 nAnchor;    // world
float nDist;
float nFade;
vec3 nScale;     // scale times growth, per plant axis
// The wind in the plant's frame: how much of it blows along +X and +Z.
vec2 nWind;
// This frame's and last frame's gust slots and the mix between them.
ivec2 nSlot;
float nSlotMix;
ivec2 nPrevSlot;
float nPrevSlotMix;

float nfGust(float t) {
    float phase = dot(nAnchor.xz, windDir) * 0.09 - t * 1.3;
    return 0.5 + 0.5 * sin(phase + 1.7 * sin(phase * 0.37 + nAnchor.x * 0.05));
}

void nfSlots(float g, out ivec2 slot, out float mixv) {
    float gs = clamp(g, 0.0, 1.0) * float(NF_GUSTS - 1);
    int g0 = min(int(floor(gs)), NF_GUSTS - 2);
    int f = min(int(iAccentSeed.w * float(NF_FLUTTERS)), NF_FLUTTERS - 1);
    slot = ivec2(g0 * NF_FLUTTERS + f, (g0 + 1) * NF_FLUTTERS + f);
    mixv = gs - float(g0);
}

// nfBeginRef reads reference ref's item and plant and decides whether it is
// drawn: false when the thinning has taken it at its distance. (The vertex
// stages call it with their instanced reference; the compute rasteriser and
// its resolve with one they read.)
bool nfBeginRef(uvec2 reference) {
    uvec4 it = texelFetch(nfItems, int(reference.x));
    nItem = reference.x;
    nWhich = reference.y;
    int cl = int(it.z);
    uvec4 c0 = texelFetch(nfClusters, cl * 2);
    uvec4 c1 = texelFetch(nfClusters, cl * 2 + 1);
    nKind = int(c0.x);
    nBucket = int(c0.y);
    nFirst = int(c0.z);
    nBoneBase = int(c0.w);
    nVoxelSize = uintBitsToFloat(c1.x);
    nShape = bucketInfo[nBucket].x;
    int t = int(it.y) * 4;
    nModel = mat4(texelFetch(nfTerrains, t), texelFetch(nfTerrains, t + 1), texelFetch(nfTerrains, t + 2), texelFetch(nfTerrains, t + 3));
    int b = int(it.x) * 4;
    iPosScale = texelFetch(instances, b);
    iUpYaw = texelFetch(instances, b + 1);
    iColorRank = texelFetch(instances, b + 2);
    iAccentSeed = texelFetch(instances, b + 3);

    vec3 up = normalize(iUpYaw.xyz);
    vec3 ref = abs(up.y) < 0.95 ? vec3(0.0, 1.0, 0.0) : vec3(1.0, 0.0, 0.0);
    vec3 t0 = normalize(cross(ref, up));
    vec3 b0 = cross(up, t0);
    float c = cos(iUpYaw.w), s = sin(iUpYaw.w);
    nX = t0 * c + b0 * s;
    nZ = cross(nX, up);
    nUp = up;
    nAnchor = (nModel * vec4(iPosScale.xyz, 1.0)).xyz;
    nDist = length(nAnchor - camPos);
    vec4 R = shapeRange[nShape];
    nFade = foliageRankFade(foliageKeep(R, nDist), iColorRank.w);
    if (nFade <= 0.0) {
        return false;
    }
    float comp = foliageComp(R, nDist);
    float scale = iPosScale.w;
    if (shapeMore[nShape].y > 0.5) {
        scale *= mix(0.25, 1.0, nFade);
    }
    nScale = vec3(comp, pow(comp, shapeMore[nShape].x), comp) * scale;

    vec3 wd = vec3(windDir.x, 0.0, windDir.y);
    mat3 m3 = mat3(nModel);
    nWind = vec2(dot(wd, normalize(m3 * nX)), dot(wd, normalize(m3 * nZ)));
    nfSlots(nfGust(time), nSlot, nSlotMix);
    nfSlots(nfGust(prevTime), nPrevSlot, nPrevSlotMix);
    return true;
}

// nfPoseRows is a pose's three rows.
void nfPoseRows(bool prev, int bone, int slot, int dir, int which, out vec4 r0, out vec4 r1, out vec4 r2) {
    int i = nfPoseIndex(nBoneBase + bone, slot, dir, which);
    if (prev) {
        r0 = texelFetch(nfPosePrev, i);
        r1 = texelFetch(nfPosePrev, i + 1);
        r2 = texelFetch(nfPosePrev, i + 2);
    } else {
        r0 = texelFetch(nfPose, i);
        r1 = texelFetch(nfPose, i + 1);
        r2 = texelFetch(nfPose, i + 2);
    }
}

// nfBoneMatrix is the affine (3 rows) a point at place w along bone takes in
// one direction of wind, mixed between the two gust slots.
void nfBoneMatrix(bool prev, int bone, float w, int dir, out vec4 r0, out vec4 r1, out vec4 r2) {
    ivec2 slot = prev ? nPrevSlot : nSlot;
    float m = prev ? nPrevSlotMix : nSlotMix;
    vec4 a0, a1, a2, b0, b1, b2;
    r0 = vec4(0.0);
    r1 = vec4(0.0);
    r2 = vec4(0.0);
    for (int k = 0; k < 2; k++) {
        float gw = k == 0 ? 1.0 - m : m;
        nfPoseRows(prev, bone, slot[k], dir, 0, a0, a1, a2);
        nfPoseRows(prev, bone, slot[k], dir, 1, b0, b1, b2);
        r0 += gw * mix(a0, b0, w);
        r1 += gw * mix(a1, b1, w);
        r2 += gw * mix(a2, b2, w);
    }
}

// nfSkinMatrix is the whole bend of a point at place w along bone, as one
// affine: the identity plus each direction's bend, weighted by the wind
// along it.
void nfSkinMatrix(bool prev, int bone, float w, out vec4 r0, out vec4 r1, out vec4 r2) {
    vec4 x0, x1, x2, z0, z1, z2;
    nfBoneMatrix(prev, bone, w, 0, x0, x1, x2);
    nfBoneMatrix(prev, bone, w, 1, z0, z1, z2);
    vec4 i0 = vec4(1.0, 0.0, 0.0, 0.0), i1 = vec4(0.0, 1.0, 0.0, 0.0), i2 = vec4(0.0, 0.0, 1.0, 0.0);
    r0 = i0 + nWind.x * (x0 - i0) + nWind.y * (z0 - i0);
    r1 = i1 + nWind.x * (x1 - i1) + nWind.y * (z1 - i1);
    r2 = i2 + nWind.x * (x2 - i2) + nWind.y * (z2 - i2);
}

vec3 nfApply(vec4 r0, vec4 r1, vec4 r2, vec3 p) {
    vec4 h = vec4(p, 1.0);
    return vec3(dot(r0, h), dot(r1, h), dot(r2, h));
}

vec3 nfApplyDir(vec4 r0, vec4 r1, vec4 r2, vec3 n) {
    return vec3(dot(r0.xyz, n), dot(r1.xyz, n), dot(r2.xyz, n));
}

// nfToWorld carries a point of the plant's frame (unit scale) to the world.
vec3 nfToWorld(vec3 q) {
    vec3 lp = q * nScale;
    return (nModel * vec4(iPosScale.xyz + nX * lp.x + nUp * lp.y + nZ * lp.z, 1.0)).xyz;
}

// nfDirToWorld carries a direction (a normal) likewise, unnormalised.
vec3 nfDirToWorld(vec3 n) {
    vec3 ln = n / nScale;
    return mat3(nModel) * (nX * ln.x + nUp * ln.y + nZ * ln.z);
}

// nfBrickMap is brick bi's map from its voxel units (0..4 on each axis) to
// the world, this frame or last: the world position of its corner, and one
// voxel's step along each of its axes as A's columns. The brick rides its
// bone rigidly, at its binding's place along it.
void nfBrickMap(bool prev, int bi, out vec3 o, out mat3 A) {
    uvec4 b0 = texelFetch(nfBricks, bi * 2);
    uvec4 b1 = texelFetch(nfBricks, bi * 2 + 1);
    vec3 corner = uintBitsToFloat(b0.xyz);
    float size = uintBitsToFloat(b0.w);
    int bone = int(b1.w & 0xffffu);
    float w = float(b1.w >> 16u) / 65535.0;
    vec4 r0, r1, r2;
    nfSkinMatrix(prev, bone, w, r0, r1, r2);
    o = nfToWorld(nfApply(r0, r1, r2, corner));
    A = mat3(nfToWorld(nfApply(r0, r1, r2, corner + vec3(size, 0.0, 0.0))) - o,
             nfToWorld(nfApply(r0, r1, r2, corner + vec3(0.0, size, 0.0))) - o,
             nfToWorld(nfApply(r0, r1, r2, corner + vec3(0.0, 0.0, size))) - o);
}
