package php

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// `var_dump` is the first library function both consumers share, and the reason
// it is here rather than in the interpreter is the reason `Increment` is: two
// implementations of one output format would diverge on whichever consumer was
// exercised less, and this one is graded byte-exact against the engine.
//
// It is also the first instance of a *mechanism*. 2,532 of the 3,847 corpus
// files whose bodies lower need at least one library function -- 66% -- and
// 1,893 need this one. A compiled program that cannot call a builtin is not
// blocked on `var_dump`; it is blocked on having no standard library, and this
// file is the shape the rest of that library takes.
//
// # One hook, not four
//
// Rendering a *scalar* needs nothing. Rendering an *object* needs a class table:
// property visibility, `__debugInfo`, whether the class is internal, and what a
// Closure shows. Every one of those sits inside the object branch, so the
// boundary is a single question -- **what should this object show?** -- asked of
// the layer that has a class table, with all the formatting staying here.
//
// That was worth checking rather than assuming: the object path calls
// `findClass`, `resolveMethod`, `invoke`, `isBuiltinOnly` and `closureDebugInfo`,
// which reads like four or five capabilities crossing the boundary. They are all
// inside one function, so one hook covers them. `lazyLabel`,
// `dumpPropertyLabel` and `referenceMarked` looked like they belonged above too
// and do not -- they are pure over `*Object` and `*Array`, so they moved.

// ObjectProperties answers what an object shows under `var_dump`: the table to
// print, and whether it came from `__debugInfo` rather than the object's own
// properties -- which decides whether the keys carry visibility annotations,
// since whatever `__debugInfo` returns is an ordinary array.
//
// Supplied from above, because it needs a class table.
type ObjectProperties func(ctx *Ctx, o *Object) (shown *Array, described bool, err error)

// SetObjectProperties installs the hook. Per Ctx for the reason the constant
// table is: PHP is shared-nothing per request and a long-lived Go process is
// not.
func (c *Ctx) SetObjectProperties(fn ObjectProperties) { c.objectProps = fn }

// ObjectProperties applies the hook. `ErrNoObjectProperties` when nothing here
// can answer, which is a gap and not an empty object: a caller that rendered
// one as `()` would be inventing a shape.
func (c *Ctx) ObjectProperties(o *Object) (*Array, bool, error) {
	if c.objectProps == nil {
		return nil, false, fmt.Errorf("%w: rendering an object", ErrNoObjectProperties)
	}
	return c.objectProps(c, o)
}

// dumper carries what one rendering needs: the arrays already being printed, so
// a reference cycle prints *RECURSION* rather than descending forever, and the
// first error the hook raised.
type dumper struct {
	ctx        *Ctx
	seen       map[*arrayBody]bool
	properties ObjectProperties
	err        error
}

// VarDump renders a value exactly as `var_dump` prints it, without writing it.
// Separating the rendering from the write is what lets the interpreter keep its
// own output buffering and a compiled program use ctx.Write.
//
// An object with no ObjectProperties installed is refused rather than rendered
// without its properties: a dump that silently omits them is wrong output, not
// a missing feature, which is the masquerade QUALITY_GATE.md forbids.
func VarDump(ctx *Ctx, v Value) (string, error) {
	d := &dumper{ctx: ctx, seen: map[*arrayBody]bool{}, properties: ctx.objectProps}
	if d.properties == nil {
		d.properties = func(_ *Ctx, o *Object) (*Array, bool, error) {
			return nil, false, fmt.Errorf("%w: var_dump of an object", ErrNoObjectProperties)
		}
	}
	out := d.dump(v, 0)
	return out, d.err
}

// DebugZvalDump renders the ownership-oriented sibling of var_dump. Go does
// not use Zend's refcounted zvals, so the facts exposed here are the ones the
// runtime genuinely maintains: interned literal provenance, explicit
// reference bindings, resource/object identity, and value nesting. Temporary
// call ownership contributes the same one top-level reference Zend adds.
func DebugZvalDump(ctx *Ctx, v Value) (string, error) {
	d := &debugZvalDumper{
		ctx: ctx, seenArrays: map[*arrayBody]bool{}, seenObjects: map[*Object]bool{},
		properties: ctx.objectProps,
	}
	if d.properties == nil {
		d.properties = func(_ *Ctx, _ *Object) (*Array, bool, error) {
			return nil, false, fmt.Errorf("%w: debug_zval_dump of an object", ErrNoObjectProperties)
		}
	}
	result := d.dump(v, 0)
	return result, d.err
}

type debugZvalDumper struct {
	ctx         *Ctx
	seenArrays  map[*arrayBody]bool
	seenObjects map[*Object]bool
	properties  ObjectProperties
	err         error
}

func debugZvalCount(depth int) int {
	if depth == 0 {
		return 2
	}
	return 1
}

