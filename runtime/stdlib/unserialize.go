package stdlib

import (
	"math"
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// `unserialize`. The reader is the mirror of the writer in serialize.go, and
// everything it needs about a class it asks the Ctx for: whether the name is
// declared, whether `new` would refuse it, an instance laid out **without** its
// constructor, an enum's case, and whether a property is one there is anywhere
// to put a value.
//
// Every parse step reports success rather than failing, because unserialize
// answers `false` for malformed input and says where it gave up.

func init() {
	php.RegisterBuiltin("unserialize", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.Bool(false), nil
		}
		text := args[0].Deref().String()
		if text == "" {
			// **The empty string is not malformed input**, it is no input: the
			// engine answers false without saying anything, where every other
			// unreadable text earns an offset.
			return php.Bool(false), nil
		}
		sessions.unserializeDepth++
		defer func() { sessions.unserializeDepth-- }()
		reader := &serialReader{ctx: ctx, text: text, failedAt: -1, fn: "unserialize"}
		value, ok := reader.value()
		if reader.refused != nil {
			return php.Undef(), reader.refused
		}
		if !ok {
			at := reader.failedAt
			if reader.truncated {
				// Nothing in the text is wrong; there is simply not enough of
				// it, so the engine points past the end of what it read.
				at = len(text)
			}
			ctx.Warn("unserialize(): Error at offset %d of %d bytes", at, len(text))
			return php.Bool(false), nil
		}
		if reader.at < len(text) {
			// A complete value followed by something else. The value still
			// stands -- the engine hands it back and only says so.
			ctx.Warn("unserialize(): Extra data starting at offset %d of %d bytes",
				reader.at, len(text))
		}
		reader.pinUnlessSole(value)
		return value, nil
	})
}

// serialReader walks a serialized string.
type serialReader struct {
	ctx  *php.Ctx
	text string
	at   int
	// fn is the function a warning names: unserialize, or the container
	// method whose session this is.
	fn string
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
	// boxes reaches the **storage** a position ended up in, for the one case
	// that needs it: `R:` says two positions share a box, and by then the
	// earlier one has already been copied into its container. Boxing the
	// recorded *value* would leave the container holding an unrelated copy, so
	// the container registers how to box its own slot instead.
	boxes []func() *php.Ref
	// slot is the position the value now being read was given, so an object can
	// register itself before its own properties are read and a self-reference
	// inside them resolves.
	slot int
	// rootMade is set when the value at position one -- the result -- is an
	// object `O:` made here, and backRef when the text used `r:` or `R:`
	// anywhere. pinUnlessSole reads both.
	rootMade, backRef bool
	// sharedRef is set when the text used `R:`, a reference rather than an
	// object back-reference.
	sharedRef bool
	// rootCustom is set when the result was made by a `C:` payload, whose
	// own unserialize() is then one more hook that may keep it.
	rootCustom bool
	// foreign are the positions a container's joined session numbered
	// (sessionReader): the engine reads them as temporaries whose
	// back-references it resolves in ways not modelled here.
	foreign [][2]int
}

// foreignRef refuses a back-reference to a position a joined session read.
func (r *serialReader) foreignRef(at int) bool {
	for _, span := range r.foreign {
		if at >= span[0] && at < span[1] {
			r.refused = php.Unsupported("unserialize() back-reference into what a container's own unserialize() read")
			return true
		}
	}
	return false
}

