package gogen

import (
	"fmt"
	"strconv"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/internal/lower"
)

// Constant expressions the compiler could not fold.
//
// `const D = G * 2` and `public $q = G` are kept by the engine as an **AST**,
// evaluated on first use, because they name something that may not exist yet.
// The harvest carries that tree -- `kind:"binary" op:3` over `constant` and
// `literal` children -- so a runtime with no parser can evaluate it after all.
//
// The values are produced at start-up, in declaration order, which is the same
// answer the engine gives: it evaluates on first use, and nothing can observe
// one before the program runs.

// ConstExpr renders a deferred initializer as a Go expression, or reports that
// it cannot. Refusing is the honest outcome for a node kind the harvest marked
// `unsupported`: the engine leaves such a constant a hole rather than inventing
// a value, and so does this.
func (e *emitter) ConstExpr(lit *pb.Literal) (string, bool) {
	if lit == nil {
		return "", false
	}
	if lit.GetKind() != pb.Literal_KIND_EXPR {
		t := lower.LiteralText(lit)
		if t == "" || strings.HasPrefix(t, "<") {
			return "", false
		}
		return e.operand(t), true
	}
	rendered, ok := e.constNode(lit.GetExpression())
	if !ok {
		return "", false
	}
	// The harvest carries the line the initializer was **written** on, which is
	// the position a diagnostic raised inside it names -- not the line that
	// read it. Wrapping is skipped when the harvest has no line, which is what
	// a folded literal looks like.
	if line := lit.GetExpression().GetLine(); line > 0 {
		// **A bare constant fetch is not evaluated in its own frame.** The
		// engine evaluates `const K = SOME_CONSTANT;` without moving the
		// reported position to the declaration, so an undefined constant in it
		// is reported against the *use* site and adds no
		// `[constant expression]` entry -- while `SOME_CONSTANT + 0` in the
		// same place is reported against the declaration and does.
		// `runtime/interp`'s `evaluatedInOwnFrame` states the same rule,
		// probed there for class constants and property defaults alike.
		if lit.GetExpression().GetKind() == "constant" {
			return rendered, true
		}
		// **Where it was written**: an included class's initializer, or one
		// in an included file's body, names that file.
		file := e.sourceConst()
		if f, ok := e.literalFile[lit]; ok {
			file = strconv.Quote(f)
			// An eval'd string is named after its site, which takes the
			// program's spelling, as a body from one does (e.body).
			if strings.HasSuffix(f, " : eval()'d code") {
				file = e.selfPath(file)
			}
		}
		return fmt.Sprintf("constAt(ctx, %s, %d, func() php.Value { return %s })", file, line, rendered), true
	}
	return rendered, true
}

