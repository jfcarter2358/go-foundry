// Command viewmodelfit solves a first-person weapon's aim-down-sights pose,
// and measures what any pose does to the screen.
//
//	viewmodelfit -models <project>/resources/models -arms fp_arms -idle rifle_idle \
//	             -weapon sniper -sight 0.128
//
// WHAT IT ANSWERS. A script that aims down the sights writes two numbers onto
// the viewmodel's root attachment — an offset and a rotation — such that the
// weapon's sight line runs through the eye along the view axis, which is what
// puts the sights in the middle of the screen. Those numbers depend on the
// hand pose of the idle clip, on the rotation the weapon is mounted with, and
// on the height of the weapon's own sights, so they cannot be guessed and have
// to be re-solved whenever one of the three changes. This is the arithmetic
// that solves them, run against the real rig through the engine's own bone
// posing rather than against a copy of it.
//
// The sight line is taken as the weapon's own −Z at `-sight` units above its
// origin, which is where a model's sights are when they are authored level —
// a rib, a scope tube, two posts of equal height. A weapon whose rear sight is
// deliberately higher than its front needs its own tilt on top of what comes
// out of here.
//
// WHAT IT ALSO PRINTS, for every pose it reports: how far the geometry reaches
// in front of the eye, how much of it is at or behind the near plane, and how
// far off the middle of the screen the worst vertex lands. Those are what tell
// an arm sliced open by the near plane from one that merely looks large, and
// how large the weapon looks. -distance (or -z, the same choice asked the other
// way) sets how far in front of the eye the sights sit; the sight picture is
// the same at every value of it, since sliding along the view axis leaves a
// line through the eye running through the eye — but too near and the weapon
// fills the screen, too far and it is a toy.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"foundry/internal/model"
	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
)

