package script

// script_attach_test.go — the attach verbs, and the two things about them that
// are easy to get wrong.
//
// The first is the pair's asymmetry: `attach` leaves Position alone because it
// becomes an offset in the carrier's space, and `detach` rebases it because a
// local offset means nothing once there is nothing to be local to. Both
// directions are asserted here, because either one silently doing the other's
// job puts effects somewhere plausible-looking and wrong.
//
// The second is that a bone binding needs an attachment name the level may not
// have had to supply, and the runtime and the linter have to say the same thing
// about it — the arrangement script_wiring_test.go guards for play_anim.

import (
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"foundry/internal/model"
	"foundry/internal/modeltest"
	"foundry/internal/world"
)

// boneDoc is a model with a three-bone chain, the first renamed so the tests
// can address it by a name an author would actually write.
func boneDoc(t *testing.T) *model.ModelDoc {
	t.Helper()
	m, idx := modeltest.BoneChain(t)
	m.Bones[idx[0]].Name = "hand_r"
	return m
}

// attachHostLevel is hostTestLevel plus the three things these verbs need: a
// rigged entity to ride, a sprite and an emitter to do the riding.
func attachHostLevel(t *testing.T, models ...world.EntityModel) *world.Level {
	t.Helper()
	lvl := hostTestLevel(t)

	if len(models) == 0 {
		models = []world.EntityModel{{Name: "body", Doc: boneDoc(t)}}
	}
	guard := world.Entity{ID: "guard", Visible: true, Scale: mgl32.Vec3{1, 1, 1},
		Position: mgl32.Vec3{10, 0, 0}, Models: models}
	lvl.Entities = append(lvl.Entities, guard)

	lvl.Sprites = append(lvl.Sprites, world.NewSprite("torch", mgl32.Vec3{0, 2, 0}))
	lvl.Emitters = append(lvl.Emitters, world.NewEmitter("smoke", mgl32.Vec3{0, 3, 0}))
	return lvl
}

// ---------- binding ----------

// TestAttachBindsASpriteToAnEntity, and leaves its position alone — the half of
// the contract that makes `spawn_effect` then `attach` land on the carrier
// rather than back where the effect was spawned.
func TestAttachBindsASpriteToAnEntity(t *testing.T) {
	lvl := attachHostLevel(t)
	runScript(t, lvl, "", script(`    attach(@torch, @guard)`))

	if got := lvl.Sprites[0].Attach; got != (world.Attach{Entity: "guard"}) {
		t.Errorf("sprite bound to %+v, want the entity guard", got)
	}
	if got := lvl.Sprites[0].Position; got != (mgl32.Vec3{0, 2, 0}) {
		t.Errorf("attach moved the sprite to %v; position is meant to become a local offset, not be rebased", got)
	}
}

func TestAttachBindsAnEmitter(t *testing.T) {
	lvl := attachHostLevel(t)
	runScript(t, lvl, "", script(`    attach(@smoke, @guard)`))

	if got := lvl.Emitters[0].Attach; got != (world.Attach{Entity: "guard"}) {
		t.Errorf("emitter bound to %+v, want the entity guard", got)
	}
}

// TestAttachToABoneFindsTheOnlyAttachment — the three-argument form. Attach.Model
// has to be filled in, because boneMatrix resolves the bone *through* the
// attachment and a binding without one silently falls back to the entity.
func TestAttachToABoneFindsTheOnlyAttachment(t *testing.T) {
	lvl := attachHostLevel(t)
	runScript(t, lvl, "", script(`    attach(@torch, @guard, "hand_r")`))

	want := world.Attach{Entity: "guard", Model: "body", Bone: "hand_r"}
	if got := lvl.Sprites[0].Attach; got != want {
		t.Errorf("bound to %+v, want %+v", got, want)
	}
}

// TestAttachToABoneNamesTheAttachmentWhenThereAreSeveral — the four-argument
// form, and the refusal to guess when the short one is ambiguous.
func TestAttachToABoneNamesTheAttachmentWhenThereAreSeveral(t *testing.T) {
	models := []world.EntityModel{
		{Name: "body", Doc: boneDoc(t)},
		{Name: "turret", Doc: boneDoc(t)},
	}
	lvl := attachHostLevel(t, models...)
	runScript(t, lvl, "", script(`    attach(@torch, @guard, "turret", "hand_r")`))

	want := world.Attach{Entity: "guard", Model: "turret", Bone: "hand_r"}
	if got := lvl.Sprites[0].Attach; got != want {
		t.Errorf("bound to %+v, want %+v", got, want)
	}

	lvl = attachHostLevel(t, models...)
	got := warningsFrom(t, lvl, "", `    attach(@torch, @guard, "hand_r")`)
	wantOneWarning(t, got, "attach did nothing", "several model attachments", `"body", "turret"`)
	if !lvl.Sprites[0].Attach.IsZero() {
		t.Error("the ambiguous binding was made anyway")
	}
}

func TestAttachWarnsOnABoneThatIsNotThere(t *testing.T) {
	lvl := attachHostLevel(t)
	got := warningsFrom(t, lvl, "", `    attach(@torch, @guard, "hand_l")`)
	wantOneWarning(t, got, "attach did nothing", `no bone called "hand_l"`, `did you mean "hand_r"?`)
}

func TestAttachWarnsOnAModelWithNoArmature(t *testing.T) {
	lvl := attachHostLevel(t, world.EntityModel{Name: "body", Doc: model.NewCubeDoc()})
	got := warningsFrom(t, lvl, "", `    attach(@torch, @guard, "hand_r")`)
	wantOneWarning(t, got, "attach did nothing", "no armature")
}

