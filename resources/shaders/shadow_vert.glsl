#version 430

uniform mat4 lightSpaceMatrix;
uniform mat4 model;

// Instanced drawing (render/scenery_instanced.go): many placements of one mesh
// in one draw, each carrying its model matrix as a per-instance attribute in
// locations 7-10. Off — the zero an unwritten uniform holds — every draw reads
// `model` as it always has, and the attribute is never looked at.
uniform int instanced;
layout(location = 7) in mat4 instanceModel;
// A cooked placement's wind (wind_common.glsl, render/nanite_scenery.go):
// its mesh's bounding sphere and its wind, beside its matrix in the instance
// record. Never enabled on any other draw, so they read (0,0,0,1) there,
// which is no wind.
layout(location = 13) in vec4 instanceSphere;
layout(location = 14) in vec4 instanceWind;

layout(location = 0) in vec3 vert;

void main() {
    // Skinned here too, or an animated entity would cast the silhouette of its
    // rest pose while the mesh moved — see skin_common.glsl.
    mat4 M = instanced != 0 ? instanceModel : model;
    vec4 local = skinPosition(vert);
    vec4 worldPos = M * local;
    // Bent as the colour pass bends it (vertex.glsl).
    if (instanced != 0) {
        worldPos.xyz += windOffset(local.xyz, M, instanceWind, instanceSphere);
    }
    gl_Position = lightSpaceMatrix * worldPos;
}