func main() {
	models := flag.String("models", "resources/models", "directory holding the .fmdl files")
	armsName := flag.String("arms", "fp_arms", "the first-person rig")
	idle := flag.String("idle", "rifle_idle", "the clip whose first frame the pose is solved against")
	weapon := flag.String("weapon", "rifle", "the weapon model to fit")
	sight := flag.Float64("sight", 0.14, "height of the weapon's sight line above its own origin")
	zs := flag.String("z", "", "comma-separated hand-root offsets along the view axis to report; negative is in front of the eye")
	distance := flag.Float64("distance", 0.46, "how far in front of the eye the sight sits, when -z is not given")
	mountX := flag.Float64("mount-x", math.Pi, "the weapon mount's X rotation, in radians")
	mountY := flag.Float64("mount-y", math.Pi/2, "…Y")
	mountZ := flag.Float64("mount-z", 0, "…Z")
	fov := flag.Float64("fov", 55, "the viewmodel's field of view, for the screen measurements")
	flag.Parse()

	load := func(name string) *model.ModelDoc {
		doc, err := model.LoadModel(filepath.Join(*models, name+".fmdl"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return doc
	}
	arms, gun := load(*armsName), load(*weapon)

	// The rig, posed on the first frame of its idle clip — exactly the entity
	// the renderer draws, minus the upload.
	e := &world.Entity{ID: "player", Scale: mgl32.Vec3{1, 1, 1}}
	hands := world.EntityModel{
		Name: "hands", Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Animations: []world.EntityAnimation{{Name: *idle, Clip: *idle}},
	}
	hands.Doc = arms
	hands.Anim = world.NewEntityAnim(arms)
	held := world.EntityModel{
		Name: "gun", Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Parent: "hands", Bone: "hand_socket.R",
		Rotation: mgl32.Vec3{float32(*mountX), float32(*mountY), float32(*mountZ)},
	}
	held.Doc = gun
	e.Models = []world.EntityModel{hands, held}
	if !e.PlayAnimation("hands", *idle) {
		fmt.Fprintf(os.Stderr, "%s has no clip called %q\n", *armsName, *idle)
		os.Exit(1)
	}
	e.UpdateAnimations(0)

	// With the hands at the origin, the attachment matrix IS where the weapon
	// sits in the hands' own space: the one constant everything below is
	// solved against.
	e.Models[0].Offset, e.Models[0].Rotation = mgl32.Vec3{}, mgl32.Vec3{}
	K := e.AttachmentMatrix(&e.Models[1])
	bore := K.Mul4x1(mgl32.Vec4{0, 0, -1, 0}).Vec3().Normalize()
	onSight := K.Mul4x1(mgl32.Vec4{0, float32(*sight), 0, 1}).Vec3()

	// The rotation that turns the bore onto the view axis is the inverse of the
	// one that would produce the bore from it, decomposed back into the
	// engine's Y-then-X-then-Z Euler order.
	pitch := float32(math.Asin(float64(bore.Y())))
	yaw := float32(math.Atan2(float64(-bore.X()), float64(-bore.Z())))
	M := world.EulerMatrixYXZ(mgl32.Vec3{pitch, yaw, 0}).Transpose()
	rot := mgl32.Vec3{
		float32(math.Asin(float64(-M.At(1, 2)))),
		float32(math.Atan2(float64(M.At(0, 2)), float64(M.At(2, 2)))),
		float32(math.Atan2(float64(M.At(1, 0)), float64(M.At(1, 1)))),
	}
	R := world.EulerMatrixYXZ(rot)
	// x and y are what put the sight line on the eye; z is free, and is the
	// -z flag's to choose.
	turned := R.Mul4x1(onSight.Vec4(1)).Vec3()

	posed := make([]mgl32.Vec3, len(arms.Verts))
	model.SkinVerts(e.Models[0].Anim.Palette, arms.Weights, arms.Verts, posed)

	fmt.Printf("%s on %s, %s, sight line %.3f above the grip\n", *weapon, *armsName, *idle, *sight)
	// A sight distance is the more useful way to ask: it is what decides how
	// large the weapon looks, and two weapons at the same distance look held the
	// same way. The hand-root offset that gives it is whatever the rig's own
	// geometry makes it.
	if *zs == "" {
		*zs = fmt.Sprintf("%g", -*distance-float64(turned.Z()))
	}
	for _, s := range strings.Split(*zs, ",") {
		var z float64
		if _, err := fmt.Sscanf(strings.TrimSpace(s), "%g", &z); err != nil {
			continue
		}
		off := mgl32.Vec3{-turned.X(), -turned.Y(), float32(z)}
		e.Models[0].Offset, e.Models[0].Rotation = off, rot
		A := e.AttachmentMatrix(&e.Models[0])
		G := e.AttachmentMatrix(&e.Models[1])

		near, far, behind, total, worst := float32(-1e9), float32(1e9), 0, 0, float32(0)
		t := float32(math.Tan(*fov * math.Pi / 360))
		measure := func(pts []mgl32.Vec3, X mgl32.Mat4) {
			for _, v := range pts {
				c := X.Mul4x1(v.Vec4(1)).Vec3()
				total++
				if c.Z() > near {
					near = c.Z()
				}
				if c.Z() < far {
					far = c.Z()
				}
				if c.Z() > -0.01 {
					behind++
					continue
				}
				if d := abs32(c.X()/(-c.Z())) / t; d > worst {
					worst = d
				}
			}
		}
		measure(posed, A)
		measure(gun.Verts, G)

		fmt.Printf("  offset <%.4f, %.4f, %.4f>  rotation <%.4f, %.4f, %.4f>  (sight %.2f in front of the eye)\n",
			off.X(), off.Y(), off.Z(), rot.X(), rot.Y(), rot.Z(), -(off.Z() + turned.Z()))
		fmt.Printf("      reaches %.2f to %.2f in front of the eye; %d of %d vertices at or behind "+
			"the near plane; worst vertex %.1f screen-widths off centre\n",
			-far, -near, behind, total, worst)
	}
}

func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}
