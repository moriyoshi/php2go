package gogen

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// quotedRE finds the Go-quoted strings in an IR operand or argument list.
var quotedRE = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// EngineReflect is the linked engine's own reflection of its classes and
// functions, asked of ReflectionClass/ReflectionFunction there rather than
// derived: what each reflector over an engine symbol answers.
type EngineReflect struct {
	// Classes are keyed by the lowercased class name.
	Classes map[string]*EngineReflectClass
	// Functions are the engine's global functions, keyed lowercased.
	Functions map[string]*EngineReflectMethod
}

// EngineReflectClass is one engine class, interface, trait or enum.
type EngineReflectClass struct {
	Name       string `json:"n"`
	Kind       string `json:"k"`
	Parent     string `json:"p"`
	Final      bool   `json:"f"`
	Abstract   bool   `json:"a"`
	Attributed bool   `json:"at"`
	// Methods are getMethods(), inherited ones included, in its order.
	Methods []EngineReflectMethod `json:"m"`
	// Props are getProperties(): own, and inherited non-private ones.
	Props []EngineReflectProp `json:"pr"`
}

// EngineReflectMethod is one method or function.
type EngineReflectMethod struct {
	Name       string               `json:"n"`
	Class      string               `json:"c"`
	Visibility string               `json:"v"`
	Static     bool                 `json:"s"`
	Abstract   bool                 `json:"a"`
	Final      bool                 `json:"f"`
	Attributed bool                 `json:"at"`
	Required   int                  `json:"r"`
	Params     []EngineReflectParam `json:"ps"`
}

// EngineReflectParam is one parameter of an engine method or function.
type EngineReflectParam struct {
	Name             string `json:"n"`
	Type             string `json:"t"`
	HasType          bool   `json:"ht"`
	AllowsNull       bool   `json:"an"`
	Optional         bool   `json:"o"`
	Variadic         bool   `json:"va"`
	ByRef            bool   `json:"br"`
	DefaultAvailable bool   `json:"d"`
	Attributed       bool   `json:"at"`
	// Class is getClass()'s answer, the class name or "" for null.
	Class string `json:"cl"`
}

// EngineReflectProp is one declared property of an engine class.
type EngineReflectProp struct {
	Name       string `json:"n"`
	Class      string `json:"c"`
	Visibility string `json:"v"`
	Type       string `json:"t"`
	HasType    bool   `json:"ht"`
	AllowsNull bool   `json:"an"`
	Static     bool   `json:"s"`
	Readonly   bool   `json:"ro"`
	Attributed bool   `json:"at"`
}

// engineReflectSelection picks the engine classes and functions a program
// can reflect: those a string operand names, the engine ancestors of its
// classes, and -- transitively -- the parents, declaring classes and
// parameter classes of what is picked. Emitting the whole engine would add
// thousands of entries to every program; one the selection misses is a gap,
// not a wrong answer.
func engineReflectSelection(p *Program) (classes []string, funcs []string) {
	er := p.EngineReflect
	if er == nil {
		return nil, nil
	}
	tokens := map[string]bool{}
	for _, fn := range EveryBody(p) {
		for _, b := range fn.Blocks {
			for _, in := range b.Insts {
				texts := append(append([]string{}, in.Operands...), in.Attrs...)
				texts = append(texts, in.ArgList)
				for _, o := range texts {
					for _, q := range quotedRE.FindAllString(o, -1) {
						if u, err := strconv.Unquote(q); err == nil {
							tokens[strings.ToLower(strings.TrimPrefix(u, "\\"))] = true
						}
					}
					tokens[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(o), "\\"))] = true
				}
			}
		}
	}
	for _, c := range p.Classes {
		tokens[strings.ToLower(c.Parent)] = true
		for _, i := range c.Interfaces {
			tokens[strings.ToLower(i)] = true
		}
	}
	picked := map[string]bool{}
	var pick func(name string)
	pick = func(name string) {
		low := strings.ToLower(strings.TrimPrefix(name, "\\"))
		c := er.Classes[low]
		if c == nil || picked[low] {
			return
		}
		picked[low] = true
		if c.Parent != "" {
			pick(c.Parent)
		}
		for _, m := range c.Methods {
			pick(m.Class)
			for _, prm := range m.Params {
				if prm.Class != "" {
					pick(prm.Class)
				}
			}
		}
		for _, pr := range c.Props {
			pick(pr.Class)
		}
	}
	for t := range tokens {
		pick(t)
	}
	pickedFn := map[string]bool{}
	for t := range tokens {
		if f := er.Functions[t]; f != nil {
			pickedFn[t] = true
			for _, prm := range f.Params {
				if prm.Class != "" {
					pick(prm.Class)
				}
			}
		}
	}
	for k := range picked {
		classes = append(classes, k)
	}
	for k := range pickedFn {
		funcs = append(funcs, k)
	}
	sort.Strings(classes)
	sort.Strings(funcs)
	return classes, funcs
}

