package interp

import (
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// Some constant initializers cannot be folded when the file is compiled:
// `const b = a[0];` depends on a constant that does not exist until the
// declaration before it runs, and `const A = self::B;` on a class constant. The
// engine keeps those as an AST and evaluates them on first use; the harvest
// carries the shape, and this evaluates it.
//
// Evaluation has three outcomes rather than two. A value; a shape the harvest
// could not read, reported as unresolvable so the gap stays visible instead of
// becoming a silent null; and a PHP error, which a constant expression can
// raise as readily as any other code -- reading a property off a non-enum is
// one, and it has to reach the program as a throw rather than as a gap.

// maxConstDepth bounds how far a constant initializer may refer through other
// deferred constants. Real ones nest two or three deep; a cycle does not stop.
const maxConstDepth = 64

// constExpressionFrame is what a backtrace calls the evaluation of an
// initializer the compiler could not fold. It is not a function and has no
// name of its own; the engine writes this in its place.
const constExpressionFrame = "[constant expression]"

// evaluatedInOwnFrame reports whether an initializer is evaluated as a constant
// expression in its own right. A single bare constant fetch is not: the engine
// evaluates that one without moving the reported position to the declaration,
// so an undefined constant in it is reported against the *use* site and adds no
// frame, while `FOO + 0` in the same place is reported against the declaration
// and does. Probed both ways, for class constants and property defaults alike.
func evaluatedInOwnFrame(value *pb.Literal) bool {
	if value.GetKind() != pb.Literal_KIND_EXPR {
		return false
	}
	return value.GetExpression().GetKind() != "constant"
}

// enterConstExpression moves the reported position to where the initializer was
// written and opens the frame a constant expression is evaluated in. Both come
// from the same mechanism in the engine: a filename/line override is in force
// for the evaluation, and a backtrace taken under one gains a synthetic
// `[constant expression]` entry recording where the program actually is.
//
// That entry is synthesised only when the two positions differ, and only once
// however many constants the evaluation reaches through -- so a nested
// evaluation moves the position again but adds no second frame.
func (u *Unit) enterConstExpression(ctx *php.Ctx, file string, line uint32) func() {
	fromFile, fromLine := ctx.Location()
	u.inConstExpression++
	pushed := u.inConstExpression == 1 && (file != fromFile || line != fromLine)
	if pushed {
		ctx.PushCall(php.StackFrame{Function: constExpressionFrame, File: fromFile, Line: fromLine})
	}
	if file != "" && line != 0 {
		ctx.SetLocation(file, line)
	}
	return func() {
		u.inConstExpression--
		if pushed {
			ctx.PopCall()
		}
		ctx.SetLocation(fromFile, fromLine)
	}
}

// evalExpr evaluates a harvested initializer. scope is the class whose body the
// initializer belongs to, which is what self:: means inside one.
func (in *Interp) evalExpr(ctx *php.Ctx, unit *Unit, e *pb.Expr, scope *Class, depth int) (php.Value, bool, error) {
	switch e.GetKind() {
	case "dim", "prop", "nullsafe_prop":
		// These carry a short circuit along the chain; every other node ends
		// one, so what happened inside it does not escape.
	default:
		defer func() { unit.constShortCircuit = false }()
	}
	if e == nil {
		return php.Undef(), false, nil
	}
	child := func(i int) *pb.Expr {
		if i < len(e.GetChildren()) {
			return e.GetChildren()[i]
		}
		return nil
	}

	switch e.GetKind() {
	case "literal":
		return in.constantLiteral(ctx, unit, e.GetValue(), scope, depth)

	case "constant":
		name := e.GetName()
		if name == "" {
			// ZEND_AST_CONST keeps the name in a child rather than inline.
			resolved, ok, err := in.evalExpr(ctx, unit, child(0), scope, depth)
			if !ok || err != nil {
				return php.Undef(), false, err
			}
			name = resolved.String()
		}
		value, ok := in.lookupConstant(ctx, name)
		if !ok {
			// An unqualified constant inside a namespace falls back to the
			// global one, exactly as the FETCH_CONSTANT opcode does.
			//
			// The opcode can tell the two spellings apart from a flag the
			// compiler puts on the opline. A *folded* constant expression --
			// a parameter default, a property default -- carries no such flag
			// that the harvest can find, so php2go falls back for both. A name
			// written out with its namespace and genuinely undefined therefore
			// resolves to a global of the same short name if one exists, which
			// PHP would not do. Recorded in TODO rather than guessed at.
			if at := strings.LastIndexByte(name, '\\'); at >= 0 {
				value, ok = in.lookupConstant(ctx, name[at+1:])
			}
		}
		if !ok {
			// A name no constant table knows is the program's error, not a gap
			// -- and a catchable one, which is what these tests check.
			return php.Undef(), false, in.throwError(ctx, unit, "Error",
				"Undefined constant \"%s\"", name)
		}
		in.noteDeprecatedConstantName(ctx, unit, name)
		return value, true, nil

	case "class_constant":
		className, ok, err := in.exprClassName(ctx, unit, child(0), scope, depth)
		if !ok || err != nil {
			return php.Undef(), false, err
		}
		member, ok, err := in.evalExpr(ctx, unit, child(1), scope, depth)
		if !ok || err != nil {
			return php.Undef(), false, err
		}
		// The engine moves the reported position to this fetch before making
		// it, so a diagnostic from resolving the class or from the constant
		// being read names the line the *reference* is on rather than the line
		// the initializer starts on -- which for a multi-line initializer are
		// different, and gh7771_2 is exactly that shape.
		if scope != nil {
			leave := unit.enterConstExpression(ctx, scope.decl.GetFilename(), e.GetLine())
			defer leave()
		}
		_, found, err := in.resolveClass(ctx, unit, className, depth)
		if err != nil {
			return php.Undef(), false, err
		}
		if !found {
			return php.Undef(), false, in.throwError(ctx, unit, "Error",
				"Class \"%s\" not found", className)
		}
		// A constant expression that reaches a constant already being evaluated
		// has nowhere to bottom out. The engine names the fetch as it is
		// written -- `self::B` rather than the class it resolves to -- so the
		// unresolved form is kept for the message.
		//
		// The mark goes on *after* the class is resolved, which matters: an
		// autoloader run by that resolution may reach the same constant
		// legitimately, and marking first would call that a cycle.
		visiting := strings.ToLower(className) + "::" + member.String()
		if unit.visitingConstant[visiting] {
			written, _, _ := in.evalExpr(ctx, unit, child(0), scope, depth)
			return php.Undef(), false, in.throwError(ctx, unit, "Error",
				"Cannot declare self-referencing constant %s::%s",
				written.String(), member.String())
		}
		if unit.visitingConstant == nil {
			unit.visitingConstant = map[string]bool{}
		}
		unit.visitingConstant[visiting] = true
		defer delete(unit.visitingConstant, visiting)
		value, found, err := in.classConstant(ctx, unit, className, member.String(), depth)
		if err != nil {
			return php.Undef(), false, err
		}
		if !found {
			// PHP leaves a class constant unquoted here, unlike the plain form,
			// and names the class as it was written rather than as it resolves.
			written, _, _ := in.evalExpr(ctx, unit, child(0), scope, depth)
			return php.Undef(), false, in.throwError(ctx, unit, "Error",
				"Undefined constant %s::%s", written.String(), member.String())
		}
		return value, true, nil

	case "class_scope":
		// `__CLASS__` in a constant expression, which a trait leaves unfolded
		// because the answer belongs to whichever class composed it. Outside a
		// class it is the empty string, not an error.
		if scope == nil {
			return php.String(""), true, nil
		}
		return php.String(scope.Name), true, nil

	case "class_name":
		if len(e.GetChildren()) == 0 || e.GetChildren()[0].GetKind() == "none" {
			// `self::class` and friends where the compiler could not fold them
			// -- inside a trait, whose answer depends on the composing class.
			// The keyword is in the node's attribute rather than in a child,
			// the same ZEND_FETCH_CLASS_* encoding the opcodes use.
			if scope == nil {
				// The constant-expression wording differs from the opcode's:
				// "use" here, "access" there. Both are the engine's own.
				return php.Undef(), false, in.throwError(ctx, unit, "Error",
					"Cannot use \"self\" when no class scope is active")
			}
			switch e.GetOp() & fetchClassMask {
			case fetchClassParent:
				if scope.Parent == "" {
					return php.Undef(), false, in.throwError(ctx, unit, "Error",
						"Cannot use \"parent\" when current class scope has no parent")
				}
				return php.String(scope.Parent), true, nil
			}
			return php.String(scope.Name), true, nil
		}
		className, ok, err := in.exprClassName(ctx, unit, child(0), scope, depth)
		if !ok || err != nil {
			return php.Undef(), false, err
		}
		return php.String(className), true, nil

	case "dim":
		unit.constShortCircuit = false
		container, ok, err := in.evalExpr(ctx, unit, child(0), scope, depth)
		if !ok || err != nil {
			return php.Undef(), false, err
		}
		if unit.constShortCircuit {
			return php.Null(), true, nil
		}
		if container.Deref().Kind() == php.KindObject {
			// An ArrayAccess object is reachable here -- an enum case may
			// implement it -- but a constant expression cannot call a method,
			// so the engine refuses outright. Probed: it refuses before the
			// index is evaluated, so an undefined constant in the index is
			// never reported.
			return php.Undef(), false, in.throwError(ctx, unit, "Error",
				"Cannot use [] on objects in constant expression")
		}
		index, ok, err := in.evalExpr(ctx, unit, child(1), scope, depth)
		if !ok || err != nil {
			return php.Undef(), false, err
		}
		element, readErr := dimensionRead(ctx, container.Deref(), index, false)
		if readErr != nil {
			// An offset the container cannot take is the program's error and
			// reaches it as one. Only a *warning* -- a key that is simply not
			// there -- leaves the value resolved.
			return php.Undef(), false, in.materialize(ctx, unit, readErr)
		}
		return element, true, nil

	case "prop", "nullsafe_prop":
		return in.evalConstProp(ctx, unit, e, scope, depth)

	case "new":
		return in.evalConstNew(ctx, unit, e, scope, depth)

	case "array":
		array := php.NewArray()
		for _, element := range e.GetChildren() {
			if element.GetKind() == "unpack" {
				spread, ok, err := in.evalExpr(ctx, unit, element.GetChildren()[0], scope, depth)
				if !ok || err != nil {
					return php.Undef(), false, err
				}
				if err := in.spreadConstant(ctx, unit, array, spread.Deref()); err != nil {
					return php.Undef(), false, err
				}
				continue
			}
			if element.GetKind() != "element" {
				return php.Undef(), false, nil
			}
			// The key is evaluated first, even though the AST holds it second:
			// `[UNKNOWN_KEY => UNKNOWN_VALUE]` reports the key. The order is
			// only observable when both sides fail, which a constant
			// expression makes easy.
			key := element.GetChildren()[1]
			var resolved php.Value
			if key.GetKind() != "none" {
				var ok bool
				var err error
				resolved, ok, err = in.evalExpr(ctx, unit, key, scope, depth)
				if !ok || err != nil {
					return php.Undef(), false, err
				}
			}
			value, ok, err := in.evalExpr(ctx, unit, element.GetChildren()[0], scope, depth)
			if !ok || err != nil {
				return php.Undef(), false, err
			}
			if key.GetKind() == "none" {
				if !array.CanAppend() {
					// The largest integer key is taken, so there is no next one
					// -- the same refusal a runtime array literal earns.
					return php.Undef(), false, in.materialize(ctx, unit, Raise("Error",
						"Cannot add element to the array as the next element is already occupied"))
				}
				array.Append(value)
				continue
			}
			// An enum case is the one object a constant expression can produce
			// for a key, and the engine refuses it in the same words a runtime
			// subscript earns.
			if offending := illegalOffsetType(resolved); offending != "" {
				return php.Undef(), false, in.materialize(ctx, unit, Raise("TypeError",
					"Cannot access offset of type %s on array", offending))
			}
			array.Set(php.ArrayKey(ctx, resolved), value)
		}
		return php.Arr(array), true, nil

	case "binary":
		left, right, ok, err := in.evalOperands(ctx, unit, child(0), child(1), scope, depth)
		if !ok || err != nil {
			return php.Undef(), false, err
		}
		// The opcode number comes from the harvest and is resolved through the
		// manifest's table, so nothing here assumes a numbering.
		name := in.name(e.GetOp())
		if strings.HasPrefix(name, "ZEND_IS_") {
			// Comparing an object with a string renders it through __toString,
			// whose declared return type is enforced -- a body that returns
			// nothing is a TypeError here as much as at runtime.
			left, right, err = in.renderForComparison(ctx, unit, left, right)
			if err != nil {
				return php.Undef(), false, in.materialize(ctx, unit, err)
			}
			return php.Bool(compare(name, left, right)), true, nil
		}
		if name == "ZEND_CONCAT" {
			// Concatenation renders its operands, so an object goes through
			// __toString -- `new Foo() . ""` as a parameter default is
			// evaluated per call and is expected to run it.
			left, err = in.toString(ctx, unit, left)
			if err == nil {
				right, err = in.toString(ctx, unit, right)
			}
			if err != nil {
				return php.Undef(), false, in.materialize(ctx, unit, err)
			}
		}
		// An operation PHP refuses is refused here too, in the same words --
		// and the same check reports a non-numeric string operand, which the
		// runtime path has always done and this one silently skipped.
		if err := in.checkOperands(ctx, unit, name, left, right); err != nil {
			return php.Undef(), false, err
		}
		// The same table the compound-assignment path uses. An operator with
		// no entry is an honest gap: silently concatenating instead turned
		// `1 % 1.5` into the string "11.5".
		value, handled, err := in.applyBinary(ctx, unit, name, left, right)
		if err != nil {
			return php.Undef(), false, err
		}
		if !handled {
			return php.Undef(), false, &UnsupportedError{
				Opcode: name + " in a constant expression",
			}
		}
		return value, true, nil

	case "greater", "greater_equal":
		left, right, ok, err := in.evalOperands(ctx, unit, child(0), child(1), scope, depth)
		if !ok || err != nil {
			return php.Undef(), false, err
		}
		name := "ZEND_IS_SMALLER"
		if e.GetKind() == "greater_equal" {
			name = "ZEND_IS_SMALLER_OR_EQUAL"
		}
		left, right, err = in.renderForComparison(ctx, unit, left, right)
		if err != nil {
			return php.Undef(), false, in.materialize(ctx, unit, err)
		}
		// The operands swap: the language has no greater-than comparison.
		return php.Bool(compare(name, right, left)), true, nil

	case "and", "or":
		left, ok, err := in.evalExpr(ctx, unit, child(0), scope, depth)
		if !ok || err != nil {
			return php.Undef(), false, err
		}
		if left.Bool() == (e.GetKind() == "or") {
			// Short-circuit: the right side is not evaluated at all.
			return php.Bool(left.Bool()), true, nil
		}
		right, ok, err := in.evalExpr(ctx, unit, child(1), scope, depth)
		if !ok || err != nil {
			return php.Undef(), false, err
		}
		return php.Bool(right.Bool()), true, nil

	case "unary":
		value, ok, err := in.evalExpr(ctx, unit, child(0), scope, depth)
		if !ok || err != nil {
			return php.Undef(), false, err
		}
		switch in.name(e.GetOp()) {
		case "ZEND_BOOL_NOT":
			return php.Bool(!value.Bool()), true, nil
		case "ZEND_BW_NOT":
			return php.BitNot(value), true, nil
		}
		return php.Undef(), false, nil

	case "negate", "identity":
		value, ok, err := in.evalExpr(ctx, unit, child(0), scope, depth)
		if !ok || err != nil {
			return php.Undef(), false, err
		}
		// The engine compiles unary minus as `-1 * value`, in that order, which
		// an "Unsupported operand types" message shows: `int * string`.
		by := php.Int(-1)
		if e.GetKind() == "identity" {
			by = php.Int(1)
		}
		if err := in.checkOperands(ctx, unit, "ZEND_MUL", by, value); err != nil {
			return php.Undef(), false, err
		}
		return arith("ZEND_MUL", by, value), true, nil

	case "conditional":
		condition, ok, err := in.evalExpr(ctx, unit, child(0), scope, depth)
		if !ok || err != nil {
			return php.Undef(), false, err
		}
		if condition.Bool() {
			// `a ?: b` leaves the middle out, and the condition is the result.
			if child(1).GetKind() == "none" {
				return condition, true, nil
			}
			return in.evalExpr(ctx, unit, child(1), scope, depth)
		}
		return in.evalExpr(ctx, unit, child(2), scope, depth)

	case "coalesce":
		// `??` asks whether something is there, so reading it says nothing
		// about it being absent -- the whole point of the operator. In a
		// constant expression the left side is a dim or property fetch, and
		// the only diagnostics it can raise are those undefined-ness reports.
		previous := ctx.BeginSilence()
		left, ok, err := in.evalExpr(ctx, unit, child(0), scope, depth)
		ctx.EndSilence(previous)
		if err != nil {
			return php.Undef(), false, err
		}
		if ok && left.Kind() != php.KindNull && left.Kind() != php.KindUndef {
			return left, true, nil
		}
		return in.evalExpr(ctx, unit, child(1), scope, depth)
	}
	return php.Undef(), false, nil
}

// evalOperands evaluates the two sides of a binary node, which every arithmetic
// and comparison kind needs in the same way.
func (in *Interp) evalOperands(ctx *php.Ctx, unit *Unit, left, right *pb.Expr, scope *Class, depth int) (php.Value, php.Value, bool, error) {
	l, ok, err := in.evalExpr(ctx, unit, left, scope, depth)
	if !ok || err != nil {
		return php.Undef(), php.Undef(), false, err
	}
	r, ok, err := in.evalExpr(ctx, unit, right, scope, depth)
	if !ok || err != nil {
		return php.Undef(), php.Undef(), false, err
	}
	return l, r, true, nil
}

// evalConstProp reads a property inside a constant expression. Since PHP 8.3
// this is allowed, but only on an enum case -- `A::Case->value` is the point of
// the feature. Any other object is an Error, and a non-object is the same
// warning any property read on one produces.
func (in *Interp) evalConstProp(ctx *php.Ctx, unit *Unit, e *pb.Expr, scope *Class, depth int) (php.Value, bool, error) {
	unit.constShortCircuit = false
	container, ok, err := in.evalExpr(ctx, unit, e.GetChildren()[0], scope, depth)
	if !ok || err != nil {
		return php.Undef(), false, err
	}
	if unit.constShortCircuit {
		return php.Null(), true, nil
	}
	// `?->` on null yields null without a diagnostic, and skips the rest of the
	// chain -- including the expression naming the property, so `(null)?->{new
	// P}` never builds the P.
	if e.GetKind() == "nullsafe_prop" {
		if held := container.Deref(); held.Kind() == php.KindNull || held.Kind() == php.KindUndef {
			unit.constShortCircuit = true
			return php.Null(), true, nil
		}
	}
	name, ok, err := in.evalExpr(ctx, unit, e.GetChildren()[1], scope, depth)
	if !ok || err != nil {
		return php.Undef(), false, err
	}
	unit.constShortCircuit = false
	// `->{expr}` names the property with whatever the expression produced, and
	// that conversion happens before the container is judged: `(new A)->{new B}`
	// reports the object that cannot be a name, not the container that cannot
	// be read.
	named, err := in.toString(ctx, unit, name.Deref())
	if err != nil {
		return php.Undef(), false, in.materialize(ctx, unit, err)
	}
	name = named
	container = container.Deref()

	if container.Kind() == php.KindObject {
		object := container.Object()
		if object == nil || object.EnumCase == "" {
			return php.Undef(), false, in.throwError(ctx, unit, "Error",
				"Fetching properties on non-enums in constant expressions is not allowed")
		}
		value, found := object.Props.Get(php.StringKey(name.String()))
		if !found {
			ctx.Warn("Undefined property: %s::$%s", php.DisplayName(object.ClassName), name.String())
			return php.Null(), true, nil
		}
		return value.Deref(), true, nil
	}

	ctx.Warn("Attempt to read property \"%s\" on %s", name.String(), givenTypeName(container))
	return php.Null(), true, nil
}

// evalConstNew builds an object inside a constant expression. Since PHP 8.1
// `new` is allowed in a parameter default, a static variable, a global constant
// and an attribute argument, and the object is built each time the initializer
// runs rather than once.
func (in *Interp) evalConstNew(ctx *php.Ctx, unit *Unit, e *pb.Expr, scope *Class, depth int) (php.Value, bool, error) {
	className, ok, err := in.exprClassName(ctx, unit, e.GetChildren()[0], scope, depth)
	if !ok || err != nil {
		return php.Undef(), false, err
	}
	class, found, err := in.resolveClass(ctx, unit, className, depth)
	if err != nil {
		return php.Undef(), false, err
	}
	if !found {
		return php.Undef(), false, in.throwError(ctx, unit, "Error", "Class \"%s\" not found", className)
	}
	// The same rule the NEW opcode follows: an engine class the runtime does
	// not model would produce an object with no behaviour, which reads as a
	// wrong answer rather than as the gap it is.
	if unit.isBuiltinOnly(class) && !unit.isThrowable(class) && class.Name != "stdClass" &&
		!isArrayBackedClass(unit, class.Name) {
		return php.Undef(), false, nil
	}

	var args []php.Value
	if list := e.GetChildren()[1]; list.GetKind() == "args" {
		for _, argument := range list.GetChildren() {
			value, ok, err := in.evalExpr(ctx, unit, argument, scope, depth)
			if !ok || err != nil {
				return php.Undef(), false, err
			}
			args = append(args, value)
		}
	} else if list != nil && list.GetKind() != "none" {
		return php.Undef(), false, nil
	}

	object := unit.instantiate(class)
	if unit.isThrowable(class) {
		recordThrowSite(ctx, unit, object)
	}
	switch ctor, declaring, hasCtor := unit.resolveMethod(class, "__construct"); {
	case hasCtor:
		if _, err := in.invoke(ctx, unit, ctor, object, declaring, class, args, depth); err != nil {
			return php.Undef(), false, err
		}
	case unit.isThrowable(class):
		if err := throwableCtor(ctx, unit.throwableCtorScope(class), object, args); err != nil {
			return php.Undef(), false, err
		}
	case isArrayBackedClass(unit, class.Name):
		newArrayStore(object, args)
	}
	return php.Obj(object), true, nil
}

// constantLiteral turns a harvested literal into a value, evaluating it first
// when the compiler left it as a shape.
func (in *Interp) constantLiteral(ctx *php.Ctx, unit *Unit, lit *pb.Literal, scope *Class, depth int) (php.Value, bool, error) {
	if lit.GetKind() == pb.Literal_KIND_EXPR {
		// A constant naming itself is a cycle; refusing past a small depth
		// leaves it undefined rather than taking the stack down.
		if unit.constDepth >= maxConstDepth {
			return php.Undef(), false, nil
		}
		unit.constDepth++
		defer func() { unit.constDepth-- }()
		return in.evalExpr(ctx, unit, lit.GetExpression(), scope, depth)
	}
	if lit.GetKind() == pb.Literal_KIND_UNSPECIFIED || lit.GetKind() == pb.Literal_KIND_OTHER {
		return php.Undef(), false, nil
	}
	return literalValue(lit), true, nil
}

// unresolvedSuffix renders the missing shape for a gap message, or nothing.
func unresolvedSuffix(lit *pb.Literal) string {
	if shape := unresolvedShape(lit.GetExpression()); shape != "" {
		return " (" + shape + ")"
	}
	return ""
}

// unresolvedShape reports the first node the harvest could not read, so a gap
// in the corpus names the missing syntax rather than only the constant it
// stopped at. An initializer that failed for another reason -- a constant that
// does not exist -- has no such node and reports nothing.
func unresolvedShape(e *pb.Expr) string {
	if e == nil {
		return ""
	}
	if e.GetKind() == "unsupported" {
		return e.GetName()
	}
	for _, child := range e.GetChildren() {
		if found := unresolvedShape(child); found != "" {
			return found
		}
	}
	return ""
}

// exprClassName resolves the class half of a class-constant reference, where
// self, parent and static name the class the initializer was written in.
func (in *Interp) exprClassName(ctx *php.Ctx, unit *Unit, e *pb.Expr, scope *Class, depth int) (string, bool, error) {
	resolved, ok, err := in.evalExpr(ctx, unit, e, scope, depth)
	if !ok || err != nil {
		return "", false, err
	}
	name := resolved.String()
	switch strings.ToLower(name) {
	case "self", "static":
		if scope == nil {
			return "", false, nil
		}
		return scope.Name, true, nil
	case "parent":
		if scope == nil || scope.Parent == "" {
			return "", false, nil
		}
		return scope.Parent, true, nil
	}
	return name, true, nil
}

// spreadConstant merges `...$a` into a constant array being built. A constant
// expression is evaluated before there is anything to iterate, so unlike the
// runtime opcode this takes arrays only -- a Traversable is refused outright.
func (in *Interp) spreadConstant(ctx *php.Ctx, unit *Unit, into *php.Array, spread php.Value) error {
	if spread.Kind() != php.KindArray {
		return in.throwError(ctx, unit, "Error",
			"Only arrays can be unpacked in constant expression")
	}
	var failure error
	spread.Array().Each(func(k php.Key, v php.Value) bool {
		if !k.IsInt {
			into.Set(k, v.Copy())
			return true
		}
		if !into.CanAppend() {
			failure = in.throwError(ctx, unit, "Error",
				"Cannot add element to the array as the next element is already occupied")
			return false
		}
		into.Append(v.Copy())
		return true
	})
	return failure
}
