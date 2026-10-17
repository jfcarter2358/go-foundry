package place

// backprofile.go — a cliff chunk's back, measured: how far its back surface
// stands off its bounding box's back plane, on a grid across the back, in
// the chunk's wall frame (backFrame). wall.go's seatChunk buries the cells
// on the OUTLINE of the profile — the rim an open-backed scan shows from the
// side — rather than the box's face alone.
//
// It is a z-buffer looking at the chunk from behind: every triangle is
// rasterised onto the grid's cell centres in the (lateral, height) plane,
// keeping the nearest depth to the back plane and the surface's normal
// there. Cells no triangle covers are empty (the chunk's silhouette does not
// reach them), and a cell on the outline is a covered one with an empty or
// out-of-grid neighbour.
//
// OPEN AND CLOSED. Where the first surface seen from behind faces the back
// (its normal toward the wall), the chunk has a back there: a gap behind it
// is a crevice in shadow, and the cell's gap counts as nothing — the box
// seating is right for it. Where it faces AWAY from the wall, that is the
// inside of the front of an open shell, and the gap is a hole a viewer
// beside the chunk looks straight into: those are the cells the seating must
// bury.

import "math"

// BackProfile is a chunk's back, measured (see the file comment).
type BackProfile struct {
	NX, NY int
	// Gap is, per cell (row-major, row 0 the bottom), the distance in mesh
	// units from the box's back plane to the back surface; negative: empty.
	Gap []float64
	// Outline is the covered cells with an empty neighbour.
	Outline []BackCell
}

// BackCell is one outline cell: its height above the chunk's base (mesh
// units), its place across the width (−1 the left edge of the box, 1 the
// right, as seatChunk's transects are placed), and its gap.
type BackCell struct {
	Y, Lateral, Gap float64
}

// MeasureBack measures variant v's back from its mesh (positions and vertex
// normals in the mesh's own frame, triangles as index triples) on an nx × ny
// grid. Without normals every covered cell counts as open.
func MeasureBack(v *Variant, pos, nrm [][3]float64, tris [][3]int, nx, ny int) *BackProfile {
	head, backZ, _, cx, width := backFrame(v)
	r := RotY(-head)
	lo, hi := v.Min, v.Max
	if lo == hi {
		lo = [3]float64{-v.Extent[0] / 2, 0, 0}
		hi = [3]float64{v.Extent[0] / 2, v.Extent[1], v.Extent[2]}
	}
	y0, y1 := lo[1], hi[1]
	x0 := cx - width/2
	if width <= 0 || y1 <= y0 || nx < 2 || ny < 2 {
		return nil
	}
	cw, ch := width/float64(nx), (y1-y0)/float64(ny)
	depth := make([]float64, nx*ny)
	facing := make([]float64, nx*ny) // the hit surface's normal z (toward the front: open)
	for i := range depth {
		depth[i] = math.Inf(1)
		facing[i] = 1
	}
	q := make([][3]float64, len(pos))
	for i, p := range pos {
		q[i] = r.Apply(p)
	}
	var qn [][3]float64
	if len(nrm) == len(pos) {
		qn = make([][3]float64, len(nrm))
		for i, n := range nrm {
			qn[i] = r.Apply(n)
		}
	}
	for _, t := range tris {
		a, b, c := q[t[0]], q[t[1]], q[t[2]]
		minX, maxX := math.Min(a[0], math.Min(b[0], c[0])), math.Max(a[0], math.Max(b[0], c[0]))
		minY, maxY := math.Min(a[1], math.Min(b[1], c[1])), math.Max(a[1], math.Max(b[1], c[1]))
		i0, i1 := int(math.Ceil((minX-x0)/cw-0.5)), int(math.Floor((maxX-x0)/cw-0.5))
		j0, j1 := int(math.Ceil((minY-y0)/ch-0.5)), int(math.Floor((maxY-y0)/ch-0.5))
		i0, j0 = max(i0, 0), max(j0, 0)
		i1, j1 = min(i1, nx-1), min(j1, ny-1)
		den := (b[1]-c[1])*(a[0]-c[0]) + (c[0]-b[0])*(a[1]-c[1])
		if math.Abs(den) < 1e-12 {
			continue
		}
		for j := j0; j <= j1; j++ {
			py := y0 + (float64(j)+0.5)*ch
			for i := i0; i <= i1; i++ {
				px := x0 + (float64(i)+0.5)*cw
				l0 := ((b[1]-c[1])*(px-c[0]) + (c[0]-b[0])*(py-c[1])) / den
				l1 := ((c[1]-a[1])*(px-c[0]) + (a[0]-c[0])*(py-c[1])) / den
				l2 := 1 - l0 - l1
				if l0 < -1e-9 || l1 < -1e-9 || l2 < -1e-9 {
					continue
				}
				z := l0*a[2] + l1*b[2] + l2*c[2]
				if k := j*nx + i; z < depth[k] {
					depth[k] = z
					if qn != nil {
						facing[k] = l0*qn[t[0]][2] + l1*qn[t[1]][2] + l2*qn[t[2]][2]
					}
				}
			}
		}
	}
	bp := &BackProfile{NX: nx, NY: ny, Gap: make([]float64, nx*ny)}
	covered := func(i, j int) bool { return i >= 0 && j >= 0 && i < nx && j < ny && !math.IsInf(depth[j*nx+i], 1) }
	for j := 0; j < ny; j++ {
		for i := 0; i < nx; i++ {
			k := j*nx + i
			if !covered(i, j) {
				bp.Gap[k] = -1
				continue
			}
			bp.Gap[k] = math.Max(depth[k]-backZ, 0)
			if facing[k] < 0 {
				bp.Gap[k] = 0 // a closed back: the box seats it
			}
			// The bottom row stands on the ground (the tier's sink buries
			// it); the outline that shows is the sides and the top.
			if (j > 0 && !covered(i, j-1)) || !covered(i-1, j) || !covered(i+1, j) || !covered(i, j+1) {
				bp.Outline = append(bp.Outline, BackCell{Y: y0 + (float64(j)+0.5)*ch, Lateral: ((float64(i)+0.5)/float64(nx))*2 - 1,
					Gap: bp.Gap[k]})
			}
		}
	}
	return bp
}
