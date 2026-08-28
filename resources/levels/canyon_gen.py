#!/usr/bin/env python3
"""Generate the canyon level.

Sectors are given as axis-aligned rectangles. The generator does the three
things that are tedious and easy to get wrong by hand:

  * planar subdivision — each sector's boundary ring is rebuilt to include every
    named point lying on it, so two sectors that abut share IDENTICAL edges.
    InferPortals pairs edges by exact reverse match, so a boundary split on one
    side and not the other silently stays a wall and the doorway never opens.
  * CCW winding, which the engine requires when seen from above.
  * per-vertex offsets for sloped floors, from a rise/run given once.

Everything is emitted as `kind: wall`; the loader's InferPortals promotes the
shared ones. Edges that must stay solid are marked `pinned`.
"""

import sys
from collections import OrderedDict

EPS = 1e-6

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


sectors = []


def rect(sid, x0, z0, x1, z1, floor, ceil, ceil_enabled=True, layer=0,
         floor_tex="stone_floor", ceil_tex="concrete", wall_tex="concrete",
         slope=None, pin=None, skirt=None, skirt_tex=None):
    for (x, z) in [(x0, z0), (x1, z0), (x1, z1), (x0, z1)]:
        P(x, z)
    sectors.append(dict(id=sid, x0=x0, z0=z0, x1=x1, z1=z1, floor=floor,
                        ceil=ceil, ceil_enabled=ceil_enabled, layer=layer,
                        floor_tex=floor_tex, ceil_tex=ceil_tex,
                        wall_tex=wall_tex, slope=slope, pin=pin,
                        skirt=skirt, skirt_tex=skirt_tex))


# --------------------------------------------------------------- dimensions --

CLIFF = 18.0            # canyon rim height
ROOM_H = 4.0            # storey height; a body needs ~2.8 to fit under a ceiling
SLAB = 0.2              # gap between a ceiling and the floor above it
FLOOR2 = ROOM_H + SLAB
UPPER_CEIL = FLOOR2 + ROOM_H

BX = [-40, -12, 12, 40]     # basin grid, centre cell is the building
BZ = [-40, -12, 12, 40]
BLD = 12                    # building half-extent
SX = 4                      # stairwell strip: X[4,12]
SZ0, SZ1 = -4, 4            # climbing north through Z[-4,4]
DOOR_Z0, DOOR_Z1 = -3, 3    # doorway in the building's west wall

PX0, PX1 = 40, 52           # the cliff path's strip, east of the basin
FOOT_Z = -34                # the path's flat landing runs Z[-40,-34]

# --------------------------------------------------------------- the canyon --
#
# Floor at 0, no ceiling — but Ceiling.Height still sets how tall the walls are,
# so the basin's outer perimeter rises as a cliff face to the rim.


def basin_pin(mx, mz):
    """Pin the basin's east edge except where it meets the path's flat foot.
    Elsewhere along X=40 the path is already climbing, and an opening there
    would be a step up rather than a way through."""
    if abs(mx - PX0) > EPS:
        return False
    return not (-40 < mz < FOOT_Z)


for i in range(3):
    for j in range(3):
        if i == 1 and j == 1:
            continue
        rect("canyon_%d%d" % (i, j), BX[i], BZ[j], BX[i + 1], BZ[j + 1],
             floor=0.0, ceil=CLIFF, ceil_enabled=False,
             floor_tex="dirt", wall_tex="rock", pin=basin_pin)

# ------------------------------------------------------------- the building --
#
# Two storeys in one chunk, overlapping in XZ — which is what layers are for.
# The stairwell is a strip the upper storey simply does not cover, so the shaft
# is open with no hole to cut.

P(-BLD, DOOR_Z0)   # the doorway needs its own points: an edge is pinned or
P(-BLD, DOOR_Z1)   # promoted as a whole, never in part.


def shell_all(mx, mz):
    return (abs(mx + BLD) < EPS or abs(mx - BLD) < EPS or
            abs(mz + BLD) < EPS or abs(mz - BLD) < EPS)


def shell_door(mx, mz):
    """The building's perimeter, minus the west doorway."""
    if not shell_all(mx, mz):
        return False
    return not (abs(mx + BLD) < EPS and DOOR_Z0 < mz < DOOR_Z1)


rect("hall",   -BLD, -BLD, SX, BLD, floor=0.0, ceil=ROOM_H, pin=shell_door)
rect("hall_s",   SX, -BLD, BLD, SZ0, floor=0.0, ceil=ROOM_H, pin=shell_door)
rect("hall_n",   SX, SZ1, BLD, BLD, floor=0.0, ceil=ROOM_H, pin=shell_door)

# The staircase: one sloped sector rising the full storey.
#
# Its ceiling is the UPPER storey's, not the ground floor's, and it is enabled —
# the shaft is open between the two floors because no upper-storey sector covers
# this strip, not because the sector is open-topped. Leaving the ceiling off
# roofed the building everywhere except over the stairwell, so the shaft looked
# straight out at the sky.
rect("stairs", SX, SZ0, BLD, SZ1, floor=0.0, ceil=UPPER_CEIL, ceil_enabled=True,
     slope=("z", 0.0, FLOOR2), pin=shell_all)

# The upper storey's skirt closes the slab between the two floors. Without it
# the 0.2 band between the hall's ceiling at 4.0 and this floor at 4.2 is walled
# by neither storey, and the building has a slit through it at the floor line.
rect("upper",   -BLD, -BLD, SX, BLD, floor=FLOOR2, ceil=UPPER_CEIL, layer=1,
     pin=shell_all, skirt="ceiling", skirt_tex="concrete")
rect("upper_s",   SX, -BLD, BLD, SZ0, floor=FLOOR2, ceil=UPPER_CEIL, layer=1,
     pin=shell_all, skirt="ceiling", skirt_tex="concrete")
