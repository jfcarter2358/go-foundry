package main

import (
	"math"

	"github.com/go-gl/gl/v3.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/veandco/go-sdl2/sdl"
)

type Camera struct {
	Matrix    mgl32.Mat4
	Uniform   int32
	Position  mgl32.Vec3
	Direction mgl32.Vec3
	Yaw       float32
	Pitch     float32
	MouseLock bool
}

var Cam Camera

func LoadCamera(position mgl32.Vec3, pitch, yaw float32) {
	// yaw64 := float64(yaw)
	// pitch64 := float64(pitch)
	Cam.Yaw = yaw
	Cam.Pitch = pitch
	Cam.Position = position
	// Cam.Direction = mgl32.Vec3{
	// 	float32(math.Cos(yaw64) * math.Cos(pitch64)),
	// 	float32(math.Sin(pitch64)),
	// 	float32(math.Sin(yaw64) * math.Cos(pitch64)),
	// }
	Cam.Direction = mgl32.Vec3{0.0, 0.0, 1.0}
	// Cam.Forward = forward
	up := mgl32.Vec3{0.0, 1.0, 0.0}
	Cam.MouseLock = true
	sdl.SetRelativeMouseMode(true)
	sdl.WarpMouseGlobal(0, 0)
	Cam.Matrix = mgl32.LookAtV(Cam.Position, Cam.Position.Add(Cam.Direction), up)
	Cam.Uniform = gl.GetUniformLocation(Program, gl.Str("camera\x00"))
	gl.UniformMatrix4fv(Cam.Uniform, 1, false, &Cam.Matrix[0])
}

func MoveCamera(f, u, r float32) {
	// yaw64 := -math.Pi / 2.0
	yaw64 := float64(Cam.Yaw)
	pitch64 := float64(Cam.Pitch)
	// Cam.Direction = mgl32.Vec3{
	// 	float32(math.Cos(yaw64)),
	// 	0.0,
	// 	float32(math.Sin(yaw64)),
	// }
	Cam.Direction = mgl32.Vec3{
		float32(math.Cos(yaw64) * math.Cos(pitch64)),
		float32(math.Sin(pitch64)),
		float32(math.Sin(yaw64) * math.Cos(pitch64)),
	}
	up := mgl32.Vec3{0.0, 1.0, 0.0}
	forward := mgl32.Vec3{Cam.Direction.X(), 0.0, Cam.Direction.Z()}
	right := mgl32.Vec3{Cam.Direction.Z(), 0.0, -Cam.Direction.X()}
	delta := forward.Mul(f).Add(up.Mul(u)).Add(right.Mul(r))
	// fmt.Printf("delta -- %f, %f, %f\n", delta.X(), delta.Y(), delta.Z())
	Cam.Position = Cam.Position.Add(delta)
	// fmt.Printf("camera -- %f, %f, %f\n", Cam.Position.X(), Cam.Position.Y(), Cam.Position.Z())
}
