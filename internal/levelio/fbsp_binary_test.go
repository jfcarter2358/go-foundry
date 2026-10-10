package levelio

// fbsp_binary_test.go — the columnar compiled-level format reads back exactly
// what the gob format did, writes the same bytes every time, and refuses a
// file it cannot trust.

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"gopkg.in/yaml.v2"

	"foundry/internal/binenc"
	"foundry/internal/bsp"
	"foundry/internal/cluster"
	"foundry/internal/compile"
	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/scriptdata"
	"foundry/internal/world"
)

// saveCompiledLevelGob writes c the way version 9 and before did — one gob
// stream after the magic — so the fallback reader has something to read.
func saveCompiledLevelGob(t testing.TB, c *CompiledLevel, path string) {
	t.Helper()
	rawYAML, err := yaml.Marshal(c.Raw)
	if err != nil {
		t.Fatal(err)
	}
	f := compiledFile{
		Version: legacyGobVersion, Faces: c.Faces, Tree: c.Tree, PVS: c.PVS,
		Chunks: c.Chunks, RawYAML: rawYAML, Stats: c.Stats, Clusters: c.Clusters,
	}
	var buf bytes.Buffer
	buf.WriteString(compiledMagic)
	if err := gob.NewEncoder(&buf).Encode(&f); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// richFixture is the round-trip fixture with everything the encodings have to
// carry: a frame-owned chunk, terrain (blocks, a paint map, biome amounts) and
// script attributes — the maps gob wrote in random order.
func richFixture(t *testing.T) *CompiledLevel {
	t.Helper()
	lvl := sealedLevelWithShip()
	tr := world.NewTerrain(mgl32.Vec3{-3, 0.1, -3}, 0.5, 12, 6, 12)
	tr.Biomes = []world.TerrainBiome{{Preset: "forest"}}
	tr.Generate(world.TerrainFill{Height: 3, Hills: 1, HillSize: 4, Top: 1, Under: 2, Deep: 2, Biome: 1})
	tr.SetBlock(3, 1, 4, 0)
	tr.Layers = [world.TerrainLayerCount]string{"grass", "dirt"}
	lvl.Chunks[0].Terrain = tr
	lvl.Chunks[0].RecomputeBounds()
	attrs := scriptdata.AttrSet{}
	for i := 0; i < 24; i++ {
		attrs[fmt.Sprintf("a%02d", i)] = scriptdata.IntAttr(int64(i * 7))
	}
	attrs["name"] = scriptdata.StringAttr("crate")
	attrs["live"] = scriptdata.BoolAttr(true)
	attrs["f"] = scriptdata.FloatAttr(0.25)
	lvl.Chunks[0].Attributes = attrs
	lvl.Chunks[0].Tags = []string{"one", "two"}
	c, err := CompileLevel(lvl, compile.DefaultOptions())
	if err != nil {
		t.Fatalf("CompileLevel: %v", err)
	}
	return c
}

// sameCompiled fails t where a and b differ, naming the part.
func sameCompiled(t *testing.T, a, b *CompiledLevel) {
	t.Helper()
	parts := []struct {
		name string
		a, b any
	}{
		{"faces", a.Faces, b.Faces}, {"tree", a.Tree, b.Tree}, {"pvs", a.PVS, b.PVS},
		{"chunks", a.Chunks, b.Chunks}, {"stats", a.Stats, b.Stats},
		{"clusters", a.Clusters, b.Clusters}, {"raw", a.Raw, b.Raw},
	}
	for _, p := range parts {
		if d := firstDiff(reflect.ValueOf(p.a), reflect.ValueOf(p.b), p.name); d != "" {
			t.Errorf("the %s differ between the two formats: %s", p.name, d)
		}
	}
}

func TestTheColumnarFormatReadsBackWhatGobDid(t *testing.T) {
	c := richFixture(t)
	dir := t.TempDir()
	oldPath, newPath := filepath.Join(dir, "old"+CompiledExt), filepath.Join(dir, "new"+CompiledExt)
	saveCompiledLevelGob(t, c, oldPath)
	if err := SaveCompiledLevel(c, newPath); err != nil {
		t.Fatal(err)
	}
	old, err := ReadCompiledLevel(oldPath)
	if err != nil {
		t.Fatalf("the gob file no longer reads: %v", err)
	}
	back, err := ReadCompiledLevel(newPath)
	if err != nil {
		t.Fatalf("the columnar file does not read: %v", err)
	}
	if old.Version != legacyGobVersion || back.Version != CompiledVersion {
		t.Errorf("versions %d and %d", old.Version, back.Version)
	}
	// The same values as gob gave, but for the sign of zero, which gob lost
	// and this format keeps.
	diffIgnoresZeroSign = true
	sameCompiled(t, old, back)
	diffIgnoresZeroSign = false
	// And exactly, bit for bit, what was compiled: every float the compile
	// produced, -0 included, read back as itself.
	for i := range c.Faces {
		if d := firstDiff(reflect.ValueOf(c.Faces[i].Points), reflect.ValueOf(back.Faces[i].Points), "points"); d != "" {
			t.Fatalf("face %d: %s", i, d)
		}
		if d := firstDiff(reflect.ValueOf(c.Faces[i].Light), reflect.ValueOf(back.Faces[i].Light), "light"); d != "" {
			t.Fatalf("face %d: %s", i, d)
		}
	}
	for i := range c.Tree.Leaves {
		if d := firstDiff(reflect.ValueOf(c.Tree.Leaves[i].Planes), reflect.ValueOf(back.Tree.Leaves[i].Planes), "planes"); d != "" {
			t.Fatalf("leaf %d: %s", i, d)
		}
	}
	if back.Chunks[0].Terrain == nil || len(back.Chunks[0].Attributes) != 27 {
		t.Fatal("the chunk's terrain or attributes did not survive")
	}
}

// Two compiles of one level are the same file — which the gob format never
// managed with a map anywhere in a chunk.
func TestTheColumnarFileIsTheSameBytesEveryTime(t *testing.T) {
	c := richFixture(t)
	first, err := encodeCompiledLevel(c)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		again, err := encodeCompiledLevel(c)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, again) {
			t.Fatal("encoding the same level twice gave different bytes")
		}
	}
	// And a file read back and written again is the file.
	back, err := readCompiledBinary(first)
	if err != nil {
		t.Fatal(err)
	}
	again, err := encodeCompiledLevel(back)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, again) {
		t.Fatal("a file read back and written again changed")
	}
}

