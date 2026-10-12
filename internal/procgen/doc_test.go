package procgen

// doc_test.go — the .fgen format and the schema: round trips, defaults left
// out, every problem reported with where it is, and the clamps.

import (
	"strings"
	"testing"
)

func allDoc() *Doc {
	return &Doc{
		Name: "test", Doc: "a note", Seed: 99, Output: "generated/x", Level: "lvl",
		Stages: []Stage{
			{Kind: "tsource", ID: "src", Params: Params{"count": Int(5)}},
			{Kind: "tall", ID: "everything", Params: Params{
				"f":       Float(0.25),
				"i":       Int(6),
				"b":       Bool(false),
				"e":       Enum("c"),
				"v2":      Vec2(3, 4),
				"v3":      Vec3(5, 6, 7),
				"c":       Color(0.1, 0.2, 0.3),
				"t":       Texture("rock"),
				"s":       String("x y"),
				"seed":    Seed(1234),
				"ref":     StageRef("src"),
				"path":    Spline([]float64{0, 0, 30}, []float64{100, 50, 12}),
				"path3":   Spline([]float64{1, 2, 3}),
				"profile": Curve([2]float64{0, 1}, [2]float64{0.5, 0.2}, [2]float64{1, 0}),
				"layers": List(
					Params{"thickness": Float(8), "hard": Bool(true)},
					Params{"from": StageRef("src")},
				),
			}},
			{Kind: "tsink", ID: "sink", Disabled: true, Params: Params{"from": StageRef("src")}},
		},
	}
}

func TestDocRoundTripsEveryParamType(t *testing.T) {
	d := allDoc()
	data, err := EncodeDoc(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeDoc(data)
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, data)
	}
	got.Name = d.Name
	again, _ := EncodeDoc(got)
	if string(again) != string(data) {
		t.Errorf("not stable:\n%s\n---\n%s", data, again)
	}
	for i := range d.Stages {
		k := KindByName(d.Stages[i].Kind)
		want := Resolve(k.Params, d.Stages[i].Params)
		have := Resolve(k.Params, got.Stages[i].Params)
		if !want.Equal(have) {
			t.Errorf("stage %s: params changed in the round trip", d.Stages[i].ID)
		}
	}
	if !got.Stages[2].Disabled {
		t.Error("enabled: false was lost")
	}
	if Fingerprint(got) != Fingerprint(d) {
		t.Error("fingerprint differs after a round trip")
	}
}

func TestDefaultsAreNotWritten(t *testing.T) {
	d := &Doc{Name: "x", Seed: 1, Stages: []Stage{{Kind: "tall", ID: "a", Params: Params{
		"f": Float(0.5), // the default
		"i": Int(5),
	}}}}
	data, _ := EncodeDoc(d)
	s := string(data)
	if strings.Contains(s, "f: 0.5") {
		t.Errorf("a default was written:\n%s", s)
	}
	if !strings.Contains(s, "i: 5") {
		t.Errorf("a set value was not written:\n%s", s)
	}
}

func TestDecodeReportsEveryProblem(t *testing.T) {
	src := `seed: 1
stages:
  - stage: tall
    id: a
    params:
      f: big
      ee: b
      e: d
      v2: {x: 1, q: 2}
      path: [{x: 1, z: nope}]
      layers: [{thickness: 3, colour: red}]
  - id: b
`
	_, err := DecodeDoc([]byte(src))
	if err == nil {
		t.Fatal("decoded a broken document")
	}
	de, ok := err.(*DecodeError)
	if !ok {
		t.Fatalf("error is %T, want *DecodeError", err)
	}
	want := []string{
		`param f: want a number`,
		`has no param "ee" (did you mean "e"?)`,
		`"d" is not one of a, b, c`,
		`no key "q"`,
		`point 1: z is "nope"`,
		`item 1: colour: not a field`,
		`stage b: has no ` + "`stage:`",
	}
	all := strings.Join(de.Problems, "\n")
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Errorf("no problem mentions %q; got:\n%s", w, all)
		}
	}
}

func TestStrictTopLevelKeys(t *testing.T) {
	_, err := DecodeDoc([]byte("seed: 1\nstagez: []\n"))
	if err == nil || !strings.Contains(err.Error(), "stagez") {
		t.Errorf("an unknown top-level key was accepted: %v", err)
	}
}

