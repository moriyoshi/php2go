package php

import "strings"

// RestoreDeclaredDefaults writes every declared property's initializer back
// into an object, which is what a lazy **ghost** does on its way into its
// initializer: the initializer starts from the declared defaults, not from an
// empty table.
//
// Three rules, and each of them was wrong once:
//
//   - A **static** property belongs to the class and a **virtual** one has no
//     slot, so neither is written.
//   - A property already in `Lazy.Skipped` is **left alone**. It has been given
//     a value and no longer defers anything, and restoring over it is how a raw
//     write got wiped on the way into the initializer.
//   - A typed property with no initializer stays **uninitialized** rather than
//     becoming null. Only an untyped one starts as NULL, and the difference is
//     visible: `var_dump` prints `uninitialized(int)` for the first.
func RestoreDeclaredDefaults(c *Ctx, object *Object) {
	if object == nil {
		return
	}
	for _, prop := range c.DeclaredPropertiesOf(object.ClassName) {
		if prop.Static || prop.Virtual {
			continue
		}
		if object.Lazy != nil && object.Lazy.Skipped[prop.Name] {
			continue
		}
		WriteDeclaredDefault(object, prop)
	}
}

// WriteDeclaredDefault writes one property's declared default.
func WriteDeclaredDefault(object *Object, prop PropertyDesc) {
	value := Undef()
	switch {
	case prop.HasDefault:
		value = prop.Default.Copy()
	case prop.Type == "":
		value = Null()
	}
	object.PropSet(StringKey(prop.Name), value)
}

// AllLazySkipped reports whether every property a lazy object could defer has
// been given a value already.
//
// That is what ends the laziness **without an initializer ever running**: an
// object with nothing left to defer is not lazy. Counting what a reset cleared
// gets the common case right and the general one wrong, which is why the
// question is asked of the deferred set rather than of the clearing.
func AllLazySkipped(c *Ctx, object *Object) bool {
	if object == nil || object.Lazy == nil {
		return false
	}
	for _, prop := range c.DeclaredPropertiesOf(object.ClassName) {
		if prop.Static || prop.Virtual {
			continue
		}
		if !object.Lazy.Skipped[prop.Name] {
			return false
		}
	}
	return true
}

// SkipLazyProperty takes one property out of a lazy object's deferred set,
// which is what both of `ReflectionProperty`'s raw writers do.
//
// `given` tells them apart. `skipLazyInitialization` writes the **declared
// default** and, on a property that is already not lazy, does nothing at all --
// it must not write the default a second time over whatever the program has
// since put there. `setRawValueWithoutLazyInitialization` was handed a value,
// so it overwrites.
//
// Neither asks whether the object is lazy: on an ordinary one the raw write is
// an ordinary write and skipping does nothing.
func SkipLazyProperty(c *Ctx, object *Object, prop PropertyDesc, value Value, given bool) error {
	if given {
		// **A raw write is a write.** It carries the property's declared type,
		// so it coerces or refuses exactly as an assignment would, and on an
		// already-initialized readonly property it is
		// `Cannot modify readonly property`. Skipping writes nothing of the
		// caller's and is never refused for either reason.
		if prop.Readonly {
			if held, ok := object.PropGet(StringKey(prop.Name)); ok && !held.Deref().IsUndef() {
				return RaiseAt(c, "Error", "Cannot modify readonly property %s::$%s",
					DisplayName(prop.Class), prop.Name)
			}
		}
		if prop.Type != "" {
			coerced, err := CoerceProperty(c, value, prop.Type, prop.Class, prop.Name, false)
			if err != nil {
				return err
			}
			value = coerced
		}
	}
	if object == nil || object.Lazy == nil {
		if given {
			object.PropSet(StringKey(prop.Name), value)
		}
		return nil
	}
	if !given && object.Lazy.Skipped[prop.Name] {
		return nil
	}
	if given {
		object.PropSet(StringKey(prop.Name), value)
	} else {
		WriteDeclaredDefault(object, prop)
	}
	if object.Lazy.Skipped == nil {
		object.Lazy.Skipped = map[string]bool{}
	}
	object.Lazy.Skipped[prop.Name] = true
	if AllLazySkipped(c, object) {
		DropLazy(c, object)
	}
	return nil
}

