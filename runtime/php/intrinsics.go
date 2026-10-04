package php

import (
	"errors"
	"strconv"
	"strings"
)

// Intrinsics: the essential builtin functions that drive the runtime.
//
// # What belongs here
//
// An intrinsic is a **value-level operation both consumers need** -- reading a
// variable, writing through a reference, reading a container element. It is not
// an IR opcode, which is the compiler's instruction set and belongs to
// `internal/lower`; and it is not part of the library surface, which is the
// ordinary PHP functions (`strlen`, `array_map`) the compiler links against.
//
//	IR opcode      compiler-only        `dim.get`, `local.set`
//	intrinsic      BOTH consumers       ReadDim, WriteVar, BindRef   <- here
//	library        closed, inherited    strlen, array_map
//
// Many opcodes map onto an intrinsic almost one-to-one, which is what makes the
// two easy to blur. `dim.get` is an opcode; `ReadDim` is the intrinsic under it.
//
// The test for membership is mechanical: **an intrinsic touches values and a
// Ctx, and nothing that knows what a class is.** Anything needing a class table
// -- method dispatch, visibility, magic accessors -- stays with the consumer and
// reaches this package through a hook, the way `ObjectProperties` and
// `ResolveTraversable` already do.
//
// These lived in the generated program's prologue until 2026-09-02, duplicated
// per program and invisible to the interpreter. Reading them in order to move
// them found three defects in `ReadDim` alone, which is the argument for the
// layer existing at all.

// ReadVar reads a variable.
//
// An unset one warns and yields null; a variable bound with `&` holds a
// reference box, and every read of it yields what the box points at.
func ReadVar(ctx *Ctx, v Value, name string) Value {
	if v.IsUndef() {
		ctx.WarnUndefinedVariable(name)
		return Null()
	}
	return v.Deref()
}

// WriteVar writes to a variable slot, **through** a reference if it holds one.
//
// This is the distinction IR-DESIGN.md warns is silently wrong backwards:
// `$a = $b` writes through a reference sitting in `$a`, so every name bound to
// it sees the new value, while `$a = &$b` *replaces* the slot.
func WriteVar(slot *Value, v Value) {
	if r, ok := slot.RefOf(); ok {
		r.Set(v)
		return
	}
	*slot = v
}

// BindRef makes two slots share one box, which is what `&` does. The source is
// boxed if it was not already, so both names end up pointing at the same
// reference rather than at two copies of a value.
func BindRef(target, source *Value) {
	r, ok := source.RefOf()
	if !ok {
		r = NewRef(source.Deref())
		*source = MakeRef(r)
	}
	r.AddBinding()
	*target = MakeRef(r)
}

// NoRefArg is what a ZEND_SEND_VAR_NO_REF sends: a value the compiler could
// not prove is a variable -- a call result -- to a parameter that takes a
// reference. A function that returned by reference gave a reference, which
// passes as one. Anything else is legal and pointless, and the engine says so,
// then hands the callee something nobody else can see: `array_splice(
// C::get(), 1, 1)` must not splice the array the getter returned.
func NoRefArg(ctx *Ctx, v Value) Value {
	if _, isRef := v.RefOf(); isRef {
		return v
	}
	ctx.Notice("Only variables should be passed by reference")
	return v.Copy()
}

// RefReturn is a by-reference function returning something with no variable
// behind it -- a literal, a temporary, `return $x = 5;` -- which the engine
// notices and hands back in a fresh reference. With callResult set it is a
// call's result instead, and a reference that call returned passes silently.
func RefReturn(ctx *Ctx, v Value, callResult bool) Value {
	if _, isRef := v.RefOf(); isRef && callResult {
		return v
	}
	ctx.Notice("Only variable references should be returned by reference")
	return MakeRef(NewRef(v.Deref()))
}

// AsRef is what a by-reference function returns for a variable the compiler
// promoted to a value: the reference the engine would have handed back. A
// value that is one already is returned as it is.
func AsRef(v Value) Value {
	if _, isRef := v.RefOf(); isRef {
		return v
	}
	return MakeRef(NewRef(v))
}

// RefArg passes a slot by reference, boxing it if it is not boxed yet. The
// callee's writes are visible through the caller's slot because both hold the
// same box.
func RefArg(slot *Value) Value {
	if r, ok := slot.RefOf(); ok {
		return MakeRef(r)
	}
	r := NewRef(slot.Deref())
	*slot = MakeRef(r)
	return MakeRef(r)
}

// TypeCheck answers ZEND_TYPE_CHECK: whether a value's type is one of the ones
// a bitmask names. It is what `$x === null`, `is_int()`, `is_scalar()` and
// their kin all compile to.
//
// The bit for a type is `1 << <the engine's type id>`, and the ids were
// measured rather than read off a header -- one probe per predicate:
//
//	null 2   false 4   true 8   int 16   float 32
//	string 64   array 128   object 256   resource 512
//
// `is_scalar` is 124, the union of false, true, int, float and string; and
// `$x !== null` is 1020, the **complement** of null. That last one is why this
// cannot be a "which type is it" question with a negation flag beside it.
//
// An undefined value tests as null: `$undef === null` is true, after the read's
// own warning.
func TypeCheck(v Value, mask int64) bool {
	var bit int64
	switch v.Deref().Kind() {
	case KindNull, KindUndef:
		bit = 1 << 1
	case KindFalse:
		bit = 1 << 2
	case KindTrue:
		bit = 1 << 3
	case KindInt:
		bit = 1 << 4
	case KindFloat:
		bit = 1 << 5
	case KindString:
		bit = 1 << 6
	case KindArray:
		bit = 1 << 7
	case KindObject:
		bit = 1 << 8
	case KindResource:
		bit = 1 << 9
	}
	return mask&bit != 0
}

// IsSet is `isset()`: **set and not null**.
//
// It cannot be spelled `!IsUndef`: a null array element or property is not set.
// And it is not the negation of `empty`, which asks whether the value is falsy.
func IsSet(v Value) bool {
	v = v.Deref()
	return !v.IsUndef() && v.Kind() != KindNull
}

// Unbox is a **by-value** parameter taking what arrived. A dynamically
// dispatched call cannot know at the send site whether the callee wants a
// reference, so it passes the caller's storage and the callee decides; a
// by-value parameter takes the value out and copies it, which is what by-value
// has always meant.
//
// A value that is not a box is handed back untouched, so the ordinary send --
// which copied already -- does not pay for a second copy.
func Unbox(v Value) Value {
	// **The parameter's own array**, shared rather than copied. An argument
	// arrives as the caller's array, and a promoted parameter is never stored,
	// so without a header of its own a write to the original -- through a
	// global, a property -- showed through the parameter:
	// `f($g)` with `global $g; $g[] = 2;` inside saw the new element. Sharing
	// costs O(1); whichever holder writes first separates.
	return v.Share()
}

// Arg is the nth argument, or null for one the caller omitted.
func Arg(args []Value, n int) Value {
	if n < len(args) {
		return args[n]
	}
	return Null()
}

// ReadDim reads `$container[$key]`. `silent` suppresses the missing-key
// warning, which is what `??` and `isset()` need.
//
// Every row read back from the engine, and three of them were missing from the
// generated program's copy of this:
//
//   - **A string is a container.** `"abc"[1]` is `"b"` and `"abc"[-1]` is `"c"`;
//     past the end warns `Uninitialized string offset N` and yields `""`, not
//     null.
//   - A **non-container warns**: `null[0]` and `5[0]` are
//     `Trying to access array offset on null` / `on int` and null, not a silent
//     null. `true` and `false` are spelled out rather than reported as `bool`.
//   - A string offset that is a **float or numeric string** warns
//     `String offset cast occurred`, while one that is a non-numeric string
//     **throws** `Cannot access offset of type string on string`. Warning
//     versus throwing, and nothing in either message hints at which.
func ReadDim(ctx *Ctx, container, key Value) (Value, error) {
	if v, ok := PackedAt(container, key); ok {
		return v, nil
	}
	return readDim(ctx, container, key, false)
}

// PackedSet is the write-back half of `$list[$i] op= v` on a packed array:
// the element at that position replaced in place, when the array's body is
// not shared -- so nothing else sees the write -- and the element is live and
// not a reference to write through. Array.SlotSet's conditions, without the
// slot: a packed list's key is its position. false writes nothing, and the
// caller takes the general path. Inlinable for the emitter, like PackedAt.
func PackedSet(slot *Value, key, v Value) bool {
	if slot.kind == KindArray && key.kind == KindInt {
		if a := (*Array)(slot.ptr); a.packed && a.shares == 0 && uint64(key.num) < uint64(len(a.vals)) {
			if b := &a.vals[key.num]; b.kind > KindUndef && b.kind < KindRef {
				a.interned = false
				*b = v
				return true
			}
		}
	}
	return false
}

// PackedAt is `$list[$i]` on a packed array holding that position: the
// element straight from the bucket slice, with no key built and no lookup.
// ok is false for anything else -- another kind of container or key, a hole,
// an index out of range, or an element bound by reference -- and the caller
// then takes ReadDim's general path, which answers each of those.
//
// Exported and inlinable so the emitter can test it **at the call site**:
// ReadDim itself cannot inline (the general call is too expensive for the
// budget), and ary3's inner loop spent more in the read than in the loop.
// `< KindRef` is a live element that is not a reference: the deleted
// sentinel sorts after every kind.
func PackedAt(container, key Value) (Value, bool) {
	if container.kind == KindArray && key.kind == KindInt {
		if a := (*Array)(container.ptr); a.packed && uint64(key.num) < uint64(len(a.vals)) {
			if v := a.vals[key.num]; v.kind < KindRef {
				return v, true
			}
		}
	}
	return Value{}, false
}

// ReadDimSilent is the IS form -- `$a[$k] ?? $d`, `isset($a[$k])`. It is a
// **different function**, not the same one with the warnings turned off: an
// out-of-range string offset is *not set* here and yields null, where the loud
// read yields `""` and warns. Two questions, two answers.
//
// It still throws, because a non-numeric string offset is a TypeError in both
// modes, and it still deprecates a float key: the engine emits
// `Implicit conversion from float 1.5 to int loses precision` for
// `$a[1.5] ?? "d"` exactly as it does for `$a[1.5]`. Settled by probe.
func ReadDimSilent(ctx *Ctx, container, key Value) (Value, error) {
	return readDim(ctx, container, key, true)
}

// ReadDimList is a destructuring read, `[$a, $b] = $v`: an array or an object
// is read as ReadDim reads it, and anything else answers NULL without a word.
func ReadDimList(ctx *Ctx, container, key Value) (Value, error) {
	switch container.Deref().Kind() {
	case KindArray, KindObject:
		return ReadDim(ctx, container, key)
	}
	return Null(), nil
}

func readDim(ctx *Ctx, container, key Value, silent bool) (Value, error) {
	// An int key on an array that has it: no offset to judge, no key to
	// convert, nothing to warn about. A missing key takes the path below,
	// which warns.
	if container.kind == KindArray && key.kind == KindInt {
		if v, ok := container.a().Get(IntKey(key.num)); ok {
			return v, nil
		}
	}
	container = container.Deref()
	switch container.Kind() {
	case KindArray:
		if IllegalOffsetKind(key) {
			// An object or an array is not an offset PHP converts: it throws,
			// where every scalar has a silent conversion. `ArrayKey` fell
			// through to `StringKey(v.String())`, so a Closure used as a key
			// became some string instead of the TypeError the engine raises.
			return Value{}, ThrowIllegalOffset(key)
		}
		k := ArrayKey(ctx, key)
		v, ok := container.Array().Get(k)
		if !ok {
			if !silent {
				ctx.WarnUndefinedArrayKey(k)
			}
			return Null(), nil
		}
		return v, nil

	case KindString:
		s := container.String()
		i, ok, err := stringOffset(ctx, key, silent)
		if err != nil {
			return Value{}, err
		}
		if !ok {
			return Null(), nil
		}
		// The warning names the offset **as written**: `$s[-10]` on "abc"
		// is `Uninitialized string offset -10`, not -7.
		written := i
		if i < 0 {
			i += int64(len(s))
		}
		if i < 0 || i >= int64(len(s)) {
			if silent {
				// In the silent mode `??` and `isset()` use, an out-of-range
				// offset is **not set** rather than the empty string:
				// `$s[9] ?? "d"` is `"d"`, where the loud read is `""` with a
				// warning. The two modes answer different questions, so they
				// give different values, not the same value with the diagnostic
				// suppressed.
				return Null(), nil
			}
			ctx.WarnUninitializedStringOffset(written)
			return String(""), nil
		}
		return String(string(s[i])), nil
	}
	if container.Kind() == KindObject {
		// An ArrayAccess answers here; anything else is an Error rather than
		// the warning a null or an int earns. Only when a hop is installed:
		// without one this package cannot tell the two apart, and a consumer
		// that answers for indexed objects itself never reaches this.
		v, handled, err := readOffset(ctx, container.Object(), key, silent)
		if err != nil {
			return Value{}, err
		}
		if handled {
			return v, nil
		}
		if ctx != nil && ctx.arrayAccess != nil {
			return Value{}, ThrowObjectAsArray(container.Object().ClassName)
		}
	}
	if !silent {
		ctx.WarnOffsetOnNonContainer(container)
	}
	return Null(), nil
}

