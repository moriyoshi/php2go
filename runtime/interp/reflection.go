package interp

import (
	"errors"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// Reflection is an engine class with no op_arrays, so its behaviour has to be
// supplied here -- the same arrangement Generator, Closure and ArrayObject use.
// What is modelled is the part the corpus actually exercises: naming a class,
// and reading the attributes written on a declaration.
//
// An attribute is *not* an object until something asks for one. The engine
// keeps the arguments as unevaluated constant expressions and instantiates the
// class on `newInstance()`, which is why `#[Attr(SOME_CONST)]` is legal above a
// class declared before the constant exists.

// reflectionClasses are the engine classes this file implements. Anything else
// reflective stays an honest gap.
var reflectionClasses = map[string]bool{
	"reflectionclass":         true,
	"reflectionobject":        true,
	"reflectionattribute":     true,
	"reflectionfunction":      true,
	"reflectionmethod":        true,
	"reflectionproperty":      true,
	"reflectionclassconstant": true,
	"reflectionparameter":     true,
	"reflectionnamedtype":     true,
	// `Attribute` itself is an engine class with one property, and reflecting
	// on it is how a program asks what an attribute may target.
	"attribute": true,
}

// Attribute::TARGET_* and IS_REPEATABLE. Only their combination matters here:
// a flags value with any other bit set is refused.
const (
	attributeTargetAll    = 63
	attributeIsRepeatable = 64
	// ReflectionAttribute::IS_INSTANCEOF, the only getAttributes filter flag.
	attributeIsInstanceOf = 2

	// Attribute::TARGET_*, which say where an attribute may be written.
	targetClass         = 1
	targetFunction      = 2
	targetMethod        = 4
	targetProperty      = 8
	targetClassConstant = 16
	targetParameter     = 32
)

// reflector is what a reflection object holds. Which field is set says what
// kind of reflector it is.
type reflector struct {
	// class is the subject of a ReflectionClass or ReflectionObject, and the
	// declaring class of a member reflector.
	class *Class
	// attribute is the subject of a ReflectionAttribute, with the class it was
	// written on as the scope its arguments are evaluated in.
	attribute *pb.Attribute
	scope     *Class
	// function is the subject of a ReflectionFunction or ReflectionMethod.
	function *pb.OpArray
	// name is the name the subject was reached by, which a trait alias may
	// have changed: `use T { test as __construct; }` gives T::test the name
	// __construct in the composing class, and that is the name reflection
	// reports and prints.
	name string
	// property and constant are the subjects of the two member reflectors.
	property *pb.Property
	constant *pb.ClassConstant
	// parameter is the subject of a ReflectionParameter, with position the
	// place it holds in the list -- which the declaration itself does not say.
	parameter *pb.ArgInfo
	position  int
	// file is where the attribute was written, which the diagnostics an
	// attribute instantiation produces are attributed to.
	file string
	// target is the Attribute::TARGET_* bit for the declaration the attribute
	// was written on, which decides whether it may be there at all.
	target int64
	// internal names an engine function, which has no op_array: the reflector
	// can answer its name and nothing else.
	internal string
	// typeName is the subject of a ReflectionNamedType, which has no
	// declaration of its own -- it is a type as written.
	typeName string
	// bound is the object a ReflectionMethod or ReflectionProperty was built
	// from, when it was built from one rather than from a class name.
	bound *php.Object
	// dynamic marks a ReflectionProperty built for a property the object grew
	// at run time. Such a property has no declaration to answer from, so the
	// reflector carries a synthesized one and this flag says so.
	dynamic bool
	// closureSubject marks a reflector whose subject *is* a Closure, however
	// it was made. `__toString` then heads the dump with `Closure [` in place
	// of `Function [` or `Method [`, and nothing else changes.
	closureSubject bool
	// realClosure marks a subject written as a closure rather than made from
	// an existing function. Such a closure carries the scope it was declared
	// in, but an attribute on it still targets a *function*: the engine asks
	// for ZEND_ACC_CLOSURE without ZEND_ACC_FAKE_CLOSURE, not merely for the
	// absence of a scope.
	realClosure bool
	// subject is an object the reflector *describes* rather than one it was
	// built from -- the Closure behind a trampoline reflector, whose signature
	// was copied out rather than referenced. The engine's reflector holds the
	// closure, so it stays alive and its handle is not handed to the next
	// `new`; this is what keeps php2go's handle numbers in step.
	subject *php.Object
	// trampoline marks the reflector the engine hands back for
	// `Closure::__invoke`: it carries the wrapped function's signature under
	// the name and scope of Closure, and counts as an internal declaration.
	trampoline bool
}

func asReflector(object *php.Object) (*reflector, bool) {
	if object == nil {
		return nil, false
	}
	held, ok := object.Internal.(*reflector)
	return held, ok
}

// reflectionMethod dispatches every method these classes answer, including the
// constructor -- which needs the class table, and so cannot be a builtinCtor.
func (in *Interp) reflectionMethod(ctx *php.Ctx, unit *Unit, call *pendingCall) (php.Value, bool, error) {
	object := call.this
	if object == nil || !reflectionClasses[strings.ToLower(object.ClassName)] {
		return php.Undef(), false, nil
	}
	argument := func(i int) php.Value {
		if i < len(call.args) {
			return call.args[i].Deref()
		}
		return php.Undef()
	}
	if strings.EqualFold(call.builtinMethod, "__construct") {
		if strings.EqualFold(object.ClassName, "Attribute") {
			return in.attributeFlagsConstruct(ctx, unit, object, call)
		}
		if value, handled, err := in.memberConstruct(ctx, unit, object, call.args); handled {
			return value, true, err
		}
		return in.reflectionConstruct(ctx, unit, object, argument(0))
	}
	held, ok := asReflector(object)
	if !ok {
		return php.Undef(), true, Raise("Error",
			"Internal error: %s object is not initialised", php.DisplayName(object.ClassName))
	}
	if strings.EqualFold(call.builtinMethod, magicToString) {
		// Every reflector prints its subject as a block of text. The format is
		// shared, so it lives together in reflectdump.go rather than once per
		// reflector.
		if text, printed, err := in.reflectorString(ctx, unit, object); err != nil {
			return php.Undef(), true, err
		} else if printed {
			return php.String(text), true, nil
		}
	}
	switch {
	case held.attribute != nil:
		return in.attributeMethod(ctx, unit, held, call, argument)
	case held.parameter != nil:
		// Before the function case: a parameter reflector carries the function
		// it belongs to as well, and the questions are its own.
		return in.parameterReflectionMethod(ctx, unit, held, call, argument)
	case held.function != nil:
		return in.functionReflectionMethod(ctx, unit, held, call, argument)
	case held.property != nil:
		return in.propertyReflectionMethod(ctx, unit, held, call, argument)
	case held.constant != nil:
		return in.constantReflectionMethod(ctx, unit, held, call, argument)
	case held.typeName != "":
		return in.namedTypeMethod(held, call)
	case held.internal != "":
		if strings.EqualFold(call.builtinMethod, "getName") {
			return php.String(held.internal), true, nil
		}
		if strings.EqualFold(call.builtinMethod, "getClosure") {
			// An engine function has no body to wrap, so the closure carries
			// its name -- which is all a call needs, and is what var_dump
			// shows instead of a file and a line.
			wrapper := unit.born(php.NewObject("Closure", unit.handle()))
			wrapper.Internal = &closure{
				builtin: held.internal, bound: map[string]php.Value{}, fake: true,
			}
			return php.Obj(wrapper), true, nil
		}
		if strings.EqualFold(call.builtinMethod, "getAttributes") {
			// An engine function carries no user attributes.
			return php.Arr(php.NewArray()), true, nil
		}
		return php.Undef(), false, nil
	}
	return in.classReflectionMethod(ctx, unit, held, call, argument)
}

// reflectionConstruct builds a ReflectionClass or ReflectionObject. PHP refuses
// a name it cannot resolve, and the refusal is a ReflectionException rather
// than the Error a missing class usually earns.
func (in *Interp) reflectionConstruct(ctx *php.Ctx, unit *Unit, object *php.Object,
	subject php.Value) (php.Value, bool, error) {
	// **Subclasses too.** `class Sub extends ReflectionAttribute {}` inherits
	// the private constructor, and the engine refuses `new Sub()` with the same
	// message -- naming ReflectionAttribute, not the subclass.
	if isReflectionAttributeClass(unit, object.ClassName) {
		// Its constructor is **private**, and the refusal is the ordinary one
		// for calling a private constructor -- it names the calling scope.
		// Measured: the older wording here, "Cannot directly construct
		// ReflectionAttribute, use ReflectionClass::getAttributes() instead",
		// is not what this engine says and was a wrong answer.
		scope := "global scope"
		if s := ctx.Scope(); s != "" {
			scope = "scope " + php.DisplayName(s)
		}
		return php.Undef(), true, Raise("Error",
			"Call to private ReflectionAttribute::__construct() from %s", scope)
	}
	name := subject.String()
	if subject.Kind() == php.KindObject {
		name = subject.Object().ClassName
	}
	class, known := in.resolveTypeClass(ctx, unit, name)
	if !known || unit.linking[strings.ToLower(strings.TrimPrefix(name, "\\"))] {
		// A class whose own declaration is still running is in the table but
		// not linked, and reflection cannot describe it yet.
		return php.Undef(), true, Raise("ReflectionException",
			"Class \"%s\" does not exist", name)
	}
	object.Internal = &reflector{class: class}
	// The declared `$name` holds the real name, NUL suffix and all, so that an
	// anonymous class's reflector compares equal to `get_class()`.
	object.Set("name", php.String(class.Name))
	return php.Null(), true, nil
}

// attributeFlagsConstruct is `new Attribute($flags)`. The argument is an int in
// weak mode and must name only bits the engine defines; anything else is
// refused rather than stored.
func (in *Interp) attributeFlagsConstruct(ctx *php.Ctx, unit *Unit, object *php.Object,
	call *pendingCall) (php.Value, bool, error) {
	flags := php.Int(attributeTargetAll)
	if len(call.args) > 0 {
		given := call.args[0].Deref()
		converted, ok := in.checkType(ctx, unit, "int", given, false)
		if !ok {
			return php.Undef(), true, Raise("TypeError",
				"Attribute::__construct(): Argument #1 ($flags) must be of type int, %s given",
				givenTypeName(given))
		}
		flags = converted
	}
	// The *value* is not checked here: `new Attribute(-1)` is accepted, and the
	// refusal only comes when something marked with that class is instantiated.
	object.Set("flags", flags)
	return php.Null(), true, nil
}

// classReflectionMethod answers for ReflectionClass and ReflectionObject.
func (in *Interp) classReflectionMethod(ctx *php.Ctx, unit *Unit, held *reflector,
	call *pendingCall, argument func(int) php.Value) (php.Value, bool, error) {
	class := held.class
	switch strings.ToLower(call.builtinMethod) {
	case "getname":
		// Reflection answers with the *real* name, NUL and all: an anonymous
		// class compares equal to `get_class()` only because both keep the
		// file-and-line suffix. Cutting it here is a diagnostic's rule, not a
		// reflector's.
		return php.String(class.Name), true, nil
	case "getshortname":
		name := class.Name
		if at := strings.LastIndex(name, "\\"); at >= 0 {
			name = name[at+1:]
		}
		return php.String(name), true, nil
	case "isinterface":
		return php.Bool(class.decl.GetKind() == pb.Class_KIND_INTERFACE), true, nil
	case "isenum":
		return php.Bool(class.decl.GetKind() == pb.Class_KIND_ENUM), true, nil
	case "isabstract":
		return php.Bool(class.decl.GetAbstract()), true, nil
	case "isfinal":
		return php.Bool(class.decl.GetFinal()), true, nil
	case "hasmethod":
		return php.Bool(unit.hasMethod(class, argument(0).String(), true)), true, nil
	case "hasproperty":
		_, found := unit.reflectableProperty(class, argument(0).String())
		return php.Bool(found), true, nil
	case "getinterfacenames":
		names := php.NewArray()
		for _, source := range unit.abstractSources(class) {
			if source != class && source.decl.GetKind() == pb.Class_KIND_INTERFACE {
				names.Append(php.String(php.DisplayName(source.Name)))
			}
		}
		return php.Arr(names), true, nil
	case "implementsinterface":
		return php.Bool(unit.instanceOfName(class, argument(0).String())), true, nil
	case "getattributes":
		return in.reflectAttributes(ctx, unit, class.decl.GetAttributes(), class,
			class.decl.GetFilename(), argument(0), argument(1), "ReflectionClass", targetClass)
	case "getparentclass":
		parent, known := unit.findClass(class.Parent)
		if class.Parent == "" || !known {
			return php.Bool(false), true, nil
		}
		return in.reflectClass(unit, parent)
	case "getmethod":
		name := argument(0).String()
		body, declaring, found := unit.reflectableMethod(class, name)
		if !found {
			return php.Undef(), true, Raise("ReflectionException",
				"Method %s::%s() does not exist", php.DisplayName(class.Name), name)
		}
		return in.reflectMember(unit, "ReflectionMethod",
			&reflector{function: body, class: declaring, name: composedMethodName(class, name, body)},
			composedMethodName(class, name, body), declaring)
	case "getproperty":
		name := argument(0).String()
		// Reflection describes static properties too, which the instance
		// lookup deliberately hides.
		prop, found := unit.reflectableProperty(class, name)
		if !found {
			return php.Undef(), true, Raise("ReflectionException",
				"Property %s::$%s does not exist", php.DisplayName(class.Name), name)
		}
		return in.reflectMember(unit, "ReflectionProperty",
			&reflector{property: prop, class: class}, name, class)
	case "getreflectionconstant":
		name := argument(0).String()
		constant, owner, found := unit.findClassConstant(class, name)
		if !found {
			return php.Bool(false), true, nil
		}
		return in.reflectMember(unit, "ReflectionClassConstant",
			&reflector{constant: constant, class: owner}, name, owner)
	case "getproperties":
		list := php.NewArray()
		for _, prop := range unit.reflectableProperties(class) {
			member, _, err := in.reflectMember(unit, "ReflectionProperty",
				&reflector{property: prop, class: class}, prop.GetName(), class)
			if err != nil {
				return php.Undef(), true, err
			}
			list.Append(member)
		}
		return php.Arr(list), true, nil
	case "newlazyghost", "newlazyproxy", "resetaslazyghost", "resetaslazyproxy",
		"initializelazyobject", "isuninitializedlazyobject",
		"marklazyobjectasinitialized":
		return in.lazyReflection(ctx, unit, class, strings.ToLower(call.builtinMethod), call.args, 0)
	case "newinstancewithoutconstructor":
		if err := instantiable(class); err != nil {
			return php.Undef(), true, err
		}
		return php.Obj(unit.instantiate(class)), true, nil
	case "newinstance", "newinstanceargs":
		if err := instantiable(class); err != nil {
			return php.Undef(), true, err
		}
		return in.reflectionNewInstance(ctx, unit, class, call)
	}
	return php.Undef(), false, nil
}

// instantiable refuses the declarations that have no instances, with the
// wording each kind earns.
func instantiable(class *Class) error {
	switch class.decl.GetKind() {
	case pb.Class_KIND_INTERFACE:
		return Raise("Error", "Cannot instantiate interface %s", php.DisplayName(class.Name))
	case pb.Class_KIND_TRAIT:
		return Raise("Error", "Cannot instantiate trait %s", php.DisplayName(class.Name))
	case pb.Class_KIND_ENUM:
		return Raise("Error", "Cannot instantiate enum %s", php.DisplayName(class.Name))
	}
	if class.decl.GetAbstract() {
		return Raise("Error", "Cannot instantiate abstract class %s", php.DisplayName(class.Name))
	}
	if internalOnly[strings.ToLower(class.Name)] {
		return Raise("Error", "The %q class is reserved for internal use and cannot be "+
			"manually instantiated", class.Name)
	}
	return nil
}

// internalOnly names the engine classes only the engine may create. They exist
// so that a program can catch them, not so that it can raise them.
var internalOnly = map[string]bool{
	"fibererror": true,
}

// reflectMember wraps a declaration in the reflector class that describes it.
// The two public properties every member reflector carries are set here, since
// PHP exposes them as properties rather than through accessors.
func (in *Interp) reflectMember(unit *Unit, className string, held *reflector,
	name string, owner *Class) (php.Value, bool, error) {
	object := unit.instantiateBuiltin(className)
	if object == nil {
		return php.Null(), true, nil
	}
	object.Internal = held
	object.Set("name", php.String(name))
	if owner != nil {
		object.Set("class", php.String(php.DisplayName(owner.Name)))
	}
	return php.Obj(object), true, nil
}

// reflectionNewInstance is `newInstance(...$args)` and its array form. Unlike
// newInstanceWithoutConstructor it runs the constructor.
func (in *Interp) reflectionNewInstance(ctx *php.Ctx, unit *Unit, class *Class,
	call *pendingCall) (php.Value, bool, error) {
	args := call.args
	object := unit.instantiate(class)
	body, declaring, found := unit.resolveMethod(class, "__construct")
	if strings.EqualFold(call.builtinMethod, "newinstanceargs") {
		list := php.NewArray()
		if len(args) > 0 && args[0].Deref().Kind() == php.KindArray {
			list = args[0].Deref().Array()
		}
		positional, named := namedFromArray(list)
		resolved, err := in.resolveArgumentNames(ctx, unit, body, positional, named)
		if err != nil {
			return php.Undef(), true, err
		}
		args = resolved
	}
	if !found {
		return php.Obj(object), true, nil
	}
	// The engine looks the constructor up with a *fake scope* of the reflected
	// class, so a private one the class inherited is not found at all and the
	// ordinary lookup Error is raised, naming that class as the scope. Only a
	// constructor the class can reach reaches the reflector's own refusal,
	// which is about it not being public.
	if !unit.methodAccessible(body, declaring, class) {
		owner := class
		if declaring != nil {
			owner = declaring
		}
		return php.Undef(), true, Raise("Error", "Call to %s %s::__construct() from %s",
			body.GetVisibility(), php.DisplayName(owner.Name), scopeName(class))
	}
	if body.GetVisibility() != "" && body.GetVisibility() != "public" {
		return php.Undef(), true, Raise("ReflectionException",
			"Access to non-public constructor of class %s", php.DisplayName(class.Name))
	}
	if _, err := in.invoke(ctx, unit, body, object, declaring, class, args, 0); err != nil {
		return php.Undef(), true, err
	}
	return php.Obj(object), true, nil
}

// namedFromArray splits an argument array into the positional values and the
// named arguments its string keys stand for. invokeArgs and newInstanceArgs
// take their arguments this way, and a string key names a parameter there
// exactly as it does for call_user_func_array.
func namedFromArray(list *php.Array) ([]php.Value, []namedArgument) {
	var positional []php.Value
	var named []namedArgument
	list.Each(func(k php.Key, v php.Value) bool {
		if k.IsInt {
			positional = append(positional, v.Deref())
			return true
		}
		named = append(named, namedArgument{name: k.String(), value: v.Deref()})
		return true
	})
	return positional, named
}

// resolveArgumentNames places named arguments into the positions the callee
// declares for them, the way a call site would. A name with no parameter is an
// error unless the callee is variadic, which keeps it as a string key.
func (in *Interp) resolveArgumentNames(ctx *php.Ctx, unit *Unit, callee *pb.OpArray,
	args []php.Value, named []namedArgument) ([]php.Value, error) {
	if len(named) == 0 || callee == nil {
		return args, nil
	}
	pending := &pendingCall{name: callee.GetName(), method: callee, args: args, named: named}
	if err := in.applyNamedArguments(ctx, unit, pending); err != nil {
		return nil, err
	}
	return pending.args, nil
}

// reflectAttributes wraps a declaration's attribute list in ReflectionAttribute
// objects, optionally keeping only those of one class. PHP's filter argument
// defaults to matching the name exactly.
func (in *Interp) reflectAttributes(ctx *php.Ctx, unit *Unit, attributes []*pb.Attribute,
	scope *Class, file string, filter, flags php.Value, owner string, target int64) (php.Value, bool, error) {
	wanted := ""
	if filter.Kind() == php.KindString {
		wanted = filter.String()
	}
	// The only flag is IS_INSTANCEOF; anything else is refused rather than
	// ignored, and the refusal names the class that declares the method.
	mode := int64(0)
	if flags.Kind() == php.KindInt {
		mode = flags.Int()
	}
	if mode != 0 && mode != attributeIsInstanceOf {
		return php.Undef(), true, Raise("Error",
			"%s::getAttributes(): Argument #2 ($flags) must be a valid attribute filter flag",
			owner)
	}
	var base *Class
	if mode == attributeIsInstanceOf && wanted != "" {
		// Matching by base type needs the base: a name that does not resolve is
		// the program's mistake, unlike an attribute class that does not.
		found, known := in.resolveTypeClass(ctx, unit, wanted)
		if !known {
			return php.Undef(), true, Raise("Error", "Class \"%s\" not found", wanted)
		}
		base = found
	}
	list := php.NewArray()
	for _, attribute := range attributes {
		if wanted != "" && !in.attributeMatches(ctx, unit, attribute, wanted, base) {
			continue
		}
		object := unit.instantiateBuiltin("ReflectionAttribute")
		if object == nil {
			return php.Undef(), true, nil
		}
		object.Internal = &reflector{attribute: attribute, scope: scope, file: file, target: target}
		// The engine declares `public readonly string $name`, so a var_dump of
		// the reflector shows it. getName() answers from the same place.
		object.Set("name", php.String(php.DisplayName(attribute.GetName())))
		list.Append(php.Obj(object))
	}
	return php.Arr(list), true, nil
}

// attributeMatches decides whether one attribute passes the filter. Without
// IS_INSTANCEOF the name is compared as written; with it the attribute's class
// has to descend from the base, and an attribute class the program never
// declared simply does not match.
func (in *Interp) attributeMatches(ctx *php.Ctx, unit *Unit, attribute *pb.Attribute,
	wanted string, base *Class) bool {
	name := strings.TrimPrefix(attribute.GetName(), "\\")
	if base == nil {
		return strings.EqualFold(name, strings.TrimPrefix(wanted, "\\"))
	}
	class, known := unit.findClass(name)
	if !known {
		return false
	}
	return unit.instanceOfName(class, base.Name)
}

// attributeMethod answers for ReflectionAttribute.
func (in *Interp) attributeMethod(ctx *php.Ctx, unit *Unit, held *reflector,
	call *pendingCall, argument func(int) php.Value) (php.Value, bool, error) {
	switch strings.ToLower(call.builtinMethod) {
	case "getname":
		return php.String(php.DisplayName(held.attribute.GetName())), true, nil
	case "getarguments":
		return in.attributeArguments(ctx, unit, held)
	case "newinstance":
		return in.attributeInstance(ctx, unit, held)
	}
	return php.Undef(), false, nil
}

// attributeArguments evaluates the arguments as written: positional ones land
// under their index, named ones under their name. They are evaluated here
// rather than at harvest because an argument may name a constant the program
// defines later.
func (in *Interp) attributeArguments(ctx *php.Ctx, unit *Unit, held *reflector) (php.Value, bool, error) {
	list := php.NewArray()
	for _, arg := range held.attribute.GetArgs() {
		value, ok, err := in.constantLiteral(ctx, unit, arg.GetValue(), held.scope, 0)
		if err != nil {
			return php.Undef(), true, err
		}
		if !ok {
			value = php.Null()
		}
		if arg.GetName() == "" {
			list.Append(value)
			continue
		}
		list.Set(php.StringKey(arg.GetName()), value)
	}
	return php.Arr(list), true, nil
}

// attributeInstance is `newInstance()`: the attribute's class is constructed
// with the arguments as written, named ones included.
func (in *Interp) attributeInstance(ctx *php.Ctx, unit *Unit, held *reflector) (php.Value, bool, error) {
	name := held.attribute.GetName()
	class, known := in.resolveTypeClass(ctx, unit, name)
	if !known {
		return php.Undef(), true, Raise("Error",
			"Attribute class \"%s\" not found", php.DisplayName(name))
	}
	// Instantiating an attribute validates the *attribute class's own*
	// `#[Attribute]` first, which is where a bad flags value or an argument
	// naming something undefined is discovered.
	if err := in.validateAttributeClass(ctx, unit, class, held.target); err != nil {
		return php.Undef(), true, err
	}
	body, declaring, found := unit.resolveMethod(class, "__construct")
	if found && !unit.methodAccessible(body, declaring, unit.callingScope(ctx)) {
		// The class named is the one that declared the constructor, not the
		// one being instantiated.
		owner := class
		if declaring != nil {
			owner = declaring
		}
		return php.Undef(), true, Raise("Error", "Call to %s %s::__construct() from %s",
			body.GetVisibility(), php.DisplayName(owner.Name), scopeName(unit.callingScope(ctx)))
	}
	object := unit.instantiate(class)
	var positional []php.Value
	var named []namedArgument
	for _, arg := range held.attribute.GetArgs() {
		value, ok, err := in.constantLiteral(ctx, unit, arg.GetValue(), held.scope, 0)
		if err != nil {
			return php.Undef(), true, err
		}
		if !ok {
			value = php.Null()
		}
		if arg.GetName() == "" {
			positional = append(positional, value)
			continue
		}
		named = append(named, namedArgument{name: arg.GetName(), value: value})
	}
	if !found {
		// An engine class has no harvested constructor, but it does have one:
		// `#[Attribute]` on Attribute itself is the case that needs it, and
		// leaving the object unconstructed left its typed $flags with no value.
		if _, err := in.builtinMethodCall(ctx, unit, &pendingCall{
			name: php.DisplayName(class.Name) + "::__construct", this: object,
			builtinMethod: "__construct", args: positional,
		}); err != nil {
			var unsupported *UnsupportedError
			if !errors.As(err, &unsupported) {
				return php.Undef(), true, err
			}
			// The class has no constructor this runtime models. With no
			// arguments to place there is nothing lost; with some, the object
			// would be silently wrong.
			if len(positional)+len(named) > 0 {
				return php.Undef(), true, err
			}
		}
		return php.Obj(object), true, nil
	}
	pending := &pendingCall{
		name: php.DisplayName(class.Name) + "::__construct", this: object,
		method: body, class: declaring, called: class,
		args: positional, named: named,
	}
	if err := in.applyNamedArguments(ctx, unit, pending); err != nil {
		return php.Undef(), true, err
	}
	// Whatever the constructor complains about is attributed to where the
	// attribute was *written*, not to the newInstance() call.
	file, line := ctx.Location()
	ctx.SetLocation(held.file, held.attribute.GetLine())
	ctx.PushCall(php.StackFrame{
		Function: php.DisplayName(class.Name) + "->__construct",
		File:     held.file, Line: held.attribute.GetLine(), Args: pending.args, This: object,
	})
	defer func() {
		ctx.PopCall()
		ctx.SetLocation(file, line)
	}()
	if _, err := in.invoke(ctx, unit, body, object, declaring, class, pending.args, 0); err != nil {
		return php.Undef(), true, err
	}
	return php.Obj(object), true, nil
}

// functionTarget distinguishes a method from a plain function, which the two
// TARGET_* bits keep apart.
func functionTarget(held *reflector) int64 {
	if held.class != nil && !held.realClosure {
		return targetMethod
	}
	return targetFunction
}

// targetNames renders a flags value as the "allowed targets" list, in the order
// the engine declares the constants.
func targetNames(flags int64) string {
	var names []string
	for _, entry := range []struct {
		bit  int64
		name string
	}{
		{targetClass, "class"},
		{targetFunction, "function"},
		{targetMethod, "method"},
		{targetProperty, "property"},
		{targetClassConstant, "class constant"},
		{targetParameter, "parameter"},
	} {
		if flags&entry.bit != 0 {
			names = append(names, entry.name)
		}
	}
	return strings.Join(names, ", ")
}

// validateAttributeClass checks the `#[Attribute]` written on an attribute
// class: it has to be there, its flags argument has to evaluate, the value has
// to name only bits the engine defines, and it has to allow the place the
// attribute was actually written. The flags are returned so the caller can
// check the target.
func (in *Interp) validateAttributeClass(ctx *php.Ctx, unit *Unit, class *Class, target int64) error {
	var marker *pb.Attribute
	for _, attribute := range class.decl.GetAttributes() {
		if strings.EqualFold(strings.TrimPrefix(attribute.GetName(), "\\"), "Attribute") {
			marker = attribute
			break
		}
	}
	if marker == nil {
		return Raise("Error", "Attempting to use non-attribute class \"%s\" as attribute",
			php.DisplayName(class.Name))
	}
	allowed := int64(attributeTargetAll)
	if len(marker.GetArgs()) > 0 {
		flags, ok, err := in.constantLiteral(ctx, unit, marker.GetArgs()[0].GetValue(), class, 0)
		if err != nil || !ok {
			return err
		}
		if flags.Kind() != php.KindInt {
			// The constructor's own parameter check comes first, and it is
			// strict here: even a numeric string is refused.
			return Raise("Error",
				"Attribute::__construct(): Argument #1 ($flags) must be of type int, %s given",
				givenTypeName(flags))
		}
		if flags.Int() < 0 || flags.Int()&^(attributeTargetAll|attributeIsRepeatable) != 0 {
			return Raise("Error", "Invalid attribute flags specified")
		}
		allowed = flags.Int()
	}
	if target != 0 && allowed&target == 0 {
		return Raise("Error", "Attribute %q cannot target %s (allowed targets: %s)",
			php.DisplayName(class.Name), targetNames(target),
			targetNames(allowed&attributeTargetAll))
	}
	return nil
}

// instantiateBuiltin makes an instance of an engine class the runtime models.
func (u *Unit) instantiateBuiltin(name string) *php.Object {
	class, ok := u.findClass(name)
	if !ok {
		return nil
	}
	return u.instantiate(class)
}

// memberConstruct builds the reflectors that name something *inside* a class,
// plus ReflectionFunction. They share a shape: resolve the subject, then hold
// the declaration the harvest already carries.
func (in *Interp) memberConstruct(ctx *php.Ctx, unit *Unit, object *php.Object,
	args []php.Value) (php.Value, bool, error) {
	first, second := php.Undef(), php.Undef()
	if len(args) > 0 {
		first = args[0].Deref()
	}
	if len(args) > 1 {
		second = args[1].Deref()
	}
	switch strings.ToLower(object.ClassName) {
	case "reflectionfunction":
		if fn, ok := asClosure(first); ok && fn.forwardTo != "" {
			// A first-class callable of a method the class does not declare is
			// a closure over the *forwarder*, but reflection does not describe
			// the forwarder. The engine hands back a trampoline named after the
			// method that was asked for, taking one `mixed ...$arguments` and
			// declaring no return type. Probed.
			object.Internal = &reflector{
				function: &pb.OpArray{
					Name:   fn.forwardTo,
					Args:   []*pb.ArgInfo{{Name: "arguments", Variadic: true, Type: "mixed"}},
					Static: fn.this == nil,
				},
				class: fn.class, closureSubject: true, subject: first.Object(),
			}
			object.Set("name", php.String(fn.forwardTo))
			return php.Null(), true, nil
		}
		if fn, ok := asClosure(first); ok && fn.op != nil {
			// A closure carries the scope it was defined in, and an attribute
			// written on it is evaluated there: `#[A(self::class)]` on a
			// closure declared in C1 answers C1, and answers Test1 once the
			// closure has been rebound to it.
			object.Internal = &reflector{function: fn.op, class: fn.class,
				realClosure: !fn.fake, closureSubject: true}
			object.Set("name", php.String(fn.op.GetName()))
			return php.Null(), true, nil
		}
		name := first.String()
		// The subject is still a Closure even when what it wraps is answered
		// for by name, and `__toString` heads its dump with `Closure [`.
		fromClosure := false
		if fn, ok := asClosure(first); ok && fn.builtin != "" {
			// A first-class callable of a plain function is represented by the
			// name rather than by a body, and reflection wants the function it
			// names -- `foo(...)` reflects as `foo`, not as a closure.
			name, fromClosure = fn.builtin, true
		}
		fn, ok := unit.functions[functionKey(name)]
		if !ok {
			if !in.functionExists(unit, name) {
				return php.Undef(), true, Raise("ReflectionException",
					"Function %s() does not exist", name)
			}
			// An engine function has no op_array to describe, so only its name
			// is answerable; anything else about it stays an honest gap.
			object.Internal = &reflector{internal: name, closureSubject: fromClosure}
			object.Set("name", php.String(name))
			return php.Null(), true, nil
		}
		object.Internal = &reflector{function: fn, closureSubject: fromClosure}
		object.Set("name", php.String(fn.GetName()))
		return php.Null(), true, nil

	case "reflectionparameter":
		// (function-or-[class, method], name-or-position). The function half
		// is resolved the way a callable is, since that is what it is.
		fn, class, err := in.reflectionParameterOwner(ctx, unit, first)
		if err != nil {
			return php.Undef(), true, err
		}
		args := fn.GetArgs()
		index := -1
		if second.Kind() == php.KindInt {
			index = int(second.Int())
			if index < 0 || index >= len(args) {
				return php.Undef(), true, Raise("ReflectionException",
					"The parameter specified by its offset %d could not be found",
					second.Int())
			}
		} else {
			for i, arg := range args {
				if arg.GetName() == second.String() {
					index = i
					break
				}
			}
			if index < 0 {
				return php.Undef(), true, Raise("ReflectionException",
					"The parameter specified by its name %s could not be found",
					second.String())
			}
		}
		object.Internal = &reflector{
			parameter: args[index], function: fn, class: class, position: index,
		}
		object.Set("name", php.String(args[index].GetName()))
		return php.Null(), true, nil

	case "reflectionmethod":
		// Either (class-or-object, name) or the single "Class::method" form.
		subject, method := first, second.String()
		if second.Kind() == php.KindUndef {
			ctx.Deprecated("Calling ReflectionMethod::__construct() with 1 argument is " +
				"deprecated, use ReflectionMethod::createFromMethodName() instead")
			className, methodName, found := strings.Cut(first.String(), "::")
			if !found {
				return php.Undef(), true, Raise("ReflectionException",
					"Invalid method name %s", first.String())
			}
			subject, method = php.String(className), methodName
		}
		// Closure declares no `__invoke`; the engine answers the lookup with a
		// trampoline that carries the *wrapped* function's body -- its
		// parameters, attributes and deprecation -- under the name
		// `Closure::__invoke`. So the signature comes from the closure and the
		// identity does not.
		if fn, isClosure := asClosure(subject); isClosure && fn.op != nil &&
			strings.EqualFold(method, magicInvoke) {
			scope, _ := unit.findClass("Closure")
			object.Internal = &reflector{function: fn.op, class: scope, bound: fn.this,
				name: magicInvoke, trampoline: true, realClosure: !fn.fake}
			object.Set("name", php.String(magicInvoke))
			object.Set("class", php.String("Closure"))
			return php.Null(), true, nil
		}
		class, bound, err := in.reflectionSubject(ctx, unit, subject)
		if err != nil {
			return php.Undef(), true, err
		}
		body, declaring, found := unit.reflectableMethod(class, method)
		if !found {
			return php.Undef(), true, Raise("ReflectionException",
				"Method %s::%s() does not exist", php.DisplayName(class.Name), method)
		}
		shown := composedMethodName(class, method, body)
		object.Internal = &reflector{function: body, class: declaring, bound: bound, name: shown}
		object.Set("name", php.String(shown))
		object.Set("class", php.String(php.DisplayName(declaring.Name)))
		return php.Null(), true, nil

	case "reflectionproperty":
		class, bound, err := in.reflectionSubject(ctx, unit, first)
		if err != nil {
			return php.Undef(), true, err
		}
		name := second.String()
		prop, found := unit.reflectableProperty(class, name)
		dynamic := false
		if !found {
			// A property the object grew at run time is reflectable through
			// *that object*: `new ReflectionProperty($c, "dyn")` answers a
			// public, non-default property. Reached through the class name
			// instead there is nothing to find, and the refusal stands.
			if _, held := boundProperty(bound, name); !held {
				return php.Undef(), true, Raise("ReflectionException",
					"Property %s::$%s does not exist", php.DisplayName(class.Name), name)
			}
			prop = &pb.Property{
				Name:           name,
				Visibility:     "public",
				DeclaringClass: class.Name,
			}
			dynamic = true
		}
		object.Internal = &reflector{property: prop, class: class, bound: bound,
			dynamic: dynamic}
		object.Set("name", php.String(name))
		object.Set("class", php.String(php.DisplayName(class.Name)))
		return php.Null(), true, nil

	case "reflectionclassconstant":
		class, _, err := in.reflectionSubject(ctx, unit, first)
		if err != nil {
			return php.Undef(), true, err
		}
		name := second.String()
		constant, owner, found := unit.findClassConstant(class, name)
		if !found {
			return php.Undef(), true, Raise("ReflectionException",
				"Constant %s::%s does not exist", php.DisplayName(class.Name), name)
		}
		object.Internal = &reflector{constant: constant, class: owner}
		object.Set("name", php.String(name))
		object.Set("class", php.String(php.DisplayName(owner.Name)))
		return php.Null(), true, nil
	}
	return php.Undef(), false, nil
}

// boundProperty reads a name off the object a reflector was built from, which
// is how a dynamic property is found: it exists on the instance and nowhere in
// the class table.
func boundProperty(object *php.Object, name string) (php.Value, bool) {
	if object == nil {
		return php.Undef(), false
	}
	return object.Get(name)
}

// reflectionSubject resolves the "class or object" argument these constructors
// share, keeping the object when there was one so getValue() has something to
// read from.
func (in *Interp) reflectionSubject(ctx *php.Ctx, unit *Unit, subject php.Value) (*Class, *php.Object, error) {
	if subject.Kind() == php.KindObject {
		object := subject.Object()
		class, known := unit.findClass(object.ClassName)
		if !known {
			return nil, nil, Raise("ReflectionException",
				"Class \"%s\" does not exist", object.ClassName)
		}
		return class, object, nil
	}
	name := subject.String()
	class, known := in.resolveTypeClass(ctx, unit, name)
	if !known {
		return nil, nil, Raise("ReflectionException", "Class \"%s\" does not exist", name)
	}
	return class, nil, nil
}

// functionReflectionMethod answers for ReflectionFunction and ReflectionMethod,
// which share everything except how they are called.
// splitFunctionName separates a function's namespace from its short name. A
// closure is in no namespace whatever its compiled name contains: that name is
// `{closure:WHERE:LINE}`, and WHERE may itself be a namespaced method -- so the
// separator inside it is not one of the function's own.
func splitFunctionName(name string) (namespace, short string) {
	if strings.HasPrefix(name, "{closure") {
		return "", name
	}
	if at := strings.LastIndex(name, "\\"); at >= 0 {
		return name[:at], name[at+1:]
	}
	return "", name
}

func (in *Interp) functionReflectionMethod(ctx *php.Ctx, unit *Unit, held *reflector,
	call *pendingCall, argument func(int) php.Value) (php.Value, bool, error) {
	fn := held.function
	switch strings.ToLower(call.builtinMethod) {
	case "getname":
		if held.trampoline {
			return php.String(held.name), true, nil
		}
		return php.String(fn.GetName()), true, nil
	case "getshortname":
		namespace, short := splitFunctionName(fn.GetName())
		_ = namespace
		return php.String(short), true, nil
	case "getnamespacename":
		namespace, _ := splitFunctionName(fn.GetName())
		return php.String(namespace), true, nil
	case "innamespace":
		namespace, _ := splitFunctionName(fn.GetName())
		return php.Bool(namespace != ""), true, nil
	case "getnumberofparameters":
		return php.Int(int64(len(fn.GetArgs()))), true, nil
	case "getnumberofrequiredparameters":
		return php.Int(int64(fn.GetRequiredArgs())), true, nil
	case "isstatic":
		return php.Bool(fn.GetStatic()), true, nil
	case "isabstract":
		return php.Bool(fn.GetAbstract()), true, nil
	case "isdeprecated":
		// #[\Deprecated] is what marks a *user* declaration deprecated; the
		// harvest records it on the op_array.
		return php.Bool(fn.GetDeprecated()), true, nil
	case "isfinal":
		return php.Bool(unit.effectiveFinal(held.class, fn, fn.GetName())), true, nil
	case "ispublic", "isprivate", "isprotected":
		// `use T { m as protected; }` changes the visibility for the composing
		// class without touching the trait, so the answer belongs to the pair.
		visibility := unit.effectiveVisibility(held.class, fn, fn.GetName())
		switch strings.ToLower(call.builtinMethod) {
		case "isprivate":
			return php.Bool(visibility == "private"), true, nil
		case "isprotected":
			return php.Bool(visibility == "protected"), true, nil
		}
		return php.Bool(visibility == "" || visibility == "public"), true, nil
	case "isvariadic":
		return php.Bool(variadicArgs(fn)), true, nil
	case "returnsreference":
		return php.Bool(fn.GetReturnsRef()), true, nil
	case "setaccessible":
		// Reflection has ignored this since PHP 8.1; it exists to be called.
		return php.Null(), true, nil
	case "getdeclaringclass":
		if held.class == nil {
			return php.Null(), true, nil
		}
		return in.reflectClass(unit, held.class)
	case "hasreturntype":
		return php.Bool(fn.GetReturnType() != ""), true, nil
	case "getreturntype":
		return in.reflectType(unit, fn.GetReturnType())
	case "getattributes":
		return in.reflectAttributes(ctx, unit, fn.GetAttributes(), held.class,
			fn.GetFilename(), argument(0), argument(1), "ReflectionFunctionAbstract",
			functionTarget(held))
	case "getparameters":
		list := php.NewArray()
		for i, arg := range fn.GetArgs() {
			parameter := unit.born(php.NewObject("ReflectionParameter", unit.handle()))
			parameter.Internal = &reflector{
				parameter: arg, function: fn, class: held.class, position: i,
			}
			parameter.Set("name", php.String(arg.GetName()))
			list.Append(php.Obj(parameter))
		}
		return php.Arr(list), true, nil
	case "invoke", "invokeargs":
		return in.reflectionInvoke(ctx, unit, held, call)
	case "getclosure":
		// The same closure the first-class callable syntax makes: it wraps a
		// function that already exists rather than being written as one, which
		// is what `fake` records and what keeps its static variables shared
		// with every other way of calling it.
		scopeName := ""
		if held.class != nil {
			scopeName = held.class.Name
		}
		wrapper := unit.born(php.NewObject("Closure", unit.handle()))
		wrapper.Internal = &closure{
			op: fn, bound: map[string]php.Value{}, this: held.bound,
			class: held.class, fake: true, scopeName: scopeName,
		}
		return php.Obj(wrapper), true, nil
	}
	return php.Undef(), false, nil
}

// reflectionParameterOwner resolves what a ReflectionParameter was built over.
// The first constructor argument is a callable in every spelling the language
// has one: a function name, a [class, method] pair, or a Closure.
func (in *Interp) reflectionParameterOwner(ctx *php.Ctx, unit *Unit,
	subject php.Value) (*pb.OpArray, *Class, error) {
	if fn, ok := asClosure(subject); ok && fn.op != nil {
		return fn.op, fn.class, nil
	}
	if subject.Kind() == php.KindArray && subject.Array().Len() == 2 {
		target, _ := subject.Array().Get(php.IntKey(0))
		method, _ := subject.Array().Get(php.IntKey(1))
		class, _, err := in.reflectionSubject(ctx, unit, target.Deref())
		if err != nil {
			return nil, nil, err
		}
		body, declaring, found := unit.resolveMethod(class, method.Deref().String())
		if !found {
			return nil, nil, Raise("ReflectionException",
				"Method %s::%s() does not exist",
				php.DisplayName(class.Name), method.Deref().String())
		}
		return body, declaring, nil
	}
	name := subject.String()
	if body, ok := unit.functions[functionKey(name)]; ok {
		return body, nil, nil
	}
	return nil, nil, Raise("ReflectionException", "Function %s() does not exist", name)
}

// parameterReflectionMethod answers for ReflectionParameter. Everything it can
// say comes from the harvested ArgInfo, which carries the name, the declared
// type, and the two flags -- so the questions about attributes and about a
// default value have no data behind them and stay honest gaps rather than
// answers made up from what is here.
func (in *Interp) parameterReflectionMethod(ctx *php.Ctx, unit *Unit, held *reflector,
	call *pendingCall, argument func(int) php.Value) (php.Value, bool, error) {
	arg := held.parameter
	switch strings.ToLower(call.builtinMethod) {
	case "getname":
		return php.String(arg.GetName()), true, nil
	case "getposition":
		return php.Int(int64(held.position)), true, nil
	case "hastype":
		return php.Bool(arg.GetType() != ""), true, nil
	case "gettype":
		return in.reflectType(unit, arg.GetType())
	case "ispassedbyreference":
		return php.Bool(arg.GetByRef()), true, nil
	case "canbepassedbyvalue":
		return php.Bool(!arg.GetByRef()), true, nil
	case "isvariadic":
		return php.Bool(arg.GetVariadic()), true, nil
	case "isoptional", "isdefaultvalueavailable":
		// Everything from the required count on has a default, and a variadic
		// is optional whatever the count says.
		return php.Bool(arg.GetVariadic() ||
			held.position >= int(held.function.GetRequiredArgs())), true, nil
	case "allowsnull":
		return php.Bool(arg.GetType() == "" || admitsNull(arg.GetType())), true, nil
	case "getdeclaringclass":
		if held.class == nil {
			return php.Null(), true, nil
		}
		return in.reflectClass(unit, held.class)
	case "iscallable":
		// Deprecated in favour of getType(), and the deprecation names the
		// replacement rather than merely reporting the method.
		ctx.Deprecated("Method ReflectionParameter::isCallable() is deprecated since 8.0, " +
			"use ReflectionParameter::getType() instead")
		return php.Bool(acceptsCallable(arg.GetType())), true, nil
	case "isarray":
		ctx.Deprecated("Method ReflectionParameter::isArray() is deprecated since 8.0, " +
			"use ReflectionParameter::getType() instead")
		return php.Bool(strings.TrimPrefix(arg.GetType(), "?") == "array"), true, nil
	case "getattributes":
		return in.reflectAttributes(ctx, unit, arg.GetAttributes(), held.class,
			held.function.GetFilename(), argument(0), argument(1),
			"ReflectionParameter", targetParameter)
	}
	return php.Undef(), false, nil
}

// reflectableMethod finds a method a reflector can describe. A method the
// engine supplies has no op_array, but the harvest recorded its signature, and
// that is enough for everything a ReflectionMethod answers -- `DateTime` has a
// constructor even though nothing was compiled for it.
func (u *Unit) reflectableMethod(class *Class, name string) (*pb.OpArray, *Class, bool) {
	if body, declaring, found := u.resolveMethod(class, name); found {
		return body, declaring, true
	}
	for current := class; current != nil; {
		if op, found := u.prototypeMethod(current, strings.ToLower(name)); found && op != nil {
			return op, current, true
		}
		if current.Parent == "" {
			break
		}
		next, ok := u.findClass(current.Parent)
		if !ok {
			break
		}
		current = next
	}
	return nil, nil, false
}

// composedMethodName is the name a class holds a method under. It is the
// method's own unless a trait alias renamed it on the way in, which the
// requested name reveals: `use T { test as __construct; }` is asked for as
// __construct and answers with T::test's body.
func composedMethodName(class *Class, requested string, body *pb.OpArray) string {
	if class != nil && !strings.EqualFold(requested, body.GetName()) {
		if _, aliased := class.methods[strings.ToLower(requested)]; !aliased {
			return requested
		}
	}
	return body.GetName()
}

// reflectionInvoke calls what a ReflectionFunction or ReflectionMethod names.
// A method takes the object as its first argument; invokeArgs takes the rest as
// an array rather than as a list.
func (in *Interp) reflectionInvoke(ctx *php.Ctx, unit *Unit, held *reflector,
	call *pendingCall) (php.Value, bool, error) {
	args := call.args
	object := held.bound
	if held.class != nil {
		// ReflectionMethod::invoke($object, ...$args)
		if len(args) > 0 {
			if first := args[0].Deref(); first.Kind() == php.KindObject {
				object = first.Object()
			}
			args = args[1:]
		}
	}
	if strings.EqualFold(call.builtinMethod, "invokeargs") {
		list := php.NewArray()
		if len(args) > 0 && args[0].Deref().Kind() == php.KindArray {
			list = args[0].Deref().Array()
		}
		var named []namedArgument
		args, named = namedFromArray(list)
		resolved, err := in.resolveArgumentNames(ctx, unit, held.function, args, named)
		if err != nil {
			return php.Undef(), true, err
		}
		args = resolved
	}
	called := held.class
	if object != nil {
		if current, known := unit.findClass(object.ClassName); known {
			called = current
		}
	}
	value, err := in.invoke(ctx, unit, held.function, object, held.class, called, args, 0)
	return value, true, err
}

// propertyReflectionMethod answers for ReflectionProperty.
func (in *Interp) propertyReflectionMethod(ctx *php.Ctx, unit *Unit, held *reflector,
	call *pendingCall, argument func(int) php.Value) (php.Value, bool, error) {
	prop := held.property
	switch strings.ToLower(call.builtinMethod) {
	case "getname":
		return php.String(prop.GetName()), true, nil
	case "isstatic":
		return php.Bool(prop.GetStatic()), true, nil
	case "hasdefaultvalue":
		// A *hooked* property has no default even when the source wrote one --
		// and a typed property with no initializer has none either, while an
		// untyped one defaults to null whether or not it was written.
		return php.Bool(!held.dynamic && !prop.GetVirtual() &&
			(prop.GetHasDefault() || prop.GetType() == "")), true, nil
	case "getdefaultvalue":
		if held.dynamic || prop.GetVirtual() || !prop.GetHasDefault() {
			return php.Null(), true, nil
		}
		value := literalValue(prop.GetDefaultValue())
		if resolved, ok := unit.defaults[prop]; ok {
			value = resolved
		}
		return value.Copy(), true, nil
	case "gethook":
		body := prop.GetGetHook()
		if which := argument(0).Deref(); which.Kind() == php.KindObject {
			if name, ok := which.Object().Get("name"); ok &&
				strings.EqualFold(name.String(), "Set") {
				body = prop.GetSetHook()
			}
		}
		if held.dynamic || body == nil {
			return php.Null(), true, nil
		}
		// A hook reflects as a ReflectionMethod named `$prop::get`, which is
		// the name the engine compiled it under.
		return in.reflectMember(unit, "ReflectionMethod",
			&reflector{function: body, class: held.class, name: body.GetName()},
			body.GetName(), held.class)
	case "isvirtual":
		// A virtual hooked property has no storage: it exists only through its
		// hooks. A dynamic property has storage and no hooks at all.
		return php.Bool(!held.dynamic && prop.GetVirtual()), true, nil
	case "hashooks":
		return php.Bool(!held.dynamic && hookedProperty(prop)), true, nil
	case "hashook":
		// ReflectionPropertyHookType::Get is 0 and ::Set is 1 -- the enum's
		// cases in declaration order.
		which := argument(0).Deref()
		wanted := prop.GetGetHook()
		if which.Kind() == php.KindObject {
			if name, held := which.Object().Get("name"); held &&
				strings.EqualFold(name.String(), "Set") {
				wanted = prop.GetSetHook()
			}
		}
		return php.Bool(!held.dynamic && wanted != nil), true, nil
	case "isdynamic":
		return php.Bool(held.dynamic), true, nil
	case "isdefault":
		// "Default" means declared, not "has an initializer".
		return php.Bool(!held.dynamic), true, nil
	case "isreadonly":
		return php.Bool(prop.GetReadonly()), true, nil
	case "ispublic":
		return php.Bool(prop.GetVisibility() == "public"), true, nil
	case "isprivate":
		return php.Bool(prop.GetVisibility() == "private"), true, nil
	case "isprotected":
		return php.Bool(prop.GetVisibility() == "protected"), true, nil
	case "setaccessible":
		return php.Null(), true, nil
	case "hastype":
		return php.Bool(prop.GetType() != ""), true, nil
	case "gettype":
		return in.reflectType(unit, prop.GetType())
	case "getdeclaringclass":
		// The class that *wrote* the property, which the reflected class's
		// flattened table records on it -- not the class the lookup went
		// through.
		return in.reflectClass(unit, unit.declaringOf(held.class, prop.GetDeclaringClass()))
	case "getattributes":
		return in.reflectAttributes(ctx, unit, prop.GetAttributes(), held.class,
			classFile(held.class), argument(0), argument(1), "ReflectionProperty", targetProperty)
	case "getvalue":
		if prop.GetStatic() {
			// A static property has no instance to read from: the value lives
			// on the class, and the argument -- if any -- is ignored.
			if ref, ok := unit.staticPropRef(held.class.Name, prop.GetName()); ok {
				return ref.Get().Deref(), true, nil
			}
			return php.Null(), true, nil
		}
		object := held.bound
		if first := argument(0); first.Kind() == php.KindObject {
			object = first.Object()
		}
		if object == nil {
			return php.Null(), true, nil
		}
		value, _ := object.Get(prop.GetName())
		return value.Deref(), true, nil
	case "islazy":
		// A property is lazy when the object is still waiting for its
		// initializer *and* this property is one of the things being waited
		// for. A static belongs to the class, a virtual one has no storage,
		// and a dynamic one was written after the fact -- none of the three is
		// ever deferred.
		subject := lazyTarget(argument(0).Object())
		if subject == nil || !lazyUninitialized(subject) ||
			prop.GetStatic() || prop.GetVirtual() || held.dynamic {
			return php.Bool(false), true, nil
		}
		return php.Bool(!subject.Lazy.Skipped[prop.GetName()]), true, nil
	case "isinitialized":
		// A static property is always "initialized" and takes no object; an
		// instance one *requires* the argument even when the reflector was
		// built from an object, because the engine keeps no reference to it.
		if prop.GetStatic() {
			return php.Bool(true), true, nil
		}
		first := argument(0)
		if first.Kind() != php.KindObject {
			return php.Undef(), true, Raise("TypeError",
				"ReflectionProperty::isInitialized(): Argument #1 ($object) must be "+
					"provided for instance properties")
		}
		subject := first.Object()
		owner := unit.declaringOf(held.class, prop.GetDeclaringClass())
		if owner != nil {
			instance, known := unit.findClass(subject.ClassName)
			if !known || !unit.instanceOfName(instance, owner.Name) {
				return php.Undef(), true, Raise("ReflectionException",
					"Given object is not an instance of the class this property "+
						"was declared in")
			}
		}
		return php.Bool(propertyInitialized(subject, prop.GetName())), true, nil
	case "setvalue":
		if prop.GetStatic() {
			// A static is written as `setValue(null, $value)`. The one-argument
			// spelling still works and is deprecated -- the deprecation is
			// about the argument count, not about statics, so passing the
			// ignored null keeps it quiet.
			written := argument(1)
			if len(call.args) < 2 {
				ctx.Deprecated("Calling ReflectionProperty::setValue() with a " +
					"single argument is deprecated")
				written = argument(0)
			}
			if ref, ok := unit.staticPropRef(held.class.Name, prop.GetName()); ok {
				ref.Set(written.Copy())
			}
			return php.Null(), true, nil
		}
		object, value := held.bound, argument(0)
		if first := argument(0); first.Kind() == php.KindObject {
			object, value = first.Object(), argument(1)
		}
		if object != nil {
			object.Set(prop.GetName(), value.Copy())
		}
		return php.Null(), true, nil

	case "skiplazyinitialization", "setrawvaluewithoutlazyinitialization":
		object := argument(0).Object()
		if object == nil {
			return php.Undef(), true, Raise("TypeError",
				"ReflectionProperty::%s(): Argument #1 ($object) must be of type object, %s given",
				call.builtinMethod, givenTypeName(argument(0)))
		}
		if held.dynamic {
			return php.Undef(), true, Raise("ReflectionException",
				"Can not use %s on dynamic property %s::$%s",
				call.builtinMethod, php.DisplayName(held.class.Name), prop.GetName())
		}
		// A static or virtual property is named by the class that *declared*
		// it -- `prop->ce` -- while the dynamic refusal above has no
		// declaration to name and uses the reflector's own class.
		owner := held.class.Name
		if declaring := prop.GetDeclaringClass(); declaring != "" {
			owner = declaring
		}
		if prop.GetStatic() {
			return php.Undef(), true, Raise("ReflectionException",
				"Can not use %s on static property %s::$%s",
				call.builtinMethod, php.DisplayName(owner), prop.GetName())
		}
		if prop.GetVirtual() {
			return php.Undef(), true, Raise("ReflectionException",
				"Can not use %s on virtual property %s::$%s",
				call.builtinMethod, php.DisplayName(owner), prop.GetName())
		}
		// Both methods ask about the object that holds the state, not about a
		// stand-in: an initialized proxy is followed to its instance first, so
		// skipping on one is the no-op skipping on an ordinary object is.
		object = lazyTarget(object)
		// Neither method asks whether the object is lazy.
		// `reflection_property_check_lazy_compatible` refuses a dynamic, static
		// or virtual property and nothing else: on an ordinary object the raw
		// write is an ordinary write, and skipping does nothing at all.
		raw := strings.EqualFold(call.builtinMethod, "setRawValueWithoutLazyInitialization")
		value := argument(1).Copy()
		if raw && prop.GetReadonly() && propertyInitialized(object, prop.GetName()) {
			// Writing the raw value is still a write: readonly holds, from
			// reflection as from anywhere else, once the slot has a value.
			// skipLazyInitialization writes nothing and is not refused.
			return php.Undef(), true, Raise("Error",
				"Cannot modify readonly property %s::$%s",
				php.DisplayName(held.class.Name), prop.GetName())
		}
		if raw {
			// Skipping the initializer does not skip the declaration: the value
			// still has to be one the property could hold, and a refusal leaves
			// the object as lazy as it was.
			verified, err := in.verifyPropertyWrite(ctx, unit, object, prop.GetName(), value, false)
			if err != nil {
				return php.Undef(), true, err
			}
			value = verified
		}
		if raw && object.Lazy == nil {
			// Nothing is deferred, so there is no laziness to skip -- the
			// value is simply written.
			object.Set(prop.GetName(), value)
			return php.Null(), true, nil
		}
		unit.skipProperty(object, prop, value, raw)
		return php.Null(), true, nil
	}
	return php.Undef(), false, nil
}

// constantReflectionMethod answers for ReflectionClassConstant.
func (in *Interp) constantReflectionMethod(ctx *php.Ctx, unit *Unit, held *reflector,
	call *pendingCall, argument func(int) php.Value) (php.Value, bool, error) {
	constant := held.constant
	switch strings.ToLower(call.builtinMethod) {
	case "getname":
		return php.String(constant.GetName()), true, nil
	case "isfinal":
		return php.Bool(constant.GetFinal()), true, nil
	case "isenumcase":
		return php.Bool(constant.GetIsCase()), true, nil
	case "ispublic":
		return php.Bool(constant.GetVisibility() == "" || constant.GetVisibility() == "public"), true, nil
	case "isprivate":
		return php.Bool(constant.GetVisibility() == "private"), true, nil
	case "isprotected":
		return php.Bool(constant.GetVisibility() == "protected"), true, nil
	case "getdeclaringclass":
		return in.reflectClass(unit, unit.declaringOf(held.class, constant.GetDeclaringClass()))
	case "getattributes":
		return in.reflectAttributes(ctx, unit, constant.GetAttributes(), held.class,
			classFile(held.class), argument(0), argument(1), "ReflectionClassConstant",
			targetClassConstant)
	case "getvalue":
		value, ok, err := in.constantLiteral(ctx, unit, constant.GetValue(), held.class, 0)
		if err != nil || !ok {
			return php.Null(), true, err
		}
		return value, true, nil
	}
	return php.Undef(), false, nil
}

// namedTypeMethod answers for ReflectionNamedType, which is a type as written
// rather than a declaration -- so `?int` is the name `int` that allows null.
func (in *Interp) namedTypeMethod(held *reflector, call *pendingCall) (php.Value, bool, error) {
	nullable := strings.HasPrefix(held.typeName, "?")
	name := strings.TrimPrefix(held.typeName, "?")
	switch strings.ToLower(call.builtinMethod) {
	case "getname":
		return php.String(name), true, nil
	case "__tostring":
		// The string form is the type *as written*, so a nullable one keeps
		// its `?`; getName() answers the name alone.
		return php.String(held.typeName), true, nil
	case "allowsnull":
		return php.Bool(nullable || strings.EqualFold(name, "mixed") ||
			strings.EqualFold(name, "null")), true, nil
	case "isbuiltin":
		return php.Bool(scalarTypes[strings.ToLower(name)] ||
			strings.EqualFold(name, "iterable") || strings.EqualFold(name, "void") ||
			strings.EqualFold(name, "static") || strings.EqualFold(name, "self")), true, nil
	}
	return php.Undef(), false, nil
}

// reflectClass wraps a class in a ReflectionClass, which several accessors
// return.
// declaringOf resolves the class a member records as its own declarer, falling
// back to the class it was looked up through when the name is unknown.
func (u *Unit) declaringOf(found *Class, declaring string) *Class {
	if declaring == "" {
		return found
	}
	if owner, known := u.findClass(declaring); known {
		return owner
	}
	return found
}

func (in *Interp) reflectClass(unit *Unit, class *Class) (php.Value, bool, error) {
	object := unit.instantiateBuiltin("ReflectionClass")
	if object == nil {
		return php.Null(), true, nil
	}
	object.Internal = &reflector{class: class}
	// The declared `$name` holds the real name, NUL suffix and all, so that an
	// anonymous class's reflector compares equal to `get_class()`.
	object.Set("name", php.String(class.Name))
	return php.Obj(object), true, nil
}

// reflectType wraps a declared type. An absent one is null rather than an
// object with an empty name, which is what `hasType()` is for.
func (in *Interp) reflectType(unit *Unit, declared string) (php.Value, bool, error) {
	if declared == "" {
		return php.Null(), true, nil
	}
	object := unit.instantiateBuiltin("ReflectionNamedType")
	if object == nil {
		return php.Null(), true, nil
	}
	object.Internal = &reflector{typeName: declared}
	return php.Obj(object), true, nil
}

// classFile names the file a class was written in, for the diagnostics its
// members' attributes produce.
func classFile(class *Class) string {
	if class == nil {
		return ""
	}
	return class.decl.GetFilename()
}

// findClassConstant looks a constant up through the hierarchy, reporting which
// class declared it.
func (u *Unit) findClassConstant(class *Class, name string) (*pb.ClassConstant, *Class, bool) {
	for _, source := range u.abstractSources(class) {
		for _, constant := range u.constants(source) {
			if constant.GetName() == name {
				return constant, source, true
			}
		}
	}
	return nil, nil, false
}

// isReflectionAttributeClass covers the subclasses of ReflectionAttribute as
// well as the class itself, which is what the inherited private constructor
// needs: `class Sub extends ReflectionAttribute {}` refuses `new Sub()` too.
func isReflectionAttributeClass(unit *Unit, name string) bool {
	if strings.EqualFold(name, "ReflectionAttribute") {
		return true
	}
	class, known := unit.findClass(name)
	return known && unit.instanceOf(class, "ReflectionAttribute")
}
