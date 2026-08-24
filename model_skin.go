package main

// model_skin.go — binding vertices to bones.
//
// The weights live in a slice parallel to ModelDoc.Verts rather than in a list
// hanging off each bone. Both encode the same thing, but only the parallel form
// makes the awkward case cheap: every mesh operation that adds or removes a
// vertex has to keep the binding correct, and "the weights of vertex v" being a
// single index lookup is what lets extrude, subdivide and Catmull-Clark do that
// in a line each (see the weightsFor/blendWeights calls in model_ops.go).
//
// Deformation is linear blend skinning: a vertex's new position is the weighted
// sum of its position pushed through each influencing bone's delta transform.
// That is the standard formulation, and it is the one an animation player will
// use unchanged — the editor's bone-drag path (SkinFrom) and a future keyframe
// player differ only in where the deltas come from.

import (
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"
)

// modelMaxInfluences caps how many bones may move one vertex. Four is what a
// GPU skinning shader can carry in a single vec4 of weights and a single vec4
// of indices, so keeping the data inside that budget now means the eventual
// hardware skinning path does not have to re-prune every model it loads.
const modelMaxInfluences = 4

// ModelVertexWeight binds one vertex to one bone.
type ModelVertexWeight struct {
	Bone   int
	Weight float32
}

// weightsFor returns vertex vi's influences, or nil when it is unbound. The
// slice is the live one — callers that keep it must copy.
func (m *ModelDoc) weightsFor(vi int) []ModelVertexWeight {
	if vi < 0 || vi >= len(m.Weights) {
		return nil
	}
	return m.Weights[vi]
}

// VertexWeights returns a copy of vertex vi's influences, for callers that want
// to read them without risking a write to the document.
func (m *ModelDoc) VertexWeights(vi int) []ModelVertexWeight {
	return append([]ModelVertexWeight(nil), m.weightsFor(vi)...)
}

// WeightOf returns how much bone bi moves vertex vi; 0 when unbound.
func (m *ModelDoc) WeightOf(vi, bi int) float32 {
	for _, w := range m.weightsFor(vi) {
		if w.Bone == bi {
			return w.Weight
		}
	}
	return 0
}

// ensureWeights grows the weight table to match the vertex pool. Every entry
// point that can add vertices calls this, so the two arrays are the same length
// whenever anything reads them.
func (m *ModelDoc) ensureWeights() {
	if len(m.Weights) == len(m.Verts) {
		return
	}
	if len(m.Weights) > len(m.Verts) {
		m.Weights = m.Weights[:len(m.Verts)]
		return
	}
	grown := make([][]ModelVertexWeight, len(m.Verts))
	copy(grown, m.Weights)
	m.Weights = grown
}

// addVertex appends a vertex carrying the given influences and returns its
// index. Every mesh operation that invents a vertex goes through this, which is
// what keeps Verts and Weights the same length without each of them having to
// remember to append to both.
func (m *ModelDoc) addVertex(p mgl32.Vec3, ws []ModelVertexWeight) int {
	m.ensureWeights()
	m.Verts = append(m.Verts, p)
	m.Weights = append(m.Weights, ws)
	return len(m.Verts) - 1
}

// hasSkin reports whether any vertex is bound to a bone. Used to skip the
// deform path entirely on an unskinned model.
func (m *ModelDoc) hasSkin() bool {
	for _, ws := range m.Weights {
		if len(ws) > 0 {
			return true
		}
	}
	return false
}

// cloneWeights deep-copies the weight table; the inner slices must not be
// shared or an edit to a snapshot would rewrite the live document.
func cloneWeights(src [][]ModelVertexWeight) [][]ModelVertexWeight {
	if src == nil {
		return nil
	}
	out := make([][]ModelVertexWeight, len(src))
	for i, ws := range src {
		if ws != nil {
			out[i] = append([]ModelVertexWeight(nil), ws...)
		}
	}
	return out
}

// ---------- normalisation ----------