func TestADamagedColumnarFileIsRefused(t *testing.T) {
	c := compileFixture(t)
	data, err := encodeCompiledLevel(c)
	if err != nil {
		t.Fatal(err)
	}
	flipped := append([]byte(nil), data...)
	flipped[len(flipped)/2] ^= 0x40
	if _, err := decodeCompiledLevel("x", flipped); err == nil {
		t.Error("a file with a flipped byte loaded")
	}
	for _, n := range []int{len(data) - 1, len(data) / 2, binaryHeaderSize + 3, 20} {
		if _, err := decodeCompiledLevel("x", data[:n]); err == nil {
			t.Errorf("a file truncated to %d of %d bytes loaded", n, len(data))
		}
	}
}

// The header alone says whether a file can load, which is what lets
// BeginLoadCompiled refuse one before the loading screen goes up.
func TestTheHeaderAloneRefusesAStaleOrForeignFile(t *testing.T) {
	dir := t.TempDir()
	c := compileFixture(t)
	c.Version = CompiledVersion + 1
	stale := filepath.Join(dir, "stale"+CompiledExt)
	if err := SaveCompiledLevel(c, stale); err != nil {
		t.Fatal(err)
	}
	if err := CheckCompiledLevel(stale); err == nil || !strings.Contains(err.Error(), "recompile") {
		t.Errorf("a stale file passed the header check: %v", err)
	}
	foreign := filepath.Join(dir, "foreign"+CompiledExt)
	os.WriteFile(foreign, []byte("chunks: []\n"), 0o644)
	if err := CheckCompiledLevel(foreign); err == nil {
		t.Error("a YAML file passed the header check")
	}
	if err := CheckCompiledLevel(filepath.Join(dir, "missing"+CompiledExt)); err == nil {
		t.Error("a missing file passed the header check")
	}
	c.Version = CompiledVersion
	good := filepath.Join(dir, "good"+CompiledExt)
	if err := SaveCompiledLevel(c, good); err != nil {
		t.Fatal(err)
	}
	if err := CheckCompiledLevel(good); err != nil {
		t.Errorf("a good file failed the header check: %v", err)
	}
}

