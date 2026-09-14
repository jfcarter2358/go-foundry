package model

// model_bone_mirror.go — left/right bone pairs, and extruding from either end.
//
// A pair is two bones whose names differ only in their side token: "Arm.L" and
// "Arm.R", "Leg_l" and "Leg_r", "LeftHand" and "RightHand". The pairing lives in
// the names rather than in a stored partner index or in the geometry, for two
// reasons:
//
//   - A stored index would have to be remapped by every op that reorders the
//     bone array (DeleteBones), and saved in the .fmdl. Names already are both.
//   - Geometry cannot tell a pair from a coincidence. Two bones extruded from a
//     centre joint start out on top of each other, so each is its own mirror
//     image as much as the other's, and a pair edited asymmetrically with mirror
//     off stops matching at all. Names survive both.
//
// Names are also what an animation clip keys on, so a future "mirror pose"
// reads the same pairs the editor builds here.
//
// The mirror plane passes through the model origin, normal to one model axis.
// ".L" is the positive side, the usual armature convention.

import (
	"math"
	"strings"
	"unicode"

	"github.com/go-gl/mathgl/mgl32"
)

// boneMirrorEps is how close to the mirror plane a point counts as on it.
const boneMirrorEps = 1e-4

// boneSideLetters are the single-letter side tokens, each with its opposite.
var boneSideLetters = map[byte]byte{'L': 'R', 'R': 'L', 'l': 'r', 'r': 'l'}

// boneSideWords are the spelled-out side tokens, each with its opposite.
var boneSideWords = [][2]string{
	{"Left", "Right"}, {"left", "right"}, {"LEFT", "RIGHT"},
}

// isBoneNameSep reports whether c separates a side letter from the rest of a
// name. A bare trailing letter is not enough — "BallR" is not a side.
func isBoneNameSep(c byte) bool {
	return c == '.' || c == '_' || c == '-' || c == ' '
}

// splitBoneNumber splits a ".001"-style suffix off a name. The side token sits
// before the number ("Arm.L.001"), because the number is what uniqueBoneName
// appends to whatever the name already was.
func splitBoneNumber(name string) (base, number string) {
	if stem := boneNameStem(name); stem != name && strings.HasPrefix(name, stem) {
		return stem, name[len(stem):]
	}
	return name, ""
}

// MirrorBoneName returns the name of bone name's opposite-side partner, or false
// when the name carries no side.
func MirrorBoneName(name string) (string, bool) {
	base, number := splitBoneNumber(name)
	if swapped, ok := mirrorBoneBase(base); ok {
		return swapped + number, true
	}
	return "", false
}

func mirrorBoneBase(base string) (string, bool) {
	n := len(base)
	// Suffix letter: "Arm.L".
	if n >= 3 && isBoneNameSep(base[n-2]) {
		if o, ok := boneSideLetters[base[n-1]]; ok {
			return base[:n-1] + string(o), true
		}
	}
	// Prefix letter: "L_Arm".
	if n >= 3 && isBoneNameSep(base[1]) {
		if o, ok := boneSideLetters[base[0]]; ok {
			return string(o) + base[1:], true
		}
	}
	// Words, at either end: "ArmLeft", "Left_Arm". A word only counts on a
	// boundary, so "Cleft" is not a side.
	for _, pair := range boneSideWords {
		for i := 0; i < 2; i++ {
			word, opp := pair[i], pair[1-i]
			if strings.HasSuffix(base, word) && len(base) > len(word) &&
				boneWordBoundary(base[len(base)-len(word)-1], word[0]) {
				return base[:len(base)-len(word)] + opp, true
			}
			if strings.HasPrefix(base, word) && len(base) > len(word) &&
				boneWordBoundary(word[len(word)-1], base[len(word)]) {
				return opp + base[len(word):], true
			}
		}
	}
	return "", false
}

// boneWordBoundary reports whether a word break falls between the adjacent
// letters before and after: a separator or digit on either side, or a
// lower-to-upper case change ("armLeft", "LeftArm"). The case change has to run
// that way round — "Cleft" changes case too, but upper-to-lower is just the
// start of an ordinary capitalised word.
func boneWordBoundary(before, after byte) bool {
	for _, c := range []byte{before, after} {
		if isBoneNameSep(c) || unicode.IsDigit(rune(c)) {
			return true
		}
	}
	return unicode.IsLower(rune(before)) && unicode.IsUpper(rune(after))
}

// MirrorBone returns the index of bone bi's opposite-side partner, or -1 when
// it has no side or its partner does not exist.
func (m *ModelDoc) MirrorBone(bi int) int {
	if !m.ValidBone(bi) {
		return -1
	}
	name, ok := MirrorBoneName(m.Bones[bi].Name)
	if !ok {
		return -1
	}
	if j := m.BoneIndexByName(name); j != bi {
		return j
	}
	return -1
}

