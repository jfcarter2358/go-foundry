#version 410

// biome_decal_vert.glsl — fake ground-contact shadow halo (biome_shadow_decal.go).
//
// Geometry is fully baked on the CPU (a ring conforming to the structural
// floor around a scattering sector's own polygon edge — see
// buildBiomeDecalMesh), so this stage just transforms it. aWeight is purely
// geometric (1 at the sector's own edge, 0 at the halo's outer reach); the
// strength the user actually painted is applied once, as a uniform, in
// biome_decal_frag.glsl — so dialing FakeShadowStrength needs no mesh rebuild,
// only FakeShadowSpread (which changes the ring's actual extent) does.

in vec3 aPos;
in float aWeight;

uniform mat4 projView;

out float vWeight;

void main() {
    vWeight = aWeight;
    gl_Position = projView * vec4(aPos, 1.0);
}