func arrayIsPacked(array *Array) bool {
	expected := int64(0)
	packed := true
	array.EachRaw(func(key Key, _ Value) bool {
		if !key.IsInt || key.Int != expected {
			packed = false
			return false
		}
		expected++
		return true
	})
	return packed
}

func (d *debugZvalDumper) dump(value Value, depth int) string {
	indent := strings.Repeat("  ", depth)
	if reference, ok := value.RefOf(); ok {
		var out strings.Builder
		fmt.Fprintf(&out, "%sreference refcount(%d) {\n", indent, reference.RefCount())
		out.WriteString(d.dump(reference.Get(), depth+1))
		fmt.Fprintf(&out, "%s}\n", indent)
		return out.String()
	}
	switch value.Kind() {
	case KindUndef:
		return indent + "UNKNOWN:0\n"
	case KindNull:
		return indent + "NULL\n"
	case KindFalse:
		return indent + "bool(false)\n"
	case KindTrue:
		return indent + "bool(true)\n"
	case KindInt:
		return fmt.Sprintf("%sint(%d)\n", indent, value.Int())
	case KindFloat:
		return indent + "float(" + DumpFloat(value.Float()) + ")\n"
	case KindString:
		suffix := fmt.Sprintf("refcount(%d)", debugZvalCount(depth))
		if value.IsInterned() {
			suffix = "interned"
		}
		return fmt.Sprintf("%sstring(%d) \"%s\" %s\n",
			indent, len(value.String()), value.String(), suffix)
	case KindResource:
		resource := value.Resource()
		return fmt.Sprintf("%sresource(%d) of type (%s) refcount(%d)\n",
			indent, resource.ID, resource.TypeName(), debugZvalCount(depth))
	case KindArray:
		array := value.Array()
		if d.seenArrays[array.arrayBody] {
			return indent + "*RECURSION*\n"
		}
		d.seenArrays[array.arrayBody] = true
		defer delete(d.seenArrays, array.arrayBody)
		if array.IsInterned() {
			return indent + "array(0) interned {\n" + indent + "}\n"
		}
		packed := ""
		if array.Len() > 0 && arrayIsPacked(array) {
			packed = "packed "
		}
		var out strings.Builder
		fmt.Fprintf(&out, "%sarray(%d) %srefcount(%d){\n",
			indent, array.Len(), packed, debugZvalCount(depth))
		array.EachRaw(func(key Key, item Value) bool {
			if key.IsInt {
				fmt.Fprintf(&out, "%s  [%d]=>\n", indent, key.Int)
			} else {
				fmt.Fprintf(&out, "%s  [\"%s\"]=>\n", indent, key.Str)
			}
			out.WriteString(d.dump(item, depth+1))
			return true
		})
		fmt.Fprintf(&out, "%s}\n", indent)
		return out.String()
	case KindObject:
		object := value.Object()
		if d.seenObjects[object] {
			return indent + "*RECURSION*\n"
		}
		d.seenObjects[object] = true
		defer delete(d.seenObjects, object)
		shown, described, err := d.properties(d.ctx, object)
		if err != nil {
			d.err = err
			return ""
		}
		initialized := 0
		shown.EachRaw(func(_ Key, item Value) bool {
			if item.Deref().Kind() != KindUndef {
				initialized++
			}
			return true
		})
		var out strings.Builder
		fmt.Fprintf(&out, "%s%sobject(%s)#%d (%d) refcount(%d){\n",
			indent, lazyLabel(object), DisplayName(object.ClassName), object.ID,
			initialized, debugZvalCount(depth))
		shown.EachRaw(func(key Key, item Value) bool {
			if item.Deref().Kind() == KindUndef && object.Declared[key.String()].Type == "" {
				return true
			}
			label := "\"" + key.String() + "\""
			if !described {
				label = dumpPropertyLabel(object, key.String())
			} else if key.IsInt {
				label = strconv.FormatInt(key.Int, 10)
			}
			fmt.Fprintf(&out, "%s  [%s]=>\n", indent, label)
			// A typed property holding nothing is named by its type, as
			// var_dump names it (typed_properties_101).
			if item.Deref().Kind() == KindUndef {
				fmt.Fprintf(&out, "%s  uninitialized(%s)\n", indent, object.Declared[key.String()].Type)
				return true
			}
			out.WriteString(d.dump(item, depth+1))
			return true
		})
		fmt.Fprintf(&out, "%s}\n", indent)
		return out.String()
	}
	return indent + "UNKNOWN:0\n"
}