// uniqueBonePairNames returns a name for a new bone built on base, and its
// mirror, both free. base must carry a side.
//
// The two are chosen together rather than each through uniqueBoneName, which
// could hand one side ".003" and the other ".004" when their histories differ —
// and then the new bones would not be a pair at all.
func (m *ModelDoc) uniqueBonePairNames(base string) (string, string) {
	base, _ = splitBoneNumber(base)
	mirror, ok := mirrorBoneBase(base)
	if !ok {
		base = sideBoneBase(base, true)
		mirror, _ = mirrorBoneBase(base)
	}
	free := func(a, b string) bool {
		return m.BoneIndexByName(a) < 0 && m.BoneIndexByName(b) < 0
	}
	if free(base, mirror) {
		return base, mirror
	}
	for n := 1; n < 1000; n++ {
		suffix := "." + padBoneNumber(n)
		if free(base+suffix, mirror+suffix) {
			return base + suffix, mirror + suffix
		}
	}
	suffix := "." + padBoneNumber(len(m.Bones))
	return base + suffix, mirror + suffix
}

func padBoneNumber(n int) string {
	s := []byte{'0', '0', '0'}
	for i := 2; i >= 0 && n > 0; i-- {
		s[i] = byte('0' + n%10)
		n /= 10
	}
	return string(s)
}

// sideBoneBase returns a side-carrying base name for a pair grown out of a
// side-less bone named name: its stem, with ".L" or ".R".
func sideBoneBase(name string, positive bool) string {
	stem := boneNameStem(name)
	side := ".R"
	if positive {
		side = ".L"
	}
	return stem + side
}

// MirrorBonePoint reflects p across the plane through the origin normal to axis.
func MirrorBonePoint(p mgl32.Vec3, axis int) mgl32.Vec3 {
	if axis >= 0 && axis < 3 {
		p[axis] = -p[axis]
	}
	return p
}

// onMirrorPlane reports whether p sits on the mirror plane.
func onMirrorPlane(p mgl32.Vec3, axis int) bool {
	return axis >= 0 && axis < 3 && math.Abs(float64(p[axis])) < boneMirrorEps
}

// MirrorBoneRoll returns the roll that makes the mirror image of b a true mirror
// of it, not just a bone between the mirrored endpoints.
//
// Negating the roll is only right for some directions: the zero-roll basis comes
// from geom.PerpAxis, which picks its reference axis from the bone's direction,
// and a reflection can invert that reference or leave it alone. So the roll is
// solved for instead — the one whose X axis is the reflection of b's, negated so
// the basis stays right-handed (a reflection flips handedness, and BoneBasisAxes
// only builds right-handed frames). Z then comes out as the plain reflection of
// b's Z.
func MirrorBoneRoll(b ModelBone, axis int) float32 {
	x, _, _, _, ok := BoneBasisAxes(b)
	if !ok {
		return -b.Roll
	}
	mirrored := ModelBone{Head: MirrorBonePoint(b.Head, axis), Tail: MirrorBonePoint(b.Tail, axis)}
	x0, _, z0, _, ok := BoneBasisAxes(mirrored)
	if !ok {
		return -b.Roll
	}
	target := MirrorBonePoint(x, axis).Mul(-1)
	// BoneBasisAxes rolls x0 to x0·cos − z0·sin.
	c := target.Dot(x0)
	s := -target.Dot(z0)
	return float32(math.Atan2(float64(s), float64(c)))
}

// MirrorBoneOnto places dst as the mirror image of src: whichever of the head,
// the tail and the roll are asked for. dst's connected parent and children are
// carried with the joints it moves, the same as any other joint edit.
func (m *ModelDoc) MirrorBoneOnto(src, dst, axis int, head, tail, roll bool) {
	if !m.ValidBone(src) || !m.ValidBone(dst) || src == dst {
		return
	}
	s := m.Bones[src]
	if head {
		m.SetBoneHead(dst, MirrorBonePoint(s.Head, axis))
	}
	if tail {
		m.Bones[dst].Tail = MirrorBonePoint(s.Tail, axis)
	}
	if roll {
		m.Bones[dst].Roll = MirrorBoneRoll(s, axis)
	}
	m.SyncConnectedBones()
}

