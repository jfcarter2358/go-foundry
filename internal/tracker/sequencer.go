package tracker

// sequencer.go — rows, ticks and effect commands.
//
// Tick 0 of a row reads its cells: notes trigger, instruments change, and the
// effects that act once (pan, offset, jumps, markers) fire. Ticks 1.. run the
// continuous effects (slides, vibrato, retrigger). After the last tick the
// row advances, and that is where pattern breaks, loops, section ends and
// quantised section requests are all resolved.

import "math"

// chanState is one channel's running note and effect state.
type chanState struct {
	instr uint8
	inst  *Instrument
	note  uint8
	vol   float32 // 0..1
	pan   float32 // -1..1, from P and Y
	pitch float32 // semitones of accumulated slide
	fine  float32 // cents, from J

	glideTarget float32 // semitone offset the glide is heading for
	glideSpeed  float32
	gliding     bool

	vibPos, vibSpeed, vibDepth    float32
	vibOn                         bool
	tremPos, tremSpeed, tremDepth float32
	tremOn                        bool
	arp                           uint8
	arpOn                         bool

	volSlide, pitchSlide, panSlide, cutSlide float32

	cutoff, reso float32 // 0..1; cutoff 1 is open
	sendA, sendB float32
	sendSet      bool

	retrig, cutAt, delayAt int
	delayed                Cell
	offset                 float32
	reverse                bool
	art                    int // 1-based articulation from @; 0 the default

	voice NoteID // the note the channel is steering, 0 none

	// effect memory: a zero parameter repeats the last
	memVol, memPitch, memGlide, memVib, memTrem uint8
}

func newChanState() chanState {
	return chanState{vol: 1, cutoff: 1, cutAt: -1, delayAt: -1}
}

func (p *Player) doTick() {
	if p.tick == 0 {
		p.processRow()
	} else {
		p.tickEffects()
	}
	p.tick++
	if p.tick >= p.song.TicksPerRow {
		p.tick = 0
		p.advanceRow()
	}
}

func (p *Player) processRow() {
	pat := p.pattern()
	if p.row >= pat.Rows() {
		p.row = 0
	}
	cells := pat.Cells[p.row]
	for ci := range p.chans {
		if ci >= len(cells) {
			break
		}
		cell := cells[ci]
		ch := &p.chans[ci]
		ch.arpOn, ch.vibOn, ch.tremOn, ch.gliding = false, false, false, false
		ch.volSlide, ch.pitchSlide, ch.panSlide, ch.cutSlide = 0, 0, 0, 0
		ch.retrig, ch.cutAt, ch.delayAt = 0, -1, -1
		ch.offset = 0

		glide := false
		for f := 0; f < MaxFXColumns; f++ {
			e := cell.FX[f]
			if e.Cmd == FxNoteDelay && e.Param > 0 && cell.Note != NoteNone {
				ch.delayAt = int(e.Param)
				ch.delayed = cell
			}
			if e.Cmd == FxGlide {
				glide = true
			}
		}
		for f := 0; f < MaxFXColumns; f++ {
			p.rowEffect(ci, ch, cell.FX[f])
		}
		if ch.delayAt > 0 {
			continue
		}
		p.playCell(ci, ch, cell, glide)
	}
}

// playCell applies a cell's instrument, note and volume.
func (p *Player) playCell(ci int, ch *chanState, cell Cell, glide bool) {
	if cell.Instr != 0 {
		if in := p.song.Instrument(cell.Instr); in != nil {
			if cell.Instr != ch.instr {
				ch.art = 0 // an articulation belongs to the instrument it was set on
			}
			ch.instr, ch.inst = cell.Instr, in
			if cell.Note == NoteNone {
				ch.vol = 1
			}
		}
	}
	for _, e := range cell.FX {
		if e.Cmd == FxArticulation {
			ch.art = int(e.Param)
		}
	}
	switch {
	case cell.Note == NoteOff:
		p.releaseChannel(ci)
	case cell.Note == NoteCut:
		p.killChannel(ci)
	case cell.Note >= NoteMin && cell.Note <= NoteMax:
		if v := p.eng.attachedLead(ch.voice); glide && v != nil && !v.released {
			// Keep the sounding note and slide its pitch to the new one. The
			// target is relative to the voice's own note, as ch.pitch is.
			ch.glideTarget = float32(cell.Note) - v.note
			ch.note = cell.Note
		} else {
			if cell.Vol == VolNone {
				ch.vol = 1
			}
			// The volume column is the note's velocity too: it picks the
			// instrument's velocity layer as well as setting the level.
			vel := ch.vol
			if cell.Vol != VolNone {
				vel = float32(cell.Vol) / float32(VolMax)
			}
			p.trigger(ci, ch, cell.Note, vel)
		}
	}
	if cell.Vol != VolNone {
		ch.vol = float32(cell.Vol) / float32(VolMax)
	}
}

