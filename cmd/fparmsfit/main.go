// Command fparmsfit finishes the first-person arms rig for the weapons it
// holds: it cuts the upper arms off the mesh, brings both forearms up from
// below, and puts the left hand on each gun.
//
//	python3 tools/fp_arms_clip_tuning.py tools/fp_arms_original.fmdl resources/models/fp_arms.fmdl
//	go run <engine>/cmd/fparmsfit -arms resources/models/fp_arms.fmdl -models resources/models
//
// Run from the game project, after the clip tuner (which rebuilds fp_arms.fmdl
// from the untouched original, and would undo all of this). Running it twice
// is harmless: the cut finds nothing left to cut, and every fitted clip is
// re-solved from its source clip each time.
//
// # The cut
//
// A first-person rig does not need shoulders. Everything behind the elbow sits
// at, beside or behind the camera, so in any pose that raises the gun to the eye
// the upper arms either fill the edge of the screen or pass through the near
// plane and are drawn sliced open. Every shooter solves this the same way — the
// viewmodel is forearms and hands — and so does this: the faces skinned to
// upper_arm.L and upper_arm.R are deleted. The bones stay, because the clips
// animate through them.
//
// # The forearms
//
// With no upper arm drawn, nothing ties an elbow to a shoulder, and that is
// what makes the rest of this possible. The clips hold the gun at arm's length
// with each forearm running back toward the shoulder it came from — which,
// with a pistol raised to the eye, is a forearm passing a hand's width from the
// camera and filling half the screen. So in every weapon clip each forearm is
// re-placed to come up from BELOW its wrist, at a fixed angle, and slid onto
// that elbow (its pose's Trans channel) rather than hanging off the real bone's
// end. The right hand's frame is put back exactly as the clip had it after the
// forearm under it moves, so the gun — which rides that hand — does not move by
// so much as a rounding error, and every aim-down-sights pose solved against
// the clip is still solved.
//
// The grenade clips are left alone: an overhand throw is a whole arm moving,
// and a forearm pinned at an angle below its wrist would not read as one.
//
// # The left hand
//
// The clips were posed with the gun in the right hand and the left arm doing
// its own thing. A gun is held with both: under the forend for anything long,
// wrapped around the right hand on a pistol's grip. So for every weapon, and
// every clip that weapon is held with, the left palm is put on that weapon's own
// support point — which moves with the right hand through a walk bob or a recoil
// kick, so the hands stay together through both.
//
// The four long guns share the rifle's right-arm motion but not the rifle's
// shape: a shotgun's pump, a sniper's forend and a launcher's forward grip are
// all in different places. So each gets its own copy of the rifle clips —
// `shotgun_idle`, `sniper_fire_ads` and so on — identical except for the left
// arm. player_combat.flang plays the weapon's own clips, and the level's
// `player: models:` block has to list them.
//
// The rocket launcher is not held out like the others at all: it is carried
// on the right shoulder, tube running forward past the face. Its clips move the
// right hand — and the launcher with it — onto the shoulder by one fixed
// transform, keeping the rifle clips' bob and kick on top.
//
// Everything is solved through the engine's own PoseMatrices and
// AttachmentMatrix and written in the engine's own pose convention, so there is
// no second implementation of either to fall out of step with.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"foundry/internal/model"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

// grip is how one weapon is held, in that weapon's own space (grip at the
// origin, barrel down −Z, top +Y).
type grip struct {
	// weapon is the model under models/, and clips the clip prefix it is held
	// with. source is the prefix those clips are copied from; the same as clips
	// for a weapon that owns its right-arm motion.
	weapon, clips, source string
	// palm is where the end of the left hand bone lands — the point the left
	// hand_socket sits at, the way the right one sits at the grip.
	palm mgl32.Vec3
	// along is the left hand's own direction, wrist to palm. A support hand
	// comes up under the gun from the left and behind, so it points forward,
	// across and up; a pistol's supporting hand wraps the grip from its left.
	along mgl32.Vec3
	// shoulder, when set, is not held out in front like a rifle but carried
	// on the right shoulder: the weapon's origin (its grip) sits here, in the
	// camera's own space at the hip pose, axis-aligned with the view. See
	// shoulderCarry.
	shoulder *mgl32.Vec3
}

