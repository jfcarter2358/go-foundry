package render

// grass_field_reload.go — forgetting grass fields whose files were rewritten
// while their level stays open (docs/terrain-paint/PHASE_P2.md, "Live
// update").
//
// A field is read once, when the camera first comes within reach of its
// tile, and kept resident by its path (grass_field.go). A live paint stroke
// in the editor reruns the generator's grass_field stage for the tiles it
// touched and rewrites those tiles' .fgrass files under the SAME paths, so
// the resident copies would go on drawing the old clumps. ReloadGrassFields
// releases them: the next frame's sync finds the volumes in reach with no
// field loaded and reads the new files, within the usual per-frame load
// budget (lifted here, so a stroke's tiles all come back in one frame).
//
// Call it on the GL context's thread (the editor runs its live splice
// through uiwin.RequestWindowOpen for that reason).

// ReloadGrassFields releases the resident fields read from these paths
// (resources-relative, as a volume's grass.file names them); they are read
// again when next in reach. Kits are left alone: a stroke rewrites a kit
// with the same bytes, and resident fields hold their kit.
func ReloadGrassFields(paths []string) {
	r := &grassFields
	if len(paths) == 0 || r.fields == nil {
		return
	}
	for _, p := range paths {
		if f := r.fields[p]; f != nil {
			f.release()
			delete(r.fields, p)
		}
	}
	// The next sync loads everything in reach at once.
	r.lvl = nil
}
