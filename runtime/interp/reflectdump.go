package interp

import (
	"fmt"
	"sort"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// Every reflector has a __toString that prints its subject as a block of text.
// The format is ext/reflection's `_class_string` and friends, and it is exact
// down to the spacing: `@@ file 11-17` for a class and `@@ file 8 - 8` for a
// method, a blank line before each section but not after the last, and a
// section that is empty still printing its braces.
//
// The pieces nest -- a class prints its methods, a method prints its
// parameters -- so they all take the indent they are being printed at.

// reflectorString renders whatever a reflector holds. The second result is
// false for a reflector this does not print, which leaves the caller to report
// the gap rather than printing something plausible.
func (in *Interp) reflectorString(ctx *php.Ctx, unit *Unit, object *php.Object) (string, bool, error) {
	held, ok := object.Internal.(*reflector)
	if !ok {
		return "", false, nil
	}
	switch strings.ToLower(object.ClassName) {
	case "reflectionclass", "reflectionobject", "reflectionenum":
		if held.class == nil {
			return "", false, nil
		}
		text, err := in.classString(ctx, unit, held.class, "")
		return text, true, err
	case "reflectionmethod", "reflectionfunction":
		if held.function == nil {
			return "", false, nil
		}
		return in.functionString(unit, held.function, held.class, held.name, "",
			held.trampoline, held.closureSubject), true, nil
	case "reflectionproperty":
		if held.property == nil {
			return "", false, nil
		}
		return unit.propertyString(held.property, "", held.dynamic), true, nil
	case "reflectionclassconstant":
		if held.constant == nil {
			return "", false, nil
		}
		text, err := in.constantString(ctx, unit, held.class, held.constant, "")
		return text, true, err
	}
	return "", false, nil
}

// classString is `_class_string`.
func (in *Interp) classString(ctx *php.Ctx, unit *Unit, class *Class, indent string) (string, error) {
	var out strings.Builder
	sub := indent + "    "
	kind := "Class"
	switch class.decl.GetKind() {
	case pb.Class_KIND_INTERFACE:
		kind = "Interface"
	case pb.Class_KIND_TRAIT:
		kind = "Trait"
	}
	fmt.Fprintf(&out, "%s%s [ <user> ", indent, kind)
	switch {
	case class.decl.GetKind() == pb.Class_KIND_INTERFACE:
		out.WriteString("interface ")
	case class.decl.GetKind() == pb.Class_KIND_TRAIT:
		out.WriteString("trait ")
	default:
		if class.decl.GetAbstract() {
			out.WriteString("abstract ")
		}
		if class.decl.GetFinal() {
			out.WriteString("final ")
		}
		out.WriteString("class ")
	}
	out.WriteString(php.DisplayName(class.Name))
	if class.Parent != "" {
		fmt.Fprintf(&out, " extends %s", php.DisplayName(class.Parent))
	}
	if names := unit.linkedInterfaces(class); len(names) > 0 {
		word := " implements "
		if class.decl.GetKind() == pb.Class_KIND_INTERFACE {
			word = " extends "
		}
		out.WriteString(word)
		out.WriteString(strings.Join(names, ", "))
	}
	out.WriteString(" ] {\n")
	fmt.Fprintf(&out, "%s  @@ %s %d-%d\n", indent,
		class.decl.GetFilename(), class.decl.GetLineStart(), class.decl.GetLineEnd())

	constants := unit.flattenedConstants(class)
	fmt.Fprintf(&out, "\n%s  - Constants [%d] {\n", indent, len(constants))
	for _, entry := range constants {
		text, err := in.constantString(ctx, unit, entry.owner, entry.constant, sub)
		if err != nil {
			return "", err
		}
		out.WriteString(text)
	}
	fmt.Fprintf(&out, "%s  }\n", indent)

	properties := unit.flattenedProperties(class)
	var static, instance []*pb.Property
	for _, prop := range properties {
		if prop.GetStatic() {
			static = append(static, prop)
		} else {
			instance = append(instance, prop)
		}
	}
	fmt.Fprintf(&out, "\n%s  - Static properties [%d] {\n", indent, len(static))
	for _, prop := range static {
		out.WriteString(unit.propertyString(prop, sub, false))
	}
	fmt.Fprintf(&out, "%s  }\n", indent)

	methods := unit.flattenedMethods(class)
	var staticFuncs, instanceFuncs []flattenedMethod
	for _, method := range methods {
		if method.op.GetStatic() {
			staticFuncs = append(staticFuncs, method)
		} else {
			instanceFuncs = append(instanceFuncs, method)
		}
	}
	fmt.Fprintf(&out, "\n%s  - Static methods [%d] {", indent, len(staticFuncs))
	for _, method := range staticFuncs {
		out.WriteString("\n")
		out.WriteString(in.functionString(unit, method.op, class, method.name, sub,
			false, false))
	}
	if len(staticFuncs) == 0 {
		out.WriteString("\n")
	}
	fmt.Fprintf(&out, "%s  }\n", indent)

	fmt.Fprintf(&out, "\n%s  - Properties [%d] {\n", indent, len(instance))
	for _, prop := range instance {
		out.WriteString(unit.propertyString(prop, sub, false))
	}
	fmt.Fprintf(&out, "%s  }\n", indent)

	fmt.Fprintf(&out, "\n%s  - Methods [%d] {", indent, len(instanceFuncs))
	for _, method := range instanceFuncs {
		out.WriteString("\n")
		out.WriteString(in.functionString(unit, method.op, class, method.name, sub,
			false, false))
	}
	if len(instanceFuncs) == 0 {
		out.WriteString("\n")
	}
	fmt.Fprintf(&out, "%s  }\n", indent)

	fmt.Fprintf(&out, "%s}\n", indent)
	return out.String(), nil
}

// functionString is `_function_string`.
func (in *Interp) functionString(unit *Unit, op *pb.OpArray, scope *Class, shown, indent string,
	internal, closure bool) string {
	if shown == "" {
		shown = op.GetName()
	}
	origin := "user"
	if internal {
		origin = "internal"
	}
	var out strings.Builder
	out.WriteString(indent)
	// A reflector built from a Closure heads its dump with `Closure [`,
	// whatever the declaration behind it was; the rest of the line still says
	// `function` or `public method` as the declaration does.
	switch {
	case closure:
		out.WriteString("Closure [ <" + origin)
	case op.GetVisibility() == "" && scope == nil:
		out.WriteString("Function [ <" + origin)
	default:
		out.WriteString("Method [ <" + origin)
	}
	if op.GetDeprecated() {
		out.WriteString(", deprecated")
	}
	declaring := unit.declaringOfMethod(scope, op)
	if scope != nil && declaring != nil {
		if !strings.EqualFold(declaring.Name, scope.Name) {
			fmt.Fprintf(&out, ", inherits %s", php.DisplayName(declaring.Name))
		} else if overwritten, found := unit.overwrittenScope(scope, shown); found {
			fmt.Fprintf(&out, ", overwrites %s", php.DisplayName(overwritten))
		}
	}
	if prototype, found := unit.prototypeScope(scope, shown); found {
		fmt.Fprintf(&out, ", prototype %s", php.DisplayName(prototype))
	}
	if strings.EqualFold(shown, "__construct") {
		out.WriteString(", ctor")
	}
	out.WriteString("> ")
	if op.GetAbstract() {
		out.WriteString("abstract ")
	}
	if unit.effectiveFinal(scope, op, shown) {
		out.WriteString("final ")
	}
	if op.GetStatic() {
		out.WriteString("static ")
	}
	if scope != nil {
		visibility := unit.effectiveVisibility(scope, op, shown)
		if visibility == "" {
			visibility = "public"
		}
		out.WriteString(visibility + " method ")
	} else {
		out.WriteString("function ")
	}
	if op.GetReturnsRef() {
		out.WriteString("&")
	}
	fmt.Fprintf(&out, "%s ] {\n", shown)
	// An internal declaration has no file behind it, so the engine prints no
	// `@@` line for one.
	if !internal {
		fmt.Fprintf(&out, "%s  @@ %s %d - %d\n", indent,
			op.GetFilename(), op.GetLineStart(), op.GetLineEnd())
	}

	// The engine prints the parameter block whenever the declaration has an
	// arg_info table at all, which it has as soon as there is a parameter or a
	// return type -- so a `function f(): void {}` prints `Parameters [0]`.
	if len(op.GetArgs()) > 0 || op.GetReturnType() != "" {
		fmt.Fprintf(&out, "\n%s  - Parameters [%d] {\n", indent, len(op.GetArgs()))
		required := int(op.GetRequiredArgs())
		for i, arg := range op.GetArgs() {
			fmt.Fprintf(&out, "%s    %s\n", indent,
				parameterString(op, arg, i, i < required, internal))
		}
		fmt.Fprintf(&out, "%s  }\n", indent)
	}
	if op.GetReturnType() != "" {
		word := "Return"
		if op.GetTentativeReturn() {
			word = "Tentative return"
		}
		fmt.Fprintf(&out, "%s  - %s [ %s ]\n", indent, word, op.GetReturnType())
	}
	fmt.Fprintf(&out, "%s}\n", indent)
	return out.String()
}

// parameterString is `_parameter_string`.
func parameterString(op *pb.OpArray, arg *pb.ArgInfo, offset int, required, internal bool) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Parameter #%d [ ", offset)
	if required {
		out.WriteString("<required> ")
	} else {
		out.WriteString("<optional> ")
	}
	if arg.GetType() != "" {
		out.WriteString(arg.GetType() + " ")
	}
	if arg.GetByRef() {
		out.WriteString("&")
	}
	if arg.GetVariadic() {
		out.WriteString("...")
	}
	out.WriteString("$" + arg.GetName())
	if !required && !arg.GetVariadic() {
		if text, ok := reflectDefaultText(op, offset); ok && !internal {
			out.WriteString(" = " + text)
		} else if internal {
			// An internal declaration keeps no RECV_INIT to read the value
			// back from, and the engine says so rather than guessing.
			out.WriteString(" = <default>")
		}
	}
	out.WriteString(" ]")
	return out.String()
}