// Measured off each model: the underside of the rifle's handguard, the
// shotgun's pump, the sniper's forend and the middle of the launcher's forward
// grip; and for the pistol, the left face of the grip just below the right hand.
var grips = []grip{
	{weapon: "rifle", clips: "rifle", source: "rifle",
		palm: mgl32.Vec3{-0.005, 0.03, -0.30}, along: mgl32.Vec3{0.35, 0.35, -0.85}},
	{weapon: "shotgun", clips: "shotgun", source: "rifle",
		palm: mgl32.Vec3{-0.005, -0.02, -0.28}, along: mgl32.Vec3{0.35, 0.35, -0.85}},
	{weapon: "sniper", clips: "sniper", source: "rifle",
		palm: mgl32.Vec3{-0.005, -0.01, -0.24}, along: mgl32.Vec3{0.35, 0.35, -0.85}},
	{weapon: "rocket_launcher", clips: "rocket", source: "rifle",
		palm: mgl32.Vec3{-0.01, -0.05, -0.25}, along: mgl32.Vec3{0.45, 0.25, -0.75},
		shoulder: &launcherOnShoulder},
	{weapon: "pistol", clips: "pistol", source: "pistol",
		palm: mgl32.Vec3{-0.03, -0.035, 0.01}, along: mgl32.Vec3{0.6, 0.45, -0.5}},
}

// launcherOnShoulder puts the launcher's grip low and to the right of the eye
// and a quarter-metre ahead, so the tube — whose bore is 7.5cm above the grip —
// runs forward past the right of the face with its rear end, shoulder rest
// and all, behind the camera: carried on the shoulder, not out in the hands.
var launcherOnShoulder = mgl32.Vec3{0.19, -0.195, -0.25}

// hipOffset is where player_combat.flang holds the hands at the hip. A
// shoulder carry is authored in the camera's own space, so the hands'
// offset is taken back off to find where that is in the rig's.
var hipOffset = mgl32.Vec3{0.13, -0.06, 0}

// clipSuffixes are the clips a weapon is held with.
var clipSuffixes = []string{"_idle", "_walk", "_fire", "_fire_ads"}

// mountRotation is how every weapon rides hand_socket.R — level1.yaml's.
var mountRotation = mgl32.Vec3{math.Pi, math.Pi / 2, 0}

// The direction each forearm runs, elbow to wrist, in the rig's own space
// (camera convention: +X right, +Y up, −Z ahead). Mostly up, so the forearms
// enter from the bottom of the screen; forward, so they lean into the gun; and
// in toward the middle from their own side.
var (
	rightForearm = mgl32.Vec3{-0.3, 0.75, -0.6}
	leftForearm  = mgl32.Vec3{0.3, 0.75, -0.6}
)

// sampleRate is how often a fitted arm is keyed. The clip's own bones are
// interpolated between their keys, so an arm keyed only where they are would
// drift off the gun in between; at 30 a second that drift is below a millimetre
// on these clips.
const sampleRate = 30

// arm is one side's bones.
type arm struct{ upper, fore, hand int }

