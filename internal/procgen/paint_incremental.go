package procgen

// paint_incremental.go — what lets a stroke rerun only the ground it touched
// (docs/terrain-paint/PHASE_P2.md, "Per-tile incremental reruns").
//
// A stroke changes a layer's content hash, so every stage that reads the
// layer is stale (paint_doc.go folds the hash in) and runs again. Running
// again does not have to mean redoing everything: the stages that read
// paint are Incremental kinds (incremental.go), and each keys its pieces —
// a grass field per terrain tile, a scatter tile, a material's paint-free
// weights — by exactly what the piece reads:
//
//   - the stage's BASE hash (Env.BaseHash): its hash with no paint folded
//     in anywhere, its refs' base hashes standing in for their hashes, so a
//     stroke leaves it alone;
//   - the paint the piece can read (Env.PaintKey): for each layer its own
//     params name, the stored tiles within one texel of the piece's
//     rectangle; for each layer an UPSTREAM stage reads (a grass field
//     avoiding a scatter that reads the layer), the tiles within
//     UpstreamPaintReach more, since the upstream stage's own pieces read
//     their neighbourhood (a scatter tile depends on its neighbours');
//   - for a RectKeyed stage above (StageKind.RectKeyed: a ground_edit,
//     whose heights follow a pond's whole outline), the stage's own key of
//     what it holds over the piece's rectangle (RectKeyer) — and then not
//     the layers only it reads, which that key follows exactly
//     (phase W2, docs/terrain-paint/PHASE_W2.md).
//
// A piece whose key matches last run's and whose files are still there is
// reused untouched (RunContext.ReuseTile). The result is the same as a run
// from nothing by construction: a piece is a pure function of what its key
// covers, and of how the upstream outputs it read came to be this run
// (upstreamLineage: a fresh terrain and one read back from its files differ
// by quantisation). Tests pin it per stage (stroke + incremental == full
// run).
//
// Planning: planPaint records each planned stage's base hash and the layers
// it and its upstream stages read; Run hands them to the stage's Env.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// UpstreamPaintReach is how far past a piece's rectangle a layer only an
// upstream stage reads is hashed (metres): three 64 m paint tiles, the
// reach of a tiled scatter's neighbour chain (place/scatter_tiled.go) plus
// a footprint.
const UpstreamPaintReach = 192.0

// planPaint sets ps's base hash (bup: its refs' base hashes and extras, in
// the order the full hash takes them) and the layers it and its refs read.
func planPaint(ps *plannedStage, k *StageKind, p Params, bup []string, byID map[string]*plannedStage) {
	ps.base = StageBaseHash(k, ps.seed, p, bup)
	ps.paintOwn = MaskRefs(k.Params, p)
	// The RectCovered params left out of the base (a ground_edit's
	// rivers): its own, and every ancestor's.
	ps.covered = CoveredHash(k, p)
	var cov []string
	for _, r := range ps.refs {
		if u := byID[r]; u != nil {
			if u.covered != "" {
				cov = append(cov, r+"="+u.covered)
			}
			if u.coveredUp != "" {
				cov = append(cov, r+"^"+u.coveredUp)
			}
		}
	}
	if len(cov) > 0 {
		sum := sha256.Sum256([]byte(strings.Join(cov, ";")))
		ps.coveredUp = hex.EncodeToString(sum[:12])
	}
	seen := map[string]bool{}
	// covered: arriving through a RectKeyed ref (its output's rect key
	// follows them exactly); direct: through any other.
	covered, direct := map[string]bool{}, map[string]bool{}
	for _, r := range ps.refs {
		u := byID[r]
		if u == nil {
			continue
		}
		for _, l := range [][]string{u.paintOwn, u.paintUp} {
			for _, n := range l {
				seen[n] = true
				if u.kind != nil && u.kind.RectKeyed {
					covered[n] = true
				} else {
					direct[n] = true
				}
			}
		}
	}
	for n := range seen {
		ps.paintUp = append(ps.paintUp, n)
		if covered[n] && !direct[n] {
			ps.paintCovered = append(ps.paintCovered, n)
		}
	}
	sort.Strings(ps.paintUp)
	sort.Strings(ps.paintCovered)
}

// ReadsPaint reports whether the stage or anything above it reads a paint
// layer.
func (e *Env) ReadsPaint() bool { return len(e.paintOwn)+len(e.paintUp) > 0 }