func TestUnknownKindIsKeptVerbatim(t *testing.T) {
	src := "seed: 1\nstages:\n- stage: from_the_future\n  id: f\n  params:\n    zeta: 3\n    alpha: [1, 2]\n"
	d, err := DecodeDoc([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	d.Name = "x"
	if probs := d.Validate(); len(probs) == 0 || !strings.Contains(probs[0].Msg, "from_the_future") {
		t.Errorf("Validate did not report the unknown kind: %v", probs)
	}
	out, _ := EncodeDoc(d)
	if !strings.Contains(string(out), "zeta: 3") || !strings.Contains(string(out), "alpha:") {
		t.Errorf("the unknown kind's params were lost:\n%s", out)
	}
	if _, err := Run(nil, d, Options{OutDir: t.TempDir()}); err == nil {
		t.Error("Run ran a document with an unknown kind")
	}
}

func TestResolveFillsDefaultsAndClamps(t *testing.T) {
	k := KindByName("tall")
	p := Resolve(k.Params, Params{
		"f":       Float(9),
		"i":       Int(0),
		"e":       Enum("zzz"),
		"c":       Color(2, -1, 0.5),
		"profile": Curve([2]float64{0.8, 2}, [2]float64{-1, 0.5}),
		"path":    Spline([]float64{1, 2}), // missing its width channel
		"layers":  List(Params{}),
	})
	if p.Float("f") != 1 || p.Int("i") != 1 {
		t.Errorf("not clamped: f %v i %v", p.Float("f"), p.Int("i"))
	}
	if p.Str("e") != "a" {
		t.Errorf("an unknown enum resolved to %q, want the first choice", p.Str("e"))
	}
	if c := p.Vec3("c"); c != [3]float64{1, 0, 0.5} {
		t.Errorf("colour clamped to %v", c)
	}
	prof := p.Points("profile")
	if prof[0][0] != 0 || prof[1][0] != 0.8 || prof[1][1] != 1 {
		t.Errorf("curve not sorted/clamped: %v", prof)
	}
	if pt := p.Points("path")[0]; len(pt) != 3 || pt[2] != 10 {
		t.Errorf("spline point not padded to its channel default: %v", pt)
	}
	if it := p.Items("layers")[0]; it.Float("thickness") != 4 {
		t.Errorf("list item not resolved: %v", it)
	}
	if p.Str("s") != "hello" || !p.Bool("b") {
		t.Error("missing params did not read their defaults")
	}
}

func TestCurveAt(t *testing.T) {
	c := Curve([2]float64{0, 0}, [2]float64{1, 10}, [2]float64{2, 10})
	for _, tc := range []struct{ x, want float64 }{{-1, 0}, {0, 0}, {0.5, 5}, {1.5, 10}, {3, 10}} {
		if got := c.CurveAt(tc.x); got != tc.want {
			t.Errorf("CurveAt(%v) = %v, want %v", tc.x, got, tc.want)
		}
	}
}

func TestValidateRefs(t *testing.T) {
	d := &Doc{Name: "v", Seed: 1, Stages: []Stage{
		{Kind: "tsink", ID: "early", Params: Params{"from": StageRef("src")}},
		{Kind: "tsource", ID: "src"},
		{Kind: "tsink", ID: "nothing", Params: Params{"from": StageRef("ghost")}},
		{Kind: "tsink", ID: "wrongkind", Params: Params{"from": StageRef("early")}},
		{Kind: "tsource", ID: "src"},
		{Kind: "tsource", ID: "Bad-ID"},
	}}
	all := ""
	for _, p := range d.Validate() {
		all += p.String() + "\n"
	}
	for _, w := range []string{
		"stage early, from: names stage \"src\", which runs after this one",
		"stage nothing, from: names stage \"ghost\", and there is none",
		"wants tsource",
		"the id is also stage 2's",
		"id \"Bad-ID\" must be",
	} {
		if !strings.Contains(all, w) {
			t.Errorf("no problem says %q; got:\n%s", w, all)
		}
	}
}

func TestCloneDoesNotShare(t *testing.T) {
	d := allDoc()
	c := d.Clone()
	c.Stages[1].Params["path"].Points[0][0] = 999
	c.Stages[1].Params["layers"].Items[0]["thickness"] = Float(77)
	c.Stages[0].ID = "changed"
	if d.Stages[1].Params["path"].Points[0][0] == 999 {
		t.Error("a clone shares spline points")
	}
	if d.Stages[1].Params["layers"].Items[0].Float("thickness") == 77 {
		t.Error("a clone shares list items")
	}
	if d.Stages[0].ID == "changed" {
		t.Error("a clone shares stages")
	}
}

func TestStageHashAndSeed(t *testing.T) {
	k := KindByName("tsource")
	p := Resolve(k.Params, nil)
	s1 := StageSeed(1, "a", k.Params, p)
	if StageSeed(1, "a", k.Params, p) != s1 {
		t.Fatal("seed not deterministic")
	}
	if StageSeed(2, "a", k.Params, p) == s1 || StageSeed(1, "b", k.Params, p) == s1 {
		t.Error("doc seed or stage id does not change the seed")
	}
	p2 := Resolve(k.Params, Params{"seed": Seed(5)})
	if StageSeed(1, "a", k.Params, p2) == s1 {
		t.Error("the seed param does not change the seed")
	}
	h := StageHash(k, s1, p, nil)
	if StageHash(k, s1, p, nil) != h {
		t.Fatal("hash not deterministic")
	}
	if StageHash(k, s1, Resolve(k.Params, Params{"count": Int(4)}), nil) == h {
		t.Error("a param change does not change the hash")
	}
	if StageHash(k, s1, p, []string{"up"}) == h {
		t.Error("an upstream hash does not change the hash")
	}
	// A value set to the default hashes as the default does.
	if StageHash(k, s1, Resolve(k.Params, Params{"count": Int(3)}), nil) != h {
		t.Error("spelling out a default changed the hash")
	}
}

func TestUniqueStageID(t *testing.T) {
	d := &Doc{Stages: []Stage{{ID: "terrain"}, {ID: "terrain_2"}}}
	if got := d.UniqueStageID("terrain"); got != "terrain_3" {
		t.Errorf("UniqueStageID = %q", got)
	}
	if got := d.UniqueStageID("rocks"); got != "rocks" {
		t.Errorf("UniqueStageID = %q", got)
	}
}

func TestTemplates(t *testing.T) {
	d, err := NewFromTemplate(BlankTemplate, "fresh")
	if err != nil || d.Name != "fresh" || len(d.Stages) != 0 {
		t.Fatalf("blank template: %v %+v", err, d)
	}
	if _, err := NewFromTemplate("nope", "x"); err == nil {
		t.Error("an unknown template built a document")
	}
}
