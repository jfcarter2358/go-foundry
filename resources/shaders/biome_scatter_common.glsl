// biome_scatter_common.glsl — shared instance placement + shaping for the
// GPU-scattered biome pass (grass/wildflower/cattail blades, rock and moss
// debris).
//
// Injected after the #version line of biome_vert.glsl (color),
// biome_shadow_vert.glsl (directional cascade depth) and biome_cube_vert.glsl
// (point-light cubemap depth) — see biomeScatterSnippet in biome_light.go.
// This mirrors detail_scatter_common.glsl's relationship with its own three
// vertex stages.
//
// It exists so a blade casts the shadow of the blade you can actually see: the
// lattice, the hash jitter, the clump gathering, the shape mask, the height
// drop, the wind sway and the bow are all evaluated ONCE here, and all three
// stages call it. A second, hand-kept copy of this math in a shadow shader
// would drift the instant either side was tuned, and the shadows would slide
// off their blades.
//
// Every blade/rock is generated entirely on the GPU: gl_InstanceID indexes a
// world-stable lattice around the camera; a hash scatters/jitters it; a
// point-in-shape test against the volume's own extents (uploaded as
// a uniform array — no coverage texture) decides whether it survives; a
// per-volume height texture drops it onto the geometry beneath it.
// No per-instance CPU data is used — only a small template mesh drawn with
// glDrawArraysInstanced. Grass can additionally gather neighbouring lattice
// slots into plant-like tufts — see the `clump` uniform.
//
// aParam encodes which "part" of the template a vertex belongs to (see the
// mesh builders in grass.go): [0,1] stem/blade bend fraction, [2,3) a
// wildflower's bloom, [3,4) a cattail's seed head, [4,5) a wildflower's
// optional bloom center, [5,6) a wildflower's optional basal leaves — whose
// fractional part, like the stem's, is the vertex's height as a fraction of
// the stem, so it bends in the wind by how high off the ground it sits.
// Rocks don't use it.

// Camera-centered instancing ring. camXZ is the camera position carried into
// the OWNING CHUNK's own local space (see uploadBiomeChunk in grass.go) —
// every uniform the lattice is tested against below (the volume shape,
// heightTex) is
// chunk-local too, so the whole placement pipeline runs self-consistently in
// that one frame; only the FINAL placed bWorldPos/bNormal get carried back
// out to true world space, by the `model` uniform each vertex stage applies
// once it returns. camPos (a separate value — the real, un-carried world
// camera position) is for the fragment stage's rock specular only and is
// never referenced by anything in this file.
uniform vec2  camXZ;
uniform vec3  camPos;
uniform float spacing;  // lattice spacing (world units) between scatter slots
uniform int   gridN;    // slots per side (instanceCount == gridN*gridN)
uniform float range;    // max spawn radius from camera THIS draw call covers

// Inner boundary of this draw call's own distance band (world units from the
// camera), 0 for a kind's innermost/only LOD tier. Lets one volume be drawn
// as up to three concentric-ring passes — Near/Mid/Far, see grass.go's
// three-tier scatter doc comment — each binding a different template mesh
// and its own [lodMin, range] slice of the SAME camera-centered lattice
// math, so a tier's own outer edge is just `range` (reusing everything
// rangeFadeFraction already does) and its inner edge is this. NOTE this
// tier's instances actually start a little BEFORE lodMin — see lodStart
// below, where this is used.
uniform float lodMin;
// Fraction of lodMin itself the grow-in band (lodStart..lodMin, see below)
// spans — always Go's fixed biomeLODFadeFraction, deliberately never the
// user's own RangeFadeFraction (which rangeFadeFraction below carries
// instead, and only means something on a tier's OUTER edge). This has to
// match the PREVIOUS tier's own outer-edge fade fraction exactly (also
// always biomeLODFadeFraction, for the same reason), because the two bands
// are meant to sit on top of each other, not end-to-end — see lodStart's own
// doc comment for why. 0 on a kind's innermost/only tier, where lodMin == 0
// already makes lodGrowIn (biomePlace) a no-op regardless.
uniform float lodFadeFraction;
// Whether THIS tier's own outer edge (`range`) is an LOD split (Near->Mid,
// Mid->Far — nonzero) or the volume's true, shape-independent outer edge
// (zero, the last/only tier). Picks which kind of fade that edge gets: an LOD
// split dithers (lodShrinkOut in biomePlace, folded into the alive test) so
// it doesn't gap against the next tier's own dithered grow-in; the true edge
// still shrinks toward its root (rangeFade), since nothing picks up past it.
uniform int   outerIsLOD;

// Radius beyond which an instance is dropped from THIS pass only. The color
// pass uploads `range` (i.e. no extra rejection); the shadow passes upload a
// shorter one, so small scatter casts contact shadows near the camera without
// re-drawing the whole ring into every cascade. Deliberately a separate
// uniform rather than a smaller `range`: `range` feeds the lattice origin and
// the distance fade, so shrinking it would move every instance and the
// shadows would no longer line up with the blades that cast them.
uniform float castRange;

