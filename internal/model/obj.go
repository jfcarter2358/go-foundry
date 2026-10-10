package model

// obj.go — the flat-buffer OBJ helper.
//
// The hand-rolled parser that used to live here was replaced by a thin wrapper
// over LoadOBJMesh (model_obj.go) when entities gained per-material submeshes.
// Two OBJ parsers in one binary is one too many: they drifted on exactly the
// details that matter (negative indices, n-gons, materials), and the reader that
// backs the model editor is the one that has to be right.
//
// This form remains useful where a caller wants nothing but triangles ready for
// the GPU and has no interest in faces or materials.

// LoadOBJ parses a Wavefront OBJ and returns a flat, de-indexed vertex buffer
// of [X, Y, Z, U, V] records — the layout Geometry uses. Faces are
// triangulated; material assignments are discarded, so everything comes back as
// one buffer. Use LoadOBJMesh directly to keep faces and materials.
func LoadOBJ(path string) ([]float32, error) {
	doc, err := LoadOBJMesh(path)
	if err != nil {
		return nil, err
	}
	var out []float32
	for fi := range doc.Faces {
		f := &doc.Faces[fi]
		for _, tri := range doc.FaceTriangles(fi) {
			for _, li := range tri {
				p := doc.Verts[f.Verts[li]]
				uv := f.UvAt(li)
				out = append(out, p.X(), p.Y(), p.Z(), uv.X(), uv.Y())
			}
		}
	}
	return out, nil
}
