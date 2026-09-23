package harness

// scenario.go — a run written down.
//
// A repro used to be a command line in a commit message, or a scratch main that
// was deleted once it had answered its question. Neither survives, so the next
// time the same thing breaks the run has to be reinvented. A scenario is the
// same run as a file: where the player goes, what gets woken, and what should be
// true by the end. It can be checked in beside the level, run from the command
// line (`foundry sim -scenario <file>`) and run again from a Go test, which is
// what stops a fixed bug coming back.
//
// It is deliberately small. Anything a scenario cannot say, a Go test driving a
// Session can — the scenario file is for the runs worth keeping, not for
// expressing everything the harness can do.

import (
	"fmt"
	"os"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"
)

// Scenario is one file's worth of run.
type Scenario struct {
	Level    string   `yaml:"level"`
	Compiled bool     `yaml:"compiled"`
	Hz       float64  `yaml:"hz"`
	God      bool     `yaml:"god"`
	Menus    string   `yaml:"menus"`
	Scripts  bool     `yaml:"scripts_only"`
	Watch    []string `yaml:"watch"`
	// Seconds is how long the run is. It defaults to the last step's time plus
	// a second, so a scenario that is only a list of steps needs no total.
	Seconds float64 `yaml:"seconds"`
	Steps   []Step  `yaml:"steps"`
}

// Step is everything that happens at one moment of the run. The fields are all
// optional and all of them fire at At, in the order they are written here.
type Step struct {
	At       float64     `yaml:"at"`
	Enable   []string    `yaml:"enable"`
	Teleport *[3]float32 `yaml:"teleport"`
	Walk     *[3]float32 `yaml:"walk"`
	Toward   *[3]float32 `yaml:"toward"`
	Speed    float32     `yaml:"speed"`
	Crouch   *bool       `yaml:"crouch"`
	Sprint   *bool       `yaml:"sprint"`
	Jump     bool        `yaml:"jump"`
	Press    bool        `yaml:"press"`
	Kill     []string    `yaml:"kill"`
	Wake     []string    `yaml:"wake"`
	// RoundTrip saves the game, reloads the level and lays the save back over
	// it, reporting anything that did not come back.
	RoundTrip bool `yaml:"round_trip"`
	// Dump reports every watched value whether it changed or not.
	Dump bool `yaml:"dump"`
	// Expect is what should be true at this moment. A failed expectation does
	// not stop the run — the rest of it is usually what explains the failure.
	Expect []Expect `yaml:"expect"`
}

// Expect is one assertion about one watch path.
//
// Equals and Not compare the read string exactly, which is what a watch prints.
// Near and Within compare as a point, for the many cases where "about there" is
// the real requirement and the last decimal is noise.
type Expect struct {
	Path   string      `yaml:"path"`
	Equals string      `yaml:"equals"`
	Not    string      `yaml:"not"`
	Near   *[3]float32 `yaml:"near"`
	Within float32     `yaml:"within"`
}

// Failure is one expectation that did not hold.
type Failure struct {
	T    float64
	Path string
	Want string
	Got  string
}

func (f Failure) String() string {
	return fmt.Sprintf("%7.2f  EXPECT %s: want %s, got %s", f.T, f.Path, f.Want, f.Got)
}

// LoadScenario reads a scenario file.
func LoadScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sc Scenario
	if err := yaml.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if sc.Level == "" {
		return nil, fmt.Errorf("%s: no level named", path)
	}
	return &sc, nil
}

