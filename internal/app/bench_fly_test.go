package app

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

// fly=1 makes the poses one flight: one reported pose, the waypoints kept,
// and the camera at the first through the warm-up, then evenly along the
// path to the last on the last measured frame.
func TestBenchFlightFliesThroughTheWaypoints(t *testing.T) {
	t.Setenv("FOUNDRY_BENCH", "level=x;fly=1;frames=11;warm=5;at=0,0,0,0,0|10,0,0,1,0|10,0,20,1,0.5")
	c, on, err := benchParse()
	if err != nil || !on {
		t.Fatal(on, err)
	}
	if len(c.poses) != 1 || len(c.path) != 3 {
		t.Fatalf("%d poses, %d waypoints", len(c.poses), len(c.path))
	}
	at := func(f int) benchPose { return benchFlightPose(c.path, f, c.frames) }
	if p := at(-3); p.eye != (mgl32.Vec3{}) {
		t.Errorf("warming up at %v", p.eye)
	}
	if p := at(5); p.eye.Sub(mgl32.Vec3{10, 0, 0}).Len() > 1e-4 || p.yaw != 1 {
		t.Errorf("halfway at %v yaw %v", p.eye, p.yaw)
	}
	if p := at(10); p.eye.Sub(mgl32.Vec3{10, 0, 20}).Len() > 1e-4 || p.pitch != 0.5 {
		t.Errorf("last frame at %v pitch %v", p.eye, p.pitch)
	}
	t.Setenv("FOUNDRY_BENCH", "level=x;fly=1;at=spawn")
	if _, _, err := benchParse(); err == nil {
		t.Error("a flight through spawn parsed")
	}
}
