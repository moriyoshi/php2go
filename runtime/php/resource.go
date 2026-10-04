package php

import (
	"strconv"
	"unsafe"
)

// Resource is PHP's IS_RESOURCE: a handle onto something the engine owns, with
// an id the program can see and a type name it prints. What is behind it is not
// this package's concern -- the runtime hangs whatever it needs off Payload --
// but the *identity* is, since a resource compares, casts and prints by id.
//
// Closing does not free the id. A closed resource keeps its number and changes
// its type to "Unknown", which is what `var_dump` then shows.
type Resource struct {
	ID      int
	Type    string
	Payload any
	closed  bool
}

// Res wraps a resource as a value.
func Res(r *Resource) Value { return Value{kind: KindResource, ptr: unsafe.Pointer(r)} }

// Resource returns the handle a value holds, or nil.
func (v Value) Resource() *Resource {
	if v.kind != KindResource {
		return nil
	}
	return v.rs()
}

// Close marks a resource closed. The id survives, so `(int)` still answers and
// `var_dump` still prints the number.
func (r *Resource) Close() {
	if r != nil {
		r.closed = true
		r.Payload = nil
	}
}

// Closed reports whether the handle has been closed.
func (r *Resource) Closed() bool { return r != nil && r.closed }

// TypeName is what var_dump prints after the id. A closed resource has lost
// whatever it named, and the engine says so.
func (r *Resource) TypeName() string {
	if r == nil {
		return "Unknown"
	}
	if r.closed {
		return "Unknown"
	}
	return r.Type
}

// DebugType is what get_debug_type answers, which distinguishes a closed
// resource rather than naming a type that is gone.
func (r *Resource) DebugType() string {
	if r == nil || r.closed {
		return "resource (closed)"
	}
	return "resource (" + r.Type + ")"
}

// String is how a resource renders when a string is asked for. The cast is
// legal and produces this; it is only concatenation-style use that is.
func (r *Resource) String() string {
	if r == nil {
		return "Resource id #0"
	}
	return "Resource id #" + strconv.Itoa(r.ID)
}

// Resources hands out resource ids for one request. See firstScriptResource
// for what the first id is and why a caller may need a different one.
type Resources struct {
	next int
	// First overrides the id the first resource receives, for a caller
	// reproducing a host that has some open already. Zero means the default.
	First int
	// Standard holds STDIN, STDOUT and STDERR, built on first use so a request
	// that never mentions them allocates nothing.
	Standard map[string]*Resource
}

// NewResource hands out the next id.
func (rs *Resources) NewResource(typeName string, payload any) *Resource {
	if rs.next == 0 {
		if rs.next = rs.First; rs.next == 0 {
			rs.next = firstScriptResource
		}
	}
	r := &Resource{ID: rs.next, Type: typeName, Payload: payload}
	rs.next++
	return r
}

// firstScriptResource is the id the first resource a script opens receives
// **in this process**, which is what the linked engine does:
//
//	$a = fopen('php://memory', 'r'); var_dump($a);   // resource(2)
//
// Ids are not reused -- closing one and opening another gives a fresh number,
// confirmed by the same probe.
//
// **The CLI is different, and the `.phpt` corpus was recorded under it**: it
// holds the three standard streams plus one more before a script runs, so its
// first is 5, and some expectations spell `resource(5)` literally rather than
// the `resource(%d)` wildcard most of them use. That is a property of the
// *host*, not of PHP, so it belongs to whoever is reproducing that host --
// `php2phpt` grades against CLI-recorded text and sets `Resources.First` to 5,
// while compiled output is graded byte-exact against the engine running here
// and wants the default. Nine corpus files differed by a uniform +3 for want of
// this distinction.
const firstScriptResource = 2
