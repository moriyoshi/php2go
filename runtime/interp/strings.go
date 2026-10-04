package interp

import (
	"github.com/moriyoshi/php2go/runtime/stdlib"
)

// The byte-string library lives in `runtime/stdlib` (see `stringfns.go` there):
// both back-ends need it, and an emitted program -- its own Go module -- can
// import that package while it cannot import the interpreter's internals. What
// is left here is the interpreter's own use of the helpers that went with it.
//
// The functions themselves come back into this package's table through the
// bridge at the foot of `builtins.go`, which is the dispatch path they were
// already on.

// mapString wraps the shared builder so an interpreter table entry keeps its own
// type. The two function types are structurally identical; the conversion is
// what keeps the table declaration unchanged.
func mapString(transform func(string) string) builtinFunc {
	return builtinFunc(stdlib.MapStringBuiltin(transform))
}
