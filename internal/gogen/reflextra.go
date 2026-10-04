package gogen

import (
	"sort"
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/internal/lower"
)

// emitReflectDocs emits what the reflectors answer about a declaration
// beyond its signature: doc comments, a method's final flag, a class's
// trait aliases and its default property table -- and reflectExtra, the
// reflector methods that read them.
//
// Every table holds only what is known exactly. A declaration missing from
// one is a gap the reflector reports as unsupported, never false: an absent
// doc comment answers false only because the table says the declaration has
// none.
func (e *emitter) emitReflectDocs(p *Program, byName map[string]*Class) {
	e.p("// reflDoc is one declaration's doc comment and, for a method, whether")
	e.p("// the class's function table holds it final (declared so, or made so by")
	e.p("// a trait alias's `as final`).")
	e.p("type reflDoc struct {")
	e.p("\tdoc   string")
	e.p("\tfinal bool")
	e.p("}")
	e.p("")
	e.p("// reflectDocs is keyed `f:func`, `m:class::method`, `c:class` and")
	e.p("// `p:class::$prop`, class and function names lowercased.")
	e.p("var reflectDocs = map[string]reflDoc{")
	type docEntry struct {
		doc   string
		final bool
	}
	docs := map[string]docEntry{}
	put := func(key, doc string, final bool) {
		if _, seen := docs[key]; !seen {
			docs[key] = docEntry{doc, final}
		}
	}
	for _, fn := range p.Functions {
		put("f:"+strings.ToLower(fn.Name), fn.DocComment, false)
	}
	for _, c := range p.Classes {
		low := strings.ToLower(c.Name)
		put("c:"+low, c.DocComment, false)
		for _, table := range []map[string]*lower.Function{c.Methods, c.StaticMethods} {
			for n, fn := range table {
				if fn == nil {
					continue
				}
				put("m:"+low+"::"+strings.ToLower(n), fn.DocComment, fn.Final || c.ComposedFinal[strings.ToLower(n)])
			}
		}
		for _, list := range [][]Prop{c.Props, c.Statics} {
			for _, pr := range list {
				owner := pr.Class
				if owner == "" {
					owner = c.Name
				}
				put("p:"+strings.ToLower(owner)+"::"+pr.Name, pr.DocComment, false)
			}
		}
	}
	keys := make([]string, 0, len(docs))
	for k := range docs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		d := docs[k]
		e.p("\t%q: {doc: %q, final: %t},", k, d.doc, d.final)
	}
	e.p("}")
	e.p("")

	e.emitReflectTraitAliases(p, byName)
	e.emitReflectDefaultProps(p, byName)
	e.emitReflectExtra()
}

// emitReflectTraitAliases emits ReflectionClass::getTraitAliases: each
// clause that names an alias, as `alias => Trait::method` (probed): the
// alias and the method as written, the trait as written when the clause
// names one and as declared when the engine had to find it.
func (e *emitter) emitReflectTraitAliases(p *Program, byName map[string]*Class) {
	e.p("// reflectTraitAliases is each class's named aliases in clause order;")
	e.p("// a nil entry is a class whose aliases are not known exactly.")
	e.p("var reflectTraitAliases = map[string][][2]string{")
	for _, c := range p.Classes {
		var pairs [][2]string
		known := true
		for _, a := range c.TraitAliases {
			if a.Alias == "" {
				continue
			}
			trait := a.Trait
			if trait == "" {
				m := strings.ToLower(a.Method)
				for _, tn := range c.Traits {
					t := byName[strings.ToLower(tn)]
					if t == nil {
						continue
					}
					if _, ok := t.Methods[m]; ok {
						trait = t.Name
						break
					}
					if _, ok := t.StaticMethods[m]; ok {
						trait = t.Name
						break
					}
				}
			}
			if trait == "" {
				known = false
				break
			}
			pairs = append(pairs, [2]string{a.Alias, trait + "::" + a.Method})
		}
		low := strings.ToLower(c.Name)
		switch {
		case !known:
			e.p("\t%q: nil,", low)
		case len(pairs) > 0:
			var b strings.Builder
			for _, pr := range pairs {
				b.WriteString("{" + strconv.Quote(pr[0]) + ", " + strconv.Quote(pr[1]) + "}, ")
			}
			e.p("\t%q: {%s},", low, strings.TrimSuffix(b.String(), ", "))
		}
	}
	e.p("}")
	e.p("")
}