func (e *emitter) constNode(x *pb.Expr) (string, bool) {
	if x == nil {
		return "", false
	}
	switch x.GetKind() {
	case "literal":
		return e.ConstExpr(x.GetValue())
	case "constant":
		// A global constant, looked up when the initializer runs rather than
		// baked in: the engine resolves it on first use for the same reason.
		return fmt.Sprintf("mustConstant(ctx, %q)", x.GetName()), true
	case "class_constant":
		// **The class and the member are children, not fields.** This read
		// `x.GetName()` and `x.GetMember()`, which the harvest never fills for
		// a `ZEND_AST_CLASS_CONST` -- it fills `Children` -- so every deferred
		// initializer naming `C::K` rendered `mustClassConst(ctx, "", "", ...)`
		// and evaluated to NULL. It was invisible because the emitted call is
		// perfectly well-formed Go and the failure is a value, not an error.
		class, ok := e.constName(x, 0)
		if !ok {
			return "", false
		}
		// **A member that is not a literal is an expression** -- `C::{K}`
		// reads the constant K for the name -- and it is evaluated before the
		// class is fetched (zend_ast.c), so `y::{y}` is `Undefined constant
		// "y"`, not `Class "y" not found` (gh10486_2). Reading its node as a
		// name took the constant's *name* for the member.
		// A literal that is not a string -- `C::{1}` -- is one too: it is
		// refused as a name when evaluated, not when compiled.
		if kids := x.GetChildren(); len(kids) > 1 && kids[1] != nil &&
			(kids[1].GetKind() != "literal" || kids[1].GetValue().GetKind() != pb.Literal_KIND_STRING) {
			rendered, ok := e.constNode(kids[1])
			if !ok {
				return "", false
			}
			return fmt.Sprintf("mustClassConstNamed(ctx, %s, %s, %s)",
				strconv.Quote(class), rendered, e.scopeRef()), true
		}
		member, ok := e.constName(x, 1)
		if !ok {
			return "", false
		}
		return fmt.Sprintf("mustClassConst(ctx, %s, %s, %s)",
			strconv.Quote(class), strconv.Quote(member), e.scopeRef()), true
	case "class_name":
		// `self::class` in an initializer. It is a **php.Value**, not a Go
		// string: returning the bare quoted name produced
		// `php.Concat("", ...)`, which Go rejects.
		name := x.GetName()
		// The node carries the keyword only in its attr, which the harvest
		// keeps as `op`: ZEND_FETCH_CLASS_PARENT for `parent::class`.
		if x.GetOp() == zendFetchClassParent {
			name = "parent"
		}
		if name == "parent" && e.scopeParam != "" {
			return "php.String(constClassNameKeyword(ctx, \"parent\", " + e.scopeParam + "))", true
		}
		if name == "" || name == "self" || name == "static" {
			// A scope only known at run time -- a closure's attributes, read
			// in whatever scope the closure is bound to -- is an expression.
			if e.scopeParam != "" {
				return "php.String(constClassNameKeyword(ctx, \"self\", " + e.scopeParam + "))", true
			}
			// No class at all -- `const C = self::class;` at file scope --
			// is the Error, not the empty name
			// (self_class_const_in_unknown_scope).
			if e.scope == "" {
				return "php.String(constClassNameKeyword(ctx, \"self\", \"\"))", true
			}
			name = e.scope
		}
		return "php.String(" + strconv.Quote(name) + ")", true
	case "class_scope":
		// `__CLASS__` (or an unfolded `self::class`) inside a trait: the class
		// that composed it, which is the scope this initializer is rendered
		// in -- `SomeClass`, not the trait.
		if e.scope == "" {
			return "", false
		}
		return "php.String(" + strconv.Quote(e.scope) + ")", true
	case "new":
		// The class is an expression too -- `new (CLASS_NAME_CONST)` is legal
		// -- and `self`/`parent` are resolved against the scope this
		// initializer was declared in.
		if len(x.GetChildren()) < 1 {
			return "", false
		}
		class, ok := e.constClassName(x.GetChildren()[0])
		if !ok {
			return "", false
		}
		args := "nil"
		if len(x.GetChildren()) > 1 {
			list := x.GetChildren()[1]
			switch list.GetKind() {
			case "args":
				var parts []string
				for _, a := range list.GetChildren() {
					v, argOK := e.constNode(a)
					if !argOK {
						return "", false
					}
					parts = append(parts, v)
				}
				args = "[]php.Value{" + strings.Join(parts, ", ") + "}"
			case "none", "":
			default:
				return "", false
			}
		}
		return fmt.Sprintf("constNew(ctx, %s, %s)", class, args), true
	case "prop", "nullsafe_prop", "dim":
		// A `?->` short circuit travels as an **undef** so it can skip the rest
		// of the chain; the chain ends here, where the program sees null.
		inner, ok := e.constChain(x)
		if !ok {
			return "", false
		}
		return "constEnd(" + inner + ")", true
	case "conditional":
		// The branches are **not** both evaluated: `new Printer ? 1 : 0` builds
		// one Printer, and a Go expression with both arms inline would build
		// it either way. So the arms are a closure, which is also what makes
		// `a ?: b` -- the middle left out -- answer with the condition itself.
		if len(x.GetChildren()) < 3 {
			return "", false
		}
		cond, ok := e.constNode(x.GetChildren()[0])
		if !ok {
			return "", false
		}
		elvis := x.GetChildren()[1].GetKind() == "none"
		then := "c"
		if !elvis {
			if then, ok = e.constNode(x.GetChildren()[1]); !ok {
				return "", false
			}
		}
		otherwise, ok := e.constNode(x.GetChildren()[2])
		if !ok {
			return "", false
		}
		return fmt.Sprintf("func() php.Value { if c := %s; c.Bool() { return %s } else { return %s } }()",
			cond, then, otherwise), true
	case "coalesce":
		// `??` asks whether something is there, so reading it says nothing
		// about it being absent -- the whole point of the operator. The left
		// side is read under silence, which is what suppresses the
		// undefined-ness reports a dim or property fetch would otherwise make.
		if len(x.GetChildren()) < 2 {
			return "", false
		}
		left, ok := e.constNode(x.GetChildren()[0])
		if !ok {
			return "", false
		}
		right, ok := e.constNode(x.GetChildren()[1])
		if !ok {
			return "", false
		}
		return fmt.Sprintf("func() php.Value { s := ctx.BeginSilence(); l := %s; ctx.EndSilence(s); "+
			"if l.Kind() != php.KindNull && l.Kind() != php.KindUndef { return l }; return %s }()",
			left, right), true
	case "negate", "identity":
		// The engine compiles unary minus as `-1 * value`, **in that order**,
		// which an "Unsupported operand types" message shows: `int * string`.
		if len(x.GetChildren()) < 1 {
			return "", false
		}
		v, ok := e.constNode(x.GetChildren()[0])
		if !ok {
			return "", false
		}
		by := "php.Int(-1)"
		if x.GetKind() == "identity" {
			by = "php.Int(1)"
		}
		// It is a real multiplication, so it owes the same conversion walk:
		// `const A = +"0foo"` warns about a non-numeric value, and `-[1]`
		// refuses.
		return fmt.Sprintf("constArith(ctx, %q, php.Mul, %s, %s)", "*", by, v), true
	case "binary", "unary":
		return e.constOp(x)
	case "greater", "greater_equal":
		// The parser has no greater-than opcode: it emits the smaller-than
		// form with the operands swapped, and gives these their own AST kinds
		// so the *source* order survives. Swapping them back here is what the
		// compiler would have done.
		if len(x.GetChildren()) != 2 {
			return "", false
		}
		lhs, ok := e.constNode(x.GetChildren()[0])
		if !ok {
			return "", false
		}
		rhs, ok := e.constNode(x.GetChildren()[1])
		if !ok {
			return "", false
		}
		fn := "IsSmallerValues"
		if x.GetKind() == "greater_equal" {
			fn = "IsSmallerOrEqualValues"
		}
		return fmt.Sprintf("constBool(ctx, php.%s, %s, %s)", fn, rhs, lhs), true
	case "and", "or":
		// **Short-circuiting**, so the right side is a closure: `false && new
		// Printer` must not build the Printer, and a Go `&&` over two rendered
		// expressions would evaluate both before the operator saw either.
		if len(x.GetChildren()) != 2 {
			return "", false
		}
		lhs, ok := e.constNode(x.GetChildren()[0])
		if !ok {
			return "", false
		}
		rhs, ok := e.constNode(x.GetChildren()[1])
		if !ok {
			return "", false
		}
		return fmt.Sprintf("constAndOr(%t, %s, func() php.Value { return %s })",
			x.GetKind() == "and", lhs, rhs), true
	case "array":
		// The children are **element** nodes, not values: each carries its
		// value and its key, in that order in the AST and the other way round
		// in evaluation -- `[UNKNOWN_KEY => UNKNOWN_VALUE]` reports the key.
		// Reading them as values rendered `constArray(ctx, []php.Value{...})`,
		// a call to a helper that was never emitted, and it never fired only
		// because `element` is not a kind constNode answers for.
		var parts []string
		for _, c := range x.GetChildren() {
			switch c.GetKind() {
			case "unpack":
				if len(c.GetChildren()) < 1 {
					return "", false
				}
				v, ok := e.constNode(c.GetChildren()[0])
				if !ok {
					return "", false
				}
				parts = append(parts, fmt.Sprintf("{value: %s, spread: true}", v))
			case "element":
				if len(c.GetChildren()) < 2 {
					return "", false
				}
				key := ""
				if c.GetChildren()[1].GetKind() != "none" {
					var ok bool
					if key, ok = e.constNode(c.GetChildren()[1]); !ok {
						return "", false
					}
				}
				v, ok := e.constNode(c.GetChildren()[0])
				if !ok {
					return "", false
				}
				if key == "" {
					parts = append(parts, fmt.Sprintf("{value: %s}", v))
					continue
				}
				parts = append(parts, fmt.Sprintf("{key: %s, hasKey: true, value: %s}", key, v))
			default:
				return "", false
			}
		}
		return "constArray(ctx, []constEntry{" + strings.Join(parts, ", ") + "})", true
	}
	return "", false
}

