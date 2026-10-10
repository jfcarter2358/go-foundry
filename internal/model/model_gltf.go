package model

// model_gltf.go — glTF 2.0 import, beside OBJ. Phase 1 of the Nanite plan
// (docs/nanite/PHASE_1.md): the format a dense, scanned or sculpted asset
// actually arrives in.
//
// OBJ comes into the model editor as a ModelDoc (model_obj.go), which is the
// right shape for editing and the wrong one for a million-triangle rock: a
// ModelDoc keeps one texture for the whole mesh and derives its normals from
// the faces, and a scan's whole look is in its authored normals and its
// material slots. So glTF comes in as an ImportMesh instead — positions,
// normals and UVs per vertex, an index list, and a material SLOT per triangle
// — which is what a Nanite cook (internal/nanitecook) takes, and what OBJ and
// .fmdl are converted to on the way there.
//
// # What is read
//
//   - .gltf (JSON, with buffers in sidecar files or data: URIs) and .glb (the
//     binary container, buffer 0 in its BIN chunk).
//   - Every mesh reachable from the default scene's nodes (or every root node
//     when there is no scene), each node's TRS or matrix composed down the
//     hierarchy and baked into the positions and normals.
//   - Primitives in TRIANGLES, TRIANGLE_STRIP or TRIANGLE_FAN mode, indexed
//     or not. POSITION, NORMAL and TEXCOORD_0, in any component type the
//     spec allows for them, normalised integers included.
//   - Materials: name, base colour factor, the base colour texture's image
//     (by URI, reduced to the engine's bare texture name, as OBJ's map_Kd is),
//     and doubleSided.
//
// Not read: skins, morph targets, animations, cameras, lights, sparse
// accessors (refused, not misread), KHR extensions. A primitive with no
// material gets a default slot.
//
// glTF's texture V runs down the image and the engine's up it (OBJ's
// convention), so V is flipped on the way in.

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

// GLTFExt and GLBExt are the two glTF file extensions.
const (
	GLTFExt = ".gltf"
	GLBExt  = ".glb"
)

// ImportMesh is a mesh as an interchange file describes it: what a Nanite
// cook takes. Normals and UVs are nil or one per position.
type ImportMesh struct {
	Positions []mgl32.Vec3
	Normals   []mgl32.Vec3
	UVs       []mgl32.Vec2
	// Indices is the triangle list, three per triangle.
	Indices []uint32
	// TriSlot is each triangle's material slot, an index into Slots.
	TriSlot []int32
	Slots   []ImportSlot
	// Files is every file the import read — the source and its sidecars —
	// so a cook can hash all of them.
	Files []string
}

// ImportSlot is one material slot.
type ImportSlot struct {
	Name string
	// Texture is the engine texture name ("brick"), or "" for none.
	Texture     string
	BaseColor   [4]float32
	DoubleSided bool
}

// TriangleCount is how many triangles the mesh has.
func (m *ImportMesh) TriangleCount() int { return len(m.Indices) / 3 }

// IsGLTFPath reports whether path names a glTF file.
func IsGLTFPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == GLTFExt || ext == GLBExt
}

// ---------- the JSON document ----------