// emitReflectDefaultProps emits ReflectionClass::getDefaultProperties as the
// engine's add_class_vars builds it (probed): the static properties, then
// the others, each walked in properties_info order -- the class's own in
// declaration order, then each ancestor's -- skipping an ancestor's private
// ones and a typed property with no default. Only classes whose every
// default is a literal are described; a deferred initializer is evaluated by
// the engine there, which is not modelled, and neither are hooks or trait
// members, whose table positions are not harvested.
func (e *emitter) emitReflectDefaultProps(p *Program, byName map[string]*Class) {
	e.p("// reflectDefaultProps builds a class's default property table.")
	e.p("var reflectDefaultProps = map[string]func() *php.Array{")
	for _, c := range p.Classes {
		if c.IsInterface || c.IsTrait {
			continue
		}
		var chain []*Class
		ok := true
		for cur := c; cur != nil; {
			if len(cur.Traits) > 0 {
				ok = false
				break
			}
			chain = append(chain, cur)
			if cur.Parent == "" {
				break
			}
			next := byName[strings.ToLower(cur.Parent)]
			if next == nil {
				ok = false
				break
			}
			cur = next
		}
		if !ok {
			continue
		}
		var lines []string
		// Each level's own entries come from that level's class: a
		// subclass's flattened copy of an inherited static carries no
		// default, its declaration does.
		section := func(pick func(*Class) []Prop) bool {
			seen := map[string]bool{}
			for i, level := range chain {
				var own []Prop
				for _, pr := range pick(level) {
					owner := pr.Class
					if owner == "" {
						owner = level.Name
					}
					if strings.EqualFold(owner, level.Name) {
						own = append(own, pr)
					}
				}
				sort.SliceStable(own, func(a, b int) bool { return own[a].Order < own[b].Order })
				for _, pr := range own {
					if seen[pr.Name] {
						continue
					}
					seen[pr.Name] = true
					if pr.GetHook != nil || pr.SetHook != nil || pr.Virtual || pr.AbstractGet || pr.AbstractSet {
						return false
					}
					if i > 0 && pr.Visibility == "private" {
						continue
					}
					if pr.DefaultLiteral != nil {
						return false
					}
					value := "php.Null()"
					if pr.Default != "" {
						value = e.widenDefault(e.operand(pr.Default), pr.Type)
					} else if pr.Type != "" {
						continue
					}
					lines = append(lines, "\t\ta.Set(php.StringKey("+strconv.Quote(pr.Name)+"), "+value+")")
				}
			}
			return true
		}
		if !section(func(k *Class) []Prop { return k.Statics }) || !section(func(k *Class) []Prop { return k.Props }) {
			continue
		}
		e.p("\t%q: func() *php.Array {", strings.ToLower(c.Name))
		e.p("\t\ta := php.NewArray()")
		for _, l := range lines {
			e.p("%s", l)
		}
		e.p("\t\treturn a")
		e.p("\t},")
	}
	e.p("}")
	e.p("")
}