// constOp resolves the engine's opcode number through the harvested table
// rather than transcribing a numbering, which is the rule the proto states and
// the one this project has been wrong about before.
func (e *emitter) constOp(x *pb.Expr) (string, bool) {
	name := ""
	if int(x.GetOp()) < len(e.opcodes) {
		name = e.opcodes[x.GetOp()]
	}
	var args []string
	for _, c := range x.GetChildren() {
		v, argOK := e.constNode(c)
		if !argOK {
			return "", false
		}
		args = append(args, v)
	}
	if len(args) == 1 {
		fn, ok := constUnary[name]
		if !ok {
			return "", false
		}
		return fmt.Sprintf(fn, args[0]), true
	}
	if len(args) != 2 {
		return "", false
	}
	// Concatenation converts through `__toString`, which needs the Ctx: `$s
	// = new Foo . ''` as a parameter default runs the method, and gave "".
	if name == "ZEND_CONCAT" {
		return fmt.Sprintf("constConcat(ctx, %s, %s)", args[0], args[1]), true
	}
	if fn, ok := constBinary[name]; ok {
		// **The diagnostic walk applies here too.** `const T = array(1,2) -
		// array(0);` is `Unsupported operand types: array - array`, and calling
		// `php.Sub` straight gave `int(0)` -- a wrong value, quietly. The
		// non-constant path guards every one of these with
		// `AcceptsArithmetic`; a deferred initializer runs the same operator
		// and owes the same refusal.
		if symbol, guarded := constGuarded[name]; guarded {
			return fmt.Sprintf("constArith(ctx, %q, php.%s, %s, %s)", symbol, fn, args[0], args[1]), true
		}
		return fmt.Sprintf("php.%s(%s, %s)", fn, args[0], args[1]), true
	}
	// A comparison answers a Go bool or int; the initializer wants a PHP value.
	if fn, ok := constCompare[name]; ok {
		return fmt.Sprintf(fn, args[0], args[1]), true
	}
	// The ones that can **refuse**: division and modulo by zero, and a
	// negative shift count. They answer `(Value, bool)`, and the false arm is
	// a throw the initializer has to be able to raise -- so it goes through a
	// helper that parks the error, exactly as the other deferred paths do.
	if fn, ok := constFallible[name]; ok {
		return fmt.Sprintf("constFallible(ctx, %q, %q, php.%s, %s, %s)", fn, constFallibleSymbol[name], fn, args[0], args[1]), true
	}
	return "", false
}

