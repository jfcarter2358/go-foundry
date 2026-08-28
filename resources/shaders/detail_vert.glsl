#version 410

// detail_vert.glsl — passthrough vertex stage for the detail tessellation
// programs. `vert` is chunk-local, same as the geometry it's displacing; the
// TCS/TES do the real work, and the TES applies the owning chunk's `model`
// once to the final displaced position (see detail.tese).

layout(location = 0) in vec3 vert;
layout(location = 1) in vec2 vertTexCoord;

out vec3 tcWorld;
out vec2 tcUV;

void main() {
    tcWorld = vert;
    tcUV = vertTexCoord;
}
