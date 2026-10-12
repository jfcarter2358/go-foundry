package polyhaven

// search.go — picking assets from the catalog: by text, category and tags
// (the browser's search box), and the deterministic "query" a procgen stage
// can use instead of a list of IDs.
//
// A stage's query must give the same IDs every run, or its output changes
// under it; download counts move daily, so Select sorts by ID, never by
// popularity. The browser sorts its grid however it likes (Sort).

import (
	"context"
	"sort"
	"strings"
)

// Query filters a catalog.
type Query struct {
	// Text matches the ID, name, tags and categories (all words, any order).
	Text string
	// Category must be one of the asset's categories (the API's c=).
	Category string
	// Tags must all be among the asset's tags or categories.
	Tags []string
	// MaxPolycount drops models with more triangles (0: no limit).
	MaxPolycount int
}

// Match reports whether a passes q.
func (q *Query) Match(a *Asset) bool {
	if q.Category != "" && !hasFold(a.Categories, q.Category) {
		return false
	}
	for _, t := range q.Tags {
		t = strings.TrimSpace(t)
		if t != "" && !hasFold(a.Tags, t) && !hasFold(a.Categories, t) {
			return false
		}
	}
	if q.MaxPolycount > 0 && a.Polycount > q.MaxPolycount {
		return false
	}
	if words := strings.Fields(strings.ToLower(q.Text)); len(words) > 0 {
		hay := strings.ToLower(a.ID + " " + a.Name + " " + strings.Join(a.Tags, " ") + " " + strings.Join(a.Categories, " "))
		for _, w := range words {
			if !strings.Contains(hay, w) {
				return false
			}
		}
	}
	return true
}

func hasFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// Filter is the catalog's assets passing q, sorted by ID.
func Filter(cat map[string]*Asset, q Query) []*Asset {
	var out []*Asset
	for _, a := range cat {
		if q.Match(a) {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Sort orders assets for display: "name", "downloads" (most first), "date"
// (newest first), "polycount" (fewest first); ID breaks ties.
func Sort(as []*Asset, by string) {
	sort.SliceStable(as, func(i, j int) bool {
		a, b := as[i], as[j]
		switch by {
		case "downloads":
			if a.DownloadCount != b.DownloadCount {
				return a.DownloadCount > b.DownloadCount
			}
		case "date":
			if a.DatePublished != b.DatePublished {
				return a.DatePublished > b.DatePublished
			}
		case "polycount":
			if a.Polycount != b.Polycount {
				return a.Polycount < b.Polycount
			}
		default:
			if !strings.EqualFold(a.Name, b.Name) {
				return strings.ToLower(a.Name) < strings.ToLower(b.Name)
			}
		}
		return a.ID < b.ID
	})
}

// Select resolves a stage's sources: the listed IDs in order (each checked
// against the catalog when one is given), then — when q has a category,
// tags or text — up to count more from the catalog by ID order, skipping
// those already listed. count <= 0 takes every match.
func Select(ctx context.Context, c *Client, typ string, ids []string, q Query, count int) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if err := checkID(id); err != nil {
			return nil, err
		}
		seen[id] = true
		out = append(out, id)
	}
	if q.Category == "" && len(q.Tags) == 0 && strings.TrimSpace(q.Text) == "" {
		return out, nil
	}
	cat, err := c.Assets(ctx, typ, q.Category)
	if err != nil {
		return nil, err
	}
	n := 0
	for _, a := range Filter(cat, q) {
		if seen[a.ID] {
			continue
		}
		if count > 0 && n >= count {
			break
		}
		seen[a.ID] = true
		out = append(out, a.ID)
		n++
	}
	return out, nil
}