func (p *Player) rowEffect(ci int, ch *chanState, e Effect) {
	x, y := float32(e.Param>>4), float32(e.Param&15)
	mem := func(m *uint8) uint8 {
		if e.Param == 0 {
			return *m
		}
		*m = e.Param
		return e.Param
	}
	switch e.Cmd {
	case FxArpeggio:
		ch.arp, ch.arpOn = e.Param, e.Param != 0
	case FxPitchUp:
		ch.pitchSlide = float32(mem(&ch.memPitch)) / 16
	case FxPitchDown:
		ch.pitchSlide = -float32(mem(&ch.memPitch)) / 16
	case FxGlide:
		ch.glideSpeed = float32(mem(&ch.memGlide)) / 16
		ch.gliding = true
	case FxVibrato:
		v := mem(&ch.memVib)
		ch.vibSpeed, ch.vibDepth, ch.vibOn = float32(v>>4), float32(v&15), true
	case FxTremolo:
		v := mem(&ch.memTrem)
		ch.tremSpeed, ch.tremDepth, ch.tremOn = float32(v>>4), float32(v&15), true
	case FxVolSlide:
		v := mem(&ch.memVol)
		ch.volSlide = (float32(v>>4) - float32(v&15)) / 64
	case FxPan:
		ch.pan = clamp32((float32(e.Param)-128)/127, -1, 1)
	case FxPanSlide:
		ch.panSlide = (x - y) / 64
	case FxRetrigger:
		ch.retrig = int(e.Param)
	case FxOffset:
		ch.offset = float32(e.Param) / 256
	case FxNoteCut:
		ch.cutAt = int(e.Param)
		if e.Param == 0 {
			p.killChannel(ci)
		}
	case FxCutoff:
		ch.cutoff = float32(e.Param) / 255
	case FxResonance:
		ch.reso = float32(e.Param) / 255
	case FxCutoffSlide:
		ch.cutSlide = (x - y) / 255
	case FxFineTune:
		ch.fine = float32(int(e.Param) - 128)
	case FxSendA:
		ch.sendA, ch.sendSet = float32(e.Param)/255, true
	case FxSendB:
		ch.sendB, ch.sendSet = float32(e.Param)/255, true
	case FxJump:
		p.jumpOrder = int(e.Param)
	case FxBreak:
		p.breakRow = int(e.Param)
	case FxLoop:
		if e.Param == 0 {
			p.loopStart = p.row
		} else if p.loopLeft == 0 {
			p.loopLeft = int(e.Param)
			p.loopJump = true
		} else {
			p.loopLeft--
			if p.loopLeft > 0 {
				p.loopJump = true
			}
		}
	case FxTempo:
		if e.Param >= 20 {
			p.bpm, p.bpmTarget, p.bpmRate = float32(e.Param), float32(e.Param), 0
			p.eng.SetTempo(p.bpm)
		}
	case FxParam:
		if i := int(x); i < len(p.params) {
			v := y / 15
			p.params[i] = paramState{cur: v, target: v}
		}
	case FxMarker:
		name := ""
		if p.song.Markers != nil {
			name = p.song.Markers[e.Param]
		}
		p.post(Event{Kind: EventMarker, Name: name, Value: int(e.Param), Order: p.order, Row: p.row, Section: p.sectionName()})
	case FxReverse:
		ch.reverse = e.Param != 0
	}
}

