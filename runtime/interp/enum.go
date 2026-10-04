package interp

import (
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// enumCase returns the singleton object for one case of an enum.
//
// Cases are singletons: `Suit::Hearts === Suit::Hearts` is true, and comparison
// throughout PHP relies on that, so the object is built once and reused rather
// than instantiated per reference.
//
// **Touching one case builds them all.** The engine materialises an enum's
// whole case table on first access, in declaration order -- measured two ways:
// after `E::B` on a three-case enum the next `new` is handle 5 rather than 3,
// and reading `Y::A` raises for `Y::B`'s bad backing value. Building lazily one
// at a time gave every later object the wrong handle number, which is graded
// byte-exact.
func (in *Interp) enumCase(ctx *php.Ctx, u *Unit, class *Class, decl *pb.ClassConstant, depth int) (php.Value, error) {
	key := class.Name + "::" + decl.GetName()
	if existing, ok := u.enumCases[key]; ok {
		return existing, nil
	}
	// **In declaration order**, the requested case included: the engine numbers
	// the objects that way, and building the others first put the one that was
	// asked for at the end of the run of handles instead of in its place.
	for _, other := range class.decl.GetConstants() {
		if !other.GetIsCase() {
			continue
		}
		if _, err := in.buildEnumCase(ctx, u, class, other, depth); err != nil {
			return php.Undef(), err
		}
	}
	if built, ok := u.enumCases[key]; ok {
		return built, nil
	}
	return in.buildEnumCase(ctx, u, class, decl, depth)
}

// buildEnumCase makes one case object. Every caller goes through enumCase,
// which is what makes the whole table appear at once.
func (in *Interp) buildEnumCase(ctx *php.Ctx, u *Unit, class *Class, decl *pb.ClassConstant, depth int) (php.Value, error) {
	key := class.Name + "::" + decl.GetName()
	if existing, ok := u.enumCases[key]; ok {
		return existing, nil
	}

	// A backed enum rejects a case whose declared value does not match the
	// backing type. PHP raises this on first use rather than at declaration,
	// which is why the value was harvested unevaluated. The initializer can be
	// a whole expression -- `case C = I::A;` names an interface constant, which
	// the compiler cannot fold -- so it is evaluated the same way any other
	// deferred initializer is.
	backing := class.decl.GetEnumBackingType()
	var backed php.Value
	if backing != "" {
		if !decl.GetHasValue() {
			return php.Undef(), Raise("TypeError",
				"Case %s of backed enum %s must have a value", decl.GetName(), class.Name)
		}
		value, resolved, err := in.constantLiteral(ctx, u, decl.GetValue(), class, depth)
		if err != nil {
			return php.Undef(), err
		}
		if !resolved {
			return php.Undef(), &UnsupportedError{
				Opcode: "value of enum case " + key + unresolvedSuffix(decl.GetValue()),
			}
		}
		if (backing == "int" && value.Kind() != php.KindInt) ||
			(backing == "string" && value.Kind() != php.KindString) {
			return php.Undef(), Raise("TypeError",
				"Enum case type %s does not match enum backing type %s",
				value.Kind().String(), backing)
		}
		backed = value
	} else if decl.GetHasValue() {
		return php.Undef(), Raise("TypeError",
			"Case %s of non-backed enum %s must not have a value", decl.GetName(), class.Name)
	}

	object := u.born(php.NewObject(class.Name, u.handle()))
	object.EnumCase = decl.GetName()
	object.EnumBacking = class.decl.GetEnumBackingType()
	object.Set("name", php.String(decl.GetName()))
	if backing != "" {
		object.Set("value", backed)
	}
	if u.enumCases == nil {
		u.enumCases = map[string]php.Value{}
	}
	value := php.Obj(object)
	u.enumCases[key] = value
	return value, nil
}

// verifyBackedValues refuses an enum whose cases do not have distinct backing
// values -- from() would have no answer. It is what building the *backed value
// table* notices, so reaching a case by name and calling from() both raise it
// while cases() does not: that one walks the declarations directly.
func (in *Interp) verifyBackedValues(ctx *php.Ctx, u *Unit, class *Class, depth int) error {
	if class.decl.GetEnumBackingType() == "" {
		return nil
	}
	// The verdict is remembered, not the fact of having asked: the table is
	// never built, so every later use raises again.
	key := strings.ToLower(class.Name)
	if verdict, asked := u.backedChecked[key]; asked {
		return verdict
	}
	if u.backedChecked == nil {
		u.backedChecked = map[string]error{}
	}
	seen := map[string]string{}
	for _, constant := range class.decl.GetConstants() {
		if !constant.GetIsCase() || !constant.GetHasValue() {
			continue
		}
		value, ok, err := in.constantLiteral(ctx, u, constant.GetValue(), class, depth)
		if err != nil || !ok {
			continue
		}
		valueKey := value.Kind().String() + ":" + value.String()
		if first, clash := seen[valueKey]; clash {
			u.backedChecked[key] = Raise("Error",
				"Duplicate value in enum %s for cases %s and %s",
				php.DisplayName(class.Name), first, constant.GetName())
			return u.backedChecked[key]
		}
		seen[valueKey] = constant.GetName()
	}
	u.backedChecked[key] = nil
	return nil
}

// enumCaseDecl finds a case by name on an enum.
func (u *Unit) enumCaseDecl(class *Class, name string) (*pb.ClassConstant, bool) {
	for _, constant := range class.decl.GetConstants() {
		if constant.GetIsCase() && constant.GetName() == name {
			return constant, true
		}
	}
	return nil, false
}

// enumStaticCall implements the methods every enum has without declaring them:
// cases(), and for a backed enum from() and tryFrom().
func (in *Interp) enumStaticCall(ctx *php.Ctx, u *Unit, class *Class, method string, args []php.Value, depth int) (php.Value, bool, error) {
	switch strings.ToLower(method) {
	case "cases":
		list := php.NewArray()
		for _, constant := range class.decl.GetConstants() {
			if !constant.GetIsCase() {
				continue
			}
			value, err := in.enumCase(ctx, u, class, constant, depth)
			if err != nil {
				return php.Undef(), true, err
			}
			list.Append(value)
		}
		return php.Arr(list), true, nil

	case "from", "tryfrom":
		if class.decl.GetEnumBackingType() == "" {
			return php.Undef(), true, Raise("Error",
				"Call to undefined method %s::%s()", class.Name, method)
		}
		if len(args) == 0 {
			return php.Undef(), true, Raise("ArgumentCountError",
				"%s::%s() expects exactly 1 argument, 0 given", class.Name, method)
		}
		// The argument is checked against the *backing type* before anything
		// else, including before the case table is built -- so a call with the
		// wrong type reports that rather than whatever is wrong with the enum.
		// The declared parameter is not the backing type in every case: an
		// int-backed enum takes `int`, while a string-backed one takes
		// `string|int` in weak mode so that passing an int does not look like
		// a coercion. Strict mode narrows that back to `string`.
		backing := class.decl.GetEnumBackingType()
		declared := backing
		if backing == "string" && !u.pending.strict {
			declared = "string|int"
		}
		given, ok := in.checkType(ctx, u, declared, args[0].Deref(), u.pending.strict)
		if !ok {
			return php.Undef(), true, Raise("TypeError",
				"%s::%s(): Argument #1 ($value) must be of type %s, %s given",
				php.DisplayName(class.Name), method, declared,
				givenTypeName(args[0].Deref()))
		}
		// An int accepted by a string-backed enum is rendered, not coerced.
		if backing == "string" && given.Kind() == php.KindInt {
			given = php.String(given.String())
		}
		if err := in.verifyBackedValues(ctx, u, class, depth); err != nil {
			return php.Undef(), true, err
		}
		for _, constant := range class.decl.GetConstants() {
			if !constant.GetIsCase() {
				continue
			}
			value, err := in.enumCase(ctx, u, class, constant, depth)
			if err != nil {
				return php.Undef(), true, err
			}
			if held, _ := value.Object().Get("value"); php.StrictEqual(held, given) {
				return value, true, nil
			}
		}
		if strings.EqualFold(method, "tryfrom") {
			return php.Null(), true, nil
		}
		// The value is reported as it was *after* coercion, which is what the
		// enum was actually searched for.
		quoted := given.String()
		if given.Kind() == php.KindString {
			quoted = `"` + quoted + `"`
		}
		return php.Undef(), true, Raise("ValueError",
			"%s is not a valid backing value for enum %s", quoted, php.DisplayName(class.Name))
	}
	return php.Undef(), false, nil
}