// exportedFields is t's exported field names, sorted.
func exportedFields(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).IsExported() {
			out = append(out, t.Field(i).Name)
		}
	}
	sort.Strings(out)
	return out
}

// The columns are written field by field, by hand. A field added to any of
// these types would be silently dropped from every compiled level — so adding
// one fails here, and the fix is a column in fbsp_binary.go (and this list).
func TestTheColumnarFormatCoversEveryField(t *testing.T) {
	want := map[reflect.Type][]string{
		reflect.TypeOf(compile.Face{}): {"Alpha", "BackIndirect", "BackLight", "BackSunShare", "BlendMask",
			"BlendTexture", "Brush", "Chunk", "CoveredBy", "Detail", "DoubleSided", "FaceIdx", "Indirect",
			"Light", "NoDraw", "Plane", "Points", "Smooth", "SmoothAngle", "SmoothBackIndirect",
			"SmoothBackLight", "SmoothCorner", "SmoothIndirect", "SmoothLight", "SmoothPoly", "SunShare",
			"Texture", "UVMap", "UVMapped", "UVOffset", "UVOrigin", "UVRotation", "UVScale"},
		reflect.TypeOf(world.UVMap{}):              {"U", "V"},
		reflect.TypeOf(bsp.Plane{}):                {"D", "N"},
		reflect.TypeOf(bsp.Tree{}):                 {"Hi", "Leaves", "Lo", "Nodes", "Root"},
		reflect.TypeOf(bsp.Node{}):                 {"Back", "Faces", "Front", "Hi", "Lo", "Plane"},
		reflect.TypeOf(bsp.Leaf{}):                 {"Faces", "Hi", "Lo", "Outside", "Planes", "Solid"},
		reflect.TypeOf(bsp.PVS{}):                  {"Bits", "Leaves", "RowBytes"},
		reflect.TypeOf(world.StaticClusterGroup{}): {"Clusters", "DAG", "Key", "Order", "Sig"},
		reflect.TypeOf(world.StaticGroupDAG{}):     {"BaseOrder", "ClusterLeaf", "Clusters", "Indices", "Leaves", "Levels", "Nodes"},
		reflect.TypeOf(CompiledLevel{}):            {"Chunks", "Clusters", "Faces", "PVS", "Raw", "Stats", "Tree", "Version"},
	}
	for tp, fields := range want {
		if got := exportedFields(tp); !reflect.DeepEqual(got, fields) {
			t.Errorf("%s has fields %v; the columnar format writes %v", tp, got, fields)
		}
	}
	// The plane and vector runs are read as floats laid end to end.
	if reflect.TypeOf(bsp.Plane{}).Size() != 16 || reflect.TypeOf(mgl32.Vec3{}).Size() != 12 {
		t.Error("bsp.Plane or mgl32.Vec3 is no longer plain float32s")
	}
	for _, tp := range []reflect.Type{reflect.TypeOf(cluster.Cluster{}), reflect.TypeOf(cluster.LODCluster{}), reflect.TypeOf(cluster.LODNode{})} {
		if podWords(tp) == 0 {
			t.Errorf("%s is no longer a run of four-byte numbers; it cannot be written as its memory", tp)
		}
	}
}

