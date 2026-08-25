package world

// vehicle_def.go — the vehicle block in YAML, on a definition and on an instance.
//
// It follows entity_def.go's conventions exactly rather than inventing its own:
// pointers where absent has to be distinguishable from zero, an instance
// overriding only what it mentions, and seats matched by name the way model
// attachments are. A level that says "a shuttle, here" should not have to
// restate the seating plan.

import "github.com/go-gl/mathgl/mgl32"

// RawVehicleSeat is the YAML form of one seat.
type RawVehicleSeat struct {
	Name       string   `yaml:"name"`
	Driver     bool     `yaml:"driver,omitempty"`
	Position   RawVec3  `yaml:"position"`
	Yaw        float32  `yaml:"yaw,omitempty"`
	YawLimit   float32  `yaml:"yaw_limit,omitempty"`
	PitchLimit float32  `yaml:"pitch_limit,omitempty"`
	Eye        *float32 `yaml:"eye,omitempty"`
	Exit       *RawVec3 `yaml:"exit,omitempty"`
}

// RawDriveSpec is the YAML form of the drive tuning. Every field is a pointer
// so an unmentioned one keeps the mode's default rather than dropping to zero —
// a vehicle whose author set only `max_speed` must not lose its ability to turn.
type RawDriveSpec struct {
	MaxSpeed *float32 `yaml:"max_speed,omitempty"`
	Accel    *float32 `yaml:"accel,omitempty"`
	Brake    *float32 `yaml:"brake,omitempty"`
	Reverse  *float32 `yaml:"reverse,omitempty"`

	TurnRate  *float32 `yaml:"turn_rate,omitempty"`
	PitchRate *float32 `yaml:"pitch_rate,omitempty"`
	RollRate  *float32 `yaml:"roll_rate,omitempty"`

	Drag        *float32 `yaml:"drag,omitempty"`
	AngularDrag *float32 `yaml:"angular_drag,omitempty"`
	Lift        *float32 `yaml:"lift,omitempty"`
	Buoyancy    *float32 `yaml:"buoyancy,omitempty"`

	GroundStick   *float32 `yaml:"ground_stick,omitempty"`
	MaxClimbAngle *float32 `yaml:"max_climb_angle,omitempty"`
}

// RawVehicle is the YAML form of a vehicle block.
type RawVehicle struct {
	Mode     string           `yaml:"mode,omitempty"` // ground (default), air, water, space
	Interior string           `yaml:"interior,omitempty"`
	Seats    []RawVehicleSeat `yaml:"seats,omitempty"`
	Drive    *RawDriveSpec    `yaml:"drive,omitempty"`
}

// vehicleSeatFromRaw converts one seat, defaulting the eye height to a seated
// one rather than to the floor.
func vehicleSeatFromRaw(r RawVehicleSeat) VehicleSeat {
	s := VehicleSeat{
		Name:       r.Name,
		Driver:     r.Driver,
		Position:   mgl32.Vec3{r.Position.X, r.Position.Y, r.Position.Z},
		Yaw:        r.Yaw,
		YawLimit:   r.YawLimit,
		PitchLimit: r.PitchLimit,
		Eye:        defaultSeatEye,
	}
	if r.Eye != nil {
		s.Eye = *r.Eye
	}
	if r.Exit != nil {
		s.Exit = mgl32.Vec3{r.Exit.X, r.Exit.Y, r.Exit.Z}
	} else {
		// No exit authored: step out beside the seat. Not correct for every
		// hull, but it is recoverable — the alternative is dropping the
		// occupant at the vehicle's origin, which is usually inside it.
		s.Exit = s.Position.Add(mgl32.Vec3{defaultSeatExitOffset, 0, 0})
	}
	return s
}

// defaultSeatEye is how far above a seat the view sits: a seated head rather
// than a standing one.
const defaultSeatEye = float32(0.7)

// defaultSeatExitOffset is how far to the side an occupant is put down when the
// seat does not say.
const defaultSeatExitOffset = float32(1.5)

func vehicleSeatToRaw(s VehicleSeat) RawVehicleSeat {
	r := RawVehicleSeat{
		Name:       s.Name,
		Driver:     s.Driver,
		Position:   RawVec3{X: s.Position.X(), Y: s.Position.Y(), Z: s.Position.Z()},
		Yaw:        s.Yaw,
		YawLimit:   s.YawLimit,
		PitchLimit: s.PitchLimit,
	}
	if s.Eye != defaultSeatEye {
		e := s.Eye
		r.Eye = &e
	}
	if want := s.Position.Add(mgl32.Vec3{defaultSeatExitOffset, 0, 0}); s.Exit != want {
		r.Exit = &RawVec3{X: s.Exit.X(), Y: s.Exit.Y(), Z: s.Exit.Z()}
	}
	return r
}

