package php_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The files whose exported functions are intrinsics -- operations that *drive*
// the runtime -- as opposed to constructors, predicates and the library surface.
//
// Listing the files rather than guessing from names is deliberate: a rule like
// "anything taking a Ctx" would have been wrong in both directions, and a list
// that has to be edited when a file is added is a place where the decision gets
// made consciously.
var intrinsicFiles = []string{"ops.go", "incdec.go", "coerce.go", "intrinsics.go", "array.go", "iter.go"}

// notIntrinsic are exported functions in those files that build or inspect
// values rather than driving anything. Each is here because it was considered.
var notIntrinsic = map[string]string{
	"Increment": "the `++` table, called by the generated unit's stepIncrement -- which is " +
		"where the intrinsic lives, because refusing to step an array or an object is a " +
		"TypeError naming the value's class and this package reports that as a sentinel. " +
		"The narrow int and numeric variants are bound there too rather than straight here: " +
		"an operation has one home, which is what this test enforces two checks above.",
	"Decrement": "as Increment",
	"SnapIter": "not an operation of its own: the by-value array case of iter.valid, iter.current " +
		"and iter.next, tested by the emitter at each step before the general Iterator* call.",
	"PackedAppend": "not an operation of its own: dim.append's push case, tested by the emitter " +
		"at the site before AppendDim.",
	"PackedSet": "not an operation of its own: the write-back of an element read-modify-write, " +
		"tested by the emitter at the site before the place's general write.",
	"PackedAt": "not an operation of its own: dim.get's packed-list case, which the emitter " +
		"tests at the call site before calling ReadDim or ReadDimArray, because neither fits " +
		"the inlining budget. The IR still says dim.get.",
	"Hold": "not an operation of its own: the root of a read-write dim chain, taken by the " +
		"emitter so WarnUndefinedKeyHeld can tell whether an error handler let go of it.",
	"WarnUndefinedKeyHeld": "not an operation of its own: the missing-key warning of a " +
		"read-write dim fetch (the place's getMod), holding its array through the handler; " +
		"the IR still says the compound assignment or step it belongs to.",
	"WriteDimIn": "not an operation of its own: dim.set through a nested target, which the " +
		"emitter picks when it renders the chain and can hand over the array the chain was " +
		"reached from; the IR still says dim.set.",
	"WriteDimKeyVar": "not an operation of its own: dim.set whose key or value is a read the " +
		"write makes itself, in the engine's order, which the emitter picks for a read_var " +
		"placed just before it with no other consumer; the IR still says dim.set.",
	"StoreVarShared": "not an operation of its own: write_var's binding, StoreVar, without the " +
		"copy. The emitter picks it for a store the ownership trace proved no one can tell " +
		"from a copy (OWNERSHIP-PERF.md P3a); the IR still says write_var.",

	"CheckPropertyAsymmetric": "the `(set)` half of PHP 8.4's asymmetric visibility, asked by " +
		"three operations with three different verbs -- the property setter (`modify`), the " +
		"unset intrinsic (`unset`) and the generated unit's place-ref (`indirectly modify`). " +
		"It is a rule those three share, not a fourth operation with a home of its own.",

	"CheckRefStep": "the step half of write_var, asked *before* it because the write cannot " +
		"tell a step from an assignment -- `$r++` on a reference bound to a typed property " +
		"lowers to a plain write_var. The emitter knows, having just rendered the increment, " +
		"so it emits this guard ahead of the store. Same shared-rule status as CoerceRefWrite " +
		"beside it, not a second home for the write.",

	"StepRefusesOverflow": "a property of a *declaration*, not an operation: whether it " +
		"admits the float an integer step produces at the ends of the range. Both write paths " +
		"that can see a step ask it -- the generated unit's property write and, later, its " +
		"reference write -- so it is a rule they share rather than a third operation. It lives " +
		"beside the coercion rules because that is what it reads, and deliberately *not* as a " +
		"coercion attempt: the underflow float is exactly PHP_INT_MIN, so coercing it succeeds " +
		"where the step must fail.",

	"CoerceRefWrite": "the typed-reference half of write_var, which is where that operation lives. " +
		"A reference bound to a typed property carries the property's declaration, and the check " +
		"is shared with the interpreter's own ref-write path rather than being a second operation.",

	"StringOffsetIndex": "isset's rule for a string offset key, which is neither the read's " +
		"nor the array's -- `isset($s[\"1x\"])` is false where `$s[\"1x\"]` warns and yields a " +
		"character. Reached from the generated unit's existsIn, which is where the isset " +
		"intrinsic lives, so the conversion is a shared rule rather than a second home for it.",

	"ArgLabel": "how a diagnostic spells one argument's position -- `Argument #2 ($array)`, " +
		"or the position alone for a variadic slot, which the engine never names. It is the " +
		"wording shared by the two argument checks that live above this package (the " +
		"interpreter's verifyBuiltinArgs and the generated unit's), not an operation.",

	"CoerceInternalArgument": "CoerceArgument for an internal callee: the same rule with no " +
		"`, called in ...` suffix, which PHP appends for a userland callee and never for an " +
		"internal one. Excluded for the same reason CoerceArgument is -- it is reached from " +
		"the generated unit, not from an opcode.",
	"CoerceLibraryArgument": "CoerceInternalArgument for a library function compiled from " +
		"the userland library, checked in its own prologue: it only moves the location to the " +
		"caller's frame first. Reached from the generated unit's parameter binding, as " +
		"CoerceArgument is.",

	"CoerceProperty": "the typed-property half of the coercion rule, called by the generated " +
		"unit's propSet -- which is where `prop.set` lives, because the declared type and the " +
		"declaring class come from this program's class table. CoerceArgument is excluded for " +
		"the same reason; only CoerceReturn is reached directly, as `verify_return`.",

	"NewIterator": "the foreach constructor's home is the generated unit: a plain object " +
		"iterates the properties visible from the calling scope, and this package has no " +
		"scope and no class table. This is the value-level half the unit's newIterator calls.",
	"NewRefIterator": "as NewIterator: called by the unit's newRefIterator, the by-reference reset",
	"NewObjectRefIterator": "not an operation of its own: iter.new.ref's cursor over a plain object's " +
		"property slots, built by the generated newRefIterator, which owns the class table.",
	"SetRefWalkScope": "not an operation of its own: records iter.new.ref's class scope on the cursor, " +
		"for the property slots a re-read object shows (Iter.follow).",
	"RefSubject": "wraps the subject the emitter hands newRefIterator when a by-reference foreach walks a variable; not an opcode of its own",
	"NewArrayIterator": "as NewIterator: called by the unit's newArrayIterator, which is " +
		"where iter.new.array lives so that the family keeps one home.",
	"NewTraversableIterator": "as NewIterator: the unit's newIterator hands it the Traversable it " +
		"built for an object with property hooks, whose reads need the unit's scope.",
	"CallableKeyword": "a question about a callable's spelling, not an operation: is_callable " +
		"and the callable parameter check ask it to deprecate a keyword class.",
	"DisplayType": "a message's spelling of a declared type, not an operation: the coercions " +
		"call it to name an anonymous class as the engine's C-string formatting does.",

	"LooseEqualChecked":  "the Checked variant reports comparability; the rule is LooseEqual's",
	"StrictEqualChecked": "same",
	"Uncomparable":       "a predicate over a comparison result",
	"AcceptsArithmetic":  "the diagnostic half of the arithmetic walk, not an operation",
	"ValueName":          "how a diagnostic names a value; a spelling, not an operation",
	"Concat":             "joins two string forms; ConcatValues is the intrinsic, and converts first",
	"NewArrayIter":       "a constructor NewIter picks; the intrinsic is IterReset, which chooses between backings",
	"NewArrayRefIter": "NewArrayIter's by-reference twin, picked the same way: `foreach ($a as &$v)` " +
		"holds the live array rather than a snapshot. The intrinsic is still IterReset",
	"NewIter":           "builds the cursor; NewIterator is the intrinsic, and wraps it in the value the IR carries",
	"WarnPrecisionLoss": "a diagnostic, catalogued in diagnostics.go",
	"TypeName":          "names a type; drives nothing",
	"IncDecTypeName":    "same",
	"ParseDeclaredType": "parses a type declaration; the operation is CoerceArgument",
	"CheckClassConstantType": "a class constant's declared type, checked by the generated " +
		"unit's classConst where the constant is read -- there is no opcode for it, and the " +
		"declaring class it resolves `self` against comes from the unit's class table. " +
		"Excluded for the same reason CoerceProperty is.",

	"WidenDefault": "the one conversion a declared type performs on an *initializer*, which " +
		"is not an instruction: there is no opcode for `public float $x = 2`. The generated " +
		"unit applies it where it declares a property, a static, a class constant or a " +
		"parameter default, for the same reason CoerceProperty lives there.",
	"IsThrowableName": "asks whether a name is one the throwable hierarchy answers about",
	"ThrowableNames":  "lists the engine's throwable names, including the interface",
	"ObjectCastProps": "the value half of `(object)$x`; the operation is ToObject, whose " +
		"handle the generated unit numbers -- the same split CloneObject has",
	"CloneObject": "the value half of `clone $o`; the operation needs `__clone` and a " +
		"handle, both of which belong to the layer with a class table and an object registry",
	"BindDim": "the element arm of `$a[$k] =& ...`; the operation is BindRef, and this " +
		"rebinds where WriteDim writes through",
	"DimRef": "the box behind a dimension, for `$r = &$a[\"k\"]`; the operation is BindRef, " +
		"and this is how the emitted place reaches a box rather than an operation of its own",
	"DimTarget": "opens the storage a **nested** write descends into; the operation is " +
		"still WriteDim, and this is how the emitted place reaches a slot rather than an " +
		"operation of its own",
	"UnsetDim": "the object arm of `unset($c[$k])`, not an operation of its own: the " +
		"array arm is the emitted place's, and this answers only when an ArrayAccess is " +
		"indexed. The hop it calls is the consumer's, so a catalogue entry would describe " +
		"an effect contract this package cannot honour alone.",
	"ExistsDim":     "same, for `isset($c[$k])` and `empty($c[$k])`",
	"Arg":           "reads the nth argument out of a slice",
	"ConstArray":    "builds an array from values",
	"SetSubclassOf": "installs a hook",
	"IntKey":        "builds an array key from an int that is already one",
	"StringKey":     "builds an array key from a string that is already one",
	"NewArray":      "allocates an array",
}

