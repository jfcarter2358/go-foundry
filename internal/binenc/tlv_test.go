package binenc

import (
	"reflect"
	"testing"
)

// Exact keeps an empty slice apart from a nil one; gob's rules do not.
func TestExactKeepsEmptyApartFromNil(t *testing.T) {
	type s struct {
		Empty, Nil []int
		Rows       [][]float32
		P          *int
	}
	zero := 0
	in := s{Empty: []int{}, Rows: [][]float32{{}, nil, {1, 2}}, P: &zero}
	data, err := MarshalOptions(&in, Options{Exact: true})
	if err != nil {
		t.Fatal(err)
	}
	var out s
	if err := Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("exact round trip:\n%#v\n%#v", in, out)
	}

	data, err = Marshal(&in)
	if err != nil {
		t.Fatal(err)
	}
	out = s{}
	if err := Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if out.Empty != nil || out.Rows[0] != nil || out.P == nil || *out.P != 0 {
		t.Fatalf("gob-rules round trip: %#v", out)
	}
}