// UnsetDim is `unset($c[$k])` on an object, which is `offsetUnset($k)`. It
// reports handled=false for anything the hop does not answer, so the ordinary
// array path stays where it is.
func UnsetDim(ctx *Ctx, container Value, key Value) (bool, error) {
	c := container.Deref()
	if c.Kind() != KindObject {
		return false, nil
	}
	_, handled, err := offsetCall(ctx, c.Object(), OffsetUnset, []Value{key})
	if err != nil {
		return true, err
	}
	if handled {
		return true, nil
	}
	if ctx != nil && ctx.arrayAccess != nil {
		return true, ThrowObjectAsArray(c.Object().ClassName)
	}
	return false, nil
}

// ExistsDim is `isset($c[$k])` / `empty($c[$k])` on an object: `offsetExists`,
// and `offsetGet` only when it said yes and the caller needs the value. It is
// the same two-step `readOffset` does for the quiet read, exposed because
// `isset` reaches the emitted layer by a different route.
func ExistsDim(ctx *Ctx, container Value, key Value, wantValue bool) (Value, bool, error) {
	c := container.Deref()
	if c.Kind() != KindObject {
		return Undef(), false, nil
	}
	exists, handled, err := offsetCall(ctx, c.Object(), OffsetExists, []Value{key})
	if err != nil || !handled {
		if !handled && ctx != nil && ctx.arrayAccess != nil {
			return Undef(), false, nil
		}
		return Undef(), handled, err
	}
	if !exists.Deref().Bool() {
		return Undef(), true, nil
	}
	if !wantValue {
		return Bool(true), true, nil
	}
	v, _, err := offsetCall(ctx, c.Object(), OffsetGet, []Value{key})
	return v, true, err
}