// A field the reader does not know is skipped, and one it lacks reads as
// zero: what lets the chunks' encoding outlive a change to world.Chunk.
func TestTheChunkEncodingToleratesAddedAndRemovedFields(t *testing.T) {
	type v1 struct {
		A    int
		Gone []string
		M    map[string]float32
		V    []mgl32.Vec3
		B    []byte
	}
	type v2 struct {
		A   int64
		M   map[string]float32
		V   []mgl32.Vec3
		B   []byte
		New string
	}
	in := v1{A: -3, Gone: []string{"x"}, M: map[string]float32{"q": 1, "r": 2}, V: []mgl32.Vec3{{1, 2, 3}}, B: []byte{9}}
	data, err := binenc.Marshal(&in)
	if err != nil {
		t.Fatal(err)
	}
	var out v2
	if err := binenc.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.A != -3 || len(out.M) != 2 || out.V[0] != (mgl32.Vec3{1, 2, 3}) || out.B[0] != 9 || out.New != "" {
		t.Errorf("got %+v", out)
	}
}

// compareRealLevels is the check against the levels that ship: every .fbsp in
// FOUNDRY_FBSP_COMPARE (a project's levels directory, gob files) read through
// the old reader, written in the new format, read back — the artefacts must be
// identical, and so must the world.Level each loads into.
func TestRealCompiledLevelsSurviveTheFormatChange(t *testing.T) {
	dir := os.Getenv("FOUNDRY_FBSP_COMPARE")
	if dir == "" {
		t.Skip("set FOUNDRY_FBSP_COMPARE to a project's resources/levels directory")
	}
	fileutil.ResourcesPath = filepath.Dir(dir)
	defer withHeadlessGFX()()
	names := gfx.TextureNames
	gfx.TextureNames = gfx.ScanTextures(fileutil.TexturesDir())
	defer func() { gfx.TextureNames = names }()
	files, _ := filepath.Glob(filepath.Join(dir, "*"+CompiledExt))
	for _, oldPath := range files {
		name := strings.TrimSuffix(filepath.Base(oldPath), CompiledExt)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(oldPath)
			if err != nil {
				t.Fatal(err)
			}
			if isBinaryCompiled(data) {
				t.Skip("already columnar")
			}
			t0 := time.Now()
			old, err := decodeCompiledLevel(oldPath, data)
			if err != nil {
				t.Fatal(err)
			}
			tGob := time.Since(t0)
			old.Version = CompiledVersion // what a recompile would stamp
			enc, err := encodeCompiledLevel(old)
			if err != nil {
				t.Fatal(err)
			}
			t0 = time.Now()
			back, err := decodeCompiledLevel(oldPath, enc)
			if err != nil {
				t.Fatal(err)
			}
			tBin := time.Since(t0)
			t.Logf("%s: gob %d bytes read in %v; columnar %d bytes read in %v", name, len(data), tGob, len(enc), tBin)
			sameCompiled(t, old, back)

			newPath := filepath.Join(t.TempDir(), name+CompiledExt)
			if err := os.WriteFile(newPath, enc, 0o644); err != nil {
				t.Fatal(err)
			}
			a, err := LoadCompiledLevel(oldPath)
			if err != nil {
				t.Fatal(err)
			}
			b, err := LoadCompiledLevel(newPath)
			if err != nil {
				t.Fatal(err)
			}
			if d := firstDiff(reflect.ValueOf(a.Static), reflect.ValueOf(b.Static), "static"); d != "" {
				t.Errorf("the static geometry differs between the two loads: %s", d)
			}
			if d := firstDiff(reflect.ValueOf(a.Chunks), reflect.ValueOf(b.Chunks), "chunks"); d != "" {
				t.Errorf("the chunks differ between the two loads: %s", d)
			}
		})
	}
}

// withHeadlessGFX turns the GPU halves of loading off for a test, and returns
// what puts them back.
func withHeadlessGFX() func() {
	g, tx := gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled
	gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled = false, false
	if gfx.Textures == nil {
		gfx.Textures = map[string]uint32{}
	}
	return func() { gfx.GeometryUploadsEnabled, gfx.TextureUploadsEnabled = g, tx }
}

