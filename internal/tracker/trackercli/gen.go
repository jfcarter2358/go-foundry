package trackercli

// gen.go — installs the sample synthesizer as `foundry tracker gen-samples`.

import "foundry/internal/tracker/synth"

func init() { GenSamples = synth.Main }
