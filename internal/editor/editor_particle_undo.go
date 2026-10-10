package editor

// editor_particle_undo.go — undo/redo for the particle editor.
//
// Whole-document snapshots on the generic boundedStack, exactly as the model
// editor does it. A diff would be smaller and would not be worth it: a .fpar is
// a few hundred bytes of numbers, and the whole point of the snapshot approach
// is that a new parameter needs no undo code at all.
//
// The one thing that has to be right is the clone being deep — Doc.Clone copies
// every ramp and burst slice, or a snapshot would share them with the live
// document and record nothing. particle.TestCloneIsDeep asserts that.

import "foundry/internal/particle"

// particleSnapshot is a restorable editor state: the document, and which layer
// was selected. Playback state is deliberately not in it — undoing a parameter
// change should not also rewind the preview.
type particleSnapshot struct {
	Doc   *particle.Doc
	Layer int
}

var (
	particleUndoStack boundedStack[particleSnapshot]
	particleRedoStack boundedStack[particleSnapshot]
)

// ParticlePushUndo records the current document before a change. Call it
// immediately BEFORE the mutation, the same contract EditorPushUndo has.
func ParticlePushUndo() {
	if particleDoc == nil {
		return
	}
	particleUndoStack.push(particleSnapshot{Doc: particleDoc.Clone(), Layer: particleActiveLayer})
	particleRedoStack = boundedStack[particleSnapshot]{}
}

// ParticlePopUndo discards the entry just pushed, for an operation that turned
// out to be a no-op.
func ParticlePopUndo() { particleUndoStack.pop() }

// ParticleClearUndoHistory drops both stacks — what opening a different
// document does, since a snapshot of the old one would restore the wrong file.
func ParticleClearUndoHistory() {
	particleUndoStack = boundedStack[particleSnapshot]{}
	particleRedoStack = boundedStack[particleSnapshot]{}
}

// ParticleCanUndo reports whether there is anything to undo.
func ParticleCanUndo() bool { return particleUndoStack.len() > 0 }

// ParticleCanRedo reports whether there is anything to redo.
func ParticleCanRedo() bool { return particleRedoStack.len() > 0 }

// ParticleUndo restores the previous document.
func ParticleUndo() {
	snap, ok := particleUndoStack.pop()
	if !ok || particleDoc == nil {
		return
	}
	particleRedoStack.push(particleSnapshot{Doc: particleDoc.Clone(), Layer: particleActiveLayer})
	particleApplySnapshot(snap)
	particleStatus = "Undo"
}

// ParticleRedo puts back what an undo took away.
func ParticleRedo() {
	snap, ok := particleRedoStack.pop()
	if !ok || particleDoc == nil {
		return
	}
	particleUndoStack.push(particleSnapshot{Doc: particleDoc.Clone(), Layer: particleActiveLayer})
	particleApplySnapshot(snap)
	particleStatus = "Redo"
}

// particleApplySnapshot swaps in a snapshot and puts the editor's own indices
// somewhere valid — the restored document may have fewer layers than the one
// being replaced, and a stale index would read off the end on the next draw.
func particleApplySnapshot(snap particleSnapshot) {
	path := particleDoc.Path
	particleDoc = snap.Doc
	particleDoc.Path = path
	particleDoc.Invalidate()

	particleActiveLayer = snap.Layer
	if particleActiveLayer >= len(particleDoc.Layers) {
		particleActiveLayer = len(particleDoc.Layers) - 1
	}
	if particleActiveLayer < 0 {
		particleActiveLayer = 0
	}
	if particleSolo >= len(particleDoc.Layers) {
		particleSolo = -1
	}
	particleRebuildSystems()
}
