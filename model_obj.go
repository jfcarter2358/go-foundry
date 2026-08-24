package main

// model_obj.go — Wavefront OBJ import and export for the model editor.
//
// obj.go's LoadOBJ is a renderer-facing loader: it fan-triangulates everything
// into the flat [X,Y,Z,U,V] buffer Geometry wants, discards vn, and has no
// concept of materials. That is the right shape for handing a mesh straight to
// the GPU and the wrong shape for editing one — the faces the user wants to
// select and extrude are exactly what it throws away.
//
// LoadOBJMesh below reads into a ModelDoc instead: n-gons stay n-gons, usemtl
// becomes a per-face texture, and shared vertices stay shared so dragging one
// corner moves every face that meets there.
//
// SaveOBJ is the repo's first OBJ writer.

import (
	"bufio"
	"fmt"
	"foundry/internal/fileutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

// LoadOBJMesh parses an OBJ file into an editable document.
//
// Supported: v, vt, f (with v, v/vt, v//vn and v/vt/vn forms), negative
// (relative) indices, usemtl, mtllib, o and g. vn is parsed but not stored —
// the editor derives normals from the mesh (smooth.go), so a file's own normals
// would go stale the first time a vertex moved.
func LoadOBJMesh(path string) (*ModelDoc, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		positions []mgl32.Vec3
		texCoords []mgl32.Vec2
		doc       = &ModelDoc{ID: trimModelExt(filepath.Base(path))}
		curTex    = ""
		mtlTex    = map[string]string{}
	)

	// resolveIdx maps an OBJ index onto a slice position. OBJ is 1-based, and a
	// negative index counts back from the most recently defined element.
	resolveIdx := func(raw, n int) (int, bool) {
		switch {
		case raw > 0:
			return raw - 1, raw-1 < n
		case raw < 0:
			return n + raw, n+raw >= 0
		default:
			return 0, false
		}
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		parts := strings.Fields(text)
		switch parts[0] {
		case "v":
			if len(parts) < 4 {
				continue
			}
			var p mgl32.Vec3
			for i := 0; i < 3; i++ {
				val, err := strconv.ParseFloat(parts[i+1], 32)
				if err != nil {
					return nil, fmt.Errorf("%s:%d: bad vertex component %q: %w", path, line, parts[i+1], err)
				}
				p[i] = float32(val)
			}
			positions = append(positions, p)

		case "vt":
			if len(parts) < 3 {
				continue
			}
			var uv mgl32.Vec2
			for i := 0; i < 2; i++ {
				val, err := strconv.ParseFloat(parts[i+1], 32)
				if err != nil {
					return nil, fmt.Errorf("%s:%d: bad texcoord %q: %w", path, line, parts[i+1], err)
				}
				uv[i] = float32(val)
			}
			texCoords = append(texCoords, uv)

		case "mtllib":
			// A missing or unreadable .mtl is not fatal: the geometry is still
			// worth importing, the faces just fall back to the default texture.
			if len(parts) >= 2 {
				for k, v := range loadMTLTextures(filepath.Join(filepath.Dir(path), parts[1])) {
					mtlTex[k] = v
				}
			}

		case "usemtl":
			if len(parts) >= 2 {
				curTex = parts[1]
			}

		case "o", "g":
			if len(parts) >= 2 && doc.ID == "" {
				doc.ID = parts[1]
			}

		case "f":
			if len(parts) < 4 {
				continue
			}
			face := ModelFace{}
			// One texture per document, so the first material the file actually
			// uses on a face wins. An OBJ that switches material partway through
			// loses the later ones — the geometry and UVs are what matter here,
			// and the model can be repointed in one click afterwards.
			if curTex != "" && doc.Texture == "" {
				if mapped, ok := mtlTex[curTex]; ok && mapped != "" {
					doc.Texture = mapped
				} else {
					doc.Texture = curTex
				}
			}
			for _, token := range parts[1:] {
				toks := strings.Split(token, "/")
				vRaw, err := strconv.Atoi(toks[0])
				if err != nil {
					return nil, fmt.Errorf("%s:%d: bad face vertex index %q: %w", path, line, toks[0], err)
				}
				vi, ok := resolveIdx(vRaw, len(positions))
				if !ok {
					return nil, fmt.Errorf("%s:%d: face references vertex %d (only %d defined)", path, line, vRaw, len(positions))
				}
				face.Verts = append(face.Verts, vi)

				var uv mgl32.Vec2
				if len(toks) >= 2 && toks[1] != "" {
					tRaw, err := strconv.Atoi(toks[1])
					if err != nil {
						return nil, fmt.Errorf("%s:%d: bad face texcoord index %q: %w", path, line, toks[1], err)
					}
					if ti, ok := resolveIdx(tRaw, len(texCoords)); ok {
						uv = texCoords[ti]
					}
				}
				face.UVs = append(face.UVs, uv)
			}
			if len(face.Verts) >= 3 {
				doc.Faces = append(doc.Faces, face)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(positions) == 0 {
		return nil, fmt.Errorf("%s: no vertices", path)
	}

	doc.Verts = positions
	doc.Path = "" // an imported OBJ has no .fmdl home yet; Save prompts for one
	doc.invalidate()
	return doc, nil
}

// loadMTLTextures maps material names to logical texture names by reading each
// newmtl's map_Kd and stripping it back to a bare basename — "textures/brick.png"
// becomes "brick", which is what Wall.Texture and ModelDoc.Texture hold.
// Returns an empty map if the file cannot be read.
func loadMTLTextures(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()

	cur := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.Fields(strings.TrimSpace(scanner.Text()))
		if len(parts) < 2 {
			continue
		}
		switch parts[0] {
		case "newmtl":
			cur = parts[1]
			out[cur] = ""
		case "map_Kd":
			if cur != "" {
				out[cur] = trimModelExt(filepath.Base(parts[len(parts)-1]))
			}
		}
	}
	return out
}

// SaveOBJ writes the document as OBJ, with a sibling .mtl naming its texture.
//
// The whole mesh is one material, so there is a single usemtl line — which is
// also the shape OBJ is happiest with, the material being a mode that persists
// until the next usemtl rather than a per-face slot.
func SaveOBJ(m *ModelDoc, path string) error {
	if len(m.Verts) == 0 {
		return fmt.Errorf("model %q has no geometry to export", m.ID)
	}

	// UVs are per face corner here but per file in OBJ, so each corner gets its
	// own vt entry. Deduplicating them would save bytes and cost clarity.
	var b strings.Builder
	mtlName := trimModelExt(filepath.Base(path)) + ".mtl"
	fmt.Fprintf(&b, "# exported by Foundry model editor\n")
	fmt.Fprintf(&b, "mtllib %s\n", mtlName)
	fmt.Fprintf(&b, "o %s\n", objSafeName(m.ID))

	for _, p := range m.Verts {
		fmt.Fprintf(&b, "v %s %s %s\n", objF(p.X()), objF(p.Y()), objF(p.Z()))
	}

	tex := m.TextureName()

	// vt indices are assigned as they are written, so the face lines below have
	// to be built in the same pass.
	var faceLines strings.Builder
	fmt.Fprintf(&faceLines, "usemtl %s\n", tex)
	vtIdx := 1
	for fi := range m.Faces {
		f := &m.Faces[fi]
		faceLines.WriteString("f")
		for i, v := range f.Verts {
			uv := f.uvAt(i)
			fmt.Fprintf(&b, "vt %s %s\n", objF(uv.X()), objF(uv.Y()))
			fmt.Fprintf(&faceLines, " %d/%d", v+1, vtIdx)
			vtIdx++
		}
		faceLines.WriteString("\n")
	}
	b.WriteString(faceLines.String())

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create export directory: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, []byte(b.String())); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	var mtl strings.Builder
	mtl.WriteString("# exported by Foundry model editor\n")
	fmt.Fprintf(&mtl, "newmtl %s\nKd 1.000 1.000 1.000\nmap_Kd %s/%s.png\n\n",
		tex, fileutil.TexturesDirName, tex)
	mtlPath := filepath.Join(filepath.Dir(path), mtlName)
	if err := fileutil.WriteFileAtomic(mtlPath, []byte(mtl.String())); err != nil {
		return fmt.Errorf("write %s: %w", mtlPath, err)
	}
	return nil
}

// objF formats a float for OBJ: 'f' never emits an exponent (which some OBJ
// readers handle poorly) and precision -1 gives the shortest form that still
// round-trips, so there are no trailing zeros to trim.
func objF(v float32) string {
	return strconv.FormatFloat(float64(v), 'f', -1, 32)
}

// objSafeName strips whitespace from an object name, which OBJ treats as a
// field separator.
func objSafeName(s string) string {
	if s == "" {
		return "model"
	}
	return strings.Join(strings.Fields(s), "_")
}

// objExt is the interchange format's extension, shared by the model editor's
// import/export dialogs and the level editor's model catalog.
const objExt = ".obj"

// ScanOBJFiles lists the .obj files available to the import dialog.
func ScanOBJFiles() []string { return fileutil.ScanFilesByExt(modelsDir(), objExt) }