func (d *dumper) dump(v Value, depth int) string {
	indent := strings.Repeat("  ", depth)
	// **A reference dumps as what it holds.** `$b = &$a; var_dump($b)` prints
	// `int(1)` with no `&`: the marker belongs to an array element or an
	// object property that a program made a reference, which is what
	// `referenceMarked` adds by looking at the container's raw slot. Falling
	// through to the default here printed `NULL` for every top-level box --
	// which is what a `function &f()` returns, so every by-reference return
	// dumped as null.
	v = v.Deref()
	switch v.Kind() {
	case KindUndef, KindNull:
		return indent + "NULL\n"
	case KindTrue:
		return indent + "bool(true)\n"
	case KindFalse:
		return indent + "bool(false)\n"
	case KindInt:
		return indent + "int(" + strconv.FormatInt(v.Int(), 10) + ")\n"
	case KindFloat:
		return indent + "float(" + DumpFloat(v.Float()) + ")\n"
	case KindString:
		s := v.String()
		return indent + "string(" + strconv.Itoa(len(s)) + ") \"" + s + "\"\n"
	case KindResource:
		r := v.Resource()
		return indent + "resource(" + strconv.Itoa(r.ID) + ") of type (" + r.TypeName() + ")\n"
	case KindObject:
		o := v.Object()
		if o == nil {
			return indent + "NULL\n"
		}
		if o.EnumCase != "" {
			return indent + "enum(" + DisplayName(o.ClassName) + "::" + o.EnumCase + ")\n"
		}
		table := o.dyn()
		if d.seen[table.arrayBody] {
			return indent + "*RECURSION*\n"
		}
		d.seen[table.arrayBody] = true
		defer delete(d.seen, table.arrayBody)
		// An uninitialized typed property is listed but not counted, so the
		// header's count is the number of properties that actually hold a value.
		shown, described, err := d.properties(d.ctx, o)
		if err != nil {
			d.err = err
			return ""
		}
		initialized := 0
		shown.Each(func(_ Key, item Value) bool {
			if item.Deref().Kind() != KindUndef {
				initialized++
			}
			return true
		})
		var b strings.Builder
		fmt.Fprintf(&b, "%s%sobject(%s)#%d (%d) {\n", indent, lazyLabel(o),
			DisplayName(o.ClassName), o.ID, initialized)
		shown.Each(func(k Key, item Value) bool {
			declared := o.Declared[k.String()]
			if item.Deref().Kind() == KindUndef && declared.Type == "" {
				// An empty slot is shown only when there is a type to name it
				// with: `if (!Z_ISUNDEF_P(val) || prop_info)`, where prop_info
				// comes from zend_get_typed_property_info_for_slot. An untyped
				// property holding nothing is not printed at all -- which is
				// what an object's own table looks like while it stands in for
				// a lazy proxy.
				return true
			}
			// Only real properties carry a visibility annotation; whatever
			// __debugInfo returns is an ordinary array, and an integer key in
			// one prints unquoted the way an array's does.
			label := "\"" + k.String() + "\""
			switch {
			case !described:
				label = dumpPropertyLabel(o, k.String())
			case k.IsInt:
				label = strconv.FormatInt(k.Int, 10)
			default:
				// A **mangled** key in it is still unmangled: `"\0*\0b" => 2`
				// dumps as `["b":protected]` (debug_info). The declarations
				// have no say -- the key is all there is.
				if plain, owner, mangled := UnmangleProperty(k.String()); mangled {
					label = "\"" + plain + "\":protected"
					if owner != "*" {
						label = "\"" + plain + "\":\"" + owner + "\":private"
					}
				}
			}
			fmt.Fprintf(&b, "%s  [%s]=>\n", indent, label)
			if item.Deref().Kind() == KindUndef {
				fmt.Fprintf(&b, "%s  uninitialized(%s)\n", indent, declared.Type)
				return true
			}
			b.WriteString(referenceMarked(shown, k, d.dump(item, depth+1), depth+1))
			return true
		})
		fmt.Fprintf(&b, "%s}\n", indent)
		return b.String()
	case KindArray:
		arr := v.Array()
		if d.seen[arr.arrayBody] {
			return strings.Repeat("  ", depth) + "*RECURSION*\n"
		}
		d.seen[arr.arrayBody] = true
		defer delete(d.seen, arr.arrayBody)
		var b strings.Builder
		fmt.Fprintf(&b, "%sarray(%d) {\n", indent, arr.Len())
		arr.Each(func(k Key, item Value) bool {
			if k.IsInt {
				fmt.Fprintf(&b, "%s  [%d]=>\n", indent, k.Int)
			} else {
				fmt.Fprintf(&b, "%s  [\"%s\"]=>\n", indent, k.Str)
			}
			b.WriteString(referenceMarked(arr, k, d.dump(item, depth+1), depth+1))
			return true
		})
		fmt.Fprintf(&b, "%s}\n", indent)
		return b.String()
	}
	return indent + "NULL\n"
}