// TestCatalogueCoversTheIntrinsicSurface fails when an intrinsic exists with no
// type-aware definition.
//
// This is the whole point of the catalogue being a table: prose cannot be
// checked against the code, and `INTRINSICS.md` proved it by describing the
// wrong layer for a year without anything noticing.
func TestCatalogueCoversTheIntrinsicSurface(t *testing.T) {
	// A signature is satisfied when an intrinsic file exports **any** name it
	// answers to: its own, or one of its variants' Go bindings. Matching on the
	// signature name alone required the two to be spelled identically, which
	// they are not once an intrinsic has a PHP-facing name and a Go one --
	// `IterReset` calls `NewIter`.
	answersTo := map[string][]string{}
	seenName := map[string]bool{}
	for _, sig := range php.Catalogue() {
		if _, twice := answersTo[sig.Name]; twice {
			t.Errorf("%s is described twice", sig.Name)
		}
		// A Local intrinsic is implemented by the generated unit, because
		// answering it needs the program's class table. It is still an
		// intrinsic -- property access is core runtime behaviour -- but it is
		// not in this package, so looking for it here would report a hole that
		// is not one. All-or-nothing: a signature with some Local variants and
		// some not would mean one operation with two homes, which is a
		// mistake worth failing on.
		local, nonLocal := 0, 0
		names := []string{sig.Name}
		for _, v := range sig.Variants {
			if v.Local {
				local++
				continue
			}
			nonLocal++
			if v.Go != "" {
				names = append(names, v.Go)
			}
		}
		if local > 0 && nonLocal > 0 {
			t.Errorf("%s has both Local and runtime/php variants; an operation has one home", sig.Name)
		}
		if local > 0 {
			continue
		}
		answersTo[sig.Name] = names
	}
	described := map[string]bool{}
	for _, names := range answersTo {
		for _, n := range names {
			described[n] = true
		}
	}

	root := "."
	for _, name := range intrinsicFiles {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("intrinsic file %s is gone: the list needs updating", name)
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() {
				continue
			}
			n := fn.Name.Name
			if described[n] {
				seenName[n] = true
				continue
			}
			if why, excluded := notIntrinsic[n]; excluded {
				if why == "" {
					t.Errorf("%s is excluded with no reason", n)
				}
				continue
			}
			t.Errorf("%s in %s is an intrinsic with no catalogue entry, and is not "+
				"listed as excluded — decide which it is", n, name)
		}
	}
	for sig, names := range answersTo {
		covered := false
		for _, n := range names {
			if seenName[n] {
				covered = true
			}
		}
		if !covered {
			t.Errorf("the catalogue describes %s, which no intrinsic file exports "+
				"under any of %v", sig, names)
		}
	}
}

