package polyhaven

// client_test.go — the client against an httptest server standing in for
// the API and the CDN (never the live service): the User-Agent on every
// request, the listing cache and its TTL, offline mode, md5 verification,
// resume, retries, the concurrency cap, de-duplication and cancellation.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeServer struct {
	*httptest.Server
	mu       sync.Mutex
	hits     map[string]int
	agents   map[string]bool
	files    map[string][]byte // path → bytes
	failNext map[string]int    // path → how many requests fail (500) first
	cutAt    map[string]int    // path → first full response stops after this many bytes
	active   atomic.Int32
	peak     atomic.Int32
	slow     time.Duration
	ranges   int
}

func newFake(t *testing.T) *fakeServer {
	f := &fakeServer{hits: map[string]int{}, agents: map[string]bool{}, files: map[string][]byte{},
		failNext: map[string]int{}, cutAt: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	key := r.URL.Path
	if r.URL.RawQuery != "" && strings.HasPrefix(key, "/assets") {
		key += "?" + r.URL.RawQuery
	}
	f.hits[key]++
	f.agents[r.Header.Get("User-Agent")] = true
	fail := f.failNext[r.URL.Path]
	if fail > 0 {
		f.failNext[r.URL.Path] = fail - 1
	}
	cut := f.cutAt[r.URL.Path]
	delete(f.cutAt, r.URL.Path)
	slow := f.slow
	f.mu.Unlock()

	n := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if slow > 0 {
		select {
		case <-time.After(slow):
		case <-r.Context().Done():
			return
		}
	}
	if fail > 0 {
		http.Error(w, "busy", http.StatusInternalServerError)
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/assets"):
		cat := map[string]any{
			"rock_a":  map[string]any{"name": "Rock A", "type": 2, "categories": []string{"rocks", "nature"}, "tags": []string{"desert"}, "polycount": 1000, "dimensions": []float64{2000, 1000, 1500}, "authors": map[string]string{"Ann": "All"}},
			"rock_b":  map[string]any{"name": "Rock B", "type": 2, "categories": []string{"rocks"}, "tags": []string{"desert", "cliff"}, "polycount": 9000},
			"shrub_c": map[string]any{"name": "Shrub C", "type": 2, "categories": []string{"plants"}, "tags": []string{"desert"}},
		}
		if c := r.URL.Query().Get("c"); c == "plants" {
			delete(cat, "rock_a")
			delete(cat, "rock_b")
		}
		json.NewEncoder(w).Encode(cat)
	case strings.HasPrefix(r.URL.Path, "/info/"):
		id := strings.TrimPrefix(r.URL.Path, "/info/")
		if id != "rock_a" {
			http.Error(w, "{}", http.StatusNotFound)
			return
		}
		fmt.Fprint(w, `{"name":"Rock A","type":2,"categories":["rocks"],"tags":["desert"],"polycount":1000,"authors":{"Ann":"All","Bo":"Photography"}}`)
	case strings.HasPrefix(r.URL.Path, "/files/"):
		fmt.Fprintf(w, `{"gltf":{"2k":{"gltf":{"url":"%s/dl/a.gltf","md5":"%s","size":3,"include":{"a.bin":{"url":"%s/dl/a.bin","md5":"x","size":1}}}}},"blend":{"2k":{"blend":{"url":"u","md5":"m","size":1}}},"odd":"not a map"}`,
			f.URL, md5hex([]byte("abc")), f.URL)
	case strings.HasPrefix(r.URL.Path, "/asset_img/thumbs/"):
		w.Write([]byte("\x89PNG\r\n\x1a\nfake"))
	case strings.HasPrefix(r.URL.Path, "/dl/"):
		data, ok := f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if rg := r.Header.Get("Range"); rg != "" {
			from, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rg, "bytes="), "-"))
			f.mu.Lock()
			f.ranges++
			f.mu.Unlock()
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, len(data)-1, len(data)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(data[from:])
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if cut > 0 {
			w.Write(data[:cut])
			if hj, ok := w.(http.Hijacker); ok {
				if c, _, err := hj.Hijack(); err == nil {
					c.Close()
				}
			}
			return
		}
		w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

func md5hex(b []byte) string { s := md5.Sum(b); return hex.EncodeToString(s[:]) }

func testClient(t *testing.T, f *fakeServer) *Client {
	return &Client{API: f.URL, CDN: f.URL, HTTP: f.Client(), CacheDir: t.TempDir(), TTL: time.Hour,
		Retries: 3, Backoff: time.Millisecond}
}

func (f *fakeServer) hit(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[key]
}

func TestEveryRequestNamesFoundry(t *testing.T) {
	f := newFake(t)
	c := testClient(t, f)
	ctx := context.Background()
	f.files["/dl/x"] = []byte("hello")
	if _, err := c.Assets(ctx, TypeModels, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Info(ctx, "rock_a"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Thumbnail(ctx, "rock_a", 256); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(ctx, File{URL: f.URL + "/dl/x", MD5: md5hex([]byte("hello")), Size: 5}, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.agents) != 1 || !f.agents[UserAgent()] || !strings.HasPrefix(UserAgent(), "Foundry-Engine/") {
		t.Fatalf("user agents %v", f.agents)
	}
}

func TestListingsAreCachedForTheTTL(t *testing.T) {
	f := newFake(t)
	c := testClient(t, f)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		cat, err := c.Assets(ctx, TypeModels, "")
		if err != nil || len(cat) != 3 || cat["rock_a"].ID != "rock_a" || cat["rock_a"].Polycount != 1000 {
			t.Fatalf("catalog %v %v", cat, err)
		}
	}
	if n := f.hit("/assets?t=models"); n != 1 {
		t.Fatalf("catalog fetched %d times, want once", n)
	}
	// A category is its own listing.
	cat, err := c.Assets(ctx, TypeModels, "plants")
	if err != nil || len(cat) != 1 {
		t.Fatalf("plants %v %v", cat, err)
	}
	// Past the TTL it is fetched again.
	c.TTL = time.Nanosecond
	time.Sleep(time.Millisecond)
	if _, err := c.Assets(ctx, TypeModels, ""); err != nil {
		t.Fatal(err)
	}
	if n := f.hit("/assets?t=models"); n != 2 {
		t.Fatalf("after the TTL fetched %d times, want 2", n)
	}
	info, err := c.Info(ctx, "rock_a")
	if err != nil || info.Name != "Rock A" || len(info.AuthorList()) != 2 || info.AuthorList()[1] != "Bo (Photography)" {
		t.Fatalf("info %+v %v", info, err)
	}
	if _, err := c.Info(ctx, "../etc"); err == nil {
		t.Fatal("a path as an ID was accepted")
	}
	if _, err := c.Info(ctx, "missing"); err == nil {
		t.Fatal("a missing asset answered")
	}
}

func TestFilesKeepTheThreeLevelEntries(t *testing.T) {
	f := newFake(t)
	c := testClient(t, f)
	files, err := c.Files(context.Background(), "rock_a")
	if err != nil {
		t.Fatal(err)
	}
	g, ok := files.Get("gltf", "2k", "gltf")
	if !ok || g.Size != 3 || len(g.Include) != 1 || g.Include["a.bin"].URL == "" {
		t.Fatalf("gltf %+v", g)
	}
	if _, ok := files["odd"]; ok {
		t.Fatal("a malformed entry was kept")
	}
	if rs := files.Resolutions("gltf"); len(rs) != 1 || rs[0] != "2k" {
		t.Fatalf("resolutions %v", rs)
	}
	rs := []string{"8k", "1k", "16k", "2k"}
	SortResolutions(rs)
	if strings.Join(rs, ",") != "1k,2k,8k,16k" {
		t.Fatalf("sorted %v", rs)
	}
}

func TestOfflineAnswersFromTheCacheOnly(t *testing.T) {
	f := newFake(t)
	c := testClient(t, f)
	ctx := context.Background()
	f.files["/dl/x"] = []byte("data!")
	file := File{URL: f.URL + "/dl/x", MD5: md5hex([]byte("data!")), Size: 5}
	if _, err := c.Assets(ctx, TypeModels, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Fetch(ctx, file, nil); err != nil {
		t.Fatal(err)
	}
	before := len(f.hits)
	c.Offline = true
	c.TTL = time.Nanosecond // stale, but offline takes it
	if cat, err := c.Assets(ctx, TypeModels, ""); err != nil || len(cat) != 3 {
		t.Fatalf("offline catalog %v %v", cat, err)
	}
	if _, err := c.Fetch(ctx, file, nil); err != nil {
		t.Fatalf("offline cached file: %v", err)
	}
	if _, err := c.Info(ctx, "rock_a"); !errors.Is(err, ErrOffline) {
		t.Fatalf("offline uncached info: %v", err)
	}
	if _, err := c.Fetch(ctx, File{URL: f.URL + "/dl/y", MD5: md5hex([]byte("y")), Size: 1}, nil); !errors.Is(err, ErrOffline) {
		t.Fatalf("offline uncached file: %v", err)
	}
	if len(f.hits) != before {
		t.Fatal("an offline client made a request")
	}
}

func TestDownloadsAreVerifiedResumedAndRetried(t *testing.T) {
	f := newFake(t)
	c := testClient(t, f)
	ctx := context.Background()
	data := []byte(strings.Repeat("0123456789", 5000))
	f.files["/dl/big"] = data
	file := File{URL: f.URL + "/dl/big", MD5: md5hex(data), Size: int64(len(data))}

	// The first response dies part way; the retry resumes with a Range.
	f.cutAt["/dl/big"] = 20000
	var last int64
	path, err := c.Fetch(ctx, file, func(done, total int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(data) || last != int64(len(data)) {
		t.Fatalf("got %d bytes (progress %d), want %d", len(got), last, len(data))
	}
	if f.ranges != 1 {
		t.Fatalf("%d ranged requests, want 1 (the resume)", f.ranges)
	}
	// Cached: no more requests.
	n := f.hit("/dl/big")
	if _, err := c.Fetch(ctx, file, nil); err != nil || f.hit("/dl/big") != n {
		t.Fatalf("refetched a stored file (%v)", err)
	}

	// Server errors are retried.
	f.files["/dl/flaky"] = []byte("flaky")
	f.failNext["/dl/flaky"] = 2
	if _, err := c.Fetch(ctx, File{URL: f.URL + "/dl/flaky", MD5: md5hex([]byte("flaky")), Size: 5}, nil); err != nil {
		t.Fatalf("retries: %v", err)
	}
	if f.hit("/dl/flaky") != 3 {
		t.Fatalf("flaky fetched %d times, want 3", f.hit("/dl/flaky"))
	}

	// A wrong md5 is refused and nothing is stored.
	f.files["/dl/bad"] = []byte("bad")
	bad := File{URL: f.URL + "/dl/bad", MD5: md5hex([]byte("good")), Size: 3}
	if _, err := c.Fetch(ctx, bad, nil); err == nil || !strings.Contains(err.Error(), "md5") {
		t.Fatalf("bad md5: %v", err)
	}
	if c.Cached(bad) {
		t.Fatal("a corrupt file was stored")
	}
	// A 404 is not retried.
	if _, err := c.Fetch(ctx, File{URL: f.URL + "/dl/none", MD5: md5hex([]byte("n")), Size: 1}, nil); err == nil {
		t.Fatal("a missing file answered")
	}
	if f.hit("/dl/none") != 1 {
		t.Fatalf("a 404 was tried %d times", f.hit("/dl/none"))
	}
	// FetchTo puts a copy where asked, and leaves a matching one alone.
	dst := t.TempDir() + "/sub/big.bin"
	if err := c.FetchTo(ctx, file, dst, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != string(data) {
		t.Fatal("FetchTo copy differs")
	}
}

func TestAtMostFourDownloadsAtOnce(t *testing.T) {
	f := newFake(t)
	f.slow = 20 * time.Millisecond
	c := testClient(t, f)
	var jobs []Job
	for i := 0; i < 12; i++ {
		b := []byte(fmt.Sprintf("file %d", i))
		p := fmt.Sprintf("/dl/f%d", i)
		f.files[p] = b
		jobs = append(jobs, Job{File: File{URL: f.URL + p, MD5: md5hex(b), Size: int64(len(b))}})
	}
	// The same file twice in one batch is one transfer.
	jobs = append(jobs, jobs[0])
	var mu sync.Mutex
	var last, total int64
	if err := c.FetchAll(context.Background(), jobs, func(done, tot int64) {
		mu.Lock()
		last, total = max(last, done), tot
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	if p := f.peak.Load(); p > MaxConcurrent || p < 2 {
		t.Fatalf("peak concurrency %d, want 2..%d", p, MaxConcurrent)
	}
	if f.hit("/dl/f0") != 1 {
		t.Fatalf("a duplicate job fetched %d times", f.hit("/dl/f0"))
	}
	if last != total {
		t.Fatalf("progress ended at %d of %d", last, total)
	}
}

func TestCancellationStopsADownload(t *testing.T) {
	f := newFake(t)
	f.slow = 5 * time.Second
	c := testClient(t, f)
	f.files["/dl/slow"] = []byte("slow")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	t0 := time.Now()
	_, err := c.Fetch(ctx, File{URL: f.URL + "/dl/slow", MD5: md5hex([]byte("slow")), Size: 4}, nil)
	if err == nil || time.Since(t0) > 2*time.Second {
		t.Fatalf("cancel: %v after %s", err, time.Since(t0))
	}
}

func TestSelectIsDeterministic(t *testing.T) {
	f := newFake(t)
	c := testClient(t, f)
	ctx := context.Background()
	ids, err := Select(ctx, c, TypeModels, []string{"rock_b", " ", "rock_b"}, Query{Tags: []string{"desert"}}, 0)
	if err != nil || strings.Join(ids, ",") != "rock_b,rock_a,shrub_c" {
		t.Fatalf("select %v %v", ids, err)
	}
	ids, _ = Select(ctx, c, TypeModels, nil, Query{Category: "rocks"}, 1)
	if strings.Join(ids, ",") != "rock_a" {
		t.Fatalf("first rock %v", ids)
	}
	ids, _ = Select(ctx, c, TypeModels, nil, Query{Text: "cliff"}, 0)
	if strings.Join(ids, ",") != "rock_b" {
		t.Fatalf("text %v", ids)
	}
	ids, _ = Select(ctx, c, TypeModels, []string{"x_y"}, Query{}, 0)
	if strings.Join(ids, ",") != "x_y" {
		t.Fatalf("ids only %v", ids)
	}
	if _, err := Select(ctx, c, TypeModels, []string{"bad id"}, Query{}, 0); err == nil {
		t.Fatal("a bad id was accepted")
	}
	cat, _ := c.Assets(ctx, TypeModels, "")
	all := Filter(cat, Query{MaxPolycount: 5000})
	if len(all) != 2 {
		t.Fatalf("polycount filter %d", len(all))
	}
	Sort(all, "name")
	if all[0].ID != "rock_a" {
		t.Fatalf("sort %v", all[0].ID)
	}
}
