package strutil

// suggest.go — "did you mean ...?" for names a user typed.
//
// Both the script host and the entity diagnostics answer an unknown name with
// the closest known one. It is the same measure in both places, and neither is
// the natural owner of it.

// EditDistance is the Levenshtein distance between two short names.
func EditDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(min(cur[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
