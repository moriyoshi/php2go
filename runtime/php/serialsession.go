package php

// The serialization a container's own `serialize()` / `unserialize()` runs:
// several values written or read as **one** session, sharing the
// back-reference table a single `serialize()` call keeps, so an object met
// twice is written as `r:N;` with the session's own numbering
// (PHP_VAR_SERIALIZE_INIT around the container's loop). The writer and the
// reader live in runtime/stdlib, which installs them here.

// SerialWriter writes values into one serialization session.
type SerialWriter interface {
	Write(v Value) (string, error)
}

// SerialReader reads values out of one text as one session.
type SerialReader interface {
	// Read reads the value at the current position. ok is false for text
	// the engine's parser would stop at; err is a refusal or a throw.
	Read() (v Value, ok bool, err error)
	// At is the current position, and Skip moves past bytes the caller
	// recognised itself.
	At() int
	Skip(n int)
	// FailOffset is where the engine's cursor stands after a failed Read,
	// which is the offset its "Error at offset" message names.
	FailOffset() int
	// SharedRefs reports that an `R:` was read: a reference the engine
	// would rebind into the container's own storage (var_replace).
	SharedRefs() bool
}

// NewSerialWriter and NewSerialReader are installed by runtime/stdlib; nil
// leaves the containers to refuse.
var (
	NewSerialWriter func(ctx *Ctx) SerialWriter
	NewSerialReader func(ctx *Ctx, text, function string) SerialReader
)

// LoadMembers is object_properties_load: the members array a container's
// serialization carries, written back onto the object -- a declared
// property into its slot, anything else as a dynamic property with the
// engine's deprecation or refusal. What it cannot place exactly -- a key
// mangled for another class, a typed or readonly property, a reference -- is
// refused.
func LoadMembers(ctx *Ctx, o *Object, members *Array, what string) error {
	var failed error
	members.EachRaw(func(k Key, v Value) bool {
		if v.IsRef() {
			failed = Unsupported("%s with a reference among the properties", what)
			return false
		}
		v = v.Deref()
		name, declared := "", false
		for n, d := range o.Declared {
			if castKey(o, StringKey(n)) == k && !k.IsInt {
				if d.Type != "" || d.Readonly {
					failed = Unsupported("%s writing a typed or readonly property", what)
					return false
				}
				name, declared = n, true
				break
			}
		}
		if declared {
			o.Set(name, v.Copy())
			return true
		}
		if !k.IsInt {
			if _, _, mangled := UnmangleProperty(k.Str); mangled {
				failed = Unsupported("%s with a property mangled for another scope", what)
				return false
			}
			if _, has := o.Declared[k.Str]; has {
				failed = Unsupported("%s writing a non-public property by its plain name", what)
				return false
			}
		}
		facts := ctx.ClassFactsOf(o.ClassName)
		if facts.NoDynamicProperties {
			failed = RaiseAt(ctx, "Error", "Cannot create dynamic property %s::$%s", DisplayName(o.ClassName), k.String())
			return false
		}
		if !facts.AllowDynamicProperties {
			ctx.Deprecated("Creation of dynamic property %s::$%s is deprecated", DisplayName(o.ClassName), k.String())
			if err := ctx.TakePendingError(); err != nil {
				failed = err
				return false
			}
		}
		o.PropSet(k, v.Copy())
		return true
	})
	return failed
}
