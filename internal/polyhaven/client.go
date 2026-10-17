package polyhaven

// client.go — the Poly Haven public API client (docs/canyon/SPEC.md, phase 4;
// docs/canyon/PHASE_4.md).
//
// Poly Haven (https://polyhaven.com) publishes CC0 scans — rocks, cliffs,
// plants, ground and rock textures — and a small JSON API over them:
//
//	https://api.polyhaven.com/assets?t=models|textures[&c=<category>]
//	    {id: {name, categories, tags, polycount, dimensions (mm), authors, ...}}
//	https://api.polyhaven.com/info/<id>      one asset's record
//	https://api.polyhaven.com/files/<id>     its files: map → resolution → format →
//	                                         {url, md5, size[, include: {path: file}]}
//	https://cdn.polyhaven.com/asset_img/thumbs/<id>.png?width=256
//
// The canyon generators use downloaded scans wherever one suits (the spec's
// ASSET SOURCING rule), so this package is what every one of them goes
// through: the procgen stages, the importer (phimport) and the editor's Poly
// Haven browser. It is pure Go: no GPU, no level, no engine package below it
// (internal/layering_test.go holds it to that).
//
// # Being a good API citizen (the ToS: github.com/Poly-Haven/Public-API ToS.md)
//
//   - Every request carries a User-Agent naming the software
//     (Foundry-Engine/<version>); the ToS asks for a unique one.
//   - The catalog, info and file listings are cached on disk
//     (os.UserCacheDir()/foundry/polyhaven/api) for a TTL, 24 h by default,
//     and an Offline client answers from the cache alone, however old.
//   - Downloaded files are cached by their md5 (…/polyhaven/files/<md5>), so
//     an asset is fetched once however many projects, generators and
//     resolutions of a stage re-import it, and every file is verified against
//     the md5 the listing gives before it is used.
//   - At most four downloads run at once per client (MaxConcurrent), each
//     retried a few times with back-off and resumed from where it stopped
//     (HTTP Range on the partial file) rather than started over.
//   - Everything takes a context, so a cancelled stage run or a closed
//     browser window stops its transfers.
//
// The UI that shows this content must credit Poly Haven visibly (Credit);
// downloaded assets are CC0 and need no attribution, but each import writes
// a provenance record anyway (phimport).

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Default endpoints.
const (
	DefaultAPI = "https://api.polyhaven.com"
	DefaultCDN = "https://cdn.polyhaven.com"
)

// Credit is the line every UI surfacing Poly Haven content shows.
const Credit = "Assets from Poly Haven (polyhaven.com), CC0"

// License is what the provenance records name.
const License = "CC0 1.0 (https://polyhaven.com/license)"

// Version is the engine version the User-Agent names.
var Version = "dev"

// UserAgent is what every request sends.
func UserAgent() string { return "Foundry-Engine/" + Version + " (+procedural canyon generator)" }

// MaxConcurrent is how many downloads a client runs at once (the ToS's
// "don't hammer": 4).
const MaxConcurrent = 4

// DefaultTTL is how long a cached catalog, info or file listing is good for.
const DefaultTTL = 24 * time.Hour

// Asset types, as the API's `t` parameter names them.
const (
	TypeModels   = "models"
	TypeTextures = "textures"
	TypeHDRIs    = "hdris"
)

// Client talks to Poly Haven. The zero value is not usable: use New.
type Client struct {
	// API and CDN are the endpoints (tests point them at an httptest server).
	API, CDN string
	// HTTP is the transport.
	HTTP *http.Client
	// CacheDir is the root of the on-disk cache.
	CacheDir string
	// TTL is how long cached listings are fresh.
	TTL time.Duration
	// Offline answers from the cache only: a listing however stale, a file
	// already downloaded; anything else is ErrOffline.
	Offline bool
	// Retries is how many times a failed download is tried again.
	Retries int
	// Backoff is the first retry's wait (doubling each time).
	Backoff time.Duration

	sem chan struct{}
	// inflight de-duplicates concurrent downloads of one file (two stages
	// importing the same asset at once).
	mu       sync.Mutex
	inflight map[string]*flight
}

type flight struct {
	done chan struct{}
	err  error
}

// ErrOffline is what an Offline client answers for anything not cached.
var ErrOffline = errors.New("polyhaven: offline, and not in the cache")

// DefaultCacheDir is os.UserCacheDir()/foundry/polyhaven, or a temp dir when
// the user has none. FOUNDRY_POLYHAVEN_CACHE overrides it.
func DefaultCacheDir() string {
	if d := os.Getenv("FOUNDRY_POLYHAVEN_CACHE"); d != "" {
		return d
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "foundry", "polyhaven")
}

