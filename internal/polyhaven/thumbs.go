package polyhaven

// thumbs.go — asset thumbnails for the browser and the Generator's id-list
// widget: the CDN's PNG at 256 px, cached on disk (thumbs/<id>_<w>.png) and
// never refetched (a thumbnail changes only when the asset's img_version
// does, which the browser can pass as a cache-buster key).

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// ThumbnailWidth is the size the browser asks for.
const ThumbnailWidth = 256

// ThumbnailURL is the CDN address of id's thumbnail at width w.
func (c *Client) ThumbnailURL(id string, w int) string {
	return fmt.Sprintf("%s/asset_img/thumbs/%s.png?width=%d", c.CDN, url.PathEscape(id), w)
}

// Thumbnail is id's thumbnail PNG bytes (width w), from the cache or the CDN.
func (c *Client) Thumbnail(ctx context.Context, id string, w int) ([]byte, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	c.init()
	if w <= 0 {
		w = ThumbnailWidth
	}
	path := filepath.Join(c.CacheDir, "thumbs", fmt.Sprintf("%s_%d.png", id, w))
	if data, err := os.ReadFile(path); err == nil {
		return data, nil
	}
	if c.Offline {
		return nil, fmt.Errorf("%w: thumbnail %s", ErrOffline, id)
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	data, err := c.fetch(ctx, c.ThumbnailURL(id, w))
	<-c.sem
	if err != nil {
		return nil, err
	}
	if len(data) < 8 || string(data[1:4]) != "PNG" {
		return nil, fmt.Errorf("polyhaven: thumbnail %s is not a PNG", id)
	}
	_ = writeAtomic(path, data)
	return data, nil
}
