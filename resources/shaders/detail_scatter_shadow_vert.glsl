#version 410

// detail_scatter_shadow_vert.glsl — directional-shadow vertex stage for
// scattered rock instances (same placement as the color pass). Paired with
// shadow_frag.glsl so scattered rocks cast accurate sun shadows.

layout(location = 0) in vec3 aLocal;
layout(location = 1) in vec3 aNormal;
layout(location = 2) in float aParam;

uniform mat4 lightSpaceMatrix;
uniform mat4 model;

void main() {
    vec3 w;
    if (!scatterPlace(gl_InstanceID, aLocal, w)) {
        gl_Position = vec4(2.0, 2.0, 2.0, 1.0);
        return;
    }
    gl_Position = lightSpaceMatrix * model * vec4(w, 1.0);
}
