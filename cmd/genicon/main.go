// Command genicon draws the Foundry editor's icon and writes every form it ships
// in.
//
//	go run ./cmd/genicon
//
// The icon is a steel cube — the engine's world — with its top cast as a pool of
// molten metal, filled from a crucible tipping in from the upper left. It is
// drawn as vectors at each size rather than scaled down from one large bitmap,
// so the small sizes can drop detail that would only turn to mud: below 64px
// the crucible and its stream go, and at 16px the steel rim round the pool goes
// too.
//
// Outputs:
//
//	packaging/icon/foundry-<size>.png     16 … 512, for desktop entries and docs
//	packaging/icon/foundry.ico            16 … 256, every size in one file
//	internal/editoricon/foundry.png       the window icon, embedded by the editor
//	internal/editoricon/rsrc_windows_amd64.syso
//	                                      the .ico as a Windows resource, so the
//	                                      editor .exe, its shortcuts and its
//	                                      Add/Remove Programs entry carry it
//
// The .syso needs mingw's windres (the same toolchain that cross-builds the
// Windows editor); without it the rest is still written and the old .syso kept.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/image/vector"
)

var (
	pngSizes = []int{16, 24, 32, 48, 64, 128, 256, 512}
	icoSizes = []int{16, 24, 32, 48, 64, 128, 256}
)

func main() {
	iconDir := flag.String("out", "packaging/icon", "where the PNGs and .ico go")
	embedDir := flag.String("embed", "internal/editoricon", "the editor's icon package")
	windres := flag.String("windres", "x86_64-w64-mingw32-windres", "windres to build the .syso with")
	flag.Parse()

	for _, d := range []string{*iconDir, *embedDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			log.Fatal(err)
		}
	}

	pngs := map[int][]byte{}
	for _, s := range pngSizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, draw(s)); err != nil {
			log.Fatal(err)
		}
		pngs[s] = buf.Bytes()
		write(filepath.Join(*iconDir, fmt.Sprintf("foundry-%d.png", s)), pngs[s])
	}
	write(filepath.Join(*embedDir, "foundry.png"), pngs[256])

	ico := filepath.Join(*iconDir, "foundry.ico")
	write(ico, encodeICO(pngs))

	// windres resolves the ICON path relative to where it runs, so the .rc is
	// written beside the .ico and windres is run there.
	rc := filepath.Join(*iconDir, "foundry.rc")
	write(rc, []byte("1 ICON \"foundry.ico\"\n"))
	syso, err := filepath.Abs(filepath.Join(*embedDir, "rsrc_windows_amd64.syso"))
	if err != nil {
		log.Fatal(err)
	}
	cmd := exec.Command(*windres, "-i", "foundry.rc", "-O", "coff", "-o", syso)
	cmd.Dir = *iconDir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("genicon: %s failed (%v) — %s not rebuilt", *windres, err, syso)
		return
	}
	fmt.Println(syso)
}

func write(path string, b []byte) {
	if err := os.WriteFile(path, b, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println(path)
}

// encodeICO packs PNG images into one .ico. PNG-in-ICO has been understood
// since Vista, and keeps the 256px entry a few KiB instead of a quarter meg.
func encodeICO(pngs map[int][]byte) []byte {
	var hdr, body bytes.Buffer
	binary.Write(&hdr, binary.LittleEndian, [3]uint16{0, 1, uint16(len(icoSizes))})
	offset := 6 + 16*len(icoSizes)
	for _, s := range icoSizes {
		dim := byte(s)
		if s >= 256 {
			dim = 0 // 0 means 256 in an ICONDIRENTRY
		}
		data := pngs[s]
		hdr.Write([]byte{dim, dim, 0, 0})
		binary.Write(&hdr, binary.LittleEndian, [2]uint16{1, 32})
		binary.Write(&hdr, binary.LittleEndian, [2]uint32{uint32(len(data)), uint32(offset)})
		body.Write(data)
		offset += len(data)
	}
	return append(hdr.Bytes(), body.Bytes()...)
}

// ---------------------------------------------------------------------------
// Drawing. Everything is laid out in a unit square and scaled at the end.

type pt struct{ x, y float64 }

func (a pt) add(b pt) pt         { return pt{a.x + b.x, a.y + b.y} }
func (a pt) sub(b pt) pt         { return pt{a.x - b.x, a.y - b.y} }
func (a pt) mul(k float64) pt    { return pt{a.x * k, a.y * k} }
func lerp(a, b pt, t float64) pt { return a.add(b.sub(a).mul(t)) }
func (a pt) rot(r float64) pt    { s, c := math.Sincos(r); return pt{a.x*c - a.y*s, a.x*s + a.y*c} }
func (a pt) len() float64        { return math.Hypot(a.x, a.y) }
func (a pt) norm() pt            { return a.mul(1 / a.len()) }
func (a pt) perp() pt            { return pt{-a.y, a.x} }

type canvas struct {
	img  *image.RGBA
	size float64
}

// fill paints the polygon with src, sampled in unit coordinates.
func (c *canvas) fill(poly []pt, src func(p pt) color.NRGBA) {
	s := int(c.size)
	r := vector.NewRasterizer(s, s)
	r.MoveTo(float32(poly[0].x*c.size), float32(poly[0].y*c.size))
	for _, p := range poly[1:] {
		r.LineTo(float32(p.x*c.size), float32(p.y*c.size))
	}
	r.ClosePath()
	r.Draw(c.img, c.img.Bounds(), shader{c.size, src}, image.Point{})
}

type shader struct {
	size float64
	f    func(p pt) color.NRGBA
}

func (shader) ColorModel() color.Model { return color.NRGBAModel }
func (shader) Bounds() image.Rectangle {
	return image.Rect(-1e9, -1e9, 1e9, 1e9)
}
func (s shader) At(x, y int) color.Color {
	return s.f(pt{(float64(x) + 0.5) / s.size, (float64(y) + 0.5) / s.size})
}

func solid(c color.NRGBA) func(pt) color.NRGBA { return func(pt) color.NRGBA { return c } }

func hex(v uint32) color.NRGBA {
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
}

func mix(a, b color.NRGBA, t float64) color.NRGBA {
	t = math.Max(0, math.Min(1, t))
	m := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return color.NRGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), m(a.A, b.A)}
}

