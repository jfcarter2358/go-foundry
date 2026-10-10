package harness

// cmd_sim_nanite_test.go — `foundry sim` with an AI agent across a cooked
// Nanite cliff (docs/nanite/PHASE_2b.md), end to end: a mesa written as an
// OBJ, cooked by the real cook (its DAG, its collision cut, its hulls), placed
// as scenery in a level saved to disk, and an agent sent to an objective on
// top of it. The run is the whole frame — the loader, navigation built off
// the main thread, the controller, the resolve — driven from the command
// line, and the scenario's expectation is that the agent ends up on the
// plateau: up the ramp, since the cliff cannot be climbed and the inside of
// the mesa cannot be walked through.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/levelio"
	"foundry/internal/meshcollide"
	"foundry/internal/nanite"
	"foundry/internal/nanitecook"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// simMesaHeight is the mesa's surface: a 2 cm apron west of x 6, a plateau
// 1.6 m up east of x 12, and a ramp between them only where z is in [8, 12];
// elsewhere the plateau ends in a cliff at x 12. Local coordinates, 0..16.
func simMesaHeight(x, z float32) float32 {
	const top = 1.6
	switch {
	case x >= 10:
		return top
	case z >= 6 && z <= 10 && x >= 4:
		return max(0.02, top*(x-4)/6)
	default:
		return 0.02
	}
}