// ReadsOwnPaint reports whether the stage's own params name a paint layer.
func (e *Env) ReadsOwnPaint() bool { return len(e.paintOwn) > 0 }

// RectKeyer is an artifact that keys what it holds in a world rectangle
// (min x, min z, max x, max z): an edited terrain's heights there
// (terrain.Terrain.RectKey). "" when it has nothing to say.
type RectKeyer interface {
	RectKey(minX, minZ, maxX, maxZ float64) string
}

// upstreamRect is one RectKeyed upstream stage a piece's key reads: its ID,
// whether it is a direct ref (else its reach widens as a layer only an
// upstream stage reads does) and its result.
type upstreamRect struct {
	id     string
	direct bool
	res    *StageResult
}

// rectKeys is the RectKeyed stages above a stage, their artifacts loaded
// on the first PaintKey that needs them.
type rectKeys struct {
	ups  []upstreamRect
	once sync.Once
	keys []RectKeyer
}

func (r *rectKeys) load() {
	r.once.Do(func() {
		r.keys = make([]RectKeyer, len(r.ups))
		for i, u := range r.ups {
			if a, err := u.res.artifact(); err == nil {
				if k, ok := a.(RectKeyer); ok {
					r.keys[i] = k
				}
			}
		}
	})
}

// PaintKey is the content key of every paint a piece covering the
// rectangle (min x, min z, max x, max z) reads, widened by margin metres
// (a footprint's reach, a blur): the stage's own layers within that, the
// layers only its upstream stages read within UpstreamPaintReach more. ""
// when nothing painted is in reach.
func (e *Env) PaintKey(rect [4]float64, margin float64) string {
	// A RectKeyed stage above keys the piece even when no paint is read
	// anywhere: its RectCovered params (a ground_edit's rivers) move the
	// ground without any layer changing (phase W3).
	if !e.ReadsPaint() && (e.rects == nil || len(e.rects.ups) == 0) {
		return ""
	}
	var parts []string
	add := func(name string, m float64) {
		l := e.Doc.PaintLayer(name)
		if h := l.RectHash(rect[0]-m, rect[1]-m, rect[2]+m, rect[3]+m); h != "" {
			parts = append(parts, name+"="+h)
		}
	}
	for _, n := range e.paintOwn {
		add(n, margin)
	}
	// Every RectKeyed stage above answering a rect key?
	keyed := false
	if r := e.rects; r != nil && len(r.ups) > 0 {
		r.load()
		keyed = true
		for _, k := range r.keys {
			keyed = keyed && k != nil
		}
	}
	for _, n := range e.paintUp {
		// A layer only a RectKeyed stage above reads is in that stage's
		// rect key below, exactly; hashing it here too, 192 m wide, would
		// dirty every piece on every stroke. Any other layer an upstream
		// stage reads is hashed that wide — even one the stage reads
		// itself too (a pond layer a ground_edit carves and the material
		// paints mud by): what the upstream stage made of it reaches
		// further than the stage's own read.
		if !(keyed && containsStr(e.paintCovered, n)) {
			add(n, margin+UpstreamPaintReach)
		}
	}
	if r := e.rects; r != nil && len(r.ups) > 0 {
		r.load()
		for i, u := range r.ups {
			if r.keys[i] == nil {
				continue
			}
			m := margin
			if !u.direct {
				m += UpstreamPaintReach
			}
			if h := r.keys[i].RectKey(rect[0]-m, rect[1]-m, rect[2]+m, rect[3]+m); h != "" {
				parts = append(parts, "@"+u.id+"="+h)
			}
		}
	}
	return strings.Join(parts, ",")
}

// upstreamRectKeyed is the RectKeyed stages among ps's ancestors (direct
// refs first marked direct), in plan order.
func upstreamRectKeyed(ps *plannedStage, byID map[string]*plannedStage, results map[string]*StageResult, order []*plannedStage) *rectKeys {
	anc := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if anc[id] {
			return
		}
		anc[id] = true
		if u := byID[id]; u != nil {
			for _, r := range u.refs {
				walk(r)
			}
		}
	}
	for _, r := range ps.refs {
		walk(r)
	}
	var out []upstreamRect
	for _, p := range order {
		id := p.stage.ID
		if !anc[id] || !p.kind.RectKeyed || results[id] == nil {
			continue
		}
		out = append(out, upstreamRect{id: id, direct: containsStr(ps.refs, id), res: results[id]})
	}
	if len(out) == 0 {
		return nil
	}
	return &rectKeys{ups: out}
}

