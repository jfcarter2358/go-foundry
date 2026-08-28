#version 410

// sprite_shadow_vert.glsl — directional (cascade) depth stage for sprites.
//
// Depth only; it pairs with shadow_frag.glsl unchanged. The placement comes
// from sprite_common.glsl, the same text the colour stage compiles, with
// `directional` set so the quad faces the light rather than the camera.

void main() {
    vec3 normal;
    gl_Position = projView * vec4(spriteWorldPos(normal), 1.0);
}