// applyRawDrive overlays whatever a raw drive block mentions onto a spec,
// leaving everything else as it was. This is what lets a definition set the
// tuning and an instance nudge one number of it.
func applyRawDrive(d DriveSpec, r *RawDriveSpec) DriveSpec {
	if r == nil {
		return d
	}
	set := func(dst *float32, src *float32) {
		if src != nil {
			*dst = *src
		}
	}
	set(&d.MaxSpeed, r.MaxSpeed)
	set(&d.Accel, r.Accel)
	set(&d.Brake, r.Brake)
	set(&d.Reverse, r.Reverse)
	set(&d.TurnRate, r.TurnRate)
	set(&d.PitchRate, r.PitchRate)
	set(&d.RollRate, r.RollRate)
	set(&d.Drag, r.Drag)
	set(&d.AngularDrag, r.AngularDrag)
	set(&d.Lift, r.Lift)
	set(&d.Buoyancy, r.Buoyancy)
	set(&d.GroundStick, r.GroundStick)
	set(&d.MaxClimbAngle, r.MaxClimbAngle)
	return d
}

// driveToRaw writes only what differs from the mode's defaults, so a vehicle
// that never touched the tuning saves no tuning at all.
func driveToRaw(d DriveSpec, mode VehicleMode) *RawDriveSpec {
	def := DefaultDriveSpec(mode)
	r := &RawDriveSpec{}
	any := false
	diff := func(dst **float32, got, want float32) {
		if got != want {
			v := got
			*dst = &v
			any = true
		}
	}
	diff(&r.MaxSpeed, d.MaxSpeed, def.MaxSpeed)
	diff(&r.Accel, d.Accel, def.Accel)
	diff(&r.Brake, d.Brake, def.Brake)
	diff(&r.Reverse, d.Reverse, def.Reverse)
	diff(&r.TurnRate, d.TurnRate, def.TurnRate)
	diff(&r.PitchRate, d.PitchRate, def.PitchRate)
	diff(&r.RollRate, d.RollRate, def.RollRate)
	diff(&r.Drag, d.Drag, def.Drag)
	diff(&r.AngularDrag, d.AngularDrag, def.AngularDrag)
	diff(&r.Lift, d.Lift, def.Lift)
	diff(&r.Buoyancy, d.Buoyancy, def.Buoyancy)
	diff(&r.GroundStick, d.GroundStick, def.GroundStick)
	diff(&r.MaxClimbAngle, d.MaxClimbAngle, def.MaxClimbAngle)
	if !any {
		return nil
	}
	return r
}

// VehicleFromRaw builds a vehicle spec from YAML.
func VehicleFromRaw(r *RawVehicle) *VehicleSpec {
	if r == nil {
		return nil
	}
	mode := VehicleModeFromName(r.Mode)
	v := &VehicleSpec{
		Mode:     mode,
		Interior: r.Interior,
		Drive:    applyRawDrive(DefaultDriveSpec(mode), r.Drive),
	}
	for _, rs := range r.Seats {
		v.Seats = append(v.Seats, vehicleSeatFromRaw(rs))
	}
	return v
}

// VehicleToRaw is the inverse.
func VehicleToRaw(v *VehicleSpec) *RawVehicle {
	if v == nil {
		return nil
	}
	r := &RawVehicle{
		Interior: v.Interior,
		Drive:    driveToRaw(v.Drive, v.Mode),
	}
	if v.Mode != VehicleGround {
		r.Mode = VehicleModeName(v.Mode)
	}
	for _, s := range v.Seats {
		r.Seats = append(r.Seats, vehicleSeatToRaw(s))
	}
	return r
}

// MergeVehicle overlays an instance's vehicle block onto its definition's.
//
// Seats are matched by name, the way model attachments are: an instance can
// move one seat without restating the rest, and may add one of its own. Naming
// is the only stable way to address a seat, since a definition gaining a seat
// would otherwise renumber every override in every level that placed it.
func MergeVehicle(base *VehicleSpec, over *RawVehicle) *VehicleSpec {
	if over == nil {
		return base
	}
	if base == nil {
		return VehicleFromRaw(over)
	}
	out := *base
	out.Seats = append([]VehicleSeat(nil), base.Seats...)

	if over.Mode != "" {
		out.Mode = VehicleModeFromName(over.Mode)
	}
	if over.Interior != "" {
		out.Interior = over.Interior
	}
	out.Drive = applyRawDrive(out.Drive, over.Drive)

	for _, rs := range over.Seats {
		seat := vehicleSeatFromRaw(rs)
		replaced := false
		for i := range out.Seats {
			if out.Seats[i].Name == rs.Name {
				out.Seats[i] = seat
				replaced = true
				break
			}
		}
		if !replaced {
			out.Seats = append(out.Seats, seat)
		}
	}
	return &out
}