// diffIgnoresZeroSign makes firstDiff call -0 and +0 the same. gob does not
// send a float equal to zero, and -0 is equal to zero, so every -0 a compile
// produces came back from a gob file as +0; the columnar format keeps it.
var diffIgnoresZeroSign bool

// firstDiff names the first place a and b differ, for a failure message.
func firstDiff(a, b reflect.Value, path string) string {
	if a.Kind() != b.Kind() {
		return path + ": kinds differ"
	}
	switch a.Kind() {
	case reflect.Ptr, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() != b.IsNil() {
				return path + ": one is nil"
			}
			return ""
		}
		return firstDiff(a.Elem(), b.Elem(), path)
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return fmt.Sprintf("%s: len %d vs %d", path, a.Len(), b.Len())
		}
		if a.Kind() == reflect.Slice && (a.IsNil() != b.IsNil()) {
			return path + ": nil vs empty"
		}
		for i := 0; i < a.Len(); i++ {
			if d := firstDiff(a.Index(i), b.Index(i), fmt.Sprintf("%s[%d]", path, i)); d != "" {
				return d
			}
		}
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if d := firstDiff(a.Field(i), b.Field(i), path+"."+a.Type().Field(i).Name); d != "" {
				return d
			}
		}
	case reflect.Map:
		if a.Len() != b.Len() || a.IsNil() != b.IsNil() {
			return path + ": map sizes differ"
		}
		for _, k := range a.MapKeys() {
			if d := firstDiff(a.MapIndex(k), b.MapIndex(k), fmt.Sprintf("%s[%v]", path, k)); d != "" {
				return d
			}
		}
	case reflect.Func:
		if a.IsNil() != b.IsNil() {
			return path + ": func nil-ness differs"
		}
	case reflect.Float32, reflect.Float64:
		// By bits: a NaN in a mesh is the same NaN either way, and
		// DeepEqual calls no two NaNs equal.
		x, y := a.Float(), b.Float()
		if diffIgnoresZeroSign && x == 0 && y == 0 {
			return ""
		}
		if math.Float64bits(x) != math.Float64bits(y) {
			return fmt.Sprintf("%s: %v vs %v", path, a.Float(), b.Float())
		}
	default:
		if !reflect.DeepEqual(valueOf(a), valueOf(b)) {
			return fmt.Sprintf("%s: %v vs %v", path, valueOf(a), valueOf(b))
		}
	}
	return ""
}

func valueOf(v reflect.Value) any {
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint()
	case reflect.Float32, reflect.Float64:
		return v.Float()
	case reflect.String:
		return v.String()
	}
	return v.Kind().String()
}

// BenchmarkDecodeCompiledLevel reads one compiled level in both formats: the
// file named by FOUNDRY_FBSP_BENCH (a gob file, re-encoded for the columnar
// run) or, without it, the test fixture.
func BenchmarkDecodeCompiledLevel(b *testing.B) {
	var c *CompiledLevel
	if p := os.Getenv("FOUNDRY_FBSP_BENCH"); p != "" {
		var err error
		if c, err = ReadCompiledLevel(p); err != nil {
			b.Fatal(err)
		}
		c.Version = CompiledVersion
	} else {
		var err error
		if c, err = CompileLevel(sealedLevelWithShip(), compile.DefaultOptions()); err != nil {
			b.Fatal(err)
		}
	}
	dir := b.TempDir()
	gobPath := filepath.Join(dir, "gob"+CompiledExt)
	saveCompiledLevelGob(b, c, gobPath)
	gobData, _ := os.ReadFile(gobPath)
	colData, err := encodeCompiledLevel(c)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("gob", func(b *testing.B) {
		b.SetBytes(int64(len(gobData)))
		for i := 0; i < b.N; i++ {
			if _, err := decodeCompiledLevel("gob", gobData); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("columnar", func(b *testing.B) {
		b.SetBytes(int64(len(colData)))
		for i := 0; i < b.N; i++ {
			if _, err := decodeCompiledLevel("columnar", colData); err != nil {
				b.Fatal(err)
			}
		}
	})
}
