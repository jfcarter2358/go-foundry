// nanite_instance_cull_comp.glsl — the GPU-driven cull's first stage
// (render/nanite_cull.go): one thread per instance, writing whether any of
// its clusters can be drawn. Follows nanite_cull_common.glsl.
//
// The test is the instance's bounding sphere against the frustum and — in
// pass 2, where there is a Hi-Z pyramid — against the depth drawn so far. A
// culled instance's clusters are all culled by the next stage without a test
// of their own, which is what makes a thousand placements behind a wall cost a
// thousand threads rather than a hundred thousand.

layout(local_size_x = 64) in;

uniform uint instanceCount;

void main() {
    uint i = gl_GlobalInvocationID.x;
    if (i >= instanceCount) {
        return;
    }
    if ((inst[i].flags.x & 1u) != 0u) {
        instVis[i] = 1u;
        return;
    }
    mat4 m = inst[i].model;
    vec4 s = inst[i].sphere;
    vec3 c = (m * vec4(s.xyz, 1.0)).xyz;
    float r = s.w * maxScale(m);
    bool vis = inFrustum(c, r);
    if (vis && mode == CULL_PASS2) {
        vis = !occluded(c, r);
    }
    instVis[i] = vis ? 1u : 0u;
}