// Run plays a scenario and reports every expectation that did not hold.
//
// opts carries where the resources are and where the events go; everything else
// the scenario itself decides, so the same file behaves the same way from the
// command line and from a test.
func (sc *Scenario) Run(opts Options) ([]Failure, error) {
	opts.Level, opts.Compiled, opts.God = sc.Level, sc.Compiled, sc.God
	opts.ScriptsOnly = sc.Scripts
	if sc.Hz != 0 {
		opts.Hz = sc.Hz
	}
	if sc.Menus != "" {
		opts.Menus = sc.Menus
	}
	s, err := Open(opts)
	if err != nil {
		return nil, err
	}
	// Sorted by time, so a file may list its steps in whatever order reads
	// best. A stable sort keeps two steps at the same moment in file order.
	steps := append([]Step(nil), sc.Steps...)
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].At < steps[j].At })

	seconds := sc.Seconds
	if seconds == 0 && len(steps) > 0 {
		seconds = steps[len(steps)-1].At + 1
	}
	if len(sc.Watch) > 0 {
		s.Watch(sc.Watch)
	}

	var fails []Failure
	// A `toward` step keeps steering every tick until it arrives, so it outlives
	// the moment it was written at.
	var toward *[3]float32
	var towardSpeed float32
	next := 0
	for s.T < seconds {
		for next < len(steps) && steps[next].At <= s.T {
			st := steps[next]
			next++
			for _, id := range st.Enable {
				s.Enable(id)
			}
			if st.Teleport != nil {
				s.Teleport(vecOf(*st.Teleport))
			}
			if st.Walk != nil {
				s.Walk(vecOf(*st.Walk))
				toward = nil
			}
			if st.Toward != nil {
				toward, towardSpeed = st.Toward, st.Speed
				if towardSpeed == 0 {
					towardSpeed = WalkSpeed()
				}
			}
			if st.Crouch != nil {
				s.Crouch(*st.Crouch)
			}
			if st.Sprint != nil {
				s.Sprint(*st.Sprint)
			}
			if st.Jump {
				s.Jump()
			}
			if st.Press {
				s.Press()
			}
			for _, tag := range st.Kill {
				s.Kill(tag)
			}
			for _, tag := range st.Wake {
				s.Wake(tag)
			}
			if st.RoundTrip {
				s.RoundTrip()
			}
			if st.Dump {
				s.Dump()
			}
			fails = append(fails, s.check(st.Expect)...)
		}
		if toward != nil && !s.WalkToward(vecOf(*toward), towardSpeed) {
			toward = nil
		}
		s.Step()
	}
	return fails, nil
}

// check tests one step's expectations against the level as it is now.
func (s *Session) check(exp []Expect) []Failure {
	var out []Failure
	for _, e := range exp {
		got := s.Read(e.Path)
		switch {
		case e.Near != nil:
			want := vecOf(*e.Near)
			within := e.Within
			if within == 0 {
				within = 1
			}
			at, ok := readVec(got)
			if !ok || at.Sub(want).Len() > within {
				out = append(out, Failure{T: s.T, Path: e.Path,
					Want: fmt.Sprintf("within %s of %s", num(within), vec(want)), Got: got})
			}
		case e.Equals != "":
			if got != e.Equals {
				out = append(out, Failure{T: s.T, Path: e.Path, Want: e.Equals, Got: got})
			}
		case e.Not != "":
			if got == e.Not {
				out = append(out, Failure{T: s.T, Path: e.Path, Want: "anything but " + e.Not, Got: got})
			}
		}
		if e.Near == nil && e.Equals == "" && e.Not == "" {
			out = append(out, Failure{T: s.T, Path: e.Path, Want: "an expectation", Got: got})
		}
	}
	for i := range out {
		s.emit(Event{Kind: "EXPECT", Name: out[i].Path,
			Value: fmt.Sprintf("want %s, got %s", out[i].Want, out[i].Got)})
	}
	return out
}

func vecOf(a [3]float32) mgl32.Vec3 { return mgl32.Vec3{a[0], a[1], a[2]} }

// readVec parses what vec() printed, so an expectation can be a point even
// though a watch answers a string.
func readVec(s string) (mgl32.Vec3, bool) {
	var v mgl32.Vec3
	if n, err := fmt.Sscanf(s, "<%f, %f, %f>", &v[0], &v[1], &v[2]); n != 3 || err != nil {
		return v, false
	}
	return v, true
}