func (p *Player) tickEffects() {
	for ci := range p.chans {
		ch := &p.chans[ci]
		if ch.volSlide != 0 {
			ch.vol = clamp32(ch.vol+ch.volSlide, 0, 1)
		}
		if ch.pitchSlide != 0 {
			ch.pitch += ch.pitchSlide
		}
		if ch.gliding && ch.glideSpeed > 0 {
			if ch.pitch < ch.glideTarget {
				ch.pitch = min(ch.pitch+ch.glideSpeed, ch.glideTarget)
			} else if ch.pitch > ch.glideTarget {
				ch.pitch = max(ch.pitch-ch.glideSpeed, ch.glideTarget)
			}
		}
		if ch.vibOn {
			ch.vibPos += ch.vibSpeed
		}
		if ch.tremOn {
			ch.tremPos += ch.tremSpeed
		}
		if ch.panSlide != 0 {
			ch.pan = clamp32(ch.pan+ch.panSlide, -1, 1)
		}
		if ch.cutSlide != 0 {
			ch.cutoff = clamp32(ch.cutoff+ch.cutSlide, 0, 1)
		}
		if ch.retrig > 0 && p.tick%ch.retrig == 0 && ch.note != NoteNone {
			p.trigger(ci, ch, ch.note, ch.vol)
		}
		if ch.cutAt > 0 && p.tick == ch.cutAt {
			p.killChannel(ci)
		}
		if ch.delayAt > 0 && p.tick == ch.delayAt {
			glide := false
			for _, e := range ch.delayed.FX {
				if e.Cmd == FxGlide {
					glide = true
				}
			}
			p.playCell(ci, ch, ch.delayed, glide)
			ch.delayAt = -1
		}
	}
}

// pitchMod is a channel's momentary pitch offset in semitones: slide,
// arpeggio step, vibrato and fine tune.
func (p *Player) pitchMod(ch *chanState) float32 {
	off := ch.pitch + ch.fine/100
	if ch.arpOn {
		switch p.tick % 3 {
		case 1:
			off += float32(ch.arp >> 4)
		case 2:
			off += float32(ch.arp & 15)
		}
	}
	if ch.vibOn {
		off += float32(math.Sin(float64(ch.vibPos)*2*math.Pi/64)) * ch.vibDepth / 8
	}
	return off
}

// volMod is a channel's momentary level: volume times tremolo.
func (p *Player) volMod(ch *chanState) float32 {
	v := ch.vol
	if ch.tremOn {
		v *= 1 - ch.tremDepth/15*0.5*(1-float32(math.Sin(float64(ch.tremPos)*2*math.Pi/64)))
	}
	return v
}

func (p *Player) sectionName() string {
	if p.section >= 0 && p.section < len(p.song.Sections) {
		return p.song.Sections[p.section].Name
	}
	return ""
}

