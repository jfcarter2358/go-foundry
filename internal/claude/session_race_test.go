package claude

import (
	"sync"
	"testing"
)

// A UI drawing from a snapshot while the reader goroutine fills in a tool's
// result: the snapshot must be safe to read without the session's lock.
//
// This caught a real race — the panel read ToolCall.Result while handle() was
// writing it, because Snapshot copied the slice but not what its entries pointed
// at. Run this package with -race.
func TestSnapshotIsSafeToReadWhileTheSessionChanges(t *testing.T) {
	s := newForTest()
	s.handle([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"Write","input":{"file_path":"/p/a.flang"}}]}}`))
	s.handle([]byte(`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Write","input":{}}}`))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			s.handle([]byte(`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_1","content":"done"}]}}`))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			snap := s.Snapshot()
			for _, e := range snap.Entries {
				if e.Tool != nil {
					_ = e.Tool.Result + e.Tool.Summary
					_ = e.Tool.Done
				}
				if e.Perm != nil {
					_ = e.Perm.Answered
				}
			}
			for _, p := range snap.Pending {
				_ = p.RequestID
			}
		}
	}()
	wg.Wait()
}
