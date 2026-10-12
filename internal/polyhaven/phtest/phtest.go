// Package phtest serves a made-up Poly Haven from an httptest server, for
// the tests of every package that downloads (phimport, the procgen stages,
// the editor's browser) — never the live API.
//
// Its assets:
//
//   - t1 (model, rocks): a curved rock face 6 m across and 4 m tall, scanned
//     from +X (open at the back), with a 0.5 m leaf card whose material is a
//     MASK cutout with a leaf_alpha map (left half opaque, right half cut);
//     its glTF, .bin and 32 px JPG textures at 1k.
//   - p1 (model, plants): the same files under another ID, for a plant stage.
//   - s2 (model, rocks): t1's mesh twice, as two root nodes 30 m apart (a
//     set of variations, imported as two parts).
//   - sand (texture): Diffuse, nor_gl, arm and Displacement JPGs at 2k.
//
// plus /assets (the catalog), /info and /files for each, and thumbnails.
package phtest

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"foundry/internal/polyhaven"
)

// Server is the fake.
type Server struct {
	*httptest.Server
	files map[string][]byte // URL path → bytes
	mu    sync.Mutex
	hits  int
}

// Hits is how many requests it has answered.
func (a *Server) Hits() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hits
}

// Client is a client on the fake with its own cache directory.
func (a *Server) Client(t testing.TB) *polyhaven.Client {
	return &polyhaven.Client{API: a.URL, CDN: a.URL, HTTP: a.Server.Client(), CacheDir: t.TempDir(), TTL: time.Hour,
		Retries: 1, Backoff: time.Millisecond}
}

func md5hex(b []byte) string { s := md5.Sum(b); return hex.EncodeToString(s[:]) }

func jpegOf(w, h int, f func(x, y int) color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, f(x, y))
		}
	}
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: 95})
	return b.Bytes()
}

// rockFace is a curved sheet: a half cylinder of radius 3 about +Y opening
// toward −X (its outside faces +X), 4 m tall, plus a 0.5 m leaf quad.
func rockFace() (pos, nrm []float32, uv []float32, idx []uint16, leafFirst int) {
	const seg, rows = 24, 8
	for j := 0; j <= rows; j++ {
		y := 4 * float64(j) / rows
		for i := 0; i <= seg; i++ {
			a := -math.Pi/2 + math.Pi*float64(i)/seg
			x, z := 3*math.Cos(a), 3*math.Sin(a)
			pos = append(pos, float32(x+10), float32(y-2), float32(z))
			nrm = append(nrm, float32(math.Cos(a)), 0, float32(math.Sin(a)))
			uv = append(uv, float32(i)/seg, float32(j)/rows)
		}
	}
	for j := 0; j < rows; j++ {
		for i := 0; i < seg; i++ {
			a := uint16(j*(seg+1) + i)
			b, c, d := a+1, a+uint16(seg+1), a+uint16(seg+2)
			// Outward (+radial) winding.
			idx = append(idx, a, c, b, b, c, d)
		}
	}
	leafFirst = len(idx)
	base := uint16(len(pos) / 3)
	for _, p := range [][3]float32{{13, 2, -0.25}, {13, 2, 0.25}, {13.5, 2.5, 0.25}, {13.5, 2.5, -0.25}} {
		pos = append(pos, p[0], p[1], p[2])
		nrm = append(nrm, 0, 1, 0)
	}
	uv = append(uv, 0, 0, 1, 0, 1, 1, 0, 1)
	idx = append(idx, base, base+1, base+2, base, base+2, base+3)
	return
}

