#version 410

// sky_vert.glsl — the skybox cube.
//
// Two tricks, both standard and both necessary:
//
//   The view matrix arrives with its translation already stripped (see
//   RenderSky), so the cube is centred on the eye no matter where the eye is.
//   A sky that translated with the camera would have a near side you could
//   walk toward.
//
//   gl_Position is written as p.xyww, which forces z/w to exactly 1.0 — the
//   far plane. With the engine's global GL_LEQUAL depth func that draws the
//   sky into precisely the pixels nothing else reached, so it costs one
//   fragment per uncovered pixel and never overdraws the world in front of it.
//   It is why the sky is drawn AFTER the opaque pass rather than before it.

layout(location = 0) in vec3 aPos;

uniform mat4 projection;
uniform mat4 view; // camera matrix, translation removed

// The unrotated sampling direction, passed through rather than yawed here.
// A sky mid-transition draws from two states that can each name their own
// rotation (see ease_sky in SCRIPTING.md), and rotating a single direction
// once in the vertex stage can only ever agree with one of them — so
// sky_frag.glsl applies skyYaw and skyYawFrom itself, each to this same raw
// direction, and this stage's job shrinks to handing it over.
out vec3 vDir;

void main() {
    vDir = aPos;

    vec4 p = projection * view * vec4(aPos, 1.0);
    gl_Position = p.xyww;
}
