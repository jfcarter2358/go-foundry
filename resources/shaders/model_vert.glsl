#version 410

// model_vert.glsl — the model editor's own vertex shader.
//
// The world shader (vertex.glsl) carries lights, cascades, cubemaps and water;
// feeding all of that from the model editor's separate GL context, for a mesh
// that is not part of any level, would mean maintaining a second copy of the
// whole lighting upload path. This program does the one thing the editor needs:
// put the mesh on screen under a fixed headlight.
//
// Attribute locations are pinned to match vertex.glsl so that a VAO built by
// Geometry.Load (which resolves its attribute indices against the world
// program) is equally valid here.

uniform mat4 projection;
uniform mat4 view;
uniform mat4 model;

// 1 = shade from vertNormal, 0 = let the fragment stage recover a facet normal
// from screen-space derivatives. Same contract as the world shader.
uniform int smoothShading;

layout(location = 0) in vec3 vert;
layout(location = 1) in vec2 vertTexCoord;
layout(location = 2) in vec3 vertNormal;

out vec2 fragTexCoord;
out vec3 fragNormal;
out vec3 fragWorldPos;

void main() {
    vec4 world = model * vec4(vert, 1.0);
    fragWorldPos = world.xyz;
    fragTexCoord = vertTexCoord;

    fragNormal = vec3(0.0);
    if (smoothShading != 0) {
        fragNormal = transpose(inverse(mat3(model))) * vertNormal;
    }

    gl_Position = projection * view * world;
}
