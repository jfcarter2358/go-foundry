package fileutil

import (
	"encoding/json"
	"hash/fnv"

	"gopkg.in/yaml.v2"
)

// Fingerprint hashes a document's serialisable form, for telling whether it
// still matches what was last written — the editors' "Saved"/"Unsaved Changes"
// chip compares two of these.
//
// JSON rather than the YAML the files are written in, because it is an order
// of magnitude faster (a 2 MB level: ~20 ms against ~300 ms) and just as
// deterministic: it sorts map keys and follows pointers where %v would print
// their addresses. JSON refuses a NaN or a map with a non-string key, so a
// value it cannot encode falls back to YAML, slow but total.
func Fingerprint(v any) uint64 {
	h := fnv.New64a()
	if err := json.NewEncoder(h).Encode(v); err != nil {
		h.Reset()
		data, err := yaml.Marshal(v)
		if err != nil {
			return 0
		}
		h.Write(data)
	}
	return h.Sum64()
}
