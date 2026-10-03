#version 430

// taa_object_vert.glsl — a moving object's motion vectors (render/taa.go).
// skin_common.glsl is injected above this line, so a skinned attachment is
// posed as the colour pass posed it.
//
// The position is computed exactly as vertex.glsl computes it — the same
// expressions, the same uniforms, gl_Position invariant — so the object's
// own depth passes the LEQUAL test against what the colour pass wrote and
// nothing in front of it is overwritten. Its motion is measured through the
// unjittered projections, from its matrix last frame to its matrix now.

uniform mat4 projection;   // jittered, as the colour pass drew
uniform mat4 camera;
uniform mat4 model;
uniform mat4 prevModel;
uniform mat4 viewProj;     // unjittered
uniform mat4 prevViewProj; // unjittered

layout(location = 0) in vec3 vert;

invariant gl_Position;

out vec4 vNow;
out vec4 vThen;

void main() {
    vec4 local = skinPosition(vert);
    vec4 worldPos = model * local;
    vec4 viewPos = camera * worldPos;
    gl_Position = projection * viewPos;
    vNow = viewProj * worldPos;
    vThen = prevViewProj * (prevModel * local);
}
