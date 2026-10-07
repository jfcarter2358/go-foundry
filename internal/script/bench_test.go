package script

// bench_test.go — what one frame of the script runtime costs on a busy level.
//
// The level is shaped like the real ones that made this worth measuring: a
// few hundred entities of which a couple of hundred are scripted, most of
// those answering `:tick:`, a crowd of props whose script answers nothing per
// frame, scripted trigger volumes for the movers to walk through, a director
// that broadcasts to a tag and calls find() every frame, and several hundred
// brushes behind it all for the tag walk to wade through.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/fileutil"
	"foundry/internal/gfx"
	"foundry/internal/world"
)

const benchSoldierSrc = `def on_tick(:tick:, dt float) -> null
    t = @self.attr.timer + dt
    @self.attr.timer = t
    p = @self.prop.position
    step = 0.5
    if @self.attr.dir < 0
        step = -0.5
    end
    x = p.x + step
    if x > 60.0
        @self.attr.dir = -1
    elif x < 0.0
        @self.attr.dir = 1
    end
    @self.prop.position = <x, p.y, p.z>
end
def on_ping(:ping:, from ref) -> null
    @self.attr.pings += 1
end
def on_damage(:damage:, n int) -> null
    @self.attr.hp -= n
end`

const benchBarrelSrc = `def on_damage(:damage:, n int) -> null
    @self.attr.hp -= n
end
def on_spawn(:spawn:) -> null
    @self.attr.hp = 10
end`

const benchDirectorSrc = `def on_tick(:tick:, dt float) -> null
    squad = find("soldiers")
    n = 0
    for s in squad
        n += 1
    end
    @self.attr.count = n
    #soldiers <- (:ping:, @self)
    #barrels <- (:damage:, 0)
end`

const benchTriggerSrc = `def on_enter(:enter:, other ref) -> null
    @self.attr.entered += 1
end
def on_exit(:exit:, other ref) -> null
    @self.attr.exited += 1
end`

// benchScriptDir points the scripts folder at a fresh temp directory and
// writes the benchmark's scripts into it, returning their level paths.
func benchScriptDir(b *testing.B, scripts map[string]string) map[string]string {
	b.Helper()
	prev := fileutil.ResourcesPath
	dir := b.TempDir()
	fileutil.ResourcesPath = dir
	b.Cleanup(func() { fileutil.ResourcesPath = prev })
	root := filepath.Join(dir, fileutil.ScriptsDirName)
	if err := os.MkdirAll(root, 0o755); err != nil {
		b.Fatal(err)
	}
	out := map[string]string{}
	for name, src := range scripts {
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			b.Fatal(err)
		}
		out[name] = fileutil.ScriptsDirName + "/" + name
	}
	return out
}

