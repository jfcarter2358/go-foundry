// sprite_common.glsl — instance layout and billboard basis for the sprite pass.
//
// Injected after the #version line of all three sprite vertex stages
// (sprite_vert.glsl, sprite_shadow_vert.glsl, sprite_cube_vert.glsl) by
// mustSpriteProgram in render/sprite_render.go, so a quad casts the shadow of
// the quad you can see. This is the same rule biome_scatter_common.glsl states
// for the scatter pass, and it bites harder here: a billboard's orientation is
// a function of where it is being looked at FROM, so a depth stage computing
// its own basis would detach a sprite's shadow from the sprite the moment the
// camera turned.
//
// What makes that work is `viewPos`/`directional` rather than a hard-coded
// camera: the colour pass hands in the eye, the cascade pass hands in the
// light's direction, and the cubemap pass hands in the light's position. The
// quad then faces whatever is looking at it. A camera-facing quad rendered from
// a light is edge-on and casts nothing at all, which is the bug this parameter
// exists to prevent.
//
// This is the engine's first use of per-instance vertex attributes
// (glVertexAttribDivisor). Everything else instanced — biome scatter, detail
// scatter — derives placement procedurally from gl_InstanceID, which is a
// placement trick and cannot carry simulated state. Locations 1..4 below step
// once per instance; location 0 steps per vertex.

// The unit quad, in [-0.5, 0.5] on both axes. Four vertices, one static buffer,
// shared by every sprite and every particle in the level.
layout(location = 0) in vec2 aCorner;

// Per-instance state, divisor 1. Packed as 14 contiguous floats — see
// spriteInstanceFloats in sprite_render.go, which must agree with this.
layout(location = 1) in vec3 iPos;   // world-space centre
layout(location = 2) in vec2 iSize;  // width, height in world units
layout(location = 3) in float iRot;  // roll about the view axis, radians
layout(location = 4) in vec4 iColor; // tint (rgb) and alpha, premultiplied by nothing
layout(location = 5) in vec4 iUV;    // u0, v0, du, dv — the cell of a sheet

uniform mat4 projView;

// viewPos is the eye for the colour pass and the light's position for the
// cubemap depth pass. Ignored when directional is set.
uniform vec3 viewPos;
// viewDir is the direction light TRAVELS, used only when directional is set;
// the quad then faces -viewDir everywhere rather than converging on a point.
uniform vec3 viewDir;
uniform int directional;

// billboardMode: 0 = full (face the viewer outright), 1 = y-axis (world up
// stays up, yaw only), 2 = fixed (no billboarding; use the basis below).
// Batched, so every instance in one draw shares a mode.
uniform int billboardMode;

// fixedRight/fixedUp are the authored basis for mode 2, from the sprite's own
// Euler rotation. Unread in the other two modes.
uniform vec3 fixedRight;
uniform vec3 fixedUp;

// spriteBasis computes the quad's right and up vectors at a world position, and
// the normal it should be lit as facing.
void spriteBasis(vec3 worldPos, out vec3 right, out vec3 up, out vec3 normal) {
    // Toward whatever is doing the looking. A directional light has no position
    // to converge on, so every quad faces the same way for it.
    vec3 toView = (directional != 0)
        ? normalize(-viewDir)
        : normalize(viewPos - worldPos);

    if (billboardMode == 2) {
        right  = fixedRight;
        up     = fixedUp;
        normal = normalize(cross(right, up));
        return;
    }

    if (billboardMode == 1) {
        // Y-axis: world up stays the quad's up and only the yaw follows the
        // viewer. A bush must not lean back as the camera rises.
        up = vec3(0.0, 1.0, 0.0);
        // `flat` is a reserved interpolation qualifier, hence the name.
        vec3 level = toView - up * dot(toView, up);
        // Looking straight down the pole leaves nothing to yaw toward; any
        // consistent fallback beats a normalize() of the zero vector.
        right = (dot(level, level) < 1e-8)
            ? vec3(1.0, 0.0, 0.0)
            : normalize(cross(up, normalize(level)));
        normal = normalize(cross(right, up));
    } else {
        // Full: a basis square to the view direction. World up as the
        // reference, swapped out when the quad is being looked at from
        // directly above or below.
        vec3 ref = (abs(toView.y) > 0.999) ? vec3(0.0, 0.0, 1.0) : vec3(0.0, 1.0, 0.0);
        right  = normalize(cross(ref, toView));
        up     = cross(toView, right);
        normal = toView;
    }

    // Roll, applied after the basis so it means the same thing in both modes:
    // a spin in the plane of the quad, which is what a tumbling particle wants.
    if (iRot != 0.0) {
        float c = cos(iRot);
        float s = sin(iRot);
        vec3 r = right * c + up * s;
        up    = up * c - right * s;
        right = r;
    }
}

// spriteWorldPos places this vertex. normal comes back for the lighting the
// colour stage does; the depth stages discard it.
vec3 spriteWorldPos(out vec3 normal) {
    vec3 right, up;
    // The basis is computed at the quad's CENTRE, not at the vertex — using the
    // vertex would give the four corners four different bases and skew the quad.
    spriteBasis(iPos, right, up, normal);
    return iPos
         + right * (aCorner.x * iSize.x)
         + up    * (aCorner.y * iSize.y);
}

// spriteTexCoord maps the corner onto the instance's cell of the sheet. The
// corner is in [-0.5, 0.5] and V is flipped, matching the rest of the engine's
// convention that V grows downward in image space.
vec2 spriteTexCoord() {
    vec2 uv = aCorner + vec2(0.5);
    uv.y = 1.0 - uv.y;
    return iUV.xy + uv * iUV.zw;
}
