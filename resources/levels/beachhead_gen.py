#!/usr/bin/env python3
"""Generate beachhead.yaml — a beach between two cliffs, a concrete bunker line
with an open gate under a walkway, and rolling grass behind it.

Written for the same reason canyon_gen.py was: the tedious parts of a hand-
authored level are the ones a script never gets wrong.

  * planar subdivision — every sector's boundary ring is rebuilt to include
    each named point lying on it, so two abutting sectors share IDENTICAL
    edges. InferPortals pairs edges by exact reverse match, and a boundary
    split on one side but not the other silently stays a wall.
  * CCW winding, which the engine requires seen from above.
  * per-vertex floor offsets sampled from ONE height field, so neighbouring
    sectors agree exactly at every shared point and the terrain has no cracks.

Three things about this level are worth knowing before reading the numbers.

WALLS HAVE THICKNESS. A sector engine's wall is a boundary, which is a plane
with no depth. So nothing here is built out of one: a bunker wall is the SOLID
GAP between two sectors — the room's outline and the roof's outline are 2.0
apart, and the space between them is not a sector at all. The room's own wall
draws the inner face; the roof slab, whose floor sits 4 above the ground
outside, draws the outer face as the portal riser between the two. Same for the
walkway over the gate: its deck is a floor at 10.0 with a `skirt` down to the
ceiling under it, so the bridge is a slab a metre thick rather than a sheet.

CLIFFS ARE TERRAIN, NOT WALLS. Making them walls would mean matching every
adjacent sector's ceiling height to the rim, which one ceiling plane per sector
cannot express at a corner where a cliff face meets a bunker face. They are
instead ground steeper than the 45 degrees ResolveSlopeCollision walks up —
about 70 — which the player cannot climb and which needs no special case
anywhere.

THE OUTER PERIMETER IS A HIDDEN WALL. An edge shared with nothing stays a wall
and is drawn from its floor to its ceiling, which for open ground is the sky.
The world's rim marks those edges `hidden`, so they still stop the player and
draw nothing.
"""

import math
import sys
from collections import OrderedDict

EPS = 1e-6

# --------------------------------------------------------------- dimensions --

X_EDGE = 110.0          # world half-width
CORRIDOR = 46.0         # cliff foot: the beach runs between -46 and 46
CLIFF_RIM = 52.0        # where the cliff face tops out; 6 units of ~70deg rock

PLAT = 6.0              # the bunker platform, and the top of the beach slope
ROOF = 10.0             # roof deck / walkway deck
CEIL_IN = 9.6           # bunker interior ceiling
CEIL_DOOR = 8.6         # doorway ceiling, so a lintel shows over every opening
CEIL_GATE = 9.0         # the walkway's underside, over the gate passage
FLUSH = 0.02            # a roof's own wall height: present, and invisible
SKY = 80.0              # ceiling height for open ground; nothing draws at it

WALL = 2.0              # bunker wall thickness — the whole point of the level
BUNK_S, BUNK_N = -40.0, -8.0    # the bunker band in Z
GATE = 8.0                      # the gate opening runs X[-8, 8]

WATER_Y = 0.0           # sea level

# Grid lines. Different bands use different column sets: the sea needs no
# detail, and the subdivision below stitches the coarse rows to the fine ones.
COLS = [-110.0, -70.0, -52.0, -46.0, -30.0, -14.0, 0.0, 14.0, 30.0, 46.0, 52.0, 70.0, 110.0]
COLS_SEA = [-110.0, -52.0, -46.0, 0.0, 46.0, 52.0, 110.0]

ROWS_SEA = [-190.0, -140.0, -112.0]
ROWS_BEACH = [-112.0, -92.0, -72.0, -56.0, -40.0]
ROWS_HILL = [-8.0, 12.0, 34.0, 56.0, 78.0, 89.0, 100.0]

# The hills get two extra columns the beach does not need. The world's side rim
# rises over the outer 22 units of X, and a cell 40 wide would average that into
# a 32-degree ramp the player can walk up — straight into the hidden wall behind
# it. Splitting at 90 keeps the ridge inside one cell and steeper than anything
# ResolveSlopeCollision admits.
COLS_HILL = sorted(COLS + [-90.0, 90.0])

Z_EDGE_S, Z_EDGE_N = ROWS_SEA[0], ROWS_HILL[-1]

# ------------------------------------------------------------ the height field --
#
# One function, sampled at every sector vertex. Two sectors meeting at a point
# get the same number there by construction, which is the whole reason the
# terrain has no seams.