// normalizeWeights merges duplicate bones, drops non-positive weights, keeps
// only the strongest modelMaxInfluences of them and rescales the survivors to
// sum to 1.
//
// Summing to 1 is what makes a fully-weighted vertex rigid: with weights that
// sum to less than 1 the blended position collapses towards the origin, which
// shows up as the mesh shrinking into the model's centre the moment a bone
// moves. Pruning happens before the rescale so the four bones that survive
// carry the whole vertex between them.
func normalizeWeights(ws []ModelVertexWeight) []ModelVertexWeight {
	if len(ws) == 0 {
		return nil
	}
	merged := make(map[int]float32, len(ws))
	order := make([]int, 0, len(ws))
	for _, w := range ws {
		if w.Bone < 0 || w.Weight <= 0 {
			continue
		}
		if _, seen := merged[w.Bone]; !seen {
			order = append(order, w.Bone)
		}
		merged[w.Bone] += w.Weight
	}
	if len(order) == 0 {
		return nil
	}
	out := make([]ModelVertexWeight, 0, len(order))
	for _, b := range order {
		out = append(out, ModelVertexWeight{Bone: b, Weight: merged[b]})
	}
	// Strongest first, bone index breaking ties so the result does not depend
	// on the order the influences happened to be added in.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Weight != out[j].Weight {
			return out[i].Weight > out[j].Weight
		}
		return out[i].Bone < out[j].Bone
	})
	if len(out) > modelMaxInfluences {
		out = out[:modelMaxInfluences]
	}
	var total float32
	for _, w := range out {
		total += w.Weight
	}
	if total <= 0 {
		return nil
	}
	for i := range out {
		out[i].Weight /= total
	}
	return out
}

// blendWeights averages several vertices' influences, for the vertices an
// operation invents between existing ones — an edge midpoint, a face centre.
//
// Averaging rather than copying one neighbour's is what keeps a subdivided limb
// smooth across the boundary between two bones: the new vertex ends up
// partly influenced by both, exactly as its neighbours are.
func blendWeights(lists ...[]ModelVertexWeight) []ModelVertexWeight {
	var acc []ModelVertexWeight
	n := 0
	for _, ws := range lists {
		if len(ws) == 0 {
			continue
		}
		n++
		acc = append(acc, ws...)
	}
	if n == 0 {
		return nil
	}
	// normalizeWeights rescales to 1 anyway, so the per-list division that a
	// true average would need cancels out — every list already sums to 1.
	return normalizeWeights(acc)
}

// remapWeightBones rewrites bone indices after the bone array has been
// compacted. remap[old] is the new index, or -1 for a bone that was deleted.
func (m *ModelDoc) remapWeightBones(remap []int) {
	for vi, ws := range m.Weights {
		if len(ws) == 0 {
			continue
		}
		out := ws[:0]
		for _, w := range ws {
			if w.Bone < 0 || w.Bone >= len(remap) || remap[w.Bone] < 0 {
				continue
			}
			w.Bone = remap[w.Bone]
			out = append(out, w)
		}
		m.Weights[vi] = normalizeWeights(out)
	}
}

// ---------- assignment ----------

// AssignVertsToBone binds the given vertices to bone bi with the given weight.
//
// weight is the influence this bone claims, not a share of a fixed total: the
// vertex's other influences are kept and everything is renormalised, so
// assigning at 1.0 to a vertex that already belongs to another bone splits it
// evenly rather than silently overriding. Assigning at 1.0 to an unbound vertex
// makes it rigid to this bone, which is the common case.
func (m *ModelDoc) AssignVertsToBone(verts []int, bi int, weight float32) int {
	if !m.validBone(bi) || weight <= 0 {
		return 0
	}
	m.ensureWeights()
	count := 0
	for _, vi := range verts {
		if vi < 0 || vi >= len(m.Weights) {
			continue
		}
		ws := append([]ModelVertexWeight(nil), m.Weights[vi]...)
		replaced := false
		for i := range ws {
			if ws[i].Bone == bi {
				ws[i].Weight = weight
				replaced = true
				break
			}
		}
		if !replaced {
			ws = append(ws, ModelVertexWeight{Bone: bi, Weight: weight})
		}
		m.Weights[vi] = normalizeWeights(ws)
		count++
	}
	return count
}

// RemoveVertsFromBone unbinds the given vertices from bone bi.
func (m *ModelDoc) RemoveVertsFromBone(verts []int, bi int) int {
	m.ensureWeights()
	count := 0
	for _, vi := range verts {
		if vi < 0 || vi >= len(m.Weights) || len(m.Weights[vi]) == 0 {
			continue
		}
		kept := make([]ModelVertexWeight, 0, len(m.Weights[vi]))
		removed := false
		for _, w := range m.Weights[vi] {
			if w.Bone == bi {
				removed = true
				continue
			}
			kept = append(kept, w)
		}
		if !removed {
			continue
		}
		m.Weights[vi] = normalizeWeights(kept)
		count++
	}
	return count
}

// VertsBoundTo returns the vertices bone bi moves, ascending.
func (m *ModelDoc) VertsBoundTo(bi int) []int {
	var out []int
	for vi, ws := range m.Weights {
		for _, w := range ws {
			if w.Bone == bi {
				out = append(out, vi)
				break
			}
		}
	}
	return out
}

// ClearWeights unbinds the given vertices from every bone.
func (m *ModelDoc) ClearWeights(verts []int) {
	m.ensureWeights()
	for _, vi := range verts {
		if vi >= 0 && vi < len(m.Weights) {
			m.Weights[vi] = nil
		}
	}
}

