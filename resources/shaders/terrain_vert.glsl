#version 410

// terrain_vert.glsl — a chunk's heightfield ground (render/terrain.go).
//
// The grid is uploaded once in chunk-local space and carried out by `model`,
// like a brush. Texture coordinates are not stored: every layer is projected
// straight down onto the chunk's own XZ plane, so the ground tiles evenly
// however it is sculpted and the texture stays put when the chunk moves.

layout(location = 0) in vec3 aPos;
layout(location = 1) in vec3 aNormal;
// One weight per layer, 0..1, summing to 1 (normalised unsigned bytes).
layout(location = 2) in vec4 aWeights;

uniform mat4 projView;
uniform mat4 model;
// Chunk units per repeat of a layer texture (Terrain.TextureScale).
uniform float textureScale;

out vec3 vWorldPos;
out vec3 vNormal;
out vec4 vWeights;
out vec2 vUV;
// Distance along the view axis, for cascade selection — computed exactly as
// vertex.glsl's fragViewDepth, so the ground picks the same cascade as a
// brush floor beside it and their shadow edges meet.
out float vViewDepth;

void main() {
    vec4 worldPos = model * vec4(aPos, 1.0);
    vWorldPos = worldPos.xyz;
    vNormal = transpose(inverse(mat3(model))) * aNormal;
    vWeights = aWeights;
    vUV = aPos.xz / textureScale;
    vViewDepth = biomeViewDepth(vWorldPos);
    gl_Position = projView * worldPos;
}