// ramp maps t in [0,1] across evenly spaced stops.
func ramp(t float64, stops ...color.NRGBA) color.NRGBA {
	t = math.Max(0, math.Min(1, t)) * float64(len(stops)-1)
	i := int(t)
	if i >= len(stops)-1 {
		return stops[len(stops)-1]
	}
	return mix(stops[i], stops[i+1], t-float64(i))
}

func roundRect(x0, y0, x1, y1, r float64) []pt {
	var out []pt
	corner := func(cx, cy, a0 float64) {
		for i := 0; i <= 8; i++ {
			a := a0 + float64(i)/8*math.Pi/2
			out = append(out, pt{cx + r*math.Cos(a), cy + r*math.Sin(a)})
		}
	}
	corner(x1-r, y0+r, -math.Pi/2)
	corner(x1-r, y1-r, 0)
	corner(x0+r, y1-r, math.Pi/2)
	corner(x0+r, y0+r, math.Pi)
	return out
}

func ellipse(c pt, rx, ry float64) []pt {
	out := make([]pt, 48)
	for i := range out {
		a := float64(i) / 48 * 2 * math.Pi
		out[i] = pt{c.x + rx*math.Cos(a), c.y + ry*math.Sin(a)}
	}
	return out
}

// Palette.
var (
	bgTop      = hex(0x343b4a)
	bgBottom   = hex(0x14171e)
	steelTop   = hex(0x9aa6ba)
	steelL0    = hex(0x566175)
	steelL1    = hex(0x2c323e)
	steelR0    = hex(0x7a869b)
	steelR1    = hex(0x485266)
	moltenHot  = hex(0xfff4c2)
	moltenMid  = hex(0xffb42e)
	moltenCool = hex(0xf2541b)
)

// molten is the pool/stream colour: white-hot at t=0, cooling to red at t=1.
func molten(t float64) color.NRGBA { return ramp(t, moltenHot, moltenMid, moltenCool) }

func draw(size int) *image.RGBA {
	c := &canvas{img: image.NewRGBA(image.Rect(0, 0, size, size)), size: float64(size)}
	detailed := size >= 64
	tiny := size <= 16

	// Tile. At 16px the tile runs to the edge; a margin there is a pixel of
	// nothing on every side of a picture that has none to spare.
	m, r := 0.04, 0.2
	if tiny {
		m, r = 0, 0.18
	}
	c.fill(roundRect(m, m, 1-m, 1-m, r), func(p pt) color.NRGBA {
		return mix(bgTop, bgBottom, (p.y-m)/(1-2*m))
	})

	// The cube. C is the near vertex shared by all three visible faces; e is
	// an edge's length. With no crucible to make room for, it grows and centres.
	e, C := 0.29, pt{0.5, 0.63}
	if !detailed {
		e, C = 0.34, pt{0.5, 0.60}
	}
	dx, dy := e*math.Cos(math.Pi/6), e*0.5
	right, back, left := C.add(pt{dx, -dy}), C.add(pt{0, -2 * dy}), C.add(pt{-dx, -dy})
	down := pt{0, e}
	topCentre := C.add(pt{0, -dy})

	// Heat haze behind the cube's top, so the pool reads as glowing and not
	// just orange.
	glowR := e * 1.25
	c.fill(ellipse(topCentre, glowR, glowR*0.8), func(p pt) color.NRGBA {
		d := p.sub(topCentre)
		t := math.Hypot(d.x/glowR, d.y/(glowR*0.8))
		g := moltenMid
		g.A = uint8(90 * math.Pow(math.Max(0, 1-t), 2))
		return g
	})

	c.fill([]pt{C, left, left.add(down), C.add(down)}, func(p pt) color.NRGBA {
		return mix(steelL0, steelL1, (p.y-left.y)/(C.y+e-left.y))
	})
	c.fill([]pt{C, right, right.add(down), C.add(down)}, func(p pt) color.NRGBA {
		return mix(steelR0, steelR1, (p.y-right.y)/(C.y+e-right.y))
	})

	// The top: a steel rim round a molten pool, or all pool at 16px.
	top := []pt{C, right, back, left}
	poolScale := 0.74
	if tiny {
		poolScale = 1
	} else {
		c.fill(top, func(p pt) color.NRGBA { return mix(steelTop, steelR0, (p.y-back.y)/(C.y-back.y)) })
	}
	pool := make([]pt, len(top))
	for i, p := range top {
		pool[i] = lerp(topCentre, p, poolScale)
	}
	c.fill(pool, func(p pt) color.NRGBA {
		d := p.sub(topCentre)
		// Isometric distance: the rhombus is twice as wide as it is tall.
		t := (math.Abs(d.x)/dx + math.Abs(d.y)/dy) / poolScale
		return molten(0.15 + 0.85*t)
	})

	// A lit edge where the two side faces meet, and along the rim's near
	// edges — the highlight that makes the cube a solid rather than three
	// flat shapes.
	if !tiny {
		w := 0.012
		c.fill([]pt{C.add(pt{-w, 0}), C.add(pt{w, 0}), C.add(down).add(pt{w, 0}), C.add(down).add(pt{-w, 0})},
			func(p pt) color.NRGBA {
				h := steelTop
				h.A = uint8(150 * (1 - (p.y-C.y)/e))
				return h
			})
	}

	if detailed {
		drawCrucible(c, topCentre)
	}
	return c.img
}