// New starts the fake (closed when t ends).
func New(t testing.TB) *Server {
	a := &Server{files: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		a.hits++
		key := r.URL.Path
		if key == "/assets" {
			key += "?" + r.URL.Query().Get("t") + "&" + r.URL.Query().Get("c")
		}
		if strings.HasPrefix(key, "/asset_img/thumbs/") {
			key = "/thumb"
		}
		data, ok := a.files[key]
		a.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	a.Server = srv

	pos, nrm, uv, idx, leafFirst := rockFace()
	var bin bytes.Buffer
	binary.Write(&bin, binary.LittleEndian, pos)
	binary.Write(&bin, binary.LittleEndian, nrm)
	binary.Write(&bin, binary.LittleEndian, uv)
	binary.Write(&bin, binary.LittleEndian, idx)
	np, nn, nu := len(pos)*4, len(nrm)*4, len(uv)*4
	nv := len(pos) / 3
	gltf := map[string]any{
		"asset":  map[string]any{"version": "2.0"},
		"scene":  0,
		"scenes": []any{map[string]any{"nodes": []int{0}}},
		"nodes":  []any{map[string]any{"mesh": 0}},
		"meshes": []any{map[string]any{"primitives": []any{
			map[string]any{"attributes": map[string]int{"POSITION": 0, "NORMAL": 1, "TEXCOORD_0": 2}, "indices": 3, "material": 0},
			map[string]any{"attributes": map[string]int{"POSITION": 0, "NORMAL": 1, "TEXCOORD_0": 2}, "indices": 4, "material": 1},
		}}},
		"accessors": []any{
			map[string]any{"bufferView": 0, "componentType": 5126, "count": nv, "type": "VEC3"},
			map[string]any{"bufferView": 1, "componentType": 5126, "count": nv, "type": "VEC3"},
			map[string]any{"bufferView": 2, "componentType": 5126, "count": nv, "type": "VEC2"},
			map[string]any{"bufferView": 3, "componentType": 5123, "count": leafFirst, "type": "SCALAR"},
			map[string]any{"bufferView": 3, "byteOffset": leafFirst * 2, "componentType": 5123, "count": len(idx) - leafFirst, "type": "SCALAR"},
		},
		"bufferViews": []any{
			map[string]any{"buffer": 0, "byteOffset": 0, "byteLength": np},
			map[string]any{"buffer": 0, "byteOffset": np, "byteLength": nn},
			map[string]any{"buffer": 0, "byteOffset": np + nn, "byteLength": nu},
			map[string]any{"buffer": 0, "byteOffset": np + nn + nu, "byteLength": len(idx) * 2},
		},
		"buffers": []any{map[string]any{"uri": "t1.bin", "byteLength": bin.Len()}},
		"materials": []any{
			map[string]any{"name": "t1_rock", "normalTexture": map[string]int{"index": 0},
				"pbrMetallicRoughness": map[string]any{"baseColorTexture": map[string]int{"index": 1}, "metallicRoughnessTexture": map[string]int{"index": 2}}},
			map[string]any{"name": "t1_leaf", "alphaMode": "MASK", "alphaCutoff": 0.5, "doubleSided": true,
				"pbrMetallicRoughness": map[string]any{"baseColorTexture": map[string]int{"index": 3}}},
		},
		"textures": []any{map[string]int{"source": 0}, map[string]int{"source": 1}, map[string]int{"source": 2}, map[string]int{"source": 3}},
		"images": []any{
			map[string]string{"uri": "textures/t1_rock_nor_gl_1k.jpg"}, map[string]string{"uri": "textures/t1_rock_diff_1k.jpg"},
			map[string]string{"uri": "textures/t1_rock_arm_1k.jpg"}, map[string]string{"uri": "textures/t1_leaf_diff_1k.jpg"},
		},
	}
	gj, _ := json.Marshal(gltf)
	tex := func(c color.RGBA) []byte { return jpegOf(32, 32, func(x, y int) color.RGBA { return c }) }
	a.files["/dl/t1.gltf"] = gj
	a.files["/dl/t1.bin"] = bin.Bytes()
	a.files["/dl/rock_diff.jpg"] = tex(color.RGBA{180, 90, 60, 255})
	a.files["/dl/rock_nor.jpg"] = tex(color.RGBA{128, 60, 230, 255}) // nor_gl: green under half, a bump facing −Y
	a.files["/dl/rock_arm.jpg"] = tex(color.RGBA{255, 200, 0, 255})
	a.files["/dl/leaf_diff.jpg"] = tex(color.RGBA{60, 140, 50, 255})
	a.files["/dl/leaf_alpha.jpg"] = jpegOf(32, 32, func(x, y int) color.RGBA {
		if x < 16 {
			return color.RGBA{255, 255, 255, 255}
		}
		return color.RGBA{0, 0, 0, 255}
	})
	a.files["/dl/sand_diff.jpg"] = tex(color.RGBA{200, 120, 80, 255})
	a.files["/dl/sand_nor.jpg"] = tex(color.RGBA{128, 128, 255, 255})
	a.files["/dl/sand_arm.jpg"] = tex(color.RGBA{230, 180, 0, 255})
	a.files["/dl/sand_disp.jpg"] = tex(color.RGBA{100, 100, 100, 255})
	f := func(p string) map[string]any {
		return map[string]any{"url": srv.URL + p, "md5": md5hex(a.files[p]), "size": len(a.files[p])}
	}
	gf := f("/dl/t1.gltf")
	gf["include"] = map[string]any{
		"t1.bin": f("/dl/t1.bin"), "textures/t1_rock_diff_1k.jpg": f("/dl/rock_diff.jpg"),
		"textures/t1_rock_nor_gl_1k.jpg": f("/dl/rock_nor.jpg"), "textures/t1_rock_arm_1k.jpg": f("/dl/rock_arm.jpg"),
		"textures/t1_leaf_diff_1k.jpg": f("/dl/leaf_diff.jpg"),
	}
	files, _ := json.Marshal(map[string]any{
		"gltf":       map[string]any{"1k": map[string]any{"gltf": gf}},
		"leaf_alpha": map[string]any{"1k": map[string]any{"jpg": f("/dl/leaf_alpha.jpg")}},
	})
	a.files["/files/t1"] = files
	// s2: the same mesh twice, as two root nodes (a set of variations).
	gltf["nodes"] = []any{map[string]any{"mesh": 0, "name": "s2_a"}, map[string]any{"mesh": 0, "name": "s2_b", "translation": []float64{30, 0, 0}}}
	gltf["scenes"] = []any{map[string]any{"nodes": []int{0, 1}}}
	g2, _ := json.Marshal(gltf)
	a.files["/dl/s2.gltf"] = g2
	gf2 := f("/dl/s2.gltf")
	gf2["include"] = gf["include"]
	files2, _ := json.Marshal(map[string]any{"gltf": map[string]any{"1k": map[string]any{"gltf": gf2}}})
	a.files["/files/s2"] = files2
	a.files["/info/s2"] = []byte(`{"name":"Two Faces","type":2,"categories":["rocks"],"tags":["cliff"],"authors":{"Ann":"All"},"polycount":800}`)
	a.files["/info/t1"] = []byte(`{"name":"Test Face","type":2,"categories":["rocks"],"tags":["cliff"],"authors":{"Ann":"All"},"polycount":400,"dimensions":[6000,4000,3000]}`)
	sfiles, _ := json.Marshal(map[string]any{
		"Diffuse":      map[string]any{"2k": map[string]any{"jpg": f("/dl/sand_diff.jpg")}},
		"nor_gl":       map[string]any{"2k": map[string]any{"jpg": f("/dl/sand_nor.jpg")}},
		"arm":          map[string]any{"2k": map[string]any{"jpg": f("/dl/sand_arm.jpg")}},
		"Displacement": map[string]any{"2k": map[string]any{"jpg": f("/dl/sand_disp.jpg")}},
	})
	a.files["/files/sand"] = sfiles
	a.files["/info/sand"] = []byte(`{"name":"Sand","type":1,"categories":["sand"],"tags":["red"],"authors":{"Bo":"All"},"dimensions":[3000,3000]}`)
	a.files["/files/p1"] = a.files["/files/t1"]
	a.files["/info/p1"] = []byte(`{"name":"Test Plant","type":2,"categories":["plants"],"tags":["desert"],"authors":{"Cy":"All"},"polycount":400}`)
	models := `{"t1":{"name":"Test Face","type":2,"categories":["rocks"],"tags":["cliff","desert"],"polycount":400,"download_count":5},` +
		`"p1":{"name":"Test Plant","type":2,"categories":["plants"],"tags":["desert"],"polycount":400,"download_count":9}}`
	a.files["/assets?models&"] = []byte(models)
	a.files["/assets?models&rocks"] = []byte(`{"t1":{"name":"Test Face","type":2,"categories":["rocks"],"tags":["cliff","desert"],"polycount":400}}`)
	a.files["/assets?models&plants"] = []byte(`{"p1":{"name":"Test Plant","type":2,"categories":["plants"],"tags":["desert"],"polycount":400}}`)
	a.files["/assets?textures&"] = []byte(`{"sand":{"name":"Sand","type":1,"categories":["sand"],"tags":["red"]}}`)
	var th bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	png.Encode(&th, img)
	a.files["/thumb"] = th.Bytes()
	return a
}