// emitReflectExtra emits reflectExtra, the reflector methods answered from
// the tables above and from the lazy-object state.
func (e *emitter) emitReflectExtra() {
	e.p("// reflectExtra answers the reflector methods read from reflectDocs and")
	e.p("// its neighbours. handled is false for a method it does not answer.")
	e.p("func reflectExtra(ctx *php.Ctx, r *reflector, name string, args []php.Value) (php.Value, bool, error) {")
	e.p("\tdocOf := func(key string) (php.Value, bool, error) {")
	e.p("\t\td, ok := reflectDocs[key]")
	e.p("\t\tif !ok {")
	e.p("\t\t\treturn php.Value{}, true, php.Unsupported(\"%%s::getDocComment of %%s\", r.kind, key)")
	e.p("\t\t}")
	e.p("\t\tif d.doc == \"\" {")
	e.p("\t\t\treturn php.Bool(false), true, nil")
	e.p("\t\t}")
	e.p("\t\treturn php.String(d.doc), true, nil")
	e.p("\t}")
	e.p("\t// methodKey is the table entry a ReflectionMethod reads. Its subject")
	e.p("\t// is already the class whose function table holds the method (the")
	e.p("\t// declaring class, or the class a trait composed it into).")
	e.p("\tmethodKey := func() (string, bool) {")
	e.p("\t\tif r.hook || r.invokeOwner != \"\" {")
	e.p("\t\t\treturn \"\", false")
	e.p("\t\t}")
	e.p("\t\treturn \"m:\" + strings.ToLower(realClassOr(r.subject)) + \"::\" + strings.ToLower(r.member), true")
	e.p("\t}")
	e.p("\tswitch name {")
	e.p("\tcase \"getdoccomment\":")
	e.p("\t\tswitch r.kind {")
	e.p("\t\tcase \"ReflectionFunction\":")
	e.p("\t\t\tfn := r.subject")
	e.p("\t\t\tif o := r.callable.Deref().Object(); o != nil {")
	e.p("\t\t\t\t// A first-class callable of a named function reads that")
	e.p("\t\t\t\t// function's comment; a closure literal is not described.")
	e.p("\t\t\t\tb, ok := o.Internal.(*closureBody)")
	e.p("\t\t\t\tif !ok || b.meta.function == \"\" || b.meta.name != b.meta.function || strings.Contains(b.meta.function, \"::\") {")
	e.p("\t\t\t\t\treturn php.Value{}, true, php.Unsupported(\"ReflectionFunction::getDocComment of a closure\")")
	e.p("\t\t\t\t}")
	e.p("\t\t\t\tfn = b.meta.function")
	e.p("\t\t\t}")
	e.p("\t\t\treturn docOf(\"f:\" + strings.ToLower(fn))")
	e.p("\t\tcase \"ReflectionMethod\":")
	e.p("\t\t\tkey, ok := methodKey()")
	e.p("\t\t\tif !ok {")
	e.p("\t\t\t\treturn php.Value{}, true, php.Unsupported(\"ReflectionMethod::getDocComment of %%s::%%s\", r.subject, r.member)")
	e.p("\t\t\t}")
	e.p("\t\t\treturn docOf(key)")
	e.p("\t\tcase \"ReflectionClass\", \"ReflectionObject\":")
	e.p("\t\t\tif r.closureClass {")
	e.p("\t\t\t\treturn php.Bool(false), true, nil")
	e.p("\t\t\t}")
	e.p("\t\t\treturn docOf(\"c:\" + strings.ToLower(realClassOr(r.subject)))")
	e.p("\t\tcase \"ReflectionProperty\":")
	e.p("\t\t\tif r.dynamic {")
	e.p("\t\t\t\treturn php.Bool(false), true, nil")
	e.p("\t\t\t}")
	e.p("\t\t\tif r.sensitiveValue {")
	e.p("\t\t\t\treturn php.Value{}, true, php.Unsupported(\"ReflectionProperty::getDocComment of an engine property\")")
	e.p("\t\t\t}")
	e.p("\t\t\tdesc, declared := reflectProperty(ctx, r.subject, r.member)")
	e.p("\t\t\tif !declared {")
	e.p("\t\t\t\treturn php.Value{}, true, php.Unsupported(\"ReflectionProperty::getDocComment of %%s::$%%s\", r.subject, r.member)")
	e.p("\t\t\t}")
	e.p("\t\t\towner := desc.Class")
	e.p("\t\t\tif owner == \"\" {")
	e.p("\t\t\t\towner = r.subject")
	e.p("\t\t\t}")
	e.p("\t\t\treturn docOf(\"p:\" + strings.ToLower(realClassOr(owner)) + \"::\" + r.member)")
	e.p("\t\tcase \"ReflectionClassConstant\":")
	e.p("\t\t\t_, k, isCase, found := reflectedConst(r.subject, r.member)")
	e.p("\t\t\tif !found || isCase || k == nil {")
	e.p("\t\t\t\treturn php.Value{}, true, php.Unsupported(\"ReflectionClassConstant::getDocComment of %%s::%%s\", r.subject, r.member)")
	e.p("\t\t\t}")
	e.p("\t\t\tif k.doc == \"\" {")
	e.p("\t\t\t\treturn php.Bool(false), true, nil")
	e.p("\t\t\t}")
	e.p("\t\t\treturn php.String(k.doc), true, nil")
	e.p("\t\t}")
	e.p("\tcase \"isfinal\":")
	e.p("\t\tif r.kind != \"ReflectionMethod\" {")
	e.p("\t\t\tbreak")
	e.p("\t\t}")
	e.p("\t\tkey, ok := methodKey()")
	e.p("\t\tif !ok {")
	e.p("\t\t\treturn php.Value{}, true, php.Unsupported(\"ReflectionMethod::isFinal of %%s::%%s\", r.subject, r.member)")
	e.p("\t\t}")
	e.p("\t\td, known := reflectDocs[key]")
	e.p("\t\tif !known {")
	e.p("\t\t\treturn php.Value{}, true, php.Unsupported(\"ReflectionMethod::isFinal of %%s::%%s\", r.subject, r.member)")
	e.p("\t\t}")
	e.p("\t\treturn php.Bool(d.final), true, nil")
	e.p("\tcase \"gettraitaliases\":")
	e.p("\t\tif r.kind != \"ReflectionClass\" && r.kind != \"ReflectionObject\" || r.closureClass {")
	e.p("\t\t\tbreak")
	e.p("\t\t}")
	e.p("\t\tpairs, listed := reflectTraitAliases[strings.ToLower(realClassOr(r.subject))]")
	e.p("\t\tif listed && pairs == nil {")
	e.p("\t\t\treturn php.Value{}, true, php.Unsupported(\"ReflectionClass::getTraitAliases of %%s\", r.subject)")
	e.p("\t\t}")
	e.p("\t\ta := php.NewArray()")
	e.p("\t\tfor _, pr := range pairs {")
	e.p("\t\t\ta.Set(php.StringKey(pr[0]), php.String(pr[1]))")
	e.p("\t\t}")
	e.p("\t\treturn php.Arr(a), true, nil")
	e.p("\tcase \"getdefaultproperties\":")
	e.p("\t\tif r.kind != \"ReflectionClass\" && r.kind != \"ReflectionObject\" || r.closureClass {")
	e.p("\t\t\tbreak")
	e.p("\t\t}")
	e.p("\t\tbuild, ok := reflectDefaultProps[strings.ToLower(realClassOr(r.subject))]")
	e.p("\t\tif !ok {")
	e.p("\t\t\treturn php.Value{}, true, php.Unsupported(\"ReflectionClass::getDefaultProperties of %%s\", r.subject)")
	e.p("\t\t}")
	e.p("\t\treturn php.Arr(build()), true, nil")
	e.p("\tcase \"iscallable\":")
	e.p("\t\tif r.kind != \"ReflectionParameter\" {")
	e.p("\t\t\tbreak")
	e.p("\t\t}")
	e.p("\t\tparams, known := reflectParams[r.subject]")
	e.p("\t\tif !known || r.index >= len(params) {")
	e.p("\t\t\treturn php.Value{}, true, php.Unsupported(\"ReflectionParameter::isCallable\")")
	e.p("\t\t}")
	e.p("\t\tctx.Deprecated(\"Method ReflectionParameter::isCallable() is deprecated since 8.0, use ReflectionParameter::getType() instead\")")
	e.p("\t\t// The engine's test is the type's pure mask equal to callable:")
	e.p("\t\t// `callable`, `?callable` and `callable|null` say true, any union")
	e.p("\t\t// with another type false (probed).")
	e.p("\t\tt := strings.ToLower(strings.ReplaceAll(params[r.index].typ, \" \", \"\"))")
	e.p("\t\tt = strings.TrimPrefix(t, \"?\")")
	e.p("\t\tvar rest []string")
	e.p("\t\tfor _, part := range strings.Split(t, \"|\") {")
	e.p("\t\t\tif part != \"null\" {")
	e.p("\t\t\t\trest = append(rest, part)")
	e.p("\t\t\t}")
	e.p("\t\t}")
	e.p("\t\treturn php.Bool(len(rest) == 1 && rest[0] == \"callable\"), true, nil")
	e.p("\tcase \"getclosure\":")
	e.p("\t\t// A ReflectionMethod of a compiled method: a closure over that")
	e.p("\t\t// declaration (reflMethodClosure).")
	e.p("\t\tif r.kind == \"ReflectionMethod\" && !r.hook && r.invokeOwner == \"\" {")
	e.p("\t\t\towner, spelled, static, ok := methodOwnerOf(realClassOr(r.subject), strings.ToLower(r.member))")
	e.p("\t\t\tif !ok {")
	e.p("\t\t\t\tbreak")
	e.p("\t\t\t}")
	e.p("\t\t\tv, err := reflMethodClosure(ctx, owner, spelled, static, namedCallableParams[strings.ToLower(owner+\"::\"+spelled)], args)")
	e.p("\t\t\treturn v, true, err")
	e.p("\t\t}")
	e.p("\t\t// Over a closure, the engine hands back that closure itself, which")
	e.p("\t\t// the compiler takes for a fresh result: one more holder, so its")
	e.p("\t\t// release does not give back the number the closure still has.")
	e.p("\t\tif r.kind == \"ReflectionFunction\" && r.callable.Deref().Kind() == php.KindObject {")
	e.p("\t\t\tc := r.callable.Deref()")
	e.p("\t\t\tc.Object().HoldHandle()")
	e.p("\t\t\treturn c, true, nil")
	e.p("\t\t}")
	e.p("\t\t// A ReflectionFunction of a named user function hands out a fresh")
	e.p("\t\t// closure over it, as Closure::fromCallable does (bug75474: they")
	e.p("\t\t// share the function's statics). One built over a closure answers")
	e.p("\t\t// that closure itself, which is left to the refusal after this.")
	e.p("\t\tif r.kind != \"ReflectionFunction\" || r.callable.Deref().Kind() != php.KindUndef && r.callable.Deref().Kind() != php.KindNull {")
	e.p("\t\t\tbreak")
	e.p("\t\t}")
	e.p("\t\tif _, user := userFuncs[strings.ToLower(r.subject)]; !user {")
	e.p("\t\t\tbreak")
	e.p("\t\t}")
	e.p("\t\treturn closureFromCallable(php.String(r.subject)), true, nil")
	e.p("\tcase \"getlazyinitializer\":")
	e.p("\t\tif r.kind != \"ReflectionClass\" && r.kind != \"ReflectionObject\" || r.closureClass {")
	e.p("\t\t\tbreak")
	e.p("\t\t}")
	e.p("\t\tsubject := php.Arg(args, 0).Deref().Object()")
	e.p("\t\tif subject == nil {")
	e.p("\t\t\tbreak")
	e.p("\t\t}")
	e.p("\t\tif incompleteAmong(args) {")
	e.p("\t\t\treturn php.Value{}, true, php.Unsupported(\"lazy-object reflection of an incomplete object\")")
	e.p("\t\t}")
	e.p("\t\tif !instanceOf(php.Obj(subject), r.subject) {")
	e.p("\t\t\treturn php.Value{}, true, php.RaiseAt(ctx, \"TypeError\", \"ReflectionClass::getLazyInitializer(): Argument #1 ($object) must be of type %%s, %%s given\", php.DisplayName(realClassOr(r.subject)), php.DisplayName(subject.ClassName))")
	e.p("\t\t}")
	e.p("\t\t// The callable as it was given (probed: `===` holds for a name, an")
	e.p("\t\t// array and a closure), only while the object is still lazy. One")
	e.p("\t\t// the lazy state alone holds is claimed as a temporary, so handing")
	e.p("\t\t// it out would make a second holder the claim did not see.")
	e.p("\t\tif subject.Lazy == nil || subject.Lazy.Instance != nil {")
	e.p("\t\t\treturn php.Null(), true, nil")
	e.p("\t\t}")
	e.p("\t\tif subject.Lazy.OwnsInitializer {")
	e.p("\t\t\treturn php.Value{}, true, php.Unsupported(\"ReflectionClass::getLazyInitializer of an initializer the lazy object alone holds\")")
	e.p("\t\t}")
	e.p("\t\treturn subject.Lazy.Initializer, true, nil")
	e.p("\tcase \"islazy\":")
	e.p("\t\tif r.kind != \"ReflectionProperty\" {")
	e.p("\t\t\tbreak")
	e.p("\t\t}")
	e.p("\t\tsubject := php.Arg(args, 0).Deref().Object()")
	e.p("\t\tif subject == nil {")
	e.p("\t\t\tbreak")
	e.p("\t\t}")
	e.p("\t\tif incompleteAmong(args) {")
	e.p("\t\t\treturn php.Value{}, true, php.Unsupported(\"lazy-object reflection of an incomplete object\")")
	e.p("\t\t}")
	e.p("\t\tif !instanceOf(php.Obj(subject), r.subject) {")
	e.p("\t\t\treturn php.Value{}, true, php.RaiseAt(ctx, \"TypeError\", \"ReflectionProperty::isLazy(): Argument #1 ($object) must be of type %%s, %%s given\", php.DisplayName(realClassOr(r.subject)), php.DisplayName(subject.ClassName))")
	e.p("\t\t}")
	e.p("\t\tif r.dynamic {")
	e.p("\t\t\treturn php.Bool(false), true, nil")
	e.p("\t\t}")
	e.p("\t\tdesc, declared := reflectProperty(ctx, r.subject, r.member)")
	e.p("\t\tif !declared {")
	e.p("\t\t\treturn php.Value{}, true, php.Unsupported(\"ReflectionProperty::isLazy of %%s::$%%s\", r.subject, r.member)")
	e.p("\t\t}")
	e.p("\t\tif desc.Static || desc.Virtual {")
	e.p("\t\t\treturn php.Bool(false), true, nil")
	e.p("\t\t}")
	e.p("\t\t// An initialized proxy answers for its instance, which may have")
	e.p("\t\t// been made lazy again (probed: the nested-proxy cases).")
	e.p("\t\tfor subject.Lazy != nil && subject.Lazy.Proxy && subject.Lazy.Instance != nil {")
	e.p("\t\t\tsubject = subject.Lazy.Instance")
	e.p("\t\t}")
	e.p("\t\tif subject.Lazy == nil || subject.Lazy.Instance != nil {")
	e.p("\t\t\treturn php.Bool(false), true, nil")
	e.p("\t\t}")
	e.p("\t\treturn php.Bool(!subject.Lazy.Skipped[r.member]), true, nil")
	e.p("\t}")
	e.p("\treturn php.Value{}, false, nil")
	e.p("}")
	e.p("")
}
