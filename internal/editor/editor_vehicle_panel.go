package editor

// editor_vehicle_panel.go — the vehicle block, in the entity panel.
//
// A vehicle is an entity with one more section, which is exactly how it is
// modelled underneath. Seats get translate gizmos through the ordinary
// selection machinery rather than a bespoke one: a seat is a point in the
// vehicle's space, and the editor already knows how to drag one of those.

import (
	"fmt"

	"foundry/internal/world"

	"github.com/go-gl/mathgl/mgl32"
	imgui "github.com/inkyblackness/imgui-go/v4"
)

// editorEntityVehicleSection draws the vehicle block, and the button that adds
// one to an entity that has none.
func editorEntityVehicleSection(lvl *world.Level, e *world.Entity) {
	if e.Vehicle == nil {
		editorTextWrapped("Not a vehicle. Making one gives it seats to sit in and " +
			"a drive model, and lets a chunk be bound to it as an interior.")
		if imgui.Button("Make this a vehicle##entvehadd") {
			EditorPushUndo()
			e.Vehicle = &world.VehicleSpec{
				Mode:  world.VehicleGround,
				Drive: world.DefaultDriveSpec(world.VehicleGround),
				Seats: []world.VehicleSeat{{
					Name: "driver", Driver: true, Eye: 0.7,
					Exit: mgl32.Vec3{1.5, 0, 0},
				}},
			}
		}
		return
	}

	v := e.Vehicle

	// --- mode ---
	mode := int32(v.Mode)
	if imgui.Combo("Mode##entvehmode", &mode, world.VehicleModeNames) {
		EditorPushUndo()
		newMode := world.VehicleMode(mode)
		// Re-default the tuning when the mode changes, unless the author has
		// already moved it away from the old mode's defaults. Silently keeping a
		// boat's drag on a spacecraft is a worse surprise than re-defaulting.
		if v.Drive == world.DefaultDriveSpec(v.Mode) {
			v.Drive = world.DefaultDriveSpec(newMode)
		}
		v.Mode = newMode
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("ground: steers and stays upright.\nair: pitch and roll, with lift.\n" +
			"water: like ground, floating.\nspace: full 6DOF, no gravity or drag.")
	}

	// --- interior ---
	editorVehicleInteriorPicker(lvl, e, v)

	// --- seats ---
	imgui.Separator()
	imgui.Text("Seats")
	remove := -1
	for i := range v.Seats {
		if editorVehicleSeatRow(v, i) {
			remove = i
		}
	}
	if remove >= 0 {
		EditorPushUndo()
		v.Seats = append(v.Seats[:remove], v.Seats[remove+1:]...)
	}
	if imgui.Button("Add seat##entvehseatadd") {
		EditorPushUndo()
		v.Seats = append(v.Seats, world.VehicleSeat{
			Name: uniqueSeatName(v, "seat"), Eye: 0.7, Exit: mgl32.Vec3{1.5, 0, 0},
		})
	}
	if v.DriverSeat() == nil {
		editorTextWrapped("No seat hands over the controls, so this vehicle cannot be driven. " +
			"Tick Driver on one of them.")
	}

	// --- drive tuning ---
	imgui.Separator()
	if imgui.CollapsingHeader("Drive##entvehdrive") {
		editorVehicleDriveTuning(v)
	}
}

// editorVehicleInteriorPicker binds chunks to the vehicle as its walkable
// interior, and keeps each chunk's FrameOwner in step.
//
// A vehicle can carry more than one chunk — a floor and a roof over the same
// footprint, or a bridge and a separate cargo hold, all sharing one
// FrameOwner. The two halves of each binding have to move together: a chunk
// names its frame owner and a vehicle names its interiors, and either one
// alone is a level that loads wrong. Setting it here writes both.
func editorVehicleInteriorPicker(lvl *world.Level, e *world.Entity, v *world.VehicleSpec) {
	imgui.Text("Interior")
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Chunks whose geometry is this vehicle's deck, authored\n" +
			"around the origin and carried around the world by it. A vehicle\n" +
			"may carry more than one — a floor and a roof, say.\n" +
			"Biome paint and procedural detail do not work on one.")
	}
	imgui.Indent()
	removeID := ""
	for _, id := range v.Interiors {
		imgui.Text(id)
		imgui.SameLine()
		if imgui.Button("Remove##entvehintrm" + id) {
			removeID = id
		}
	}
	if removeID != "" {
		EditorPushUndo()
		unbindChunkFromVehicle(lvl, removeID)
	}

	label := "(add interior chunk)"
	if imgui.BeginCombo("##entvehintadd", label) {
		any := false
		for ci := range lvl.Chunks {
			ch := &lvl.Chunks[ci]
			if v.CarriesChunk(ch.ID) {
				continue
			}
			// A chunk already owned by another vehicle is not offered: two
			// vehicles cannot carry the same geometry.
			if ch.FrameOwner != "" && ch.FrameOwner != e.ID {
				continue
			}
			any = true
			if imgui.Selectable(ch.ID) {
				EditorPushUndo()
				ch.FrameOwner = e.ID
				v.AddInterior(ch.ID)
				_ = world.RebuildIndexes(lvl)
			}
		}
		if !any {
			editorTextMuted("(no unowned chunks)")
		}
		imgui.EndCombo()
	}
	imgui.Unindent()
	if v.HasInterior() {
		editorTextWrapped("Interiors are authored around the origin in the 2D map, not where " +
			"the vehicle sits. Bind a portal to `world` to walk in and out of one.")
	}
}

