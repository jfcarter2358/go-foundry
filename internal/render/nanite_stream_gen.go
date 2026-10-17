package render

// nanite_stream_gen.go — what a streamed mesh draws, as a number the
// virtual shadow map's tracker can hash (docs/regions/PRESETS_4.md, bug 2a).
//
// The virtual shadow map keeps a page's static layer until the tracker says
// something that casts into it changed (vsm_track.go). For a cooked
// placement it used to hash the matrix and the mesh pointer — which say
// nothing about the mesh's RESIDENCY. With the page pool over its budget
// (nanite_stream.go), a mesh draws whatever its resident pages allow: at
// load only the pages the pool had room for, the coarse root clusters of the
// rest, and finer pages as the feedback asks for them. A static shadow page
// drawn in that first stretch kept the coarse caster for good: a terrain
// tile's root clusters stand metres above the ground they simplify, and the
// fine ground under them read them as an occluder — big polygonal "fan"
// shadows lying on open ground with nothing above them, that only a
// FOUNDRY_NANITE_STREAM=0 run, a bigger pool or a level with fewer meshes
// (the biome showcase without half its desert wall sheets) did not show.
//
// So every streamed mesh counts the times how much of it is resident moved
// to another BAND (streamMesh.gen, bumped by noteDrawGen as flushRecords
// rewrites its records): its root pages alone, or a share of the rest in
// eighths. The tracker folds the count into each placement's signature, and
// a change redraws the pages under the placement once the new records are in
// — the next frame's shadow pass, as the records go up before the first cull
// of the frame they change in.
//
// Settled bands rather than every change: a pool over its budget THRASHES —
// the showcase's took in and threw out ~16 pages a frame with the camera
// still — and redrawing a static page means culling every placement over it
// again (~6 ms a rectangle on the GTX 1070 with 14,000 scans). Hashing every
// change put the sun's shadow from 0.7 to 38 ms of GPU in the pine forest,
// and bands alone (a small mesh crosses one with each page) to 32. So a
// mesh's count moves only when its band has HELD for streamGenSettle frames:
// a terrain tile that comes in from its roots and stays in counts once and
// redraws its shadow once; a mesh the pool keeps trading pages of never
// settles, and keeps the shadow it was last drawn with — one of the states
// it flickers between, a level of its DAG off, not the roots of a tile.

import "foundry/internal/nanite"

// meshGen is how many times mesh m's residency settled in another band (0:
// not in this pool, or never) — counting, as it is asked, a band that has
// held for streamGenSettle frames.
func (s *naniteStreamer) meshGen(m *nanite.Mesh) uint64 {
	if s == nil || m == nil {
		return 0
	}
	if s.genIndex == nil || len(s.genIndex) != len(s.meshes) {
		s.genIndex = make(map[*nanite.Mesh]*streamMesh, len(s.meshes))
		for _, sm := range s.meshes {
			s.genIndex[sm.m] = sm
		}
	}
	sm := s.genIndex[m]
	if sm == nil {
		return 0
	}
	if sm.band != sm.noted && s.frame-sm.bandSince >= streamGenSettle {
		sm.noted = sm.band
		sm.gen++
	}
	return sm.gen
}

// streamGenBands is how many bands the share of a mesh's non-root pages
// resident is cut into (band 0 is the roots alone).
const streamGenBands = 8

// streamGenSettle is how many streamer frames a band must hold before it
// counts (meshGen).
const streamGenSettle = 30

// noteDrawGen records the band of the share of sm's pages resident, and the
// frame it moved to it in (flushRecords, whenever sm's records change).
func (s *naniteStreamer) noteDrawGen(sm *streamMesh) {
	band := residencyBand(sm.res.Resident, s.pages[sm.pageBase:int(sm.pageBase)+len(sm.res.Resident)])
	if band != sm.band {
		sm.band, sm.bandSince = band, s.frame
	}
}

// residencyBand is which band a mesh's residency is in: 0 for its root
// pages alone (or less), else 1 + the share of its other pages resident in
// streamGenBands steps (the last band is all of them).
func residencyBand(resident []bool, pages []streamPage) int {
	n, of := 0, 0
	for i, r := range resident {
		if i < len(pages) && pages[i].root {
			continue
		}
		of++
		if r {
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return 1 + n*streamGenBands/(of+1)
}

// naniteMeshDrawGen is meshGen in the level's scenery pool: 0 when nothing
// streams.
func naniteMeshDrawGen(m *nanite.Mesh) uint64 {
	return naniteScenery.stream.meshGen(m)
}
