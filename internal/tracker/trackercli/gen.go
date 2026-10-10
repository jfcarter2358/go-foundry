package trackercli

// gen.go — installs the sample synthesizer as `foundry tracker gen-samples`
// and its starter instruments as `foundry tracker gen-instruments`.

import "foundry/internal/tracker/synth"

func init() {
	GenSamples = synth.Main
	GenInstruments = synth.InstrumentsMain
}
