package render

import (
	"reflect"
	"testing"
)

// The cache reads back exactly the assets it was given, and refuses an entry
// that is cut short.
func TestTheFoliageCacheRoundTrips(t *testing.T) {
	a := testAssets(t)
	b := a.encode()
	back, err := decodeNfAssets(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*a, back) {
		t.Fatal("the assets changed in the round trip")
	}
	if _, err := decodeNfAssets(b[:len(b)-8]); err == nil {
		t.Fatal("a cut-short entry decoded")
	}
}