// benchLevel builds the busy level described at the top of the file.
func benchLevel(paths map[string]string, soldiers, barrels, walkers, triggers, chunks int) *world.Level {
	lvl := &world.Level{}
	for c := 0; c < chunks; c++ {
		ch := world.Chunk{ID: fmt.Sprintf("chunk%d", c)}
		ch.Brushes = append(ch.Brushes,
			world.NewHollowRoom(fmt.Sprintf("a%d", c), mgl32.Vec3{0, 0, 0}, mgl32.Vec3{10, 8, 10}, 0.5, "concrete")...)
		ch.Brushes = append(ch.Brushes,
			world.NewHollowRoom(fmt.Sprintf("b%d", c), mgl32.Vec3{10, 0, 0}, mgl32.Vec3{20, 8, 10}, 0.5, "concrete")...)
		if c%4 == 0 {
			ch.Brushes[0].Tags = []string{"walls"}
		}
		lvl.Chunks = append(lvl.Chunks, ch)
	}
	bounded := func(e world.Entity) world.Entity {
		e.Visible = true
		e.Scale = mgl32.Vec3{1, 1, 1}
		e.BoundingKind = world.BoundingAABB
		e.BoundHalfX, e.BoundHalfY, e.BoundHalfZ = 0.5, 1, 0.5
		return e
	}
	lvl.Entities = append(lvl.Entities, bounded(world.Entity{
		ID: "director", Script: paths["director.flang"],
		Attributes: world.AttrSet{"count": world.IntAttr(0)},
	}))
	for i := 0; i < soldiers; i++ {
		dir := int64(1)
		if i%2 == 1 {
			dir = -1
		}
		lvl.Entities = append(lvl.Entities, bounded(world.Entity{
			ID: fmt.Sprintf("soldier%d", i), Script: paths["soldier.flang"],
			Tags:     []string{"soldiers"},
			Position: mgl32.Vec3{float32(i % 60), 0, float32(i / 60 * 3)},
			Attributes: world.AttrSet{
				"timer": world.FloatAttr(0), "dir": world.IntAttr(dir),
				"pings": world.IntAttr(0), "hp": world.IntAttr(100),
			},
		}))
	}
	for i := 0; i < barrels; i++ {
		lvl.Entities = append(lvl.Entities, bounded(world.Entity{
			ID: fmt.Sprintf("barrel%d", i), Script: paths["barrel.flang"],
			Tags:       []string{"barrels"},
			Position:   mgl32.Vec3{float32(i%40) * 1.5, 0, 40 + float32(i/40*2)},
			Attributes: world.AttrSet{"hp": world.IntAttr(10)},
		}))
	}
	for i := 0; i < walkers; i++ {
		lvl.Entities = append(lvl.Entities, world.Entity{
			ID: fmt.Sprintf("walker%d", i), Visible: true, Scale: mgl32.Vec3{1, 1, 1},
			Position:   mgl32.Vec3{float32(i%50) * 1.2, 0, 60 + float32(i/50*2)},
			Attributes: world.AttrSet{},
		})
	}
	for i := 0; i < triggers; i++ {
		v := world.NewVolume(fmt.Sprintf("trigger%d", i), mgl32.Vec3{float32(i%10) * 6, 0, float32(i/10) * 8})
		v.HalfX, v.HalfY, v.HalfZ = 2, 2, 2
		v.Script = paths["trigger.flang"]
		v.SetAttr("entered", world.IntAttr(0))
		v.SetAttr("exited", world.IntAttr(0))
		lvl.Volumes = append(lvl.Volumes, v)
	}
	return lvl
}

// benchRuntime loads lvl into the runtime, restoring the previous one when the
// benchmark ends.
func benchRuntime(b *testing.B, lvl *world.Level) {
	b.Helper()
	prevUploads := gfx.GeometryUploadsEnabled
	gfx.GeometryUploadsEnabled = false
	prevRT := *Scripts
	prevEv := scriptEvents
	prevPlayer := world.PlayerEntity
	world.PlayerEntity = nil
	b.Cleanup(func() {
		*Scripts = prevRT
		scriptEvents = prevEv
		world.PlayerEntity = prevPlayer
		gfx.GeometryUploadsEnabled = prevUploads
	})
	ScriptEventsReset()
	ScriptRuntimeLoad(lvl)
}

var benchScripts = map[string]string{
	"soldier.flang":  benchSoldierSrc,
	"barrel.flang":   benchBarrelSrc,
	"director.flang": benchDirectorSrc,
	"trigger.flang":  benchTriggerSrc,
}

// BenchmarkScriptTick is one whole frame of the runtime on the busy level.
func BenchmarkScriptTick(b *testing.B) {
	paths := benchScriptDir(b, benchScripts)
	lvl := benchLevel(paths, 200, 150, 300, 50, 40)
	benchRuntime(b, lvl)
	// Settle: the first frames post :spawn: and load lazily.
	for i := 0; i < 3; i++ {
		ScriptTick(lvl, 1.0/60.0, true)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ScriptTick(lvl, 1.0/60.0, true)
	}
}

// BenchmarkOverlapEvents is the containment diff alone, on many movers and
// many containers: every entity both moves and (being scripted and bounded)
// contains.
func BenchmarkOverlapEvents(b *testing.B) {
	paths := benchScriptDir(b, benchScripts)
	lvl := benchLevel(paths, 200, 150, 300, 200, 1)
	benchRuntime(b, lvl)
	ScriptTick(lvl, 1.0/60.0, true)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Scripts.queue = Scripts.queue[:0]
		postOverlapEvents(Scripts, lvl)
	}
}

// BenchmarkTagLookup is a `#tag` resolved against the busy level.
func BenchmarkTagLookup(b *testing.B) {
	paths := benchScriptDir(b, benchScripts)
	lvl := benchLevel(paths, 200, 150, 300, 50, 40)
	benchRuntime(b, lvl)
	ScriptTick(lvl, 1.0/60.0, true)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if n := len(Scripts.host.ResolveTag("barrels")); n != 150 {
			b.Fatalf("%d barrels", n)
		}
	}
}
