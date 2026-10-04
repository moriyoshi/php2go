package main

import (
	"os"
	"testing"
)

// TestGeneratedShapeIsCurrent fails when shape.tsv no longer describes the
// userland library, for the same reason its surface twin does: the table is
// only useful as review material if it is regenerated with the change that
// moves it.
//
// A diff here is not automatically a problem. Two kinds are:
//
//   - a helper's `selectors` going from 0 or 1 to 2 or more, which means a new
//     operation was added by widening an existing helper's string switch rather
//     than by writing a function;
//   - a new row with `reachable=no`, which means another userland function just
//     became impossible to emit.
func TestGeneratedShapeIsCurrent(t *testing.T) {
	want, err := os.ReadFile("../../shape.tsv")
	if err != nil {
		t.Fatal(err)
	}
	got, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("shape.tsv is stale; run go generate ./internal/userland")
	}
}
