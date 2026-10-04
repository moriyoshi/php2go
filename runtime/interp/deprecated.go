package interp

import (
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// PHP 8.4's `#[\Deprecated]`. Using what it marks produces an E_USER_DEPRECATED
// naming the thing and, optionally, a suffix built from the attribute's two
// arguments. Those arguments can be constant expressions -- one of the tests
// uses a deprecated constant as another's message -- so they are evaluated
// through the same machinery as any other deferred initializer.

// deprecationSuffix renders the `[ since X][, message]` tail. Either half may
// be absent, and an argument that cannot be evaluated suppresses the whole
// diagnostic, as it does in the engine.
func (in *Interp) deprecationSuffix(ctx *php.Ctx, unit *Unit, scope *Class,
	message, since *pb.Literal, strict bool, depth int) (string, bool, error) {
	var refused error
	text := func(lit *pb.Literal, position int, name string) (string, bool) {
		if lit == nil {
			return "", true
		}
		value, ok, err := in.constantLiteral(ctx, unit, lit, scope, depth)
		if err != nil || !ok {
			return "", false
		}
		// The attribute is an object: using what it marks *constructs* one, and
		// the constructor's `?string` parameters are checked like any others.
		// Without strict_types an int or a float coerces, which is why
		// `#[\Deprecated(1234)]` usually reads "deprecated, 1234"; an array or
		// an object never does, and under strict_types nothing but a string
		// does.
		converted, fits := in.checkType(ctx, unit, "?string", value.Deref(), strict)
		if !fits {
			// The engine constructs the attribute at the site that used what
			// it marks, so that is where the frame points.
			file, at := ctx.Location()
			ctx.PushCall(php.StackFrame{
				Function: "Deprecated->__construct",
				File:     file, Line: at,
				Args: []php.Value{value.Deref()},
			})
			refused = in.throwError(ctx, unit, "TypeError",
				"Deprecated::__construct(): Argument #%d ($%s) must be of type ?string, %s given",
				position, name, givenTypeName(value.Deref()))
			ctx.PopCall()
			return "", false
		}
		return converted.Deref().String(), true
	}
	messageText, ok := text(message, 1, "message")
	if !ok {
		return "", false, refused
	}
	sinceText, ok := text(since, 2, "since")
	if !ok {
		return "", false, refused
	}
	suffix := ""
	if sinceText != "" {
		suffix += " since " + sinceText
	}
	if messageText != "" {
		suffix += ", " + messageText
	}
	return suffix, true, nil
}

// deprecatedConstruct is `new \Deprecated(...)` written out. The class is an
// attribute, but an attribute is an ordinary object: a program may build one,
// and its two properties are readonly -- which the property table already
// enforces, since the harvest records the flag.
func (in *Interp) deprecatedConstruct(ctx *php.Ctx, unit *Unit, object *php.Object,
	args []php.Value) (php.Value, error) {
	for i, name := range []string{"message", "since"} {
		if propertyInitialized(object, name) {
			// Calling __construct a second time is an ordinary write to a
			// readonly property, and is refused as one.
			return php.Undef(), in.throwError(ctx, unit, "Error",
				"Cannot modify readonly property Deprecated::$%s", name)
		}
		value := php.Null()
		if i < len(args) {
			value = args[i].Deref()
		}
		converted, fits := in.checkType(ctx, unit, "?string", value, false)
		if !fits {
			return php.Undef(), in.throwError(ctx, unit, "TypeError",
				"Deprecated::__construct(): Argument #%d ($%s) must be of type ?string, %s given",
				i+1, name, givenTypeName(value))
		}
		object.Set(name, converted.Deref())
	}
	return php.Null(), nil
}

// noteDeprecatedCall reports the deprecation a call to a marked function or
// method earns. The scope decides the wording: a method names its class.
func (in *Interp) noteDeprecatedCall(ctx *php.Ctx, unit *Unit, op *pb.OpArray,
	scope *Class, depth int) error {
	return in.noteDeprecatedCallAs(ctx, unit, op, scope, "", depth)
}

// noteDeprecatedCallAs is noteDeprecatedCall for a call that reaches its callee
// under a different name than the callee's own: __call and __callStatic are
// reported against the method the program asked for, not against the accessor
// that received it.
func (in *Interp) noteDeprecatedCallAs(ctx *php.Ctx, unit *Unit, op *pb.OpArray,
	scope *Class, as string, depth int) error {
	if op == nil || !op.GetDeprecated() || unit.silentDeprecation > 0 {
		return nil
	}
	suffix, ok, refused := in.deprecationSuffix(ctx, unit, scope,
		op.GetDeprecatedMessage(), op.GetDeprecatedSince(), op.GetStrictTypes(), depth)
	if !ok {
		return refused
	}
	name := op.GetName()
	if as != "" {
		name = as
	}
	if scope != nil {
		ctx.UserDeprecated("Method %s::%s() is deprecated%s",
			php.DisplayName(scope.Name), name, suffix)
		return nil
	}
	ctx.UserDeprecated("Function %s() is deprecated%s", name, suffix)
	return nil
}

// noteDeprecatedConstant reports the deprecation reading a marked class
// constant earns. An enum case is named as one.
func (in *Interp) noteDeprecatedConstant(ctx *php.Ctx, unit *Unit, owner *Class,
	constant *pb.ClassConstant, depth int) {
	if constant == nil || !constant.GetDeprecated() || unit.silentDeprecation > 0 {
		return
	}
	// A message may name a deprecated constant, which is reported in its own
	// right and reported first. Naming its own constant is what recursed
	// forever in GH-17711, so a report already under way is not started again.
	key := strings.ToLower(owner.Name) + "::" + constant.GetName()
	if unit.reportingDeprecation[key] {
		return
	}
	if unit.reportingDeprecation == nil {
		unit.reportingDeprecation = map[string]bool{}
	}
	unit.reportingDeprecation[key] = true
	suffix, ok, _ := in.deprecationSuffix(ctx, unit, owner,
		constant.GetDeprecatedMessage(), constant.GetDeprecatedSince(), false, depth)
	delete(unit.reportingDeprecation, key)
	if !ok {
		return
	}
	kind := "Constant"
	if constant.GetIsCase() {
		kind = "Enum case"
	}
	ctx.UserDeprecated("%s %s::%s is deprecated%s",
		kind, php.DisplayName(owner.Name), constant.GetName(), suffix)
}

// noteDeprecatedConstantName reports a *global* constant the engine has
// deprecated -- E_STRICT since 8.4. Which names carry the flag is harvested,
// not written here. defined() asks without using, and is already silenced.
func (in *Interp) noteDeprecatedConstantName(ctx *php.Ctx, unit *Unit, name string) {
	if unit != nil && unit.silentDeprecation > 0 {
		return
	}
	if in.deprecatedConstants[strings.TrimPrefix(name, "\\")] {
		ctx.Deprecated("Constant %s is deprecated", strings.TrimPrefix(name, "\\"))
	}
}