// drawCrucible draws the tipped crucible and the stream it pours into the pool
// at target.
func drawCrucible(c *canvas, target pt) {
	pivot := pt{0.28, 0.23}
	tilt := 0.8 // radians clockwise: tipped past level so it pours

	// The crucible in its own frame, mouth up (−y), then tipped and placed:
	// straight flared sides, a rounded bottom, and an open mouth seen as an
	// ellipse with the melt inside it.
	place := func(ps []pt) []pt {
		out := make([]pt, len(ps))
		for i, p := range ps {
			out[i] = p.rot(tilt).add(pivot)
		}
		return out
	}
	tw, bw, h, rb := 0.12, 0.09, 0.17, 0.05
	body := []pt{{-tw, -h / 2}, {tw, -h / 2}, {bw, h/2 - rb}}
	for i := 1; i < 16; i++ {
		a := float64(i) / 16 * math.Pi
		body = append(body, pt{bw * math.Cos(a), h/2 - rb + rb*math.Sin(a)})
	}
	body = append(body, pt{-bw, h/2 - rb})
	c.fill(place(body), func(p pt) color.NRGBA {
		// Lit along the side facing the pool.
		d := p.sub(pivot).rot(-tilt)
		return mix(steelL1, steelR0, 0.5+d.x/(2*tw))
	})

	band := []pt{{-tw * 0.95, -h * 0.2}, {tw * 0.95, -h * 0.2}, {tw * 0.9, -h * 0.06}, {-tw * 0.9, -h * 0.06}}
	c.fill(place(band), solid(hex(0x262b35)))

	mouthC := pt{0, -h / 2}
	c.fill(place(ellipse(mouthC, tw, 0.034)), solid(steelTop))
	c.fill(place(ellipse(mouthC, tw*0.84, 0.024)), func(p pt) color.NRGBA {
		d := p.sub(pivot).rot(-tilt).sub(mouthC)
		return molten(0.1 + 0.8*math.Hypot(d.x/(tw*0.84), d.y/0.024))
	})

	// The stream leaves the lip — the mouth's lowest corner once tipped — arcs
	// out, and falls into the pool, thinning as it goes.
	lip := place([]pt{{tw * 0.92, -h / 2}})[0]
	ctrl := pt{target.x + 0.01, lip.y - 0.005}
	end := target.add(pt{0, -0.01})
	const n = 40
	var edgeA, edgeB []pt
	for i := 0; i <= n; i++ {
		t := float64(i) / n
		p := lerp(lerp(lip, ctrl, t), lerp(ctrl, end, t), t)
		tan := lerp(ctrl, end, t).sub(lerp(lip, ctrl, t)).norm()
		w := 0.03 - 0.012*t
		edgeA = append(edgeA, p.add(tan.perp().mul(w/2)))
		edgeB = append(edgeB, p.sub(tan.perp().mul(w/2)))
	}
	ribbon := edgeA
	for i := len(edgeB) - 1; i >= 0; i-- {
		ribbon = append(ribbon, edgeB[i])
	}
	c.fill(ribbon, func(p pt) color.NRGBA {
		return molten(0.05 + 0.55*(p.y-lip.y)/(end.y-lip.y))
	})

	// Where it lands: a splash of white heat on the pool.
	c.fill(ellipse(end.add(pt{0, 0.012}), 0.045, 0.02), func(p pt) color.NRGBA {
		d := p.sub(end.add(pt{0, 0.012}))
		t := math.Hypot(d.x/0.045, d.y/0.02)
		k := moltenHot
		k.A = uint8(230 * math.Max(0, 1-t*t))
		return k
	})
}