// TestAttachToAFrame — the vehicle case. A frame exists because a chunk names
// its owner, so the verb is checking the FrameSet and not "is this a vehicle".
func TestAttachToAFrame(t *testing.T) {
	lvl := attachHostLevel(t)
	lvl.Chunks = append(lvl.Chunks, world.Chunk{ID: "hull", FrameOwner: "guard"})
	fs, err := world.BuildFrames(lvl)
	if err != nil {
		t.Fatalf("BuildFrames: %v", err)
	}
	lvl.Frames = fs
	world.RefreshFrames(lvl, lvl.Frames)

	runScript(t, lvl, "", script(`    attach(@smoke, "guard")`))
	if got := lvl.Emitters[0].Attach; got != (world.Attach{Frame: "guard"}) {
		t.Errorf("bound to %+v, want the frame guard", got)
	}
}

// TestAttachToAFrameThatIsNotThere points at the entity of the same name, which
// is the mistake actually being made: a vehicle with nothing parented to it has
// no frame yet, and riding the entity is what the author wanted.
func TestAttachToAFrameThatIsNotThere(t *testing.T) {
	lvl := attachHostLevel(t)
	got := warningsFrom(t, lvl, "", `    attach(@smoke, "guard")`)
	wantOneWarning(t, got, "no frame called \"guard\"", "attach(@smoke, @guard)")
	if !lvl.Emitters[0].Attach.IsZero() {
		t.Error("a binding to a frame that does not exist was made anyway")
	}
}

// TestAttachRefusesAnEntity — an entity boards a vehicle with mount, which has
// answers for collision and seats that this has no business duplicating.
func TestAttachRefusesAnEntity(t *testing.T) {
	lvl := attachHostLevel(t)
	err := runScriptErr(t, lvl, "", script(`    attach(@crate, @guard)`))
	if !strings.Contains(err.Error(), "mount") {
		t.Errorf("error %q does not point at mount", err)
	}
}

// ---------- coming loose ----------

// TestDetachLeavesItWhereItIs — the other half of the asymmetry. The local
// offset is meaningless once the binding is gone, so it is replaced by the world
// position it currently resolves to.
func TestDetachLeavesItWhereItIs(t *testing.T) {
	lvl := attachHostLevel(t)
	lvl.Emitters[0].Attach = world.Attach{Entity: "guard"}
	lvl.Emitters[0].Position = mgl32.Vec3{0, 3, 0}
	want := lvl.Emitters[0].WorldPosition(lvl)

	runScript(t, lvl, "", script(`    detach(@smoke)`))

	if !lvl.Emitters[0].Attach.IsZero() {
		t.Error("detach left the binding in place")
	}
	if got := lvl.Emitters[0].Position; got != want {
		t.Errorf("detached emitter is at %v, want %v — the world position it was at while attached", got, want)
	}
	if want == (mgl32.Vec3{0, 3, 0}) {
		t.Fatal("the test proves nothing: the carrier is at the origin, so local and world agree")
	}
}

func TestDetachOnSomethingLooseIsFalse(t *testing.T) {
	lvl := attachHostLevel(t)
	got := runScript(t, lvl, "", script(`    return detach(@torch)`))
	if b, ok := got.(interface{ String() string }); ok && b.String() != "false" {
		t.Errorf("detach on an unattached sprite returned %v, want false", got)
	}
	if got := lvl.Sprites[0].Position; got != (mgl32.Vec3{0, 2, 0}) {
		t.Errorf("detach moved a sprite that was never attached, to %v", got)
	}
}

// ---------- reading the binding back ----------

func TestAttachPropertiesReportTheBinding(t *testing.T) {
	lvl := attachHostLevel(t)
	lvl.Sprites[0].Attach = world.Attach{Entity: "guard", Model: "body", Bone: "hand_r"}

	got := runScript(t, lvl, "", script(`    return @torch.prop.attach`))
	if got.String() != "guard.body.hand_r" {
		t.Errorf("prop.attach is %q, want the binding as the editor names it", got.String())
	}

	// carrier is a ref, so it compares against an address — which is the line a
	// script wants to write, and the reason it is not a string.
	if v := runScript(t, lvl, "", script(`    return @torch.prop.carrier == @guard`)); v.String() != "true" {
		t.Errorf("prop.carrier did not compare equal to @guard: %v", v)
	}

	// And null, not a dangling ref, when it rides nothing.
	if v := runScript(t, lvl, "", script(`    return @smoke.prop.carrier`)); v.String() != "null" {
		t.Errorf("an unattached emitter's carrier is %v, want null", v)
	}
}

// ---------- before it runs ----------

func TestLintFlagsABoneThatIsNotThere(t *testing.T) {
	lvl := attachHostLevel(t)
	got := lintSource(t, lvl, script(`    attach(@torch, @guard, "hand_l")`))
	wantOneIssue(t, got, "attach would bind to nothing", `no bone called "hand_l"`)
}

func TestLintFlagsAttachingSomethingThatCannotRide(t *testing.T) {
	lvl := attachHostLevel(t)
	got := lintSource(t, lvl, script(`    attach(@crate, @guard)`))
	wantOneIssue(t, got, "an entity cannot be attached", "mount")
}

func TestLintFlagsRidingSomethingThatIsNotAnEntity(t *testing.T) {
	lvl := attachHostLevel(t)
	got := lintSource(t, lvl, script(`    attach(@torch, @trigger)`))
	wantOneIssue(t, got, "a volume cannot be ridden", "or a frame as a string")
}