// propertyString is `_property_string`.
func (u *Unit) propertyString(prop *pb.Property, indent string, dynamic bool) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%sProperty [ ", indent)
	if dynamic {
		out.WriteString("<dynamic> ")
	}
	visibility := prop.GetVisibility()
	if visibility == "" {
		visibility = "public"
	}
	out.WriteString(visibility + " ")
	switch prop.GetSetVisibility() {
	case "private":
		out.WriteString("private(set) ")
	case "protected":
		out.WriteString("protected(set) ")
	}
	if prop.GetStatic() {
		out.WriteString("static ")
	}
	if prop.GetReadonly() {
		out.WriteString("readonly ")
	}
	if prop.GetType() != "" {
		out.WriteString(prop.GetType() + " ")
	}
	out.WriteString("$" + prop.GetName())
	if prop.GetHasDefault() {
		value := literalValue(prop.GetDefaultValue())
		if resolved, ok := u.defaults[prop]; ok {
			value = resolved
		}
		out.WriteString(" = " + reflectValueText(value))
	}
	out.WriteString(" ]\n")
	return out.String()
}

// constantString is `_class_const_string`. The type shown is the declared one
// where there is one and otherwise the type of the value, so the constant has
// to be evaluated to print it.
func (in *Interp) constantString(ctx *php.Ctx, unit *Unit, owner *Class,
	constant *pb.ClassConstant, indent string) (string, error) {
	value, _, err := in.classConstant(ctx, unit, owner.Name, constant.GetName(), 0)
	if err != nil {
		return "", err
	}
	final := ""
	if constant.GetFinal() {
		final = "final "
	}
	visibility := constant.GetVisibility()
	if visibility == "" {
		visibility = "public"
	}
	typeName := constant.GetType()
	if typeName == "" {
		typeName = php.TypeName(value)
	}
	shown := ""
	switch value.Kind() {
	case php.KindArray:
		shown = "Array"
	case php.KindObject:
		shown = "Object"
	default:
		shown = value.String()
	}
	return fmt.Sprintf("%sConstant [ %s%s %s %s ] { %s }\n",
		indent, final, visibility, typeName, constant.GetName(), shown), nil
}

