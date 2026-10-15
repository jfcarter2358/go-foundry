// nanite_instance_cull_comp.glsl — the GPU-driven cull's first stage
// (render/nanite_cull.go): one thread per instance, writing whether any of
// its clusters can be drawn. Follows nanite_cull_common.glsl.
//
// The test is the instance's bounding sphere against the frustum and — in
// pass 2, where there is a Hi-Z pyramid — against the depth drawn so far. A
// culled instance's clusters are all culled by the next stage without a test
// of their own, which is what makes a thousand placements behind a wall cost a
// thousand threads rather than a hundred thousand.

//
// And its draw-distance window (render/nanite_draw_distance.go;
// world/scenery_draw.go), measured from drawEye to the placement's origin —
// its matrix's translation — never its sphere, so a mesh and its impostor
// agree on the distance to the bit:
//
//   DRAW_OFF     every distance (a batch with no windows, a debug view)
//   DRAW_DITHER  a camera pass whose pixels the ID pass dithers: the whole
//                window, both fade bands included
//   DRAW_HASHED  a camera pass drawn the forward way: the per-instance cut,
//                each instance at its own distance in the band
//   DRAW_SHADOW  a shadow pass: what the CPU decided (instShadowHidden), the
//                same answer the virtual shadow map's tracker hashes

layout(local_size_x = 64) in;

#define DRAW_OFF 0
#define DRAW_DITHER 1
#define DRAW_HASHED 2
#define DRAW_SHADOW 3
uniform int drawMode;
uniform vec3 drawEye;

bool drawKeeps(uint i) {
    vec4 dr = inst[i].draw;
    if (drawMode == DRAW_SHADOW) {
        return (inst[i].flags.x & 16u) == 0u;
    }
    if (drawMode == DRAW_OFF || (dr.x <= 0.0 && dr.y <= 0.0)) {
        return true;
    }
    float d = distance(drawEye, inst[i].model[3].xyz);
    // The fade band: the window's last tenth (world.DrawFadeBand).
    float k = drawMode == DRAW_HASHED ? 0.9 + 0.1 * dr.z : 1.0;
    float kFrom = drawMode == DRAW_HASHED ? k : 0.9;
    if (dr.x > 0.0 && d < dr.x * kFrom) {
        return false;
    }
    return dr.y <= 0.0 || d < dr.y * k;
}

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
    float r = s.w * maxScale(m) + instancePad(i);
    bool vis = inFrustum(c, r) && drawKeeps(i);
    if (vis && mode == CULL_PASS2) {
        vis = !occluded(c, r);
    }
    instVis[i] = vis ? 1u : 0u;
}