// Soft edges (see BiomeBrush's RangeFadeFraction/EdgeFadeDistance doc
// comments) — both shrink an instance toward its root via the SAME `fade`
// factor `off` is already scaled by below, so a boundary dissolves as
// instances shrinking to a point rather than as a visible density cliff.
//
// rangeFadeFraction is the fraction of `range` (from the outer edge inward)
// the camera-ring boundary fades over; 0 reproduces the exact hard cutoff the
// `dist <= range` alive test already gives on its own (no shrink at all).
uniform float rangeFadeFraction;
// edgeFadeDistance is how many world units in from the volume's OWN boundary
// an instance takes to grow to full size; 0 disables it (an
// instance right at the boundary is already full size, i.e. today's hard
// edge). Off by default: unlike the range fade, it costs a real per-instance
// distance-to-boundary query (see volEdgeDist), so it's only paid for volumes
// that actually opt in.
uniform float edgeFadeDistance;

// BoundaryJitter (world units) — see BiomeBrush's own doc comment. Bulges
// EVERY circular boundary this instance's distance is compared against (the
// alive test's range/lodStart/castRange cutoffs, and every fade/dither
// envelope's smoothstep span) by a smooth, low-frequency world-space noise,
// so a volume's rings read as an organic patch of thinning vegetation instead
// of a perfect circle centered on the camera. Applied once, to `dist` itself (see
// distJ in biomePlace), rather than to each boundary separately — that's what
// keeps a volume's Near/Mid/Far/outer rings bulging and receding TOGETHER at
// the same world positions instead of each wobbling on its own.
uniform float boundaryJitter;

uniform float time;       // seconds, for wind animation
uniform int   shapeKind;  // 0=grass, 1=rock, 2=wildflower, 3=cattail
//                           (Moss draws through this program too, but always
//                           uploads shapeKind=1 — a squashed VAO is its only
//                           difference from Rock; see renderBiomeSector.)
uniform float bladeHeight;// world height of a unit blade/stem
uniform float bladeWidth; // world half-width of a blade/stem at its base
uniform float headScale;  // world size of a wildflower bloom / cattail seed head
uniform float rockScale;  // world scale of a unit rock

// Wildflower bloom options (see BiomeBrush's Bloom* fields). bloomAspect
// scales the template's UNIT bloom blob into its actual silhouette — a squat
// petal disc or an elongated spike — so one VAO serves both. Cattail uploads
// (1,1,1) here, since its seed head's proportions are baked into its own
// template instead. hasCenter gates the separate center blob (collapsed to a
// degenerate triangle when off); bloomSolid swaps the per-instance random hue
// for tintColor used directly.
uniform vec3 bloomAspect;
uniform int  hasCenter;
uniform vec3 centerColor;
uniform int  bloomSolid;
uniform int  hasLeaves; // wildflower basal leaves, gated the same degenerate-triangle way
// Multiplier on the leaves' authored size. NOT a world size like headScale:
// the leaves are built in fractions of the stem's own height, so this scales
// what is already proportional rather than replacing it.
uniform float leafScale;
uniform float density;    // 0..1 survival probability, uniform across the volume

// Grass clumping (BiomeBrush.GrassClump). 0 reproduces the original
// one-blade-per-lattice-slot scatter EXACTLY — every expression below folds
// away to its old value at 0 — and 1 gathers each CLUMP_SIDE^2 block of
// lattice slots into a tuft that reads as a single grass plant: the blades
// pull in toward a shared root, fan out radially, arc outward along their
// length, and are graded so the middle of the tuft stands tall and near
// vertical while the rim splays short and low. Only Grass ever uploads a
// non-zero value (see renderBiomeSector).
uniform float clump;

const float CLUMP_SIDE  = 3.0;  // lattice slots per tuft, per axis (so up to 9 blades)
const float CLUMP_TIGHT = 0.26; // fraction of its original spread a blade keeps at clump==1
const float CLUMP_ROOT_JITTER = 0.85; // how much of its own cell a tuft's root wanders over
const float CLUMP_SIZE_MIN = 0.72; // per-tuft overall scale range, so plants aren't all one size
const float CLUMP_SIZE_MAX = 1.28;
const float CLUMP_ARC   = 0.85; // outward tip lean of a rim blade, in blade heights
const float CLUMP_DROOP = 0.30; // how much that arc foreshortens the blade, so it bows instead of stretching
const float CLUMP_TALL  = 1.50; // height multiplier at the middle of a tuft
const float CLUMP_SHORT = 0.55; // height multiplier at its rim

// Fake self-shadowing for foliage — the cheapest lighting cue in the whole
// pass, and the one that does the most work. Real per-blade occlusion would
// mean each blade sampling its neighbours; instead the two things that
// occlusion actually correlates with are already known here for free:
//   * how far up the stem a vertex sits (a root is buried in the sward, a tip
//     stands in open sky), and
//   * how deep into a tuft a blade sits (the middle blade of a clump is
//     surrounded, the rim ones are not).
// Two mixes, no samples, no bandwidth. See bAO.
const float BIOME_ROOT_AO = 0.55; // lighting multiplier at the very base of a stem
const float BIOME_TUFT_AO = 0.78; // extra multiplier at the middle of a full clump