// New is a client on the live endpoints with the default cache. Setting
// FOUNDRY_POLYHAVEN_OFFLINE=1 makes it offline.
func New() *Client {
	c := &Client{API: DefaultAPI, CDN: DefaultCDN, CacheDir: DefaultCacheDir(), TTL: DefaultTTL,
		HTTP: &http.Client{Timeout: 10 * time.Minute}, Retries: 3, Backoff: 500 * time.Millisecond}
	c.Offline = os.Getenv("FOUNDRY_POLYHAVEN_OFFLINE") == "1"
	return c
}

// Clone is a client with c's settings and its own semaphore and state (for
// a caller that wants to change a setting, e.g. Offline, without touching
// a shared client).
func (c *Client) Clone() *Client {
	return &Client{API: c.API, CDN: c.CDN, HTTP: c.HTTP, CacheDir: c.CacheDir, TTL: c.TTL, Offline: c.Offline,
		Retries: c.Retries, Backoff: c.Backoff}
}

func (c *Client) init() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sem == nil {
		c.sem = make(chan struct{}, MaxConcurrent)
	}
	if c.inflight == nil {
		c.inflight = map[string]*flight{}
	}
	if c.HTTP == nil {
		c.HTTP = http.DefaultClient
	}
}

// ---------- the API records ----------

// Asset is one catalog entry (the fields the engine reads; the API has more).
type Asset struct {
	ID            string            `json:"-" yaml:"id"`
	Name          string            `json:"name" yaml:"name"`
	Type          int               `json:"type" yaml:"type"` // 0 hdri, 1 texture, 2 model
	Categories    []string          `json:"categories" yaml:"categories"`
	Tags          []string          `json:"tags" yaml:"tags"`
	Authors       map[string]string `json:"authors" yaml:"authors"`
	Polycount     int               `json:"polycount" yaml:"polycount,omitempty"`
	Dimensions    []float64         `json:"dimensions" yaml:"dimensions,omitempty,flow"` // millimetres
	MaxResolution []int             `json:"max_resolution" yaml:"max_resolution,omitempty,flow"`
	DatePublished int64             `json:"date_published" yaml:"date_published"`
	DownloadCount int               `json:"download_count" yaml:"download_count,omitempty"`
	Description   string            `json:"description" yaml:"description,omitempty"`
	ThumbnailURL  string            `json:"thumbnail_url" yaml:"thumbnail_url,omitempty"`
	Category      string            `json:"category" yaml:"category,omitempty"`
}

// IsModel and IsTexture say what the asset is.
func (a *Asset) IsModel() bool   { return a.Type == 2 }
func (a *Asset) IsTexture() bool { return a.Type == 1 }

// AuthorList is the authors, "name (role)", sorted.
func (a *Asset) AuthorList() []string {
	var out []string
	for n, r := range a.Authors {
		if r == "" || r == "All" {
			out = append(out, n)
		} else {
			out = append(out, n+" ("+r+")")
		}
	}
	sort.Strings(out)
	return out
}

// SizeMetres is the dimensions in metres, largest first (the API's order is
// the DCC's X, Y, Z — Blender's Z up — so the height is not always the same
// slot; callers compare sorted extents).
func (a *Asset) SizeMetres() []float64 {
	out := make([]float64, len(a.Dimensions))
	for i, d := range a.Dimensions {
		out[i] = d / 1000
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(out)))
	return out
}

// File is one downloadable file. Include lists the files a glTF needs beside
// it (its .bin and textures, by path relative to the .gltf).
type File struct {
	URL     string          `json:"url" yaml:"url"`
	MD5     string          `json:"md5" yaml:"md5"`
	Size    int64           `json:"size" yaml:"size"`
	Include map[string]File `json:"include,omitempty" yaml:"include,omitempty"`
}

// Files is an asset's file listing: map name (Diffuse, nor_gl, gltf,
// leaf_diff...) → resolution (1k, 2k...) → format (jpg, png, gltf...).
type Files map[string]map[string]map[string]File

// Get is one file, or false.
func (f Files) Get(mapName, res, format string) (File, bool) {
	r, ok := f[mapName]
	if !ok {
		return File{}, false
	}
	x, ok := r[res]
	if !ok {
		return File{}, false
	}
	file, ok := x[format]
	return file, ok && file.URL != ""
}

// Resolutions is the resolutions the map is offered at, smallest first.
func (f Files) Resolutions(mapName string) []string {
	var out []string
	for r := range f[mapName] {
		out = append(out, r)
	}
	SortResolutions(out)
	return out
}

