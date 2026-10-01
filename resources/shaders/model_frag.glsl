#version 430

// model_frag.glsl — flat headlight shading for the model editor.
//
// A camera-relative light rather than the scene's: a model under authoring has
// no scene, and a fixed key light would leave whole faces unreadably dark
// exactly when the user rotates round to work on them. The ambient floor keeps
// silhouette faces visible instead of black.

uniform sampler2D tex;
// Untextured meshes take their albedo from the surface normal instead of from
// tex — the same rule the world shader states at length (fragment.glsl), so a
// model with no texture looks the same in the editor as it does in the level.
uniform int  normalColor;
uniform vec3 camPos;
uniform vec3 tint;          // multiplies the sample; selection highlighting
uniform int  smoothShading;

in vec2 fragTexCoord;
in vec3 fragNormal;
in vec3 fragWorldPos;

out vec4 outputColor;

void main() {
    vec3 n;
    if (smoothShading != 0 && dot(fragNormal, fragNormal) > 1e-12) {
        n = normalize(fragNormal);
    } else {
        n = normalize(cross(dFdx(fragWorldPos), dFdy(fragWorldPos)));
    }

    vec3 viewDir = normalize(camPos - fragWorldPos);
    // The normal as the surface declares it, before the flip below: an
    // untextured face keeps one colour whichever side of it the camera is on,
    // which is exactly what the flip would destroy on a mesh that is open or
    // inside-out mid-edit.
    vec3 albedoNormal = n;
    // Editor meshes are routinely open or inside-out mid-edit, so light both
    // facings rather than dropping back-faces into flat ambient.
    if (dot(n, viewDir) < 0.0) {
        n = -n;
    }

    float lambert = max(dot(n, viewDir), 0.0);
    vec3 base;
    if (normalColor != 0) {
        base = albedoNormal * 0.5 + 0.5;
    } else {
        base = texture(tex, fragTexCoord).rgb;
    }
    outputColor = vec4(base * (0.35 + 0.65 * lambert) * tint, 1.0);
}