// constBinary is the operators a constant initializer can use that answer a
// value directly. Diagnostics are fine here: the tree is evaluated **on first
// use**, not at start-up, and `constAt` gives it the line the initializer was
// written on.
var constBinary = map[string]string{
	"ZEND_ADD": "Add", "ZEND_SUB": "Sub", "ZEND_MUL": "Mul",
	"ZEND_CONCAT": "Concat", "ZEND_POW": "Pow",
	"ZEND_BW_AND": "BitAnd", "ZEND_BW_OR": "BitOr", "ZEND_BW_XOR": "BitXor",
}

// constGuarded are the operators that owe `AcceptsArithmetic`'s left-to-right
// walk before they run, mapped to the symbol the refusal quotes. `Concat` is
// absent because concatenation accepts anything a string can be made from, and
// `Pow` is here for the same reason `Mul` is.
var constGuarded = map[string]string{
	"ZEND_ADD": "+", "ZEND_SUB": "-", "ZEND_MUL": "*", "ZEND_POW": "**",
	"ZEND_BW_AND": "&", "ZEND_BW_OR": "|", "ZEND_BW_XOR": "^",
}

// constCompare are the orderings and equalities, which answer a Go bool or int.
var constCompare = map[string]string{
	// The loose comparisons go through the Ctx forms, which is where an
	// object meets `__toString`: `function f($y = new Foo > '')` runs it, and
	// its failure is the default's.
	"ZEND_IS_EQUAL":            "constBool(ctx, php.LooseEqualValues, %s, %s)",
	"ZEND_IS_NOT_EQUAL":        "constBool(ctx, php.NotLooseEqualValues, %s, %s)",
	"ZEND_IS_IDENTICAL":        "php.Bool(php.StrictEqual(%s, %s))",
	"ZEND_IS_NOT_IDENTICAL":    "php.Bool(!php.StrictEqual(%s, %s))",
	"ZEND_IS_SMALLER":          "constBool(ctx, php.IsSmallerValues, %s, %s)",
	"ZEND_IS_SMALLER_OR_EQUAL": "constBool(ctx, php.IsSmallerOrEqualValues, %s, %s)",
	"ZEND_SPACESHIP":           "constSpaceship(ctx, %s, %s)",
	"ZEND_BOOL_XOR":            "php.Bool(php.BoolXor(%s, %s))",
}

// constFallible are the two-result operators, named by their Go function so the
// helper can report which one refused.
var constFallible = map[string]string{
	"ZEND_DIV": "Div", "ZEND_MOD": "Mod",
	"ZEND_SL": "ShiftLeft", "ZEND_SR": "ShiftRight",
}