def clamp01(t):
    return 0.0 if t < 0.0 else (1.0 if t > 1.0 else t)


def smoothstep(t):
    t = clamp01(t)
    return t * t * (3.0 - 2.0 * t)


def lerp(a, b, t):
    return a + (b - a) * t


def base(z):
    """The profile along Z: seabed, surf, beach, platform.

    The waterline is where this crosses 0, at about z = -99. Everything from
    the bunker line north is flat at PLAT, which is what lets the bunker sit on
    ground with no slope under it."""
    if z <= -140.0:
        return -8.5
    if z <= -112.0:
        return lerp(-8.5, -2.2, smoothstep((z + 140.0) / 28.0))
    if z <= -92.0:
        return lerp(-2.2, 1.0, (z + 112.0) / 20.0)
    if z <= BUNK_S:
        return lerp(1.0, PLAT, smoothstep((z + 92.0) / 52.0))
    return PLAT


def wobble(x, z):
    """Small variation over the sea floor and the beach, so neither is a plane."""
    return (0.46 * math.sin(x * 0.062 + 1.7) * math.cos(z * 0.048)
            + 0.28 * math.sin(x * 0.021 - z * 0.037 + 0.6)
            + 0.13 * math.sin(x * 0.150 + z * 0.110))


def hills(x, z):
    """The rolling ground behind the bunkers, plus a gentle overall rise."""
    return (3.4 * math.sin((x + 22.0) * 0.040) * math.cos((z + 6.0) * 0.047)
            + 1.7 * math.sin((x - 34.0) * 0.075 + z * 0.026 + 0.9)
            + 0.9 * math.cos((x + 60.0) * 0.115 - z * 0.052)
            + 0.030 * (z - BUNK_N))


def rim_ridge(x, z):
    """The hills rise into a ridge at the world's north and side edges.

    The map is bounded by hidden walls — there is nothing to draw at its rim and
    nothing beyond it — and walking into one across open grass reads as a bug.
    A ridge steeper than the slope resolver walks up turns the same boundary
    into terrain that simply cannot be crossed. max rather than a sum, so the
    two edges meeting in a corner make one hill rather than one twice as tall."""
    return 22.0 * max(smoothstep((z - 78.0) / 22.0),
                      smoothstep((abs(x) - 88.0) / 22.0))


def ground(x, z):
    """The corridor floor: beach south of the bunkers, hills north of them, and
    dead flat between, where the bunkers stand."""
    y = base(z)
    if z < BUNK_S:
        y += smoothstep((BUNK_S - z) / 16.0) * wobble(x, z)
    elif z > BUNK_N:
        t = smoothstep((z - BUNK_N) / 26.0)
        y += t * (hills(x, z) + 0.35 * wobble(x, z) + rim_ridge(x, z))
    return y


def cliff_rim(x, z):
    """The rim height, and the headland rolling away behind it."""
    y = 23.0 + 5.0 * math.sin(z * 0.031 + (0.4 if x > 0 else 2.3)) \
        + 2.5 * math.cos(z * 0.017 - 1.1)
    ax = abs(x)
    if ax > CLIFF_RIM:
        y += smoothstep((ax - CLIFF_RIM) / 22.0) * (
            2.4 * math.sin(ax * 0.05 + z * 0.03) + 1.5 * math.cos(z * 0.06 - ax * 0.02))
    return y


def terrain(x, z):
    """The height of open ground anywhere in the world.

    Outside the corridor the cliff is blended in over 6 units of X — about 70
    degrees, well past the 45 the slope resolver walks up — and faded out over
    Z north of the bunker line, so the headlands melt into the hills instead of
    ending in a face nobody can see the back of."""
    g = ground(x, z)
    ax = abs(x)
    if ax <= CORRIDOR:
        return g
    wz = 1.0 - smoothstep((z - BUNK_N) / 28.0)
    if wz <= 0.0:
        return g
    wx = smoothstep((ax - CORRIDOR) / (CLIFF_RIM - CORRIDOR))
    return g + wz * wx * (cliff_rim(x, z) - g)


# ------------------------------------------------------------------ points ----

points = OrderedDict()
_by_xz = {}


def P(x, z):
    key = (round(x, 4), round(z, 4))
    if key in _by_xz:
        return _by_xz[key]
    name = "p%d" % len(points)
    points[name] = key
    _by_xz[key] = name
    return name


# ----------------------------------------------------------------- sectors ----