// LookupDeclaredProperty finds one declared property by name.
func LookupDeclaredProperty(c *Ctx, class, name string) (PropertyDesc, bool) {
	for _, prop := range c.DeclaredPropertiesOf(class) {
		if prop.Name == name {
			return prop, true
		}
	}
	return PropertyDesc{}, false
}

// ClassIsFinal reports whether nothing may extend a class, which the lazy reset
// rule turns on and which only the class table knows.
type ClassIsFinal func(class string) bool

// SetClassIsFinal installs the hook.
func (c *Ctx) SetClassIsFinal(fn ClassIsFinal) { c.classIsFinal = fn }

// StripStateForReset unsets an object's declared properties so that a lazy
// initializer may write them again, and answers the names it **kept**.
//
// Two things survive, and both have to be marked non-deferred by the caller --
// keeping the value alone is not enough, because the ghost's `restoreDefaults`
// pass wipes it on the way into the initializer otherwise.
//
//   - A property declared by a **subclass of the reflected class**. The reset
//     walks the reflected class's slots, so resetting an instance of
//     `B extends A` through `ReflectionClass(A::class)` leaves B's own
//     properties alone -- their slots are past the end of what it walks. A
//     *dynamic* property has a slot in neither and is cleared with the rest.
//   - A **readonly** property already holding a value, when the object's class
//     is **final**. With no subclass able to reach it there is nothing left that
//     could legitimately write it again, so the value stands and the
//     reconstructing `__construct` is refused. A non-final class clears it and
//     `__construct` may set it once more.
func StripStateForReset(c *Ctx, object *Object, reflected string) []string {
	inReflected := map[string]bool{}
	if reflected != "" {
		for _, prop := range c.DeclaredPropertiesOf(reflected) {
			inReflected[strings.ToLower(prop.Name)] = true
		}
	}
	isFinal := c.classIsFinal != nil && c.classIsFinal(object.ClassName)
	var kept []string
	for _, key := range object.PropKeys() {
		name := key.String()
		if reflected != "" {
			plain, _, _ := UnmangleProperty(name)
			_, declared := object.Declared[name]
			if declared && !object.Dynamic[plain] && !inReflected[strings.ToLower(plain)] {
				kept = append(kept, name)
				continue
			}
		}
		if isFinal && !object.Reinitable[name] {
			plain, _, _ := UnmangleProperty(name)
			if prop, declared := LookupDeclaredProperty(c, object.ClassName, plain); declared && prop.Readonly {
				if value, held := object.PropGet(key); held && value.Deref().Kind() != KindUndef {
					kept = append(kept, name)
					continue
				}
			}
		}
		object.PropSet(key, Undef())
	}
	return kept
}

// StripLazyState takes an object back to having no property *values*, keeping
// every slot. The slots are what preserve the order the class declares them in,
// and -- for a lazy object -- what a dump of an uninitialized one has to name.
func StripLazyState(object *Object) {
	for _, key := range object.PropKeys() {
		object.PropSet(key, Undef())
	}
}

// DropDynamicProperties removes the properties the class never declared.
//
// It belongs with the case where laziness was asked for and there was nothing
// to defer: the object goes back to being ordinary, and an ordinary one made
// from a class with no declared properties has none at all.
func DropDynamicProperties(object *Object) {
	for _, key := range object.PropKeys() {
		if _, declared := object.Declared[key.String()]; declared {
			continue
		}
		object.PropUnset(key)
		delete(object.Dynamic, key.String())
	}
}

// LazyVisibleProperties is what a dump of an **uninitialized** lazy object
// shows: its typed properties, named but with no value.
//
// An untyped property has no type to name and PHP does not list it at all --
// there is nothing to say about it. `var_dump` renders the rest as
// `uninitialized(int)`, and the header count excludes them.
func LazyVisibleProperties(object *Object) *Array {
	shown := NewArray()
	object.PropEach(func(k Key, v Value) bool {
		if v.Deref().IsUndef() && object.Declared[k.String()].Type == "" {
			return true
		}
		shown.Set(k, v)
		return true
	})
	return shown
}
