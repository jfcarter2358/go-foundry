package interpret

// tempo.go — ticks to seconds and back.
//
// The tempo map is a list of points in performance ticks, each a tempo in
// quarters a minute that either holds until the next point or moves to it
// in a straight line (a rit. or accel., linear in score time). Seconds are
// the integral of 60/(qpm*960) over ticks, which for a linear ramp has a
// closed form, so both directions are exact and cheap: the scheduler asks
// for them every block.

import (
	"math"
	"sort"

	"foundry/internal/score"
)

// DefaultQPM is the tempo of a score with no tempo marks.
const DefaultQPM = 120

// TempoPoint is a tempo from a tick on.
type TempoPoint struct {
	Tick float64
	QPM  float64
	// Ramp moves linearly to the next point's QPM instead of holding.
	Ramp bool
}

// TempoMap converts between performance ticks and seconds.
type TempoMap struct {
	Points []TempoPoint
	secs   []float64 // seconds at each point
}

// NewTempoMap builds a map from points in tick order. An empty list is a
// steady DefaultQPM.
func NewTempoMap(pts []TempoPoint) *TempoMap {
	if len(pts) == 0 || pts[0].Tick > 0 {
		q := float64(DefaultQPM)
		if len(pts) > 0 {
			q = pts[0].QPM
		}
		pts = append([]TempoPoint{{Tick: 0, QPM: q}}, pts...)
	}
	m := &TempoMap{Points: pts, secs: make([]float64, len(pts))}
	for i := 1; i < len(pts); i++ {
		m.secs[i] = m.secs[i-1] + m.segSeconds(i-1, pts[i].Tick-pts[i-1].Tick)
	}
	return m
}

const secPerTickAt1QPM = 60.0 / score.TicksPerQuarter

// segEnd is the QPM a segment moves to: the next point's when it ramps.
func (m *TempoMap) segEnd(i int) (q0, q1, length float64) {
	p := m.Points[i]
	q0, q1 = p.QPM, p.QPM
	length = math.Inf(1)
	if i+1 < len(m.Points) {
		length = m.Points[i+1].Tick - p.Tick
		if p.Ramp {
			q1 = m.Points[i+1].QPM
		}
	}
	return
}

// segSeconds is the time from segment i's start to x ticks into it.
func (m *TempoMap) segSeconds(i int, x float64) float64 {
	q0, q1, l := m.segEnd(i)
	if x <= 0 {
		return 0
	}
	if q0 == q1 || math.IsInf(l, 1) || l <= 0 {
		return x * secPerTickAt1QPM / q0
	}
	k := (q1 - q0) / l
	return secPerTickAt1QPM / k * math.Log((q0+k*x)/q0)
}

// segTicks inverts segSeconds.
func (m *TempoMap) segTicks(i int, s float64) float64 {
	q0, q1, l := m.segEnd(i)
	if s <= 0 {
		return 0
	}
	if q0 == q1 || math.IsInf(l, 1) || l <= 0 {
		return s * q0 / secPerTickAt1QPM
	}
	k := (q1 - q0) / l
	return q0 * (math.Exp(s*k/secPerTickAt1QPM) - 1) / k
}

func (m *TempoMap) segAtTick(t float64) int {
	i := sort.Search(len(m.Points), func(i int) bool { return m.Points[i].Tick > t }) - 1
	return max(i, 0)
}

// Seconds is the time at a tick, unscaled.
func (m *TempoMap) Seconds(tick float64) float64 {
	i := m.segAtTick(tick)
	return m.secs[i] + m.segSeconds(i, tick-m.Points[i].Tick)
}

// Tick is the tick at a time.
func (m *TempoMap) Tick(sec float64) float64 {
	i := sort.Search(len(m.secs), func(i int) bool { return m.secs[i] > sec }) - 1
	i = max(i, 0)
	return m.Points[i].Tick + m.segTicks(i, sec-m.secs[i])
}

// QPM is the tempo at a tick, in quarters a minute.
func (m *TempoMap) QPM(tick float64) float64 {
	i := m.segAtTick(tick)
	q0, q1, l := m.segEnd(i)
	if q0 == q1 || math.IsInf(l, 1) || l <= 0 {
		return q0
	}
	x := tick - m.Points[i].Tick
	return q0 + (q1-q0)*math.Min(x/l, 1)
}