sectors = []


def rect(sid, x0, z0, x1, z1, floor, ceil, ceil_enabled=True, layer=0,
         floor_tex="concrete", ceil_tex="concrete", wall_tex="concrete",
         skirt=None, skirt_tex=None, detail=None, smooth=False,
         pin=None, hidden=None, gaptex=None, nocollide=None):
    """One rectangular sector. `floor` is either a number or a function of
    (x, z) sampled per vertex."""
    for (x, z) in [(x0, z0), (x1, z0), (x1, z1), (x0, z1)]:
        P(x, z)
    sectors.append(dict(id=sid, x0=x0, z0=z0, x1=x1, z1=z1, floor=floor,
                        ceil=ceil, ceil_enabled=ceil_enabled, layer=layer,
                        floor_tex=floor_tex, ceil_tex=ceil_tex,
                        wall_tex=wall_tex, skirt=skirt, skirt_tex=skirt_tex,
                        detail=detail, smooth=smooth, pin=pin, hidden=hidden,
                        gaptex=gaptex, nocollide=nocollide))


def ring_for(s):
    """The sector's boundary, with every named point that lies on it inserted.

    This is the whole reason abutting sectors share edges rather than merely
    touching, and it is what lets a coarse row of sea cells sit against a finer
    row of beach cells with no T-junction."""
    x0, z0, x1, z1 = s["x0"], s["z0"], s["x1"], s["z1"]
    corners = [(x0, z0), (x1, z0), (x1, z1), (x0, z1)]   # CCW in (X, Z)
    out = []
    for k in range(4):
        ax, az = corners[k]
        bx, bz = corners[(k + 1) % 4]
        out.append(P(ax, az))
        on = []
        for name, (px, pz) in points.items():
            if (px, pz) == (ax, az) or (px, pz) == (bx, bz):
                continue
            if abs(ax - bx) < EPS:
                if abs(px - ax) < EPS and min(az, bz) - EPS < pz < max(az, bz) + EPS:
                    on.append((abs(pz - az), name))
            elif abs(az - bz) < EPS:
                if abs(pz - az) < EPS and min(ax, bx) - EPS < px < max(ax, bx) + EPS:
                    on.append((abs(px - ax), name))
        on.sort()
        out.extend(n for _, n in on)
    return out


def floor_heights(s, ring):
    f = s["floor"]
    if callable(f):
        return [f(*points[n]) for n in ring]
    return [f] * len(ring)


# ------------------------------------------------------------------ terrain ---
#
# The world's rim: an edge nothing else walks stays a wall, and open ground's
# ceiling is the sky, so without this the map is ringed by an 80-unit slab.


def on_rim(mx, mz):
    return (abs(abs(mx) - X_EDGE) < EPS
            or abs(mz - Z_EDGE_S) < EPS or abs(mz - Z_EDGE_N) < EPS)


def bunker_face(mx, mz):
    """True on the bunker complex's outer footprint, where the riser between
    open ground and the roof slab draws the block's concrete face. Without a
    gap texture the riser would inherit the sand or grass under it."""
    if BUNK_S - EPS < mz < BUNK_N + EPS and abs(abs(mx) - CORRIDOR) < EPS:
        return "concrete"
    if abs(mz - BUNK_S) < EPS or abs(mz - BUNK_N) < EPS:
        if abs(mx) <= CORRIDOR + EPS:
            return "concrete"
    return None


CLIFF_DETAIL = "cliff"


def terrain_row(z0, z1, cols, band):
    """One row of open-ground cells, textured by where it sits."""
    for i in range(len(cols) - 1):
        x0, x1 = cols[i], cols[i + 1]
        mx = (x0 + x1) / 2.0
        ax = abs(mx)
        cliff = CORRIDOR - EPS < ax < CLIFF_RIM + EPS and (z0 + z1) / 2.0 < BUNK_N + 20.0
        if band == "sea":
            tex = "sand"
        elif band == "beach":
            tex = "sand"
        elif band == "hill":
            tex = "grass"
        else:
            tex = "concrete"
        if ax > CORRIDOR + EPS:
            # The cliff face and the headland behind it. Grass takes over on the
            # tops north of the beach, where the rock has already faded out.
            tex = "rock" if (cliff or band != "hill") else "grass"
        rect("t_%d_%d_%s" % (int(round(x0)), int(round(z0)), band),
             x0, z0, x1, z1,
             floor=terrain, ceil=SKY, ceil_enabled=False,
             floor_tex=tex, wall_tex="rock", smooth=(band != "sea"),
             detail=CLIFF_DETAIL if cliff else None,
             hidden=on_rim, gaptex=bunker_face)