// UpstreamPaintHash is the whole content hash of every layer an upstream
// stage reads (whether or not this stage reads it too): what a cache of the stage's paint-free work (a material's
// weights before its own paint) must also be keyed by, since an upstream
// stage's output may follow paint anywhere. "" when there is none.
func (e *Env) UpstreamPaintHash() string {
	var parts []string
	for _, n := range e.paintUp {
		// Own layers too: one an upstream stage also reads changes the
		// upstream output (a ground_edit's pond layer moves the ground the
		// material's paint-free weights are made from).
		h := "none"
		if l := e.Doc.PaintLayer(n); l != nil {
			h = l.Hash()
		}
		parts = append(parts, n+"="+h)
	}
	// The RectCovered params above (a ground_edit's rivers), which the
	// base hash leaves out (phase W3).
	if e.coveredUp != "" {
		parts = append(parts, "covered="+e.coveredUp)
	}
	return strings.Join(parts, ",")
}

// PieceKey hashes a piece's identity and inputs into a tile hash: the
// stage's base hash, its upstream lineage, a version the stage bumps when a
// piece's output changes for the same inputs, and the parts given.
func (e *Env) PieceKey(version string, parts ...string) string {
	h := sha256.New()
	fmt.Fprintf(h, "piece\x00%s\x00%s\x00%s\x00", e.BaseHash, e.lineage, version)
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// upstreamLineage is how a stage's upstream outputs came to be in this
// run, hashed: for each ref, L when it was skipped (its artifact is loaded
// back from its files) or F with its own lineage when it ran. The two are
// not the same bytes — a terrain's heights read back from its heightmap are
// quantised, a scatter's footprints rounded to the centimetre — so a piece
// made against a fresh upstream is not reused against a loaded one, and the
// first rerun after an upstream stage ran rebuilds every piece once. That
// is what makes a reused piece exactly what a run from nothing would make
// against the same upstream. A stage whose fresh product IS its loaded one
// (RunContext.ArtifactAsLoaded: a tiled scatter) counts as L either way, so
// a stroke that reruns it does not make its readers rebuild everything.
func upstreamLineage(refs []string, results map[string]*StageResult, lineage map[string]string) string {
	if len(refs) == 0 {
		return ""
	}
	h := sha256.New()
	for _, r := range refs {
		res := results[r]
		switch {
		case res == nil:
			fmt.Fprintf(h, "%s:?;", r)
		case res.Skipped || res.AsLoaded:
			fmt.Fprintf(h, "%s:L;", r)
		default:
			fmt.Fprintf(h, "%s:F(%s);", r, lineage[r])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// freshRefs is the refs that ran this run (rather than being read back).
func freshRefs(refs []string, results map[string]*StageResult) []string {
	var out []string
	for _, r := range refs {
		if res := results[r]; res != nil && !res.Skipped && !res.AsLoaded {
			out = append(out, r)
		}
	}
	return out
}

// PreviousVolumes is the volumes the stage's previous run placed, by their
// own ID: an incremental stage re-places a reused piece's volume exactly as
// it was. Empty when there was no previous run (or under Force). Read
// once; safe from Parallel workers.
func (rc *RunContext) PreviousVolumes() map[string]VolumeEntry {
	rc.loadPrevious()
	return rc.prevVolumes
}

func (rc *RunContext) loadPrevious() {
	rc.prevOnce.Do(func() {
		rc.prevVolumes = map[string]VolumeEntry{}
		if rc.Previous == nil {
			return
		}
		f, err := readPlacements(filepath.Join(rc.Dir, PlacementsFileName))
		if err != nil {
			return
		}
		for _, v := range f.Volumes {
			if id, _ := v["id"].(string); id != "" {
				rc.prevVolumes[id] = v
			}
		}
	})
}

// PlanEnv is the Env a run would hand the stage with ID id (its resolved
// params, seed, base hash and the paint it reads), without running
// anything: for tools and tests that ask which pieces a stroke dirties.
func PlanEnv(d *Doc, id string) (*Env, error) {
	ps, err := plan(d)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.stage.ID == id {
			return &Env{Doc: d, Stage: p.stage, Kind: p.kind, Params: p.params, Seed: p.seed, Region: p.region, paintCovered: p.paintCovered,
				BaseHash: p.base, paintOwn: p.paintOwn, paintUp: p.paintUp, coveredUp: p.coveredUp}, nil
		}
	}
	return nil, fmt.Errorf("no enabled stage %q", id)
}
