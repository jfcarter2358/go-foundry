#version 430

// sprite_vert.glsl — colour-pass vertex stage for the instanced sprite pass.
//
// Placement is entirely in sprite_common.glsl, shared verbatim with the two
// depth stages. All this stage adds is the varyings the fragment stage needs.

out vec2 fragTexCoord;
out vec3 fragWorldPos;
out vec3 fragNormal;
out vec4 fragColor;

void main() {
    vec3 normal;
    vec3 worldPos = spriteWorldPos(normal);

    fragWorldPos = worldPos;
    fragNormal   = normal;
    fragTexCoord = spriteTexCoord();
    fragColor    = iColor;

    gl_Position = projView * vec4(worldPos, 1.0);
}
