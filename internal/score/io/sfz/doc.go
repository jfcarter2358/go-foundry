// Package sfz imports SFZ sample libraries as .finst instruments.
//
// An SFZ instrument is a text file of regions, each mapping one sample
// over a key and velocity range with its tuning, loop, envelope, round
// robin turn and keyswitch (parse.go). Build turns the regions into a
// tracker.Instrument — zones, velocity layers, round robin, articulations
// from keyswitches, an amplitude envelope — and the samples it plays, each
// cut, looped and tuned the way its regions play it (build.go); Write puts
// both into the shared library:
//
//	sounds/instruments/imported/<name>.finst
//	sounds/samples/imported/<name>/<sample>.wav (+ .fsmp)
//
// so the Score and the Tracker play an imported library like any other
// instrument. SF2 files (internal/score/io/sf2) are read as regions too and
// built the same way. What a .finst cannot hold (release triggers, CC
// modulation, effects) is listed in Built.Warnings rather than dropped
// silently.
package sfz
