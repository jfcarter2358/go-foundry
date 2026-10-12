package procgen

// testkinds_test.go — stage kinds the core's tests register for themselves,
// so they depend on no real stage.
//
// "tsource" makes a file and some placements and counts its runs; "tsink"
// refers to a tsource through a stage param and copies what it reads, so the
// incremental machinery has an edge to follow; "tall" declares one of every
// parameter type, for the file format.

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

var (
	sourceRuns atomic.Int64
	sinkRuns   atomic.Int64
	// slowGate, when set, makes tsource wait on it — the cancellation test
	// holds a run inside a stage with it.
	slowGate chan struct{}
)

func init() {
	RegisterStage(StageKind{
		Name: "tsource", Label: "Test source", Category: "Test", Version: 1,
		Params: []ParamDef{
			{Name: "count", Type: ParamInt, Default: Int(3), Min: 0, Max: 1000},
			{Name: "value", Type: ParamFloat, Default: Float(1.5)},
			{Name: "seed", Type: ParamSeed, Default: Seed(0)},
			{Name: "fail", Type: ParamBool, Default: Bool(false)},
		},
		Run: func(rc *RunContext) error {
			sourceRuns.Add(1)
			if slowGate != nil {
				select {
				case <-slowGate:
				case <-rc.Ctx.Done():
					return ErrCancelled
				}
			}
			if rc.Params.Bool("fail") {
				return errors.New("asked to fail")
			}
			n := rc.Params.Int("count")
			r := rc.RNG()
			out := fmt.Sprintf("value=%g first=%d\n", rc.Params.Float("value"), r.Uint64())
			if err := rc.WriteFile("data.txt", []byte(out)); err != nil {
				return err
			}
			err := rc.ParallelProgress(n, func(i int) error {
				rc.Place(Placement{ID: fmt.Sprintf("r%03d", i), Model: rc.AssetPath("data.txt"),
					Position: Point3{X: float32(i), Y: 0, Z: float32(r.Fork(uint64(i)).Float64())}})
				return nil
			})
			rc.Artifact = out
			return err
		},
		Load: func(res *StageResult) (any, error) {
			data, err := os.ReadFile(res.File("data.txt"))
			return string(data), err
		},
		Preview: func(pc *PreviewContext) (*PreviewData, error) {
			n := pc.Params.Int("count")
			vals := make([]float32, 4)
			for i := range vals {
				vals[i] = float32(i)
			}
			p := &PreviewData{Images: []ImageLayer{GrayImage("g", 2, 2, vals, 0, 3, Rect{0, 0, 10, 10})}, Artifact: n}
			return p, nil
		},
	})
	RegisterStage(StageKind{
		Name: "tsink", Category: "Test", Version: 1,
		Params: []ParamDef{
			{Name: "from", Type: ParamStage, Default: StageRef(""), StageKinds: []string{"tsource"}},
			{Name: "scale", Type: ParamFloat, Default: Float(2)},
		},
		Run: func(rc *RunContext) error {
			sinkRuns.Add(1)
			a, err := rc.UpstreamArtifact(rc.Params.Str("from"))
			if err != nil {
				return err
			}
			rc.PlaceVolume(VolumeEntry{"id": "zone", "kind": "trigger"})
			return rc.WriteFile("copy.txt", []byte(fmt.Sprint(a)))
		},
		Preview: func(pc *PreviewContext) (*PreviewData, error) {
			up := pc.Upstream(pc.Params.Str("from"))
			if up == nil {
				return nil, errors.New("no upstream preview")
			}
			return &PreviewData{Notes: []string{fmt.Sprintf("upstream count %v", up.Artifact)}}, nil
		},
	})
	RegisterStage(StageKind{
		Name: "tall", Category: "Test",
		Params: []ParamDef{
			{Name: "f", Type: ParamFloat, Default: Float(0.5), Min: 0, Max: 1},
			{Name: "i", Type: ParamInt, Default: Int(4), Min: 1, Max: 8},
			{Name: "b", Type: ParamBool, Default: Bool(true)},
			{Name: "e", Type: ParamEnum, Default: Enum("a"), Choices: []string{"a", "b", "c"}},
			{Name: "v2", Type: ParamVec2, Default: Vec2(1, 2)},
			{Name: "v3", Type: ParamVec3, Default: Vec3(1, 2, 3)},
			{Name: "c", Type: ParamColor, Default: Color(1, 0.5, 0.25), Min: 0, Max: 1},
			{Name: "t", Type: ParamTexture, Default: Texture("")},
			{Name: "s", Type: ParamString, Default: String("hello")},
			{Name: "seed", Type: ParamSeed, Default: Seed(7)},
			{Name: "ref", Type: ParamStage, Default: StageRef("")},
			{Name: "path", Type: ParamSpline, Default: Spline(), Channels: []string{"width"}, ChannelDefaults: []float64{10}},
			{Name: "path3", Type: ParamSpline, Default: Spline(), Spline3D: true},
			{Name: "profile", Type: ParamCurve, Default: Curve([2]float64{0, 0}, [2]float64{1, 1}), Min: 0, Max: 1},
			{Name: "layers", Type: ParamList, Default: List(), Fields: []ParamDef{
				{Name: "thickness", Type: ParamFloat, Default: Float(4), Min: 0, Max: 100},
				{Name: "hard", Type: ParamBool, Default: Bool(false)},
				{Name: "from", Type: ParamStage, Default: StageRef("")},
			}},
		},
		Run: func(rc *RunContext) error { return nil },
	})
}

// waitFor polls cond for up to a second.
func waitFor(cond func() bool) bool {
	for i := 0; i < 200; i++ {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