func referenceMarked(source *Array, key Key, dumped string, depth int) string {
	raw, ok := source.Raw(key)
	if !ok || !SharedRef(raw) {
		return dumped
	}
	indent := strings.Repeat("  ", depth)
	if !strings.HasPrefix(dumped, indent) {
		return dumped
	}
	if strings.HasPrefix(dumped[len(indent):], "*RECURSION*") {
		// PHP does not mark the line it refuses to descend into: the marker
		// belongs to a value it printed, and this is a refusal to print one.
		return dumped
	}
	return indent + "&" + dumped[len(indent):]
}

// DumpFloat renders a float the way var_dump does, and is exported because the
// interpreter formats floats this way in diagnostics too -- one implementation,
// for the reason the rest of this file exists.
func DumpFloat(f float64) string {
	// PHP spells the three exceptional floats in capitals, which Go does not.
	switch {
	case math.IsInf(f, 1):
		return "INF"
	case math.IsInf(f, -1):
		return "-INF"
	case math.IsNaN(f):
		return "NAN"
	}
	return ReprFloat(f, 'E', false)
}

func dumpPropertyLabel(o *Object, name string) string {
	if plain, owner, mangled := UnmangleProperty(name); mangled {
		if owner == "*" {
			return "\"" + plain + "\":protected"
		}
		return "\"" + plain + "\":\"" + owner + "\":private"
	}
	switch declared := o.Declared[name]; {
	case o.Dynamic[name]:
		// Created at run time over an invisible declared name: public.
	case declared.Visibility == "protected":
		return "\"" + name + "\":protected"
	case declared.Visibility == "private":
		return "\"" + name + "\":\"" + DisplayName(declared.Class) + "\":private"
	}
	return "\"" + name + "\""
}

func lazyLabel(object *Object) string {
	if object == nil || object.Lazy == nil || object.Lazy.Incomplete {
		return ""
	}
	// While the initializer runs, the object is not waiting for one: the engine
	// drops the label for the duration, ghost and proxy alike. Probed -- a
	// proxy gets its label back once the initializer has returned.
	if object.Lazy.Running {
		return ""
	}
	if object.Lazy.Proxy {
		return "lazy proxy "
	}
	return "lazy ghost "
}

// ExportPair is one entry `var_export` writes for an object: the name as the
// engine prints it -- plain, never mangled -- and the value. Duplicates are
// possible and are kept: two classes in a chain may each declare a private
// property of one name, and var_export writes both.
type ExportPair struct {
	Name string
	// Key is set when the entry is an **element** rather than a property --
	// an ArrayObject's storage is written that way -- and it is rendered by
	// the array rule: an integer bare, a string quoted. A property name is
	// always quoted, and leaves this nil.
	Key   *Key
	Value Value
}

// ExportProperties answers what an object shows under `var_export`. It is a
// second hook rather than ObjectProperties because the two questions have
// different answers: `var_dump` wants the table with its visibility
// annotations, and `var_export` wants plain names, duplicates kept, a **get
// hook run** where one exists, and a virtual property included even though it
// has no slot to read.
//
// Supplied from above, because running a hook needs a class table.
type ExportProperties func(ctx *Ctx, o *Object) ([]ExportPair, error)

// SetExportProperties installs the hook.
func (c *Ctx) SetExportProperties(fn ExportProperties) { c.exportProps = fn }

// ExportProperties applies it, falling back to the object's own table with the
// names unmangled. The fallback is right for a class with no property hooks,
// which is every class in a program that declares none -- and a caller that
// *can* have hooks installs a hook that says so.
func (c *Ctx) ExportProperties(o *Object) ([]ExportPair, error) {
	// **The store's elements, flat**, and neither back-end's hook knows about
	// them. Measured rather than assumed: `var_export(new ArrayObject([1,2]))`
	// writes `\ArrayObject::__set_state(array( 0 => 1, 1 => 2, ))` -- the
	// contents directly, with no `storage` entry, which is the opposite of
	// what `var_dump` shows for the same object.
	if store, backed := AsArrayStore(o); backed {
		var out []ExportPair
		store.Items().Each(func(k Key, item Value) bool {
			key := k
			out = append(out, ExportPair{Name: k.String(), Key: &key, Value: item.Deref()})
			return true
		})
		return out, nil
	}
	if c.exportProps != nil {
		return c.exportProps(c, o)
	}
	return TableExportProperties(o), nil
}

// TableExportProperties is the no-hook answer: the property table as it
// stands, with the names unmangled and uninitialised slots left out.
func TableExportProperties(o *Object) []ExportPair {
	if o == nil {
		return nil
	}
	var out []ExportPair
	o.PropEach(func(k Key, item Value) bool {
		if item.Deref().Kind() == KindUndef {
			return true
		}
		name := k.String()
		if plain, _, mangled := UnmangleProperty(name); mangled {
			name = plain
		}
		out = append(out, ExportPair{Name: name, Value: item.Deref()})
		return true
	})
	return out
}
