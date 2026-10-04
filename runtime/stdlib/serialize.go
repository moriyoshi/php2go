package stdlib

import (
	"fmt"
	"math"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// `serialize`. Everything it needs about an object it asks the Ctx for: whether
// the class has `__serialize`, `serialize` or `__sleep`, the call that runs
// one, whether the class is one the engine refuses to write, and the
// initializer a lazy object owes before any of it.
//
// The property table it writes is the **storage** one -- `"\0*\0b"` for a
// protected property, `"\0C\0c"` for a private -- which is the same question
// `get_mangled_object_vars` asks, so it is the same code.

// maxSerializeNesting bounds a structure php2go would otherwise follow until
// the Go stack ran out. The engine has no such limit; this is an honest refusal
// rather than a crash.
const maxSerializeNesting = 512

func init() {
	php.RegisterBuiltin("serialize", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.String("N;"), nil
		}
		subject := php.Arg(args, 0)
		if !serializesLazily(ctx, subject) {
			// Serializing normally has to see the state, so it produces it --
			// unless the object was made with SKIP_INITIALIZATION_ON_SERIALIZE,
			// or its class answers for serialization itself, in which case the
			// accessor is asked and the state is never wanted.
			resolved, err := ctx.Materialize(subject)
			if err != nil {
				return php.Undef(), err
			}
			subject = resolved
		}
		sessions.serializeDepth++
		defer func() { sessions.serializeDepth-- }()
		var out strings.Builder
		// **Where the call was made**, kept because running `__sleep` moves the
		// location to the method's own lines and nothing puts it back: the
		// engine blames the `serialize()` call for a name `__sleep` invented,
		// not the last line the hook executed.
		state := &serialState{}
		state.file, state.line = ctx.Location()
		if err := serializeValue(ctx, &out, subject, state); err != nil {
			return php.Undef(), err
		}
		return php.String(out.String()), nil
	})
}

// serializesLazily reports an object whose state serialize must **not**
// produce. `__serialize` decides for itself what to write, so the state is
// never wanted; `__sleep` does not -- it answers with property *names*, and the
// values behind them still have to exist.
func serializesLazily(ctx *php.Ctx, value php.Value) bool {
	object := value.Deref().Object()
	if object == nil || object.Lazy == nil {
		return false
	}
	// An incomplete object's marker is not laziness: its state is what it
	// holds, and serializeObject writes it.
	if object.Lazy.Incomplete {
		return true
	}
	return object.Lazy.SkipSerialize || ctx.HasMethod(object.ClassName, "__serialize")
}

// serialState is the writer's bookkeeping: what has been written already, so a
// repeat is a back-reference rather than a second copy, and what position each
// one was given.
type serialState struct {
	// n counts the values written, which is what `r:` and `R:` index.
	n       int
	nesting int
	seen    map[*php.Object]int
	refs    map[*php.Ref]int
	// file and line are the call site, for a diagnostic raised after a hook
	// has run and moved the location.
	file string
	line uint32
}

func serializeValue(ctx *php.Ctx, out *strings.Builder, value php.Value, state *serialState) error {
	state.n++
	state.nesting++
	defer func() { state.nesting-- }()
	if state.nesting > maxSerializeNesting {
		return php.Unsupported("serialization nested deeper than php2go can follow")
	}
	if ref, isRef := value.RefOf(); isRef && ref != nil {
		if at, written := state.refs[ref]; written {
			fmt.Fprintf(out, "R:%d;", at)
			return nil
		}
		if state.refs == nil {
			state.refs = map[*php.Ref]int{}
		}
		state.refs[ref] = state.n
	}
	value = value.Deref()
	switch value.Kind() {
	case php.KindUndef, php.KindNull:
		out.WriteString("N;")
	case php.KindTrue:
		out.WriteString("b:1;")
	case php.KindFalse:
		out.WriteString("b:0;")
	case php.KindInt:
		fmt.Fprintf(out, "i:%d;", value.Int())
	case php.KindFloat:
		fmt.Fprintf(out, "d:%s;", serializeFloat(value.Float()))
	case php.KindString:
		writeSerialString(out, value.String())
	case php.KindArray:
		array := value.Array()
		fmt.Fprintf(out, "a:%d:{", array.Len())
		var failed error
		array.EachRaw(func(k php.Key, item php.Value) bool {
			if k.IsInt {
				fmt.Fprintf(out, "i:%d;", k.Int)
			} else {
				writeSerialString(out, k.Str)
			}
			if err := serializeValue(ctx, out, item, state); err != nil {
				failed = err
				return false
			}
			return true
		})
		if failed != nil {
			return failed
		}
		out.WriteString("}")
	case php.KindObject:
		return serializeObject(ctx, out, value.Object(), state)
	default:
		return php.Raise("Exception", "Serialization of '%s' is not allowed", php.TypeName(value))
	}
	return nil
}