// writeMesaOBJ writes the mesa as a closed OBJ: its surface a quarter metre
// a quad over 16 x 16, a flat bottom and skirts.
func writeMesaOBJ(t *testing.T, path string) {
	t.Helper()
	const n = 64
	var b strings.Builder
	for _, top := range []bool{true, false} {
		for j := 0; j <= n; j++ {
			for i := 0; i <= n; i++ {
				x, z := 16*float32(i)/n, 16*float32(j)/n
				y := float32(0)
				if top {
					y = simMesaHeight(x, z)
				}
				fmt.Fprintf(&b, "v %g %g %g\n", x, y, z)
			}
		}
	}
	w := n + 1
	tp := func(i, j int) int { return j*w + i + 1 }
	bt := func(i, j int) int { return w*w + j*w + i + 1 }
	f := func(a, c, d int) { fmt.Fprintf(&b, "f %d %d %d\n", a, c, d) }
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			f(tp(i, j), tp(i, j+1), tp(i+1, j))
			f(tp(i+1, j), tp(i, j+1), tp(i+1, j+1))
			f(bt(i, j), bt(i+1, j), bt(i, j+1))
			f(bt(i+1, j), bt(i+1, j+1), bt(i, j+1))
		}
	}
	quad := func(t0, t1, b0, b1 int) { f(t0, b0, t1); f(t1, b0, b1) }
	for k := 0; k < n; k++ {
		quad(tp(k+1, 0), tp(k, 0), bt(k+1, 0), bt(k, 0))
		quad(tp(k, n), tp(k+1, n), bt(k, n), bt(k+1, n))
		quad(tp(0, k), tp(0, k+1), bt(0, k), bt(0, k+1))
		quad(tp(n, k+1), tp(n, k), bt(n, k+1), bt(n, k))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSimWalksAnAgentUpACookedMesa(t *testing.T) {
	// Pinned on: FOUNDRY_NANITE_COLLISION=0 in the environment would make
	// this a test of the box path.
	prevNC := world.NaniteCollision
	world.NaniteCollision = true
	t.Cleanup(func() { world.NaniteCollision = prevNC })
	dir := scratchProject(t, "scratch")
	res := filepath.Join(dir, "resources")

	// The mesa, cooked.
	src := filepath.Join(res, "models", "mesa.obj")
	writeMesaOBJ(t, src)
	cooked, err := nanitecook.CookFile(src, "", true, nanitecook.Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := cooked.Mesh
	if m.Collision == nil || m.Collision.OpenEdges != 0 || m.Collision.TriangleCount() >= m.TriangleCount() {
		t.Fatalf("the mesa's collision: %+v", m.Collision)
	}
	t.Logf("cooked: %s", nanitecook.Describe(m))

	// A room with the mesa in it, an agent at its foot and an objective on
	// its plateau.
	room := world.Chunk{ID: "room"}
	room.Brushes = world.NewHollowRoom("room", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{24, 6, 24}, 0.5, "concrete")
	mesa := world.NewScenery("mesa", "models/mesa"+nanite.Ext, mgl32.Vec3{4, 0, 4})
	spec := world.DefaultAISpec()
	spec.Objectives = []string{"top"}
	walker := world.Entity{
		ID: "walker", Position: mgl32.Vec3{6, 0.02, 6}, Scale: mgl32.Vec3{1, 1, 1},
		BoundingKind: world.BoundingCylinder, BoundHalfX: 0.3, BoundHalfY: 0.9, BoundHalfZ: 0.3,
		BoundOffset: mgl32.Vec3{0, 0.9, 0}, Visible: true,
		Collision: world.EntityCollision{World: true, Gravity: true, Movable: true},
		AI:        &spec,
	}
	top := world.NewVolume("top", mgl32.Vec3{18, 2.2, 7})
	obj := world.DefaultVolumeObjective()
	obj.Duration = 1000 // stay there
	top.Objective = &obj
	lvl := &world.Level{Chunks: []world.Chunk{room}, Scenery: []world.Scenery{mesa}, Entities: []world.Entity{walker}, Volumes: []world.Volume{top}}
	lvl.Lights.Global = world.GlobalLight{Color: mgl32.Vec3{1, 1, 1}, Brightness: 1}
	if err := world.RebuildIndexes(lvl); err != nil {
		t.Fatal(err)
	}
	if err := levelio.SaveLevel(lvl, levelio.LevelPath("mesa")); err != nil {
		t.Fatal(err)
	}

	sc := writeScenario(t, dir, "mesa.yaml", `
level: mesa
seconds: 30
watch: [walker.position]
steps:
  - at: 0
    teleport: [2, 0.5, 20]
  - at: 29
    expect:
      - path: walker.position
        near: [18, 1.6, 7]
        within: 1.2
`)
	var out strings.Builder
	code := SimMain(dir, []string{"-scenario", sc}, &out)
	if code != 0 || !strings.Contains(out.String(), "all expectations held") {
		t.Fatalf("exit %d; the agent did not reach the plateau:\n%s", code, out.String())
	}
	// It got there by the ramp: every position the run printed that is off
	// the apron west of the cliff is over the ramp's band (z 10..14 in the
	// world, a little slack for the body's radius).
	seen, onRamp := 0, 0
	for _, line := range strings.Split(out.String(), "\n") {
		// "  12.30  walker.position   <x, y, z>"
		f := strings.Fields(strings.NewReplacer("<", " ", ">", " ", ",", " ").Replace(line))
		if len(f) != 5 || f[1] != "walker.position" {
			continue
		}
		var tm, x, y, z float32
		if _, err := fmt.Sscan(f[0], &tm); err != nil {
			continue
		}
		fmt.Sscan(f[2], &x)
		fmt.Sscan(f[3], &y)
		fmt.Sscan(f[4], &z)
		seen++
		if x > 4 && x < 13.9 && y > 0.3 {
			if z < 9.5 || z > 14.5 {
				t.Errorf("at %vs the walker is at (%v, %v, %v): off the ground beside the ramp", tm, x, y, z)
			}
			onRamp++
		}
	}
	if seen < 100 || onRamp == 0 {
		t.Errorf("read %d positions, %d of them on the ramp: the run did not show the climb", seen, onRamp)
	}
	t.Logf("%d positions watched, %d on the ramp", seen, onRamp)
	_ = meshcollide.ModeMesh
}
