package php

import "strings"

// __PHP_Incomplete_Class is what unserialize makes of an object whose class
// it cannot find: an object of that engine class holding the properties it
// read, with the original class name kept under __PHP_Incomplete_Class_Name.
// Its object handlers refuse most things (probed, bug71841):
//
//   - reading a property -- plain, isset, empty, `??`, property_exists -- is a
//     warning, "<function>: The script tried to access a property on an
//     incomplete object ...", and reads NULL (isset false);
//   - writing, unsetting, taking a reference, a compound assignment or a
//     dimension write through a property is an Error, "... tried to modify
//     a property ...";
//   - calling a method, method_exists and is_callable are an Error, "...
//     tried to call a method ...";
//   - var_dump, print_r, var_export and the (array) cast show the
//     properties as stored, the name included;
//   - serialize writes the original class name back, without the name
//     property.
//
// Everything else an object can do -- foreach, clone, get_object_vars,
// json_encode, comparisons, the rest of reflection -- is refused: the
// object carries a Lazy marker, so every property path reaches ResolveLazy,
// which refuses it unless one of the handlers above answered first.

// IncompleteClass is the engine class's name.
const IncompleteClass = "__PHP_Incomplete_Class"

// IncompleteNameProperty is the property the original class name is kept in.
const IncompleteNameProperty = "__PHP_Incomplete_Class_Name"

// NewIncompleteObject makes an object of the class, gated.
func NewIncompleteObject(id int) *Object { return NewObject(IncompleteClass, id) }

// IsIncomplete reports an __PHP_Incomplete_Class object.
func IsIncomplete(o *Object) bool {
	return o != nil && o.Lazy != nil && o.Lazy.Incomplete
}

// IncompleteName is the class name an incomplete object stands for: its
// name property when that is a string, else "" (the engine's "unknown").
func IncompleteName(o *Object) string {
	if v, ok := o.Get(IncompleteNameProperty); ok && v.Deref().Kind() == KindString {
		return v.Deref().RawString()
	}
	return ""
}

func incompleteMessage(o *Object, verb string) string {
	name := IncompleteName(o)
	if name == "" {
		name = "unknown"
	}
	return "The script tried to " + verb + " on an incomplete object. Please ensure that the class definition \"" +
		name + "\" of the object you are trying to operate on was loaded _before_ unserialize() gets called or provide an autoloader to load the class definition"
}

// incompletePrefix is the active function as php_error_docref names it:
// "Unknown" at file scope, `f`, `C::m` for a method, a closure's own name.
// Inside an include, an eval or a library function the answer is not
// modelled, and ok is false.
func (c *Ctx) incompletePrefix() (string, bool) {
	stack := c.CallStack()
	if len(stack) == 0 {
		return "Unknown", true
	}
	top := stack[len(stack)-1]
	if top.Internal || IsIncludeFunction(top.Function) || top.Function == "" {
		return "", false
	}
	return strings.Replace(top.Function, "->", "::", 1) + "()", true
}

// IncompleteReadWarning is the read handler: the warning, and NULL.
func IncompleteReadWarning(c *Ctx, o *Object) (Value, error) {
	prefix, ok := c.incompletePrefix()
	if !ok {
		return Value{}, Unsupported("property read on an incomplete object here")
	}
	c.Warn("%s: %s", prefix, incompleteMessage(o, "access a property"))
	if err := c.TakePendingError(); err != nil {
		return Value{}, err
	}
	return Null(), nil
}

// IncompleteReadWarningFrom is the same warning raised from inside a library
// function (property_exists), which names itself.
func IncompleteReadWarningFrom(c *Ctx, o *Object, function string) error {
	c.Warn("%s(): %s", function, incompleteMessage(o, "access a property"))
	return c.TakePendingError()
}

// IncompleteModifyError is the write handler's Error.
func IncompleteModifyError(c *Ctx, o *Object) error {
	return RaiseAt(c, "Error", "%s", incompleteMessage(o, "modify a property"))
}

// IncompleteCallError is the get_method handler's Error.
func IncompleteCallError(c *Ctx, o *Object) error {
	return RaiseAt(c, "Error", "%s", incompleteMessage(o, "call a method"))
}

// SetEngineClassCheck installs what EngineHasClass asks.
func (c *Ctx) SetEngineClassCheck(fn func(string) bool) { c.engineHasClass = fn }

// EngineHasClass reports a class the linked engine registers. With no hook
// installed every name counts as one, which refuses rather than guesses.
func (c *Ctx) EngineHasClass(class string) bool {
	if c.engineHasClass == nil {
		return true
	}
	return c.engineHasClass(class)
}

// SetClassAutoloader installs what AutoloadClass runs.
func (c *Ctx) SetClassAutoloader(fn func(*Ctx, string) (bool, error)) { c.autoloadClass = fn }

// AutoloadClass runs the program's autoloaders for a class and reports
// whether one of them made it exist. With no hook installed nothing is
// loaded -- but an autoloader the back-end cannot run must not be skipped,
// so a registered one without a hook is refused.
func (c *Ctx) AutoloadClass(class string) (bool, error) {
	if c.autoloadClass == nil {
		if len(c.autoloaders) > 0 {
			return false, Unsupported("autoloading %s here", class)
		}
		return false, nil
	}
	return c.autoloadClass(c, class)
}

// IncompleteCallable reports a callable over an incomplete object: the
// object itself, or an [object, method] pair holding one.
func IncompleteCallable(v Value) bool {
	v = v.Deref()
	switch v.Kind() {
	case KindObject:
		return IsIncomplete(v.Object())
	case KindArray:
		if first, ok := v.Array().Get(IntKey(0)); ok && v.Array().Len() == 2 {
			return IsIncomplete(first.Deref().Object())
		}
	}
	return false
}
