// ground_trail_comp.glsl — the ground trails' update (render/ground_trails.go,
// docs/trails/SPEC.md).
//
// The trail map is a square of world ground around the player, one texel a
// few centimetres, wrapped TOROIDALLY: world cell c lives in texel c mod
// size, so as the window follows the player nothing is copied — the texels
// that scroll out of one side are the ones that come in on the other, and
// they are cleared here as they do (they hold ground from `size` texels
// away). Each texel is how far the ground there is pressed in, as a share of
// the soft layer's own depth: positive a dent, negative a raised rim. Two
// channels — red fills back in within a minute or so (sand slumping and
// blowing back), green lasts the session (snow) — both stamped alike and
// decayed at their own rates; the splat shader mixes them by each layer's
// fill.
//
// A stamp is a capsule (a footprint is a short one along the foot): inside,
// a flat-bottomed bowl pressed to its strength, deepest where it is pressed
// hardest and never shallower than what was there; around it, a rim of the
// ground it pushed aside, raised a little where nothing has been pressed.

layout(local_size_x = 8, local_size_y = 8) in;

layout(rg16f, binding = 0) uniform image2D trail;

uniform ivec2 origin;     // the window's first world cell, this update
uniform ivec2 prevOrigin; // and the last's
uniform int size;         // texels a side
// The cells this dispatch covers: the whole window (cellBase = origin,
// extent = size), or only the rectangle a frame's stamps reach, on a frame
// with nothing else to do (decay 1, prevOrigin = origin).
uniform ivec2 cellBase;
uniform ivec2 extent;
uniform float texel;      // metres a texel
uniform vec2 decay;       // what each channel keeps this update (red fast, green slow)
uniform int clearAll;     // 1: the first update (or a jump): everything is stale
uniform int stampCount;
uniform vec4 stampAB[32]; // a.x, a.z, b.x, b.z (world metres)
uniform vec4 stampP[32];  // radius (m), strength (0..1), rim (0..1), unused

// imod is a mod b for any a (GLSL's % is undefined for negatives).
int imod(int a, int b) {
    return a - b * int(floor(float(a) / float(b)));
}

void main() {
    ivec2 g = ivec2(gl_GlobalInvocationID.xy);
    if (g.x >= extent.x || g.y >= extent.y) {
        return;
    }
    // The world cell (inside the window by construction) and its texel.
    ivec2 c = cellBase + g;
    ivec2 t = ivec2(imod(c.x, size), imod(c.y, size));
    vec2 v = imageLoad(trail, t).rg;
    bool stale = clearAll != 0 || any(lessThan(c, prevOrigin)) || any(greaterThanEqual(c, prevOrigin + ivec2(size)));
    if (stale) {
        v = vec2(0.0);
    }
    v *= decay;
    // Below a hundredth of the depth there is nothing left to see.
    v = mix(v, vec2(0.0), lessThan(abs(v), vec2(0.004)));

    vec2 p = (vec2(c) + 0.5) * texel;
    for (int i = 0; i < stampCount; i++) {
        vec2 a = stampAB[i].xy, b = stampAB[i].zw;
        vec2 ab = b - a;
        float h = clamp(dot(p - a, ab) / max(dot(ab, ab), 1e-8), 0.0, 1.0);
        // How far outside the print's edge (metres; negative inside).
        float beyond = length(p - a - ab * h) - stampP[i].x;
        if (beyond > 0.08) {
            continue; // past the rim
        }
        float strength = stampP[i].y;
        // A flat floor and a steep wall a centimetre and a half wide: a
        // boot leaves an edge, not a bowl.
        float press = strength * (1.0 - smoothstep(-0.015, 0.003, beyond));
        if (press > 0.0) {
            v = max(v, vec2(press));
        }
        // The rim: ground pushed out of the print, piled just outside it,
        // raised only where the ground is not already pressed (a print laid
        // over an older one's rim flattens it, and its own rim does not
        // fill the older print back in).
        // A band about 2.5 cm outside the print and 2 cm wide.
        float rim = stampP[i].z * strength * exp(-pow((beyond - 0.025) / 0.02, 2.0));
        v = max(v - vec2(rim) * step(v, vec2(0.05)), vec2(-0.6));
    }
    imageStore(trail, t, vec4(v, 0.0, 0.0));
}