func main() {
	armsPath := flag.String("arms", "resources/models/fp_arms.fmdl", "the first-person rig to finish, rewritten in place")
	modelsDir := flag.String("models", "resources/models", "where the weapon models are")
	flag.Parse()

	doc, err := model.LoadModel(*armsPath)
	if err != nil {
		fail("%v", err)
	}

	fmt.Printf("cut %d upper-arm faces\n", cutUpperArms(doc))

	left := arm{mustBone(doc, "upper_arm.L"), mustBone(doc, "forearm.L"), mustBone(doc, "hand.L")}
	right := arm{mustBone(doc, "upper_arm.R"), mustBone(doc, "forearm.R"), mustBone(doc, "hand.R")}

	for _, g := range grips {
		gun, err := model.LoadModel(filepath.Join(*modelsDir, g.weapon+".fmdl"))
		if err != nil {
			fail("%v", err)
		}
		var carry *mgl32.Mat4
		if g.shoulder != nil {
			m := shoulderCarry(doc, gun, g)
			carry = &m
		}
		for _, suffix := range clipSuffixes {
			src := doc.ClipIndexByName(g.source + suffix)
			if src < 0 {
				fail("%s has no clip %q to hold the %s with", *armsPath, g.source+suffix, g.weapon)
			}
			dst := src
			if g.clips != g.source {
				dst = replaceClip(doc, src, g.clips+suffix)
			}
			palmErr, gunErr := fitArms(doc, doc.Clip(dst), gun, g, left, right, carry)
			fmt.Printf("  %-18s left palm within %.2f mm of the %s's grip; gun moved %.3f mm\n",
				doc.Clip(dst).Name, palmErr*1000, g.weapon, gunErr*1000)
		}
	}

	if err := model.SaveModel(doc, *armsPath); err != nil {
		fail("%v", err)
	}
	fmt.Println("wrote", *armsPath)
}

// cutUpperArms deletes every face whose vertices are all skinned mostly to an
// upper arm, and reports how many went.
func cutUpperArms(doc *model.ModelDoc) int {
	cut := map[int]bool{}
	for _, name := range []string{"upper_arm.L", "upper_arm.R"} {
		if bi := doc.BoneIndexByName(name); bi >= 0 {
			cut[bi] = true
		}
	}
	doc.EnsureWeights()
	var faces []int
	for fi := range doc.Faces {
		all := true
		for _, v := range doc.Faces[fi].Verts {
			if !cut[dominantBone(doc, v)] {
				all = false
				break
			}
		}
		if all {
			faces = append(faces, fi)
		}
	}
	doc.DeleteFaces(faces)
	return len(faces)
}

func dominantBone(doc *model.ModelDoc, v int) int {
	best, bestW := -1, float32(-1)
	if v < len(doc.Weights) {
		for _, w := range doc.Weights[v] {
			if w.Weight > bestW {
				best, bestW = w.Bone, w.Weight
			}
		}
	}
	return best
}

// replaceClip makes a fresh copy of clip src called name, replacing any clip
// already called that, and returns its index.
func replaceClip(doc *model.ModelDoc, src int, name string) int {
	srcName := doc.Clip(src).Name
	if old := doc.ClipIndexByName(name); old >= 0 {
		doc.DeleteClip(old)
	}
	i := doc.DuplicateClip(doc.ClipIndexByName(srcName))
	doc.RenameClip(i, name)
	return doc.ClipIndexByName(name)
}

