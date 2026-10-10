package glctx

import (
	"testing"

	"foundry/internal/setup"
)

// Setup checks a player's driver for the version the engine asks for, and is
// built without cgo, so it keeps its own copy.
func TestSetupChecksTheVersionTheEngineNeeds(t *testing.T) {
	if setup.GLMajor != MajorVersion || setup.GLMinor != MinorVersion {
		t.Errorf("setup checks for OpenGL %d.%d, the engine needs %d.%d",
			setup.GLMajor, setup.GLMinor, MajorVersion, MinorVersion)
	}
}