// The scatter volume's own shape (in the frame camXZ is in —
// same frame as camXZ above). Tested directly instead of sampling a
// rasterized coverage texture.
// The scatter volume's own shape, in the frame every other uniform here is in
// (see camXZ). A candidate is carried INTO the volume's frame by volInvRot and
// tested analytically, which replaces the ray-cast point-in-polygon the painted
// version needed — cheaper, and four uniforms instead of a 64-entry array.
//
// volShape matches world.VolumeShape: 0 = box, 1 = sphere, 2 = cylinder.
uniform vec3 volCenter;
uniform vec3 volHalf;
uniform mat4 volInvRot;
uniform int  volShape;

// Per-volume height/coverage texture: covers [heightOrigin,
// heightOrigin+heightSize]. .r is the height to drop the instance onto
// (structural floor, or a covering face's actual displaced surface — see
// biome_displace.go); .g is a slope-coverage factor in [0,1] (1 = fully
// covered) that gates survival below alongside density, so instances thin
// out and vanish past the brush's configured slope range instead of
// floating on / embedding in a cliff face.
uniform sampler2D heightTex;
uniform vec2 heightOrigin;
uniform vec2 heightSize;

// Per-volume seed (derived from the volume's ID) shifts the hash inputs so
// different volumes don't scatter identically even when their lattices overlap
// in world space — e.g. tall grass and short grass placed
// over the same area land on different spots instead of stacking.
uniform vec2 seed;
uniform vec3 tintColor; // paint color: multiplies the kind's base color(s)

// ---- biomePlace outputs (valid only when it returned true) ----
// Despite the name, bWorldPos/bNormal are chunk-local here (the frame every
// other uniform above is in) — each caller's own vertex stage applies `model`
// to carry them out to true world space once biomePlace returns. The name is
// kept because that's what it becomes by the time anything downstream reads
// it as a varying.
vec3  bWorldPos;   // this vertex's final position
vec3  bBasePt;     // the instance's root point on the ground
vec3  bNormal;     // normal — rock: warped facet; foliage: up
vec3  bBaseColor;  // unlit albedo for this vertex
float bAO;         // fake self-shadow occlusion multiplier (see BIOME_ROOT_AO)

float hash11(float n) { return fract(sin(n) * 43758.5453123); }

vec2 hash22(vec2 p) {
    p = vec2(dot(p, vec2(127.1, 311.7)), dot(p, vec2(269.5, 183.3)));
    return fract(sin(p) * 43758.5453);
}

// BOUNDARY_JITTER_FREQ sets the wavelength of the bulges BoundaryJitter draws
// on the range/LOD circles — a fixed constant (not user-exposed) rather than
// another slider, since the one thing worth authoring is HOW FAR the boundary
// wanders (BoundaryJitter itself); how tightly it wiggles while doing so isn't.
// 1/0.05 = a 20-world-unit period, a handful of gentle lobes around even the
// smallest default range (Wildflower/Cattail's 40).
const float BOUNDARY_JITTER_FREQ = 0.05;

// Smooth low-frequency 2D value noise, built from the SAME hash22 the lattice
// jitter above uses. Deliberately much lower frequency than the scatter
// lattice itself (BOUNDARY_JITTER_FREQ vs 1/spacing), so many neighbouring
// instances share the same gentle bulge/recede — an independent per-instance
// jitter here would look like static fraying the edge rather than an organic
// shape change, the actual complaint being addressed.
float valueNoise2(vec2 p) {
    vec2 i = floor(p);
    vec2 f = fract(p);
    f = f * f * (3.0 - 2.0 * f);
    float n00 = hash22(i).x;
    float n10 = hash22(i + vec2(1.0, 0.0)).x;
    float n01 = hash22(i + vec2(0.0, 1.0)).x;
    float n11 = hash22(i + vec2(1.0, 1.0)).x;
    float nx0 = mix(n00, n10, f.x);
    float nx1 = mix(n01, n11, f.x);
    return mix(nx0, nx1, f.y); // ~[0,1]
}

// Bilinear (height, slope-coverage) at a chunk-local XZ, sampled from this
// volume's cached height/coverage texture — see the heightTex uniform doc.
vec2 sampleHeightCoverage(vec2 local) {
    vec2 uv = (local - heightOrigin) / heightSize;
    return textureLod(heightTex, uv, 0.0).rg;
}

// volLocalXZ carries a candidate's XZ into the volume's own frame. The Y it
// uses is the volume's own centre height, not the instance's: an instance is
// placed on the ground under the lattice slot, and gating it on ITS height
// would mean a volume had to be tall enough to enclose the terrain as well as
// wide enough to cover it — which is not what "scatter grass in this region"
// means. The vertical extent still matters for the sphere and cylinder tests
// below, where the shape's footprint narrows away from its middle; taking the
// centre plane is what makes those read as the footprint the author drew.
vec2 volLocalXZ(vec2 p) {
    vec3 rel = vec3(p.x, volCenter.y, p.y) - volCenter;
    vec3 loc = (volInvRot * vec4(rel, 0.0)).xyz;
    return loc.xz;
}