type gltfDoc struct {
	Asset struct {
		Version string `json:"version"`
	} `json:"asset"`
	Scene  *int `json:"scene"`
	Scenes []struct {
		Nodes []int `json:"nodes"`
	} `json:"scenes"`
	Nodes []struct {
		Mesh        *int      `json:"mesh"`
		Children    []int     `json:"children"`
		Matrix      []float32 `json:"matrix"`
		Translation []float32 `json:"translation"`
		Rotation    []float32 `json:"rotation"`
		Scale       []float32 `json:"scale"`
	} `json:"nodes"`
	Meshes []struct {
		Name       string `json:"name"`
		Primitives []struct {
			Attributes map[string]int `json:"attributes"`
			Indices    *int           `json:"indices"`
			Material   *int           `json:"material"`
			Mode       *int           `json:"mode"`
		} `json:"primitives"`
	} `json:"meshes"`
	Accessors []struct {
		BufferView    *int            `json:"bufferView"`
		ByteOffset    int             `json:"byteOffset"`
		ComponentType int             `json:"componentType"`
		Normalized    bool            `json:"normalized"`
		Count         int             `json:"count"`
		Type          string          `json:"type"`
		Sparse        json.RawMessage `json:"sparse"`
	} `json:"accessors"`
	BufferViews []struct {
		Buffer     int `json:"buffer"`
		ByteOffset int `json:"byteOffset"`
		ByteLength int `json:"byteLength"`
		ByteStride int `json:"byteStride"`
	} `json:"bufferViews"`
	Buffers []struct {
		URI        string `json:"uri"`
		ByteLength int    `json:"byteLength"`
	} `json:"buffers"`
	Materials []struct {
		Name                 string `json:"name"`
		DoubleSided          bool   `json:"doubleSided"`
		PbrMetallicRoughness *struct {
			BaseColorFactor  []float32 `json:"baseColorFactor"`
			BaseColorTexture *struct {
				Index int `json:"index"`
			} `json:"baseColorTexture"`
		} `json:"pbrMetallicRoughness"`
	} `json:"materials"`
	Textures []struct {
		Source *int `json:"source"`
	} `json:"textures"`
	Images []struct {
		URI      string `json:"uri"`
		Name     string `json:"name"`
		MimeType string `json:"mimeType"`
	} `json:"images"`
}

// glTF component types.
const (
	gltfByte          = 5120
	gltfUnsignedByte  = 5121
	gltfShort         = 5122
	gltfUnsignedShort = 5123
	gltfUnsignedInt   = 5125
	gltfFloat         = 5126
)