// fitArms re-keys both arms through one weapon clip, and returns the worst
// distance it left between the left palm and its grip, and the furthest the
// gun moved from where the clip had it — which should be nothing.
func fitArms(doc *model.ModelDoc, clip *model.ModelClip, gun *model.ModelDoc, g grip, left, right arm, carry *mgl32.Mat4) (palmErr, gunErr float32) {
	n := doc.BoneCount()

	// The rig as the renderer builds it: hands at the origin of their own space,
	// the weapon on the right hand socket. Only the palette is ever written, so
	// every sample reads the gun's frame exactly as a frame of play would.
	e := &world.Entity{ID: "fit", Scale: mgl32.Vec3{1, 1, 1}}
	hands := world.EntityModel{Name: "hands", Visible: true, Scale: mgl32.Vec3{1, 1, 1}}
	hands.Doc = doc
	hands.Anim = world.NewEntityAnim(doc)
	held := world.EntityModel{
		Name: "gun", Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Parent: "hands", Bone: "hand_socket.R", Rotation: mountRotation,
	}
	held.Doc = gun
	e.Models = []world.EntityModel{hands, held}
	gunFrame := func(p model.ModelPose) mgl32.Mat4 {
		copy(e.Models[0].Anim.Palette, doc.PoseDeltas(p))
		return e.AttachmentMatrix(&e.Models[1])
	}

	// Every pose first, from the clip as it stands, before any key is changed:
	// sampling a clip part-way through being re-keyed would read arms that are
	// half old and half new.
	times := sampleTimes(clip)
	poses := make([]model.ModelPose, len(times))
	for i, t := range times {
		poses[i] = model.NewModelPose(n)
		clip.SampleInto(&poses[i], n, t)
	}

	for i := range poses {
		p := &poses[i]
		before := gunFrame(*p)

		// The right forearm up from below, and the hand — and so the gun —
		// put back exactly where it was.
		// For a shoulder carry, the hand is first moved — the whole hand, and
		// so the weapon on it — by the one fixed transform that lifts the
		// launcher from where the rifle clip holds it onto the shoulder. The
		// clip's own motion rides on top unchanged: a walk still bobs it and
		// a shot still kicks it.
		handFrame := doc.PoseMatrices(*p)[right.hand]
		if carry != nil {
			handFrame = carry.Mul4(handFrame)
			before = carry.Mul4(before)
		}
		bringUpFromBelow(doc, p, right, handFrame.Col(3).Vec3(), rightForearm)
		setBoneFrame(doc, p, right.hand, handFrame)

		after := gunFrame(*p)
		if d := after.Col(3).Vec3().Sub(before.Col(3).Vec3()).Len(); d > gunErr {
			gunErr = d
		}

		// The left palm on the grip, and its forearm up from below the wrist.
		palm := after.Mul4x1(g.palm.Vec4(1)).Vec3()
		along := model.XformDir(after, g.along).Normalize()
		wrist := palm.Sub(along.Mul(doc.BoneLength(left.hand)))
		bringUpFromBelow(doc, p, left, wrist, leftForearm)
		aimBone(doc, p, left.hand, along)

		tip := doc.PoseMatrices(*p)[left.hand].Mul4x1(mgl32.Vec4{0, doc.BoneLength(left.hand), 0, 1}).Vec3()
		if d := tip.Sub(palm).Len(); d > palmErr {
			palmErr = d
		}
	}

	for _, a := range []arm{left, right} {
		for _, bi := range []int{a.upper, a.fore, a.hand} {
			clip.ClearBone(bi)
			for i, t := range times {
				clip.SetKey(bi, t, poses[i].Bone(bi))
			}
		}
	}
	return palmErr, gunErr
}

// shoulderCarry is the transform, in the rig's own space, that moves a weapon
// from where the source idle clip's first frame holds it to the grip's shoulder
// placement — the same pose every aim-down-sights solve is taken against, so
// it is the one the carry is authored against too.
func shoulderCarry(doc *model.ModelDoc, gun *model.ModelDoc, g grip) mgl32.Mat4 {
	idle := doc.ClipIndexByName(g.source + "_idle")
	if idle < 0 {
		fail("no %s_idle clip to carry the %s from", g.source, g.weapon)
	}
	p := model.NewModelPose(doc.BoneCount())
	doc.Clip(idle).SampleInto(&p, doc.BoneCount(), 0)

	e := &world.Entity{ID: "carry", Scale: mgl32.Vec3{1, 1, 1}}
	hands := world.EntityModel{Name: "hands", Visible: true, Scale: mgl32.Vec3{1, 1, 1}}
	hands.Doc = doc
	hands.Anim = world.NewEntityAnim(doc)
	held := world.EntityModel{
		Name: "gun", Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Parent: "hands", Bone: "hand_socket.R", Rotation: mountRotation,
	}
	held.Doc = gun
	e.Models = []world.EntityModel{hands, held}
	copy(e.Models[0].Anim.Palette, doc.PoseDeltas(p))
	from := e.AttachmentMatrix(&e.Models[1])

	at := g.shoulder.Sub(hipOffset)
	to := mgl32.Translate3D(at.X(), at.Y(), at.Z())
	return to.Mul4(from.Inv())
}