// ---------- automatic weights ----------

// modelAutoWeightSpread controls how far past the nearest bone a vertex still
// picks up influence: a bone gets a share only if it is within this multiple of
// the nearest bone's distance. 2 is loose enough that a vertex sitting between
// two bones of a chain is shared between them (which is what makes a joint bend
// instead of creasing), and tight enough that the far side of the model does
// not bleed in.
const modelAutoWeightSpread = float32(2)

// AutoWeightVerts binds each of the given vertices to the bones nearest it,
// with influence falling off as the inverse square of the distance to the
// bone's segment.
//
// This is a distance heuristic, not a proper heat-diffusion solve: it cannot
// tell that two limbs which pass close together are separate, so a hand held
// against a hip will pick up hip weight. It is here because it turns a fresh
// skeleton into something that deforms in one click, which is the difference
// between the weights being usable and being a thing nobody fills in by hand.
// Per-bone Assign is the escape hatch for whatever it gets wrong.
func (m *ModelDoc) AutoWeightVerts(verts []int) int {
	if len(m.Bones) == 0 {
		return 0
	}
	m.ensureWeights()
	count := 0
	dists := make([]float32, len(m.Bones))
	for _, vi := range verts {
		if vi < 0 || vi >= len(m.Verts) {
			continue
		}
		p := m.Verts[vi]
		nearest := float32(math.MaxFloat32)
		for bi := range m.Bones {
			dists[bi] = m.distToBone(p, bi)
			if dists[bi] < nearest {
				nearest = dists[bi]
			}
		}
		if nearest == float32(math.MaxFloat32) {
			continue
		}
		// A vertex sitting exactly on a bone would divide by zero; the epsilon
		// also keeps a near-zero distance from swamping every other influence
		// with a weight in the millions.
		const eps = 1e-4
		cutoff := nearest*modelAutoWeightSpread + eps
		var ws []ModelVertexWeight
		for bi := range m.Bones {
			if dists[bi] > cutoff {
				continue
			}
			d := dists[bi] + eps
			ws = append(ws, ModelVertexWeight{Bone: bi, Weight: 1 / (d * d)})
		}
		m.Weights[vi] = normalizeWeights(ws)
		count++
	}
	return count
}

// ---------- deformation ----------

// SkinFrom moves every bound vertex to follow the bones, given the rest pose
// the current positions belong to.
//
// restVerts and restBones must be the state the mesh was last in agreement
// with — the editor snapshots both when a bone drag starts, so the whole drag
// is one absolute transform of the original mesh rather than a chain of
// incremental ones that would accumulate rounding as the mouse moves.
//
// Unbound vertices are left exactly where they are, which is what makes it safe
// to run this on a partly-weighted model: the parts nobody has assigned yet
// simply do not move.
func (m *ModelDoc) SkinFrom(restBones []ModelBone, restVerts []mgl32.Vec3) {
	if len(m.Bones) == 0 || !m.hasSkin() {
		return
	}
	m.ensureWeights()
	if skinVerts(m.BoneDeltas(restBones), m.Weights, restVerts, m.Verts) {
		m.touch()
	}
}

// skinVerts is the linear blend itself, factored out of SkinFrom so the two
// sources of deltas share one implementation: a rest-skeleton drag passes
// BoneDeltas, an animated pose passes PoseDeltas (model_pose.go). It reports
// whether anything moved.
//
// out may be restVerts itself, which is the in-place case SkinFrom uses. Only
// bound vertices are written, so an out that starts as a copy of restVerts comes
// back complete — the unbound parts of a partly-rigged model simply stay put.
func skinVerts(deltas []mgl32.Mat4, weights [][]ModelVertexWeight, restVerts, out []mgl32.Vec3) bool {
	moved := false
	for vi := range out {
		if vi >= len(weights) || vi >= len(restVerts) {
			break
		}
		ws := weights[vi]
		if len(ws) == 0 {
			continue
		}
		rest := restVerts[vi]
		p4 := mgl32.Vec4{rest.X(), rest.Y(), rest.Z(), 1}
		var acc mgl32.Vec3
		var total float32
		for _, w := range ws {
			if w.Bone < 0 || w.Bone >= len(deltas) {
				continue
			}
			t := deltas[w.Bone].Mul4x1(p4)
			acc = acc.Add(mgl32.Vec3{t.X(), t.Y(), t.Z()}.Mul(w.Weight))
			total += w.Weight
		}
		if total <= 0 {
			continue
		}
		// Rescaling by the total covers a vertex whose influences did not sum
		// to 1 — a bone deleted out from under it, say — instead of dragging it
		// towards the origin.
		out[vi] = acc.Mul(1 / total)
		moved = true
	}
	return moved
}