for i in range(len(ROWS_SEA) - 1):
    terrain_row(ROWS_SEA[i], ROWS_SEA[i + 1], COLS_SEA, "sea")
for i in range(len(ROWS_BEACH) - 1):
    terrain_row(ROWS_BEACH[i], ROWS_BEACH[i + 1], COLS, "beach")
for i in range(len(ROWS_HILL) - 1):
    terrain_row(ROWS_HILL[i], ROWS_HILL[i + 1], COLS_HILL, "hill")

# The bunker band's flanks: everything outside the corridor beside the blocks.
for (x0, x1) in [(-110.0, -70.0), (-70.0, -52.0), (-52.0, -46.0),
                 (46.0, 52.0), (52.0, 70.0), (70.0, 110.0)]:
    mx = (x0 + x1) / 2.0
    cliff = CORRIDOR - EPS < abs(mx) < CLIFF_RIM + EPS
    rect("t_%d_flank" % int(round(x0)), x0, BUNK_S, x1, BUNK_N,
         floor=terrain, ceil=SKY, ceil_enabled=False,
         floor_tex="rock", wall_tex="rock",
         detail=CLIFF_DETAIL if cliff else None,
         hidden=on_rim, gaptex=bunker_face)

# ------------------------------------------------------------- the bunkers ----
#
# One block is described; the other is its mirror. Inside a block, nothing
# touches anything else: every room is inset WALL from the footprint and from
# its neighbours, and the solid between them is what the player walks into.

IN_W, IN_E = -CORRIDOR + WALL, -GATE - WALL      # interior span in X: [-44, -10]
IN_S, IN_N = BUNK_S + WALL, BUNK_N - WALL        # interior span in Z: [-38, -10]
MID_W, MID_E = -30.0, -28.0                      # the internal wall between the rooms
RAMP_S, RAMP_N = -20.0, BUNK_N                   # the ramp trench cut into the north face
RAMP_W, RAMP_E = IN_W, MID_W


def ramp_floor(x, z):
    """6.0 at the north face, 10.0 where it meets the roof: 4 over 12, or 18
    degrees, which is a walk rather than a climb."""
    return PLAT + (ROOF - PLAT) * (RAMP_N - z) / (RAMP_N - RAMP_S)


west = []


def wrect(sid, x0, z0, x1, z1, **kw):
    west.append((sid, x0, z0, x1, z1, kw))


# Rooms. Their own outlines are the walls' INNER faces; the outer faces are the
# roof's risers, WALL away.
wrect("room_w", IN_W, IN_S, MID_W, -22.0,
      floor=PLAT, ceil=CEIL_IN, floor_tex="stone_floor")
wrect("room_e", MID_E, IN_S, IN_E, IN_N,
      floor=PLAT, ceil=CEIL_IN, floor_tex="stone_floor")

# The ramp to the roof: a trench in the block's north face, open to the sky, so
# there is no hole to cut in the roof and no shaft to light.
wrect("ramp", RAMP_W, RAMP_S, RAMP_E, RAMP_N,
      floor=ramp_floor, ceil=12.0, ceil_enabled=False, floor_tex="concrete")

# Doorways. Each is its own short sector through the thickness of a wall, which
# is the only way a 2-unit-thick wall has a way through it at all.
wrect("door_s", -40.0, BUNK_S, -36.0, IN_S,
      floor=PLAT, ceil=CEIL_DOOR, floor_tex="concrete")     # beach -> west room
wrect("door_mid", MID_W, -32.0, MID_E, -28.0,
      floor=PLAT, ceil=CEIL_DOOR, floor_tex="concrete")     # west room -> east room
wrect("door_gate", IN_E, -26.0, -GATE, -22.0,
      floor=PLAT, ceil=CEIL_DOOR, floor_tex="concrete")     # east room -> gate passage
wrect("door_n", -22.0, IN_N, -18.0, BUNK_N,
      floor=PLAT, ceil=CEIL_DOOR, floor_tex="concrete")     # east room -> the hills

# The roof, in three pieces: the footprint minus the ramp trench. Its skirt
# closes the slab between a room's ceiling and this floor — which is what draws
# the lintel over each doorway, seen from outside.
wrect("roof_w", -CORRIDOR, BUNK_S, IN_W, BUNK_N,
      floor=ROOF, ceil=ROOF + FLUSH, ceil_enabled=False,
      skirt="ceiling", skirt_tex="concrete", nocollide=True)
