package polyhaven

// download.go — files, fetched once and verified.
//
// Every file a listing names carries its md5, so the on-disk store is
// content-addressed by it: files/<md5> holds the bytes, verified before they
// are moved there. Fetch answers from the store when the file is there (even
// offline), and otherwise downloads it:
//
//   - through the client's semaphore (MaxConcurrent at once),
//   - into files/<md5>.part, resumed with an HTTP Range from what is already
//     there when an attempt fails part way (a server that ignores the Range
//     answers 200 and the part file is started over),
//   - retried Retries times with doubling back-off — but never a cancelled
//     context, and never a 4xx (a missing file does not appear on a retry),
//   - verified against the md5 (a mismatch discards the part file and counts
//     as a failed attempt),
//   - de-duplicated: two callers asking for one md5 at once share one
//     transfer.
//
// FetchTo copies (hard-links where it can) a stored file to where an import
// wants it.

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Progress reports bytes done of total (total 0 when unknown).
type Progress func(done, total int64)

// StorePath is where file f is kept once downloaded.
func (c *Client) StorePath(f File) string {
	return filepath.Join(c.CacheDir, "files", strings.ToLower(f.MD5))
}

// Cached reports whether f is already in the store.
func (c *Client) Cached(f File) bool {
	_, err := os.Stat(c.StorePath(f))
	return err == nil
}

// Fetch makes sure f is in the store and returns its path. progress (may be
// nil) is told about bytes as they arrive.
func (c *Client) Fetch(ctx context.Context, f File, progress Progress) (string, error) {
	c.init()
	if f.URL == "" {
		return "", fmt.Errorf("polyhaven: a file with no url")
	}
	if len(f.MD5) != 32 {
		return "", fmt.Errorf("polyhaven: %s: no md5 to verify against", f.URL)
	}
	dst := c.StorePath(f)
	if _, err := os.Stat(dst); err == nil {
		if progress != nil {
			progress(f.Size, f.Size)
		}
		return dst, nil
	}
	if c.Offline {
		return "", fmt.Errorf("%w: %s", ErrOffline, f.URL)
	}
	key := strings.ToLower(f.MD5)
	c.mu.Lock()
	if fl, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-fl.done:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		if fl.err != nil {
			return "", fl.err
		}
		if progress != nil {
			progress(f.Size, f.Size)
		}
		return dst, nil
	}
	fl := &flight{done: make(chan struct{})}
	c.inflight[key] = fl
	c.mu.Unlock()

	fl.err = c.download(ctx, f, dst, progress)
	c.mu.Lock()
	delete(c.inflight, key)
	c.mu.Unlock()
	close(fl.done)
	if fl.err != nil {
		return "", fl.err
	}
	return dst, nil
}

// permanent marks an error a retry cannot fix.
type permanent struct{ error }

func (c *Client) download(ctx context.Context, f File, dst string, progress Progress) error {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.sem }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	part := dst + ".part"
	wait := c.Backoff
	var err error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
			}
			wait *= 2
		}
		err = c.attempt(ctx, f, part, progress)
		if err == nil {
			if err = verifyMD5(part, f.MD5); err == nil {
				return os.Rename(part, dst)
			}
			os.Remove(part) // corrupt: start over
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var p permanent
		if errors.As(err, &p) {
			return p.error
		}
	}
	return fmt.Errorf("polyhaven: %s: %w (after %d attempts)", f.URL, err, c.Retries+1)
}

// attempt fetches f into part, resuming from part's length.
func (c *Client) attempt(ctx context.Context, f File, part string, progress Progress) error {
	var have int64
	if st, err := os.Stat(part); err == nil {
		have = st.Size()
		if f.Size > 0 && have >= f.Size {
			// Complete (or over-long): let the md5 decide.
			if have == f.Size {
				return nil
			}
			os.Remove(part)
			have = 0
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return permanent{err}
	}
	req.Header.Set("User-Agent", UserAgent())
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
		flags |= os.O_APPEND
	case resp.StatusCode == http.StatusOK:
		flags |= os.O_TRUNC
		have = 0
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		os.Remove(part)
		return fmt.Errorf("range not satisfiable")
	case resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests:
		return permanent{fmt.Errorf("polyhaven: %s: %s", f.URL, resp.Status)}
	default:
		return fmt.Errorf("%s", resp.Status)
	}
	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return permanent{err}
	}
	var done atomic.Int64
	done.Store(have)
	w := io.Writer(out)
	if progress != nil {
		progress(have, f.Size)
		w = progressWriter{w: out, n: &done, total: f.Size, fn: progress}
	}
	_, cerr := io.Copy(w, resp.Body)
	if err := out.Close(); cerr == nil {
		cerr = err
	}
	return cerr
}

type progressWriter struct {
	w     io.Writer
	n     *atomic.Int64
	total int64
	fn    Progress
}

func (p progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.fn(p.n.Add(int64(n)), p.total)
	return n, err
}

// verifyMD5 checks path's md5.
func verifyMD5(path, want string) error {
	got, err := FileMD5(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("md5 mismatch: got %s, want %s", got, want)
	}
	return nil
}

// FileMD5 is a file's md5, hex.
func FileMD5(path string) (string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer fh.Close()
	h := md5.New()
	if _, err := io.Copy(h, fh); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FetchTo fetches f and puts a copy at dst (a hard link when the store and
// dst share a file system), unless dst already holds those bytes.
func (c *Client) FetchTo(ctx context.Context, f File, dst string, progress Progress) error {
	if st, err := os.Stat(dst); err == nil && (f.Size == 0 || st.Size() == f.Size) {
		if verifyMD5(dst, f.MD5) == nil {
			if progress != nil {
				progress(f.Size, f.Size)
			}
			return nil
		}
	}
	src, err := c.Fetch(ctx, f, progress)
	if err != nil {
		return err
	}
	return CopyFile(src, dst)
}

// CopyFile copies src to dst (atomically, linking when it can).
func CopyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	os.Remove(tmp)
	if err := os.Link(src, tmp); err != nil {
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(tmp)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			os.Remove(tmp)
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
	return os.Rename(tmp, dst)
}

// Job is one file of a batch.
type Job struct {
	File File
	// Dst, when set, is where FetchAll copies it; otherwise it stays in the
	// store.
	Dst string
}

// FetchAll fetches every job (MaxConcurrent at a time — the semaphore is the
// client's, so concurrent batches share it), reporting the batch's bytes.
// It stops at the first error, cancelling the rest.
func (c *Client) FetchAll(ctx context.Context, jobs []Job, progress Progress) error {
	c.init()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var total int64
	for _, j := range jobs {
		total += j.File.Size
	}
	per := make([]atomic.Int64, len(jobs))
	var sum atomic.Int64
	errs := make(chan error, len(jobs))
	for i, j := range jobs {
		go func(i int, j Job) {
			p := func(done, _ int64) {
				d := done - per[i].Swap(done)
				if progress != nil {
					progress(sum.Add(d), total)
				}
			}
			var err error
			if j.Dst != "" {
				err = c.FetchTo(ctx, j.File, j.Dst, p)
			} else {
				_, err = c.Fetch(ctx, j.File, p)
			}
			if err != nil {
				cancel()
			}
			errs <- err
		}(i, j)
	}
	var first error
	for range jobs {
		if err := <-errs; err != nil && (first == nil || errors.Is(first, context.Canceled)) {
			first = err
		}
	}
	return first
}