// bringUpFromBelow places one arm's forearm so it runs along dir and ends at
// wrist. The upper arm is swung to point at the new elbow — it has no mesh, but
// a rig shown with its bones in the model editor should still read as an arm —
// and the forearm is slid onto that elbow rather than left hanging off the
// upper arm's real tail, which is nowhere near it.
func bringUpFromBelow(doc *model.ModelDoc, p *model.ModelPose, a arm, wrist, dir mgl32.Vec3) {
	elbow := wrist.Sub(dir.Normalize().Mul(doc.BoneLength(a.fore)))
	shoulder := doc.PoseMatrices(*p)[a.upper].Col(3).Vec3()
	aimBone(doc, p, a.upper, elbow.Sub(shoulder))
	placeBone(doc, p, a.fore, elbow)
	aimBone(doc, p, a.fore, wrist.Sub(elbow))
}

// localOf is a bone pose's own transform: translate, rotate, scale — the order
// ModelBonePose builds it in (model_pose.go).
func localOf(bp model.ModelBonePose) mgl32.Mat4 {
	s := bp.EffectiveScale()
	return mgl32.Translate3D(bp.Trans.X(), bp.Trans.Y(), bp.Trans.Z()).
		Mul4(bp.EffectiveRot().Mat4()).Mul4(mgl32.Scale3D(s.X(), s.Y(), s.Z()))
}

// frameBefore is the frame a bone's own pose transform acts in: where it would
// be with that transform taken back off. The pose convention is
// posed = parentPosed · restLocal · local, so this is parentPosed · restLocal.
func frameBefore(doc *model.ModelDoc, p model.ModelPose, bi int) mgl32.Mat4 {
	return doc.PoseMatrices(p)[bi].Mul4(localOf(p.Bone(bi)).Inv())
}

// aimBone swings one bone of the pose so it points along want in the rig's own
// space, keeping as much of its existing twist as the swing allows.
func aimBone(doc *model.ModelDoc, p *model.ModelPose, bi int, want mgl32.Vec3) {
	bp := p.Bone(bi)
	rot := bp.EffectiveRot()
	target := model.XformDir(frameBefore(doc, *p, bi).Inv(), want).Normalize()
	current := rot.Rotate(mgl32.Vec3{0, 1, 0})
	bp.Rot = mgl32.QuatBetweenVectors(current, target).Mul(rot).Normalize()
	p.Set(bi, bp)
}

// placeBone slides one bone of the pose so its head sits at head, in the rig's
// own space, without changing which way it points.
func placeBone(doc *model.ModelDoc, p *model.ModelPose, bi int, head mgl32.Vec3) {
	bp := p.Bone(bi)
	bp.Trans = frameBefore(doc, *p, bi).Inv().Mul4x1(head.Vec4(1)).Vec3()
	p.Set(bi, bp)
}

// setBoneFrame puts one bone back at a whole placement — position and
// orientation both — in the rig's own space, whatever its parents now do.
func setBoneFrame(doc *model.ModelDoc, p *model.ModelPose, bi int, frame mgl32.Mat4) {
	bp := p.Bone(bi)
	local := frameBefore(doc, *p, bi).Inv().Mul4(frame)
	bp.Trans = local.Col(3).Vec3()
	bp.Rot = mgl32.Mat4ToQuat(local).Normalize()
	p.Set(bi, bp)
}

// sampleTimes is every key the clip already has, plus a steady sampleRate
// across its length.
func sampleTimes(clip *model.ModelClip) []float32 {
	seen := map[float32]bool{}
	for _, t := range clip.KeyTimes() {
		seen[t] = true
	}
	d := clip.EffectiveDuration()
	steps := int(math.Ceil(float64(d) * sampleRate))
	for i := 0; i <= steps; i++ {
		t := float32(i) / sampleRate
		if t > d {
			t = d
		}
		seen[float32(math.Round(float64(t)*10000)/10000)] = true
	}
	out := make([]float32, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

func mustBone(doc *model.ModelDoc, name string) int {
	bi := doc.BoneIndexByName(name)
	if bi < 0 {
		fail("the rig has no bone called %q", name)
	}
	return bi
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