wrect("roof_s", IN_W, BUNK_S, RAMP_E, RAMP_S,
      floor=ROOF, ceil=ROOF + FLUSH, ceil_enabled=False,
      skirt="ceiling", skirt_tex="concrete", nocollide=True)
wrect("roof_e", MID_W, BUNK_S, -GATE, BUNK_N,
      floor=ROOF, ceil=ROOF + FLUSH, ceil_enabled=False,
      skirt="ceiling", skirt_tex="concrete", nocollide=True)

for sid, x0, z0, x1, z1, kw in west:
    layer = 1 if sid.startswith("roof") else 0
    rect("w_" + sid, x0, z0, x1, z1, layer=layer, **kw)
    # The mirror. Reflecting X reverses the winding, so the corners are swapped
    # back into CCW by giving rect the mirrored span in ascending order.
    mkw = dict(kw)
    if callable(kw.get("floor")):
        mkw["floor"] = (lambda f: (lambda x, z: f(-x, z)))(kw["floor"])
    rect("e_" + sid, -x1, z0, -x0, z1, layer=layer, **mkw)

# ---------------------------------------------------------- the gate passage --
#
# Three sectors, not one: only the middle stretch has the walkway over it, and
# a sector has exactly one ceiling.

rect("gate_s", -GATE, BUNK_S, GATE, -26.0,
     floor=PLAT, ceil=SKY, ceil_enabled=False, floor_tex="concrete")
rect("gate_m", -GATE, -26.0, GATE, -18.0,
     floor=PLAT, ceil=CEIL_GATE, floor_tex="concrete")
rect("gate_n", -GATE, -18.0, GATE, BUNK_N,
     floor=PLAT, ceil=SKY, ceil_enabled=False, floor_tex="concrete")

# The walkway. Its footprint is exactly gate_m's, so the skirt under its edges
# finds that ceiling and draws the deck's own thickness — a metre of concrete
# rather than a sheet of it.
rect("walkway", -GATE, -26.0, GATE, -18.0, layer=1,
     floor=ROOF, ceil=ROOF + FLUSH, ceil_enabled=False,
     skirt="ceiling", skirt_tex="concrete", nocollide=True)

# ------------------------------------------------------------------- biomes ---
#
# The sea is a Water-kind biome, and Water is authored as a lake: the surface
# sits `water_depth` BELOW the structural floor sampled at the polygon's AABB
# centre (BiomeWaterCentroidAndFloor). So the depth is not a free number — it is
# read off the floor at that one point, and the point has to be somewhere the
# ground is above sea level or the depth comes out negative and the engine
# substitutes its own.
#
# Hence the polygon runs from the deep water well up the beach, putting its
# centre on dry sand at about +0.4. Everything of it landward of the waterline
# is under ground that has already risen over it, which is exactly how a
# shoreline should be drawn: the water plane ends where the beach overtakes it,
# not at an authored edge.
#
# The height is the FLOOR'S INTERPOLATED value, not the height field's — inside
# a cell the floor is a triangle fan through its corner heights, and the two
# differ by the field's curvature. sampled_floor reproduces the fan, so the sea
# is at y=0 exactly rather than a few centimetres out.


def point_in_polygon(p, poly):
    """Ray-cast PIP, matching geom.PointInPolygon closely enough for a point
    chosen to be well inside one cell."""
    px, pz = p
    inside = False
    n = len(poly)
    for i in range(n):
        ax, az = poly[i]
        bx, bz = poly[(i + 1) % n]
        if (az > pz) != (bz > pz):
            t = (pz - az) / (bz - az)
            if px < ax + t * (bx - ax):
                inside = not inside
    return inside


def tri_bary(p, a, b, c):
    d = (b[1] - c[1]) * (a[0] - c[0]) + (c[0] - b[0]) * (a[1] - c[1])
    if abs(d) < 1e-12:
        return None
    wa = ((b[1] - c[1]) * (p[0] - c[0]) + (c[0] - b[0]) * (p[1] - c[1])) / d
    wb = ((c[1] - a[1]) * (p[0] - c[0]) + (a[0] - c[0]) * (p[1] - c[1])) / d
    wc = 1.0 - wa - wb
    if wa < -1e-9 or wb < -1e-9 or wc < -1e-9:
        return None
    return wa, wb, wc