// stringOffset converts a key used as a string offset, which is stricter than
// an array key: a non-numeric string throws rather than becoming 0.
// leadingIntegerOffset reads the integer prefix of a string offset that is not
// itself numeric. A prefix followed by anything that would have made the string
// a *float* -- a `.` or an exponent -- is not one: `"1.5x"` is a TypeError where
// `"1x"` is a warning and an offset.
func leadingIntegerOffset(s string) (int64, bool) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '\v' || s[i] == '\f') {
		i++
	}
	start := i
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == digits {
		return 0, false
	}
	if i < len(s) && (s[i] == '.' || s[i] == 'e' || s[i] == 'E') {
		return 0, false
	}
	n, err := strconv.ParseInt(s[start:i], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// StringOffsetIndex is isset's rule for a string offset, which is neither the
// read's nor the array key's. Asked of the engine, `isset($s[$k])` converts a
// float (deprecating a lossy one), a bool and a null, accepts only an
// integer-canonical string, and answers plain `false` -- no throw -- for an
// array or an object, where a read raises a TypeError for all three of the
// string cases the read refuses.
func StringOffsetIndex(ctx *Ctx, key Value) (int64, bool) {
	switch key.Kind() {
	case KindInt:
		return key.Int(), true
	case KindFloat:
		warnPrecisionLoss(ctx, key)
		return key.Int(), true
	case KindNull, KindTrue, KindFalse:
		return key.Int(), true
	case KindString:
		if n, ok := key.IsNumericString(); ok && n.Kind() == KindInt {
			return n.Int(), true
		}
	}
	return 0, false
}

// stringOffset converts a string offset's key. The bool is *usable*: in the
// silent mode `??` reads with, a string key that names no offset is *not set*
// rather than a TypeError -- `"hello"["x"] ?? "d"` is "d" and `$s["1.5"] ?? "d"`
// is "d" -- while an array or an object key throws in both modes.
func stringOffset(ctx *Ctx, key Value, silent bool) (int64, bool, error) {
	switch key.Kind() {
	case KindInt:
		return key.Int(), true, nil
	case KindFloat:
		if !silent {
			ctx.WarnStringOffsetCast()
		}
		return key.Int(), true, nil
	case KindString:
		// A string offset reads its string key by **three** rules, not one, and
		// they are not the array-key rules. Asked of the engine:
		//
		//	$s["1"] $s[" 1"] $s["1 "] $s["-1"] $s["+1"]  silent
		//	$s["1x"] $s["0x1"] $s["1_0"]  Illegal string offset, then the prefix
		//	$s["1.5"] $s["1e0"] $s["1.5x"] $s["x"] $s[""] TypeError
		//
		// So an integer-canonical string is used silently, a *float*-shaped one
		// is refused outright rather than truncated the way a float **value**
		// is, and anything else with a leading integer warns and uses that
		// prefix. `IsNumericString` answers only the first two together, which
		// sent `"1.5"` down the cast-warning path and `"1x"` to the TypeError --
		// both of the cases the engine treats specially, and both backwards.
		if n, ok := key.IsNumericString(); ok {
			if n.Kind() == KindFloat {
				if silent {
					return 0, false, nil
				}
				return 0, false, ThrowStringOffsetType(TypeName(key), "string")
			}
			return n.Int(), true, nil
		}
		if n, ok := leadingIntegerOffset(key.RawString()); ok {
			// Loud even in the silent mode: `$s["1x"] ?? "d"` warns and yields
			// the character, where `isset($s["1x"])` is plain false. The two
			// answer different questions here, so `??` is not isset with the
			// diagnostic suppressed -- see StringOffsetIndex for isset's rule.
			ctx.Warn("Illegal string offset %q", key.RawString())
			return n, true, nil
		}
		if silent {
			return 0, false, nil
		}
		return 0, false, ThrowStringOffsetType(TypeName(key), "string")
	case KindNull, KindTrue, KindFalse:
		if !silent {
			ctx.WarnStringOffsetCast()
		}
		return key.Int(), true, nil
	}
	return 0, false, ThrowStringOffsetType(TypeName(key), "string")
}

// ConstArray builds an array from evaluated elements, which is what a constant
// array initializer and an array literal both reduce to once the elements are
// values.
func ConstArray(ctx *Ctx, items []Value) Value {
	a := NewArray()
	for _, v := range items {
		a.Append(v)
	}
	return Arr(a)
}

// WriteDim writes `$container[$key] = $v`, or appends when `appending`.
//
// It is the mirror of ReadDim and carries the rules that make a *write* differ
// from a read:
//
//   - **Autovivification.** An unset or null slot becomes an array. A scalar
//     does not: it throws `Cannot use a scalar value as an array`.
//   - **A string is written by offset, not converted.** `$s[1] = "X"` on
//     `"abc"` gives `"aXc"`; writing past the end **pads with spaces**
//     (`$t[5]="Z"` on `"abc"` is `"abc  Z"`); assigning more than one byte
//     keeps the first and warns. None of that is derivable from the array path,
//     which is why the old open-coded version threw on all three.
//   - `$s[] = 1` on a string is its own error, `[] operator not supported for
//     strings`, not the scalar one.
//
// The slot is dereferenced first, so a write through a reference reaches the
// box every name shares.
func WriteDim(ctx *Ctx, slot *Value, key Value, v Value) (Value, error) {
	return writeDim(ctx, slot, HeldArray{}, key, "", v, "", false)
}

// WriteDimIn is WriteDim for a **nested** target: root is the array the chain
// was reached from. A handler run by the write's own diagnostics may replace
// it, and the engine then gives the write up rather than filling storage
// nobody can see any more (holdArrayAcross).
func WriteDimIn(ctx *Ctx, slot *Value, root HeldArray, key Value, v Value) (Value, error) {
	return writeDim(ctx, slot, root, key, "", v, "", false)
}

// WriteDimKeyVar is `$c[$k] = $v` whose key -- and perhaps value -- is a
// variable **the write reads itself**, handed over as the variable held it
// rather than read beforehand. keyName and valName name them; empty means
// that operand was an ordinary value.
//
// The engine's ASSIGN_DIM reads a CV key inside the handler, after it has
// decided what the container is, and that order is observable:
//
//   - `$f = false; $f[$u] = 1` deprecates the conversion **before** it
//     reports `$u` undefined, and a handler that lets go of `$f` there ends
//     the write before the key is read at all (falsetoarray_002);
//   - `$s = "abc"; $s[$u] = "Q"` whose handler reassigns `$s` still goes on
//     to `String offset cast occurred` for the string it had, and then gives
//     the write up (str_offset_006);
//   - `$a[0][$d] = "b"` fetches `$a[0]` before `$d` is read, so a handler
//     that replaces `$a` leaves the write to a detached element
//     (str_offset_007).
//
// Reading the key as an instruction of its own put its warning ahead of all
// of that. So does reading the value, which the engine reads after the key.
func WriteDimKeyVar(ctx *Ctx, slot *Value, root HeldArray, key Value, keyName string, v Value, valName string) (Value, error) {
	return writeDim(ctx, slot, root, key, keyName, v, valName, false)
}

// refAutoInit refuses turning an undefined or null value into an array
// through a reference a typed property holds, when that property's type
// cannot hold an array: `$r = &$o->p; $r[] = 1` with `?string $p` is `Cannot
// auto-initialize an array inside a reference held by property Foo::$p of
// type ?string`, and the box keeps its null (typed_properties_083).
func refAutoInit(r *Ref) error {
	sources := r.Sources()
	if len(sources) == 0 {
		return nil
	}
	if cur := r.Get().Deref(); !cur.IsUndef() && cur.Kind() != KindNull {
		return nil
	}
	for _, src := range sources {
		if !typeHoldsArray(src.Type) {
			return Raise("TypeError",
				"Cannot auto-initialize an array inside a reference held by property %s::$%s of type %s",
				DisplayName(src.Class), src.Name, src.Type)
		}
	}
	return nil
}

// typeHoldsArray reports whether a declared type admits an array: `array`,
// `iterable` or `mixed` as the type or one member of a union.
func typeHoldsArray(declared string) bool {
	for _, part := range strings.Split(strings.TrimPrefix(declared, "?"), "|") {
		switch strings.ToLower(strings.Trim(strings.TrimSpace(part), "()")) {
		case "array", "iterable", "mixed":
			return true
		}
	}
	return false
}

// AppendDim is `$a[] = v`. It is its own intrinsic rather than a flag on
// WriteDim because it is a different operation: there is no key, the index is
// chosen at write time, and appending to a string is its own error
// (`[] operator not supported for strings`) where writing an offset is legal.
//
// Both return the value written, because an assignment is an expression whose
// value is what was assigned.
func AppendDim(ctx *Ctx, slot *Value, v Value) (Value, error) {
	return writeDim(ctx, slot, HeldArray{}, Value{}, "", v, "", true)
}

func writeDim(ctx *Ctx, slot *Value, root HeldArray, key Value, keyName string, v Value, valName string, appending bool) (Value, error) {
	// An operand the write reads itself arrives as its variable held it:
	// undefined warns here, in the engine's order, and a reference is read
	// through -- what ReadVar would have done at the instruction before.
	keyWarn := keyName != "" && key.IsUndef()
	valWarn := valName != "" && v.IsUndef()
	if keyName != "" {
		key = key.Deref()
	}
	if valName != "" {
		v = v.Deref()
	}
	if !keyWarn && !valWarn {
		// An int key over an element that holds a plain value: one lookup, no
		// typed box to satisfy. A new key or a boxed element takes the path
		// below.
		if !appending && slot.kind == KindArray && key.kind == KindInt &&
			slot.a().setUnboxed(IntKey(key.num), v.Share()) {
			return v, nil
		}
		// `$a[] = v` on an array with a next key: the append below, without
		// the questions about objects and boxes it asks first.
		if appending && slot.kind == KindArray {
			if a := slot.a(); a.CanAppend() {
				a.Append(v.Share())
				return v, nil
			}
		}
	}
	if o := slot.Deref(); o.Kind() == KindObject {
		// The engine holds the **object** while it reads the key and then the
		// value: a handler that reassigns the variable meanwhile does not
		// change which object `offsetSet` is asked of.
		if keyWarn {
			ctx.WarnUndefinedVariable(keyName)
			if err := ctx.TakePendingError(); err != nil {
				return Null(), err
			}
			key = Null()
		}
		if valWarn {
			ctx.WarnUndefinedVariable(valName)
			if err := ctx.TakePendingError(); err != nil {
				return Null(), err
			}
			v = Null()
		}
		// `$c[$k] = v` is `offsetSet($k, v)` and `$c[] = v` is
		// `offsetSet(null, v)` -- the append passes an explicit null rather
		// than fewer arguments, which is what lets one accessor answer both.
		arg := key
		if appending {
			arg = Null()
		}
		// A WeakMap tells `$m[] = v` from `$m[null] = v`, which the null
		// convention above cannot: the first has no key to invent, the second
		// names one that is not an object.
		if _, weak := o.Object().Internal.(*WeakMapStore); weak {
			if appending {
				return v, RaiseAt(ctx, "Error", "Cannot append to WeakMap")
			}
			if _, err := WeakMapKey(key); err != nil {
				return v, err
			}
		}
		_, handled, err := offsetCall(ctx, o.Object(), OffsetSet, []Value{arg, v})
		if err != nil {
			return v, err
		}
		if handled {
			return v, nil
		}
		if ctx != nil && ctx.arrayAccess != nil {
			return v, ThrowObjectAsArray(o.Object().ClassName)
		}
	}
	// gone reports that a handler let go of the container **around** the
	// storage being written: the array a nested chain came from, or -- for a
	// variable bound to a box -- the variable itself, which `unset` or a
	// rebinding leaves pointing elsewhere while the box lives on.
	gone := root.Lost
	if r, isRef := slot.RefOf(); isRef {
		if err := refAutoInit(r); err != nil {
			return v, err
		}
		outer := slot
		gone = func() bool {
			if root.Lost() {
				return true
			}
			now, still := outer.RefOf()
			return !still || now != r
		}
		// Write **through** the box rather than copying its value out, into a
		// recursive call, and back. The copy-back was what made autovivification
		// through a reference visible to every sharer; a pointer to the shared
		// storage has that for free.
		if inner := r.Slot(); inner != nil {
			slot = inner
		}
	}
	switch slot.Kind() {
	case KindArray:
	case KindString:
		// **An empty string is still a string.** It used to break out to the
		// autovivify path on the reading that `""` behaves like null; measured,
		// it does not in PHP 8.4 -- `$e = ""; $e[] = 1;` is `[] operator not
		// supported for strings`, `$e["k"] = 1` is `Cannot access offset of
		// type string on string`, and `$e[0] = "z"` writes the offset and
		// yields `string(1) "z"`. All three are the ordinary string answers.
		if appending {
			return v, ThrowAppendToString()
		}
		return writeStringOffset(ctx, slot, gone, key, keyName, keyWarn, v, valName, valWarn)

	case KindUndef, KindNull:
		// Autovivified **before** the key is read: a handler run by the key's
		// warning sees the empty array (probed), and one that lets go of it
		// ends the write.
		*slot = Arr(NewArray())

	case KindFalse:
		// **`false` autovivifies, and says so.** It is the one scalar that
		// does: `true`, `0` and `1.5` are all `Cannot use a scalar value as an
		// array`, and a non-empty string has its own wording again. Measured
		// for the keyed and the appending form alike.
		//
		// The array is made first and the deprecation raised over it, held:
		// a handler sees `array(0)`, and one that lets go of it -- assigns the
		// variable, unsets it -- ends the write there, before the key is even
		// read (falsetoarray_002). A handler that throws leaves the empty
		// array behind.
		*slot = Arr(NewArray())
		lost, err := holdArrayAcross(slot, HeldArray{}, "a write is filling", func() {
			ctx.Deprecated("Automatic conversion of false to array is deprecated")
		})
		if err != nil {
			return Null(), err
		}
		if err := ctx.TakePendingError(); err != nil {
			return Null(), err
		}
		if lost || gone() {
			return Null(), nil
		}

	default:
		return v, ThrowScalarAsArray()
	}
	return writeArrayDim(ctx, slot, gone, key, keyName, keyWarn, v, valName, valWarn, appending)
}

// writeArrayDim is writeDim once the container is an array. Every diagnostic
// it raises runs **holding** the array, as the engine's fetch does
// (GC_ADDREF around the warning): a handler that lets go of it -- or of the
// array a nested chain was reached from -- gives the write up, and the
// assignment evaluates to NULL (probed for the key's undefined-variable
// warning, a float key's precision deprecation and the value's warning).
func writeArrayDim(ctx *Ctx, slot *Value, gone func() bool, key Value, keyName string, keyWarn bool, v Value, valName string, valWarn bool, appending bool) (Value, error) {
	held := func(warn func()) (bool, error) {
		lost, err := holdArrayAcross(slot, HeldArray{}, "a write is filling", warn)
		if err != nil {
			return false, err
		}
		if err := ctx.TakePendingError(); err != nil {
			return false, err
		}
		return lost || gone(), nil
	}
	if appending {
		// **There is not always a next key.** Once the largest integer key is
		// taken the engine refuses rather than wrapping, and the element is
		// not added. `Array.CanAppend` already answers it; only this path was
		// not asking.
		if !slot.Array().CanAppend() {
			return v, ThrowNextElementOccupied()
		}
		if valWarn {
			if lost, err := held(func() { ctx.WarnUndefinedVariable(valName) }); err != nil || lost {
				return Null(), err
			}
			v = Null()
		}
		// **Assignment copies**, which is what makes `$a[] = $a` store the
		// array as it was rather than a loop back into itself. Without the
		// copy the element holds the same `*Array` and `var_dump` reports
		// `*RECURSION*` -- where the engine prints the old contents, and
		// reserves `*RECURSION*` for a structure genuinely made recursive
		// through a reference.
		slot.Array().Append(v.Share())
		return v, nil
	}
	if keyWarn {
		if lost, err := held(func() { ctx.WarnUndefinedVariable(keyName) }); err != nil || lost {
			return Null(), err
		}
		key = Null()
	}
	if IllegalOffsetKind(key) {
		return v, ThrowIllegalOffset(key)
	}
	var k Key
	if kind := key.Kind(); kind == KindFloat || kind == KindResource {
		// The two keys whose conversion is remarked on.
		if lost, err := held(func() { k = ArrayKey(ctx, key) }); err != nil || lost {
			return Null(), err
		}
	} else {
		k = ArrayKey(ctx, key)
	}
	if valWarn {
		// **The element exists before the value is read** (probed): a handler
		// run by the value's warning sees it as NULL, and it stays when the
		// handler throws.
		if _, present := slot.Array().Raw(k); !present {
			slot.Array().Set(k, Null())
		}
		if lost, err := held(func() { ctx.WarnUndefinedVariable(valName) }); err != nil || lost {
			return Null(), err
		}
		v = Null()
	}
	// **The element may hold a typed box.** `$a = [&$o->n]` on
	// `public int $n` leaves `$a[0]` sharing the property's box, and a
	// write through *that* name has to satisfy the property's declaration:
	// `$a[0] .= "5"` stores an int and `$a[0] .= "e50"` is
	// `Cannot assign string to reference held by property C::$n of type
	// int`. The constraint travels with the box, so nothing about the array
	// can see it. Two other sites already asked this -- a variable slot in
	// StoreVar and a property in the emitter's property write -- and the
	// array element was the one that did not.
	//
	// `Raw`, not `Get`: Get derefs, and the box is exactly what has to be
	// asked here.
	if cur, present := slot.Array().Raw(k); present {
		if r, boxed := cur.RefOf(); boxed && len(r.Sources()) > 0 {
			out, err := CoerceRefWrite(ctx, r, v, false)
			if err != nil {
				return v, err
			}
			r.Set(out.Share())
			// The assignment's value is what the box now holds.
			return out, nil
		}
	}
	// Assignment copies; see the append above.
	slot.Array().Set(k, v.Share())
	return v, nil
}

// DimTarget opens a **writable slot** for `$container[$key]`, so a nested write
// -- `$a["x"]["y"] = 1` -- has somewhere to write through. `appending` is the
// `$a[][...]` form, which chooses the index here rather than at the write.
//
// The slot is real storage inside the array, reached through a temporary box:
// `RefAt` puts one in and the returned func takes it out again. Without the
// second half the element stays a reference and `var_dump` prints `&array(...)`
// where PHP prints `array(...)` -- the box is a mechanism, not a value the
// program asked for.
//
// The container is autovivified by the same code an ordinary write uses --
// null becomes an array, a scalar throws `Cannot use a scalar value as an
// array` -- because a second copy of those rules is how they come apart.
func DimTarget(ctx *Ctx, slot *Value, key Value, appending bool) (*Value, func(), error) {
	ref, release, err := DimRef(ctx, slot, key, appending)
	if err != nil {
		return nil, release, err
	}
	// **The write through this element is open until the release runs**
	// (arrayBody.openWrites): a share taken meanwhile gets a snapshot rather
	// than the elements about to change. Counted here and not in DimRef,
	// because `$r = &$a[0]` keeps DimRef's box and never releases -- counting
	// it there left every array a reference was ever taken into snapshotting
	// for good.
	var body *arrayBody
	if held := slot.Deref(); held.Kind() == KindArray && held.Array() != nil {
		body = held.Array().arrayBody
		body.openWrites++
	}
	return ref.Slot(), func() {
		if body != nil {
			body.openWrites--
		}
		release()
	}, nil
}

// DimRef is DimTarget's other half: the **box** rather than the storage inside
// it.
//
// A nested write wants the storage and then wants the box gone again; `$r =
// &$a["k"]` wants the box and wants it to stay, because there the reference is
// what the program asked for rather than a mechanism. One function cannot
// decide that, so the caller does -- by running the release func or not.
func DimRef(ctx *Ctx, slot *Value, key Value, appending bool) (*Ref, func(), error) {
	noop := func() {}
	if r, isRef := slot.RefOf(); isRef {
		if err := refAutoInit(r); err != nil {
			return nil, noop, err
		}
		if inner := r.Slot(); inner != nil {
			slot = inner
		}
	}
	if held := slot.Deref(); held.Kind() == KindObject {
		if r, release, handled, err := storeElementRef(ctx, held.Object(), key, appending); handled {
			return r, release, err
		}
		if r, handled, err := overloadedElement(ctx, held.Object(), key, appending); handled {
			return r, noop, err
		}
	}
	// **A string refuses a reference outright**, before any write is attempted.
	// Measured: `$a = "s"; &$a[1]` and `$a = ""; &$a[0]` are both
	// `Cannot create references to/from string offsets`, so the refusal follows
	// the type rather than whether the offset exists -- while `$a = null`
	// autovivifies an array and succeeds. Appending has its own message.
	//
	// Without this the placeholder write below ran first and reported whatever
	// *it* refuses on, which is a TypeError about accessing an offset -- the
	// wrong class as well as the wrong words.
	if held := slot.Deref(); held.Kind() == KindString {
		if appending {
			return nil, noop, ThrowAppendToString()
		}
		// The offset is read first: `&$s["2bbb"]` warns `Illegal string
		// offset "2bbb"` and then refuses the reference.
		if k := key.Deref(); k.Kind() == KindString {
			if _, numeric := k.IsNumericString(); !numeric {
				if _, leading := leadingIntegerOffset(k.RawString()); leading {
					ctx.Warn("Illegal string offset %q", k.RawString())
					if err := ctx.TakePendingError(); err != nil {
						return nil, noop, err
					}
				}
			}
		}
		return nil, noop, ThrowStringOffsetReference()
	}
	vivified := false
	if slot.Kind() != KindArray {
		// Not an array yet: run the ordinary write with a placeholder, which
		// autovivifies or refuses exactly as it would for a real one, and then
		// reach the slot it created.
		if _, err := writeDim(ctx, slot, HeldArray{}, key, "", Null(), "", appending); err != nil {
			return nil, noop, err
		}
		if slot.Kind() != KindArray {
			// A string offset was written, or an ArrayAccess answered. Neither
			// hands out storage to descend into.
			return nil, noop, ThrowScalarAsArray()
		}
		vivified = true
	}
	arr := slot.Array()
	var k Key
	if appending {
		// The refusal comes **before** the reference is made, and before the
		// `Only variables` notice a bad right-hand side would earn: measured,
		// `$arr[] =& val()` on a saturated array reports the occupied next
		// element and not the notice. Making the box first would have bound a
		// slot the engine never created.
		// **The autovivifying write already appended.** `t($undef[])` on an
		// undefined variable ran the placeholder write above, which created the
		// array *and* its first element; appending again here made two where
		// the engine makes one. The element to hand back is the one that write
		// created, so take its key rather than adding another.
		if keys := arr.Keys(); vivified && len(keys) > 0 {
			k = keys[len(keys)-1]
		} else {
			if !arr.CanAppend() {
				return nil, noop, ThrowNextElementOccupied()
			}
			k = arr.Append(Null())
		}
	} else {
		if IllegalOffsetKind(key) {
			return nil, noop, ThrowIllegalOffset(key)
		}
		k = ArrayKey(ctx, key)
	}
	ref := arr.RefAt(k)
	return ref, func() { arr.CollapseUnsharedRef(k, ref) }, nil
}

// BindDim is `$a[$k] =& ...`: the element stops holding what it held and holds
// what it is given, box and all.
//
// `Bind`, not `Set`: an ordinary write goes **through** whatever reference the
// element already holds, and a rebinding replaces it. Going through DimTarget
// and writing its slot cannot express this -- that slot lives inside a box the
// release then takes away again, so the write lands nowhere.
func BindDim(ctx *Ctx, slot *Value, key Value, appending bool, v Value) error {
	if r, isRef := slot.RefOf(); isRef {
		if inner := r.Slot(); inner != nil {
			slot = inner
		}
	}
	if o := slot.Object(); slot.Kind() == KindObject && o != nil {
		// **An object's dimension is fetched for writing first** (probed).
		// A WeakMap hands out the entry, so `$m[$o] =& $x` binds it -- and
		// refuses an append, a key that is not an object, or a key it does
		// not hold, in its own words (gh20073). Any other ArrayAccess runs
		// offsetGet, with its `Indirect modification` notice, and then
		// refuses the binding outright.
		if store, weak := AsWeakMapStore(o); weak {
			if appending {
				return RaiseAt(ctx, "Error", "Cannot append to WeakMap")
			}
			k, err := WeakMapKey(key)
			if err != nil {
				return err
			}
			at, found := store.find(k)
			if !found {
				return RaiseAt(ctx, "Error", "Object %s#%d not contained in WeakMap",
					DisplayName(k.ClassName), k.ID)
			}
			store.bindAt(at, v)
			return nil
		}
		if _, handled, err := overloadedElement(ctx, o, key, appending); err != nil {
			return err
		} else if handled {
			return RaiseAt(ctx, "Error", "Cannot assign by reference to an array dimension of an object")
		}
	}
	if slot.Kind() != KindArray {
		if _, err := writeDim(ctx, slot, HeldArray{}, key, "", Null(), "", appending); err != nil {
			return err
		}
		if slot.Kind() != KindArray {
			return ThrowScalarAsArray()
		}
		// The placeholder write **appended** its null, so it comes off
		// again before the reference goes on: `$null[] = &$x` is one
		// element, not a NULL followed by the reference.
		if appending {
			slot.Array().PopLast()
		}
	}
	arr := slot.Array()
	if appending {
		// The third append path with the same question. `$arr[] =& $var` on a
		// saturated array reports the occupied next element, and it does so
		// **before** the `Only variables should be assigned by reference` a bad
		// right-hand side would earn -- the test that names this is called
		// `assign_ref_error_var_handling`, and that precedence is its point.
		if !arr.CanAppend() {
			return ThrowNextElementOccupied()
		}
		arr.Append(v)
		return nil
	}
	if IllegalOffsetKind(key) {
		return ThrowIllegalOffset(key)
	}
	arr.Bind(ArrayKey(ctx, key), v)
	return nil
}

// writeStringOffset is `$s[i] = v`, answering the assignment's value: the
// **one byte written**, `string(1) "x"` for `$s[1] = "xyz"`. An offset before
// the start is a warning, not an Error, and leaves the string alone with the
// expression's value NULL.
func writeStringOffset(ctx *Ctx, slot *Value, gone func() bool, key Value, keyName string, keyWarn bool, v Value, valName string, valWarn bool) (Value, error) {
	// **The string is held across every diagnostic**, and the write is given
	// up -- evaluating to NULL -- if a handler lets go of it: reassigns the
	// variable, unsets it, or replaces the array a nested chain came from.
	// Probed for the key's undefined-variable warning, `String offset cast
	// occurred`, `Illegal string offset`, the value's warning and `Only the
	// first byte`. A handler replacing it during the key's warning does not
	// stop the offset's own diagnostics: the engine had already chosen the
	// string path (str_offset_006).
	//
	// Held by identity -- where the bytes live -- since a string has no share
	// count here. So the variable is first given bytes of its own, as the
	// engine separates the string before it warns: an assignment of the equal
	// interned literal it held is then a change too. A handler that keeps a
	// copy is still not seen (the engine then writes into the copy as well,
	// which a Go string cannot).
	if keyWarn || valWarn || key.Kind() != KindInt || v.Kind() != KindString || v.num != 1 {
		*slot = String(strings.Clone(slot.String()))
	}
	orig := *slot
	stillHeld := func() bool {
		return !gone() && slot.kind == KindString && slot.ptr == orig.ptr && slot.num == orig.num
	}
	if keyWarn {
		// Not given up here even if the handler let go: the key is null
		// now, and its cast is the next diagnostic, checked below.
		ctx.WarnUndefinedVariable(keyName)
		if err := ctx.TakePendingError(); err != nil {
			return Null(), err
		}
		key = Null()
	}
	i, _, err := stringOffset(ctx, key, false)
	if err != nil {
		return v, err
	}
	if err := ctx.TakePendingError(); err != nil {
		return Null(), err
	}
	if !stillHeld() {
		return Null(), nil
	}
	if valWarn {
		ctx.WarnUndefinedVariable(valName)
		if err := ctx.TakePendingError(); err != nil {
			return Null(), err
		}
		v = Null()
		if !stillHeld() {
			return Null(), nil
		}
	}
	s := orig.String()
	if i < 0 {
		i += int64(len(s))
		if i < 0 {
			ctx.WarnNegativeStringOffset(i - int64(len(s)))
			return Null(), nil
		}
	}
	written := v.String()
	// An object is converted through `__toString`, whose exception ends
	// the write (probed: exception_from_toString).
	if v.Deref().Kind() == KindObject {
		conv, err := ToString(ctx, v)
		if err != nil {
			return Null(), err
		}
		written = conv.String()
		if !stillHeld() {
			return Null(), nil
		}
	}
	if len(written) == 0 {
		return v, RaiseAt(ctx, "Error", "Cannot assign an empty string to a string offset")
	}
	if len(written) > 1 {
		ctx.WarnMultibyteStringOffsetAssign()
		if err := ctx.TakePendingError(); err != nil {
			return Null(), err
		}
		if !stillHeld() {
			return Null(), nil
		}
	}
	b := []byte(s)
	for int64(len(b)) <= i {
		// Past the end pads with **spaces**, not NULs.
		b = append(b, ' ')
	}
	b[i] = written[0]
	*slot = String(string(b))
	return String(written[:1]), nil
}

// SubclassOf answers whether one class name descends from another, and is
// installed by the layer that has a class table.
//
// It is the fourth hook, alongside ObjectProperties, ResolveTraversable and
// CallValue, and it exists for the same reason: property **visibility** is a
// value-level rule -- it reads `Object.Declared` and a scope name -- except for
// the single question "are these two classes related", which needs a hierarchy
// this package deliberately does not have.
type SubclassOf func(child, ancestor string) bool

// SetSubclassOf installs the hop.
func (c *Ctx) SetSubclassOf(fn SubclassOf) { c.subclassOf = fn }

// InstanceOfClass is the **directed** question a class type declaration asks:
// is this value an object of that class or a descendant of it. `related` is
// symmetric, which is right for the protected-property rule and wrong here.
//
// It answers false when no class table is installed, so a caller must treat
// "false" as "cannot prove it matches" rather than as "proved it does not" --
// see declaredClassRejects.
func (c *Ctx) InstanceOfClass(v Value, class string) bool {
	o := v.Deref()
	if o.Kind() != KindObject {
		return false
	}
	name := o.Object().ClassName
	if strings.EqualFold(name, class) {
		return true
	}
	if c.subclassOf == nil {
		return false
	}
	return c.subclassOf(name, class)
}

// SetParentOf installs the parent lookup `parent` in a declared type needs.
func (c *Ctx) SetParentOf(fn func(class string) string) { c.parentOf = fn }

// HasClassTable reports whether the hierarchy hop is installed at all, which is
// the difference between "not a subclass" and "no way to tell".
func (c *Ctx) HasClassTable() bool { return c.subclassOf != nil }

func (c *Ctx) related(a, b string) bool {
	if strings.EqualFold(a, b) {
		return true
	}
	if c.subclassOf == nil {
		return false
	}
	return c.subclassOf(a, b) || c.subclassOf(b, a)
}

// PropertyAccess is how a scope may reach a declared property.
type PropertyAccess int

const (
	// PropertyReachable is ordinary access.
	PropertyReachable PropertyAccess = iota
	// PropertyInaccessible is declared but forbidden: an Error, and the class
	// named in it is the **instance's**, not the declaring one.
	PropertyInaccessible
	// PropertyInvisible is an inherited `private`, which is not an error but
	// simply **not there**: reading it warns `Undefined property` and yields
	// null. The difference turns on the *instance's* class, not on the scope.
	PropertyInvisible
)

// CheckPropertyRead applies PHP's visibility rule.
//
// Read back from the engine, and two rows are not guessable:
//
//   - the diagnostic names the **instance's** class -- a property declared in
//     `B` read on a `D` instance says `D::$prot`;
//   - an inherited **private** is *invisible* rather than inaccessible.
//     Reading `B`'s private on a `D` instance gives `Undefined property:
//     D::$priv`, while the same read on a `B` instance is `Cannot access
//     private property B::$priv`.
func CheckPropertyRead(ctx *Ctx, o *Object, name, scope string) PropertyAccess {
	d, declared := o.Declared[name]
	if !declared || d.Visibility == "" || d.Visibility == "public" {
		return PropertyReachable
	}
	if d.Visibility == "private" {
		if strings.EqualFold(scope, d.Class) {
			return PropertyReachable
		}
		if !strings.EqualFold(o.ClassName, d.Class) {
			return PropertyInvisible
		}
		return PropertyInaccessible
	}
	// protected: any scope in the same hierarchy as the declaring class --
	// or as the **first** class in the chain to declare the name. `C1` and
	// `C2` both redeclaring P's `protected $foo` can read each other's
	// (gh19044), because the property is P's.
	if scope != "" && (ctx.related(scope, d.Class) || d.SetClass != "" && ctx.related(scope, d.SetClass)) {
		return PropertyReachable
	}
	return PropertyInaccessible
}

// CheckPropertyWrite is CheckPropertyRead plus **readonly**, whose two failures
// are different rules and so have different messages.
//
// Writing an *initialised* readonly property fails **even inside the declaring
// class** -- readonly means written once, not written only from inside -- while
// *initialising* one from outside is a visibility failure, because the engine
// models readonly as `protected(set)`.
func CheckPropertyWrite(ctx *Ctx, o *Object, name, scope string) (PropertyAccess, error) {
	access := CheckPropertyRead(ctx, o, name, scope)
	d, declared := o.Declared[name]
	if err := CheckPropertyAsymmetric(ctx, o, name, scope, "modify"); err != nil {
		return access, err
	}
	if !declared || !d.Readonly {
		return access, nil
	}
	// **The declaring class, not the instance's.** `class B extends A` with
	// `A` declaring the readonly property reports `A::$p` from every one of
	// these, whatever `B` the value happens to be -- read back from the engine
	// for all three messages. Using `o.ClassName` named the subclass.
	owner := d.Class
	if owner == "" {
		owner = o.ClassName
	}
	if o.Reinitable[name] {
		// PHP 8.3: `__clone` may write a readonly property **once more** on the
		// object being cloned. Two things about it were measured rather than
		// assumed. The scope rule is the `protected(set)` one and not the
		// declaring-class one -- a subclass's `__clone` may reinitialize a
		// property its parent declared, and a foreign scope is refused with
		// `Cannot modify protected(set) readonly property C::$p from global
		// scope` rather than the plain readonly message. And the permission is
		// **per object**: another instance of the same class reached from
		// inside `__clone` is refused normally.
		if scope == "" || !ctx.related(scope, d.Class) {
			return access, ThrowReadonlyInitOutsideScope(owner, name, scope)
		}
		o.ConsumeReinit(name)
		return access, nil
	}
	if cur, _ := o.Get(name); !cur.IsUndef() {
		return access, ThrowReadonlyModification(owner, name)
	}
	// **`protected(set)` since PHP 8.4**: the first write may come from the
	// declaring class or from any class related to it -- a subclass's
	// method initializes a property its parent declared, and a parent's
	// method one a subclass redeclared. Measured in
	// readonly_props/initialization_scope. `public(set) readonly` may be
	// initialized from anywhere, once.
	if d.SetVisibility != "public" && (scope == "" || !ctx.related(scope, d.Class)) {
		return access, ThrowReadonlyInitOutsideScope(owner, name, scope)
	}
	return access, nil
}

// CheckPropertyAsymmetric applies PHP 8.4's `private(set)` / `protected(set)`
// to an operation that would write a property. Asked **before** the readonly
// rules, because it is a different question -- who may write at all, rather
// than whether this is the second write -- and it has its own message, which
// names the `(set)` modifier and the calling scope.
//
// `verb` is the operation, and the engine uses three:
//
//	$o->p = 1    $o->p++    $o->p += 1     Cannot modify
//	unset($o->p)                           Cannot unset
//	$o->a[] = 1   $o->a[$k] = 1   &$o->p   Cannot indirectly modify
//
// The third is the one that makes this a shared check rather than a line inside
// the setter: reaching a place is not the same operation as storing through it,
// and a property that refuses an indirect modification still allows a read. The
// unset and indirect positions checked nothing at all, so `unset($o->p)` on a
// `private(set)` property from outside quietly removed it.
func CheckPropertyAsymmetric(ctx *Ctx, o *Object, name, scope, verb string) error {
	d, declared := o.Declared[name]
	// A readonly property is the readonly rules' business -- unless it is
	// `private(set)`, which is refused in these words instead.
	if !declared || d.SetVisibility == "" || d.SetVisibility == "public" || d.Readonly && d.SetVisibility != "private" {
		return nil
	}
	if writeVisible(ctx, d, scope) {
		return nil
	}
	owner := d.Class
	if owner == "" {
		owner = o.ClassName
	}
	return ThrowAsymmetricAccess(verb, d.SetVisibility, owner, name, scope)
}

// writeVisible applies the `(set)` half of an asymmetric declaration: private
// only from the declaring class, protected from anywhere in its hierarchy --
// the same rule the read half uses, asked about a different modifier.
func writeVisible(ctx *Ctx, d Declaration, scope string) bool {
	if scope == "" {
		return false
	}
	if d.SetVisibility == "private" {
		return strings.EqualFold(scope, d.Class)
	}
	// **The first class to declare the name**, not the one that redeclared it:
	// `protected(set)` on `Base::$p` stays writable from a sibling of the
	// subclass that redeclared it, because both descend from Base.
	return ctx.related(scope, d.setOwner())
}

// CheckPropertyUnset is CheckPropertyWrite's rule for `unset($o->p)`.
//
// The split is the same and so is the reason for it: unsetting an *initialised*
// readonly property is refused even inside the declaring class, while
// *uninitialised* is a visibility question -- a constructor may `unset` its own
// readonly property, which is how a class makes one lazily initialisable.
// Only the verb in the message differs, and that is settled in diagnostics.go.
func CheckPropertyUnset(ctx *Ctx, o *Object, name, scope string) (PropertyAccess, error) {
	access := CheckPropertyRead(ctx, o, name, scope)
	if err := CheckPropertyAsymmetric(ctx, o, name, scope, "unset"); err != nil {
		return access, err
	}
	d, declared := o.Declared[name]
	if !declared || !d.Readonly {
		return access, nil
	}
	if o.Reinitable[name] && scope != "" && ctx.related(scope, d.Class) {
		// `unset($this->p)` inside `__clone` is allowed, and -- measured --
		// it does **not** spend the reinit permission: unsetting and then
		// writing both work in the same hook, and so does unsetting twice. So
		// this reads the permission without consuming it, where the write path
		// consumes.
		return access, nil
	}
	// The declaring class, as in CheckPropertyWrite above.
	owner := d.Class
	if owner == "" {
		owner = o.ClassName
	}
	if cur, _ := o.Get(name); !cur.IsUndef() {
		return access, ThrowReadonlyUnset(owner, name)
	}
	if !strings.EqualFold(scope, d.Class) {
		return access, ThrowReadonlyUnsetOutsideScope(owner, name, scope)
	}
	return access, nil
}

// ReadDeclaredProp reads a property slot that visibility has already allowed.
//
// A declared *typed* property that has never been written is a **third state**:
// the slot exists holding undef, which `var_dump` shows as `uninitialized(int)`.
// Reading it is an Error, not the `Undefined property` warning an absent one
// gets. `found` is false when the object has no such slot at all, which is the
// caller's cue to try a magic accessor.
func ReadDeclaredProp(ctx *Ctx, o *Object, name string) (v Value, found bool, err error) {
	v, ok := o.Get(name)
	if !ok {
		return Null(), false, nil
	}
	if v.IsUndef() {
		// **The declaring class.** `class B extends A` with `A` declaring the
		// typed property reports `A::$t`, not `B::$t`. Swept alongside the
		// readonly messages, where three other diagnostics -- `Undefined
		// property`, the `__toString` refusal and `count()`'s TypeError --
		// were checked and do name the *instance's* class, so this is not a
		// blanket rule.
		owner := o.ClassName
		if d, declared := o.Declared[name]; declared && d.Class != "" {
			owner = d.Class
		}
		return Value{}, true, ThrowUninitializedProperty(owner, name)
	}
	return v, true, nil
}

// Echo writes a value to the output, converting it to a string first.
//
// An intrinsic rather than a method call at the emission site: `echo` is an
// essential builtin that drives the runtime, and the IR should contain a call
// to it like every other operation. It is also the second most frequent
// instruction in the corpus, after a user function call.
// It converts through ToString rather than the value's plain string form,
// because `echo $obj` re-enters `__toString` and `echo $arr` warns. Delegating
// to Ctx.Echo got the array warning and missed the object entirely: the object
// printed as nothing.
func Echo(ctx *Ctx, v Value) error {
	s, err := ToString(ctx, v)
	if err != nil {
		return err
	}
	ctx.Write(s.String())
	return nil
}

// ToBool is PHP's boolean conversion as a value-producing operation, which is
// what `ZEND_BOOL` is: `(bool)$x` and the condition of an `if` are the same
// coercion.
func ToBool(v Value) Value {
	// A bool is already its own answer. Tested here rather than left to
	// Value.Bool, whose inlined fast path pushed this over the inlining
	// budget: `&&` and `||` pass every operand through, and mandel's inner
	// loop spent 47% of its time in the call.
	if v.kind == KindTrue || v.kind == KindFalse {
		return v
	}
	return toBoolSlow(v)
}

//go:noinline
func toBoolSlow(v Value) Value { return Bool(v.boolSlow()) }

// ToInt is `(int)$x`. `Value.Int` already carries every scalar rule; what is
// added here is the **object** one, which is a warning rather than a silent
// answer -- measured: `(int)$obj` prints `Object of class C could not be
// converted to int` and then yields 1.
//
// An array does not warn: `(int)[1,2]` is 1 and `(int)[]` is 0, quietly.
func ToInt(ctx *Ctx, v Value) Value {
	v = v.Deref()
	if v.Kind() == KindObject {
		ctx.WarnObjectConversion(v.Object().ClassName, "int")
		return Int(1)
	}
	return Int(v.Int())
}

// ToFloat is `(float)$x`, on the same rules.
func ToFloat(ctx *Ctx, v Value) Value {
	v = v.Deref()
	if v.Kind() == KindObject {
		ctx.WarnObjectConversion(v.Object().ClassName, "float")
		return Float(1)
	}
	return Float(v.Float())
}

// ToNull is `(unset)$x` -- gone from PHP 8, but the cast target still has a
// number and a program that reaches it should get null rather than a refusal.
func ToNull(Value) Value { return Null() }

// ToArray is `(array)$x`.
//
// Three rules, each measured: null becomes the **empty** array rather than a
// one-element one, an array is itself, and an object hands over its property
// table **with the engine's mangled keys** -- `(array)` of a class with a
// protected `$b` has a key of `"\0*\0b"`, which is exactly how the properties
// are stored here, so the copy needs no translation.
func ToArray(v Value) Value {
	v = v.Deref()
	switch v.Kind() {
	case KindArray:
		return v
	case KindNull, KindUndef:
		return Arr(NewArray())
	case KindObject:
		o := v.Object()
		// An initialized proxy casts as the instance behind it. The cast
		// itself initializes nothing: an uninitialized one is empty.
		for o.Lazy != nil && o.Lazy.Instance != nil {
			o = o.Lazy.Instance
		}
		// **An ArrayObject or ArrayIterator casts to its storage**, not its
		// properties -- unless constructed with STD_PROP_LIST. A store over
		// an object casts as that object does, which is also how a nested
		// ArrayObject reaches the innermost storage (probed;
		// get_mangled_object_vars, arraystore_array_cast).
		if store, ok := o.Internal.(*ArrayStore); ok && !store.stdPropList {
			if store.owner != nil {
				return ToArray(Obj(store.owner))
			}
			return Arr(store.items.Clone())
		}
		// An SplFixedArray casts to its elements, then its properties.
		if cast, ok := splFixedCast(o); ok {
			return Arr(cast)
		}
		// A date object casts to its date, zone type and zone, which are
		// not held (datetime.go).
		if HidesDateState(o) {
			AbortGap(DateGap(o, "array cast"))
		}
		out := NewArray()
		// **A Closure is wrapped, not opened**: `(array) $fn` is `[$fn]`, as
		// it is for a scalar -- the engine's convert_to_array special-cases
		// the class, and array_splice's replacement goes the same way
		// (bug52193; closure_array_cast).
		if o.ClassName == "Closure" {
			out.Append(v)
			return Arr(out)
		}
		// A SensitiveParameterValue casts to nothing: its value is the one
		// thing the class exists to keep out of view.
		if HidesProperties(o) {
			return Arr(out)
		}
		{
			o.PropEach(func(k Key, e Value) bool {
				// **An uninitialised typed property is not an element.** It
				// has a slot and no value, and `(array)` copies the values --
				// measured: `class B { public $r = 3; public int $u; }` casts
				// to a one-element array, not a two-element one with a null.
				// `var_dump` shows it as `uninitialized`, which is a different
				// question and a different renderer.
				if e.Deref().IsUndef() {
					return true
				}
				out.Set(castKey(o, k), e.Deref().Copy())
				return true
			})
		}
		return Arr(out)
	}
	out := NewArray()
	out.Append(v)
	return Arr(out)
}

// castKey is the name `(array)` gives a property.
//
// **A non-public property is mangled**, and only here: the storage keeps the
// plain name (except for a shadowed private, which is already mangled), while
// the cast hands out `"\0*\0b"` for a protected one and `"\0C\0c"` for a
// private one declared by C. Measured -- `var_dump((array)$o)` shows those
// keys, which is what makes them visible at all.
func castKey(o *Object, k Key) Key {
	if k.IsInt {
		return k
	}
	// A shadowed private is held under its mangled key already; mangling it
	// again gave `"\0Test\0\0Test\0changed"` (property_hooks/dump).
	if _, _, already := UnmangleProperty(k.Str); already {
		return k
	}
	d, declared := o.Declared[k.Str]
	if !declared {
		return k
	}
	switch d.Visibility {
	case "protected":
		return StringKey(MangleProperty("*", k.Str))
	case "private":
		owner := d.Class
		if owner == "" {
			owner = o.ClassName
		}
		return StringKey(MangleProperty(DisplayName(owner), k.Str))
	}
	return k
}

// ObjectCastProps is `(object)$x`'s half that this package can answer: the
// property table the new stdClass should hold. The **object** is the caller's
// to make, because handles are the generated unit's to number.
//
// An array becomes those properties -- an integer key included, which is why
// `var_dump` then shows `["2"]` -- null becomes an object with none, and any
// other scalar goes under the name `scalar`.
func ObjectCastProps(v Value) *Array {
	v = v.Deref()
	out := NewArray()
	switch v.Kind() {
	case KindArray:
		v.Array().Each(func(k Key, e Value) bool {
			out.Set(k, e.Deref().Copy())
			return true
		})
	case KindNull, KindUndef:
	default:
		out.Set(StringKey("scalar"), v)
	}
	return out
}

// NotBool is `!$x`. It is its own intrinsic rather than ToBool composed with a
// negation because the engine has its own opcode for it, and because a
// consumer that meets `bool_not` should not have to know that `!` is two steps.
func NotBool(v Value) Value {
	// The same split as ToBool, for the same reason.
	if v.kind == KindTrue || v.kind == KindFalse {
		// The other of the two, without a branch per answer; spelled this
		// way to stay under the inlining budget.
		return Value{kind: KindTrue + KindFalse - v.kind}
	}
	return notBoolSlow(v)
}

//go:noinline
func notBoolSlow(v Value) Value { return Bool(!v.boolSlow()) }

// The negated and ordering comparisons. They are trivial over LooseEqual,
// StrictEqual and Compare, and that is exactly why they belong here: the
// emitter had been composing `!php.LooseEqual(a, b)` and `php.Compare(a, b) < 0`
// from an operator table of its own, which is the second-table shape that had
// already drifted once and stopped seven compound operators compiling.
//
// Their effect contracts are their bases': identity never calls user code, so
// `!==` is pure, while `!=` and the ordering comparisons inherit `Compare`'s
// coercion and its re-entry through __toString.
func NotLooseEqual(a, b Value) bool { return !LooseEqual(a, b) }

func NotStrictEqual(a, b Value) bool { return !StrictEqual(a, b) }

func IsSmaller(a, b Value) bool { return Compare(a, b) < 0 }

func IsSmallerOrEqual(a, b Value) bool { return Compare(a, b) <= 0 }

// BoolXor is `xor`, which boolifies both operands: a coercion with no
// diagnostics, so the whole operation is pure.
func BoolXor(a, b Value) bool { return a.Bool() != b.Bool() }

// ---- array construction ----
//
// Building an array literal is three intrinsics rather than one, because the
// engine builds it that way: INIT_ARRAY then ADD_ARRAY_ELEMENT per entry, with
// the accumulator threaded through. Each returns the container so the IR stays
// a chain of values rather than a statement with a hidden mutation.

// NewArrayValue is a fresh empty array as a value.
func NewArrayValue() Value { return Arr(NewArray()) }

// ArrayAppend adds an element with the next integer key and returns the
// container. The element is **copied**: `[$a]` holds a copy, so a later write
// to `$a` does not reach inside the literal.
func ArrayAppend(c, v Value) (Value, error) {
	// **A literal saturates too.** `[PHP_INT_MAX => 42, new stdClass]` raises
	// rather than building a two-element array, and it raises while the literal
	// is under construction -- so nothing is bound and `var_dump` never runs.
	// The same refusal `$a[] = v` earns; the container being anonymous changes
	// nothing about it.
	if !c.Array().CanAppend() {
		return c, ThrowNextElementOccupied()
	}
	c.Array().Append(v.Share())
	return c, nil
}

// ArrayAppendBound is `[&$x]`: the element **holds the box**, so a later write
// through either name is visible from the other.
//
// It is a second intrinsic rather than a flag on ArrayAppend because the two do
// opposite things with the value they are handed -- one copies it and one keeps
// it -- and because the engine says which is meant, in `extended_value`'s low
// bit, at the point the element is added.
func ArrayAppendBound(c, ref Value) (Value, error) {
	if !c.Array().CanAppend() {
		return c, ThrowNextElementOccupied()
	}
	c.Array().Append(Shared(ref))
	return c, nil
}

// ArraySetBound is ArrayAppendBound with a key: `["k" => &$x]`.
func ArraySetBound(ctx *Ctx, c, k, ref Value) Value {
	c.Array().Set(ArrayKey(ctx, k), Shared(ref))
	return c
}

// ArraySet adds an element under a key and returns the container. The key goes
// through ArrayKey, which is where the float-precision deprecation and the
// resource-offset warning live -- so this needs the ctx that a plain append
// does not.
func ArraySet(ctx *Ctx, c, k, v Value) Value {
	// An object or an array is no key at all: `[new stdClass => 1]` is a
	// TypeError, parked for the check that follows every warning-capable
	// intrinsic, and the element is not added.
	if IllegalOffsetKind(k) {
		ctx.ParkError(RaiseAt(ctx, "TypeError", "Cannot access offset of type %s on array", TypeName(k.Deref())))
		return c
	}
	c.Array().Set(ArrayKey(ctx, k), v.Share())
	return c
}

// ---- the iterator protocol ----
//
// `foreach` is six intrinsics, which is what the engine's own opcodes are:
// FE_RESET_R, then per step the valid test, FE_FETCH_R for the value and the
// key, FE_FREE at the end.
//
// # The state is a value, not a Go type in a frame slot
//
// These took a `*Iter` -- a Go type that is not a `php.Value` -- so the IR
// needed a `*iter` representation, a frame slot to keep it in, and an argument
// shape that passed it through unconverted. Three special cases for one handle,
// and mem2reg could not promote the slot because nothing else could name it.
//
// `NewIterator` returns an **internal object** instead: an ordinary `php.Value`
// whose `Object.Internal` holds the cursor, which is the same mechanism a
// Generator already uses. The handle is then a value like any other -- it lives
// in an SSA value, it promotes, and every `iter.*` intrinsic takes a `Value`.
//
// It is deliberately not reachable from PHP: `foreach` never exposes its
// iterator, so the object has no handle ID and a class name no program can
// name.

// internalIteratorClass is the class name of the object carrying a foreach
// cursor. The `@` makes it unspellable in PHP source, so no program can name
// the type and no `instanceof` can see it.
const internalIteratorClass = "Iterator@internal"

// NewIterator begins an iteration and returns the state as a value.
func NewIterator(ctx *Ctx, subject Value) (Value, error) {
	it, err := NewIter(ctx, subject)
	if err != nil {
		return Null(), err
	}
	return Obj(&Object{ClassName: internalIteratorClass, Internal: it}), nil
}

// NewTraversableIterator wraps a Traversable the caller built itself in the
// cursor a foreach walks, opened as every Traversable is: rewind, then valid.
// It is how a layer with a scope -- the generated program -- iterates an
// object whose properties have to be read on the way (a hooked property's get
// hook runs as its element is reached, not before the loop).
func NewTraversableIterator(ctx *Ctx, t Traversable) (Value, error) {
	it := &Iter{trav: t}
	if err := t.Rewind(ctx); err != nil {
		return Null(), err
	}
	var err error
	if it.valid, err = t.Valid(ctx); err != nil {
		return Null(), err
	}
	return Obj(&Object{ClassName: internalIteratorClass, Internal: it}), nil
}

// RefTraversable is a Traversable that can hand out a box for a
// by-reference loop -- `foreach ($o as &$v)` over an object whose property
// is read through a hook.
type RefTraversable interface {
	Traversable
	CurrentRef(ctx *Ctx) (Value, error)
}

// NewRefIterator is `iter.new.ref`, the FE_RESET_RW half. Over an array it
// builds the **live** cursor a by-reference loop needs; everything else falls
// back to the ordinary constructor, which is what makes `foreach ($gen as &$v)`
// behave as a by-value loop rather than refusing.
func NewRefIterator(ctx *Ctx, subject Value) (Value, error) {
	held := subject.Deref()
	if held.Kind() == KindArray {
		it := NewArrayRefIter(held.Array())
		if r, isRef := subject.RefOf(); isRef {
			// Opened on a variable or a property's box: each fetch re-reads
			// it (Iter.follow).
			it.source, it.ctx = r, ctx
			it.file, it.line = ctx.Location()
		}
		return Obj(&Object{ClassName: internalIteratorClass, Internal: it}), nil
	}
	if held.Kind() == KindObject {
		if g, isGen := held.Object().Internal.(*Generator); isGen && !g.YieldsByRef() {
			// **An Exception, not an Error**, and the wording is the engine's.
			// A generator that did not declare `function &gen()` has no slot to
			// hand out, and PHP refuses rather than quietly copying.
			return Null(), Raise("Exception", "%s",
				"You can only iterate a generator by-reference if it declared that it yields by-reference")
		}
	}
	// A Traversable is not settled here: whether its elements can be bound
	// depends on what getIterator() resolves to and whether the class
	// overrides the iterator methods, which only the layer with a class table
	// can answer (the generated program's newRefIterator). Without one it
	// runs by value.
	return NewIterator(ctx, subject)
}

// RefSubject is the subject of a by-reference foreach over a variable, as
// FE_RESET_RW takes it: over an array the variable is made a reference and the
// loop holds it, so an array assigned to the variable in the body is the one
// the loop goes on with, and an `unset` of the variable leaves the loop the
// array it had. An **object** is held the same way, and a loop over its
// property table re-reads it too (gh20085: probed, another object or an array
// assigned in the body is walked from its first element, the same object goes
// on); a loop over a Traversable's iterator never looks at it again. Anything
// else is the value read, as before.
func RefSubject(slot *Value, read Value) Value {
	switch read.Deref().Kind() {
	case KindArray, KindObject:
		return RefArg(slot)
	}
	return read
}

// ObjectRefWalk is the generated program's half of a by-reference foreach
// over an object's property table, which only a layer with the class table
// can do: Boxes builds the array of an object's property slots the walk binds
// to, initialising a lazy object first (user code, which may throw), or
// reports false for one it does not model. A re-read asks it of *any* object:
// the engine walks whatever object the variable now holds through its
// property table, a Traversable included (probed: an ArrayIterator or a
// generator ends the loop, an IteratorAggregate's public properties are
// walked).
type ObjectRefWalk struct {
	Boxes func(ctx *Ctx, o *Object, scope string) (Value, bool, error)
}

// SetObjectRefWalk installs the program's ObjectRefWalk.
func (c *Ctx) SetObjectRefWalk(w ObjectRefWalk) { c.objectRefWalk = w }

// SetRefWalkScope records the class scope a by-reference walk runs in, which
// decides the properties a re-read object shows it (Iter.follow).
func SetRefWalkScope(v Value, scope string) {
	if it := cursorOf(v); it != nil {
		it.scope = scope
	}
}

// NewObjectRefIterator is the cursor over a plain object's property slots
// (boxes) that a by-reference foreach opened on subject -- the variable's
// reference, when RefSubject made one -- so each fetch re-reads it
// (Iter.follow).
func NewObjectRefIterator(ctx *Ctx, subject Value, o *Object, boxes Value) Value {
	it := NewArrayRefIter(boxes.Array())
	if r, isRef := subject.RefOf(); isRef {
		it.source, it.ctx = r, ctx
		it.file, it.line = ctx.Location()
		it.object = o
	}
	return Obj(&Object{ClassName: internalIteratorClass, Internal: it})
}

// cursorOf recovers the iteration state. A value that is not one is a compiler
// defect rather than a program error -- only lowering produces these -- so it
// answers nil and each caller degrades to "exhausted" rather than panicking in
// generated code.
func cursorOf(v Value) *Iter {
	// The loop's own cursor is a plain object value, never a reference: the
	// common case asserts straight away. Every step of a foreach asks this
	// three times -- valid, current, next -- and the Deref-then-accessor path
	// was 41% of arrpass's `sum()`.
	if v.kind == KindObject {
		it, _ := (*Object)(v.ptr).Internal.(*Iter)
		return it
	}
	return cursorOfSlow(v)
}

func cursorOfSlow(v Value) *Iter {
	o := v.Deref()
	if o.Kind() != KindObject {
		return nil
	}
	it, _ := o.Object().Internal.(*Iter)
	return it
}

// IteratorValid is whether the cursor is on an element.
//
// Pure, and the only one that is: a user Iterator's `valid()` is user code, but
// the engine always pairs `next` with the `valid` after it, so the cursor folds
// that pair into Next and caches the answer. The re-entry happens in
// `iter.next`, and this reads what it left.
func IteratorValid(v Value) bool {
	it := cursorOf(v)
	return it != nil && it.Valid()
}

// IteratorCurrent is the value at the cursor.
func IteratorCurrent(ctx *Ctx, v Value) (Value, error) {
	it := cursorOf(v)
	if it == nil {
		return Null(), nil
	}
	return it.Current(ctx)
}

// IteratorCurrentRef is the **by-reference** fetch: `foreach ($a as &$v)`.
//
// Over an array it answers the element's box, so the loop variable binds to the
// storage and a write through it lands in the array. Over anything else --
// an object, a generator -- there is no element to point at and it falls back
// to the ordinary read, which is what makes a `&` loop over a Traversable
// behave as a by-value one rather than refusing.
func IteratorCurrentRef(ctx *Ctx, v Value) (Value, error) {
	it := cursorOf(v)
	if it == nil {
		return Null(), nil
	}
	if r, ok := it.CurrentRef(); ok {
		// **Not marked shared here.** The bind is what records a program
		// binding, and marking at both ends counted each element twice: moving
		// to the next element drops one holder, so every element the loop had
		// visited kept var_dump's `&`. One mark, one drop, at the two ends of
		// the same binding.
		return MakeRef(r), nil
	}
	if it.gen != nil {
		if r, ok := it.gen.CurrentRef(); ok {
			return MakeRef(r), nil
		}
	}
	if rt, ok := it.trav.(RefTraversable); ok && it.Valid() {
		return rt.CurrentRef(ctx)
	}
	return it.Current(ctx)
}

// IteratorKey is the key at the cursor.
func IteratorKey(ctx *Ctx, v Value) (Value, error) {
	it := cursorOf(v)
	if it == nil {
		return Null(), nil
	}
	return it.Key(ctx)
}

// IteratorNext advances the cursor and **returns the state**.
//
// Returning it is what makes the stepping visible in the IR. The cursor is one
// object either way -- advancing really does mutate it -- but a call that only
// took the state left every use of it looking alike, so `%1` meant a different
// position before and after each step and nothing in the def-use chain said so.
// Threading the value through the loop is the difference between an iterator
// the IR describes and one it merely mentions.
func IteratorNext(ctx *Ctx, v Value) (Value, error) {
	it := cursorOf(v)
	if it == nil {
		return v, nil
	}
	return v, it.Next(ctx)
}

// IteratorFree releases the iteration. A user Iterator's `rewind`/`valid` may
// have left state that has to be dropped even when the loop exited early.
func IteratorFree(v Value) {
	if it := cursorOf(v); it != nil {
		it.Free()
	}
}

// IteratorFreeOwned is `iter.free` where the loop **owned** its subject: the
// subject was a temporary, so ending the loop destroys it.
//
// For an abandoned generator that is when PHP runs its `finally`, and the
// timing is observable -- one held in a variable waits for the unset, one held
// to the end of the script waits for shutdown. Only the loop-owned case is
// decidable without a refcount, and it is the common one.
func IteratorFreeOwned(ctx *Ctx, v Value) error {
	it := cursorOf(v)
	if it == nil {
		return nil
	}
	err := it.Close(ctx)
	it.Free()
	if err != nil && isGeneratorClose(err) {
		// The unwind reaching the top of the body is its expected end, and it
		// must not escape into the program that abandoned the generator.
		return nil
	}
	return err
}

// ---- string conversion, and the builder built on it ----

// ObjectToString calls an object's `__toString`. It is a hook because whoever
// owns the class table owns method lookup, and this package deliberately has
// neither. It reports false when the class declares no `__toString`, which is
// an Error rather than a fallback.
//
// Both consumers need it, which is why it lives here: the interpreter converted
// objects correctly and compiled code did not convert them at all, so
// `echo "x$obj"` printed `xy` where the engine printed `xSy`.
//
// **Per Ctx**, like every other hook. It was a package-level var, which the
// interpreter never set -- it converted objects through its own path and never
// came this way -- so the moment a shared builtin reached `ToString` with an
// object, `settype($withToString, "string")` threw where PHP converts.
type ObjectStringer func(ctx *Ctx, o *Object) (Value, bool, error)

// SetObjectToString installs it.
func (c *Ctx) SetObjectToString(fn ObjectStringer) { c.objectToString = fn }

// ObjectToString applies it, reporting false when nothing can answer.
func (c *Ctx) ObjectToString(o *Object) (Value, bool, error) {
	if c.objectToString == nil {
		return Null(), false, nil
	}
	return c.objectToString(c, o)
}

// ToString is PHP's string conversion, including the parts that are not pure:
// an object re-enters `__toString`, an array warns, and an object without
// `__toString` throws.
func ToString(ctx *Ctx, v Value) (Value, error) {
	switch v.Deref().Kind() {
	case KindObject:
		o := v.Deref().Object()
		// `__toString` runs user code, which moves the position; what the
		// caller raises next is still at its own line (implode's "Array to
		// string conversion" after an object's piece).
		file, line := ctx.Location()
		s, has, err := ctx.ObjectToString(o)
		ctx.SetLocation(file, line)
		if err != nil {
			return Null(), err
		} else if has {
			return String(s.String()), nil
		}
		return Null(), Raise("Error", "Object of class %s could not be converted to string", DisplayName(o.ClassName))
	case KindArray:
		// The value still converts, to the literal "Array"; the warning is the
		// whole of PHP's objection.
		ctx.Warn("Array to string conversion")
		return String("Array"), nil
	}
	return String(v.Deref().String()), nil
}

// StrBuf is a string under construction.
//
// `"a$b c"` compiles to ROPE_INIT/ROPE_ADD/ROPE_END -- the engine telling us a
// string is being built -- and building it as a chain of pure concatenations is
// O(n^2) against immutable Go strings where the engine mutates in place. The IR
// has carried the `*strbuf` representation since lowering was written; this is
// the type it names.
type StrBuf struct{ b strings.Builder }

// NewStrBuf starts a string under construction from its first piece.
func NewStrBuf(ctx *Ctx, v Value) (*StrBuf, error) {
	s, err := ToString(ctx, v)
	if err != nil {
		return nil, err
	}
	buf := &StrBuf{}
	buf.b.WriteString(s.String())
	return buf, nil
}

// StrBufAppend adds a piece. It can throw and can re-enter user code, because
// converting the piece can: the engine converts each piece as it is added, so
// a `__toString` that echoes does so **in build order**, before the finished
// string is printed.
func StrBufAppend(ctx *Ctx, buf *StrBuf, v Value) (*StrBuf, error) {
	s, err := ToString(ctx, v)
	if err != nil {
		return nil, err
	}
	buf.b.WriteString(s.String())
	return buf, nil
}

// StrBufFinish is the completed string.
func StrBufFinish(buf *StrBuf) Value { return String(buf.b.String()) }

// ---- count ----

// CountObject calls a Countable's `count()`. A hook for the same reason
// ObjectToString is: method lookup belongs to whoever owns the class table.
// False means the object is not Countable, which is a TypeError.
type ObjectCounter func(ctx *Ctx, o *Object) (Value, bool, error)

// SetCountObject installs it.
func (c *Ctx) SetCountObject(fn ObjectCounter) { c.countObject = fn }

// CountObject applies it, reporting false when nothing can answer.
func (c *Ctx) CountObject(o *Object) (Value, bool, error) {
	if c.countObject == nil {
		return Null(), false, nil
	}
	return c.countObject(c, o)
}

// Count is `count($v)`.
//
// PHP 8 takes only `Countable|array`: a string, a null or a plain object is a
// TypeError naming the type it got, and for an object that type is the **class
// name** rather than "object".
func Count(ctx *Ctx, v Value) (Value, error) {
	v = v.Deref()
	switch v.Kind() {
	case KindArray:
		return Int(int64(v.Array().Len())), nil
	case KindObject:
		o := v.Object()
		{
			if n, is, err := ctx.CountObject(o); err != nil {
				return Null(), err
			} else if is {
				return n, nil
			}
		}
		return Null(), Raise("TypeError",
			"count(): Argument #1 ($value) must be of type Countable|array, %s given", o.ClassName)
	}
	return Null(), Raise("TypeError",
		"count(): Argument #1 ($value) must be of type Countable|array, %s given", ValueName(v))
}

// ArrayKeyExists answers `array_key_exists($key, $array)`, which the compiler
// gives its own opcode. Two things separate it from `isset($array[$key])`: a
// key whose value is **null** is present, and a non-array second argument is a
// TypeError rather than false. The dynamic call reaches the same answer through
// the library table after the engine's own argument verification; the opcode
// has no verification in front of it, so it raises the TypeError itself.
func ArrayKeyExists(ctx *Ctx, key, container Value) (Value, error) {
	container = container.Deref()
	if container.Kind() != KindArray {
		return Undef(), Raise("TypeError",
			"array_key_exists(): Argument #2 ($array) must be of type array, %s given",
			ValueName(container))
	}
	_, exists := container.Array().Get(ArrayKey(ctx, key))
	return Bool(exists), nil
}

// ValueName is how a diagnostic names a value's type, which for a **bool is its
// value**: PHP 8 says "true given" and "false given", not "bool given". Settled
// with the engine, because TypeName says "bool" and is right for the places
// that ask what type something is rather than what was passed.
func ValueName(v Value) string {
	switch v.Deref().Kind() {
	case KindTrue:
		return "true"
	case KindFalse:
		return "false"
	}
	return TypeName(v.Deref())
}

// ---- value materialisation ----

// CopyValue materialises a value into a temporary, which is what the engine's
// QM_ASSIGN does.
//
// It is the identity today, and deliberately a named operation rather than an
// IR move: this is **the node copy elision elides**. The plan's D3 asks whether
// the source is dead after this point, and that question needs somewhere to be
// asked. An assignment's own copy is WriteVar's, not this one.
func CopyValue(v Value) Value { return v }

// ConcatValues is `.` on two PHP values.
//
// Concat itself takes the *string* forms, and getting a string form out of an
// object means re-entering `__toString`, which needs a ctx and can throw. The
// two are kept apart rather than merged so there is still exactly one place
// that joins two strings: this converts, then calls Concat.
//
// The signature is why this exists at all. The catalogue said Concat
// `ReEnters`, and it could not: `echo $obj . "x"` printed `x` where the engine
// prints the object's string form. A stated effect the implementation cannot
// perform is the same defect as an uncatalogued function, one level in.
func ConcatValues(ctx *Ctx, a, b Value) (Value, error) {
	l, err := ToString(ctx, a)
	if err != nil {
		return Null(), err
	}
	r, err := ToString(ctx, b)
	if err != nil {
		return Null(), err
	}
	return Concat(l, r), nil
}

// ReadVarSilent is reading a variable without the undefined-variable warning.
//
// `$q ?? 5` is one opcode in the engine and the suppression is intrinsic to it:
// the question `??` asks is whether the variable is set, so warning that it is
// not would be answering it twice. A separate intrinsic rather than a flag,
// because the two have different effect contracts -- this one is pure.
func ReadVarSilent(v Value) Value { return v.Deref() }

// StoreVar is `$x = v`: it writes **through** whatever reference the slot holds,
// and the value it writes is a **copy**.
//
// The copy is PHP's value semantics for arrays -- `$c = $b; $c[0] = 99` leaves
// `$b` alone -- and it is inside the intrinsic rather than at the call site so
// there is one place that knows an assignment copies, and one node for copy
// elision to elide.
// StoreVar is `$a = v` -- a write **through** a reference if the slot holds one.
//
// It is fallible because a reference can be **typed**: binding one to a typed
// property makes every write through any name sharing it satisfy that
// property's declaration. `$r = &$t->i` on `public int $i` then `$r = "21"`
// stores `int(21)`, and `$r = "x"` is
// `Cannot assign string to reference held by property T::$i of type int`.
// Without this the write went straight through and left a string in an `int`
// property.
// WarnIfUndefinedVar is the container half of a read-write or unset dimension
// fetch: `$y["k"] .= "s"` and `unset($x[0])` both warn about an undefined `$y`
// or `$x`, and the warning is **its own instruction** so it lands before the
// key is evaluated -- `$a[$b] += 1` says `$a` first and `$b` second, and the
// key's read is hoisted ahead of the fetch that consumes it.
func WarnIfUndefinedVar(c *Ctx, slot *Value, name string) {
	if slot != nil && slot.IsUndef() {
		c.WarnUndefinedVariable(name)
	}
}

// StoreVarShared is StoreVar without the copy, for a store the compiler has
// proved no one can tell from one: the value is an array the frame built,
// never written in place and never leaving the frame (OWNERSHIP-PERF.md P3a).
// Everything else -- writing through a reference, a typed reference's
// coercion -- is StoreVar's.
func StoreVarShared(ctx *Ctx, slot *Value, v Value, strict bool) error {
	if r, ok := slot.RefOf(); ok && len(r.Sources()) > 0 {
		out, err := CoerceRefWrite(ctx, r, v, strict)
		if err != nil {
			return err
		}
		r.Set(out)
		return nil
	}
	WriteVar(slot, v.Deref())
	return nil
}

// StoreVar is an assignment to a variable. A scalar into a slot that is not a
// box -- most stores -- is inlined; the rest is storeVarSlow.
func StoreVar(ctx *Ctx, slot *Value, v Value, strict bool) error {
	if slot.kind != KindRef && v.kind != KindArray && v.kind != KindRef {
		*slot = v
		return nil
	}
	return storeVarSlow(ctx, slot, v, strict)
}

func storeVarSlow(ctx *Ctx, slot *Value, v Value, strict bool) error {
	// A scalar into a slot that is not a box: nothing to coerce, nothing to
	// share, nothing to write through. Most stores are this.
	if slot.kind != KindRef && v.kind != KindArray && v.kind != KindRef {
		*slot = v
		return nil
	}
	if r, ok := slot.RefOf(); ok && len(r.Sources()) > 0 {
		out, err := CoerceRefWrite(ctx, r, v, strict)
		if err != nil {
			return err
		}
		r.Set(out.Share())
		return nil
	}
	// Shared, not cloned: the copy an assignment makes, deferred to whichever
	// holder writes first (Array.Share).
	WriteVar(slot, v.Share())
	return nil
}

// CoerceRefWrite applies the declarations of every typed property a reference
// is shared with to a value being written through it.
//
// Each property is asked about the value that was **written**, not about what
// the property before it made of it: two declarations that would coerce it
// differently are in conflict, and chaining would hide that by handing the
// second one the first one's answer. The diagnostic names the property rather
// than the name being assigned, since the name itself has no type.
func CoerceRefWrite(ctx *Ctx, r *Ref, v Value, strict bool) (Value, error) {
	sources := r.Sources()
	converted := v
	first := -1
	for i, src := range sources {
		next, err := coerceDeclared(ctx, v.Deref(), src.Type, strict)
		if err != nil {
			return v, RaiseAt(ctx, "TypeError",
				"Cannot assign %s to reference held by property %s::$%s of type %s",
				ValueName(v.Deref()), DisplayName(src.Class), src.Name, src.Type)
		}
		if first < 0 {
			first, converted = i, next
			continue
		}
		if next.Deref().Kind() == converted.Deref().Kind() {
			continue
		}
		// One box cannot hold two different values. The engine names both
		// properties, in the order they were bound.
		return v, RaiseAt(ctx, "TypeError",
			"Cannot assign %s to reference held by property %s::$%s of type %s "+
				"and property %s::$%s of type %s, as this would result in an "+
				"inconsistent type conversion",
			ValueName(v.Deref()),
			DisplayName(sources[first].Class), sources[first].Name, sources[first].Type,
			DisplayName(src.Class), src.Name, src.Type)
	}
	return converted, nil
}

// coerceDeclared applies a declared type without shaping a message, which is
// what the reference path needs: it has its own wording and asks several
// declarations in turn.
func coerceDeclared(ctx *Ctx, v Value, declared string, strict bool) (Value, error) {
	d, ok := ParseDeclaredType(declared)
	if !ok {
		// The reference path asks several declarations in turn, each the
		// declared type of a property the box is bound to -- so the `?` is the
		// whole truth about null there too.
		if declaredClassRejects(ctx, v, declared, true) {
			return v, errNotCoercible
		}
		return v, nil
	}
	return coerce(ctx, v, d, strict)
}

// BindVar is the slot *becoming* the box, which is what a by-reference argument
// arriving does. It does not copy: copying would dereference the box and sever
// the link the `&` exists to make.
//
// A different intrinsic from StoreVar rather than a flag on it, because getting
// the two the wrong way round is silently wrong -- `$a = $b` writes through a
// reference where `$a = &$b` replaces it.
func BindVar(slot *Value, v Value) { *slot = v }

// IterValid is whether the cursor is on an element.
//
// Pure, and the only iterator intrinsic that is: a user Iterator's `valid()` is
// user code, but the engine always pairs `next` with the `valid` after it, so
// Iter folds that pair into Next and caches the answer. The re-entry happens in
// `iter.next`, and this reads what it left.
func IterValid(it *Iter) bool { return it.Valid() }

// ---- specialized on the container's shape ----
//
// These are what a **generic** type buys the specializer. Knowing a value is an
// array, rather than merely that it may be one, removes whole branches:
//
//   - `NewIterator` has to ask whether the subject is a Generator, a
//     Traversable reached through `getIterator()`, or an array, and the first
//     two run user code -- so it takes a ctx, can throw, and re-enters. Over an
//     array there is nothing to run.
//   - `ReadDim` has to handle a string subject by offset and a non-container by
//     diagnostic. Over an array neither arises, and it cannot throw.
//   - `Count` has to ask whether an object is Countable and call its `count()`.
//     Over an array it is a length.
//
// Each is the *same operation* with a narrower effect contract, which is what
// the catalogue's variants are for. None of them is a different answer.

// NewArrayIterator begins an iteration over an array. Pure: no ctx, no error,
// no user code -- the three things the general form exists to handle.
func NewArrayIterator(subject Value) Value {
	it := NewArrayIter(subject.Deref().Array())
	return Obj(&Object{ClassName: internalIteratorClass, Internal: it})
}

// The iterator protocol over an array. Each is the general form with the two
// branches that can run user code removed, which is what knowing the subject
// buys: a Traversable's `current()`, `key()` and `next()` are method calls that
// can print, throw and re-enter, while stepping an array is an index into a
// snapshot. `Iter` already says so -- "advancing a generator can fail, its body
// runs, while advancing an array cannot".
//
// They take no ctx and return no error, so the emitted loop loses three error
// branches per iteration.
func IteratorCurrentArray(v Value) Value {
	it := cursorOf(v)
	if it == nil {
		return Null()
	}
	return it.CurrentArray()
}

func IteratorKeyArray(v Value) Value {
	it := cursorOf(v)
	if it == nil {
		return Null()
	}
	return it.KeyArray()
}

func IteratorNextArray(v Value) Value {
	if it := cursorOf(v); it != nil {
		it.NextArray()
	}
	return v
}

// ArraySetKey is `array.set` for a key already known to be an int or a string.
// `ArrayKey`'s only diagnostic is the float-precision deprecation, so those two
// key types make the whole operation pure and it needs no ctx. Settled against
// the engine: `$a[$i]`, `$a["k"]` and `$a["7"]` are all silent, and only a
// float that loses precision is not.
func ArraySetKey(c, k, v Value) Value {
	c.Array().Set(ArrayKey(nil, k), v.Share())
	return c
}

// ReadDimArray reads an element of an array. It still **warns** on a missing
// key -- that is the read's own diagnostic and not the subject's -- but it
// cannot throw, because throwing is what a non-container offset does.
func ReadDimArray(ctx *Ctx, container, key Value) Value {
	v, err := readDim(ctx, container, key, false)
	if err != nil {
		// Unreachable for an array subject; answering null rather than
		// panicking keeps a lowering defect from becoming a crash in generated
		// code, and the corpus grades it as a wrong answer instead.
		return Null()
	}
	return v
}

// ReadDimArraySilent is the IS form on a known array: no undefined-key warning
// and no offset-on-a-non-container throw, because the subject is a container.
//
// It still takes the `*Ctx` and it is **not** pure: a float key deprecates
// under `??` too, which a probe settled rather than a reading of ArrayKey.
func ReadDimArraySilent(ctx *Ctx, container, key Value) Value {
	v, err := readDim(ctx, container, key, true)
	if err != nil {
		// Unreachable for an array subject, as in ReadDimArray.
		return Null()
	}
	return v
}

// CountArray is `count()` of an array: a length, with no Countable to ask.
func CountArray(v Value) Value { return Int(int64(v.Deref().Array().Len())) }

// StrLen is `strlen($s)`, which the compiler gives its own opcode -- so it
// never reaches the library table and needs an implementation here.
//
// It is **the ordinary string coercion followed by a byte count**, not a length
// with special rules: measured, `strlen(123)` is 3 and `strlen(new S)` runs
// `__toString`, while an array is `strlen(): Argument #1 ($string) must be of
// type string, array given` and a null deprecates on the way through. Every one
// of those comes from CoerceArgument, so the coercion is what is called rather
// than reimplemented.
func StrLen(ctx *Ctx, v Value, strict bool) (Value, error) {
	s, err := CoerceInternalArgument(ctx, v, "string", "strlen", 1, "string", strict)
	if err != nil {
		return Null(), err
	}
	return Int(int64(len(s.String()))), nil
}

// GetType is `gettype($v)`, also compiled to its own opcode.
//
// The names are **not** TypeName's: `gettype` answers `integer`, `double`,
// `boolean` and `NULL` where every diagnostic in this package says `int`,
// `float`, `bool` and `null`. Two vocabularies, and this is the older one --
// `get_debug_type` is the function that gives the other.
func GetType(v Value) Value {
	switch v.Deref().Kind() {
	case KindInt:
		return String("integer")
	case KindFloat:
		return String("double")
	case KindString:
		return String("string")
	case KindTrue, KindFalse:
		return String("boolean")
	case KindArray:
		return String("array")
	case KindObject:
		return String("object")
	case KindResource:
		return String("resource")
	}
	return String("NULL")
}

// overloadedElement is an **ArrayAccess reached as storage**: `$c["k"][] = 1`,
// `$r = &$c["k"]`, or `$c["k"]` passed to a by-reference parameter.
//
// It is the same shape as a `__get` reached the same way, with its own wording:
// `offsetGet` runs, and unless it returns by reference the write earns
// `Indirect modification of overloaded element of C has no effect` -- naming
// the **class**, not the offset -- and then lands on a copy. Measured for all
// three shapes, with the object untouched afterwards in every one.
//
// An `&offsetGet` hands back a real box and the write reaches what it named,
// which needs no declaration table to detect: the returned value simply holds
// one. A value that is itself an object is written through the handle and
// modifies no element, so it is silent too.
//
// handled=false means the object is not an ArrayAccess; the caller then raises
// whatever it raises for indexing a plain object.
func overloadedElement(ctx *Ctx, o *Object, key Value, appending bool) (*Ref, bool, error) {
	arg := key
	if appending {
		// `$c[] = v` asks with an explicit null, the same convention the write
		// uses -- fewer arguments would be a different call.
		arg = Null()
		// A WeakMap refuses the descent in its own words rather than asking
		// offsetGet(null), which would complain about the key instead.
		if _, weak := o.Internal.(*WeakMapStore); weak {
			return nil, true, RaiseAt(ctx, "Error", "Cannot append to WeakMap")
		}
	} else if slot, found := WeakMapSlot(o, key); found {
		// **A WeakMap's dimension handler hands out the entry itself**, not
		// a copy the way an overloaded offsetGet does: `$m[$o][] = 2`,
		// `$m[$o]++` and `$r = &$m[$o]` all reach the stored value (probed).
		// A missing key goes on to offsetGet, which refuses it in the
		// engine's words.
		return slot, true, nil
	}
	v, handled, err := offsetCall(ctx, o, OffsetGet, []Value{arg})
	if !handled || err != nil {
		return nil, handled, err
	}
	if r, boxed := v.RefOf(); boxed {
		return r, true, nil
	}
	held := v.Deref()
	if held.Kind() == KindObject {
		return NewRef(held), true, nil
	}
	ctx.Notice("Indirect modification of overloaded element of %s has no effect", DisplayName(o.ClassName))
	if err := ctx.TakePendingError(); err != nil {
		return nil, true, err
	}
	return NewRef(held.Copy()), true, nil
}

// isGeneratorClose recognises the close unwind after it has been dressed as a
// thrown object by the compiled unit, as well as in its bare form.
func isGeneratorClose(err error) bool {
	if errors.Is(err, ErrGeneratorClose) {
		return true
	}
	o, thrown := AsThrown(err)
	return thrown && o != nil && o.ClassName == GeneratorCloseClass
}