// Is this lattice slot inside the volume? The three shapes match
// world.ShapeContainsPoint's own conventions: half-extents are the box the
// shape is inscribed in, so a sphere uses the largest of them as its radius and
// a cylinder is a circle in XZ.
bool pointInVolume(vec2 p) {
    vec2 q = volLocalXZ(p);
    if (volShape == 1) {              // sphere
        float r = max(volHalf.x, max(volHalf.y, volHalf.z));
        return dot(q, q) <= r * r;
    }
    if (volShape == 2) {              // cylinder
        float r = max(volHalf.x, volHalf.z);
        return dot(q, q) <= r * r;
    }
    return abs(q.x) <= volHalf.x && abs(q.y) <= volHalf.z;
}

// Distance from p IN to the volume's own boundary — the same shape
// pointInVolume tests, read as a magnitude instead of a yes/no. Only called for
// instances that already passed pointInVolume AND the density/coverage survival
// test (see edgeFade below), so its cost lands on the instances actually being
// drawn rather than on the full lattice — the same reasoning that keeps
// sampleHeightCoverage behind the cheap rejects.
//
// Analytic per shape rather than a loop over edges, which is the other thing
// the volume buys: a box's inset distance is the smallest of its two axis
// margins, and a round shape's is its radius less the point's own.
float volEdgeDist(vec2 p) {
    vec2 q = volLocalXZ(p);
    if (volShape == 1) {
        float r = max(volHalf.x, max(volHalf.y, volHalf.z));
        return max(r - length(q), 0.0);
    }
    if (volShape == 2) {
        float r = max(volHalf.x, volHalf.z);
        return max(r - length(q), 0.0);
    }
    return max(min(volHalf.x - abs(q.x), volHalf.z - abs(q.y)), 0.0);
}

// A handful of fixed wildflower petal hues; tintColor (default neutral)
// multiplies whichever gets picked so a volume reads as mixed flowers rather
// than one repeated color, while still staying user-tintable.
vec3 flowerPalette(float h) {
    if (h < 0.2) return vec3(0.95, 0.55, 0.65); // pink
    if (h < 0.4) return vec3(0.95, 0.85, 0.25); // yellow
    if (h < 0.6) return vec3(0.65, 0.45, 0.85); // purple
    if (h < 0.8) return vec3(0.97, 0.97, 0.92); // white
    return vec3(0.92, 0.55, 0.20);              // orange
}