// pinUnlessSole keeps the result's handle number from ever being given back
// unless the result is **held by nothing but the caller**.
//
// The compiler treats `unserialize` as returning a fresh object, so
// `var_dump(unserialize($s))` hands the number back once var_dump returns --
// the engine frees the object there and the next `new` reuses it (measured;
// readonly_props/serialization). That is true only of an object made here that
// nothing else reached:
//
//   - `__wakeup` or `__unserialize` may store `$this` (measured: the next
//     object gets a fresh number);
//   - a back-reference can make the object hold itself, and a cycle waits for
//     the collector (measured);
//   - an enum case is a singleton, never freed;
//   - `__destruct` would run at that point, which is a timing question, not a
//     numbering one.
//
// Any of those pins the handle instead; a child object is never released by
// this, only the root.
func (r *serialReader) pinUnlessSole(value php.Value) {
	root := value.Object()
	if root == nil {
		return
	}
	if r.rootMade && !r.backRef {
		sole := true
		hooks := []string{"__wakeup", "__unserialize", "__destruct"}
		if r.rootCustom {
			hooks = append(hooks, "unserialize")
		}
		for _, hook := range hooks {
			// A container's own hook keeps nothing of the object.
			if r.ctx.HasMethod(root.ClassName, hook) && !php.SplNativeHook(r.ctx, root.ClassName, hook) {
				sole = false
			}
		}
		if sole {
			return
		}
	}
	root.PinHandle()
}

func (r *serialReader) accept(prefix string) bool {
	if strings.HasPrefix(r.text[r.at:], prefix) {
		r.at += len(prefix)
		return true
	}
	return false
}

// atEnd reports whether the reader has consumed the whole text.
func (r *serialReader) atEnd() bool { return r.at >= len(r.text) }

// closes reports whether the next byte ends the container being read, which is
// how a count larger than the braces hold is noticed.
func (r *serialReader) closes() bool {
	return r.at < len(r.text) && r.text[r.at] == '}'
}

// until reads up to the next occurrence of one byte and consumes it.
func (r *serialReader) until(stop byte) (string, bool) {
	at := strings.IndexByte(r.text[r.at:], stop)
	if at < 0 {
		r.truncated = true
		return "", false
	}
	s := r.text[r.at : r.at+at]
	r.at += at + 1
	return s, true
}

// readString reads the `<len>:"<bytes>";` a string value is written as.
func (r *serialReader) readString() (string, bool) { return r.readCounted(';') }

