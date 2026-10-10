//go:build !release

package app

// The development build's half of the same rule: it opens the level you are
// editing, which is the authored file. A development build that loaded compiled
// levels would show you the last compile rather than the level, and the edit
// you just made would not be on screen.

import (
	"path/filepath"
	"testing"

	"foundry/internal/levelio"
)

func TestDevelopmentBuildLoadsAuthoredLevels(t *testing.T) {
	if levelSourceExt != levelio.LevelExt {
		t.Errorf("a development build's level source extension is %q, want %q", levelSourceExt, levelio.LevelExt)
	}
	if got := levelSourcePath("canyon"); filepath.Ext(got) != levelio.LevelExt {
		t.Errorf("levelSourcePath(\"canyon\") = %q, which is not an authored level", got)
	}
}
