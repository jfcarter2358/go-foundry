package harness

// sim_digest_test.go — a whole-frame fingerprint of a real level, for proving a
// performance change left the simulation bit-for-bit where it was.
//
// Opt-in: it needs the game project (FOUNDRY_SIM_DIGEST_ROOT, the directory
// holding resources/) and runs for minutes. It loads a level, wakes its enemies,
// steps it, and after every frame hashes everything the frame moves — every
// entity's placement, motion, footing, frame, attributes and every animated
// attachment's clock and bone palette; the player; every particle — writing one
// line per frame to FOUNDRY_SIM_DIGEST_OUT. Two runs (two builds, or one build
// with a parallel path switched off) are then compared with diff: the first
// line that differs is the first frame they disagree.
//
// Navigation is built synchronously before each frame, so the background
// builder's timing — the one thing about a run that is not a function of its
// inputs — cannot make two runs disagree.
//
//	FOUNDRY_SIM_DIGEST_ROOT=/mnt/c/Users/jcart/OneDrive/Documents/Foundry \
//	FOUNDRY_SIM_DIGEST_LEVEL=level3 FOUNDRY_SIM_DIGEST_WAKE=l3_enemy \
//	FOUNDRY_SIM_DIGEST_FRAMES=1200 FOUNDRY_SIM_DIGEST_OUT=/tmp/a.txt \
//	go test ./internal/harness -run TestSimDigest -v -timeout 30m

import (
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"foundry/internal/collision"
	"foundry/internal/flang"
	"foundry/internal/nav"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func TestSimDigest(t *testing.T) {
	root := os.Getenv("FOUNDRY_SIM_DIGEST_ROOT")
	if root == "" {
		t.Skip("FOUNDRY_SIM_DIGEST_ROOT not set")
	}
	level := os.Getenv("FOUNDRY_SIM_DIGEST_LEVEL")
	if level == "" {
		level = "level3"
	}
	frames := 600
	if v, err := strconv.Atoi(os.Getenv("FOUNDRY_SIM_DIGEST_FRAMES")); err == nil && v > 0 {
		frames = v
	}
	// Scripts roll dice seeded from the clock; a run that is to be compared
	// with another has to roll the same ones.
	var seeds uint64
	flang.TestSeeds = func() uint64 { seeds++; return seeds * 0x9E3779B97F4A7C15 }
	defer func() { flang.TestSeeds = nil }()
	s, err := Open(Options{Project: root, Level: level, Compiled: os.Getenv("FOUNDRY_SIM_DIGEST_YAML") == "", God: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tag := range strings.Split(os.Getenv("FOUNDRY_SIM_DIGEST_WAKE"), ",") {
		if tag != "" {
			s.Wake(tag)
		}
	}
	// FOUNDRY_SIM_DIGEST_DUMPAT names frames whose whole state is written out
	// as well, for finding what differs at the frame the digests first part.
	dumpAt := map[int]bool{}
	for _, v := range strings.Split(os.Getenv("FOUNDRY_SIM_DIGEST_DUMPAT"), ",") {
		if n, err := strconv.Atoi(v); err == nil {
			dumpAt[n] = true
		}
	}
	// The parallel and memoised paths can be switched off, for comparing a
	// run against the plain serial one.
	if os.Getenv("FOUNDRY_SIM_DIGEST_SERIAL") != "" {
		defer func(e, m bool) { collision.ParallelEntityUpdate, world.ParallelEmitters = e, m }(collision.ParallelEntityUpdate, world.ParallelEmitters)
		collision.ParallelEntityUpdate, world.ParallelEmitters = false, false
	}
	if os.Getenv("FOUNDRY_SIM_DIGEST_NOSLEEP") != "" {
		defer func(s bool) { collision.SleepRestingBodies = s }(collision.SleepRestingBodies)
		collision.SleepRestingBodies = false
	}
	hits := collision.MotionMemoHits()
	defer func() { t.Logf("motion memo answered %d body steps", collision.MotionMemoHits()-hits) }()
	var out strings.Builder
	for f := 0; f < frames; f++ {
		nav.Sync(s.Level)
		s.Step()
		fmt.Fprintf(&out, "%d %s\n", f, simDigest(s.Level))
		if dumpAt[f] {
			fmt.Fprintf(&out, "---- frame %d\n%s----\n", f, simDump(s.Level))
		}
	}
	// The whole final state, readable, for finding what differs once the
	// per-frame lines have said when.
	out.WriteString(simDump(s.Level))
	if path := os.Getenv("FOUNDRY_SIM_DIGEST_OUT"); path != "" {
		if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// simDigest hashes simDump.
func simDigest(lvl *world.Level) string {
	h := fnv.New64a()
	h.Write([]byte(simDump(lvl)))
	return fmt.Sprintf("%016x", h.Sum64())
}

// simDump writes the state a frame moves, floats as their exact bits.
func simDump(lvl *world.Level) string {
	var b strings.Builder
	f := func(v float32) { fmt.Fprintf(&b, "%08x ", math.Float32bits(v)) }
	v3 := func(v mgl32.Vec3) { f(v[0]); f(v[1]); f(v[2]) }
	entity := func(e *world.Entity) {
		fmt.Fprintf(&b, "%s frame=%s ground=%v vis=%v ", e.ID, e.Frame, e.Collision.OnGround, e.Visible)
		v3(e.Position)
		v3(e.Velocity)
		v3(e.Rotation)
		v3(e.AngularVelocity)
		f(e.StepOffset)
		keys := make([]string, 0, len(e.Attributes))
		for k := range e.Attributes {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s=%v ", k, e.Attributes[k])
		}
		for mi := range e.Models {
			a := e.Models[mi].Anim
			if a == nil {
				continue
			}
			fmt.Fprintf(&b, "[%s clip=%d playing=%v ", e.Models[mi].Name, a.Clip, a.Playing)
			f(a.Time)
			for _, m := range a.Palette {
				for _, x := range m {
					f(x)
				}
			}
			b.WriteString("]")
		}
		b.WriteString("\n")
	}
	for i := range lvl.Entities {
		entity(&lvl.Entities[i])
	}
	if world.PlayerEntity != nil {
		b.WriteString("player ")
		entity(world.PlayerEntity)
	}
	for i := range lvl.Emitters {
		em := &lvl.Emitters[i]
		fmt.Fprintf(&b, "emitter %s ", em.ID)
		for _, sys := range em.Systems {
			for _, p := range sys.Live {
				v3(p.Pos)
				v3(p.Vel)
				f(p.Age)
			}
			b.WriteString("| ")
		}
		b.WriteString("\n")
	}
	return b.String()
}