// reflectValueText is `format_default_value`, which renders a string in single
// quotes and an array as a bracketed list -- neither the way var_export does.
func reflectValueText(value php.Value) string {
	switch value.Kind() {
	case php.KindUndef, php.KindNull:
		return "NULL"
	case php.KindTrue:
		return "true"
	case php.KindFalse:
		return "false"
	case php.KindString:
		return "'" + value.String() + "'"
	case php.KindArray:
		var out strings.Builder
		out.WriteString("[")
		list := value.Array().IsList()
		first := true
		value.Array().Each(func(k php.Key, v php.Value) bool {
			if !first {
				out.WriteString(", ")
			}
			first = false
			if !list {
				if k.IsInt {
					fmt.Fprintf(&out, "%d => ", k.Int)
				} else {
					fmt.Fprintf(&out, "'%s' => ", k.String())
				}
			}
			out.WriteString(reflectValueText(v.Deref()))
			return true
		})
		out.WriteString("]")
		return out.String()
	case php.KindObject:
		if object := value.Object(); object != nil && object.EnumCase != "" {
			return php.DisplayName(object.ClassName) + "::" + object.EnumCase
		}
		return "stdlib.Object(" + php.DisplayName(value.Object().ClassName) + ")"
	}
	return value.String()
}

// reflectDefaultText renders a parameter's default. An internal declaration
// keeps it with the parameter; a user one keeps it in the RECV_INIT opline that
// fills the slot, which is where `get_default_from_recv` looks.
func reflectDefaultText(op *pb.OpArray, index int) (string, bool) {
	args := op.GetArgs()
	if index >= len(args) {
		return "", false
	}
	if text := args[index].GetDefaultValue(); text != "" {
		return text, true
	}
	for _, line := range op.GetOplines() {
		if line.GetOp1() != uint32(index+1) {
			continue
		}
		literal := literalOperand(op, line)
		if literal == nil {
			continue
		}
		switch literal.GetKind() {
		case pb.Literal_KIND_NULL, pb.Literal_KIND_BOOL, pb.Literal_KIND_INT,
			pb.Literal_KIND_FLOAT, pb.Literal_KIND_STRING, pb.Literal_KIND_ARRAY:
			return reflectValueText(literalValue(literal)), true
		}
		// A default the compiler could not fold is exported as the expression
		// it was written as, which is a shape of its own.
		return literalDeclarationText(literal), true
	}
	return "", false
}