def sampled_floor(cx, cz):
    """What InterpolateSurfaceHeight would return for the floor at (cx, cz).

    Asserts exactly one sector covers the point: with two, which one the engine
    picks is a ranking this script would have to reproduce, and the honest fix
    is to move the point rather than to guess."""
    hits = []
    for s in sectors:
        ring = ring_for(s)
        poly = [points[n] for n in ring]
        if not point_in_polygon((cx, cz), poly):
            continue
        hs = floor_heights(s, ring)
        for i in range(1, len(poly) - 1):
            w = tri_bary((cx, cz), poly[0], poly[i], poly[i + 1])
            if w:
                hits.append((s["id"], w[0] * hs[0] + w[1] * hs[i] + w[2] * hs[i + 1]))
                break
    if len(hits) != 1:
        raise SystemExit("water centre (%g, %g) is under %d sectors: %s"
                         % (cx, cz, len(hits), [h[0] for h in hits]))
    return hits[0][1]


# x is offset by one so the centre lands inside a cell rather than on the
# column line at 0, where point-in-polygon is a coin toss.
WATER_POLY = (-112.0, Z_EDGE_S, 110.0, 4.0)
WATER_C = ((WATER_POLY[0] + WATER_POLY[2]) / 2.0, (WATER_POLY[1] + WATER_POLY[3]) / 2.0)
water_depth = sampled_floor(*WATER_C) - WATER_Y
if water_depth <= 0:
    raise SystemExit("water centre is below sea level; depth would be %g" % water_depth)

biomes = []


def biome(bid, x0, z0, x1, z1, brush):
    biomes.append((bid, [(x0, z0), (x1, z0), (x1, z1), (x0, z1)], brush))


biome("sea", *WATER_POLY, brush=OrderedDict([
    ("kind", 5),                    # BiomeWater
    ("density", 1.0),
    ("water_depth", round(water_depth, 4)),
    ("water", OrderedDict([
        ("wave_amplitude", 0.14), ("wave_length", 7.0), ("wave_speed", 0.55),
        ("reflectivity", 0.35),
        ("reflect_color", OrderedDict([("r", 0.42), ("g", 0.55), ("b", 0.62)])),
    ])),
]))

biome("beach_sand", -CORRIDOR + 2, -108.0, CORRIDOR - 2, BUNK_S, OrderedDict([
    ("kind", 4),                    # BiomeSand — a dune patch, no scatter
    ("density", 1.0),
    ("color", OrderedDict([("r", 0.86), ("g", 0.79), ("b", 0.63)])),
    ("sand_height", 0.35),
    ("ground_uv_scale", OrderedDict([("x", 0.35), ("y", 0.35)])),
    ("range_fade_fraction", 0.25),
    ("edge_fade_distance", 6.0),
]))

GRASS = [
    ("kind", 0),                    # BiomeGrass
    ("density", 0.75),
    ("range", 55.0),
    ("color", OrderedDict([("r", 0.36), ("g", 0.52), ("b", 0.24)])),
    ("grass_height", 0.5),
    ("grass_width", 0.045),
    ("grass_clump", 0.55),
    ("ground_texture", "grass"),
    ("ground_amplitude", 0.09),
    ("ground_frequency", 0.22),
    ("ground_octaves", 3),
    ("ground_uv_scale", OrderedDict([("x", 0.3), ("y", 0.3)])),
    ("range_fade_fraction", 0.3),
    ("edge_fade_distance", 8.0),
    ("min_slope_angle", 0.0),
    ("max_slope_angle", 42.0),
]

biome("hills_near", -X_EDGE + 6, BUNK_N, X_EDGE - 6, 40.0, OrderedDict(GRASS))
biome("hills_far", -X_EDGE + 6, 40.0, X_EDGE - 6, Z_EDGE_N - 4, OrderedDict(
    GRASS[:3] + [("color", OrderedDict([("r", 0.33), ("g", 0.49), ("b", 0.22)]))] + GRASS[4:]))

biome("meadow", -34.0, 18.0, 26.0, 54.0, OrderedDict([
    ("kind", 2),                    # BiomeWildflower
    ("density", 0.28),
    ("range", 45.0),
    ("color", OrderedDict([("r", 0.82), ("g", 0.72), ("b", 0.30)])),
    ("grass_height", 0.42),
    ("grass_width", 0.030),
    ("head_scale", 0.17),
    ("bloom_center", True),
    ("stem_leaves", True),
    ("center_color", OrderedDict([("r", 0.95), ("g", 0.84), ("b", 0.30)])),
    ("ground_texture", "grass"),
    ("edge_fade_distance", 6.0),
]))