func engineMethodLiteral(m EngineReflectMethod) string {
	var ps []string
	for _, prm := range m.Params {
		ps = append(ps, fmt.Sprintf("{name: %q, typ: %q, class: %q, hasType: %t, allowsNull: %t, optional: %t, variadic: %t, byRef: %t, defAvail: %t, attrs: %t}",
			prm.Name, prm.Type, prm.Class, prm.HasType, prm.AllowsNull, prm.Optional, prm.Variadic, prm.ByRef, prm.DefaultAvailable, prm.Attributed))
	}
	return fmt.Sprintf("&engM{name: %q, class: %q, vis: %q, static: %t, abstract: %t, final: %t, attrs: %t, required: %d, params: []engP{%s}}",
		m.Name, m.Class, m.Visibility, m.Static, m.Abstract, m.Final, m.Attributed, m.Required, strings.Join(ps, ", "))
}

// emitEngineReflect emits the selected engine reflection tables and the
// reflectors that answer from them.
func (e *emitter) emitEngineReflect(p *Program) {
	classes, funcs := engineReflectSelection(p)
	e.p("// engP, engM, engPr and engR are the engine's own reflection of a")
	e.p("// parameter, a method or function, a property and a class (harvested).")
	e.p("type engP struct {")
	e.p("\tname, typ, class                                                  string")
	e.p("\thasType, allowsNull, optional, variadic, byRef, defAvail, attrs bool")
	e.p("}")
	e.p("")
	e.p("type engM struct {")
	e.p("\tname, class, vis                      string")
	e.p("\tstatic, abstract, final, attrs        bool")
	e.p("\trequired                              int")
	e.p("\tparams                                []engP")
	e.p("}")
	e.p("")
	e.p("type engPr struct {")
	e.p("\tname, class, vis, typ                          string")
	e.p("\thasType, allowsNull, static, readonly, attrs bool")
	e.p("}")
	e.p("")
	e.p("type engR struct {")
	e.p("\tname, kind, parent     string")
	e.p("\tfinal, abstract, attrs bool")
	e.p("\tmethods                map[string]*engM")
	e.p("\tprops                  map[string]*engPr")
	e.p("}")
	e.p("")
	e.p("// engineReflectClasses are the engine classes this program can reflect,")
	e.p("// keyed lowercased; one absent is a gap.")
	e.p("var engineReflectClasses = map[string]*engR{")
	for _, k := range classes {
		c := p.EngineReflect.Classes[k]
		e.p("\t%q: {name: %q, kind: %q, parent: %q, final: %t, abstract: %t, attrs: %t,", k, c.Name, c.Kind, c.Parent, c.Final, c.Abstract, c.Attributed)
		e.p("\t\tmethods: map[string]*engM{")
		for _, m := range c.Methods {
			e.p("\t\t\t%q: %s,", strings.ToLower(m.Name), engineMethodLiteral(m))
		}
		e.p("\t\t},")
		e.p("\t\tprops: map[string]*engPr{")
		for _, pr := range c.Props {
			e.p("\t\t\t%q: {name: %q, class: %q, vis: %q, typ: %q, hasType: %t, allowsNull: %t, static: %t, readonly: %t, attrs: %t},",
				pr.Name, pr.Name, pr.Class, pr.Visibility, pr.Type, pr.HasType, pr.AllowsNull, pr.Static, pr.Readonly, pr.Attributed)
		}
		e.p("\t\t},")
		e.p("\t},")
	}
	e.p("}")
	e.p("")
	e.p("// engineReflectFuncs are the engine functions this program can reflect.")
	e.p("var engineReflectFuncs = map[string]*engM{")
	for _, k := range funcs {
		e.p("\t%q: %s,", k, engineMethodLiteral(*p.EngineReflect.Functions[k]))
	}
	e.p("}")
	e.p("")
	e.emitEngineReflectRuntime()
}