// unbindChunkFromVehicle releases one chunk this vehicle owns, without
// touching any of its other interior chunks.
func unbindChunkFromVehicle(lvl *world.Level, chunkID string) {
	for ci := range lvl.Chunks {
		ch := &lvl.Chunks[ci]
		if ch.ID != chunkID {
			continue
		}
		if e := world.FindEntity(lvl, ch.FrameOwner); e != nil && e.Vehicle != nil {
			e.Vehicle.RemoveInterior(chunkID)
		}
		ch.FrameOwner = ""
		ch.ClearFrameTransform()
		break
	}
	_ = world.RebuildIndexes(lvl)
}

// editorVehicleSeatRow draws one seat and reports whether it was deleted.
func editorVehicleSeatRow(v *world.VehicleSpec, i int) bool {
	s := &v.Seats[i]
	id := fmt.Sprintf("##entvehseat%d", i)

	if !imgui.CollapsingHeaderV(seatHeader(s)+id+"hdr", imgui.TreeNodeFlagsDefaultOpen) {
		return false
	}
	imgui.Indent()
	defer imgui.Unindent()

	name := s.Name
	if imgui.InputTextV("Name"+id, &name, imgui.InputTextFlagsEnterReturnsTrue, nil) && name != "" {
		EditorPushUndo()
		s.Name = name
	}
	driver := s.Driver
	if imgui.Checkbox("Driver"+id, &driver) {
		EditorPushUndo()
		if driver {
			// Only one seat drives. Making this one the driver takes it off
			// whichever seat had it, rather than leaving two and letting the
			// first in list order quietly win.
			for j := range v.Seats {
				v.Seats[j].Driver = false
			}
		}
		s.Driver = driver
	}
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Sitting here hands the movement keys to the vehicle.")
	}

	seatVec3("Position"+id, &s.Position)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Vehicle-local, and the occupant's feet rather than their eyes.")
	}
	seatVec3("Exit"+id, &s.Exit)
	if imgui.IsItemHovered() {
		imgui.SetTooltip("Vehicle-local, where they are put down on leaving.\nAuthored, because the outside of a hull is not\nsomething the engine can work out.")
	}
	seatFloat("Yaw"+id, &s.Yaw, "rest facing, radians")
	seatFloat("Eye"+id, &s.Eye, "view height above the seat")
	seatFloat("Yaw limit"+id, &s.YawLimit, "how far they may look sideways; 0 = unrestricted")
	seatFloat("Pitch limit"+id, &s.PitchLimit, "how far up and down; 0 = unrestricted")

	return imgui.Button("Delete seat" + id)
}

func seatHeader(s *world.VehicleSeat) string {
	if s.Driver {
		return s.Name + " (driver)"
	}
	return s.Name
}

// editorVehicleDriveTuning draws every field of the drive model, with a button
// to put the mode's defaults back.
func editorVehicleDriveTuning(v *world.VehicleSpec) {
	d := &v.Drive
	seatFloat("Max speed##vd1", &d.MaxSpeed, "units per second")
	seatFloat("Accel##vd2", &d.Accel, "units per second squared")
	seatFloat("Brake##vd3", &d.Brake, "how hard it stops")
	seatFloat("Reverse##vd4", &d.Reverse, "top speed backwards")
	seatFloat("Turn rate##vd5", &d.TurnRate, "radians per second")
	seatFloat("Pitch rate##vd6", &d.PitchRate, "radians per second")
	seatFloat("Roll rate##vd7", &d.RollRate, "radians per second")
	seatFloat("Drag##vd8", &d.Drag, "fraction of speed shed per second")
	seatFloat("Angular drag##vd9", &d.AngularDrag, "how quickly a turn stops")
	seatFloat("Lift##vd10", &d.Lift, "air: speed traded for altitude")
	seatFloat("Buoyancy##vd11", &d.Buoyancy, "water: how hard the surface pushes back")
	seatFloat("Ground stick##vd12", &d.GroundStick, "how firmly it is held down")
	seatFloat("Max climb##vd13", &d.MaxClimbAngle, "steepest slope it will take, radians")

	if imgui.Button("Reset to defaults##vdreset") {
		EditorPushUndo()
		v.Drive = world.DefaultDriveSpec(v.Mode)
	}
}

// uniqueSeatName mints a seat name nothing else on this vehicle uses.
func uniqueSeatName(v *world.VehicleSpec, base string) string {
	if v.Seat(base) == nil {
		return base
	}
	for i := 2; ; i++ {
		name := fmt.Sprintf("%s_%d", base, i)
		if v.Seat(name) == nil {
			return name
		}
	}
}

// seatVec3 and seatFloat are the two widgets this panel needs, kept local so the
// vehicle block reads as one thing rather than as calls into a shared widget
// file that nothing else in it uses.
func seatVec3(label string, v *mgl32.Vec3) {
	a := [3]float32{v.X(), v.Y(), v.Z()}
	if editorDragFloat3Step(label, &a, -10000, 10000, "%.3f", 0.05) {
		EditorPushUndo()
		*v = mgl32.Vec3{a[0], a[1], a[2]}
	}
}

func seatFloat(label string, f *float32, tip string) {
	v := *f
	if imgui.DragFloatV(label, &v, 0.02, -10000, 10000, "%.3f", 0) {
		EditorPushUndo()
		*f = v
	}
	if tip != "" && imgui.IsItemHovered() {
		imgui.SetTooltip(tip)
	}
}