// advanceRow moves to the next row, resolving every kind of jump.
func (p *Player) advanceRow() {
	s := p.song
	if p.loopJump {
		p.loopJump = false
		p.row = p.loopStart
		p.publish()
		return
	}
	newOrder, newRow := p.order, p.row+1
	crossed := false
	if p.jumpOrder >= 0 || p.breakRow >= 0 {
		newOrder = p.order + 1
		if p.jumpOrder >= 0 {
			newOrder = p.jumpOrder
		}
		newRow = max(p.breakRow, 0)
		crossed = true
		p.jumpOrder, p.breakRow = -1, -1
	} else if newRow >= p.pattern().Rows() {
		newOrder, newRow, crossed = p.order+1, 0, true
	}

	sectionEnded := false
	if crossed {
		p.loopStart, p.loopLeft = 0, 0
		if p.section >= 0 && p.section < len(s.Sections) {
			sec := s.Sections[p.section]
			if newOrder >= sec.End || newOrder < sec.Start {
				sectionEnded = true
				if p.pendingSection >= 0 && p.pendingQuant != QuantNow {
					// Any boundary request is satisfied by a section end.
					p.gotoSection(p.pendingSection)
					return
				}
				switch {
				case sec.Loop:
					newOrder = sec.Start
				case sec.Next != "":
					if i := s.SectionIndex(sec.Next); i >= 0 {
						p.gotoSection(i)
						return
					}
				}
			}
		}
		if newOrder >= len(s.Order) || newOrder < 0 {
			if !s.Loop {
				p.finish()
				return
			}
			newOrder = 0
		}
		if newRow >= s.Patterns[s.Order[newOrder]].Rows() {
			newRow = 0
		}
	}

	if p.pendingSection >= 0 {
		lpb := s.LPB
		ok := false
		switch p.pendingQuant {
		case QuantBeat:
			ok = newRow%lpb == 0
		case QuantBar:
			ok = newRow%(lpb*s.BeatsPerBar) == 0
		case QuantPattern:
			ok = crossed
		case QuantSection:
			ok = sectionEnded || (p.section < 0 && crossed)
		}
		if ok {
			p.gotoSection(p.pendingSection)
			return
		}
	}
	for i := 0; i < len(p.pending); i++ {
		r := p.pending[i]
		ok := false
		switch r.q {
		case QuantBeat:
			ok = newRow%s.LPB == 0
		case QuantBar:
			ok = newRow%(s.LPB*s.BeatsPerBar) == 0
		default:
			ok = crossed
		}
		if ok {
			p.addStinger(r.p)
			p.pending = append(p.pending[:i], p.pending[i+1:]...)
			i--
		}
	}

	p.order, p.row = newOrder, newRow
	if crossed {
		if sec := p.sectionAt(p.order); sec != p.section {
			p.section = sec
			if sec >= 0 {
				p.post(Event{Kind: EventSection, Name: s.Sections[sec].Name, Section: s.Sections[sec].Name, Order: p.order})
			}
		}
	}
	if s.BeatEvents && p.row%s.LPB == 0 {
		k := EventBeat
		if p.row%(s.LPB*s.BeatsPerBar) == 0 {
			k = EventBar
		}
		p.post(Event{Kind: k, Order: p.order, Row: p.row, Section: p.sectionName()})
	}
	p.publish()
}

func (p *Player) finish() {
	p.playing = false
	p.finished = true
	p.tailFrames = p.rate * 3 // let reverb and delay ring out
	for i := range p.chans {
		p.releaseChannel(i)
	}
	p.post(Event{Kind: EventEnd, Order: p.order, Row: p.row})
	p.publish()
}

// ---------- notes ----------

// trigger starts a note on a channel, moving its old note off to fade. vel
// (0..1) picks the instrument's velocity layer.
func (p *Player) trigger(ci int, ch *chanState, note uint8, vel float32) {
	in := ch.inst
	if in == nil {
		return
	}
	e := p.eng
	if old := ch.voice; e.attached(old) {
		e.detach(old)
		if in.Polyphony > 1 && e.stripNotes(ci) < in.Polyphony {
			e.NoteOff(old)
		} else {
			e.Kill(old)
		}
	}
	ch.note = note
	ch.pitch, ch.vibPos, ch.tremPos = 0, 0, 0
	if !ch.sendSet && ci < len(p.song.Channels) {
		ch.sendA, ch.sendB = p.song.Channels[ci].SendA, p.song.Channels[ci].SendB
	}
	ch.voice = e.NoteOn(ci, in, float32(note), NoteOptions{
		Velocity: vel, Articulation: ch.art, Offset: ch.offset, Reverse: ch.reverse, Follow: true,
	})
}

func (p *Player) releaseChannel(ci int) {
	p.eng.releaseAttached(p.chans[ci].voice)
}

func (p *Player) killChannel(ci int) {
	if id := p.chans[ci].voice; p.eng.attached(id) {
		p.eng.Kill(id)
	}
}

// ---------- preview notes ----------

func (p *Player) previewNote(key int, instr, note uint8, vol, hold float32) {
	in := p.song.Instrument(instr)
	if in == nil || note < NoteMin || note > NoteMax {
		return
	}
	if key >= 0 {
		p.previewOff(key)
	}
	id := p.eng.NoteOn(PreviewStrip, in, float32(note), NoteOptions{Volume: vol, Hold: hold})
	if id == 0 || key < 0 {
		return
	}
	for k := range p.keys {
		if !p.keys[k].used {
			p.keys[k] = previewKey{key: key, voice: id, used: true}
			return
		}
	}
	// No free key slot: the note plays and ends on its own.
}

func (p *Player) previewOff(key int) {
	for k := range p.keys {
		pk := &p.keys[k]
		if pk.used && pk.key == key {
			p.eng.NoteOff(pk.voice)
			pk.used = false
		}
	}
}
