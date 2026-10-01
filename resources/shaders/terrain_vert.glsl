#version 430

// terrain_vert.glsl — a chunk's block terrain (render/terrain.go).
//
// The surface is uploaded once per section in chunk-local space and carried
// out by `model`, like a brush. Texture coordinates are not stored: layers
// are projected along the chunk's own three axes and blended by the normal in
// the fragment stage (triplanar), so a block's top and its sides tile evenly
// however the ground is shaped, and the texture stays put when the chunk
// moves.

layout(location = 0) in vec3 aPos;
layout(location = 1) in vec3 aNormal;
// One weight per layer, 0..1, summing to 1 (normalised unsigned bytes).
layout(location = 2) in vec4 aWeights;
// The painted biome's ground tint (rgb) and how much of it (a).
layout(location = 3) in vec4 aCover;
// How much of the ground round this corner is painted with each of the
// terrain's biome ground textures (render/terrain_ground.go), channels 0-3
// and 4-7.
layout(location = 4) in vec4 aGround0;
layout(location = 5) in vec4 aGround1;

uniform mat4 projView;
uniform mat4 model;

out vec3 vWorldPos;
out vec3 vNormal;
out vec3 vLocalPos;
out vec3 vLocalNormal;
out vec4 vWeights;
out vec4 vCover;
out vec4 vGround0;
out vec4 vGround1;
// Distance along the view axis, for cascade selection — computed exactly as
// vertex.glsl's fragViewDepth, so the ground picks the same cascade as a
// brush floor beside it and their shadow edges meet.
out float vViewDepth;

void main() {
    vec4 worldPos = model * vec4(aPos, 1.0);
    vWorldPos = worldPos.xyz;
    vNormal = transpose(inverse(mat3(model))) * aNormal;
    vLocalPos = aPos;
    vLocalNormal = aNormal;
    vWeights = aWeights;
    vCover = aCover;
    vGround0 = aGround0;
    vGround1 = aGround1;
    vViewDepth = biomeViewDepth(vWorldPos);
    gl_Position = projView * worldPos;
}
