package editor

// editor_scenery_collision.go — a prop's Collision group, and the wires that
// show what it collides as.
//
// A prop's bounds are read off its model rather than authored, so for an
// ordinary model there is nothing here to edit and everything to explain: a
// box. A cooked Nanite mesh carries its own collision (docs/nanite/PHASE_2b.md)
// — a cut of its DAG a few centimetres off the drawn surface, and that cut
// split into convex hulls — and the group lets a placement pick how it
// collides: the mesh's own choice (auto: hulls for something a body walks
// round, the triangles for something it walks on), or box, hulls, mesh or
// none outright. What the cook found is reported beside it, and the same
// warnings `foundry check` prints are printed here, where the fix is.
//
// "Show collision" draws what a body actually meets — the hulls' edges, the
// collision cut's triangles, or the box — over the selected prop, through
// everything, so a hull bridging a gap or a missing overhang reads at once.

import (
	"fmt"

	"foundry/internal/collision"
	"foundry/internal/gizmo"
	"foundry/internal/imguibackend"
	"foundry/internal/meshcollide"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// sceneryShowCollision draws the selected prop's collision. A session
// toggle, like the navigation overlay's.
var sceneryShowCollision bool

// sceneryCollisionColor is the debug wires': a green that reads against the
// selection cage's colour and against most stone.
var sceneryCollisionColor = mgl32.Vec3{0.25, 1, 0.45}

// sceneryCollisionWireCap bounds the edges drawn for a mesh-mode cut: past it
// the wires are a solid smear anyway, and the frame should not pay for them.
const sceneryCollisionWireCap = 60000

var sceneryModeHelp = [...]string{
	"The mesh's own choice: hulls for a placement up to its auto size\nacross, the triangles for anything bigger.",
	"The placement's bounding box, turned with it — how scenery has\nalways collided. Cheapest; wrong for anything that is not a box.",
	"A few convex hulls, resolved like brushes. Right for a rock a body\nwalks round. An open mesh's hulls fill in behind it.",
	"The collision cut's triangles, probed like terrain. Right for a\ncliff or a hillside a body walks on.",
	"Nothing: bodies and shots pass through.",
}

// editorSceneryCollision draws the Collision group.
func editorSceneryCollision(s *world.Scenery) {
	if imgui.Checkbox("Solid##scsolid", &s.Solid) {
		EditorPushUndo()
		collision.InvalidateScenery()
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Stops the player and any entity that collides with\nthe world. Switch off for grass and decals.")
	}
	d := s.CollisionData()
	if d != nil {
		if imguibackend.BeginCombo("Mode##sccolmode", s.Collision.String()) {
			for i, name := range meshcollide.ModeNames() {
				m := meshcollide.Mode(i)
				if imguibackend.SelectableV(name, m == s.Collision, 0, imgui.Vec2{}) && m != s.Collision {
					EditorPushUndo()
					s.Collision = m
					collision.InvalidateScenery()
				}
				if imgui.IsItemHovered() {
					imgui.SetTooltip(sceneryModeHelp[i])
				}
			}
			imguibackend.EndCombo()
		}
	}
	imgui.Checkbox("Show collision##scshowcol", &sceneryShowCollision)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Draw what a body meets — the hulls, the collision\ncut or the box — over the selected prop.")
	}
	for _, line := range sceneryCollisionReport(s) {
		editorTextMutedWrapped(line)
	}
}

// sceneryCollisionReport is what the group says about a prop's collision:
// the resolved mode and why, what the cook made, and its warnings.
func sceneryCollisionReport(s *world.Scenery) []string {
	mode := s.CollisionMode()
	d := s.CollisionData()
	var out []string
	switch mode {
	case meshcollide.ModeNone:
		if s.Solid && s.Collision != meshcollide.ModeNone {
			out = append(out, "No volume: the model is flat on an axis, or has not loaded. Nothing will collide with this.")
		} else {
			out = append(out, "Collides with nothing.")
		}
	case meshcollide.ModeBox:
		h := s.BoundHalfExtents()
		out = append(out, fmt.Sprintf("Box from the model: %.2f x %.2f x %.2f", h.X()*2, h.Y()*2, h.Z()*2))
	case meshcollide.ModeHulls:
		verts := 0
		for i := range d.Hulls {
			verts += len(d.Hulls[i].Pts)
		}
		out = append(out, fmt.Sprintf("%d convex hulls, %d points in all", len(d.Hulls), verts))
	case meshcollide.ModeMesh:
		out = append(out, fmt.Sprintf("The collision cut: %d triangles (of %d drawn at full detail), probed like terrain",
			d.TriangleCount(), s.Nanite.TriangleCount()))
	}
	if d == nil {
		return out
	}
	if s.Collision == meshcollide.ModeAuto && (mode == meshcollide.ModeHulls || mode == meshcollide.ModeMesh) {
		out = append(out, fmt.Sprintf("Auto: %.1f m across; hulls up to %.1f m.", s.CollisionSize(), d.Settings.Resolved().AutoSize))
	}
	out = append(out, fmt.Sprintf("Cooked at %.0f cm error: %d triangles, %d hulls, %d open edges.",
		d.Settings.Error*100, d.TriangleCount(), len(d.Hulls), d.OpenEdges))
	// The same warnings `foundry check` prints.
	for _, w := range world.SceneryCollisionWarnings(s) {
		out = append(out, "Warning: "+w)
	}
	return out
}

// drawSelectedSceneryCollision draws the selected prop's collision, when
// Show collision is on.
func drawSelectedSceneryCollision() {
	if !sceneryShowCollision {
		return
	}
	s := activeScenery()
	if s == nil {
		return
	}
	m := s.ModelMatrix()
	w := func(p mgl32.Vec3) mgl32.Vec3 {
		r := m.Mul4x1(p.Vec4(1))
		return mgl32.Vec3{r.X(), r.Y(), r.Z()}
	}
	d := s.CollisionData()
	switch s.CollisionMode() {
	case meshcollide.ModeBox:
		lo, hi := s.LocalMin, s.LocalMax
		drawWireBox(lo, hi, w, sceneryCollisionColor)
	case meshcollide.ModeHulls:
		for hi := range d.Hulls {
			h := &d.Hulls[hi]
			seen := map[[2]uint16]bool{}
			for k := 0; k+2 < len(h.Tris); k += 3 {
				for e := 0; e < 3; e++ {
					a, b := h.Tris[k+e], h.Tris[k+(e+1)%3]
					if a > b {
						a, b = b, a
					}
					if seen[[2]uint16{a, b}] {
						continue
					}
					seen[[2]uint16{a, b}] = true
					gizmo.GizmoLine(w(h.Pts[a]), w(h.Pts[b]), sceneryCollisionColor)
				}
			}
		}
	case meshcollide.ModeMesh:
		seen := map[[2]uint32]bool{}
		for t := 0; t < d.TriangleCount() && len(seen) < sceneryCollisionWireCap; t++ {
			ix := d.Tris[3*t : 3*t+3]
			for e := 0; e < 3; e++ {
				a, b := ix[e], ix[(e+1)%3]
				if a > b {
					a, b = b, a
				}
				if seen[[2]uint32{a, b}] {
					continue
				}
				seen[[2]uint32{a, b}] = true
				gizmo.GizmoLine(w(d.Dequantize(a)), w(d.Dequantize(b)), sceneryCollisionColor)
			}
		}
	}
}
