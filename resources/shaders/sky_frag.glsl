#version 430

// sky_frag.glsl — what the skybox cube is filled with.
//
// Two modes behind one uniform rather than two programs: a level that has art
// samples a cubemap, and one that does not gets a three-stop vertical gradient
// that needs no art at all. The gradient is not a placeholder — it is what
// makes `sky:` worth writing in a level that only wants the horizon to stop
// being a flat clear colour, and it is what gives fog something to agree with.
//
// A THIRD state rides alongside those two rather than a mode of its own: a
// script's ease_sky (see SCRIPTING.md) can be blending between two complete
// sky states — kind, art and all — rather than switching between them
// instantly. skyLook() evaluates one full state (the "to" one this level is
// authored as right now, or the "from" one a script eased away from) exactly
// as this file always has, and main() mixes the two results by skyBlend. That
// is the only way to cross-fade a cubemap into a gradient, or one cubemap into
// a different one, that does not need a third shader: colour can be lerped,
// but "kind" cannot, so the lerp has to happen after each side has already
// been turned into a colour.
//
// fog.glsl is injected above this (see InjectFog): the sky is the one surface
// fog must NOT simply be mixed into at full strength, because it is infinitely
// far away and would then be nothing but fog. What it takes instead is a
// horizon-weighted share of it — see skyFogBlend below.

uniform int skyKind; // 0 = gradient, 1 = cubemap. Matches world.SkyKind.

uniform samplerCube skyTex;

// Gradient stops. zenith is straight up, ground straight down, and horizon is
// the ring between them the two are eased into.
uniform vec3 skyZenith;
uniform vec3 skyHorizon;
uniform vec3 skyGround;

uniform vec3  skyTint;      // multiplies whichever mode produced the colour
uniform float skyIntensity;
uniform float skyYaw;       // radians about +Y; turns the sky, not the cube

// The state being eased AWAY from, one full sky evaluated the same way, and
// skyBlend between the two: 0 is skyFrom alone, 1 is sky (above) alone. Outside
// a transition RenderSky uploads skyBlend as 1 and these are never sampled, so
// a settled sky still costs one cubemap fetch rather than two.
uniform float skyBlend;
uniform int   skyKindFrom;
uniform samplerCube skyTexFrom;
uniform vec3  skyZenithFrom;
uniform vec3  skyHorizonFrom;
uniform vec3  skyGroundFrom;
uniform vec3  skyTintFrom;
uniform float skyIntensityFrom;
uniform float skyYawFrom;

// How much of the fog colour the sky takes at the horizon, and how tightly
// that is confined to it. 0 leaves the sky untouched.
//
// Without this, fog is a horizon problem you can see: ground fades to fogColor
// while the sky behind it stays crisp, and the join between them becomes the
// most visible edge on screen — precisely at the distance the fog was added to
// hide. Weighting by |direction.y| puts the fog where the geometry it is
// hiding actually is and leaves the zenith clear.
uniform float skyFogBlend;
uniform float skyFogFalloff;

// How far along the view ray the sky is treated as being, for the fog volume
// test. Any distance past the far plane does; it only has to be beyond every
// volume in the level, and a volume's own bounds are what actually decide the
// span.
#define SKY_FOG_VOLUME_REACH 100000.0

in vec3 vDir;

out vec4 outColor;

// skyLook evaluates one complete sky state — kind, art or gradient, tint and
// intensity — in the direction d, which is vDir yawed by this state's OWN
// rotation. The two call sites (the current sky and, mid-transition, the one
// it is easing away from) are otherwise identical, which is the point: a
// transition is two of these mixed, not a third code path.
vec3 skyLook(vec3 dLocal, float yaw, int kind, samplerCube tex,
             vec3 zenith, vec3 horizon, vec3 ground, vec3 tint, float intensity) {
    float c = cos(yaw);
    float s = sin(yaw);
    vec3 d = normalize(vec3(c * dLocal.x + s * dLocal.z, dLocal.y, -s * dLocal.x + c * dLocal.z));

    vec3 col;
    if (kind == 1) {
        col = texture(tex, d).rgb;
    } else {
        // pow eases each half so the horizon band stays narrow and the zenith
        // holds its colour over most of the dome, which is what a sky actually
        // does; a straight lerp puts the midpoint at 45 degrees of elevation
        // and reads as a painted ramp.
        float t = clamp(abs(d.y), 0.0, 1.0);
        vec3 far = (d.y >= 0.0) ? zenith : ground;
        col = mix(horizon, far, pow(t, 0.55));
    }
    return col * tint * intensity;
}

void main() {
    vec3 col = skyLook(vDir, skyYaw, skyKind, skyTex,
                        skyZenith, skyHorizon, skyGround, skyTint, skyIntensity);

    if (skyBlend < 1.0) {
        vec3 from = skyLook(vDir, skyYawFrom, skyKindFrom, skyTexFrom,
                             skyZenithFrom, skyHorizonFrom, skyGroundFrom, skyTintFrom, skyIntensityFrom);
        col = mix(from, col, clamp(skyBlend, 0.0, 1.0));
    }

    vec3 d = normalize(vDir);
    if (fogMode != 0 && fogMode != 4 && skyFogBlend > 0.0) {
        float horizon = pow(1.0 - clamp(abs(d.y), 0.0, 1.0), max(skyFogFalloff, 0.001));
        col = mix(col, fogColor, clamp(skyFogBlend * horizon, 0.0, 1.0));
    }

    // The atmosphere (fog.glsl), integrated along the sky ray as far as
    // fogSkyDistance: the same height fog and aerial haze a far cliff is
    // seen through, so the cliff and the sky behind it end in the same air.
    // It needs no horizon weighting of its own — a ray climbing out of the
    // haze passes through little of it, a level one through all of it — and
    // that is what makes the horizon haze match the ground's rather than
    // approximate it. fogSkyHaze scales it, for a sky whose art already holds
    // a haze of its own.
    if (fogAtmos != 0 && fogSkyHaze > 0.0) {
        vec3 T;
        vec3 add = fogAtmosSegmentDisplay(fogCamPos, d, fogSkyDistance, T);
        col = mix(col, col * T + add, clamp(fogSkyHaze, 0.0, 1.0));
    }

    // Fog VOLUMES, unlike the distance fog, take the sky at full strength, and
    // need no horizon weighting to do it: they are bounded, so the ray leaves
    // them and the span is finite however far the sky is. Standing inside a fog
    // bank with a crisp sky overhead is the single most obvious way for one to
    // stop reading as fog, and looking through a distant bank at the horizon
    // should dim that horizon for the same reason it dims the hill in front of
    // it. A point far enough along the view ray stands in for infinity; the
    // volume clips the segment long before it gets there.
    col = applyFogVolumesDisplay(col, fogCamPos + d * SKY_FOG_VOLUME_REACH);

    // The sky is screen colour, worked out in screen colour to here: in an
    // HDR frame (gfx/hdr.go) it is carried into the scene's units once, at
    // the end, so the tone curve gives it back exactly as it would have been
    // shown. (Mixing the haze in after the carry instead, in the scene's
    // units, magnified the cubemap's 8-bit steps near white, where the
    // curve's inverse is steep, into visible bands.) Off, a no-op.
    outColor = vec4(hdrFromDisplay(col), 1.0);
}
