package script

// script_volume_cloud_test.go — a script filling a region with cloud, and
// changing the sky's cloud deck.
//
// Both follow the fog and sky properties beside them, and the interesting case
// is the same one: what a write does where there is no block yet — a plain
// trigger with no `cloud:`, a level with no sky — because that is most of them.

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/flang"
	"foundry/internal/world"
)

func TestVolumeWithoutCloudReadsAsOff(t *testing.T) {
	lvl := fogVolumeLevel(t)

	if got := runScript(t, lvl, "cellar", `
def main() -> bool
    return @cellar.prop.cloud
end
`); got != flang.BoolVal(false) {
		t.Errorf("a volume with no cloud block read %v, want false", got)
	}
	if got := runScript(t, lvl, "cellar", `
def main() -> float
    return @cellar.prop.cloud_coverage
end
`); got != flang.FloatVal(float64(world.DefaultVolumeCloud().Coverage)) {
		t.Errorf("coverage read %v, want the default", got)
	}
	if lvl.Volumes[0].Cloud != nil {
		t.Error("merely reading gave the volume a cloud block")
	}
}

func TestScriptCanFillAVolumeWithCloud(t *testing.T) {
	lvl := fogVolumeLevel(t)

	runScript(t, lvl, "cellar", `
def main() -> null
    @cellar.prop.cloud = true
    @cellar.prop.cloud_color = <0.4, 0.4, 0.45>
    @cellar.prop.cloud_coverage = 0.9
    @cellar.prop.cloud_wind = <3, 0, 0>
    @cellar.prop.cloud_shadows = false
    return null
end
`)

	c := lvl.Volumes[0].Cloud
	if c == nil {
		t.Fatal("the script's write did not give the volume any cloud")
	}
	if !c.Enabled || c.Color != (mgl32.Vec3{0.4, 0.4, 0.45}) || c.Coverage != 0.9 ||
		c.Wind != (mgl32.Vec3{3, 0, 0}) || c.CastShadows {
		t.Errorf("cloud is %+v", *c)
	}
	if c.Density != world.DefaultVolumeCloud().Density {
		t.Errorf("an untouched field read %v rather than its default", c.Density)
	}
	if !lvl.Volumes[0].HasCloud() {
		t.Error("the volume still would not be drawn")
	}
}

func TestSwitchingOffCloudThatWasNeverThereAddsNothing(t *testing.T) {
	lvl := fogVolumeLevel(t)
	runScript(t, lvl, "cellar", `
def main() -> null
    @cellar.prop.cloud = false
    return null
end
`)
	if lvl.Volumes[0].Cloud != nil {
		t.Errorf("the volume acquired a cloud block from being switched off: %+v", lvl.Volumes[0].Cloud)
	}
}

func TestScriptCloudWritesAreClamped(t *testing.T) {
	lvl := fogVolumeLevel(t)
	runScript(t, lvl, "cellar", `
def main() -> null
    @cellar.prop.cloud_coverage = 5
    @cellar.prop.cloud_density = 1000
    return null
end
`)
	c := lvl.Volumes[0].Cloud
	if c == nil {
		t.Fatal("no cloud block")
	}
	if c.Coverage != 1 || c.Density != world.CloudMaxDensity {
		t.Errorf("coverage %v and density %v; want them clamped to 1 and %v", c.Coverage, c.Density, world.CloudMaxDensity)
	}
}

// A deck written into a level with no sky gives it a sky and a deck, the
// untouched numbers at their defaults; switching the deck off keeps them.
func TestScriptCanGiveTheSkyACloudDeck(t *testing.T) {
	lvl := skyTestLevel(t)

	runScript(t, lvl, "", `
def main() -> null
    @level.prop.sky_cloud_altitude = 300
    @level.prop.sky_cloud_color = <0.9, 0.8, 0.7>
    return null
end
`)
	if lvl.Sky == nil || lvl.Sky.Clouds == nil {
		t.Fatal("the write gave the level no sky or no deck")
	}
	d := lvl.Sky.Clouds
	if !d.Enabled || d.Altitude != 300 || d.Color != (mgl32.Vec3{0.9, 0.8, 0.7}) {
		t.Errorf("deck is %+v", *d)
	}
	if d.Thickness != world.DefaultSkyClouds().Thickness {
		t.Errorf("thickness %v, want the default", d.Thickness)
	}

	runScript(t, lvl, "", `
def main() -> null
    @level.prop.sky_clouds = false
    return null
end
`)
	if lvl.Sky.Clouds == nil || lvl.Sky.Clouds.Enabled || lvl.Sky.Clouds.Altitude != 300 {
		t.Errorf("switching the deck off should keep its numbers: %+v", lvl.Sky.Clouds)
	}

	clear := skyTestLevel(t)
	runScript(t, clear, "", `
def main() -> null
    @level.prop.sky_clouds = false
    return null
end
`)
	if clear.Sky != nil {
		t.Error("switching off a deck that was never there gave the level a sky")
	}
}

// ease_sky arms a blend; the deck written after it is the destination and the
// deck before it is kept to ease from.
func TestEaseSkyEasesTheDeck(t *testing.T) {
	lvl := skyTestLevel(t)
	sky := world.DefaultSky()
	deck := world.DefaultSkyClouds()
	sky.Clouds = &deck
	lvl.Sky = &sky
	before := deck.Coverage

	runScript(t, lvl, "", `
def main() -> null
    ease_sky(4)
    @level.prop.sky_cloud_coverage = 1
    return null
end
`)
	if lvl.Sky.TransitionFrom == nil || lvl.Sky.TransitionFrom.Clouds == nil {
		t.Fatal("ease_sky did not snapshot the deck")
	}
	if lvl.Sky.TransitionFrom.Clouds.Coverage != before {
		t.Errorf("the snapshot's coverage is %v, want the %v it had before the write", lvl.Sky.TransitionFrom.Clouds.Coverage, before)
	}
	if lvl.Sky.Clouds.Coverage != 1 {
		t.Errorf("the live deck's coverage is %v, want 1", lvl.Sky.Clouds.Coverage)
	}
}
