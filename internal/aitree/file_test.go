package aitree

import (
	"testing"

	"gopkg.in/yaml.v2"
)

func mustMarshal(t *testing.T, d *Doc) string {
	t.Helper()
	b, err := yaml.Marshal(ToRaw(d))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func roundTripYAML(t *testing.T, in, out any) {
	t.Helper()
	b, err := yaml.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(b, out); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
}
