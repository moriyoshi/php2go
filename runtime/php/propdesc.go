package php

import "strings"

// What a declared property looks like to code that has no class table.
//
// `runtime/php` cannot see the IR: emitted programs import `runtime/*` and
// never `internal/*`, so a shared algorithm cannot take `*pb.Property` the way
// the interpreter's own does. This is the neutral shape instead -- exactly the
// fields the lazy-object machinery reads, and no more, so that adding one is a
// deliberate act rather than a side effect of exposing a protobuf.

// PropertyDesc is one property as **declared**, not as an instance holds it.
type PropertyDesc struct {
	// Name is the property's plain name, unmangled.
	Name string
	// Class is the class that declared it, which is not the instance's class
	// for anything inherited.
	Class string
	// Type is the declared type as PHP renders it, empty when untyped. It is
	// what decides whether a property with no initializer starts as NULL or
	// stays *uninitialized*: only a typed one stays.
	Type string
	// Static properties belong to the class, and Virtual ones -- a hooked
	// property with no backing store -- have no slot at all. Neither defers
	// anything, so neither takes part in laziness.
	Static  bool
	Virtual bool
	// Visibility is "private", "protected", or "" for public. It is here for
	// one reason: a **private** property is its own `properties_info` entry per
	// declaring class, so two classes in a chain may each have one of the same
	// name, and the lazy-proxy check counts those entries.
	Visibility string
	// Readonly marks a property writable once, from its declaring class only.
	// The lazy reset rule reads it: a readonly property holding a value
	// survives when the class is final. Taken from the **class table** rather
	// than from the object's own `Declared` map, because not every back-end
	// records the flag there and the declaration is the authority either way.
	Readonly bool
	// HasDefault says whether Default is meaningful. A property declared
	// `public int $x;` has no default and is not the same as one defaulting to
	// null.
	HasDefault bool
	// Default is the declared initializer, already evaluated.
	Default Value
}

// DeclaredProperties answers a class's properties in **the order the class
// table walks them**, which is the order the engine restores defaults in. The
// hierarchy walk belongs to whoever owns that table, which is why it happens
// inside the hook rather than around it -- the two back-ends flatten
// inheritance differently and neither order is this package's to assume.
type DeclaredProperties func(class string) []PropertyDesc

// SetDeclaredProperties installs the hook.
func (c *Ctx) SetDeclaredProperties(fn DeclaredProperties) { c.declaredProps = fn }

// DeclaredPropertiesOf applies it, answering nil when no class table is
// installed -- which is not the same as a class with no properties, but the two
// behave alike here: nothing to defer either way.
func (c *Ctx) DeclaredPropertiesOf(class string) []PropertyDesc {
	if c.declaredProps == nil {
		return nil
	}
	return c.declaredProps(class)
}

// DeclaresProperty reports whether the class declares a property of that name,
// which is what tells a *dynamic* property from a declared one.
func (c *Ctx) DeclaresProperty(class, name string) bool {
	for _, p := range c.DeclaredPropertiesOf(class) {
		if p.Name == name {
			return true
		}
	}
	return false
}

// PropertyInfoCount is how many entries a class's `properties_info` has, which
// is what the lazy-proxy compatibility check compares.
//
// **Counted, not searched for.** One entry per declared name, except that a
// private property is its own entry per declaring class. An *override* shares
// the entry it overrides, so a subclass that redeclares an inherited property
// adds nothing while one that declares a new name adds an entry. That is what
// makes an override free, and it is why `default_properties_count` is the wrong
// number -- the engine's own comment says it is off by the number of overrides.
func PropertyInfoCount(c *Ctx, class string) int {
	seen := map[string]bool{}
	for _, prop := range c.DeclaredPropertiesOf(class) {
		key := strings.ToLower(prop.Name)
		if prop.Visibility == "private" {
			owner := prop.Class
			if owner == "" {
				owner = class
			}
			key = strings.ToLower(owner) + "\x00" + key
		}
		seen[key] = true
	}
	return len(seen)
}
