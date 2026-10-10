#version 430

// nanite_material_vert.glsl — the material pass's tiles (render/
// nanite_vis.go): one 64x64 quad an instance, the instance's tile read from
// the bin's list, at the bin's depth. A bin draws only the tiles it has pixels
// in, and the depth test (GL_EQUAL against the bins' depth) only its pixels.

layout(std430, binding = 6) readonly buffer TileLists {
    uint tileList[];
};

uniform uint bin;
uniform uint tileCap;
uniform uint tilesX;
uniform vec2 viewSize;

const vec2 corners[6] = vec2[6](vec2(0, 0), vec2(1, 0), vec2(1, 1), vec2(0, 0), vec2(1, 1), vec2(0, 1));

void main() {
    uint tile = tileList[bin * tileCap + uint(gl_InstanceID)];
    vec2 corner = vec2(float(tile % tilesX), float(tile / tilesX)) * 64.0;
    vec2 p = min(corner + corners[gl_VertexID] * 64.0, viewSize);
    // At the bin's own depth (nanite_vis_bindepth_frag.glsl): (bin + 1) / 2^20,
    // which 2d - 1 and back carries exactly.
    float d = float(bin + 1u) / 1048576.0;
    gl_Position = vec4(p / viewSize * 2.0 - 1.0, 2.0 * d - 1.0, 1.0);
}
