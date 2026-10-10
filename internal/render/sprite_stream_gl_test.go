package render

// sprite_stream_gl_test.go — the sprite pass's instance buffer, drawn through:
// several batches written in one go, each drawn from its own stretch by its
// base instance, frame after frame round the mapped ring and through the
// orphaned fallback alike.
//
//	FOUNDRY_GL_TEST=1 go test -run TestSpriteBatchesDrawFromTheirOwnStretch ./internal/render/

import (
	"testing"

	"foundry/internal/gfx"
	"foundry/internal/glext"
	"foundry/internal/gltest"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
)

func spriteTestTexture(t *testing.T, name string) {
	t.Helper()
	var tex uint32
	gl.GenTextures(1, &tex)
	gl.BindTexture(gl.TEXTURE_2D, tex)
	white := []uint8{255, 255, 255, 255}
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, 1, 1, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(white))
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.BindTexture(gl.TEXTURE_2D, 0)
	prev, had := gfx.Textures[name]
	gfx.Textures[name] = tex
	t.Cleanup(func() {
		gl.DeleteTextures(1, &tex)
		if had {
			gfx.Textures[name] = prev
		} else {
			delete(gfx.Textures, name)
		}
	})
}

func TestSpriteBatchesDrawFromTheirOwnStretch(t *testing.T) {
	for _, persistent := range []bool{true, false} {
		name := "persistent"
		if !persistent {
			name = "orphaned"
		}
		t.Run(name, func(t *testing.T) {
			gltest.Context(t)
			withRepoResources(t)
			volTestTarget(t)
			if gfx.Textures == nil {
				gfx.Textures = map[string]uint32{}
			}
			spriteTestTexture(t, "sprite_test_a")
			spriteTestTexture(t, "sprite_test_b")
			prev := SpritePersistent
			SpritePersistent = persistent
			DeleteSprites()
			SpriteInit()
			t.Cleanup(func() {
				DeleteSprites()
				SpritePersistent = prev
			})
			if persistent && glext.BufferStorageAvailable() && !sprites.stream.persistent {
				t.Fatal("buffer storage is there but the ring was not mapped")
			}
			t.Logf("mapped ring: %v", sprites.stream.persistent)

			gl.Disable(gl.CULL_FACE)
			eye := mgl32.Vec3{}
			projView := volLook(mgl32.Vec3{0, 0, 1}, mgl32.Vec3{0, 1, 0})
			// A batch of offscreen filler first, so the two that show are not
			// at the start of the buffer; then red on the left and green on
			// the right, in batches of their own.
			filler := spriteBatch{Texture: "sprite_test_a", Blend: gfx.SpriteBlendAdditive}
			for i := 0; i < 5000; i++ {
				filler.Instances = append(filler.Instances, SpriteInstance{Pos: mgl32.Vec3{0, 0, -10}, Size: mgl32.Vec2{0.1, 0.1}})
			}
			quad := func(x float32, c mgl32.Vec4) SpriteInstance {
				return SpriteInstance{Pos: mgl32.Vec3{x, 0, 10}, Size: mgl32.Vec2{4, 4}, Color: c, UV: mgl32.Vec4{0, 0, 1, 1}}
			}
			for frame := 0; frame < 12; frame++ {
				drawFrameNo++
				drawFrameOpen = true
				// Big enough that the ring comes round more than once over
				// the frames, and has to wait on itself.
				big := spriteBatch{Texture: "sprite_test_b", Blend: gfx.SpriteBlendAdditive}
				for i := 0; i < 40000; i++ {
					big.Instances = append(big.Instances, SpriteInstance{Pos: mgl32.Vec3{0, 0, -10}, Size: mgl32.Vec2{0.1, 0.1}})
				}
				gl.ClearColor(0, 0, 0, 1)
				gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
				// Mirror the two sides every other frame, so a stretch read
				// from the wrong place shows.
				left, right := mgl32.Vec4{1, 0, 0, 1}, mgl32.Vec4{0, 1, 0, 1}
				if frame%2 == 1 {
					left, right = right, left
				}
				batches := []spriteBatch{filler,
					{Texture: "sprite_test_a", Blend: gfx.SpriteBlendAdditive, Instances: []SpriteInstance{quad(4, left)}},
					big,
					{Texture: "sprite_test_b", Blend: gfx.SpriteBlendAdditive, Instances: []SpriteInstance{quad(-4, right)}},
				}
				if e := gl.GetError(); e != gl.NO_ERROR {
					t.Fatalf("frame %d: GL error 0x%x before the pass", frame, e)
				}
				// The lighting upload is what points every sampler at a unit
				// of its own.
				uploadSpriteLighting(sprites.color.prog, ambientLevel(), ShadowFrame{})
				drawSpritePass(batches, projView, eye, gfx.SpriteBlendAdditive)
				if e := gl.GetError(); e != gl.NO_ERROR {
					t.Fatalf("frame %d: GL error 0x%x", frame, e)
				}
				drawFrameOpen = false
				var l, r [4]uint8
				// +X is to the left looking down +Z with +Y up.
				glext.ReadPixels(volTestSize/4, volTestSize/2, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&l[0]))
				glext.ReadPixels(3*volTestSize/4, volTestSize/2, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(&r[0]))
				want := func(c mgl32.Vec4) [3]uint8 { return [3]uint8{uint8(c[0] * 255), uint8(c[1] * 255), uint8(c[2] * 255)} }
				if [3]uint8{l[0], l[1], l[2]} != want(left) || [3]uint8{r[0], r[1], r[2]} != want(right) {
					t.Fatalf("frame %d: left %v right %v, want %v and %v", frame, l, r, want(left), want(right))
				}
			}
			if e := gl.GetError(); e != gl.NO_ERROR {
				t.Errorf("GL error 0x%x", e)
			}
		})
	}
}