biome("headland_w", -X_EDGE + 6, -132.0, -CLIFF_RIM - 2, BUNK_N, OrderedDict([
    ("kind", 1),                    # BiomeRock — sparse boulders on the tops
    ("density", 0.30),
    ("range", 90.0),
    ("color", OrderedDict([("r", 0.52), ("g", 0.50), ("b", 0.47)])),
    ("rock_sides", 5),
    ("rock_scale", 0.55),
    ("ground_texture", "rock"),
    ("ground_amplitude", 0.14),
    ("max_slope_angle", 38.0),
]))
biome("headland_e", CLIFF_RIM + 2, -132.0, X_EDGE - 6, BUNK_N, OrderedDict([
    ("kind", 1),
    ("density", 0.30),
    ("range", 90.0),
    ("color", OrderedDict([("r", 0.52), ("g", 0.50), ("b", 0.47)])),
    ("rock_sides", 5),
    ("rock_scale", 0.55),
    ("ground_texture", "rock"),
    ("ground_amplitude", 0.14),
    ("max_slope_angle", 38.0),
]))

# -------------------------------------------------------------------- emit ----


def fmt(v):
    t = ("%.4f" % v).rstrip("0").rstrip(".")
    return t if t not in ("", "-0") else "0"


DETAILS = {
    # The compiled-in "cliff" preset, written out: a level file carries the
    # numbers, not the name, so editing the preset later cannot silently
    # restyle a level that shipped.
    "cliff": [
        "      preset: cliff",
        "      seed: 20250828",
        "      displace: true",
        "      waves:",
        "      - {enabled: true, amplitude: 0.6, frequency: 0.2, rotation: 0, tess_level: 24}",
        "      - {enabled: true, amplitude: 0.25, frequency: 0.55, rotation: 55, tess_level: 24}",
        "      - {enabled: true, amplitude: 0.1, frequency: 1.4, rotation: 115, tess_level: 24}",
        "      noise: true",
        "      noise_amplitude: 0.3",
        "      noise_frequency: 0.7",
        "      noise_octaves: 5",
        "      noise_tess_level: 24",
        "      edge_taper: 0.6",
        "      scatter: true",
        "      scatter_mesh: rock",
        "      scatter_tex: rock",
        "      scatter_density: 0.22",
        "      scatter_spacing: 2.4",
        "      scatter_scale: 0.7",
        "      scatter_jitter: 0.9",
        "      scatter_range: 70",
    ],
}

L = [
    "# beachhead.yaml — GENERATED by beachhead_gen.py. Edit that, not this.",
    "#",
    "# A landing beach between two cliffs, rising to a concrete bunker line with",
    "# an open gate under a walkway, and rolling grass behind it.",
    "#",
    "# Every wall in the complex is a solid gap between two sectors rather than a",
    "# boundary between them, so it has real thickness: an interior room's",
    "# outline is 2.0 inside the roof slab's, and the space between is not a",
    "# sector at all. The cliffs are ground steeper than the slope resolver will",
    "# walk up, not walls. See the generator's header for the reasoning.",
    "chunks:",
    "- id: Beachhead",
    "  points:",
]
for name, (x, z) in points.items():
    L.append('    %s: {x: %s, "y": %s}' % (name, fmt(x), fmt(z)))
L.append("  layers:")
L.append('  - {name: Ground, base_y: %s, ceil_y: %s}' % (fmt(PLAT), fmt(CEIL_IN)))
L.append('  - {name: Roof, base_y: %s, ceil_y: %s}' % (fmt(ROOF), fmt(ROOF + 2.0)))
L.append("  sectors:")

