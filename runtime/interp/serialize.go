package interp

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// serialState is what makes a repeated object come out as a back-reference
// rather than as a second copy -- and what stops a self-referential one
// recursing forever.
//
// PHP numbers *every* value it writes, starting at one, and remembers where it
// first wrote each object. Property and element keys are not values and are not
// counted. A repeated object is written `r:<first position>;`.
// maxSerializeNesting bounds how deep the writer will follow a structure. PHP
// has no such limit -- it recurses on the C stack and relies on the structure
// being finite -- so a program that nests further is a gap here rather than a
// wrong answer.
const maxSerializeNesting = 4096

type serialState struct {
	n    int
	seen map[*php.Object]int
	// nesting is how deep the writer currently is. A structure can nest more
	// deeply than php2go can recurse, and running out of Go stack is a crash
	// rather than a result.
	nesting int
	// refs is the same for references. A reference used once is written as its
	// value; used again it is `R:<first position>;`, which is what terminates a
	// cycle built out of `$a[] = &$a` rather than out of objects.
	refs map[*php.Ref]int
}

func (in *Interp) serializeValue(ctx *php.Ctx, unit *Unit, out *strings.Builder,
	value php.Value, state *serialState, depth int) error {
	state.n++
	state.nesting++
	defer func() { state.nesting-- }()
	if state.nesting > maxSerializeNesting {
		return &UnsupportedError{Opcode: "serialization nested deeper than php2go can follow"}
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
		array.Each(func(k php.Key, _ php.Value) bool {
			if k.IsInt {
				fmt.Fprintf(out, "i:%d;", k.Int)
			} else {
				writeSerialString(out, k.Str)
			}
			// The raw slot, not what Each dereferenced: whether the element is
			// a reference is exactly what decides between writing it again and
			// writing where it first appeared.
			item, _ := array.Raw(k)
			if err := in.serializeValue(ctx, unit, out, item, state, depth); err != nil {
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
		return in.serializeObject(ctx, unit, out, value.Object(), state, depth)
	default:
		return Raise("Exception", "Serialization of '%s' is not allowed", phpTypeName(value))
	}
	return nil
}

func (in *Interp) serializeObject(ctx *php.Ctx, unit *Unit, out *strings.Builder,
	object *php.Object, state *serialState, depth int) error {
	if at, written := state.seen[object]; written {
		// The same object again: PHP writes where it first appeared rather
		// than a second copy, which is also what terminates a cycle.
		fmt.Fprintf(out, "r:%d;", at)
		return nil
	}
	if class, known := unit.findClass(object.ClassName); known &&
		class.decl.GetNotSerializable() {
		// A handful of the engine's classes hold state that has no written
		// form, and it says so rather than writing an empty object.
		return Raise("Exception", "Serialization of '%s' is not allowed",
			php.DisplayName(object.ClassName))
	}
	if state.seen == nil {
		state.seen = map[*php.Object]int{}
	}
	state.seen[object] = state.n
	if object.EnumCase != "" {
		// An enum case serializes as its name, not its state: the case itself
		// is the identity.
		writeSerialTagged(out, "E", object.ClassName+":"+object.EnumCase)
		return nil
	}
	class, known := unit.findClass(object.ClassName)

	// __serialize replaces the whole state with the array it returns, and the
	// result is written with the O: form all the same.
	if known {
		if body, declaring, found := unit.resolveMethod(class, "__serialize"); found {
			described, err := in.invoke(ctx, unit, body, object, declaring, class, nil, depth)
			if err != nil {
				return err
			}
			if described.Deref().Kind() != php.KindArray {
				return Raise("TypeError", "%s::__serialize() must return an array",
					object.ClassName)
			}
			writeSerialHeader(out, object.ClassName, described.Deref().Array().Len())
			var failed error
			described.Deref().Array().Each(func(k php.Key, item php.Value) bool {
				if k.IsInt {
					fmt.Fprintf(out, "i:%d;", k.Int)
				} else {
					writeSerialString(out, k.Str)
				}
				if err := in.serializeValue(ctx, unit, out, item, state, depth); err != nil {
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
		// The deprecated Serializable interface: the class writes its own
		// payload and the result is tagged `C:` rather than `O:`. __serialize
		// wins when a class has both, which is why this comes after it. A
		// serialize() that answers null writes a plain null instead.
		if unit.instanceOf(class, "Serializable") {
			if body, declaring, found := unit.resolveMethod(class, "serialize"); found {
				payload, err := in.invoke(ctx, unit, body, object, declaring, class, nil, depth)
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
		}
	}

	// __sleep is the older hook: it does not replace the state, it *selects*
	// which properties of it are written, in the order it returns them.
	var keep []string
	if known {
		if body, declaring, found := unit.resolveMethod(class, "__sleep"); found {
			selected, err := in.invoke(ctx, unit, body, object, declaring, class, nil, depth)
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
			// __sleep may have touched the object and so produced its state.
			// For a proxy that state lives in the instance, and it is the
			// instance's properties that get written from here on.
			if object.Lazy != nil && object.Lazy.Instance != nil {
				object = object.Lazy.Instance
			}
		}
	}

	var names []string
	var values []php.Value
	if keep != nil {
		for _, name := range keep {
			item, ok := object.Props.Get(php.StringKey(name))
			if !ok || item.Deref().Kind() == php.KindUndef {
				// A declared property that simply holds nothing yet is left
				// out without complaint: the name exists, it is the value that
				// does not. Only a name the class never declared earns the
				// warning.
				if _, declared := object.Declared[name]; !declared {
					ctx.Warn("serialize(): %q returned as member variable from "+
						"__sleep() but does not exist", name)
				}
				continue
			}
			names = append(names, serialPropertyName(unit, class, known, name))
			values = append(values, item)
		}
	} else {
		object.Props.Each(func(k php.Key, item php.Value) bool {
			if item.Deref().Kind() == php.KindUndef {
				return true
			}
			names = append(names, serialPropertyName(unit, class, known, k.String()))
			values = append(values, item)
			return true
		})
	}
	writeSerialHeader(out, object.ClassName, len(names))
	for i, name := range names {
		writeSerialString(out, name)
		if err := in.serializeValue(ctx, unit, out, values[i], state, depth); err != nil {
			return err
		}
	}
	out.WriteString("}")
	return nil
}

// writeSerialHeader writes the `O:<len>:"<class>":<count>:{` an object opens
// with. It differs from a plain string only in what follows the closing quote.
func writeSerialHeader(out *strings.Builder, className string, count int) {
	fmt.Fprintf(out, "O:%d:\"", len(className))
	out.WriteString(className)
	fmt.Fprintf(out, "\":%d:{", count)
}

// serialPropertyName mangles a non-public property the way the engine stores
// it: protected becomes "\0*\0name" and private "\0Class\0name". The mangling
// is what makes an unserialized private property land back in the right class.
func serialPropertyName(unit *Unit, class *Class, known bool, name string) string {
	if !known {
		return name
	}
	prop, ok := unit.propertyDecl(class, name)
	if !ok {
		return name
	}
	switch prop.GetVisibility() {
	case "protected":
		return "\x00*\x00" + name
	case "private":
		return "\x00" + prop.GetDeclaringClass() + "\x00" + name
	}
	return name
}

func writeSerialString(out *strings.Builder, s string) {
	writeSerialTagged(out, "s", s)
}

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
	return strconv.FormatFloat(number, 'G', -1, 64)
}

// serialReader walks a serialized string. Every parse step reports success
// rather than failing, since unserialize answers false for malformed input.
type serialReader struct {
	text string
	at   int
	// failedAt is where the innermost value that could not be read started,
	// which is the offset PHP reports. It is written once so that an outer
	// container unwinding over it does not replace it with its own start.
	failedAt int
	// truncated is set when a read failed because the input ran out rather
	// than because it held something unreadable. The two are reported
	// differently: an unreadable byte is blamed on the offset the value it
	// belongs to started at, while running off the end is blamed on the end.
	truncated bool
	// refused is set when the text names a class the engine will not
	// reconstruct. It is not a malformed input, so it is reported as its own
	// exception rather than as an offset.
	refused error
	// values are the values read so far, indexed by the position the writer
	// numbered them with -- so `r:2;` is values[2]. Index zero is unused, since
	// the numbering starts at one. A key is not a value and takes no position.
	values []php.Value
	// slot is the position the value now being read was given, so an object
	// can register itself before its own properties are read and a
	// self-reference inside them resolves.
	slot int
}

func (r *serialReader) accept(prefix string) bool {
	if strings.HasPrefix(r.text[r.at:], prefix) {
		r.at += len(prefix)
		return true
	}
	return false
}

// until reads up to the next occurrence of one byte, consuming it.
func (r *serialReader) until(sep byte) (string, bool) {
	end := strings.IndexByte(r.text[r.at:], sep)
	if end < 0 {
		r.truncated = true
		return "", false
	}
	s := r.text[r.at : r.at+end]
	r.at += end + 1
	return s, true
}

// readString reads the `<len>:"<bytes>";`-shaped body the string forms share.
// The length is in bytes and is trusted over the closing quote, which is what
// lets a serialized string contain quotes.
func (r *serialReader) readString() (string, bool) {
	return r.readCounted(';')
}

// readCounted reads `<len>:"<bytes>"` followed by one terminator byte. The
// length delimits the bytes, which is what lets a serialized string hold a
// quote; the terminator differs between a value and a class name.
func (r *serialReader) readCounted(terminator byte) (string, bool) {
	// A length that overruns the text is not the text running out: the count
	// is wrong, and the engine blames the count rather than the end.
	start := r.at
	digits, ok := r.until(':')
	if !ok {
		return "", false
	}
	length, err := strconv.Atoi(digits)
	if err != nil || length < 0 {
		return "", false
	}
	if !r.accept(`"`) {
		r.truncated = r.at >= len(r.text)
		return "", false
	}
	if r.at+length > len(r.text) {
		r.failedAt = start
		return "", false
	}
	s := r.text[r.at : r.at+length]
	r.at += length
	if !r.accept(`"` + string(terminator)) {
		if r.at+1 >= len(r.text) {
			r.truncated = true
			return "", false
		}
		// The count said the bytes end here and they do not. What is blamed is
		// the byte that should have closed the string -- or, when the quote is
		// there and only the terminator is wrong, the one after it.
		r.failedAt = r.at
		if r.text[r.at] == '"' {
			r.failedAt = r.at + 1
		}
		return "", false
	}
	return s, true
}

func (in *Interp) unserializeValue(ctx *php.Ctx, unit *Unit, r *serialReader, depth int) (php.Value, bool) {
	// A failure is reported at the offset the *value* started, not wherever
	// the scan gave up inside it.
	start := r.at
	if len(r.values) == 0 {
		r.values = append(r.values, php.Undef())
	}
	r.values = append(r.values, php.Undef())
	slot, outer := len(r.values)-1, r.slot
	r.slot = slot
	value, ok := in.unserializeOne(ctx, unit, r, depth)
	r.slot = outer
	if ok {
		r.values[slot] = value
	}
	if !ok && r.failedAt < 0 {
		r.failedAt = start
	}
	return value, ok
}

func (in *Interp) unserializeOne(ctx *php.Ctx, unit *Unit, r *serialReader, depth int) (php.Value, bool) {
	switch {
	case r.accept("r:"):
		// The same object again, written as where it first appeared.
		body, ok := r.until(';')
		if !ok {
			return php.Undef(), false
		}
		at, err := strconv.Atoi(body)
		if err != nil || at <= 0 || at >= len(r.values) {
			return php.Undef(), false
		}
		return r.values[at], true
	case r.accept("R:"):
		// The same *reference* again. The two slots have to share one box, so
		// the value at that position is boxed if it is not already and both
		// ends point at it.
		body, ok := r.until(';')
		if !ok {
			return php.Undef(), false
		}
		at, err := strconv.Atoi(body)
		if err != nil || at <= 0 || at >= len(r.values) {
			return php.Undef(), false
		}
		ref, boxed := r.values[at].RefOf()
		if !boxed {
			ref = php.NewRef(r.values[at])
			r.values[at] = php.MakeRef(ref)
		}
		return php.MakeRef(ref), true
	case r.accept("N;"):
		return php.Null(), true
	case r.accept("b:"):
		body, ok := r.until(';')
		return php.Bool(body == "1"), ok
	case r.accept("i:"):
		body, ok := r.until(';')
		if !ok {
			return php.Undef(), false
		}
		number, err := strconv.ParseInt(body, 10, 64)
		return php.Int(number), err == nil
	case r.accept("d:"):
		body, ok := r.until(';')
		if !ok {
			return php.Undef(), false
		}
		switch body {
		case "NAN":
			return php.Float(math.NaN()), true
		case "INF":
			return php.Float(math.Inf(1)), true
		case "-INF":
			return php.Float(math.Inf(-1)), true
		}
		number, err := strconv.ParseFloat(body, 64)
		return php.Float(number), err == nil
	case r.accept("s:"):
		s, ok := r.readString()
		return php.String(s), ok
	case r.accept("E:"):
		// An enum case, written as `E:<n>:"Class:Case";`. Every way this can
		// go wrong is reported before the offset is, and *where* it is
		// reported from differs: a name the reader cannot make sense of fails
		// at the value's own start, while a name it resolved and then could
		// not find a case for fails at the end of what it read.
		s, ok := r.readString()
		if !ok {
			return php.Undef(), false
		}
		className, caseName, found := strings.Cut(s, ":")
		if !found {
			ctx.Warn("unserialize(): Invalid enum name '%s' (missing colon)", s)
			return php.Undef(), false
		}
		class, ok := unit.findClass(className)
		if !ok {
			ctx.Warn("unserialize(): Class '%s' not found", className)
			return php.Undef(), false
		}
		if class.decl.GetKind() != pb.Class_KIND_ENUM {
			ctx.Warn("unserialize(): Class '%s' is not an enum", php.DisplayName(class.Name))
			return php.Undef(), false
		}
		decl, ok := unit.enumCaseDecl(class, caseName)
		if !ok {
			// The name was read in full, so the offset is the end of it. A
			// constant that exists but is not a case is told apart from one
			// that does not exist at all.
			r.failedAt = r.at
			if _, _, isConstant := unit.findClassConstant(class, caseName); isConstant {
				ctx.Warn("unserialize(): %s::%s is not an enum case",
					php.DisplayName(class.Name), caseName)
			} else {
				ctx.Warn("unserialize(): Undefined constant %s::%s",
					php.DisplayName(class.Name), caseName)
			}
			return php.Undef(), false
		}
		value, err := in.enumCase(ctx, unit, class, decl, depth)
		return value, err == nil
	case r.accept("a:"):
		return in.unserializeArray(ctx, unit, r, depth)
	case r.accept("O:"):
		return in.unserializeObject(ctx, unit, r, depth)
	case r.accept("C:"):
		return in.unserializeSerializable(ctx, unit, r, depth)
	}
	// Nothing matched. With text left, one of these bytes is unreadable and
	// the value's own start is what gets blamed; with none left, the text
	// simply stopped where a value was due.
	r.truncated = r.atEnd()
	return php.Undef(), false
}

// unserializeCount reads the `<n>:{` header the array and object forms share.
func (r *serialReader) unserializeCount() (int, bool) {
	digits, ok := r.until(':')
	if !ok {
		return 0, false
	}
	count, err := strconv.Atoi(digits)
	if err != nil || count < 0 || !r.accept("{") {
		return 0, false
	}
	return count, true
}

func (in *Interp) unserializeArray(ctx *php.Ctx, unit *Unit, r *serialReader, depth int) (php.Value, bool) {
	count, ok := r.unserializeCount()
	if !ok {
		return php.Undef(), false
	}
	array := php.NewArray()
	for i := 0; i < count; i++ {
		if r.closes() {
			// The header promised more elements than the braces hold. Reaching
			// the closing brace early is a different failure from running out
			// of text, and the engine says so before reporting the offset --
			// which is the brace, not the end.
			ctx.Warn("unserialize(): Unexpected end of serialized data")
			r.failedAt = r.at
			return php.Undef(), false
		}
		key, ok := in.unserializeOne(ctx, unit, r, depth)
		if !ok {
			return php.Undef(), false
		}
		value, ok := in.unserializeValue(ctx, unit, r, depth)
		if !ok {
			return php.Undef(), false
		}
		array.Set(php.ArrayKey(nil, key), value)
	}
	if !r.accept("}") {
		r.truncated = r.atEnd()
		return php.Undef(), false
	}
	return php.Arr(array), true
}

// unserializeSerializable reads the `C:` form the deprecated Serializable
// interface writes. Unlike `O:`, the payload is opaque: the class is built
// without its constructor and handed the bytes through its own unserialize().
func (in *Interp) unserializeSerializable(ctx *php.Ctx, unit *Unit, r *serialReader,
	depth int) (php.Value, bool) {
	className, ok := r.readCounted(':')
	if !ok {
		return php.Undef(), false
	}
	class, known := unit.findClass(className)
	if !known {
		return php.Undef(), false
	}
	if class.decl.GetNotSerializable() {
		r.refused = Raise("Exception", "Unserialization of '%s' is not allowed",
			php.DisplayName(class.Name))
		return php.Undef(), false
	}
	size, ok := r.unserializeCount()
	if !ok {
		return php.Undef(), false
	}
	if r.at+size > len(r.text) {
		r.truncated = true
		return php.Undef(), false
	}
	payload := r.text[r.at : r.at+size]
	r.at += size
	if !r.accept("}") {
		return php.Undef(), false
	}
	object := unit.instantiate(class)
	body, declaring, found := unit.resolveMethod(class, "unserialize")
	if !found {
		return php.Undef(), false
	}
	if _, err := in.invoke(ctx, unit, body, object, declaring, class,
		[]php.Value{php.String(payload)}, depth); err != nil {
		r.refused = err
		return php.Undef(), false
	}
	return php.Obj(object), true
}

func (in *Interp) unserializeObject(ctx *php.Ctx, unit *Unit, r *serialReader, depth int) (php.Value, bool) {
	// The class name is a counted string like any other, except that it is
	// followed by ':' rather than the ';' a value ends with.
	className, ok := r.readCounted(':')
	if !ok {
		return php.Undef(), false
	}
	count, ok := r.unserializeCount()
	if !ok {
		return php.Undef(), false
	}
	class, known := unit.findClass(className)
	if !known {
		return php.Undef(), false
	}
	if class.decl.GetNotSerializable() {
		r.refused = Raise("Exception", "Unserialization of '%s' is not allowed",
			php.DisplayName(class.Name))
		return php.Undef(), false
	}
	if err := instantiable(class); err != nil {
		// `O:` asks for an instance, and an enum or an abstract class has none
		// to give -- the refusal comes before any property is read.
		r.refused = err
		return php.Undef(), false
	}
	object := unit.instantiate(class)
	// Register the object before reading its properties: one of them may be
	// the object itself, written as a back-reference to this position.
	if r.slot > 0 && r.slot < len(r.values) {
		r.values[r.slot] = php.Obj(object)
	}
	state := php.NewArray()
	for i := 0; i < count; i++ {
		key, ok := in.unserializeOne(ctx, unit, r, depth)
		if !ok {
			return php.Undef(), false
		}
		// A virtual property has no storage, so there is nowhere to put a
		// value serialized for it. The engine says so and then fails the whole
		// unserialize at the offset the value starts at.
		if declared, found := unit.propertyDecl(class, unmangleProperty(key.String())); found &&
			declared.GetVirtual() {
			ctx.Warn("unserialize(): Cannot unserialize value for virtual property %s::$%s",
				php.DisplayName(class.Name), declared.GetName())
			r.failedAt = r.at
			return php.Undef(), false
		}
		value, ok := in.unserializeValue(ctx, unit, r, depth)
		if !ok {
			return php.Undef(), false
		}
		state.Set(php.ArrayKey(nil, key), value)
	}
	if !r.accept("}") {
		return php.Undef(), false
	}

	// __unserialize takes the state as an array and populates the object
	// itself; without it the entries are written back as properties, with the
	// visibility mangling undone.
	if body, declaring, found := unit.resolveMethod(class, "__unserialize"); found {
		if _, err := in.invoke(ctx, unit, body, object, declaring, class,
			[]php.Value{php.Arr(state)}, depth); err != nil {
			return php.Undef(), false
		}
		return php.Obj(object), true
	}
	refused := false
	state.Each(func(k php.Key, v php.Value) bool {
		name := unmangleProperty(k.String())
		// Writing a property the class does not declare is creating a dynamic
		// one, and a class that forbids those refuses it here as it would
		// anywhere else -- a readonly class among them.
		if _, declared := unit.propertyDecl(class, name); !declared {
			if class.decl.GetNoDynamicProperties() {
				r.refused = in.throwError(ctx, unit, "Error",
					"Cannot create dynamic property %s::$%s",
					php.DisplayName(class.Name), name)
				refused = true
				return false
			}
			if !class.decl.GetAllowDynamicProperties() {
				ctx.Deprecated("Creation of dynamic property %s::$%s is deprecated",
					php.DisplayName(class.Name), name)
			}
		}
		object.Set(name, v)
		return true
	})
	if refused {
		return php.Undef(), false
	}
	if body, declaring, found := unit.resolveMethod(class, "__wakeup"); found {
		if _, err := in.invoke(ctx, unit, body, object, declaring, class, nil, depth); err != nil {
			return php.Undef(), false
		}
	}
	return php.Obj(object), true
}

// unmangleProperty undoes the "\0*\0name" and "\0Class\0name" forms.
func unmangleProperty(name string) string {
	if !strings.HasPrefix(name, "\x00") {
		return name
	}
	if at := strings.IndexByte(name[1:], 0); at >= 0 {
		return name[at+2:]
	}
	return name
}

// serializesLazily reports whether serialize() must leave a lazy object
// uninitialized: either the caller asked for that when the object was made, or
// the class supplies __serialize and so decides for itself what to write.
func (u *Unit) serializesLazily(value php.Value) bool {
	object := value.Deref().Object()
	if object == nil || object.Lazy == nil {
		return false
	}
	if object.Lazy.SkipSerialize {
		return true
	}
	// __serialize decides for itself what to write, so the state is never
	// wanted. __sleep does not: it answers with property *names*, and the
	// values behind them still have to be produced.
	return u.hasMagicNamed(object.ClassName, "__serialize")
}

// atEnd reports whether the reader has consumed the whole text.
func (r *serialReader) atEnd() bool { return r.at >= len(r.text) }

// closes reports whether the next byte ends the container being read, which is
// how a count larger than the braces hold is noticed.
func (r *serialReader) closes() bool {
	return r.at < len(r.text) && r.text[r.at] == '}'
}