// AddBonePair adds a bone and, when it lies off the mirror plane, its mirror
// image, named as a pair. Returns the bone at the given endpoints and its
// partner, or -1 for a partner when the bone is on the plane and so is its own
// mirror.
func (m *ModelDoc) AddBonePair(head, tail mgl32.Vec3, axis int) (int, int) {
	if onMirrorPlane(head, axis) && onMirrorPlane(tail, axis) {
		return m.AddBone(head, tail), -1
	}
	lead := m.AddBone(head, tail)
	if lead < 0 {
		return -1, -1
	}
	side := head[axis]
	if onMirrorPlane(head, axis) {
		side = tail[axis]
	}
	partner := m.AddBone(MirrorBonePoint(head, axis), MirrorBonePoint(tail, axis))
	m.nameBonePair(lead, partner, sideBoneBase(defaultBoneName, side > 0))
	m.Bones[partner].Roll = MirrorBoneRoll(m.Bones[lead], axis)
	return lead, partner
}

// nameBonePair gives lead a free name built on base and partner the mirror of
// it. Both are cleared first so their placeholder names cannot block the pair.
func (m *ModelDoc) nameBonePair(lead, partner int, base string) {
	m.Bones[lead].Name, m.Bones[partner].Name = "", ""
	m.Bones[lead].Name, m.Bones[partner].Name = m.uniqueBonePairNames(base)
}

// ExtrudeBoneFrom grows a child from one end of bone bi and returns its index.
//
// From the tail it is ExtrudeBone: a connected continuation of the chain. From
// the head it branches instead — a child that starts where bi starts and points
// the opposite way, the shape a pair of legs hanging off an upward pelvis takes.
// That child is not connected: Connected welds a head to the parent's *tail*,
// and a head-grown child is nowhere near it. It still follows bi in a pose,
// swinging with the joint they share.
func (m *ModelDoc) ExtrudeBoneFrom(bi int, fromHead bool) int {
	if !m.ValidBone(bi) {
		return -1
	}
	if !fromHead {
		return m.ExtrudeBone(bi)
	}
	parent := m.Bones[bi]
	dir := parent.Head.Sub(parent.Tail)
	if dir.Len() < 1e-6 {
		dir = mgl32.Vec3{0, 1, 0}
	}
	m.Bones = append(m.Bones, ModelBone{
		Name:   m.uniqueBoneName(parent.Name),
		Parent: bi,
		Head:   parent.Head,
		Tail:   parent.Head.Add(dir),
		Roll:   parent.Roll,
	})
	return len(m.Bones) - 1
}

// ExtrudeBoneMirrored extrudes bi from one end and, when bi has a partner,
// extrudes the partner from the same end, naming the two children as a pair.
// Returns bi's child and the partner's (-1 when there is no partner).
//
// The partner's child is grown from the partner as it stands rather than forced
// onto the mirror image of bi's child: on a symmetric pair the two are the same
// thing, and on an asymmetric one forcing it would detach the child from the
// joint it is supposed to be connected to.
func (m *ModelDoc) ExtrudeBoneMirrored(bi int, fromHead bool) (int, int) {
	partner := m.MirrorBone(bi)
	child := m.ExtrudeBoneFrom(bi, fromHead)
	if child < 0 || partner < 0 {
		return child, -1
	}
	mirrorChild := m.ExtrudeBoneFrom(partner, fromHead)
	if mirrorChild < 0 {
		return child, -1
	}
	m.nameBonePair(child, mirrorChild, m.Bones[bi].Name)
	return child, mirrorChild
}

// ForkBone grows a mirrored pair of children from one end of bi, which must lie
// on the mirror plane — a spine's shoulder joint, a pelvis. Returns the child on
// the positive side and its partner, or -1, -1 when the joint is off the plane
// (a pair hung off a joint on one side could not be each other's mirror).
//
// The two start pointing straight out along ±axis, bi's length long, rather than
// both continuing bi's direction the way ExtrudeBone would: on top of each other
// they could not be told apart or clicked, and out to the sides is where arms
// and clavicles go anyway.
func (m *ModelDoc) ForkBone(bi int, fromHead bool, axis int) (int, int) {
	if !m.ValidBone(bi) || axis < 0 || axis > 2 {
		return -1, -1
	}
	parent := m.Bones[bi]
	joint := parent.Tail
	if fromHead {
		joint = parent.Head
	}
	if !onMirrorPlane(joint, axis) {
		return -1, -1
	}
	length := m.BoneLength(bi)
	if length < 1e-6 {
		length = 1
	}
	var out mgl32.Vec3
	out[axis] = length

	add := func(tail mgl32.Vec3) int {
		m.Bones = append(m.Bones, ModelBone{
			Parent:    bi,
			Head:      joint,
			Tail:      tail,
			Connected: !fromHead,
		})
		return len(m.Bones) - 1
	}
	pos := add(joint.Add(out))
	neg := add(joint.Sub(out))
	m.Bones[neg].Roll = MirrorBoneRoll(m.Bones[pos], axis)
	m.nameBonePair(pos, neg, sideBoneBase(parent.Name, true))
	return pos, neg
}