// SortResolutions sorts "1k", "2k", "16k" by size.
func SortResolutions(rs []string) {
	sort.Slice(rs, func(i, j int) bool { return ResolutionPixels(rs[i]) < ResolutionPixels(rs[j]) })
}

// ResolutionPixels is "2k" as 2048 (0 when it is not a resolution).
func ResolutionPixels(r string) int {
	var n int
	if _, err := fmt.Sscanf(strings.ToLower(r), "%dk", &n); err != nil {
		return 0
	}
	return n * 1024
}

// parseFiles decodes a /files listing, keeping every entry that has the
// three-level shape and skipping any that does not (the API grows keys).
func parseFiles(data []byte) (Files, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := Files{}
	for k, v := range raw {
		var lvl map[string]map[string]File
		if err := json.Unmarshal(v, &lvl); err != nil {
			continue
		}
		out[k] = lvl
	}
	return out, nil
}

// ---------- listings, cached ----------

// Assets is the catalog of one type (models, textures), optionally one
// category, keyed by ID.
func (c *Client) Assets(ctx context.Context, typ, category string) (map[string]*Asset, error) {
	q := url.Values{}
	if typ != "" {
		q.Set("t", typ)
	}
	if category != "" {
		q.Set("c", category)
	}
	key := "assets_" + safeKey(typ) + "_" + safeKey(category)
	data, err := c.getJSON(ctx, c.API+"/assets?"+q.Encode(), key)
	if err != nil {
		return nil, err
	}
	var m map[string]*Asset
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("polyhaven: catalog: %w", err)
	}
	for id, a := range m {
		if a == nil {
			delete(m, id)
			continue
		}
		a.ID = id
	}
	return m, nil
}

// Info is one asset's record.
func (c *Client) Info(ctx context.Context, id string) (*Asset, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	data, err := c.getJSON(ctx, c.API+"/info/"+url.PathEscape(id), "info_"+id)
	if err != nil {
		return nil, err
	}
	var a Asset
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("polyhaven: info %s: %w", id, err)
	}
	if a.Name == "" {
		return nil, fmt.Errorf("polyhaven: no asset %q", id)
	}
	a.ID = id
	return &a, nil
}

// Files is one asset's file listing.
func (c *Client) Files(ctx context.Context, id string) (Files, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	data, err := c.getJSON(ctx, c.API+"/files/"+url.PathEscape(id), "files_"+id)
	if err != nil {
		return nil, err
	}
	f, err := parseFiles(data)
	if err != nil {
		return nil, fmt.Errorf("polyhaven: files %s: %w", id, err)
	}
	if len(f) == 0 {
		return nil, fmt.Errorf("polyhaven: asset %q has no files", id)
	}
	return f, nil
}

// ValidID reports whether id could be a Poly Haven asset ID.
func ValidID(id string) error { return checkID(id) }

// checkID refuses an ID that could not be a Poly Haven asset (it becomes a
// file and directory name).
func checkID(id string) error {
	if id == "" {
		return fmt.Errorf("polyhaven: empty asset id")
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return fmt.Errorf("polyhaven: %q is not an asset id", id)
		}
	}
	return nil
}

func safeKey(s string) string {
	if s == "" {
		return "all"
	}
	b := []byte{}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' {
			b = append(b, ch)
		} else {
			b = append(b, '_')
		}
	}
	sum := md5.Sum([]byte(s))
	return string(b) + "_" + hex.EncodeToString(sum[:3])
}

// getJSON is a listing: the cache when fresh (or offline), else the API,
// cached on success. A failed request falls back to a stale cache entry.
func (c *Client) getJSON(ctx context.Context, u, key string) ([]byte, error) {
	c.init()
	path := filepath.Join(c.CacheDir, "api", key+".json")
	st, statErr := os.Stat(path)
	if statErr == nil && (c.Offline || c.TTL <= 0 || time.Since(st.ModTime()) < c.TTL) {
		if data, err := os.ReadFile(path); err == nil {
			return data, nil
		}
	}
	if c.Offline {
		return nil, fmt.Errorf("%w: %s", ErrOffline, u)
	}
	data, err := c.fetch(ctx, u)
	if err != nil {
		if statErr == nil && ctx.Err() == nil {
			if stale, rerr := os.ReadFile(path); rerr == nil {
				return stale, nil
			}
		}
		return nil, err
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("polyhaven: %s: not JSON", u)
	}
	_ = writeAtomic(path, data)
	return data, nil
}

// fetch GETs u whole.
func (c *Client) fetch(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent())
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("polyhaven: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("polyhaven: %s: %s", u, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}

// writeAtomic writes data to path through a temporary file.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}
