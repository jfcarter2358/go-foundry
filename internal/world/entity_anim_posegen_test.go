package world

// The pose generation steps when, and only when, the palette's contents do:
// the point-shadow cache keys a posed character's shadow on it, so a missed
// step freezes the shadow and a spurious one redraws a cube face for nothing
// — every frame, for a character holding the last frame of a finished clip.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"
)

func TestThePoseGenerationStepsOnlyWhenThePaletteChanges(t *testing.T) {
	a := &EntityAnim{Palette: make([]mgl32.Mat4, 3)}
	a.restPalette()
	gen := a.PoseGen

	a.restPalette()
	if a.PoseGen != gen {
		t.Error("re-resting a rest pose stepped the generation")
	}

	pose := []mgl32.Mat4{mgl32.Ident4(), mgl32.Translate3D(0, 1, 0), mgl32.Ident4()}
	a.setPalette(pose)
	if a.PoseGen == gen {
		t.Fatal("a new pose left the generation where it was")
	}
	if a.Palette[1] != pose[1] {
		t.Fatal("setPalette did not write the pose")
	}
	gen = a.PoseGen

	a.setPalette(pose)
	if a.PoseGen != gen {
		t.Error("the same pose again stepped the generation")
	}

	a.restPalette()
	if a.PoseGen == gen {
		t.Error("going back to rest left the generation where it was")
	}
}
