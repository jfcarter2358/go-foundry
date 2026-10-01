#version 430

uniform vec3  lightPos;
uniform float farPlane;

in vec3 fragWorldPos;

out vec4 fragColor;

void main() {
    // Store linear distance from light (not clip-space depth) for omnidirectional shadows.
    float dist = length(fragWorldPos - lightPos);
    gl_FragDepth = dist / farPlane;
    fragColor = vec4(1.0);
}