// LoadGLTF reads a .gltf or .glb file into an ImportMesh.
func LoadGLTF(path string) (*ImportMesh, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := ParseGLTF(data, filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	m.Files = append([]string{path}, m.Files...)
	return m, nil
}

// ParseGLTF parses glTF bytes — JSON or a GLB container — resolving any
// sidecar buffer relative to dir.
func ParseGLTF(data []byte, dir string) (*ImportMesh, error) {
	jsonBytes, bin, err := splitGLB(data)
	if err != nil {
		return nil, err
	}
	var doc gltfDoc
	if err := json.Unmarshal(jsonBytes, &doc); err != nil {
		return nil, fmt.Errorf("glTF JSON: %w", err)
	}
	if !strings.HasPrefix(doc.Asset.Version, "2") {
		return nil, fmt.Errorf("glTF version %q; only 2.x is supported", doc.Asset.Version)
	}
	out := &ImportMesh{}
	buffers := make([][]byte, len(doc.Buffers))
	for i, b := range doc.Buffers {
		switch {
		case b.URI == "":
			if i != 0 || bin == nil {
				return nil, fmt.Errorf("buffer %d has no uri and there is no GLB binary chunk", i)
			}
			buffers[i] = bin
		case strings.HasPrefix(b.URI, "data:"):
			comma := strings.IndexByte(b.URI, ',')
			if comma < 0 || !strings.Contains(b.URI[:comma], ";base64") {
				return nil, fmt.Errorf("buffer %d: only base64 data URIs are supported", i)
			}
			raw, err := base64.StdEncoding.DecodeString(b.URI[comma+1:])
			if err != nil {
				return nil, fmt.Errorf("buffer %d: %w", i, err)
			}
			buffers[i] = raw
		default:
			p := filepath.Join(dir, filepath.FromSlash(unescapeURI(b.URI)))
			raw, err := os.ReadFile(p)
			if err != nil {
				return nil, fmt.Errorf("buffer %d: %w", i, err)
			}
			buffers[i] = raw
			out.Files = append(out.Files, p)
		}
		if len(buffers[i]) < b.ByteLength {
			return nil, fmt.Errorf("buffer %d holds %d bytes, declares %d", i, len(buffers[i]), b.ByteLength)
		}
	}

	// Material slots, plus a default one for primitives with none.
	for _, mat := range doc.Materials {
		slot := ImportSlot{Name: mat.Name, BaseColor: [4]float32{1, 1, 1, 1}, DoubleSided: mat.DoubleSided}
		if pbr := mat.PbrMetallicRoughness; pbr != nil {
			if len(pbr.BaseColorFactor) == 4 {
				copy(slot.BaseColor[:], pbr.BaseColorFactor)
			}
			if t := pbr.BaseColorTexture; t != nil && t.Index >= 0 && t.Index < len(doc.Textures) {
				if src := doc.Textures[t.Index].Source; src != nil && *src >= 0 && *src < len(doc.Images) {
					slot.Texture = gltfTextureName(doc.Images[*src].URI, doc.Images[*src].Name)
				}
			}
		}
		if slot.Name == "" {
			slot.Name = fmt.Sprintf("material%d", len(out.Slots))
		}
		out.Slots = append(out.Slots, slot)
	}
	defaultSlot := int32(-1)
	anyNormals, anyUVs := false, false

	read := func(ai int) (gltfView, error) { return gltfAccessor(&doc, buffers, ai) }

	var walk func(node int, parent mgl32.Mat4, depth int) error
	walk = func(node int, parent mgl32.Mat4, depth int) error {
		if node < 0 || node >= len(doc.Nodes) || depth > 64 {
			return fmt.Errorf("node %d out of range (or a cycle)", node)
		}
		n := &doc.Nodes[node]
		world := parent.Mul4(gltfNodeMatrix(n.Matrix, n.Translation, n.Rotation, n.Scale))
		if n.Mesh != nil {
			if *n.Mesh < 0 || *n.Mesh >= len(doc.Meshes) {
				return fmt.Errorf("node %d names mesh %d of %d", node, *n.Mesh, len(doc.Meshes))
			}
			normalM := world.Mat3().Inv().Transpose()
			flip := world.Mat3().Det() < 0
			for pi, prim := range doc.Meshes[*n.Mesh].Primitives {
				mode := 4
				if prim.Mode != nil {
					mode = *prim.Mode
				}
				if mode != 4 && mode != 5 && mode != 6 {
					continue // points and lines carry no surface
				}
				pa, ok := prim.Attributes["POSITION"]
				if !ok {
					return fmt.Errorf("mesh %d primitive %d has no POSITION", *n.Mesh, pi)
				}
				pos, err := read(pa)
				if err != nil {
					return fmt.Errorf("mesh %d primitive %d POSITION: %w", *n.Mesh, pi, err)
				}
				var nrm, uv gltfView
				if na, ok := prim.Attributes["NORMAL"]; ok {
					if nrm, err = read(na); err != nil {
						return fmt.Errorf("mesh %d primitive %d NORMAL: %w", *n.Mesh, pi, err)
					}
				}
				if ua, ok := prim.Attributes["TEXCOORD_0"]; ok {
					if uv, err = read(ua); err != nil {
						return fmt.Errorf("mesh %d primitive %d TEXCOORD_0: %w", *n.Mesh, pi, err)
					}
				}
				base := uint32(len(out.Positions))
				// Every vertex gets a normal and a UV slot, zero where its
				// primitive has none; whether the mesh had any at all is
				// settled at the end. A zero normal is one the cook derives.
				anyNormals = anyNormals || nrm.count > 0
				anyUVs = anyUVs || uv.count > 0
				for v := 0; v < pos.count; v++ {
					p := pos.vec(v, 3)
					out.Positions = append(out.Positions, mulPoint3(world, mgl32.Vec3{p[0], p[1], p[2]}))
					var nn mgl32.Vec3
					if v < nrm.count {
						q := nrm.vec(v, 3)
						nn = normalM.Mul3x1(mgl32.Vec3{q[0], q[1], q[2]})
						if l := nn.Len(); l > 0 {
							nn = nn.Mul(1 / l)
						}
					}
					out.Normals = append(out.Normals, nn)
					var tc mgl32.Vec2
					if v < uv.count {
						q := uv.vec(v, 2)
						tc = mgl32.Vec2{q[0], 1 - q[1]}
					}
					out.UVs = append(out.UVs, tc)
				}
				var idx []uint32
				if prim.Indices != nil {
					iv, err := read(*prim.Indices)
					if err != nil {
						return fmt.Errorf("mesh %d primitive %d indices: %w", *n.Mesh, pi, err)
					}
					idx = make([]uint32, iv.count)
					for k := range idx {
						idx[k] = iv.index(k)
					}
				} else {
					idx = make([]uint32, pos.count)
					for k := range idx {
						idx[k] = uint32(k)
					}
				}
				tris := gltfTriangles(idx, mode)
				slot := int32(-1)
				if prim.Material != nil && *prim.Material >= 0 && *prim.Material < len(out.Slots) {
					slot = int32(*prim.Material)
				} else {
					if defaultSlot < 0 {
						defaultSlot = int32(len(out.Slots))
						out.Slots = append(out.Slots, ImportSlot{Name: "default", BaseColor: [4]float32{1, 1, 1, 1}})
					}
					slot = defaultSlot
				}
				for t := 0; t+2 < len(tris); t += 3 {
					a, b, c := tris[t], tris[t+1], tris[t+2]
					if int(a) >= pos.count || int(b) >= pos.count || int(c) >= pos.count {
						return fmt.Errorf("mesh %d primitive %d: index out of range", *n.Mesh, pi)
					}
					if flip {
						b, c = c, b // a mirroring node turns the winding round
					}
					out.Indices = append(out.Indices, base+a, base+b, base+c)
					out.TriSlot = append(out.TriSlot, slot)
				}
			}
		}
		for _, c := range n.Children {
			if err := walk(c, world, depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	var roots []int
	switch {
	case len(doc.Scenes) > 0:
		s := 0
		if doc.Scene != nil && *doc.Scene >= 0 && *doc.Scene < len(doc.Scenes) {
			s = *doc.Scene
		}
		roots = doc.Scenes[s].Nodes
	default:
		child := map[int]bool{}
		for _, n := range doc.Nodes {
			for _, c := range n.Children {
				child[c] = true
			}
		}
		for i := range doc.Nodes {
			if !child[i] {
				roots = append(roots, i)
			}
		}
		if len(doc.Nodes) == 0 {
			// No nodes at all: every mesh at the origin.
			for i := range doc.Meshes {
				doc.Nodes = append(doc.Nodes, struct {
					Mesh        *int      `json:"mesh"`
					Children    []int     `json:"children"`
					Matrix      []float32 `json:"matrix"`
					Translation []float32 `json:"translation"`
					Rotation    []float32 `json:"rotation"`
					Scale       []float32 `json:"scale"`
				}{Mesh: &i})
				roots = append(roots, i)
			}
		}
	}
	for _, r := range roots {
		if err := walk(r, mgl32.Ident4(), 0); err != nil {
			return nil, err
		}
	}
	if !anyNormals {
		out.Normals = nil
	}
	if !anyUVs {
		out.UVs = nil
	}
	if len(out.Indices) == 0 {
		return nil, fmt.Errorf("no triangles")
	}
	return out, nil
}

// splitGLB returns a GLB's JSON and BIN chunks, or the data itself as JSON
// when it is not a GLB.
func splitGLB(data []byte) (jsonBytes, bin []byte, err error) {
	if len(data) < 12 || string(data[:4]) != "glTF" {
		return data, nil, nil
	}
	if v := binary.LittleEndian.Uint32(data[4:]); v != 2 {
		return nil, nil, fmt.Errorf("GLB container version %d; only 2 is supported", v)
	}
	total := int(binary.LittleEndian.Uint32(data[8:]))
	if total > len(data) {
		return nil, nil, fmt.Errorf("GLB truncated: %d of %d bytes", len(data), total)
	}
	off := 12
	for off+8 <= total {
		n := int(binary.LittleEndian.Uint32(data[off:]))
		kind := string(data[off+4 : off+8])
		if off+8+n > total {
			return nil, nil, fmt.Errorf("GLB chunk overruns the file")
		}
		chunk := data[off+8 : off+8+n]
		switch kind {
		case "JSON":
			jsonBytes = bytes.TrimRight(chunk, " \x00")
		case "BIN\x00":
			if bin == nil {
				bin = chunk
			}
		}
		off += 8 + (n+3)&^3
	}
	if jsonBytes == nil {
		return nil, nil, fmt.Errorf("GLB has no JSON chunk")
	}
	return jsonBytes, bin, nil
}

// gltfView is one accessor's data, ready to read element by element.
type gltfView struct {
	data       []byte
	stride     int
	count      int
	comps      int
	ctype      int
	normalized bool
}

func gltfComponents(t string) int {
	switch t {
	case "SCALAR":
		return 1
	case "VEC2":
		return 2
	case "VEC3":
		return 3
	case "VEC4", "MAT2":
		return 4
	case "MAT3":
		return 9
	case "MAT4":
		return 16
	}
	return 0
}

func gltfComponentSize(ct int) int {
	switch ct {
	case gltfByte, gltfUnsignedByte:
		return 1
	case gltfShort, gltfUnsignedShort:
		return 2
	case gltfUnsignedInt, gltfFloat:
		return 4
	}
	return 0
}

func gltfAccessor(doc *gltfDoc, buffers [][]byte, ai int) (gltfView, error) {
	if ai < 0 || ai >= len(doc.Accessors) {
		return gltfView{}, fmt.Errorf("accessor %d out of range", ai)
	}
	a := &doc.Accessors[ai]
	if len(a.Sparse) > 0 && string(a.Sparse) != "null" {
		return gltfView{}, fmt.Errorf("accessor %d is sparse, which is not supported", ai)
	}
	comps, csize := gltfComponents(a.Type), gltfComponentSize(a.ComponentType)
	if comps == 0 || csize == 0 {
		return gltfView{}, fmt.Errorf("accessor %d: type %s, component %d unsupported", ai, a.Type, a.ComponentType)
	}
	v := gltfView{count: a.Count, comps: comps, ctype: a.ComponentType, normalized: a.Normalized, stride: comps * csize}
	if a.BufferView == nil {
		// All zeros, as the spec says.
		v.data = make([]byte, a.Count*v.stride)
		return v, nil
	}
	if *a.BufferView < 0 || *a.BufferView >= len(doc.BufferViews) {
		return gltfView{}, fmt.Errorf("accessor %d names buffer view %d", ai, *a.BufferView)
	}
	bv := &doc.BufferViews[*a.BufferView]
	if bv.Buffer < 0 || bv.Buffer >= len(buffers) {
		return gltfView{}, fmt.Errorf("buffer view %d names buffer %d", *a.BufferView, bv.Buffer)
	}
	if bv.ByteStride > 0 {
		v.stride = bv.ByteStride
	}
	start := bv.ByteOffset + a.ByteOffset
	need := 0
	if a.Count > 0 {
		need = (a.Count-1)*v.stride + comps*csize
	}
	buf := buffers[bv.Buffer]
	if start < 0 || start+need > len(buf) || bv.ByteOffset+bv.ByteLength > len(buf) {
		return gltfView{}, fmt.Errorf("accessor %d runs past its buffer", ai)
	}
	v.data = buf[start:]
	return v, nil
}

// comp reads component c of element i as a float, normalising integers the
// accessor says are normalised.
func (v *gltfView) comp(i, c int) float32 {
	off := i*v.stride + c*gltfComponentSize(v.ctype)
	d := v.data[off:]
	switch v.ctype {
	case gltfFloat:
		return math.Float32frombits(binary.LittleEndian.Uint32(d))
	case gltfUnsignedByte:
		if v.normalized {
			return float32(d[0]) / 255
		}
		return float32(d[0])
	case gltfByte:
		if v.normalized {
			return max(float32(int8(d[0]))/127, -1)
		}
		return float32(int8(d[0]))
	case gltfUnsignedShort:
		x := binary.LittleEndian.Uint16(d)
		if v.normalized {
			return float32(x) / 65535
		}
		return float32(x)
	case gltfShort:
		x := int16(binary.LittleEndian.Uint16(d))
		if v.normalized {
			return max(float32(x)/32767, -1)
		}
		return float32(x)
	case gltfUnsignedInt:
		return float32(binary.LittleEndian.Uint32(d))
	}
	return 0
}

// vec reads element i's first n components.
func (v *gltfView) vec(i, n int) [4]float32 {
	var out [4]float32
	for c := 0; c < n && c < v.comps; c++ {
		out[c] = v.comp(i, c)
	}
	return out
}

// index reads element i of an index accessor.
func (v *gltfView) index(i int) uint32 {
	off := i * v.stride
	d := v.data[off:]
	switch v.ctype {
	case gltfUnsignedByte:
		return uint32(d[0])
	case gltfUnsignedShort:
		return uint32(binary.LittleEndian.Uint16(d))
	case gltfUnsignedInt:
		return binary.LittleEndian.Uint32(d)
	}
	return uint32(v.comp(i, 0))
}

// gltfTriangles turns a strip or fan into a triangle list.
func gltfTriangles(idx []uint32, mode int) []uint32 {
	switch mode {
	case 5: // strip
		var out []uint32
		for i := 0; i+2 < len(idx); i++ {
			if i%2 == 0 {
				out = append(out, idx[i], idx[i+1], idx[i+2])
			} else {
				out = append(out, idx[i+1], idx[i], idx[i+2])
			}
		}
		return out
	case 6: // fan
		var out []uint32
		for i := 1; i+1 < len(idx); i++ {
			out = append(out, idx[0], idx[i], idx[i+1])
		}
		return out
	}
	return idx[:len(idx)/3*3]
}

// gltfNodeMatrix is a node's local transform: its matrix, or T * R * S.
func gltfNodeMatrix(matrix, t, r, s []float32) mgl32.Mat4 {
	if len(matrix) == 16 {
		var m mgl32.Mat4
		copy(m[:], matrix) // both column-major
		return m
	}
	out := mgl32.Ident4()
	if len(t) == 3 {
		out = mgl32.Translate3D(t[0], t[1], t[2])
	}
	if len(r) == 4 {
		q := mgl32.Quat{W: r[3], V: mgl32.Vec3{r[0], r[1], r[2]}}.Normalize()
		out = out.Mul4(q.Mat4())
	}
	if len(s) == 3 {
		out = out.Mul4(mgl32.Scale3D(s[0], s[1], s[2]))
	}
	return out
}

func mulPoint3(m mgl32.Mat4, p mgl32.Vec3) mgl32.Vec3 {
	r := m.Mul4x1(p.Vec4(1))
	return mgl32.Vec3{r[0], r[1], r[2]}
}

// gltfTextureName is an image's engine texture name: its URI's base name
// without the extension ("textures/rock_albedo.png" → "rock_albedo"), as
// OBJ's map_Kd is read. An embedded image (data URI or buffer view) goes by
// its name, if it has one.
func gltfTextureName(uri, name string) string {
	if uri != "" && !strings.HasPrefix(uri, "data:") {
		return trimModelExt(filepath.Base(filepath.FromSlash(unescapeURI(uri))))
	}
	return name
}

// unescapeURI undoes the percent-escapes a relative URI may carry.
func unescapeURI(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			var v byte
			if _, err := fmt.Sscanf(s[i+1:i+3], "%02x", &v); err == nil {
				b.WriteByte(v)
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// ---------- writing ----------

// EncodeGLB writes m as a binary glTF: one buffer, one mesh, one primitive
// per material slot, each slot a material (its texture an image URI of the
// texture's name plus ".png", which is how LoadGLTF reads it back). The
// inverse of LoadGLTF for what an ImportMesh holds — for tests, and for
// generating dense assets to cook.
func EncodeGLB(m *ImportMesh) ([]byte, error) {
	if len(m.Indices) == 0 || len(m.TriSlot) != len(m.Indices)/3 {
		return nil, fmt.Errorf("EncodeGLB: %d indices, %d triangle slots", len(m.Indices), len(m.TriSlot))
	}
	var bin bytes.Buffer
	put := func(v any) { _ = binary.Write(&bin, binary.LittleEndian, v) }
	align := func() {
		for bin.Len()%4 != 0 {
			bin.WriteByte(0)
		}
	}
	type view struct {
		Buffer     int `json:"buffer"`
		ByteOffset int `json:"byteOffset"`
		ByteLength int `json:"byteLength"`
	}
	type accessor struct {
		BufferView    int       `json:"bufferView"`
		ComponentType int       `json:"componentType"`
		Count         int       `json:"count"`
		Type          string    `json:"type"`
		Min           []float32 `json:"min,omitempty"`
		Max           []float32 `json:"max,omitempty"`
	}
	var views []view
	var accs []accessor
	addView := func(write func()) int {
		align()
		start := bin.Len()
		write()
		views = append(views, view{ByteOffset: start, ByteLength: bin.Len() - start})
		return len(views) - 1
	}
	n := len(m.Positions)
	lo, hi := m.Positions[0], m.Positions[0]
	for _, p := range m.Positions {
		lo = mgl32.Vec3{min(lo[0], p[0]), min(lo[1], p[1]), min(lo[2], p[2])}
		hi = mgl32.Vec3{max(hi[0], p[0]), max(hi[1], p[1]), max(hi[2], p[2])}
	}
	posView := addView(func() {
		for _, p := range m.Positions {
			put(p)
		}
	})
	accs = append(accs, accessor{BufferView: posView, ComponentType: gltfFloat, Count: n, Type: "VEC3", Min: lo[:], Max: hi[:]})
	attrs := map[string]int{"POSITION": 0}
	if len(m.Normals) == n {
		v := addView(func() {
			for _, q := range m.Normals {
				put(q)
			}
		})
		attrs["NORMAL"] = len(accs)
		accs = append(accs, accessor{BufferView: v, ComponentType: gltfFloat, Count: n, Type: "VEC3"})
	}
	if len(m.UVs) == n {
		v := addView(func() {
			for _, q := range m.UVs {
				put(mgl32.Vec2{q[0], 1 - q[1]})
			}
		})
		attrs["TEXCOORD_0"] = len(accs)
		accs = append(accs, accessor{BufferView: v, ComponentType: gltfFloat, Count: n, Type: "VEC2"})
	}
	type prim struct {
		Attributes map[string]int `json:"attributes"`
		Indices    int            `json:"indices"`
		Material   int            `json:"material"`
	}
	var prims []prim
	for si := range m.Slots {
		var idx []uint32
		for t, s := range m.TriSlot {
			if int(s) == si {
				idx = append(idx, m.Indices[3*t:3*t+3]...)
			}
		}
		if len(idx) == 0 {
			continue
		}
		v := addView(func() { put(idx) })
		prims = append(prims, prim{Attributes: attrs, Indices: len(accs), Material: si})
		accs = append(accs, accessor{BufferView: v, ComponentType: gltfUnsignedInt, Count: len(idx), Type: "SCALAR"})
	}
	align()
	type texRef struct {
		Index int `json:"index"`
	}
	type pbr struct {
		BaseColorFactor  []float32 `json:"baseColorFactor"`
		BaseColorTexture *texRef   `json:"baseColorTexture,omitempty"`
	}
	type material struct {
		Name                 string `json:"name"`
		DoubleSided          bool   `json:"doubleSided,omitempty"`
		PbrMetallicRoughness pbr    `json:"pbrMetallicRoughness"`
	}
	type image struct {
		URI string `json:"uri"`
	}
	type texture struct {
		Source int `json:"source"`
	}
	var mats []material
	var images []image
	var textures []texture
	for _, s := range m.Slots {
		mat := material{Name: s.Name, DoubleSided: s.DoubleSided, PbrMetallicRoughness: pbr{BaseColorFactor: s.BaseColor[:]}}
		if s.Texture != "" {
			images = append(images, image{URI: s.Texture + ".png"})
			textures = append(textures, texture{Source: len(images) - 1})
			mat.PbrMetallicRoughness.BaseColorTexture = &texRef{Index: len(textures) - 1}
		}
		mats = append(mats, mat)
	}
	doc := map[string]any{
		"asset":       map[string]string{"version": "2.0", "generator": "foundry"},
		"scene":       0,
		"scenes":      []map[string]any{{"nodes": []int{0}}},
		"nodes":       []map[string]any{{"mesh": 0}},
		"meshes":      []map[string]any{{"primitives": prims}},
		"accessors":   accs,
		"bufferViews": views,
		"buffers":     []map[string]int{{"byteLength": bin.Len()}},
		"materials":   mats,
	}
	if len(images) > 0 {
		doc["images"] = images
		doc["textures"] = textures
	}
	js, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	for len(js)%4 != 0 {
		js = append(js, ' ')
	}
	var out bytes.Buffer
	out.WriteString("glTF")
	put32 := func(v uint32) { _ = binary.Write(&out, binary.LittleEndian, v) }
	put32(2)
	put32(uint32(12 + 8 + len(js) + 8 + bin.Len()))
	put32(uint32(len(js)))
	out.WriteString("JSON")
	out.Write(js)
	put32(uint32(bin.Len()))
	out.WriteString("BIN\x00")
	out.Write(bin.Bytes())
	return out.Bytes(), nil
}
