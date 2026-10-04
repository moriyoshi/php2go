package php

import "strings"

// `SensitiveParameterValue` -- the wrapper `#[\SensitiveParameter]` puts around
// an argument in a backtrace, so a password does not end up in a log.
//
// It holds one value and shows nothing: var_dump, print_r, var_export, a cast
// to array and json_encode all see an object with no properties, because the
// value is not in the property table as far as any of them can tell. That is
// the whole point, so the value lives in `Internal` and only `getValue()`
// answers.
//
// The type is **here** rather than in either back-end because both need to make
// one: the interpreter builds it from its object store and a compiled program
// from its own, and neither should have its own idea of what the payload is.

// SensitiveValue is what one of these objects holds.
type SensitiveValue struct{ value Value }

// HidesProperties keeps the declared `$value` slot out of anything that walks
// the table.
func (*SensitiveValue) HidesProperties() bool { return true }

// Held is the wrapped value, which `getValue()` answers with.
func (s *SensitiveValue) Held() Value {
	if s == nil {
		return Null()
	}
	return s.value
}

// InitSensitiveParameterValue fills an **already numbered** object. The handle
// belongs to whoever allocated it, which is why this takes the object rather
// than making one.
func InitSensitiveParameterValue(o *Object, held Value) {
	if o == nil {
		return
	}
	held = held.Deref().Copy()
	o.Internal = &SensitiveValue{value: held}
	// The declared private `$value` really does hold it -- reflection reads
	// that slot -- and `HidesProperties` is what keeps everything else out.
	o.Set("value", held)
}

// SensitiveHeld answers the wrapped value, reporting false for any other
// object.
func SensitiveHeld(o *Object) (Value, bool) {
	if o == nil {
		return Undef(), false
	}
	held, ok := o.Internal.(*SensitiveValue)
	if !ok {
		return Undef(), false
	}
	return held.Held(), true
}

// ReleaseTraceHandles hands back the numbers of the SensitiveParameterValue
// objects a **dying trace array** made, in the order the engine frees them:
// frames in array order, and within a frame its `args` in order. The engine's
// free list is last-in first-out, so that order is what the next objects are
// numbered by -- `debug_print_backtrace()` of a call with two hidden arguments
// leaves the second one's number on top (sensitive_parameter_multiple_arguments).
//
// Only the wrappers are released. Everything else a trace holds -- a receiver
// under `object`, an argument that is an object -- belongs to a frame that is
// still running, and survives the array.
func ReleaseTraceHandles(c *Ctx, trace Value) {
	frames := trace.Deref().Array()
	if frames == nil {
		return
	}
	frames.Each(func(_ Key, frame Value) bool {
		entry := frame.Array()
		if entry == nil {
			return true
		}
		args, ok := entry.Get(StringKey("args"))
		if !ok || args.Deref().Array() == nil {
			return true
		}
		args.Deref().Array().Each(func(_ Key, arg Value) bool {
			if o := arg.Object(); o != nil {
				if _, hidden := o.Internal.(*SensitiveValue); hidden {
					c.ReleaseHandle(o)
				}
			}
			return true
		})
		return true
	})
}

// The engine's own classes that refuse to be written at all: their state has no
// serialized form, so `serialize` raises rather than writing an empty object.
//
// **Here** because both back-ends need it and neither declares these classes --
// they are the engine's, so the harvest's per-class flag never covers them for
// a program that merely *uses* one. Read back from the engine rather than
// guessed at.
var engineNotSerializable = map[string]bool{
	"generator":     true,
	"closure":       true,
	"weakmap":       true,
	"weakref":       true,
	"weakreference": true,
	"fiber":         true,
	// Probed: `Serialization of 'InternalIterator' is not allowed`.
	"internaliterator": true,
	// Probed: `serialize(new SensitiveParameterValue("x"))` and the
	// `unserialize` of one are both refused.
	"sensitiveparametervalue": true,
}

// EngineNotSerializable reports one of them.
func EngineNotSerializable(class string) bool {
	return engineNotSerializable[strings.ToLower(strings.TrimPrefix(class, "\\"))]
}