for s in sectors:
    ring = ring_for(s)
    heights = floor_heights(s, ring)
    base_h = min(heights)
    offs = [h - base_h for h in heights]
    L.append("  - id: %s" % s["id"])
    if s["layer"]:
        L.append("    layer: %d" % s["layer"])
    L.append("    floor:")
    L.append("      enabled: true")
    L.append("      height: %s" % fmt(base_h))
    L.append("      texture: %s" % s["floor_tex"])
    L.append("      terrain: false")
    if any(abs(o) > 1e-4 for o in offs):
        L.append("      vertex_offsets: [%s]" % ", ".join(fmt(o) for o in offs))
    if s["skirt"]:
        L.append("      skirt: %s" % s["skirt"])
        if s["skirt_tex"]:
            L.append("      skirt_texture: %s" % s["skirt_tex"])
    if s["smooth"]:
        L.append("      smooth: true")
    if s["detail"]:
        L.append("      detail:")
        L.extend(DETAILS[s["detail"]])
    L.append("    ceiling:")
    L.append("      enabled: %s" % ("true" if s["ceil_enabled"] else "false"))
    L.append("      height: %s" % fmt(s["ceil"]))
    L.append("      texture: %s" % s["ceil_tex"])
    L.append("    edges:")
    for k, a in enumerate(ring):
        b = ring[(k + 1) % len(ring)]
        ax, az = points[a]
        bx, bz = points[b]
        mid = ((ax + bx) / 2.0, (az + bz) / 2.0)
        extra = ""
        if s["pin"] and s["pin"](*mid):
            extra += ", pinned: true"
        if s["hidden"] and s["hidden"](*mid):
            extra += ", hidden: true"
        if s["gaptex"]:
            tex = s["gaptex"](*mid)
            if tex:
                extra += ", gap_texture: %s" % tex
        collision = "false" if s["nocollide"] else "true"
        L.append("    - {id: e%d, from: %s, to: %s, kind: wall, texture: %s, collision: %s%s}"
                 % (k, a, b, s["wall_tex"], collision, extra))

L.append("  biome_sectors:")


def emit_scalar(v):
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, float):
        return fmt(v)
    return str(v)


def emit_brush(prefix, brush):
    """A brush block. Nested maps are emitted inline when they hold only
    scalars — a colour reads better on one line — and as a block otherwise,
    which is the case `water` runs into with its own colour inside it."""
    out = []
    for key, val in brush.items():
        if isinstance(val, OrderedDict):
            if any(isinstance(v, OrderedDict) for v in val.values()):
                out.append("%s%s:" % (prefix, key))
                out.extend(emit_brush(prefix + "  ", val))
            else:
                inner = ", ".join("%s: %s" % (k, emit_scalar(v)) for k, v in val.items())
                out.append("%s%s: {%s}" % (prefix, key, inner))
        else:
            out.append("%s%s: %s" % (prefix, key, emit_scalar(val)))
    return out


for bid, poly, brush in biomes:
    L.append("  - id: %s" % bid)
    L.append("    points:")
    for (x, z) in poly:
        L.append('    - {x: %s, "y": %s}' % (fmt(x), fmt(z)))
    L.append("    brush:")
    L.extend(emit_brush("      ", brush))

spawn_z = -88.0
L += [
    '  origin: {x: 0, "y": 0, z: 0}',
    "  rotation: 0",
    "  scale: 1",
    "spawn:",
    # Feet on the sand a few paces up the beach, facing inland. Rotating the
    # marker's local +X by -90 degrees about Y points it at +Z.
    '  position: {x: 0, "y": %s, z: %s}' % (fmt(terrain(0.0, spawn_z) + 0.1), fmt(spawn_z)),
    '  rotation: {x: 0, "y": -1.5708, z: 0}',
    "lights:",
    "  global:",
    "    color: {r: 0.72, g: 0.80, b: 0.92}",
    "    brightness: 0.34",
    "  directional:",
    "  - id: sun",
    # Late afternoon, low and off to the west, so the cliffs and the bunker
    # faces are raked rather than lit flat.
    '    direction: {x: 0.55, "y": -0.52, z: 0.65}',
    "    color: {r: 1, g: 0.94, b: 0.82}",
    "    brightness: 0.95",
    "    cast_shadows: true",
    "    shadow_strength: 0.65",
    "  point:",
]
for (pid, px, pz) in [("lamp_w1", -37.0, -30.0), ("lamp_w2", -19.0, -24.0),
                      ("lamp_e1", 37.0, -30.0), ("lamp_e2", 19.0, -24.0)]:
    L += [
        "  - id: %s" % pid,
        '    position: {x: %s, "y": 8.6, z: %s}' % (fmt(px), fmt(pz)),
        "    color: {r: 1, g: 0.86, b: 0.66}",
        "    brightness: 0.9",
        "    radius: 14",
        "    cast_shadows: false",
        "    shadow_strength: 0",
    ]

open(sys.argv[1], "w").write("\n".join(L) + "\n")
print("%d points, %d sectors, %d biome sectors, water depth %.3f"
      % (len(points), len(sectors), len(biomes), water_depth))