// Place and shape one template vertex of instance `instanceID`. Returns false
// when the instance doesn't survive its shape/range/density tests — the
// caller then collapses the vertex off-screen. Fills the b* globals above.
bool biomePlace(int instanceID, vec3 local, vec3 nrm, float param) {
    int id = instanceID;
    int ix = id % gridN;
    int iz = id / gridN;

    // World-stable lattice. The blade is hashed from its INTEGER global lattice
    // index, not its float world position: as the camera moves, the (min corner,
    // ix) split that reaches a given world point changes, so the world position
    // recomputes with ~1 ULP of difference every frame. Feeding that through
    // sin()-based hashing produces a totally different jitter each frame and the
    // grass visibly crawls. The integer index is bit-identical regardless of the
    // split, so every blade is deterministic and stays put. mod() keeps the
    // index small for stable sin() precision far from the origin.
    vec2 baseIdx = floor((camXZ - vec2(range)) / spacing);
    vec2 gi = baseIdx + vec2(float(ix), float(iz));
    vec2 hgi = mod(gi, 2048.0);
    vec2 cellPos = gi * spacing;
    vec2 rnd = hash22(hgi + seed);
    cellPos += (rnd - 0.5) * spacing * 0.85;

    // Clumping (see the `clump` uniform). Each CLUMP_SIDE^2 block of lattice
    // slots shares one jittered root point, and every blade in the block is
    // pulled toward it. Done BEFORE the shape/range/height tests below, so a
    // blade is masked and dropped onto the floor by where it actually ends up
    // rather than by the slot it came from.
    //
    // A blade's place within its tuft (`rim`: 0 in the middle, 1 at the edge)
    // comes from its INTEGER slot index, not from its jittered position — so
    // the middle slot is reliably the middle blade, the tall upright one,
    // instead of whichever blade the jitter happened to throw nearest the root.
    //
    // Three separate things keep the tufts from reading as the lattice they
    // are built on, and ALL of them are needed — the roots being jittered is
    // not enough on its own:
    //   * the root jitter spans nearly the whole tuft cell (CLUMP_ROOT_JITTER),
    //     the same jittered-sampling trick the per-blade lattice above uses;
    //   * each tuft is rotated by its own angle, so its blades don't all fan
    //     out along the same handful of compass directions the slot grid hands
    //     them — without this every tuft is a copy of one stamp, and the eye
    //     picks the repeat straight out however far the roots are jittered;
    //   * each tuft gets its own overall scale, so they aren't all one plant
    //     repeated at one size.
    vec2  clumpIdx = floor(gi / CLUMP_SIDE);
    vec2  hci      = mod(clumpIdx, 2048.0);
    vec2  clumpRnd = hash22(hci + seed + 31.7);
    vec2  clumpRnd2 = hash22(hci * 1.37 + seed + 91.3);

    float tuftRot = clumpRnd2.x * 6.2831853;
    mat2  tuftM = mat2(cos(tuftRot), sin(tuftRot), -sin(tuftRot), cos(tuftRot));
    float tuftScale = mix(1.0, CLUMP_SIZE_MIN + (CLUMP_SIZE_MAX - CLUMP_SIZE_MIN) * clumpRnd2.y, clump);

    vec2  fromMid  = mod(gi, CLUMP_SIDE) - (CLUMP_SIDE - 1.0) * 0.5;
    float rim      = clamp(length(fromMid) / ((CLUMP_SIDE - 1.0) * 0.7071), 0.0, 1.0);
    vec2  rootPos  = (clumpIdx * CLUMP_SIDE + (CLUMP_SIDE - 1.0) * 0.5) * spacing
                   + (clumpRnd - 0.5) * spacing * CLUMP_SIDE * CLUMP_ROOT_JITTER;
    cellPos = mix(cellPos, rootPos + tuftM * (cellPos - rootPos) * CLUMP_TIGHT * tuftScale, clump);

    float dist = distance(cellPos, camXZ);
    float pick = hash11(dot(hgi + seed, vec2(12.9898, 78.233)));

    // Bulge the circular range/LOD boundaries this instance is tested against
    // (see the boundaryJitter uniform's doc comment) by a smooth per-position
    // noise, BEFORE any of those tests run — every one of them below uses
    // distJ instead of the true dist, so the population cutoff (the alive
    // test, LOD dither included) and the visual shrink (rangeFade/edgeFade)
    // always agree about where this instance's edge actually is. seed shifts
    // the noise per volume, same as every other hash in this file, so two
    // overlapping volumes don't bulge in lockstep.
    float distJ = dist;
    if (boundaryJitter > 0.0) {
        float n = valueNoise2(cellPos * BOUNDARY_JITTER_FREQ + seed * 0.37) * 2.0 - 1.0;
        // Clamped at 0, not just left to go negative: a kind's innermost tier
        // has lodStart == 0, and the alive test below requires distJ >=
        // lodStart. Without the clamp, a negative jitter sample on an
        // instance already standing right next to the camera (true dist near
        // 0) could push distJ below that 0 floor and kill it — a "hole" in
        // the scatter at the player's own feet, the opposite of what jitter
        // is supposed to do. dist is a real Euclidean distance and can never
        // itself be negative, so 0 is the correct floor, not an arbitrary one.
        distJ = max(distJ + n * boundaryJitter, 0.0);
    }

    // lodStart is where THIS tier's instances actually start appearing —
    // lodMin ITSELF minus a grow-in band, not lodMin. The band is sized to
    // match the PREVIOUS tier's own fade-out band at that exact same
    // boundary (both use lodFadeFraction of the span on their side of it —
    // see the lodFadeFraction uniform's own doc comment), so the two bands
    // sit on top of each other instead of end-to-end: the previous tier is
    // fading out over the same stretch this tier is fading in over, and
    // between them coverage never drops to nothing. Getting this wrong —
    // starting the grow-in band AT lodMin instead of BEFORE it — is what
    // used to leave a bald ring at every LOD boundary: both tiers hit zero
    // at the one shared point instead of one picking up as the other let go.
    float lodStart = lodMin - lodMin * lodFadeFraction;

    // LOD crossfade envelopes (0 = fully faded, 1 = fully present) for this
    // tier's inner and outer edges, WHEN those edges are LOD splits rather
    // than the volume's true shape-independent outer edge. lodGrowIn is
    // this tier's own inner boundary (a no-op — always 1 — on a kind's
    // innermost/only tier, where lodMin is 0). lodShrinkOut is this tier's
    // OWN outer boundary, but only when outerIsLOD says that boundary is an
    // LOD split (Near->Mid, Mid->Far) rather than the volume's true Range
    // edge — the true edge uses rangeFade below instead, a DIFFERENT kind of
    // fade for a reason explained where it's applied.
    //
    // These are folded into the ALIVE test below as a survival PROBABILITY —
    // NOT into a late size shrink the way rangeFade/edgeFade work — because a
    // crossfade between two DIFFERENT tiers can't use a size blend without a
    // visible gap: fade0 + fade1 sum to exactly 1 by construction (a
    // smoothstep and its complement), so scaling BOTH tiers' blade height by
    // their share nets "one tier's worth" of height split across two tiers
    // whose BASE DENSITY differs (Mid/Far are deliberately sparser, cheaper
    // lattices than Near) — the result reads as thinner ground coverage than
    // either tier alone, a visible bald band right at the seam. Dithering
    // instead keeps every surviving instance at FULL SIZE and only varies
    // which fraction of each tier's own already-full-density lattice is
    // present, which is what an LOD crossfade needs to look continuous. The
    // volume's true outer edge has no second tier picking up past it, so
    // shrinking into nothing is the right look there and stays a shrink.
    float lodGrowIn = lodMin > 0.0
        ? clamp(smoothstep(lodStart, lodMin, distJ), 0.0, 1.0)
        : 1.0;
    float lodShrinkOut = (outerIsLOD != 0)
        ? clamp(1.0 - smoothstep(range * (1.0 - lodFadeFraction), range, distJ), 0.0, 1.0)
        : 1.0;

    // Cheap rejects first (no texture fetch); the height/coverage sample —
    // needed for both the final survival test and the instance's own drop
    // height — only happens for candidates that pass. castRange is `range`
    // in the color pass, so it rejects nothing there. distJ >= lodStart
    // admits this tier's own grow-in band, which overlaps the end of an
    // INNER tier's own [0, lodMin] pass rather than picking up exactly where
    // it leaves off — lodMin is 0 on a kind's innermost/only tier, so
    // lodStart is 0 too and this is a no-op there.
    bool alive = pointInVolume(cellPos) && (distJ >= lodStart) && (distJ <= range) && (distJ <= castRange);
    vec2 hc = vec2(0.0);
    if (alive) {
        hc = sampleHeightCoverage(cellPos);
        // lodGrowIn/lodShrinkOut fold into the SAME per-cell `pick` density
        // already gates (rather than a second, independent hash) so one
        // random roll decides density AND LOD survival consistently: an
        // instance already kept by a lenient density roll is also the one
        // most likely to survive a nearby LOD dither, instead of the two
        // rolling independently and compounding into extra-sparse coverage
        // right where a crossfade already thins things out.
        alive = pick <= density * hc.y * lodGrowIn * lodShrinkOut;
    }

    if (!alive) {
        return false;
    }

    // Edge fade: how close this survivor sits to the volume's OWN
    // boundary, as a 0..1 grow-in factor. Computed only now, after both cheap
    // rejects and the density thinning above, so its cost lands on instances
    // that are actually going to draw — see volEdgeDist's doc comment.
    float edgeFade = 1.0;
    if (edgeFadeDistance > 0.0) {
        edgeFade = clamp(volEdgeDist(cellPos) / edgeFadeDistance, 0.0, 1.0);
    }

    float baseY = hc.x;
    vec3 basePt = vec3(cellPos.x, baseY, cellPos.y);
    bBasePt = basePt;

    // Blade facing. Unclumped this is a free random yaw. Clumped, the blade is
    // turned so its flat ribbon lies in the plane it bows through — its width
    // axis perpendicular to the outward splay direction, hence the quarter
    // turn — with slop left in so a tuft doesn't read as a perfect pinwheel.
    // Blended as a shortest-way angular delta, so dragging the slider sweeps
    // blades round smoothly instead of spinning them the long way.
    // splay is taken through the tuft's own rotation, so a blade bows outward
    // along the direction its plant actually sits at rather than along the
    // slot grid's axes.
    float baseYaw = rnd.x * 6.2831853;
    vec2  fanDir  = tuftM * fromMid;
    vec2  splay   = length(fanDir) > 1e-5 ? normalize(fanDir) : vec2(cos(baseYaw), sin(baseYaw));
    float radYaw  = atan(splay.y, splay.x) + 1.5707963 + (rnd.y - 0.5) * 0.7;
    float dYaw    = mod(radYaw - baseYaw + 3.14159265, 6.2831853) - 3.14159265;
    float yaw     = baseYaw + dYaw * clump;
    float ca = cos(yaw), sa = sin(yaw);
    float sizeVar = 0.7 + 0.6 * rnd.y;

    // Shrink over a band toward the far edge of the camera ring, sized by
    // RangeFadeFraction, so the boundary dissolves gradually instead of
    // reading as a hard circle — 0 disables it and falls back exactly to the
    // hard `dist <= range` cutoff the alive test already gives on its own
    // (fadeStart == range collapses smoothstep's rising edge to nothing).
    // Composed with edgeFade (the volume's own boundary fade, if
    // enabled) into the same shrink, so a corner near both the ring and the
    // polygon edge shrinks by whichever is stronger rather than double-fading.
    //
    // ONLY applies when this tier's outer edge is the volume's actual Range
    // (outerIsLOD == 0) — an LOD split's own outer edge already dithered
    // itself out via lodShrinkOut above, in the alive test, so shrinking it
    // here too would fade a population that's already partly gone, on top of
    // the fade the NEXT tier's lodGrowIn is doing to fill back in, right back
    // into the same size-blend gap this whole scheme exists to avoid.
    float rangeFrac = (outerIsLOD == 0) ? clamp(rangeFadeFraction, 0.0, 1.0) : 0.0;
    // smoothstep's edge0==edge1 case is undefined by the GLSL spec (a 0/0 in
    // its interpolation), so the disabled case is branched out explicitly
    // rather than trusting every driver to resolve range==fadeStart the same
    // way — 1.0 here reproduces the old hard `dist <= range` cutoff exactly.
    float rangeFade = rangeFrac > 0.0
        ? clamp(1.0 - smoothstep(range * (1.0 - rangeFrac), range, distJ), 0.0, 1.0)
        : 1.0;

    float fade = rangeFade * edgeFade;

    vec3 off;
    if (shapeKind == 1) {
        // Rock: per-instance shader warp on top of the shared template — a
        // low-frequency wobble keyed by direction (so it stays smooth across
        // a triangle) plus this instance's lattice hash, so every scattered
        // rock gets a unique lumpy silhouette without a unique mesh. This is
        // the "detail from shaders, not geometry" trick applied to shape.
        vec3 dir = length(local) > 1e-5 ? normalize(local) : vec3(0.0, 1.0, 0.0);
        float warp = 0.0;
        warp += sin(dir.x * 3.1 + hgi.x * 0.7 + seed.x) * 0.14;
        warp += sin(dir.y * 2.3 + dir.z * 3.7 + hgi.y * 0.5 + seed.y) * 0.10;
        warp += hash11(dot(hgi + seed, vec2(41.3, 7.1))) * 0.10 - 0.05;

        vec3 p = local * (1.0 + warp) * rockScale * sizeVar;
        off = vec3(p.x * ca - p.z * sa, p.y, p.x * sa + p.z * ca);
        bNormal = normalize(vec3(nrm.x * ca - nrm.z * sa, nrm.y,
                                 nrm.x * sa + nrm.z * ca));
        bBaseColor = tintColor * (0.8 + 0.4 * rnd.x);
        // Rock and moss get their occlusion from the noise field in
        // biome_frag.glsl instead — it varies over the surface, which a single
        // per-vertex factor could not.
        bAO = 1.0;
    } else {
        // Foliage: grass (0), wildflower (2) and cattail (3) share the
        // tapered-stem transform. part<=1 is the stem (bend fraction t);
        // part>=2 is a head (bloom/seed-head), which bends with the stem's
        // own tip so it doesn't detach from it in the wind.
        // Bands are checked outermost-first: a leaf's tag (>=5) would
        // otherwise also satisfy the head and center tests below it.
        float part = floor(param);
        bool isLeaf = part >= 5.0;              // wildflower basal leaf
        bool isCenter = !isLeaf && part >= 4.0; // wildflower bloom center
        bool isHead = !isLeaf && part >= 2.0;   // bloom / cattail seed head

        // Parts switched off by their toggle collapse to a single point, so
        // their triangles degenerate to zero area and never rasterize. t is
        // forced to 0 alongside, which is what actually keeps them collapsed:
        // every displacement below (wind sway, clump arc) scales with t, and a
        // LEAF's vertices each carry a different t — so leaving t alone would
        // let the wind pull a switched-off leaf back open into visible
        // slivers. (The bloom center got away without this only because all
        // its vertices share one aParam and so displace identically.)
        bool collapsed = (isCenter && hasCenter == 0) || (isLeaf && hasLeaves == 0);

        // Wildflower/cattail head anchor, as a fraction of the stem's own
        // height — MUST match wildflowerHeadAnchor/cattailHeadAnchor in
        // grass.go. Hoisted above t so a head's wind sway (below) uses the
        // same bend fraction the stem's own topmost vertex does.
        float headAnchor = (shapeKind == 2) ? 0.84 : 0.95;

        // A leaf carries its height in the fraction, like the stem does — but
        // can't just fract(), since the stem's tip is aParam == 1.0 exactly.
        // A head uses headAnchor here rather than a flat 1.0: the stem is
        // deliberately never rendered past that fraction (the head swallows
        // the tip — see grass.go), so headAnchor IS "fully extended" for this
        // template. Using 1.0 over-swayed the head past where the stem's own
        // tip vertex sways (sway grows with t*t, so even cattail's ~5% gap at
        // anchor 0.95 shows up), tearing head from stem in the wind — most
        // visible on an elongated spike bloom, whose narrow base has no wide
        // petal disc to hide the seam under.
        float t = collapsed ? 0.0 : (isHead ? headAnchor : (isLeaf ? param - 5.0 : param));
        // Tuft grading (all no-ops at clump==0): the middle blade grows tall
        // and stays upright, the rim ones stay short and bow hard outward.
        float h = bladeHeight * sizeVar * tuftScale * mix(1.0, mix(CLUMP_TALL, CLUMP_SHORT, rim), clump);
        float arc = clump * CLUMP_ARC * rim; // outward tip lean, in blade heights
        // Blades of one tuft sway in step, so a plant moves as a plant rather
        // than as nine independently shivering blades.
        float phase = mix(rnd.x, clumpRnd.x, clump) * 6.2831 + time * 1.8;
        float sway = (sin(phase) + 0.3 * sin(phase * 2.3)) * 0.18 * t * t;

        // The bloom center's proportions relative to headScale, and how far
        // it rides above the bloom's own mid-plane (as a fraction of the
        // bloom's half-height, so it keeps peeking out at any bloomAspect).
        const vec3 CENTER_ASPECT = vec3(0.34, 0.30, 0.34);
        const float CENTER_LIFT = 0.45;

        vec3 p;
        if (collapsed) {
            p = vec3(0.0);
        } else if (isLeaf) {
            // Basal leaves are authored in fractions of the stem's own height
            // on every axis, radial ones included (see buildWildflowerLeaves),
            // so one uniform scale by h keeps a short flower's leaves in the
            // same proportion as a tall one's, with leafScale on top as the
            // authored-size multiplier. bladeWidth deliberately doesn't enter:
            // a leaf is not a thinner or fatter copy of the stem.
            p = local * h * leafScale;
        } else if (isHead) {
            // The head's local geometry is centered at the origin (radius
            // only, no baked translation — see buildBlobMesh's callers in
            // grass.go), so headScale grows/shrinks it around a fixed anchor
            // point on the stem instead of also moving that anchor (headAnchor,
            // hoisted above with t — MUST match wildflowerHeadAnchor/
            // cattailHeadAnchor in grass.go).
            vec3 aspect = isCenter ? CENTER_ASPECT : bloomAspect;
            float lift = isCenter ? headScale * bloomAspect.y * CENTER_LIFT * sizeVar : 0.0;
            vec3 hp = local * headScale * sizeVar * aspect;
            p = vec3(hp.x, hp.y + headAnchor * h + lift, hp.z);
        } else {
            p = vec3(local.x * bladeWidth * sizeVar, local.y * h, local.z * bladeWidth * sizeVar);
        }

        // Bow the blade outward from its root, strongest toward the tip — the
        // curve that makes a tuft read as a growing plant rather than a bundle
        // of upright spikes. Local -z is the axis yaw maps onto the outward
        // splay direction (yaw being the splay angle plus a quarter turn, see
        // above), and the y term foreshortens the blade by roughly the length
        // the lean consumes, so it curves over instead of stretching. Applied
        // to heads too (t==1 there) so a head would ride the tip rather than
        // detach — moot while only Grass clumps, but correct if that changes.
        p.z -= arc * h * t * t;
        p.y -= p.y * CLUMP_DROOP * arc * t * t;

        off = vec3(p.x * ca - p.z * sa, p.y, p.x * sa + p.z * ca);
        vec2 windDir = normalize(vec2(0.8, 0.6));
        off.xz += windDir * sway * h;

        vec3 base;
        if (shapeKind == 0) {
            // Grass: root->tip gradient of the paint color directly.
            vec3 root = tintColor * 0.55;
            vec3 tip = tintColor;
            base = mix(root, tip, t) * (0.85 + 0.3 * rnd.y);
        } else if (shapeKind == 2) {
            // Wildflower: fixed green stem; petals either a per-instance hue
            // from the palette (tinted by the paint color, which defaults
            // neutral so the palette shows through) or the paint color used
            // directly when bloomSolid is set; separate center color on top.
            vec3 stemCol = vec3(0.30, 0.50, 0.22);
            vec3 leafCol = vec3(0.24, 0.44, 0.18); // a shade deeper than the stem, so leaves read as separate
            float hue = hash11(dot(hgi + seed, vec2(53.1, 91.7)));
            vec3 petalCol = (bloomSolid != 0) ? tintColor : flowerPalette(hue) * tintColor;
            vec3 partCol = isLeaf ? leafCol : (isCenter ? centerColor : (isHead ? petalCol : stemCol));
            base = partCol * (0.85 + 0.3 * rnd.y);
        } else {
            // Cattail: fixed reed-green stem; brown seed head tinted by the
            // paint color (default neutral).
            vec3 stemCol = vec3(0.33, 0.42, 0.20);
            vec3 headCol = vec3(0.40, 0.27, 0.14) * tintColor;
            base = (part <= 1.0 ? stemCol : headCol) * (0.85 + 0.3 * rnd.y);
        }
        bBaseColor = base;
        // Foliage has no meaningful surface normal — a blade is a flat ribbon
        // whose facet normal points wherever its random yaw happened to land,
        // which would light neighbouring blades in wildly different shades.
        // Up is what a sward of them collectively faces, and is what the pass
        // has always used.
        bNormal = vec3(0.0, 1.0, 0.0);
        // See BIOME_ROOT_AO/BIOME_TUFT_AO: dark at the root, darker still in
        // the middle of a clump. Both terms are 1.0 at a tip, and the tuft term
        // folds away entirely at clump==0.
        bAO = mix(BIOME_ROOT_AO, 1.0, t) * mix(1.0, BIOME_TUFT_AO, clump * (1.0 - rim));
    }

    bWorldPos = basePt + off * fade;
    return true;
}
