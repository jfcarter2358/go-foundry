// wind_common.glsl — wind on static meshes (gfx/wind_shader.go splices this
// in after the #version line).
//
// A cooked Nanite placement (world.Scenery with Wind set) has no bones; it
// bends in the vertex stage instead, by an offset worked out from where each
// vertex sits in its mesh: how high above the mesh's base (the trunk bends
// over by the square of it) and how far out from its vertical axis (branches
// and leaves flutter more the further they reach from the trunk). Nothing
// about the mesh is authored or cooked for it.
//
// EVERY stage that turns a cooked vertex into a position must add the same
// offset, or the passes disagree about where the tree is: vertex.glsl (the
// forward pass, the depth prepass, the visibility buffer's ID pass and its
// material pass), shadow_vert.glsl / shadow_cube_vert.glsl, the compute
// rasteriser and the debug view. The cull pads its bounds by windReach.
//
// The gust is the scatter's own travelling wave (foliage_common.glsl), on
// the same direction, strength and clock, so a tree bends with the grass
// round its feet.

// windNow: xy the wind's direction across the ground (unit), z its strength
// (0: still air), w the wind clock in seconds (render.BiomeWindTime).
// Written once a frame (gfx.Wind); a program that never reads it pays
// nothing for declaring it.
layout(std140, binding = 2) uniform WindBlock {
    vec4 windNow;
};

// windOffset is how far the wind moves the vertex at local, of a placement
// whose model matrix is M. w is the placement's wind: x the bend (how far
// the top leans over in a full gust, as a fraction of the height), y the
// flutter (how far a leaf at the crown's edge moves, in metres), z the
// mesh's base height and w one over its height, both in its own space.
// sphere is the mesh's bounding sphere in its own space: its centre's xz is
// the axis the trunk stands on, its radius how far out the crown reaches.
// Zero when the placement has no wind (x and y zero — also what an
// instance attribute that was never enabled reads) or the air is still.
vec3 windOffset(vec3 local, mat4 M, vec4 w, vec4 sphere) {
    float k = windNow.z;
    if (k <= 0.0 || (w.x <= 0.0 && w.y <= 0.0)) {
        return vec3(0.0);
    }
    float t = windNow.w;
    vec2 dir = windNow.xy;
    vec3 wd = vec3(dir.x, 0.0, dir.y);
    vec3 root = M[3].xyz;
    float height = length(M[1].xyz) / max(w.w, 1e-6);
    // 0 at the base, 1 at the top; 0 on the trunk's axis, 1 at the crown's edge.
    float up = clamp((local.y - w.z) * w.w, 0.0, 1.0);
    float out_ = clamp(length(local.xz - sphere.xz) / max(sphere.w, 1e-4), 0.0, 1.0);

    // The gust, travelling across the level along the wind.
    float phase = dot(root.xz, dir) * 0.09 - t * 1.3;
    float gust = 0.5 + 0.5 * sin(phase + 1.7 * sin(phase * 0.37 + root.x * 0.05));

    // The trunk leans with the gust and rocks a little about its lean.
    float rock = sin(t * 1.1 + root.x * 0.31 + root.z * 0.17);
    float bend = k * w.x * height * up * up * (0.2 + 0.8 * gust + 0.15 * rock);
    vec3 d = wd * bend;
    // What bends over comes down, so the top swings on an arc.
    d.y -= bend * bend * 0.5 / max(height, 0.05);

    // Branches bob and leaves flutter, more the further out and the higher
    // they are. The phases run through the vertex's world position at a few
    // metres (a branch) and a few tens of centimetres (a leaf), so a leaf's
    // corners move together and its neighbours do not.
    vec3 p = (M * vec4(local, 1.0)).xyz;
    float f = k * w.y * out_ * up * (0.5 + 0.5 * gust);
    float bob = sin(t * 2.3 + dot(p, vec3(0.41, 0.23, 0.37)));
    float l1 = sin(t * 7.1 + dot(p, vec3(3.1, 2.3, 2.7)));
    float l2 = sin(t * 5.3 + dot(p, vec3(-2.2, 3.4, 1.9)));
    d += (vec3(0.0, bob, 0.0) + wd * (0.5 * bob)) * f + vec3(l1, l2, l2 - l1) * (0.35 * f);
    return d;
}

// windReach is the farthest windOffset can move any vertex of the placement:
// what the cull pads a placement's and its clusters' spheres by, so a
// branch is never culled where it has swung to.
float windReach(mat4 M, vec4 w) {
    float k = windNow.z;
    if (k <= 0.0 || (w.x <= 0.0 && w.y <= 0.0)) {
        return 0.0;
    }
    float height = length(M[1].xyz) / max(w.w, 1e-6);
    float bend = k * max(w.x, 0.0) * height * 1.15;
    return bend + bend * bend * 0.5 / max(height, 0.05) + 2.5 * k * max(w.y, 0.0);
}