func (e *emitter) emitEngineReflectRuntime() {
	e.b.WriteString(`// engineChainClass is the engine class a reflection of class reaches: the
// class itself when the program does not compile it, else the first engine
// ancestor of its compiled chain. nil when there is none this program can
// reflect.
func engineChainClass(class string) *engR {
	for c := realClassOr(resolveAlias(class)); c != ""; c = parentOf[realClassOr(c)] {
		if _, own := classDecl[strings.ToLower(c)]; own {
			continue
		}
		return engineReflectClasses[strings.ToLower(c)]
	}
	return nil
}

// fillEngineClassReflector makes o the ReflectionClass/ReflectionObject of
// engine class c.
func fillEngineClassReflector(o *php.Object, kind string, c *engR) {
	o.Internal = &reflector{kind: kind, subject: c.name, engClass: c}
	o.Declare("name", php.Declaration{Class: kind, Readonly: true})
	o.Set("name", php.String(c.name))
}

// newEngineMethodReflector is the ReflectionMethod of engine method m.
func newEngineMethodReflector(m *engM) *php.Object {
	return newEngineMethodReflectorInto(php.NewObject("ReflectionMethod", allocObjectID()), m)
}

// fillEnginePropReflector makes o the ReflectionProperty of engine
// property pr.
func fillEnginePropReflector(o *php.Object, pr *engPr) {
	o.Internal = &reflector{kind: "ReflectionProperty", subject: pr.class, member: pr.name, engProp: pr}
	o.Declare("name", php.Declaration{Class: "ReflectionProperty", Readonly: true})
	o.Set("name", php.String(pr.name))
	o.Declare("class", php.Declaration{Class: "ReflectionProperty", Readonly: true})
	o.Set("class", php.String(pr.class))
}

// engineMethodVisibility is methodVisibility's entry for a non-public
// method an engine class declares and the program does not compile over:
// the declaring class, the visibility, and the class at the root of its
// prototype chain, which a protected method's reach is measured from
// (SplHeap::compare for every heap's).
func engineMethodVisibility(class, lower string) ([3]string, bool) {
	if hasCompiledMethod(class, lower) {
		return [3]string{}, false
	}
	c := engineChainClass(class)
	if c == nil {
		return [3]string{}, false
	}
	m, ok := c.methods[lower]
	if !ok || m.vis == "public" {
		return [3]string{}, false
	}
	root := m.class
	for p := engineReflectClasses[strings.ToLower(root)]; p != nil && p.parent != ""; {
		p = engineReflectClasses[strings.ToLower(p.parent)]
		if p == nil {
			break
		}
		if pm, ok := p.methods[lower]; ok && pm.vis != "private" {
			root = pm.class
		}
	}
	return [3]string{m.class, m.vis, root}, true
}

// engineMethodByName is the method a ReflectionMethod or getMethod() over
// the class name finds. Closure's __invoke is listed by getMethods() and
// answered by hasMethod(), but is in no function table, so asked for by name
// without a closure it does not exist (probed).
func engineMethodByName(c *engR, name string) (*engM, bool) {
	low := strings.ToLower(name)
	if c.name == "Closure" && low == "__invoke" {
		return nil, false
	}
	m, ok := c.methods[low]
	return m, ok
}

// reflMethodClosure is ReflectionMethod::getClosure: a closure over the
// declared method itself (zend_create_fake_closure), scoped to its declaring
// class, bound to the object given for a non-static one. Calling it reaches
// that class's method, not whatever the bound object's class overrides it
// with.
func reflMethodClosure(ctx *php.Ctx, class, method string, static bool, params []closureParam, args []php.Value) (php.Value, error) {
	obj := php.Arg(args, 0).Deref()
	switch obj.Kind() {
	case php.KindNull, php.KindUndef, php.KindObject:
	default:
		return php.Value{}, php.RaiseAt(ctx, "TypeError", "ReflectionMethod::getClosure(): Argument #1 ($object) must be of type ?object, %s given", php.ValueName(obj))
	}
	var this *php.Object
	called := class
	if !static {
		if obj.Kind() != php.KindObject {
			return php.Value{}, php.RaiseAt(ctx, "ValueError", "%s", "ReflectionMethod::getClosure(): Argument #1 ($object) cannot be null for non-static methods")
		}
		if !instanceOf(obj, class) {
			return php.Value{}, php.RaiseAt(ctx, "ReflectionException", "%s", "Given object is not an instance of the class this method was declared in")
		}
		this = obj.Object()
		called = this.ClassName
	}
	fn := func(ctx *php.Ctx, this *php.Object, bound map[string]*php.Value, scope string, args []php.Value) (php.Value, error) {
		return callStaticFrom(ctx, class, method, this, args, class)
	}
	named := class + "::" + method
	o := php.NewObject("Closure", allocObjectID())
	o.Internal = &closureBody{fn: fn, bound: map[string]*php.Value{}, this: this, scope: class, called: called,
		meta: closureMeta{name: named, function: named, params: params}}
	return php.Obj(o), nil
}

// engineShortName splits a namespaced engine class name.
func engineShortName(name string) (string, string) {
	if i := strings.LastIndex(name, "\\"); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

// engineReflectorMethod answers a reflector over an engine symbol from the
// harvested table, and refuses whatever the table does not settle.
func engineReflectorMethod(ctx *php.Ctx, r *reflector, name string, args []php.Value) (php.Value, error) {
	switch name {
	case "isinternal":
		return php.Bool(true), nil
	case "isuserdefined":
		return php.Bool(false), nil
	case "getfilename", "getstartline", "getendline", "getdoccomment":
		if r.engParam == nil {
			return php.Bool(false), nil
		}
	}
	switch {
	case r.engClass != nil:
		c := r.engClass
		switch name {
		case "getname":
			return php.String(c.name), nil
		case "getshortname":
			_, short := engineShortName(c.name)
			return php.String(short), nil
		case "getnamespacename":
			ns, _ := engineShortName(c.name)
			return php.String(ns), nil
		case "innamespace":
			ns, _ := engineShortName(c.name)
			return php.Bool(ns != ""), nil
		case "isinterface":
			return php.Bool(c.kind == "interface"), nil
		case "istrait":
			return php.Bool(c.kind == "trait"), nil
		case "isenum":
			return php.Bool(c.kind == "enum"), nil
		case "isfinal":
			return php.Bool(c.final), nil
		case "isabstract":
			return php.Bool(c.abstract), nil
		case "getattributes":
			if !c.attrs {
				return php.Arr(php.NewArray()), nil
			}
		case "hasmethod":
			_, ok := c.methods[strings.ToLower(php.Arg(args, 0).Deref().String())]
			return php.Bool(ok), nil
		case "getmethod":
			wanted := php.Arg(args, 0).Deref().String()
			if m, ok := engineMethodByName(c, wanted); ok {
				return php.Obj(newEngineMethodReflector(m)), nil
			}
			return php.Value{}, php.RaiseAt(ctx, "ReflectionException", "%s", "Method "+c.name+"::"+wanted+"() does not exist")
		case "hasproperty":
			if r.kind == "ReflectionClass" {
				_, ok := c.props[php.Arg(args, 0).Deref().String()]
				return php.Bool(ok), nil
			}
		case "getproperty":
			wanted := php.Arg(args, 0).Deref().String()
			if pr, ok := c.props[wanted]; ok {
				o := php.NewObject("ReflectionProperty", allocObjectID())
				fillEnginePropReflector(o, pr)
				return php.Obj(o), nil
			}
			if r.kind == "ReflectionClass" {
				return php.Value{}, php.RaiseAt(ctx, "ReflectionException", "%s", "Property "+c.name+"::$"+wanted+" does not exist")
			}
		case "getparentclass":
			if c.parent == "" {
				return php.Bool(false), nil
			}
			return newReflector(ctx, "ReflectionClass", []php.Value{php.String(c.parent)})
		}
		return php.Value{}, php.Unsupported("%s::%s of engine class %s", r.kind, name, c.name)
	case r.engMethod != nil:
		m := r.engMethod
		switch name {
		case "getname", "getshortname":
			return php.String(m.name), nil
		case "getattributes":
			if !m.attrs {
				return php.Arr(php.NewArray()), nil
			}
		case "isstatic":
			return php.Bool(m.static), nil
		case "isabstract":
			return php.Bool(m.abstract), nil
		case "isfinal":
			return php.Bool(m.final), nil
		case "ispublic":
			return php.Bool(m.vis == "public"), nil
		case "isprivate":
			return php.Bool(m.vis == "private"), nil
		case "isprotected":
			return php.Bool(m.vis == "protected"), nil
		case "getnumberofparameters":
			return php.Int(int64(len(m.params))), nil
		case "getnumberofrequiredparameters":
			return php.Int(int64(m.required)), nil
		case "getparameters":
			return php.Arr(engineParamReflectors(m)), nil
		case "getdeclaringclass":
			return newReflector(ctx, "ReflectionClass", []php.Value{php.String(m.class)})
		case "isconstructor":
			return php.Bool(strings.EqualFold(m.name, "__construct")), nil
		case "isdestructor":
			return php.Bool(strings.EqualFold(m.name, "__destruct")), nil
		case "setaccessible":
			return php.Null(), nil
		case "getclosure":
			if m.class == "Closure" {
				break
			}
			params := make([]closureParam, 0, len(m.params))
			for _, prm := range m.params {
				shown := "$" + prm.name
				if prm.byRef {
					shown = "&" + shown
				}
				params = append(params, closureParam{name: shown, required: !prm.optional})
			}
			return reflMethodClosure(ctx, m.class, m.name, m.static, params, args)
		case "invoke", "invokeargs":
			// Called through the program's own dispatch, as the program's
			// reflectors call a method: the receiver first, null for a
			// static one. A Closure's __invoke is not one of these.
			if m.class == "Closure" {
				break
			}
			recv := php.Arg(args, 0).Deref()
			var list []php.Value
			if name == "invokeargs" {
				var err error
				if list, err = reflectSpreadArgs(ctx, php.Arg(args, 1)); err != nil {
					return php.Value{}, err
				}
			} else if len(args) > 1 {
				list = args[1:]
			}
			if m.static {
				return callStatic(ctx, m.class, m.name, nil, list)
			}
			if recv.Kind() != php.KindObject {
				break
			}
			return callMethodFrom(ctx, recv, m.name, list, m.class)
		}
		return php.Value{}, php.Unsupported("%s::%s of engine method %s::%s", r.kind, name, m.class, m.name)
	case r.engParam != nil:
		prm := r.engParam
		switch name {
		case "getname":
			return php.String(prm.name), nil
		case "getposition":
			return php.Int(int64(r.index)), nil
		case "isoptional":
			return php.Bool(prm.optional), nil
		case "isvariadic":
			return php.Bool(prm.variadic), nil
		case "ispassedbyreference":
			return php.Bool(prm.byRef), nil
		case "isdefaultvalueavailable":
			return php.Bool(prm.defAvail), nil
		case "allowsnull":
			return php.Bool(prm.allowsNull), nil
		case "hastype":
			return php.Bool(prm.hasType), nil
		case "gettype":
			if !prm.hasType {
				return php.Null(), nil
			}
			if namedTypeOnly(prm.typ) {
				return php.Obj(newReflectionType(prm.typ)), nil
			}
		case "getattributes":
			if !prm.attrs {
				return php.Arr(php.NewArray()), nil
			}
		case "getclass":
			ctx.Deprecated("Method ReflectionParameter::getClass() is deprecated since 8.0, use ReflectionParameter::getType() instead")
			if err := ctx.TakePendingError(); err != nil {
				return php.Value{}, err
			}
			if prm.class == "" {
				return php.Null(), nil
			}
			return newReflector(ctx, "ReflectionClass", []php.Value{php.String(prm.class)})
		}
		return php.Value{}, php.Unsupported("ReflectionParameter::%s of an engine parameter", name)
	case r.engProp != nil:
		pr := r.engProp
		switch name {
		case "getname":
			return php.String(pr.name), nil
		case "getattributes":
			if !pr.attrs {
				return php.Arr(php.NewArray()), nil
			}
		case "ispublic":
			return php.Bool(pr.vis == "public"), nil
		case "isprivate":
			return php.Bool(pr.vis == "private"), nil
		case "isprotected":
			return php.Bool(pr.vis == "protected"), nil
		case "isstatic":
			return php.Bool(pr.static), nil
		case "isreadonly":
			return php.Bool(pr.readonly), nil
		case "hastype":
			return php.Bool(pr.hasType), nil
		case "gettype":
			if !pr.hasType {
				return php.Null(), nil
			}
			if namedTypeOnly(pr.typ) {
				return php.Obj(newReflectionType(pr.typ)), nil
			}
		case "getdeclaringclass":
			return newReflector(ctx, "ReflectionClass", []php.Value{php.String(pr.class)})
		case "getvalue", "setvalue":
			return engineTraceValue(ctx, pr, name, args)
		}
		return php.Value{}, php.Unsupported("ReflectionProperty::%s of engine property %s::$%s", name, pr.class, pr.name)
	}
	return php.Value{}, php.Unsupported("%s::%s of an engine symbol", r.kind, name)
}

// initEngineMember builds 'new ReflectionMethod/ReflectionProperty' over
// an engine class's member. handled is false when the program's own tables
// answer (a compiled member) or no engine class this output reflects is
// reached. A class the program does not compile is described by the engine's
// complete table, so a member missing from it is the engine's
// ReflectionException; reached through a compiled subclass, an engine
// ancestor's private property is not visible, as in the engine (probed).
func initEngineMember(ctx *php.Ctx, o *php.Object, kind, class, member string, given php.Value) (bool, error) {
	low := strings.ToLower(class)
	_, compiled := classDecl[low]
	// An engine class this output models (declaredClasses: ArrayIterator,
	// DateTime, ...) keeps its own reflectors; the harvested table only fills
	// what they cannot see.
	if !compiled && !declaredClasses[low] {
		c := engineReflectClasses[low]
		if c == nil {
			return false, nil
		}
		if kind == "ReflectionMethod" {
			if m, ok := engineMethodByName(c, member); ok {
				newEngineMethodReflectorInto(o, m)
				return true, nil
			}
			return true, php.RaiseAt(ctx, "ReflectionException", "%s", "Method "+c.name+"::"+member+"() does not exist")
		}
		if pr, ok := c.props[member]; ok {
			fillEnginePropReflector(o, pr)
			return true, nil
		}
		if held := given.Object(); held != nil {
			if _, has := held.PropGet(php.StringKey(member)); has {
				return true, php.Unsupported("ReflectionProperty of a dynamic property of engine class %s", c.name)
			}
		}
		return true, php.RaiseAt(ctx, "ReflectionException", "%s", "Property "+c.name+"::$"+member+" does not exist")
	}
	c := engineChainClass(class)
	if c == nil {
		return false, nil
	}
	if kind == "ReflectionMethod" {
		lowMember := strings.ToLower(member)
		if _, _, _, ok := methodOwnerOf(realClass(class), lowMember); ok {
			return false, nil
		}
		if _, _, ok := abstractMethod(realClass(class), lowMember); ok {
			return false, nil
		}
		if m, ok := engineMethodByName(c, lowMember); ok {
			newEngineMethodReflectorInto(o, m)
			return true, nil
		}
		return false, nil
	}
	if !compiled {
		// A modelled engine class's own properties keep the reflectors
		// written for them (SensitiveParameterValue::$value).
		return false, nil
	}
	if desc, ok := reflectProperty(ctx, class, member); ok {
		// Declared by a compiled class: the program's own tables answer.
		// One only the engine ancestor declares (Exception::$message seen
		// through a subclass) is the engine's.
		if _, compiledOwner := classDecl[strings.ToLower(desc.Class)]; compiledOwner {
			return false, nil
		}
		if pr, engine := c.props[member]; engine && pr.vis != "private" {
			fillEnginePropReflector(o, pr)
			return true, nil
		}
		return false, nil
	}
	if pr, ok := c.props[member]; ok && pr.vis != "private" {
		fillEnginePropReflector(o, pr)
		return true, nil
	}
	// Not compiled and not visible from the engine ancestor's complete
	// table: absent, unless it is the object's own dynamic property, which
	// the program's tables answer.
	if held := given.Object(); held != nil {
		if _, has := held.PropGet(php.StringKey(member)); has {
			return false, nil
		}
	}
	return true, php.RaiseAt(ctx, "ReflectionException", "%s", "Property "+php.DisplayName(realClassOr(class))+"::$"+member+" does not exist")
}

// newEngineMethodReflectorInto makes o the ReflectionMethod of m.
func newEngineMethodReflectorInto(o *php.Object, m *engM) *php.Object {
	o.Internal = &reflector{kind: "ReflectionMethod", subject: m.class, member: m.name, engMethod: m}
	o.Declare("name", php.Declaration{Class: "ReflectionMethod", Readonly: true})
	o.Set("name", php.String(m.name))
	o.Declare("class", php.Declaration{Class: "ReflectionMethod", Readonly: true})
	o.Set("class", php.String(m.class))
	return o
}

// engineParamReflectors is getParameters() of an engine method.
func engineParamReflectors(m *engM) *php.Array {
	out := php.NewArray()
	for i := range m.params {
		o := php.NewObject("ReflectionParameter", allocObjectID())
		o.Internal = &reflector{kind: "ReflectionParameter", subject: m.class + "::" + m.name, member: m.params[i].name, index: i, engParam: &m.params[i]}
		o.Declare("name", php.Declaration{Class: "ReflectionParameter", Readonly: true})
		o.Set("name", php.String(m.params[i].name))
		out.Append(php.Obj(o))
	}
	return out
}

// engineTraceValue is getValue/setValue of a throwable's private trace,
// the one engine property whose storage this output models for reflection.
// setValue takes an array keyed by integers (anything else is a gap: the
// engine coerces or prints its own garbage) and marks the trace replaced,
// so getTraceAsString renders it (bug63762).
func engineTraceValue(ctx *php.Ctx, pr *engPr, name string, args []php.Value) (php.Value, error) {
	obj := php.Arg(args, 0).Deref().Object()
	if pr.name != "trace" || obj == nil || !instanceOf(php.Obj(obj), "Throwable") || !instanceOf(php.Obj(obj), pr.class) {
		return php.Value{}, php.Unsupported("ReflectionProperty::%s of engine property %s::$%s", name, pr.class, pr.name)
	}
	key := php.StringKey(php.MangleProperty(php.ObjectThrowableRoot(obj), "trace"))
	if name == "getvalue" {
		if v, ok := obj.PropRaw(key); ok {
			return v.Deref().Copy(), nil
		}
		return php.Value{}, php.Unsupported("ReflectionProperty::getValue of an unset trace")
	}
	a := php.Arg(args, 1).Deref().Array()
	if a == nil || !php.TraceArrayRenderable(a) {
		return php.Value{}, php.Unsupported("ReflectionProperty::setValue of a trace that is not an integer-keyed array")
	}
	obj.PropSet(key, php.Arg(args, 1).Deref().Copy())
	obj.TraceReplaced = true
	return php.Null(), nil
}

`)
}