func serializeObject(ctx *php.Ctx, out *strings.Builder, object *php.Object, state *serialState) error {
	if at, written := state.seen[object]; written {
		// The same object again: PHP writes where it first appeared rather than
		// a second copy, which is also what terminates a cycle.
		fmt.Fprintf(out, "r:%d;", at)
		return nil
	}
	// An **anonymous class** is never serializable -- it has no name an
	// unserialize could find it by -- and its name is the one with a NUL in it.
	if ctx.ClassFactsOf(object.ClassName).NotSerializable || strings.IndexByte(object.ClassName, 0) >= 0 {
		// A handful of the engine's classes hold state that has no written
		// form, and it says so rather than writing an empty object.
		return php.Raise("Exception", "Serialization of '%s' is not allowed",
			php.DisplayName(object.ClassName))
	}
	if state.seen == nil {
		state.seen = map[*php.Object]int{}
	}
	state.seen[object] = state.n
	if object.EnumCase != "" {
		// An enum case serializes as its **name**, not its state: the case
		// itself is the identity.
		writeSerialTagged(out, "E", object.ClassName+":"+object.EnumCase)
		return nil
	}
	if php.IsIncomplete(object) {
		return serializeIncomplete(ctx, out, object, state)
	}

	// `__serialize` replaces the whole state with the array it returns, and the
	// result is written with the `O:` form all the same.
	if ctx.HasMethod(object.ClassName, "__serialize") {
		described, err := lockedHook(func() (php.Value, error) { return callOn(ctx, object, "__serialize") })
		if err != nil {
			return err
		}
		if described.Deref().Kind() != php.KindArray {
			return php.Raise("TypeError", "%s::__serialize() must return an array", object.ClassName)
		}
		table := described.Deref().Array()
		writeSerialHeader(out, object.ClassName, table.Len())
		var failed error
		table.EachRaw(func(k php.Key, item php.Value) bool {
			if k.IsInt {
				fmt.Fprintf(out, "i:%d;", k.Int)
			} else {
				writeSerialString(out, k.Str)
			}
			if err := serializeValue(ctx, out, item, state); err != nil {
				failed = err
				return false
			}
			return true
		})
		if failed != nil {
			return failed
		}
		out.WriteString("}")
		return nil
	}

	// The deprecated `Serializable` interface: the class writes its own payload
	// and the result is tagged `C:` rather than `O:`. `__serialize` wins when a
	// class has both, which is why this comes after it.
	if ctx.InstanceOfClass(php.Obj(object), "Serializable") &&
		ctx.HasMethod(object.ClassName, "serialize") {
		payload, err := joiningWriter(state, func() (php.Value, error) { return callOn(ctx, object, "serialize") })
		if err != nil {
			return err
		}
		if payload.Deref().Kind() == php.KindNull {
			out.WriteString("N;")
			return nil
		}
		text := payload.Deref().String()
		fmt.Fprintf(out, "C:%d:%q:%d:{%s}", len(object.ClassName),
			object.ClassName, len(text), text)
		return nil
	}

	// `__sleep` is the older hook: it does not replace the state, it *selects*
	// which properties of it are written, in the order it returns them.
	var keep []string
	if ctx.HasMethod(object.ClassName, "__sleep") {
		selected, err := lockedHook(func() (php.Value, error) { return callOn(ctx, object, "__sleep") })
		if err != nil {
			return err
		}
		if selected.Deref().Kind() != php.KindArray {
			ctx.Notice("serialize(): __sleep() should return an array only " +
				"containing the names of instance-variables to serialize")
			writeSerialHeader(out, object.ClassName, 0)
			out.WriteString("}")
			return nil
		}
		selected.Deref().Array().Each(func(_ php.Key, item php.Value) bool {
			keep = append(keep, item.Deref().String())
			return true
		})
		// __sleep may have touched the object and so produced its state. For a
		// proxy that state lives in the instance, and it is the instance's
		// properties that get written from here on.
		object = php.LazyState(object)
	}

	// The **storage** table: a protected property is `"\0*\0name"` and a
	// private `"\0Class\0name"`, which is what makes an unserialized private
	// land back in the right class.
	stored := MangledProperties(object)
	var names []string
	var values []php.Value
	if keep != nil {
		for _, name := range keep {
			key, item, found := storedProperty(object, stored, name)
			if !found {
				// A declared property that simply holds nothing yet is left out
				// without complaint: the name exists, it is the value that does
				// not. Only a name the class never declared earns the warning.
				if _, declared := object.Declared[name]; !declared {
					ctx.SetLocation(state.file, state.line)
					ctx.Warn("serialize(): %q returned as member variable from "+
						"__sleep() but does not exist", name)
				}
				continue
			}
			names = append(names, key)
			values = append(values, item)
		}
	} else {
		stored.EachRaw(func(k php.Key, item php.Value) bool {
			names = append(names, k.String())
			values = append(values, item)
			return true
		})
	}
	writeSerialHeader(out, object.ClassName, len(names))
	for i, name := range names {
		writeSerialString(out, name)
		if err := serializeValue(ctx, out, values[i], state); err != nil {
			return err
		}
	}
	out.WriteString("}")
	return nil
}