rect("upper_n",   SX, SZ1, BLD, BLD, floor=FLOOR2, ceil=UPPER_CEIL, layer=1,
     pin=shell_all, skirt="ceiling", skirt_tex="concrete")

# ----------------------------------------------------------- the cliff path --
#
# A switchback climbing the east wall. Ramps alternate with flat landings so a
# body never has to change direction on a slope. Each ramp stays well under the
# ~45 degree limit the slope resolver walks up.

PATH = [
    # (z0, z1, y at z0, y at z1)
    (-40, FOOT_Z, 0.0, 0.0),    # flat foot, level with the basin
    (FOOT_Z, -16, 0.0, 6.0),    # 6 over 18
    (-16, -8, 6.0, 6.0),        # landing
    (-8, 16, 6.0, 12.0),        # 6 over 24
    (16, 24, 12.0, 12.0),       # landing
    (24, 40, 12.0, CLIFF),      # 6 over 16, out onto the rim
]


def path_pin(mx, mz):
    """The path's west side is the cliff face it is cut into — solid, except at
    the foot where it meets the canyon floor."""
    if abs(mx - PX0) > EPS:
        return False
    return not (-40 < mz < FOOT_Z)


for n, (z0, z1, y0, y1) in enumerate(PATH):
    rect("path_%d" % n, PX0, z0, PX1, z1,
         floor=y0, ceil=CLIFF + 8, ceil_enabled=False,
         slope=None if y0 == y1 else ("z", y0, y1),
         floor_tex="dirt", wall_tex="rock", pin=path_pin)

# ---------------------------------------------------------------- the rim ----
#
# The plateau the path arrives on. It abuts the canyon's north edge in plan but
# sits 18 units above it, so InferPortals leaves that boundary a wall — two
# sectors sharing an edge are only joined when their heights overlap.

rect("rim_e", PX0, 40, 80, 60, floor=CLIFF, ceil=CLIFF + 8, ceil_enabled=False,
     floor_tex="grass", wall_tex="rock")
rect("rim_w", -40, 40, PX0, 60, floor=CLIFF, ceil=CLIFF + 8, ceil_enabled=False,
     floor_tex="grass", wall_tex="rock")

# ------------------------------------------------------------------- emit ----


def ring_for(s):
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


def vertex_offsets(s, ring):
    if not s["slope"]:
        return None
    axis, lo, hi = s["slope"]
    a0, a1 = (s["z0"], s["z1"]) if axis == "z" else (s["x0"], s["x1"])
    out = []
    for name in ring:
        px, pz = points[name]
        v = pz if axis == "z" else px
        t = 0.0 if a1 == a0 else (v - a0) / (a1 - a0)
        out.append(round(lo + (hi - lo) * t - s["floor"], 4))
    return out


def fmt(v):
    t = ("%.4f" % v).rstrip("0").rstrip(".")
    return t if t not in ("", "-0") else "0"


L = ["# canyon.yaml — a two-storey building on the floor of a canyon, with a",
     "# switchback path climbing the east cliff to the rim.",
     "#",
     "# Generated. Every boundary is split so abutting sectors share exact edges",
     "# (InferPortals pairs by exact reverse match), and the building's shell is",
     "# `pinned` so it is not dissolved into the basin around it.",
     "chunks:", "- id: Canyon", "  points:"]
for name, (x, z) in points.items():
    L.append('    %s: {x: %s, "y": %s}' % (name, fmt(x), fmt(z)))
L.append("  layers:")
L.append('  - {name: Ground, base_y: 0, ceil_y: %s}' % fmt(ROOM_H))
L.append('  - {name: Upper, base_y: %s, ceil_y: %s}' % (fmt(FLOOR2), fmt(UPPER_CEIL)))
L.append("  sectors:")
for s in sectors:
    ring = ring_for(s)
    offs = vertex_offsets(s, ring)
    L.append("  - id: %s" % s["id"])
    if s["layer"]:
        L.append("    layer: %d" % s["layer"])
    L.append("    floor:")
    L.append("      enabled: true")
    L.append("      height: %s" % fmt(s["floor"]))
    L.append("      texture: %s" % s["floor_tex"])
    L.append("      terrain: false")
    if offs:
        L.append("      vertex_offsets: [%s]" % ", ".join(fmt(o) for o in offs))
    if s["skirt"]:
        L.append("      skirt: %s" % s["skirt"])
        if s["skirt_tex"]:
            L.append("      skirt_texture: %s" % s["skirt_tex"])
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
        pinned = ", pinned: true" if (s["pin"] and s["pin"](*mid)) else ""
        L.append("    - {id: e%d, from: %s, to: %s, kind: wall, texture: %s, collision: true%s}"
                 % (k, a, b, s["wall_tex"], pinned))
L += ['  origin: {x: 0, "y": 0, z: 0}',
      "  rotation: 0",
      "  scale: 1",
      "spawn:",
      '  position: {x: -25, "y": 1, z: 0}',
      '  rotation: {x: 0, "y": 0, z: 0}',
      "lights:",
      "  global:",
      "    color: {r: 1, g: 1, b: 1}",
      "    brightness: 0.25",
      "  directional:",
      "  - id: sun",
      '    direction: {x: -0.35, "y": -0.86, z: 0.36}',
      "    yaw: 134",
      "    pitch: -59",
      "    color: {r: 1, g: 0.96, b: 0.86}",
      "    brightness: 0.85",
      "    cast_shadows: false",
      "    shadow_strength: 0",
      "  point: []"]

open(sys.argv[1], "w").write("\n".join(L) + "\n")
print("%d points, %d sectors" % (len(points), len(sectors)))