// TestCatalogueReportsItsOwnCoverage prints how much of the surface is backed by
// evidence rather than intent.
//
// It never fails on the ratio. A catalogue that forced itself green would be
// filled in by guessing, which is exactly the failure it exists to prevent --
// the honest state is the one worth seeing.
func TestCatalogueReportsItsOwnCoverage(t *testing.T) {
	byGroup := map[string][2]int{}
	for _, s := range php.Catalogue() {
		c := byGroup[s.Group]
		c[1]++
		if s.Verified {
			c[0]++
		}
		byGroup[s.Group] = c
	}
	groups := make([]string, 0, len(byGroup))
	for g := range byGroup {
		groups = append(groups, g)
	}
	var b strings.Builder
	total, verified := 0, 0
	for _, g := range groups {
		c := byGroup[g]
		verified += c[0]
		total += c[1]
		b.WriteString("\n  " + g + ": " + itoa(c[0]) + "/" + itoa(c[1]) + " verified")
	}
	t.Logf("intrinsic contracts read back from the engine: %d/%d%s", verified, total, b.String())

	// What *is* enforced: an entry claiming to be verified must say something
	// specific enough to have been checked.
	for _, s := range php.Catalogue() {
		if s.Verified && s.Note == "" {
			t.Errorf("%s claims Verified but records no rule; an entry that says "+
				"nothing specific cannot have been read back from anything", s.Name)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestVariantSelectionNarrowsByOperandType exercises the mechanism directly.
//
// It is tested here rather than through the emitter because the emitter cannot
// currently reach the narrow variants: it knows only *representation* types, and
// the one PHP type it can prove -- an integer literal -- is constant-folded by
// the compiler before it arrives. So the machinery is in place and unexercised
// end to end, which is worth stating rather than implying.
//
// What makes that acceptable rather than dead code: selection **falls back**.
// An operand set with no information selects the general variant, so a consumer
// without inference is correct, and inference narrows the choice later without
// the consumer learning anything new.
func TestVariantSelectionNarrowsByOperandType(t *testing.T) {
	add, ok := php.Lookup("Add")
	if !ok {
		t.Fatal("Add is not catalogued")
	}

	narrow := add.Select([]php.Type{php.Of(php.MaskInt), php.Of(php.MaskInt)})
	if narrow.Effects != php.Pure {
		t.Errorf("int+int selected a variant with effects %v; two ints cannot diagnose", narrow.Effects)
	}
	if narrow.Guard != "" {
		t.Errorf("int+int selected a variant owing the guard %q, which it cannot need", narrow.Guard)
	}

	general := add.Select([]php.Type{php.Of(php.MaskAny), php.Of(php.MaskAny)})
	if general.Guard == "" {
		t.Error("the general variant does not owe the diagnostic walk, so nothing would emit it")
	}

	// A *partly* known operand set must not select the narrow variant: knowing
	// one side is an int says nothing about what the other can do.
	half := add.Select([]php.Type{php.Of(php.MaskInt), php.Of(php.MaskAny)})
	if half.Guard == "" {
		t.Error("one known operand was enough to drop the walk, which is unsound")
	}

	// Selection is by subset, so a mask *narrower* than the variant's still
	// matches -- this is what lets inference report `int|float` and still pick
	// a numeric variant. It must pick the **numeric** one, not the int-only
	// one: int|float is not a subset of int.
	//
	// This asserted the selected variant had a guard, which was a proxy for
	// "not the int variant" that stopped holding the moment a *second*
	// guardless variant existed. Naming the variant says what is meant.
	if got := add.Select([]php.Type{php.Of(php.MaskInt), php.Of(php.MaskInt | php.MaskFloat)}); got.Name != "add.numeric" {
		t.Errorf("int|float selected %s; it is not a subset of int", got.Name)
	}
}

// TestEveryVariantNamesAnImplementedFunction stops the catalogue naming a Go
// function that does not exist, which would be a build failure in every
// generated program rather than here.
func TestEveryVariantNamesAnImplementedFunction(t *testing.T) {
	exported := map[string]bool{}
	for _, name := range intrinsicFiles {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.IsExported() {
				exported[fn.Name.Name] = true
			}
		}
	}
	// A Local variant's function is emitted into the generated program by
	// internal/gogen, so it is checked against that prelude instead.
	prelude, err := os.ReadFile(filepath.Join("..", "..", "internal", "gogen", "gogen.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range php.Catalogue() {
		for _, v := range s.Variants {
			if v.Local {
				if !strings.Contains(string(prelude), "func "+v.Go+"(") {
					t.Errorf("%s has a Local variant naming %s, which gogen's prelude does not define",
						s.Name, v.Go)
				}
				continue
			}
			if !exported[v.Go] {
				t.Errorf("%s has a variant naming %s, which no intrinsic file exports", s.Name, v.Go)
			}
			if v.Guard != "" && !exported[v.Guard] {
				t.Errorf("%s has a variant guarded by %s, which no intrinsic file exports", s.Name, v.Guard)
			}
		}
	}
}

// TestGoBindingShapeIsDescribed catches a variant whose Go function does not
// return a Value without saying so.
//
// The catalogue describes **PHP** types, but the emitter translates through the
// **Go** binding, and the two are not the same statement: `Result: MaskBool`
// says the operation yields a PHP bool, not whether the Go function hands back
// `true` or `php.Bool(true)`. Getting it wrong is a build failure in every
// generated program, which is exactly the kind of thing that should fail here.
func TestGoBindingShapeIsDescribed(t *testing.T) {
	returnsGoScalar := map[string]string{}
	for _, name := range intrinsicFiles {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() || fn.Type.Results == nil {
				continue
			}
			if len(fn.Type.Results.List) == 0 {
				continue
			}
			// Any bare Go type that is not a Value, not only bool. Checking
			// for `bool` alone is what let `Compare`'s `int` go undescribed:
			// the field enumerated one shape and so did its test.
			if id, isIdent := fn.Type.Results.List[0].Type.(*ast.Ident); isIdent && id.Name != "Value" {
				returnsGoScalar[fn.Name.Name] = id.Name
			}
			if len(fn.Type.Results.List) > 1 {
				// (Value, error) and friends are described by Ret, not by
				// GoScalar: the first result is the value.
				delete(returnsGoScalar, fn.Name.Name)
			}
		}
	}
	for _, s := range php.Catalogue() {
		for _, v := range s.Variants {
			if v.Ret == "err" || v.Ret == "errno" || v.Ret == "none" {
				// The return shape is stated by Ret. GoScalar answers a
				// different question -- how to wrap a plain Go value -- and
				// asking it of a binding that returns an error or nothing gets
				// a nonsense answer.
				continue
			}
			if returnsGoScalar[v.Go] != v.GoScalar {
				t.Errorf("variant %s calls %s, which returns Go %q, but GoScalar = %q",
					v.Name, v.Go, returnsGoScalar[v.Go], v.GoScalar)
			}
		}
	}
}

// TestIntArithmeticCanYieldAFloat.
//
// `PHP_INT_MAX + 1` is a float, and so are `$m * 2` and `PHP_INT_MIN - 1`.
// `add.int`, `sub.int` and `mul.int` claimed MaskInt, which is a lattice that
// lies -- and a lattice that lies eventually selects a variant on a type the
// value does not have. Bitwise is the genuine exception: `PHP_INT_MAX << 1` is
// int(-2), so those stay MaskInt. Both halves settled with the engine.
func TestIntArithmeticCanYieldAFloat(t *testing.T) {
	overflows := map[string]bool{"add.int": true, "sub.int": true, "mul.int": true, "pow.int": true}
	staysInt := map[string]bool{"bit_and.int": true, "bit_or.int": true, "bit_xor.int": true}
	for _, v := range php.Variants() {
		switch {
		case overflows[v.Name]:
			if v.Result.Mask&php.MaskFloat == 0 {
				t.Errorf("%s cannot yield a float, but int arithmetic overflows to one", v.Name)
			}
		case staysInt[v.Name]:
			if v.Result.Mask != php.MaskInt {
				t.Errorf("%s = %v; bitwise on two ints is always an int", v.Name, v.Result)
			}
		}
	}
}
