#!/usr/bin/env python3
"""Writes the MIDI fixtures in this directory, byte by byte.

The files are built here rather than by the package's own writer so the
reader is tested against bytes it did not produce: running status across
channels, note-on with velocity 0 as note-off, system exclusive, unknown
meta events and chunks, a track with no end-of-track. Run it from this
directory; the tests read the .mid files it writes, and the expectations
in import_test.go follow the music below.
"""
import random
import struct


def vlq(v):
    out = [v & 0x7F]
    v >>= 7
    while v:
        out.append((v & 0x7F) | 0x80)
        v >>= 7
    return bytes(reversed(out))


class Track:
    """Absolute-time events, written in time order with running status."""

    def __init__(self):
        self.events = []  # (tick, order, bytes-with-status or meta)
        self.n = 0

    def add(self, tick, data):
        self.events.append((tick, self.n, data))
        self.n += 1

    def meta(self, tick, typ, payload):
        self.add(tick, b'\xff' + bytes([typ]) + vlq(len(payload)) + payload)

    def text(self, tick, typ, s):
        self.meta(tick, typ, s.encode())

    def tempo(self, tick, bpm):
        us = int(round(60e6 / bpm))
        self.meta(tick, 0x51, us.to_bytes(3, 'big'))

    def timesig(self, tick, num, den):
        self.meta(tick, 0x58, bytes([num, den.bit_length() - 1, 24, 8]))

    def keysig(self, tick, fifths, minor=False):
        self.meta(tick, 0x59, struct.pack('bB', fifths, 1 if minor else 0))

    def sysex(self, tick, payload):
        self.add(tick, b'\xf0' + vlq(len(payload)) + payload)

    def chan(self, tick, status, *data):
        self.add(tick, bytes([status]) + bytes(data))

    def note(self, tick, ch, key, vel, dur, off_as_zero_on=True):
        self.chan(tick, 0x90 | ch, key, vel)
        if off_as_zero_on:
            self.chan(tick + dur, 0x90 | ch, key, 0)
        else:
            self.chan(tick + dur, 0x80 | ch, key, 64)

    def encode(self, end=True):
        out = bytearray()
        last = 0
        status = None
        # Note-offs (and zero-velocity note-ons) before note-ons at a tick.
        def key(e):
            tick, n, d = e
            off = d[0] & 0xF0 == 0x80 or (d[0] & 0xF0 == 0x90 and d[2] == 0)
            return (tick, 0 if off else 1, n)
        for tick, _, d in sorted(self.events, key=key):
            out += vlq(tick - last)
            last = tick
            if d[0] >= 0xF0:
                out += d
                status = None
            elif d[0] == status:
                out += d[1:]  # running status
            else:
                out += d
                status = d[0]
        if end:
            out += vlq(0) + b'\xff\x2f\x00'
        return bytes(out)


def smf(fmt, division, tracks, extra=b'', ends=None):
    out = b'MThd' + struct.pack('>IHHH', 6, fmt, len(tracks), division)
    for i, t in enumerate(tracks):
        body = t.encode(end=True if ends is None else ends[i])
        out += b'MTrk' + struct.pack('>I', len(body)) + body
    return out + extra


# ---------------------------------------------------------------------------
# format0_band.mid: one track, three channels, 96 ticks a quarter, D major,
# 4/4 at 100. Piano chords on channel 1, a bass on channel 2, drums on 10;
# the channels interleave, so running status holds only within a channel's
# run. Four bars.
q = 96
t = Track()
t.text(0, 0x03, 'Band')
t.text(0, 0x01, 'made by make_fixtures.py')
t.tempo(0, 100)
t.timesig(0, 4, 4)
t.keysig(0, 2)
t.sysex(0, bytes([0x7E, 0x7F, 0x09, 0x01, 0xF7]))  # GM reset
t.meta(0, 0x7F, bytes([0x00, 0x00, 0x41]))  # sequencer-specific: unknown
t.chan(0, 0xC0, 0)   # piano
t.chan(0, 0xC1, 33)  # finger bass
chords = [(62, 66, 69), (67, 71, 74), (69, 73, 76), (62, 66, 69)]
for bar, ch in enumerate(chords):
    for beat in range(4):
        for k in ch:
            t.note(bar * 4 * q + beat * q, 0, k, 80, q)