// flattenedMethod is one entry of a class's function table, with the class that
// declared it.
type flattenedMethod struct {
	op    *pb.OpArray
	owner *Class
	name  string
}

// flattenedMethods lists a class's function table the way the engine builds it:
// what the class declares itself, then what it inherits, with an ancestor's
// private methods left out -- they are in the table but hidden, and the
// reflectors skip them.
func (u *Unit) flattenedMethods(class *Class) []flattenedMethod {
	var out []flattenedMethod
	seen := map[string]bool{}
	for current := class; current != nil; {
		for _, op := range current.decl.GetMethods() {
			key := strings.ToLower(op.GetName())
			if seen[key] {
				continue
			}
			seen[key] = true
			if current != class && op.GetVisibility() == "private" {
				continue
			}
			out = append(out, flattenedMethod{op: op, owner: current, name: op.GetName()})
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
	return out
}

// flattenedProperties is the same for the property table.
func (u *Unit) flattenedProperties(class *Class) []*pb.Property {
	var out []*pb.Property
	seen := map[string]bool{}
	for current := class; current != nil; {
		for _, prop := range u.properties(current) {
			if seen[prop.GetName()] {
				continue
			}
			seen[prop.GetName()] = true
			if current != class && prop.GetVisibility() == "private" {
				continue
			}
			out = append(out, prop)
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
	return out
}

// ownedConstant is a class constant with the class whose scope evaluates it.
type ownedConstant struct {
	constant *pb.ClassConstant
	owner    *Class
}

// flattenedConstants is the same for the constant table, which also takes in
// what the interfaces declare.
func (u *Unit) flattenedConstants(class *Class) []ownedConstant {
	var out []ownedConstant
	seen := map[string]bool{}
	var add func(c *Class)
	add = func(c *Class) {
		if c == nil {
			return
		}
		for _, constant := range u.constants(c) {
			if seen[constant.GetName()] {
				continue
			}
			seen[constant.GetName()] = true
			out = append(out, ownedConstant{constant: constant, owner: c})
		}
	}
	for current := class; current != nil; {
		add(current)
		for _, name := range current.decl.GetInterfaces() {
			if iface, ok := u.findClass(name); ok {
				add(iface)
			}
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
	return out
}

// linkedInterfaces is the interface list the engine keeps on the class entry,
// which is the transitive closure rather than what the declaration wrote.
func (u *Unit) linkedInterfaces(class *Class) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(c *Class, direct bool)
	walk = func(c *Class, direct bool) {
		if c == nil {
			return
		}
		for _, name := range c.decl.GetInterfaces() {
			iface, ok := u.findClass(name)
			shown := name
			if ok {
				shown = iface.Name
			}
			if !seen[strings.ToLower(shown)] {
				seen[strings.ToLower(shown)] = true
				out = append(out, php.DisplayName(shown))
			}
			if ok {
				walk(iface, false)
			}
		}
	}
	walk(class, true)
	sort.SliceStable(out, func(i, j int) bool { return false })
	return out
}

// declaringOfMethod finds the class in the chain that wrote a method.
func (u *Unit) declaringOfMethod(class *Class, op *pb.OpArray) *Class {
	for current := class; current != nil; {
		for _, own := range current.decl.GetMethods() {
			if own == op {
				return current
			}
		}
		if current.Parent == "" {
			return nil
		}
		next, ok := u.findClass(current.Parent)
		if !ok {
			return nil
		}
		current = next
	}
	return nil
}

// overwrittenScope names the ancestor whose method a class's own declaration
// replaces. A private one is not replaced: it was never reachable.
func (u *Unit) overwrittenScope(class *Class, name string) (string, bool) {
	if class == nil || class.Parent == "" {
		return "", false
	}
	parent, ok := u.findClass(class.Parent)
	if !ok {
		return "", false
	}
	for current := parent; current != nil; {
		if op, found := current.methods[strings.ToLower(name)]; found {
			if op.GetVisibility() == "private" {
				return "", false
			}
			return current.Name, true
		}
		if current.Parent == "" {
			return "", false
		}
		next, ok := u.findClass(current.Parent)
		if !ok {
			return "", false
		}
		current = next
	}
	return "", false
}

// prototypeScope names the declaration a method has to remain compatible with:
// an interface's, or an abstract one further up. The engine records it while
// linking; here it is the outermost such declaration reachable from the class.
func (u *Unit) prototypeScope(class *Class, name string) (string, bool) {
	if class == nil {
		return "", false
	}
	lower := strings.ToLower(name)
	found := ""
	var consider func(c *Class)
	consider = func(c *Class) {
		if c == nil {
			return
		}
		if op, has := c.methods[lower]; has {
			if c.decl.GetKind() == pb.Class_KIND_INTERFACE || op.GetAbstract() {
				found = c.Name
			}
		}
		for _, iface := range c.decl.GetInterfaces() {
			if next, ok := u.findClass(iface); ok {
				consider(next)
			}
		}
	}
	for current := class; current != nil; {
		consider(current)
		if current.Parent == "" {
			break
		}
		next, ok := u.findClass(current.Parent)
		if !ok {
			break
		}
		current = next
	}
	return found, found != ""
}
