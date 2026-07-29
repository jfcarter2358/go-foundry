#version 410

// detail_scatter_cube_vert.glsl — point-light (cubemap) shadow vertex stage for
// scattered rock instances. Outputs displaced world position for
// shadow_cube_frag.glsl's linear-distance depth.

layout(location = 0) in vec3 aLocal;
layout(location = 1) in vec3 aNormal;
layout(location = 2) in float aParam;

uniform mat4 lightSpaceMatrix;

out vec3 fragWorldPos;

void main() {
    vec3 w;
    if (!scatterPlace(gl_InstanceID, aLocal, w)) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        fragWorldPos = vec3(0.0);
        return;
    }
    fragWorldPos = w;
    gl_Position = lightSpaceMatrix * vec4(w, 1.0);
}
