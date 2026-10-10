package editor

import (
	"testing"

	"foundry/internal/fileutil"
	"foundry/internal/model"
)

// An attachment names its model relative to the resources directory and the
// model editor saves to the full path; both must come out as the same key, or a
// save would reload nothing.
func TestModelReloadKeyMatchesAttachmentPath(t *testing.T) {
	prev := fileutil.ResourcesPath
	defer func() { fileutil.ResourcesPath = prev }()
	fileutil.ResourcesPath = "res"

	saved := modelReloadKey(model.ModelPath("crate"))
	if got := modelReloadKey(fileutil.ResolveAssetPath("models/crate.fmdl")); got != saved {
		t.Errorf("attachment key %q != saved key %q", got, saved)
	}
	if got := modelReloadKey(fileutil.ResolveAssetPath("models/barrel.fmdl")); got == saved {
		t.Errorf("a different model matched the saved key %q", saved)
	}
}
