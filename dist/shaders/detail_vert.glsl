#version 410

// detail_vert.glsl — passthrough vertex stage for the detail tessellation
// programs. World-space geometry (model is identity), so `vert` IS the world
// position; the TCS/TES do the real work.

layout(location = 0) in vec3 vert;
layout(location = 1) in vec2 vertTexCoord;

out vec3 tcWorld;
out vec2 tcUV;

void main() {
    tcWorld = vert;
    tcUV = vertTexCoord;
}
