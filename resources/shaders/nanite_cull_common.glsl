// nanite_cull_common.glsl — what the two stages of the GPU-driven cull share
// (render/nanite_cull.go). The #version line is the Go side's.
//
// A cull is run over a BATCH: a list of instances (a placement's matrix and
// the sphere its mesh fits in) and a list of tiles, each a run of one
// instance's clusters with the slots of the indirect-command list they write.
// The instance stage (nanite_instance_cull_comp.glsl) decides per instance;
// the cluster stage (nanite_cluster_cull_comp.glsl) per cluster, and writes
// one DrawElementsIndirectCommand per slot whatever it decides — a culled
// cluster's is a zero count — so the list never needs clearing.
//
// The occlusion is two-pass (mode): pass 1 draws only what was visible last
// frame and still passes the frustum; the Hi-Z pyramid is then built from the
// depth so far; pass 2 tests everything against it, draws what pass 1 missed,
// and records this frame's verdict for the next.

// What a pass culls for.
#define CULL_PLAIN 0  // frustum (and cone): shadows, and the view with occlusion off
#define CULL_PASS1 1  // frustum, cone, and visible last frame
#define CULL_PASS2 2  // frustum, cone, Hi-Z; draws what pass 1 did not

// What the normal-cone test applies to (useCone).
#define CONE_NONE   0
#define CONE_ALL    1
#define CONE_CLOSED 2

uniform int mode;
uniform vec4 planes[6];
uniform mat4 projView;
// The eye the normal cone is tested from: xyz its position (w 1), or for an
// orthographic view — a sun cascade — the direction every view ray runs
// (w 0), which is the same test with one direction for every cluster.
uniform vec4 eye;
// Which clusters the cone may drop: CONE_NONE, CONE_ALL (a pass that culls
// back faces: the camera's), CONE_CLOSED (a shadow pass, which draws back
// faces: only on instances of closed meshes, flags.x bit 2 — see
// internal/nanite/closed.go for why that is exact).
uniform int useCone;
uniform sampler2D hiz;
uniform ivec2 hizSize;
uniform int hizLevels;
uniform int useHiz;
uniform uint instanceCount;

// Level of detail (render/nanite_lod.go). lodMode 0: level 0 only, the full
// detail; 1: the cut the camera chooses. lodEye is the camera, whatever view
// the pass draws; lodK folds the frame's height, field of view and the error
// threshold in pixels into one factor (an error e at distance d is fine when
// e·lodK <= d). passStamp marks the leaves the tree walk reached this pass.
uniform int lodMode;
// lodWalk: the tree walk ran this pass, so the leaf marks are current.
uniform int lodWalk;
uniform vec3 lodEye;
uniform float lodK;
uniform uint passStamp;

// The nearest the eye is taken to be to an LOD sphere (cluster.LODNearClamp).
#define LOD_NEAR 1e-3

// RANGE_SLOTS, a range tile's command slots, is defined by the Go side
// (render/nanite_cull.go, rangeSlots).

// flags.x: bit 0 — always visible (a batch's identity instance: a compiled
// level's surfaces, which are in world space and culled cluster by cluster);
// bit 1 — no cone test (a mirroring or unevenly scaled placement, whose back
// faces are not where the cone says); bit 2 — its mesh is closed, so a shadow
// pass may cone-test it too (CONE_CLOSED); bit 3 — its mesh has a cluster
// DAG: flags.y is its LOD tree's root node, flags.z its first leaf mark.
// wind: how it bends in the wind (wind_common.glsl, spliced in above this
// file); zero for none.
struct Instance {
    mat4 model;
    vec4 sphere; // local centre, radius
    uvec4 flags;
    vec4 wind;
    // draw: the distance window it is drawn over (render/nanite_draw_distance.go):
    // x from, y to (0: open), z its hash, w unused.
    vec4 draw;
};

layout(std430, binding = 0) readonly buffer Instances {
    Instance inst[];
};
layout(std430, binding = 1) buffer InstanceVisible {
    uint instVis[];
};

// Leaf marks: per instance with a DAG, per leaf of its tree, the stamp of the
// last pass whose tree walk reached it (nanite_lod_traverse_comp.glsl).
layout(std430, binding = 12) buffer LeafMarks {
    uint leafVis[];
};

// An error of e (world units) over the sphere (c, r) is fine enough to draw
// from the camera: cluster.LODWithin.
bool lodWithin(vec3 c, float r, float e) {
    return e * lodK <= max(distance(c, lodEye) - r, LOD_NEAR);
}

bool inFrustum(vec3 c, float r) {
    for (int i = 0; i < 6; i++) {
        vec4 p = planes[i];
        if (dot(p.xyz, c) + p.w < -r * length(p.xyz)) {
            return false;
        }
    }
    return true;
}

// instancePad is how far instance i's wind can carry any of its vertices
// (windReach): what its sphere, its tree's and its clusters' are grown by.
float instancePad(uint i) {
    return windReach(inst[i].model, inst[i].wind);
}

// clusterPad is the pad of the instance whose clusters this invocation is
// testing, set by the cull stages before they call clusterDraws.
float clusterPad = 0.0;

float maxScale(mat4 m) {
    return sqrt(max(dot(m[0].xyz, m[0].xyz), max(dot(m[1].xyz, m[1].xyz), dot(m[2].xyz, m[2].xyz))));
}

// Hidden behind the depth already drawn: every texel of the pyramid under the
// sphere's screen rectangle is nearer than the sphere's nearest point. The
// same test the foliage cull makes (foliage_cull_comp.glsl).
bool occluded(vec3 c, float r) {
    if (useHiz == 0) {
        return false;
    }
    vec2 lo = vec2(1.0);
    vec2 hi = vec2(0.0);
    float zNear = 1.0;
    for (int i = 0; i < 8; i++) {
        vec3 corner = c + r * vec3((i & 1) != 0 ? 1.0 : -1.0, (i & 2) != 0 ? 1.0 : -1.0, (i & 4) != 0 ? 1.0 : -1.0);
        vec4 clip = projView * vec4(corner, 1.0);
        if (clip.w <= 1e-3) {
            return false;
        }
        vec3 ndc = clip.xyz / clip.w;
        vec2 uv = ndc.xy * 0.5 + 0.5;
        lo = min(lo, uv);
        hi = max(hi, uv);
        zNear = min(zNear, ndc.z * 0.5 + 0.5);
    }
    lo = clamp(lo, 0.0, 1.0);
    hi = clamp(hi, 0.0, 1.0);
    if (any(greaterThanEqual(lo, hi))) {
        return false;
    }
    vec2 span = (hi - lo) * vec2(hizSize);
    int level = clamp(int(ceil(log2(max(max(span.x, span.y), 1.0)))), 0, hizLevels - 1);
    ivec2 size = max(hizSize >> level, ivec2(1));
    ivec2 a = clamp(ivec2(lo * vec2(size)), ivec2(0), size - 1);
    ivec2 b = clamp(ivec2(hi * vec2(size)), ivec2(0), size - 1);
    float far = max(max(texelFetch(hiz, a, level).r, texelFetch(hiz, ivec2(b.x, a.y), level).r),
                    max(texelFetch(hiz, ivec2(a.x, b.y), level).r, texelFetch(hiz, b, level).r));
    return zNear > far;
}