// storedProperty finds a property __sleep named, under whichever storage key it
// actually has: the name as written for a public one, and the mangled form for
// a protected or private.
func storedProperty(object *php.Object, stored *php.Array, name string) (string, php.Value, bool) {
	for _, key := range []string{name,
		php.MangleProperty("*", name),
		php.MangleProperty(php.DisplayName(declaringClassOf(object, name)), name)} {
		if item, ok := stored.Raw(php.StringKey(key)); ok {
			return key, item, true
		}
	}
	return "", php.Undef(), false
}

// declaringClassOf is the class a property was written in, for the private
// mangling. Empty when nothing declares it, which leaves the mangled candidate
// harmlessly unmatched.
func declaringClassOf(object *php.Object, name string) string {
	if d, ok := object.Declared[name]; ok && d.Class != "" {
		return d.Class
	}
	return object.ClassName
}

// callOn runs one of the serialization hooks. It is a method call like any
// other, so it goes through the callable hook rather than a second dispatcher.
func callOn(ctx *php.Ctx, object *php.Object, method string) (php.Value, error) {
	callee := php.NewArray()
	callee.Append(php.Obj(object))
	callee.Append(php.String(method))
	return ctx.Call(php.Arr(callee), nil)
}

// writeSerialHeader writes the `O:<len>:"<class>":<count>:{` an object opens
// with. It differs from a plain string only in what follows the closing quote.
func writeSerialHeader(out *strings.Builder, className string, count int) {
	fmt.Fprintf(out, "O:%d:\"", len(className))
	out.WriteString(className)
	fmt.Fprintf(out, "\":%d:{", count)
}

func writeSerialString(out *strings.Builder, s string) { writeSerialTagged(out, "s", s) }

// writeSerialTagged writes the `<tag>:<len>:"<bytes>";` form. The bytes go out
// raw -- the length is what delimits them, so a quote or a NUL inside needs no
// escaping and must not get any.
func writeSerialTagged(out *strings.Builder, tag, s string) {
	fmt.Fprintf(out, "%s:%d:\"", tag, len(s))
	out.WriteString(s)
	out.WriteString("\";")
}

// serializeFloat writes a float the way serialize does with the default
// serialize_precision: the shortest form that round-trips, with the specials
// spelled out.
func serializeFloat(number float64) string {
	switch {
	case math.IsNaN(number):
		return "NAN"
	case math.IsInf(number, 1):
		return "INF"
	case math.IsInf(number, -1):
		return "-INF"
	}
	return php.ReprFloat(number, 'E', false)
}

// serializeIncomplete writes an __PHP_Incomplete_Class object back under the
// class it stands for, without the name property. One with no name -- made
// with `new`, or unserialized from its own class name -- writes as an empty
// object of the incomplete class, whatever it holds (probed).
func serializeIncomplete(ctx *php.Ctx, out *strings.Builder, object *php.Object, state *serialState) error {
	name := php.IncompleteName(object)
	if name == "" {
		writeSerialHeader(out, php.IncompleteClass, 0)
		out.WriteString("}")
		return nil
	}
	stored := MangledProperties(object)
	var names []string
	var values []php.Value
	stored.EachRaw(func(k php.Key, item php.Value) bool {
		if !k.IsInt && k.Str == php.IncompleteNameProperty {
			return true
		}
		names = append(names, k.String())
		values = append(values, item)
		return true
	})
	writeSerialHeader(out, name, len(names))
	for i, n := range names {
		writeSerialString(out, n)
		if err := serializeValue(ctx, out, values[i], state); err != nil {
			return err
		}
	}
	out.WriteString("}")
	return nil
}
