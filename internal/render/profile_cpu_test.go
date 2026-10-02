package render

import "testing"

// ProfileAddCPU makes a CPU-only row under whatever section is open — the
// props' collision rows under "entity update" — and adds to it each call,
// with no GL call (it runs where no timestamp query is wanted).
func TestProfileAddCPUAddsACPUOnlyRow(t *testing.T) {
	saved := prof
	savedOn := ProfileEnabled
	t.Cleanup(func() { prof, ProfileEnabled = saved, savedOn })
	prof.sections, prof.byName, prof.stack = nil, nil, nil

	ProfileEnabled = false
	ProfileAddCPU("props: resolve", 1)
	if len(ProfileSamples()) != 0 {
		t.Fatal("a row was added with the profiler off")
	}
	ProfileEnabled = true
	ProfileAddCPU("props: resolve", 0.25)
	ProfileAddCPU("props: resolve", 0.5)
	s := ProfileSamples()
	if len(s) != 1 || s[0].Name != "props: resolve" || s[0].Depth != 0 {
		t.Fatalf("samples %+v", s)
	}
	if got := prof.sections[0].cpuFrame; got != 0.75 {
		t.Errorf("the row holds %v ms this frame, want 0.75", got)
	}
	if prof.sections[0].used != [profileRing]int{} {
		t.Error("a CPU-only row issued timestamp queries")
	}
}