basses = [38, 43, 45, 38]
for bar, b in enumerate(basses):
    t.note(bar * 4 * q, 1, b, 96, 4 * q, off_as_zero_on=False)
for bar in range(4):
    for beat in range(4):
        at = bar * 4 * q + beat * q
        t.note(at, 9, 36 if beat % 2 == 0 else 38, 100, q // 2)
        t.note(at, 9, 42, 70, q // 2)
        t.note(at + q // 2, 9, 42, 60, q // 2)
open('format0_band.mid', 'wb').write(smf(0, q, [t]))

# ---------------------------------------------------------------------------
# format1_orchestra.mid: 480 ticks a quarter. A conductor track, then five
# instruments played by a (seeded) human: onsets wander by up to 15 ticks
# and notes are held 90% of their length. 8 bars of 4/4 in G at 96, a ramp
# up to 120 over bar 7, then 4 bars of 3/4 in E-flat at 120. Rehearsal
# marks A and B, a cue "boom" on bar 10 beat 2.
random.seed(11)
q = 480
bar44 = 4 * q
bar34 = 3 * q


def at_bar(b):  # 1-based bar to tick
    if b <= 9:
        return (b - 1) * bar44
    return 8 * bar44 + (b - 9) * bar34


def human(tick):
    return max(0, tick + random.randint(-15, 15))


cond = Track()
cond.text(0, 0x03, 'Hangar Sketch')
cond.text(0, 0x02, 'J. Composer')
cond.tempo(0, 96)
cond.timesig(0, 4, 4)
cond.keysig(0, 1)
cond.text(0, 0x06, 'A')
cond.text(at_bar(5), 0x06, 'B')
for i in range(9):  # bar 7: 96 → 120 in eighths
    cond.tempo(at_bar(7) + i * q // 2, 96 + 3 * i)
cond.timesig(at_bar(9), 3, 4)
cond.keysig(at_bar(9), -3)
cond.text(at_bar(10) + q, 0x07, 'boom')

# Flute: quarters and eighths, eighth triplets on bar 3 beat 2, getting
# louder bar by bar (p → f), with an expression swell in bar 2.
flute = Track()
flute.text(0, 0x03, 'Flute')
flute.chan(0, 0xC0, 73)
melody = {  # bar: [(beat offset in quarters, length in quarters, key)]
    1: [(0, 1, 74), (1, 1, 76), (2, 2, 78)],
    2: [(0, 1.5, 79), (1.5, 0.5, 78), (2, 2, 76)],
    3: [(0, 1, 74), (1, 1 / 3, 76), (4 / 3, 1 / 3, 78), (5 / 3, 1 / 3, 79), (2, 2, 81)],
    4: [(0, 4, 79)],
    5: [(0, 0.5, 83), (0.5, 0.5, 81), (1, 0.5, 79), (1.5, 0.5, 78), (2, 2, 76)],
    6: [(0, 2, 74), (2, 2, 79)],
    7: [(0, 1, 78), (1, 1, 76), (2, 1, 74), (3, 1, 76)],
    8: [(0, 4, 79)],
    9: [(0, 1, 75), (1, 1, 77), (2, 1, 79)],
    10: [(0, 3, 80)],
    11: [(0, 1, 79), (1, 1, 77), (2, 1, 75)],
    12: [(0, 3, 75)],
}
vels = {1: 48, 2: 48, 3: 64, 4: 64, 5: 80, 6: 80, 7: 96, 8: 96, 9: 80, 10: 80, 11: 64, 12: 64}
for b, notes in melody.items():
    for off, ln, k in notes:
        start = at_bar(b) + int(round(off * q))
        flute.note(human(start), 0, k, vels[b] + random.randint(-4, 4), int(ln * q * 0.9))
for i in range(9):
    flute.chan(at_bar(2) + i * q // 2, 0xB0, 11, 127 - abs(4 - i) * 16)

# Violins: a held half note under moving quarters: two voices.
vln = Track()
vln.text(0, 0x03, 'Violins')
vln.chan(0, 0xC1, 48)
for b in range(1, 9):
    base = at_bar(b)
    vln.note(human(base), 1, 67, 70, int(2 * q * 0.95), off_as_zero_on=False)
    vln.note(human(base + 2 * q), 1, 66, 70, int(2 * q * 0.95), off_as_zero_on=False)
    for beat, k in enumerate((74, 76, 74, 72)):
        vln.note(human(base + beat * q), 1, k, 70, int(q * 0.9), off_as_zero_on=False)

# Cello: whole notes, its fader and pan set by controllers.
cello = Track()
cello.text(0, 0x03, 'Cello')
cello.chan(0, 0xC2, 42)
cello.chan(0, 0xB2, 7, 90)
cello.chan(0, 0xB2, 10, 40)
for b, k in zip(range(1, 9), (43, 48, 50, 43, 48, 45, 50, 43)):
    cello.note(human(at_bar(b)), 2, k, 72, int(bar44 * 0.95))

# Piano: right-hand chords over left-hand octaves: a grand staff.
pno = Track()
pno.text(0, 0x03, 'Piano')
pno.chan(0, 0xC3, 0)
for b in range(1, 9):
    base = at_bar(b)
    for beat in range(4):
        for k in (67, 71, 74):
            pno.note(human(base + beat * q), 3, k, 64, int(q * 0.8))
    for k in (31, 43):
        pno.note(human(base), 3, k, 72, int(bar44 * 0.9))

# A click on channel 10.
click = Track()
click.text(0, 0x03, 'Click')
for b in range(1, 9):
    for beat in range(4):
        click.note(at_bar(b) + beat * q, 9, 42, 50, q // 4)

open('format1_orchestra.mid', 'wb').write(smf(1, q, [cond, flute, vln, cello, pno, click]))

# ---------------------------------------------------------------------------
# waltz_nometa.mid: no time or key signature at all. A waltz in F: a low,
# long, loud bass on each downbeat, soft short chords on beats 2 and 3, a
# tune on top. The importer must guess 3/4 and F major. 192 ticks a quarter.
q = 192
w = Track()
w.text(0, 0x03, 'Waltz')
w.chan(0, 0xC0, 0)
harmony = [(41, (69, 72, 77)), (36, (67, 70, 76)), (36, (67, 70, 76)), (41, (69, 72, 77)),
           (46, (70, 74, 77)), (41, (69, 72, 77)), (36, (67, 70, 76)), (41, (69, 72, 77))] * 2
tune = [77, 79, 81, 79, 77, 76, 74, 72, 70, 69, 67, 65, 72, 74, 76, 77]
for i, (bass, chord) in enumerate(harmony):
    base = i * 3 * q
    w.note(base, 0, bass, 110, int(2.8 * q))
    for beat in (1, 2):
        for k in chord:
            w.note(base + beat * q, 0, k - 12, 50, q // 2)
    w.note(base, 0, tune[i] + 12, 90, 2 * q)
    w.note(base + 2 * q, 0, tune[(i + 1) % len(tune)] + 12, 70, q)
open('waltz_nometa.mid', 'wb').write(smf(1, q, [w]))

# ---------------------------------------------------------------------------
# sloppy.mid: what real files get wrong. A RIFF (RMID) wrapper, an unknown
# chunk before the track, a track with no end-of-track, and a note never
# released.
q = 120
s = Track()
s.chan(0, 0xC0, 40)
s.note(0, 0, 60, 90, q)
s.note(q, 0, 62, 90, q)
s.chan(2 * q, 0x90, 64, 90)  # never released
body = s.encode(end=False)
mid = (b'MThd' + struct.pack('>IHHH', 6, 0, 1, q) + b'XFIH' + struct.pack('>I', 4) + b'junk'
       + b'MTrk' + struct.pack('>I', len(body)) + body)
riff = b'RIFF' + struct.pack('<I', 4 + 8 + len(mid)) + b'RMID' + b'data' + struct.pack('<I', len(mid)) + mid
open('sloppy.mid', 'wb').write(riff)
