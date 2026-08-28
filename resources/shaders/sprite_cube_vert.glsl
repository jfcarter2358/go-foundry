#version 410

// sprite_cube_vert.glsl — point-light cubemap depth stage for sprites.
//
// Pairs with shadow_cube_frag.glsl, which writes linear distance, so the world
// position has to reach it as a varying. `viewPos` is the light's position
// here, which is also what the quad turns to face.

out vec3 fragWorldPos;

void main() {
    vec3 normal;
    vec3 worldPos = spriteWorldPos(normal);
    fragWorldPos  = worldPos;
    gl_Position   = projView * vec4(worldPos, 1.0);
}