// readCounted reads `<len>:"<bytes>"` followed by one terminator byte. The
// length delimits the bytes, which is what lets a serialized string hold a
// quote; the terminator differs between a value and a class name.
func (r *serialReader) readCounted(terminator byte) (string, bool) {
	// A length that overruns the text is not the text running out: the count is
	// wrong, and the engine blames the count rather than the end.
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

// count reads the `<n>:{` header the array and object forms share.
func (r *serialReader) count() (int, bool) {
	digits, ok := r.until(':')
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 0 || !r.accept("{") {
		return 0, false
	}
	return n, true
}

// value reads one **numbered** value: it takes a position before reading, so a
// back-reference inside it can already name it.
func (r *serialReader) value() (php.Value, bool) {
	// A failure is reported at the offset the *value* started, not wherever the
	// scan gave up inside it.
	start := r.at
	if len(r.values) == 0 {
		r.values = append(r.values, php.Undef())
		r.boxes = append(r.boxes, nil)
	}
	r.values = append(r.values, php.Undef())
	r.boxes = append(r.boxes, nil)
	slot, outer := len(r.values)-1, r.slot
	r.slot = slot
	value, ok := r.one()
	r.slot = outer
	if ok {
		r.values[slot] = value
	}
	if !ok && r.failedAt < 0 {
		r.failedAt = start
	}
	return value, ok
}

func (r *serialReader) one() (php.Value, bool) {
	switch {
	case r.accept("r:"):
		// The same object again, written as where it first appeared.
		r.backRef = true
		at, ok := r.position()
		if !ok || r.foreignRef(at) {
			return php.Undef(), false
		}
		// A value still being read -- the array this element belongs to --
		// has nothing to point at yet, and the engine fails the whole
		// unserialize rather than hand back a null. An object registers
		// itself before its properties, so `r:` to one is fine.
		if r.values[at].IsUndef() {
			// Blamed just past the back-reference, not at its start.
			r.failedAt = r.at
			return php.Undef(), false
		}
		return r.values[at], true
	case r.accept("R:"):
		// The same *reference* again. The two slots have to share one box, so
		// the value at that position is boxed if it is not already and both
		// ends point at it.
		r.backRef, r.sharedRef = true, true
		at, ok := r.position()
		if !ok || r.foreignRef(at) {
			return php.Undef(), false
		}
		// **Marked shared**, because the reference is one the *program* can
		// see: assigning the unserialized array copies it, and a copy keeps
		// only the boxes the program asked for -- an unmarked one flattens back
		// to two separate values.
		if ref, boxed := r.values[at].RefOf(); boxed {
			return php.Shared(php.MakeRef(ref)), true
		}
		// **Box the storage, not the record.** The value at that position is
		// already sitting in an array element or a property, and a box made
		// around the copy here would not be the one the container holds.
		if at < len(r.boxes) && r.boxes[at] != nil {
			ref := r.boxes[at]()
			r.values[at] = php.Shared(php.MakeRef(ref))
			return php.Shared(php.MakeRef(ref)), true
		}
		ref := php.NewRef(r.values[at])
		r.values[at] = php.Shared(php.MakeRef(ref))
		return php.Shared(php.MakeRef(ref)), true
	case r.accept("N;"):
		return php.Null(), true
	case r.accept("b:"):
		body, ok := r.until(';')
		// **A scalar with no terminator is unreadable, not cut short**: the
		// engine's scanner matches `i:<digits>;` whole or not at all, so it
		// blames the value's start rather than the end (probed: `i:0` fails
		// at offset 0 of 3).
		r.truncated = false
		return php.Bool(body == "1"), ok
	case r.accept("i:"):
		body, ok := r.until(';')
		if !ok {
			r.truncated = false
			return php.Undef(), false
		}
		number, err := strconv.ParseInt(body, 10, 64)
		return php.Int(number), err == nil
	case r.accept("d:"):
		body, ok := r.until(';')
		if !ok {
			r.truncated = false
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
		return r.enumCase()
	case r.accept("a:"):
		return r.array()
	case r.accept("O:"):
		return r.object()
	case r.accept("C:"):
		return r.serializable()
	}
	// Nothing matched. With text left, one of these bytes is unreadable and the
	// value's own start is what gets blamed; with none left, the text simply
	// stopped where a value was due.
	r.truncated = r.atEnd()
	return php.Undef(), false
}

// position reads the `<n>;` a back-reference names, bounded by what has been
// read so far.
func (r *serialReader) position() (int, bool) {
	body, ok := r.until(';')
	if !ok {
		return 0, false
	}
	at, err := strconv.Atoi(body)
	if err != nil || at <= 0 || at >= len(r.values) {
		return 0, false
	}
	return at, true
}

// enumCase reads `E:<n>:"Class:Case";`. Every way this can go wrong is reported
// before the offset is, and *where* it is reported from differs: a name the
// reader cannot make sense of fails at the value's own start, while a name it
// resolved and then could not find a case for fails at the end of what it read.
func (r *serialReader) enumCase() (php.Value, bool) {
	s, ok := r.readString()
	if !ok {
		return php.Undef(), false
	}
	className, caseName, found := strings.Cut(s, ":")
	if !found {
		r.ctx.Warn("%s(): Invalid enum name '%s' (missing colon)", r.fn, s)
		return php.Undef(), false
	}
	if !r.ctx.ClassFactsOf(className).Known {
		r.ctx.Warn("%s(): Class '%s' not found", r.fn, className)
		return php.Undef(), false
	}
	value, how := r.ctx.EnumCaseOf(className, caseName)
	switch how {
	case php.EnumCaseFound:
		return value, true
	case php.EnumNotAnEnum:
		r.ctx.Warn("%s(): Class '%s' is not an enum", r.fn, php.DisplayName(className))
		return php.Undef(), false
	}
	// The name was read in full, so the offset is the end of it. A constant that
	// exists but is not a case is told apart from one that does not exist.
	r.failedAt = r.at
	if how == php.EnumNotACase {
		r.ctx.Warn("%s(): %s::%s is not an enum case", r.fn, php.DisplayName(className), caseName)
	} else {
		r.ctx.Warn("%s(): Undefined constant %s::%s", r.fn, php.DisplayName(className), caseName)
	}
	return php.Undef(), false
}

func (r *serialReader) array() (php.Value, bool) {
	n, ok := r.count()
	if !ok {
		return php.Undef(), false
	}
	array := php.NewArray()
	for i := 0; i < n; i++ {
		if r.closes() {
			// The header promised more elements than the braces hold. Reaching
			// the closing brace early is a different failure from running out
			// of text, and the engine says so before reporting the offset --
			// which is the brace, not the end.
			r.ctx.Warn("%s(): Unexpected end of serialized data", r.fn)
			r.failedAt = r.at
			return php.Undef(), false
		}
		key, ok := r.one()
		if !ok {
			return php.Undef(), false
		}
		at := len(r.values)
		value, ok := r.value()
		if !ok {
			return php.Undef(), false
		}
		slot := php.ArrayKey(nil, key)
		array.Set(slot, value)
		// Where this element ended up, so a later `R:` naming it boxes the
		// array's own slot rather than a copy of what went into it.
		if at < len(r.boxes) {
			r.boxes[at] = func() *php.Ref { return array.RefAt(slot) }
		}
	}
	if !r.accept("}") {
		r.truncated = r.atEnd()
		return php.Undef(), false
	}
	return php.Arr(array), true
}

// newInstance lays out an object of a class the text named, applying the two
// refusals that come before any property is read.
func (r *serialReader) newInstance(className string) (*php.Object, bool) {
	facts := r.ctx.ClassFactsOf(className)
	if !facts.Known {
		// **Not malformed input**: the engine asks the autoloaders, then
		// makes a class it still cannot find an `__PHP_Incomplete_Class`.
		// One the engine has and this runtime does not -- DateInterval
		// (bug70121) -- is its own, and refused.
		return r.incompleteInstance(className)
	}
	if facts.NotSerializable {
		r.refused = php.Raise("Exception", "Unserialization of '%s' is not allowed",
			php.DisplayName(className))
		return nil, false
	}
	if facts.Uninstantiable != nil {
		// `O:` asks for an instance, and an enum or an abstract class has none
		// to give.
		r.refused = facts.Uninstantiable
		return nil, false
	}
	object, made, err := r.ctx.NewInstance(className)
	if err != nil {
		r.refused = err
		return nil, false
	}
	if !made {
		r.refused = php.Unsupported("unserialize of an object of class %s", className)
		return nil, false
	}
	return object, true
}

func (r *serialReader) object() (php.Value, bool) {
	// The class name is a counted string like any other, except that it is
	// followed by ':' rather than the ';' a value ends with.
	className, ok := r.readCounted(':')
	if !ok {
		return php.Undef(), false
	}
	n, ok := r.count()
	if !ok {
		return php.Undef(), false
	}
	object, ok := r.newInstance(className)
	if !ok {
		return php.Undef(), false
	}
	// **The class made, not the name written**: an alias -- one an
	// autoloader has just made with class_alias, say -- names a class whose
	// declarations are under its own name, and asking under the alias took
	// every declared property for a dynamic one.
	if !php.IsIncomplete(object) {
		className = object.ClassName
	}
	// Register the object before reading its properties: one of them may be the
	// object itself, written as a back-reference to this position.
	if r.slot > 0 && r.slot < len(r.values) {
		r.values[r.slot] = php.Obj(object)
	}
	if r.slot == 1 {
		r.rootMade = true
	}
	state := php.NewArray()
	for i := 0; i < n; i++ {
		key, ok := r.one()
		if !ok {
			return php.Undef(), false
		}
		// A **virtual** property has no storage, so there is nowhere to put a
		// value serialized for it. The engine says so and then fails the whole
		// unserialize at the offset the value starts at.
		name := unmangleSerialName(key.String())
		if _, virtual := r.ctx.PropertyFactsOf(className, name); virtual {
			r.ctx.Warn("%s(): Cannot unserialize value for virtual property %s::$%s", r.fn,
				php.DisplayName(className), name)
			r.failedAt = r.at
			return php.Undef(), false
		}
		value, ok := r.value()
		if !ok {
			return php.Undef(), false
		}
		state.Set(php.ArrayKey(nil, key), value)
	}
	if !r.accept("}") {
		return php.Undef(), false
	}

	// An incomplete object keeps the entries as they were written, mangled
	// keys included -- it has no declarations to unmangle them against -- and
	// with no dynamic-property deprecation (probed).
	if php.IsIncomplete(object) {
		state.EachRaw(func(k php.Key, v php.Value) bool {
			if !k.IsInt && k.Str == php.IncompleteNameProperty {
				// A written name replaces the one unserialize put first.
				object.Set(php.IncompleteNameProperty, v)
				return true
			}
			object.PropSet(k, v)
			return true
		})
		return php.Obj(object), true
	}
	// `__unserialize` takes the state as an array and populates the object
	// itself; without it the entries are written back as properties, with the
	// visibility mangling undone.
	if r.ctx.HasMethod(className, "__unserialize") {
		if _, err := lockedHook(func() (php.Value, error) { return callOn2(r.ctx, object, "__unserialize", php.Arr(state)) }); err != nil {
			r.refused = err
			return php.Undef(), false
		}
		return php.Obj(object), true
	}
	facts := r.ctx.ClassFactsOf(className)
	refused := false
	state.Each(func(k php.Key, v php.Value) bool {
		name := unmangleSerialName(k.String())
		// Writing a property the class does not declare is creating a dynamic
		// one, and a class that forbids those refuses it here as it would
		// anywhere else -- a readonly class among them.
		if declared, _ := r.ctx.PropertyFactsOf(className, name); !declared {
			if facts.NoDynamicProperties {
				r.refused = php.RaiseAt(r.ctx, "Error", "Cannot create dynamic property %s::$%s",
					php.DisplayName(className), name)
				refused = true
				return false
			}
			if !facts.AllowDynamicProperties {
				r.ctx.Deprecated("Creation of dynamic property %s::$%s is deprecated",
					php.DisplayName(className), name)
			}
		}
		// **A typed property checks what it is given**, strictly, as the
		// engine's object_common does: `Cannot assign stdClass to property
		// Exception::$previous of type ?Throwable` (bug70121).
		typ, class := "", ""
		if d, typed := object.Declared[name]; typed && d.Type != "" {
			typ, class = d.Type, d.Class
			if class == "" {
				class = className
			}
		} else {
			// An exception's own are not declared with their types here.
			typ, class = throwableSerialType(r.ctx, className, k.String(), name)
		}
		if typ != "" && !v.IsRef() {
			checked, err := php.CoerceProperty(r.ctx, v, typ, class, name, true)
			if err != nil {
				r.refused = err
				refused = true
				return false
			}
			v = checked
		}
		// **Into the slot the key names** when the object has one: an
		// exception keeps `message` as `\0*\0message`, and a shadowed private
		// lives under its mangled key, so writing the unmangled name made a
		// second property and left the real one empty.
		if _, held := object.PropGet(k); held && k.String() != name {
			object.PropSet(k, v)
			return true
		}
		object.Set(name, v)
		return true
	})
	if refused {
		return php.Undef(), false
	}
	if r.ctx.HasMethod(className, "__wakeup") {
		if _, err := lockedHook(func() (php.Value, error) { return callOn(r.ctx, object, "__wakeup") }); err != nil {
			r.refused = err
			return php.Undef(), false
		}
	}
	return php.Obj(object), true
}

// serializable reads the `C:` form the deprecated Serializable interface writes.
// Unlike `O:`, the payload is opaque: the class is built without its constructor
// and handed the bytes through its own `unserialize()`.
func (r *serialReader) serializable() (php.Value, bool) {
	className, ok := r.readCounted(':')
	if !ok {
		return php.Undef(), false
	}
	facts := r.ctx.ClassFactsOf(className)
	if !facts.Known {
		return php.Undef(), false
	}
	if facts.NotSerializable {
		r.refused = php.Raise("Exception", "Unserialization of '%s' is not allowed",
			php.DisplayName(className))
		return php.Undef(), false
	}
	size, ok := r.count()
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
	if !r.ctx.HasMethod(className, "unserialize") {
		return php.Undef(), false
	}
	object, ok := r.newInstance(className)
	if !ok {
		return php.Undef(), false
	}
	if r.slot == 1 {
		r.rootMade, r.rootCustom = true, true
	}
	if _, err := joiningReader(r, func() (php.Value, error) { return callOn2(r.ctx, object, "unserialize", php.String(payload)) }); err != nil {
		r.refused = err
		return php.Undef(), false
	}
	return php.Obj(object), true
}

// throwableSerialTypes are the typed properties Exception and Error
// declare -- message and code are untyped (probed through reflection) -- and
// whether each is private, which a serialized key names with the root.
var throwableSerialTypes = map[string]struct {
	typ     string
	private bool
}{
	"string":   {"string", true},
	"file":     {"string", false},
	"line":     {"int", false},
	"trace":    {"array", true},
	"previous": {"?Throwable", true},
}

// throwableSerialType is the declared type a serialized key reaches on an
// exception object, with the class that declares it; "" for any other key.
// Only the exact key the engine writes counts: `\0*\0line`, or
// `\0Exception\0previous` for a private one.
func throwableSerialType(ctx *php.Ctx, className, key, name string) (string, string) {
	root := php.ThrowableRootFor(ctx, className)
	t, ok := throwableSerialTypes[name]
	if root == "" || !ok {
		return "", ""
	}
	want := "\x00*\x00" + name
	if t.private {
		want = "\x00" + root + "\x00" + name
	}
	if key != want {
		return "", ""
	}
	return t.typ, root
}

// unmangleSerialName undoes the "\0*\0name" and "\0Class\0name" forms.
func unmangleSerialName(name string) string {
	if !strings.HasPrefix(name, "\x00") {
		return name
	}
	if at := strings.IndexByte(name[1:], 0); at >= 0 {
		return name[at+2:]
	}
	return name
}

// callOn2 is callOn with one argument.
func callOn2(ctx *php.Ctx, object *php.Object, method string, arg php.Value) (php.Value, error) {
	callee := php.NewArray()
	callee.Append(php.Obj(object))
	callee.Append(php.String(method))
	return ctx.Call(php.Arr(callee), []php.Value{arg})
}

// incompleteInstance is unserialize meeting a class it cannot find: the
// autoloaders run first, and a class they produce is built as usual;
// otherwise the object is an __PHP_Incomplete_Class holding the name
// (bug71841). Refused where the engine would do something this runtime does
// not model: unserialize_callback_func, a name the engine has as a class of
// its own, or one that is not a valid class name.
func (r *serialReader) incompleteInstance(className string) (*php.Object, bool) {
	refuse := func() (*php.Object, bool) {
		r.refused = php.Unsupported("unserialize of an object of class %s", className)
		return nil, false
	}
	if !validSerialClassName(className) || r.ctx.EngineHasClass(className) && !strings.EqualFold(className, php.IncompleteClass) {
		return refuse()
	}
	if callback, _ := r.ctx.IniRead("unserialize_callback_func"); callback != "" {
		return refuse()
	}
	if !strings.EqualFold(className, php.IncompleteClass) {
		loaded, err := r.ctx.AutoloadClass(className)
		if err != nil {
			r.refused = err
			return nil, false
		}
		if loaded {
			if !r.ctx.ClassFactsOf(className).Known {
				return refuse()
			}
			return r.newInstance(className)
		}
	}
	object, made, err := r.ctx.NewInstance(php.IncompleteClass)
	if err != nil {
		r.refused = err
		return nil, false
	}
	if !made || !php.IsIncomplete(object) {
		return refuse()
	}
	if !strings.EqualFold(className, php.IncompleteClass) {
		object.Set(php.IncompleteNameProperty, php.String(className))
	}
	return object, true
}

// validSerialClassName is the engine's character check on an `O:` class
// name: letters, digits, `_`, a backslash and bytes above 0x7f, a leading
// digit included (probed: `O:1:"1":0:{}` autoloads "1" and is incomplete).
// A name failing it is the engine's "Error at offset", which this path does
// not reproduce, so it is refused.
func validSerialClassName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '_' || c == '\\' || c >= 0x80 || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}