// constFallibleSymbol is the symbol each one quotes in its conversion walk,
// which runs before the operator exactly as constGuarded's does.
var constFallibleSymbol = map[string]string{
	"ZEND_DIV": "/", "ZEND_MOD": "%",
	"ZEND_SL": "<<", "ZEND_SR": ">>",
}

// constUnary is the one-child form. `-A` is not in the AST as a unary at all --
// the compiler folds it into `MUL` by -1 -- so what is left is the two the
// engine really does keep.
var constUnary = map[string]string{
	"ZEND_BW_NOT":   "php.BitNot(%s)",
	"ZEND_BOOL_NOT": "php.NotBool(%s)",
}

// constName reads the nth child of a constant-expression node as a plain name.
//
// A class or member position in a folded initializer is nearly always a literal
// string -- the compiler has already resolved `self` and namespace aliases into
// one -- and anything else is refused rather than guessed at, which leaves the
// constant a hole exactly as an unsupported node kind does.
func (e *emitter) constName(x *pb.Expr, n int) (string, bool) {
	children := x.GetChildren()
	if n >= len(children) || children[n] == nil {
		return "", false
	}
	c := children[n]
	switch c.GetKind() {
	case "literal":
		lit := c.GetValue()
		if lit.GetKind() != pb.Literal_KIND_STRING {
			return "", false
		}
		return lit.GetStringValue(), true
	case "class_name", "constant":
		// `self::class` in the class position arrives as a name of its own.
		if c.GetName() == "" {
			return "", false
		}
		return c.GetName(), true
	}
	return "", false
}

// constClassName renders the class position of a constant-expression `new` as a
// Go string expression. `self` and `parent` are answered from the scope the
// initializer was declared in, which is the same rule the runtime `new` follows.
// zendFetchClassParent is ZEND_FETCH_CLASS_PARENT, a class-name node's attr
// for `parent`.
const zendFetchClassParent = 2

func (e *emitter) constClassName(x *pb.Expr) (string, bool) {
	if name, ok := e.constName(&pb.Expr{Kind: "wrapper", Children: []*pb.Expr{x}}, 0); ok {
		switch kw := strings.ToLower(name); kw {
		case "self", "static", "parent":
			if e.scopeParam != "" {
				return fmt.Sprintf("constKeywordClass(ctx, %q, %s)", kw, e.scopeParam), true
			}
			// With no class to resolve against the keyword is an Error at
			// evaluation -- `function f($x = new self)` is `Cannot access
			// "self" when no class scope is active` when the default is
			// used, and fine when it is not -- so it renders as a check.
			if e.scope == "" {
				return fmt.Sprintf("constKeywordClass(ctx, %q, \"\")", kw), true
			}
			if kw == "parent" {
				return fmt.Sprintf("constKeywordClass(ctx, \"parent\", %q)", e.scope), true
			}
			return strconv.Quote(e.scope), true
		}
		return strconv.Quote(name), true
	}
	// Not a plain name: an expression that produces one. It renders as a value,
	// and the class is what that value spells.
	rendered, ok := e.constNode(x)
	if !ok {
		return "", false
	}
	return "(" + rendered + ").String()", true
}

// constChain renders a property or offset chain **unwrapped**, so the short
// circuit an earlier `?->` left can travel along it. Everything else renders as
// an ordinary value; only the container position of a chain node asks for this.
func (e *emitter) constChain(x *pb.Expr) (string, bool) {
	switch x.GetKind() {
	case "prop", "nullsafe_prop":
		if len(x.GetChildren()) < 2 {
			return "", false
		}
		container, ok := e.constChain(x.GetChildren()[0])
		if !ok {
			return "", false
		}
		name, ok := e.constNode(x.GetChildren()[1])
		if !ok {
			return "", false
		}
		return fmt.Sprintf("constProp(ctx, %s, func() php.Value { return %s }, %t)",
			container, name, x.GetKind() == "nullsafe_prop"), true
	case "dim":
		if len(x.GetChildren()) < 2 {
			return "", false
		}
		container, ok := e.constChain(x.GetChildren()[0])
		if !ok {
			return "", false
		}
		index, ok := e.constNode(x.GetChildren()[1])
		if !ok {
			return "", false
		}
		return fmt.Sprintf("constDim(ctx, %s, func() php.Value { return %s })", container, index), true
	}
	return e.constNode(x)
}
