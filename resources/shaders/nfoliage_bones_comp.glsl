// nfoliage_bones_comp.glsl — the Nanite foliage's bones, posed for this
// frame's wind (render/nfoliage.go; the bones themselves are
// render/foliage_assembly.go's). The #version line and the NF_* defines are
// the Go side's.
//
// A thread per (variant, slot, direction). A SLOT is one of NF_GUSTS gust
// levels times NF_FLUTTERS flutter phases: every instance of a variant
// blends the two gust levels its own gust falls between, at its own flutter
// phase (nfoliage_common.glsl), so a meadow's waves still travel and no two
// neighbours shiver in step. A DIRECTION is the wind along the plant's own
// +X or +Z; an instance mixes the two by how its frame faces the world's
// wind — exact to first order in the bend, which is small.
//
// Bones are walked in order, parents first (the assembly guarantees it):
//
//	B_k = mix(B_parent, M_parent, at_k)               the base pose
//	M_k = B_k · T(pivot) · R(axis, θ_k) · T(−pivot)    its own
//	θ_k = flex_k · strength · (0.2 + 0.8·gust) + flutter_k · strength · sin(6.5·time + phase)
//
// — render.poseFoliageBones on the CPU, which this must match. Each matrix
// is written as its three rows (a 3x4 affine), base then own, direction 0
// then 1, slot by slot, bone by bone: nfPoseIndex below.

layout(local_size_x = 64) in;

// pivot.xyz, parent; tip.xyz, at; flex, flutter, phase, 0.
layout(std430, binding = 9) readonly buffer Bones {
    vec4 bones[];
};
// Per variant, two each: (first bone, bones, first cluster, clusters) and
// its bounding sphere's bits (nfoliage_cull_comp.glsl).
layout(std430, binding = 5) readonly buffer Buckets {
    uvec4 buckets[];
};
// Written, and read back for each bone's parent — the thread's own earlier
// writes, which it always sees. (A local array of every bone's matrices,
// indexed by parent, took Mesa's D3D12 compiler minutes and ran 40 ms a
// frame there.)
layout(std430, binding = 10) buffer Poses {
    vec4 poses[];
};

uniform int bucketCount;
uniform float time;
uniform float windStrength;

int nfPoseIndex(int bone, int slot, int dir, int which) {
    return (((bone * NF_SLOTS + slot) * 2 + dir) * 2 + which) * 3;
}

// An affine as its three rows, times a rotation about axis through pivot.
void rotateRows(inout vec4 r0, inout vec4 r1, inout vec4 r2, vec3 pivot, vec3 axis, float ang) {
    float c = cos(ang), s = sin(ang), t = 1.0 - c;
    vec3 a = axis;
    // The rotation's rows.
    vec3 q0 = vec3(t * a.x * a.x + c, t * a.x * a.y - s * a.z, t * a.x * a.z + s * a.y);
    vec3 q1 = vec3(t * a.x * a.y + s * a.z, t * a.y * a.y + c, t * a.y * a.z - s * a.x);
    vec3 q2 = vec3(t * a.x * a.z - s * a.y, t * a.y * a.z + s * a.x, t * a.z * a.z + c);
    vec3 tr = pivot - vec3(dot(q0, pivot), dot(q1, pivot), dot(q2, pivot));
    mat3 Q = transpose(mat3(q0, q1, q2)); // columns q0..q2 transposed: rows
    vec4 o0 = vec4(r0.xyz * Q, dot(r0.xyz, tr) + r0.w);
    vec4 o1 = vec4(r1.xyz * Q, dot(r1.xyz, tr) + r1.w);
    vec4 o2 = vec4(r2.xyz * Q, dot(r2.xyz, tr) + r2.w);
    r0 = o0;
    r1 = o1;
    r2 = o2;
}

void main() {
    int id = int(gl_GlobalInvocationID.x);
    int perBucket = NF_SLOTS * 2;
    if (id >= bucketCount * perBucket) {
        return;
    }
    int bucket = id / perBucket;
    int slot = (id % perBucket) / 2;
    int dir = id % 2;
    int gustLevel = slot / NF_FLUTTERS;
    int flutterPhase = slot % NF_FLUTTERS;
    float gust = float(gustLevel) / float(NF_GUSTS - 1);
    float amount = windStrength * (0.2 + 0.8 * gust);
    vec3 axis = dir == 0 ? vec3(0.0, 0.0, -1.0) : vec3(1.0, 0.0, 0.0);

    uvec4 bk = buckets[bucket * 2];
    int first = int(bk.x);
    int count = int(bk.y);
    for (int i = 0; i < count; i++) {
        vec4 pv = bones[(first + i) * 3];
        vec4 tp = bones[(first + i) * 3 + 1];
        vec4 pr = bones[(first + i) * 3 + 2];
        int parent = int(pv.w);
        vec4 b0 = vec4(1.0, 0.0, 0.0, 0.0), b1 = vec4(0.0, 1.0, 0.0, 0.0), b2 = vec4(0.0, 0.0, 1.0, 0.0);
        vec4 m0 = b0, m1 = b1, m2 = b2;
        if (parent >= 0) {
            int pb = nfPoseIndex(first + parent, slot, dir, 0);
            int pm = nfPoseIndex(first + parent, slot, dir, 1);
            b0 = mix(poses[pb], poses[pm], tp.w);
            b1 = mix(poses[pb + 1], poses[pm + 1], tp.w);
            b2 = mix(poses[pb + 2], poses[pm + 2], tp.w);
            float flutter = windStrength * sin(time * 6.5 + float(flutterPhase) * 1.5707963 + pr.z);
            float ang = pr.x * amount + pr.y * flutter;
            m0 = b0;
            m1 = b1;
            m2 = b2;
            rotateRows(m0, m1, m2, pv.xyz, axis, ang);
        }
        int ob = nfPoseIndex(first + i, slot, dir, 0);
        int om = nfPoseIndex(first + i, slot, dir, 1);
        poses[ob] = b0;
        poses[ob + 1] = b1;
        poses[ob + 2] = b2;
        poses[om] = m0;
        poses[om + 1] = m1;
        poses[om + 2] = m2;
    }
}
