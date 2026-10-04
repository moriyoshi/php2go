package interp

import (
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// Autoloading: a class the program has not declared is not necessarily missing.
// spl_autoload_register hands the engine a callback to consult first, and the
// callback usually includes the file that declares it -- which is why include
// merges its declarations into the running unit.

// autoloadBuiltin implements the spl_autoload_* family, which needs the unit's
// registry rather than only the context.
func (in *Interp) autoloadBuiltin(ctx *php.Ctx, unit *Unit, name string, args []php.Value) (php.Value, bool, error) {
	switch name {
	case "spl_autoload_register":
		if len(args) == 0 || args[0].Kind() == php.KindNull {
			// PHP falls back to its own spl_autoload, which resolves names
			// against the include path. Nothing here implements that, so
			// saying so is more honest than registering nothing.
			return php.Undef(), true, &UnsupportedError{Opcode: "spl_autoload_register with no callback"}
		}
		if _, ok := unit.dynamicTarget(ctx, args[0]); !ok {
			return php.Undef(), true, Raise("TypeError",
				"spl_autoload_register(): Argument #1 ($callback) must be a valid callback")
		}
		// The registry, its ordering and its duplicate rule live on the Ctx so
		// compiled code shares them; what stays here is the validation above,
		// which needs a class table.
		return php.Bool(ctx.RegisterAutoloader(args[0], len(args) > 2 && args[2].Bool())), true, nil

	case "spl_autoload_unregister":
		if len(args) == 0 {
			return php.Bool(false), true, nil
		}
		return php.Bool(ctx.UnregisterAutoloader(args[0])), true, nil

	case "spl_autoload_functions":
		registered := php.NewArray()
		for _, callback := range ctx.Autoloaders() {
			registered.Append(callback)
		}
		return php.Arr(registered), true, nil

	case "spl_autoload_call":
		if len(args) == 0 {
			return php.Null(), true, nil
		}
		_, _, err := in.autoload(ctx, unit, args[0].String(), 0)
		return php.Null(), true, err
	}
	return php.Undef(), false, nil
}

// resolveClass looks a class up, consulting the autoloaders when it is not
// already known. Every place that names a class at run time goes through this
// rather than findClass, which is the lookup that must not have side effects.
func (in *Interp) resolveClass(ctx *php.Ctx, unit *Unit, name string, depth int) (*Class, bool, error) {
	if unit.classAvailable(in, name) {
		class, _ := unit.findClass(name)
		return class, true, nil
	}
	// Present in the harvest but not declared yet is not available: the engine
	// would not have the class either, and asking for it runs the autoloaders.
	// If none of them produces it, the harvested declaration is still used --
	// deciding that a program may not reach its own class is a bigger claim
	// than this needs to make.
	if class, ok, err := in.autoload(ctx, unit, name, depth); ok || err != nil {
		return class, ok, err
	}
	class, ok := unit.findClass(name)
	return class, ok, nil
}

// autoload runs the registered callbacks until one of them declares the class.
// A callback that throws stops the search, as it does in PHP.
func (in *Interp) autoload(ctx *php.Ctx, unit *Unit, name string, depth int) (*Class, bool, error) {
	// A fully qualified name may be written with a leading separator; the class
	// table is keyed without one and the autoloader is handed the name without
	// one -- `new '\\Foo\\Bar'` calls the callback with "Foo\\Bar".
	name = strings.TrimPrefix(name, "\\")
	if unit.linking[strings.ToLower(name)] {
		// The name is already in the class table -- its own declaration is
		// running -- so there is nothing for an autoloader to supply. Asking
		// one would re-enter it and redeclare whatever it declares.
		return nil, false, nil
	}
	if unit.autoloading[strings.ToLower(name)] {
		// A callback that triggers autoloading of the same name would loop; PHP
		// suppresses the re-entry rather than recursing.
		return nil, false, nil
	}
	if unit.autoloading == nil {
		unit.autoloading = map[string]bool{}
	}
	lower := strings.ToLower(name)
	unit.autoloading[lower] = true
	defer delete(unit.autoloading, lower)

	for _, callback := range ctx.Autoloaders() {
		target, ok := unit.dynamicTarget(ctx, callback)
		if !ok {
			continue
		}
		target.args = []php.Value{php.String(name)}
		// The autoloader is a frame in every backtrace taken inside it, and the
		// call site it names is wherever the program asked for the class --
		// which is where the engine puts it too.
		file, line := ctx.Location()
		ctx.PushCall(php.StackFrame{
			Function: target.traceName(), File: file, Line: line,
			Args: target.args, This: target.this,
		})
		_, err := in.dispatch(ctx, unit, target, depth)
		ctx.PopCall()
		// The autoloader's body moved the reported location, and what happens
		// next belongs to the site that asked for the class: a `Class not
		// found` raised after this names the opline that wanted it, which for
		// an eval is the eval'd code and not the file that ran it.
		ctx.SetLocation(file, line)
		if err != nil {
			// A callback that throws stops the search and the throw carries on
			// outwards; one that calls exit() stops the request. Swallowing
			// either turned a deliberate stop into "class not found".
			return nil, false, err
		}
		if class, found := unit.findClass(name); found {
			return class, true, nil
		}
	}
	return nil, false, nil
}
