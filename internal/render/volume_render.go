package render

// volume_render.go — drawing a volume in the editor, and only in the editor.
//
// A volume is a region, not a thing: during play it is invisible, and the only
// evidence it exists is that a script fired. That makes it exactly the kind of
// object an author cannot place accurately without seeing it, so the editor
// draws each one as a wireframe cage the same way the spawn point draws an
// arrow — an authoring aid that must never appear in the shot the player sees.
//
// The cage is a line list rather than a translucent solid. A solid would sort
// into the transparent pass and fight with the water and the glass already
// there, and stacking two overlapping triggers would produce a muddle no one
// could read. Lines stay legible however many overlap.

import (
	"foundry/internal/world"
)

// RenderVolumes draws every volume's cage. Editor-only: the caller is
// responsible for not calling it during play.
//
// Depth testing is left on so a trigger behind a wall stays behind it — an
// author needs to see which room a volume is actually in.
func RenderVolumes(lvl *world.Level, cull *Frustum) {
	if lvl == nil || len(lvl.Volumes) == 0 {
		return
	}
	for vi := range lvl.Volumes {
		v := &lvl.Volumes[vi]
		if len(v.Geometry.Vertices) == 0 {
			continue
		}
		if cull != nil {
			if lo, hi, ok := v.WorldAABB(); ok && !AABBInFrustum(*cull, lo, hi) {
				continue
			}
		}
		v.Geometry.DrawLinesWithTransform(v.ModelMatrix())
	}
}

// LoadVolumes uploads every volume's cage, for use after a level load or an
// undo. Errors are not fatal: a marker that failed to build is a missing
// authoring aid, not a broken level.
func LoadVolumes(lvl *world.Level) {
	if lvl == nil {
		return
	}
	for i := range lvl.Volumes {
		_ = lvl.Volumes[i].Load()
	}
}

// DeleteVolumes releases every volume's GPU handles, so replacing a level does
// not leak the previous one's markers.
func DeleteVolumes(lvl *world.Level) {
	if lvl == nil {
		return
	}
	for i := range lvl.Volumes {
		lvl.Volumes[i].Geometry.Delete()
	}
}
