#version 430

// nanite_debug_vert.glsl — the Nanite debug views' vertex stage
// (render/nanite_debug.go). Every command in a debug view is one cluster,
// drawn with its slot as its base instance; slot is that slot's (instance,
// cluster) from the batch's table, an instanced attribute. The matrix and
// the cluster's level come from the cull's own buffers.

layout(location = 0) in vec3 vert;
layout(location = 11) in uvec2 slot;

struct Instance {
    mat4 model;
    vec4 sphere;
    uvec4 flags;
    vec4 wind;
    vec4 draw; // draw distance window (render/nanite_draw_distance.go)
};
layout(std430, binding = 0) readonly buffer Instances {
    Instance inst[];
};

struct Cluster {
    vec4 sphere;
    vec4 apex;
    vec4 cone;
    uvec4 draw;
    vec4 lod;
    vec4 parent;
    vec4 errs;
};
layout(std430, binding = 2) readonly buffer Clusters {
    Cluster clusters[];
};

uniform mat4 projView;

flat out uint vInstance;
flat out uint vCluster;
flat out uint vLevel;
out vec3 vWorld;

void main() {
    mat4 M = inst[slot.x].model;
    vec4 w = M * vec4(vert, 1.0);
    // Bent as the colour pass bends it (wind_common.glsl, spliced in by the Go side).
    w.xyz += windOffset(vert, M, inst[slot.x].wind, inst[slot.x].sphere);
    vWorld = w.xyz;
    vInstance = slot.x;
    vCluster = slot.y;
    vLevel = uint(clusters[slot.y].errs.z);
    gl_Position = projView * w;
}
