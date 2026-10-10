package levelio

import (
	"fmt"
	"testing"
)

// A release build reads the stamp out of a game binary to tell whether it can
// load what this compiler writes; a stamp left behind on a bump would wave a
// stale binary through.
func TestCompiledVersionStampMatchesTheVersion(t *testing.T) {
	if want := fmt.Sprintf("FOUNDRY-FBSP-VERSION=%d;", CompiledVersion); CompiledVersionStamp != want {
		t.Errorf("CompiledVersionStamp = %q, want %q", CompiledVersionStamp, want)
	}
}
