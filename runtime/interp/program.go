package interp

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"

	"github.com/moriyoshi/php2go/runtime/libc"
	"github.com/moriyoshi/php2go/runtime/php"
	"github.com/moriyoshi/php2go/runtime/stdlib"
)

// libc's openlog state is process-global and retains the identity pointer.
// A request that touches it owns this lock until closelog() or request teardown,
// preventing another interpreter from replacing its identity between calls.
var syslogMu sync.Mutex

func acquireSyslog(unit *Unit) {
	if unit.syslogLocked {
		return
	}
	syslogMu.Lock()
	unit.syslogLocked = true
}

func releaseSyslog(unit *Unit) {
	if !unit.syslogLocked {
		return
	}
	unit.syslogLocked = false
	syslogMu.Unlock()
}

func cleanupSyslog(unit *Unit) {
	if !unit.syslogLocked {
		return
	}
	libc.SyslogClose()
	releaseSyslog(unit)
}

// syslogSegments applies main/php_syslog.c's byte filter before each libc call.
// Newlines split records unless raw mode bypasses filtering altogether.
func syslogSegments(message, filter string) []string {
	if filter == "raw" {
		return []string{message}
	}
	segments := make([]string, 0, 1+strings.Count(message, "\n"))
	var current strings.Builder
	hex := "0123456789abcdef"
	for index := 0; index < len(message); index++ {
		char := message[index]
		switch {
		case char >= 0x20 && char <= 0x7e:
			current.WriteByte(char)
		case char >= 0x80 && filter != "ascii":
			current.WriteByte(char)
		case char == '\n':
			segments = append(segments, current.String())
			current.Reset()
		case char < 0x20 && filter == "all":
			current.WriteByte(char)
		default:
			current.WriteString("\\x")
			current.WriteByte(hex[char>>4])
			current.WriteByte(hex[char&0x0f])
		}
	}
	return append(segments, current.String())
}

// execLine applies ext/standard/exec.c's C-locale isspace trimming. Go's
// Unicode whitespace set is deliberately wider than libc's byte-oriented one.
func execLine(text string) string {
	return strings.TrimRight(text, " \t\n\v\f\r")
}

// processOutputLines visits exactly the records php_exec sees: a newline is
// retained in each complete record, and a final newline does not invent an
// additional empty record.
func processOutputLines(output string, visit func(string) error) error {
	for start := 0; start < len(output); {
		end := len(output)
		if newline := strings.IndexByte(output[start:], '\n'); newline >= 0 {
			end = start + newline + 1
		}
		if err := visit(output[start:end]); err != nil {
			return err
		}
		start = end
	}
	return nil
}

func validateShellCommand(name, command string) error {
	if command == "" {
		return Raise("ValueError", "%s(): Argument #1 ($command) must not be empty", name)
	}
	if strings.IndexByte(command, 0) >= 0 {
		return Raise("ValueError", "%s(): Argument #1 ($command) must not contain any null bytes", name)
	}
	return nil
}

// iniAssigned reports whether a setting was given a value rather than being
// left at its default. fiber.stack_size needs the distinction: unset means "use
// the platform's default", while an explicit empty string means zero, which is
// too small for any fiber.
func (u *Unit) iniAssigned(name string) bool {
	_, held := u.ini[name]
	return held
}

// iniQuantity reads a byte-count setting the way the engine parses one: a
// number with an optional K, M or G suffix, each a multiple of 1024.
// minFiberStack is the smallest stack a fiber may be given: two pages on the
// platforms php2go builds for, which is what the engine reports.
const minFiberStack = 8192

// setting reads a modelled setting, falling back to its default.
func (u *Unit) setting(name string) string {
	if value, held := u.ini[name]; held {
		return value
	}
	if entry := u.iniByName[name]; entry != nil && entry.LocalValue != nil {
		return entry.GetLocalValue()
	}
	value, _ := stdlib.IniModelled(name)
	return value
}

// iniValue reports a setting and whether php2go models it at all.
func (u *Unit) iniValue(name string) (string, bool) {
	if _, modelled := stdlib.IniModelled(name); !modelled {
		return "", false
	}
	return u.setting(name), true
}

func (u *Unit) setSetting(name, value string) {
	if u.ini == nil {
		u.ini = map[string]string{}
	}
	u.ini[name] = value
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func (u *Unit) ensureAssertionOptions() {
	if u.assertOptionsInitialized {
		return
	}
	u.assertOptionsInitialized = true
	u.assertActive = true
	u.assertWarning = true
	u.assertBail = false
	u.assertException = true
	u.assertCallback = php.Null()
}

func (u *Unit) assertionOption(option int64) php.Value {
	u.ensureAssertionOptions()
	switch option {
	case 1:
		return php.Int(boolInt(u.assertActive))
	case 2:
		return u.assertCallback.Copy()
	case 3:
		return php.Int(boolInt(u.assertBail))
	case 4:
		return php.Int(boolInt(u.assertWarning))
	case 5:
		return php.Int(boolInt(u.assertException))
	default:
		return php.Null()
	}
}

func (u *Unit) setAssertionOption(option int64, value php.Value) php.Value {
	previous := u.assertionOption(option)
	switch option {
	case 1:
		u.assertActive = value.Deref().Bool()
	case 2:
		u.assertCallback = value.Copy()
	case 3:
		u.assertBail = value.Deref().Bool()
	case 4:
		u.assertWarning = value.Deref().Bool()
	case 5:
		u.assertException = value.Deref().Bool()
	}
	return previous
}

// programBuiltin implements the functions that act on the program itself --
// its class table, its shutdown sequence, its exception handler. They cannot
// live in the plain builtin map, which sees only a context.
func (in *Interp) programBuiltin(ctx *php.Ctx, unit *Unit, name string, args []php.Value, depth int) (php.Value, bool, error) {
	if value, handled, err := in.autoloadBuiltin(ctx, unit, name, args); handled {
		return value, true, err
	}
	if value, handled, err := in.callbackBuiltin(ctx, unit, name, args, depth); handled {
		return value, true, err
	}
	// set_error_handler and restore_error_handler moved to runtime/php with the
	// stack they manipulate, and are dispatched here because that is the path
	// they were on. printf's family also moved, and is bridged into the
	// `builtins` table instead -- see sharedBuiltins in builtins.go, and the
	// comment there on why the path matters.
	if fn, ok := php.LookupBuiltin(name); ok {
		switch name {
		case "set_error_handler", "restore_error_handler",
			"set_exception_handler", "restore_exception_handler":
			value, err := fn(ctx, args)
			return value, true, err
		}
	}
	if value, handled, err := in.iteratorBuiltin(ctx, unit, name, args, depth); handled {
		return value, true, in.materialize(ctx, unit, err)
	}
	if value, handled, err := in.localsBuiltin(ctx, unit, name, args); handled {
		return value, true, in.materialize(ctx, unit, err)
	}
	if value, handled, err := randomBuiltin(ctx, unit, name, args); handled {
		return value, true, in.materialize(ctx, unit, err)
	}
	if value, handled, err := in.introspectBuiltin(ctx, unit, name, args); handled {
		return value, true, in.materialize(ctx, unit, err)
	}
	if value, handled, err := in.streamBuiltin(ctx, unit, name, args, depth); handled {
		return value, true, in.materialize(ctx, unit, err)
	}
	if value, handled, err := in.sourceRenderBuiltin(ctx, unit, name, args); handled {
		return value, true, in.materialize(ctx, unit, err)
	}
	switch name {
	case "realpath":
		path := args[0].Deref().String()
		if strings.IndexByte(path, 0) >= 0 {
			return php.Undef(), true, Raise(
				"ValueError",
				"realpath(): Argument #1 ($path) must not contain any null bytes",
			)
		}
		resolved, errorNumber, available := in.resolveRealpath(unit, path)
		if !available {
			return php.Undef(), true, &UnsupportedError{Opcode: "libc realpath without cgo"}
		}
		if errorNumber != 0 {
			return php.Bool(false), true, nil
		}
		return php.String(resolved), true, nil

	case "realpath_cache_size":
		return php.Int(unit.realpathCacheSize), true, nil

	case "realpath_cache_get":
		return unit.realpathCacheValue(), true, nil

	case "php2go\\internal\\realpath_cache":
		if len(args) < 1 || args[0].Deref().String() != "clear" {
			return php.Undef(), true, Raise(
				"Error", "php2go internal realpath_cache received an invalid operation")
		}
		path := ""
		if len(args) > 1 {
			path = args[1].Deref().String()
		}
		selective := len(args) > 2 && args[2].Deref().Bool()
		unit.clearRealpathCache(path, selective)
		return php.Null(), true, nil

	case "getcwd":
		if resolver, ok := in.loader.(WorkingDirectoryResolver); ok {
			if directory, virtual := resolver.WorkingDirectory(); virtual {
				return php.String(directory), true, nil
			}
		}
		directory, err := os.Getwd()
		if err != nil {
			return php.Bool(false), true, nil
		}
		return php.String(directory), true, nil

	case "chdir":
		directory := args[0].Deref().String()
		if strings.IndexByte(directory, 0) >= 0 {
			return php.Undef(), true, Raise(
				"ValueError",
				"chdir(): Argument #1 ($directory) must not contain any null bytes",
			)
		}
		warn := func(errorNumber int) (php.Value, bool, error) {
			_, message := stdlib.SocketErrno(syscall.Errno(errorNumber))
			ctx.Warn("chdir(): %s (errno %d)", message, errorNumber)
			return php.Bool(false), true, nil
		}
		if resolver, ok := in.loader.(WorkingDirectoryResolver); ok {
			if errorNumber, handled := resolver.ChangeWorkingDirectory(directory); handled {
				if errorNumber != 0 {
					return warn(errorNumber)
				}
				return php.Bool(true), true, nil
			}
		}
		errorNumber, available := libc.Chdir(directory)
		if !available {
			return php.Undef(), true, &UnsupportedError{Opcode: "libc chdir without cgo"}
		}
		if errorNumber != 0 {
			return warn(errorNumber)
		}
		if resolver, ok := in.loader.(WorkingDirectoryResolver); ok {
			resolver.UseHostWorkingDirectory()
		}
		return php.Bool(true), true, nil

	case "setlocale":
		category := int(args[0].Deref().Int())
		attempt := func(candidate php.Value) (php.Value, bool, error) {
			rendered, err := in.toString(ctx, unit, candidate)
			if err != nil {
				return php.Undef(), false, err
			}
			if pending := ctx.TakePendingError(); pending != nil {
				return php.Undef(), false, pending
			}
			locale := rendered.String()
			if len(locale) >= 255 {
				ctx.Warn("setlocale(): Specified locale name is too long")
				if pending := ctx.TakePendingError(); pending != nil {
					return php.Undef(), false, pending
				}
				return php.Bool(false), false, nil
			}
			result, ok, available := libc.SetLocale(category, locale, locale == "0")
			if !available {
				return php.Undef(), false, &UnsupportedError{Opcode: "libc setlocale without cgo"}
			}
			if !ok {
				return php.Bool(false), false, nil
			}
			return php.String(result), true, nil
		}
		for _, candidate := range args[1:] {
			candidate = candidate.Deref()
			if candidate.Kind() != php.KindArray {
				value, succeeded, err := attempt(candidate)
				if err != nil || succeeded {
					return value, true, err
				}
				continue
			}
			value := php.Bool(false)
			succeeded := false
			var attemptErr error
			candidate.Array().Each(func(_ php.Key, item php.Value) bool {
				value, succeeded, attemptErr = attempt(item.Deref())
				return attemptErr == nil && !succeeded
			})
			if attemptErr != nil || succeeded {
				return value, true, attemptErr
			}
		}
		return php.Bool(false), true, nil

	case "php2go\\internal\\register_superglobal":
		if len(args) == 0 {
			return php.Undef(), true,
				fmt.Errorf("php2go internal register_superglobal requires a name")
		}
		name := args[0].Deref().String()
		if name == "" || name == "GLOBALS" {
			return php.Undef(), true,
				fmt.Errorf("php2go internal register_superglobal received an invalid name")
		}
		initial := php.Null()
		if len(args) > 1 {
			initial = args[1].Deref().Copy()
		}
		// First wins: a host or application initializer that defined the
		// name before the library's defaults keeps its value.
		if !ctx.DefineSuperglobal(name, initial) {
			return php.Bool(false), true, nil
		}
		// Registration can happen from the function that immediately wants to
		// use the new superglobal. Rebind all live frames, not only future ones.
		for _, frame := range unit.frames {
			in.bindRegisteredSuperglobals(ctx, frame)
		}
		return php.Bool(true), true, nil

	case "class_exists", "interface_exists", "enum_exists", "trait_exists":
		if len(args) == 0 {
			return php.Bool(false), true, nil
		}
		// The second argument asks whether to autoload, defaulting to true.
		autoload := len(args) < 2 || args[1].Bool()
		class, ok := unit.findClass(args[0].String())
		if ok && !unit.classDeclared(in, args[0].String()) {
			// The harvest carries a class whose declaration has not run yet;
			// the program cannot see it, and asking may still autoload.
			class, ok = nil, false
		}
		if !ok && autoload {
			var err error
			if class, ok, err = in.autoload(ctx, unit, args[0].String(), depth); err != nil {
				return php.Undef(), true, err
			}
		}
		if !ok {
			return php.Bool(false), true, nil
		}
		// The four spellings differ only in which kind they accept.
		kind := class.decl.GetKind().String()
		switch name {
		case "interface_exists":
			return php.Bool(kind == "KIND_INTERFACE"), true, nil
		case "enum_exists":
			return php.Bool(kind == "KIND_ENUM"), true, nil
		case "trait_exists":
			return php.Bool(kind == "KIND_TRAIT"), true, nil
		}
		return php.Bool(kind == "KIND_CLASS" || kind == "KIND_ENUM"), true, nil

	case "function_exists":
		if len(args) == 0 {
			return php.Bool(false), true, nil
		}
		lower := functionKey(args[0].String())
		_, declared := unit.functions[lower]
		_, builtin := builtins[lower]
		if !declared && !builtin {
			// The build may provide it without this runtime implementing it;
			// function_exists asks about the build.
			declared = in.engineFunctions[lower]
		}
		return php.Bool(declared || builtin), true, nil

	case "method_exists":
		if len(args) < 2 {
			return php.Bool(false), true, nil
		}
		className := args[0].String()
		viaObject := args[0].Kind() == php.KindObject
		if viaObject {
			className = args[0].Object().ClassName
		}
		class, ok := unit.findClass(className)
		if !ok {
			return php.Bool(false), true, nil
		}
		if unit.hasMethod(class, args[1].String(), viaObject) {
			return php.Bool(true), true, nil
		}
		if viaObject {
			// A wrapping iterator answers for what it wraps: the engine's
			// method lookup falls through to the inner iterator.
			if held, ok := asCallbackFilter(args[0].Object()); ok {
				_, found := in.innerPassthrough(unit, held.inner, args[1].String())
				return php.Bool(found), true, nil
			}
		}
		return php.Bool(false), true, nil

	case "property_exists":
		if len(args) < 2 {
			return php.Bool(false), true, nil
		}
		if args[0].Kind() == php.KindObject {
			if _, ok := args[0].Object().Get(args[1].String()); ok {
				return php.Bool(true), true, nil
			}
		}
		className := args[0].String()
		if args[0].Kind() == php.KindObject {
			className = args[0].Object().ClassName
		}
		class, ok := unit.findClass(className)
		if !ok {
			return php.Bool(false), true, nil
		}
		for current := class; current != nil; {
			for _, prop := range unit.properties(current) {
				if prop.GetName() == args[1].String() {
					return php.Bool(true), true, nil
				}
			}
			if current.Parent == "" {
				break
			}
			next, found := unit.findClass(current.Parent)
			if !found {
				break
			}
			current = next
		}
		return php.Bool(false), true, nil

	case "class_alias":
		if len(args) < 2 {
			return php.Bool(false), true, nil
		}
		// A single underscore is reserved for a future language use, and
		// aliasing to it is on its way out.
		if args[1].String() == "_" {
			ctx.Deprecated(`Using "_" as a class alias is deprecated since 8.4`)
		}
		// A reserved word cannot be a class name, and the alias is refused
		// before the class being aliased is even looked up.
		if reservedClassName(args[1].String()) {
			return php.Undef(), true, in.fatal(ctx,
				"Cannot use %q as a class alias as it is reserved", args[1].String())
		}
		class, ok := unit.findClass(args[0].String())
		if !ok {
			// The engine raises this one through zend_error rather than
			// zend_argument_error, so it carries no `class_alias():` prefix --
			// unlike most of a builtin's own diagnostics.
			ctx.Warn("Class \"%s\" not found", args[0].String())
			return php.Bool(false), true, nil
		}
		// The second name must be free. PHP warns rather than throwing here,
		// and names where the existing class was declared.
		if existing, taken := unit.findClass(args[1].String()); taken {
			// The kind named is the one being aliased, not the one already
			// holding the name.
			ctx.Warn("Cannot redeclare %s %s (previously declared in %s:%d)",
				classKindWord(class.decl.GetKind()), php.DisplayName(args[1].String()),
				existing.decl.GetFilename(), existing.decl.GetLineStart())
			return php.Bool(false), true, nil
		}
		// An alias is the same class under a second name, so the entry is
		// shared rather than copied -- static properties and enum cases must
		// not split in two.
		alias := strings.ToLower(args[1].String())
		unit.classes[alias] = class
		// get_declared_classes and its siblings list an alias too, under the
		// name the table holds it by -- which is the lowercased one.
		unit.classOrder = append(unit.classOrder, alias)
		return php.Bool(true), true, nil

	case "get_called_class":
		// `static::` resolved to a name. Outside a class there is nothing for
		// it to mean, and the engine says so rather than answering false.
		if called := ctx.CalledScope(); called != "" {
			if class, ok := unit.findClass(called); ok {
				return php.String(class.Name), true, nil
			}
			return php.String(called), true, nil
		}
		return php.Undef(), true, Raise("Error",
			"get_called_class() must be called from within a class")

	case "get_parent_class":
		var className string
		if len(args) == 0 {
			// No argument means the class the call is inside -- which PHP has
			// deprecated, since it reads a scope the call site cannot see.
			ctx.Deprecated("Calling get_parent_class() without arguments is deprecated")
			className = ctx.Scope()
		} else if object := args[0].Deref().Object(); object != nil {
			className = object.ClassName
		} else {
			// Anything else has to name a class that exists. The declared type
			// admits null and any string, so this refusal is the function's own
			// rather than the signature's -- including for null, which is the
			// default the signature gives a parameter this function will not
			// accept it for.
			className = args[0].Deref().String()
			if _, known := unit.findClass(className); !known ||
				args[0].Deref().Kind() != php.KindString {
				return php.Undef(), true, Raise("TypeError",
					"get_parent_class(): Argument #1 ($object_or_class) must be "+
						"an object or a valid class name, %s given",
					givenTypeName(args[0].Deref()))
			}
		}
		class, ok := unit.findClass(className)
		if !ok || class.Parent == "" {
			return php.Bool(false), true, nil
		}
		parent, ok := unit.findClass(class.Parent)
		if !ok {
			return php.String(class.Parent), true, nil
		}
		return php.String(parent.Name), true, nil

	case "is_callable":
		if len(args) == 0 {
			return php.Bool(false), true, nil
		}
		if _, ok := asClosure(args[0]); ok {
			return php.Bool(true), true, nil
		}
		// A plain name resolves through dynamicTarget whether or not the
		// function exists -- that check is deferred to the call. is_callable
		// has to answer now, so the tables are consulted directly.
		if args[0].Kind() == php.KindString {
			name := strings.ToLower(args[0].String())
			if class, method, isStatic := strings.Cut(name, "::"); isStatic {
				return php.Bool(unit.namedMethodCallable(ctx, class, method, false)), true, nil
			}
			key := functionKey(name)
			_, declared := unit.functions[key]
			_, builtin := builtins[key]
			return php.Bool(declared || builtin), true, nil
		}
		// The two-element array form names either an object and a method or a
		// class and a static one, and the visibility rules differ between them
		// only in which magic forwarder can answer.
		if args[0].Kind() == php.KindArray {
			items := args[0].Array()
			if items.Len() != 2 {
				return php.Bool(false), true, nil
			}
			target, _ := items.Get(php.IntKey(0))
			method, _ := items.Get(php.IntKey(1))
			target = target.Deref()
			if method.Deref().Kind() != php.KindString {
				return php.Bool(false), true, nil
			}
			if strings.Contains(method.Deref().String(), "::") {
				// The compound form -- `["B", "parent::foo"]` -- has scope
				// rules of its own, and reporting that it is deprecated is
				// part of answering the question.
				_, resolvable := unit.dynamicTarget(ctx, args[0])
				return php.Bool(resolvable), true, nil
			}
			switch target.Kind() {
			case php.KindString:
				return php.Bool(unit.namedMethodCallable(ctx, target.String(), method.Deref().String(), false)), true, nil
			case php.KindObject:
				class, known := unit.findClass(target.Object().ClassName)
				if !known {
					return php.Bool(false), true, nil
				}
				return php.Bool(unit.methodCallable(ctx, class, method.Deref().String(), true)), true, nil
			}
			return php.Bool(false), true, nil
		}
		_, resolvable := unit.dynamicTarget(ctx, args[0])
		return php.Bool(resolvable), true, nil

	case "get_class_methods":
		var className string
		if len(args) > 0 {
			className = args[0].String()
			if args[0].Kind() == php.KindObject {
				className = args[0].Object().ClassName
			}
		}
		class, ok := unit.findClass(className)
		if !ok {
			return php.Bool(false), true, nil
		}
		names := php.NewArray()
		seen := map[string]bool{}
		// get_class_methods lists what the *calling scope* can reach, not
		// everything the class has: from outside, the public methods only.
		scope := unit.callingScope(ctx)
		for current := class; current != nil; {
			for _, method := range current.decl.GetMethods() {
				key := strings.ToLower(method.GetName())
				if seen[key] {
					continue
				}
				seen[key] = true
				if unit.methodAccessibleAs(method, current, scope, method.GetName()) {
					names.Append(php.String(method.GetName()))
				}
			}
			// A class the engine supplies has no op_arrays, only names.
			for _, method := range current.decl.GetEngineMethods() {
				key := strings.ToLower(method.GetName())
				if seen[key] {
					continue
				}
				seen[key] = true
				// get_class_methods lists only what the calling scope can
				// reach; Closure::__construct is private and never appears.
				if method.GetVisibility() == "public" || method.GetVisibility() == "" ||
					scope != nil {
					names.Append(php.String(method.GetName()))
				}
			}
			// A trait's methods and the aliases the `use` clause gave them
			// belong to the using class. The order is the engine's: a class's
			// own aliases sit right after its own methods, and each trait's
			// aliases right after that trait's -- which is why
			// `class A { use T { m as n; } }` lists n before m, while a class
			// composing a trait that aliases lists the trait's m first.
			for _, holder := range unit.traitComposition(current) {
				if holder != current {
					for _, method := range holder.decl.GetMethods() {
						key := strings.ToLower(method.GetName())
						if seen[key] {
							continue
						}
						seen[key] = true
						// A trait's method belongs to the using class, so that
						// is the scope its visibility is measured against.
						if unit.methodAccessibleAs(method, current, scope, method.GetName()) {
							names.Append(php.String(method.GetName()))
						}
					}
				}
				for _, alias := range holder.decl.GetTraitAliases() {
					aliasName := alias.GetAlias()
					if aliasName == "" {
						continue
					}
					key := strings.ToLower(aliasName)
					if seen[key] {
						continue
					}
					method, found := unit.aliasedTraitMethod(holder, key)
					if !found {
						continue
					}
					seen[key] = true
					if unit.methodAccessibleAs(method, current, scope, aliasName) {
						names.Append(php.String(aliasName))
					}
				}
			}
			// **And then the interfaces'.** A class that has not implemented an
			// interface method -- an abstract one -- still lists the name, and
			// a class that has declared it already saw it among its own. Read
			// back from the engine: `abstract class A implements I` lists `im`
			// last, after its own methods and its traits'.
			for _, name := range current.decl.GetInterfaces() {
				contract, found := unit.findClass(name)
				if !found {
					continue
				}
				for _, method := range contract.decl.GetMethods() {
					key := strings.ToLower(method.GetName())
					if seen[key] {
						continue
					}
					seen[key] = true
					if unit.methodAccessibleAs(method, current, scope, method.GetName()) {
						names.Append(php.String(method.GetName()))
					}
				}
			}
			if current.Parent == "" {
				break
			}
			next, found := unit.findClass(current.Parent)
			if !found {
				break
			}
			current = next
		}
		return php.Arr(names), true, nil

	case "get_object_vars":
		if len(args) == 0 || args[0].Kind() != php.KindObject {
			return php.Bool(false), true, nil
		}
		// Reading an object's properties is what produces a lazy one's state.
		subject, err := in.materializeLazy(ctx, unit, args[0], depth)
		if err != nil {
			return php.Undef(), true, err
		}
		// Visibility is decided against the class whose body made the call,
		// which the stack frame pushed for this very call recorded.
		props, err := in.hookedVisibleProps(ctx, unit, nil, subject.Object(),
			unit.callingScope(ctx), depth, false)
		if err != nil {
			return php.Undef(), true, err
		}
		return php.Arr(props), true, nil

	case "gc_enabled":
		return php.Bool(unit.setting("zend.enable_gc") != "0"), true, nil
	case "gc_enable":
		unit.setSetting("zend.enable_gc", "1")
		return php.Null(), true, nil
	case "gc_disable":
		// php2go collects cycles only when gc_collect_cycles() asks, so there
		// is no automatic collector for this to switch off. The setting is
		// still recorded, because a program can read it back.
		unit.setSetting("zend.enable_gc", "0")
		return php.Null(), true, nil

	case "ini_get_all":
		if !unit.iniRegistryAvailable {
			return php.Undef(), true, &UnsupportedError{Opcode: "manifest without INI registry"}
		}
		extension := ""
		if len(args) > 0 && args[0].Deref().Kind() != php.KindNull {
			extension = args[0].Deref().String()
			if !unit.extensions[extension] {
				ctx.Warn("ini_get_all(): Extension \"%s\" cannot be found", extension)
				return php.Bool(false), true, nil
			}
		}
		details := true
		if len(args) > 1 {
			details = args[1].Deref().Bool()
		}
		result := php.NewArray()
		for _, entry := range unit.iniEntries {
			if extension != "" && entry.GetExtension() != extension {
				continue
			}
			local := php.Null()
			if entry.LocalValue != nil {
				local = php.String(entry.GetLocalValue())
			}
			if value, changed := unit.ini[entry.GetName()]; changed {
				local = php.String(value)
			}
			if !details {
				result.Set(php.StringKey(entry.GetName()), local)
				continue
			}
			option := php.NewArray()
			global := php.Null()
			if entry.GlobalValue != nil {
				global = php.String(entry.GetGlobalValue())
			}
			option.Set(php.StringKey("global_value"), global)
			option.Set(php.StringKey("local_value"), local)
			option.Set(php.StringKey("access"), php.Int(int64(entry.GetAccess())))
			result.Set(php.StringKey(entry.GetName()), php.Arr(option))
		}
		return php.Arr(result), true, nil

	case "openlog":
		acquireSyslog(unit)
		if !libc.SyslogOpen(
			args[0].Deref().String(), args[1].Deref().Int(), args[2].Deref().Int(),
		) {
			releaseSyslog(unit)
			return php.Undef(), true, &UnsupportedError{Opcode: "libc syslog without cgo"}
		}
		return php.Bool(true), true, nil

	case "syslog":
		acquireSyslog(unit)
		priority := args[0].Deref().Int()
		for _, message := range syslogSegments(args[1].Deref().String(), unit.setting("syslog.filter")) {
			if !libc.SyslogWrite(priority, message) {
				libc.SyslogClose()
				releaseSyslog(unit)
				return php.Undef(), true, &UnsupportedError{Opcode: "libc syslog without cgo"}
			}
		}
		return php.Bool(true), true, nil

	case "closelog":
		acquireSyslog(unit)
		if !libc.SyslogClose() {
			releaseSyslog(unit)
			return php.Undef(), true, &UnsupportedError{Opcode: "libc syslog without cgo"}
		}
		releaseSyslog(unit)
		return php.Bool(true), true, nil

	case "proc_nice":
		errorNumber, available := libc.Nice(args[0].Deref().Int())
		if !available {
			return php.Undef(), true, &UnsupportedError{Opcode: "libc nice without cgo"}
		}
		if errorNumber != 0 {
			ctx.Warn("proc_nice(): Only a super user may attempt to increase the priority of a process")
			return php.Bool(false), true, nil
		}
		return php.Bool(true), true, nil

	case "php2go\\internal\\mail_send":
		payload := args[0].Deref().String()
		extra := args[1].Deref().String()
		path := unit.setting("sendmail_path")
		command := path
		if extra != "" {
			command += " " + extra
		}
		status, errorNumber, started, available := libc.MailCommand(command, payload)
		if !available {
			return php.Undef(), true, &UnsupportedError{Opcode: "libc mail popen without cgo"}
		}
		result := php.NewArray()
		result.Append(php.Bool(started))
		result.Append(php.Int(int64(status)))
		result.Append(php.String(path))
		result.Append(php.Int(int64(errorNumber)))
		return php.Arr(result), true, nil

	case "php2go\\internal\\sapi_log":
		_, _ = fmt.Fprintln(os.Stderr, args[0].Deref().String())
		return php.Bool(true), true, nil

	case "shell_exec", "exec", "system", "passthru":
		command := args[0].Deref().String()
		if err := validateShellCommand(name, command); err != nil {
			return php.Undef(), true, err
		}

		// exec initializes a writable scalar output argument to an array even
		// when the command emits nothing. An existing array is appended to, and
		// is cloned before mutation to preserve PHP array value semantics.
		var resultArray *php.Array
		if name == "exec" && len(args) > 1 {
			if ref, writable := args[1].RefOf(); writable {
				if current := ref.Get(); current.Kind() == php.KindArray {
					resultArray = current.Array().Clone()
				} else {
					resultArray = php.NewArray()
				}
				ref.Set(php.Arr(resultArray))
			}
		}

		output, status, _, started, available := libc.ShellCommand(command)
		if !available {
			return php.Undef(), true, &UnsupportedError{Opcode: "libc popen without cgo"}
		}
		if !started {
			if name == "shell_exec" {
				ctx.Warn("shell_exec(): Unable to execute '%s'", command)
			} else {
				ctx.Warn("%s(): Unable to fork [%s]", name, command)
			}
			return php.Bool(false), true, nil
		}

		if name != "shell_exec" {
			statusIndex := 1
			if name == "exec" {
				statusIndex = 2
			}
			if len(args) > statusIndex {
				if ref, writable := args[statusIndex].RefOf(); writable {
					ref.Set(php.Int(int64(status)))
				}
			}
		}

		switch name {
		case "shell_exec":
			if output == "" {
				return php.Null(), true, nil
			}
			return php.String(output), true, nil
		case "passthru":
			for start := 0; start < len(output); start += 4096 {
				end := min(start+4096, len(output))
				ctx.Write(output[start:end])
				if pending := ctx.TakePendingError(); pending != nil {
					return php.Undef(), true, pending
				}
			}
			return php.Null(), true, nil
		}

		last := ""
		err := processOutputLines(output, func(line string) error {
			last = execLine(line)
			if name == "exec" {
				if resultArray != nil {
					resultArray.Append(php.String(last))
				}
				return nil
			}
			ctx.Write(line)
			return ctx.TakePendingError()
		})
		if err != nil {
			return php.Undef(), true, err
		}
		return php.String(last), true, nil

	case "php2go\\internal\\ini_restore":
		if len(args) == 0 {
			return php.Null(), true, nil
		}
		name := args[0].Deref().String()
		switch name {
		case "error_reporting":
			ctx.SetErrorReporting(in.defaultErrorReporting)
		case "include_path":
			// The linked embed build leaves its runtime include_path override in
			// place when ini_restore() is called.
		default:
			if _, modelled := stdlib.IniModelled(name); modelled {
				delete(unit.ini, name)
			}
		}
		return php.Null(), true, nil

	case "php2go\\internal\\output_rewriter":
		if len(args) == 0 {
			return php.Undef(), true, Raise("Error", "php2go internal output_rewriter requires an operation")
		}
		if unit.outputRewriter == nil {
			unit.outputRewriter = &outputRewriter{unit: unit, ctx: ctx}
		}
		switch args[0].Deref().String() {
		case "add":
			if len(args) < 5 {
				return php.Undef(), true, Raise("Error", "php2go internal output_rewriter add requires encoded names and values")
			}
			state := unit.outputRewriter
			state.add(
				args[1].Deref().String(), args[2].Deref().String(),
				args[3].Deref().String(), args[4].Deref().String(),
			)
			if !state.active {
				ctx.PushInternalBufferConfigured(state, "URL-Rewriter", 0, php.OutputHandlerStdFlags)
				state.active = true
			}
			return php.Bool(true), true, nil
		case "reset":
			unit.outputRewriter.reset()
			return php.Bool(true), true, nil
		default:
			return php.Undef(), true, Raise("Error", "php2go internal output_rewriter received an invalid operation")
		}

	case "var_export":
		// A trigger: var_export writes the state, so there has to be one.
		if len(args) == 0 {
			return php.Null(), true, nil
		}
		resolved, err := in.materializeLazy(ctx, unit, args[0], depth)
		if err != nil {
			return php.Undef(), true, err
		}
		// A proxy exports the state of what it forwards to, not the empty
		// wrapper, so the resolved object replaces the argument.
		//
		// The rendering is in runtime/stdlib so compiled code has it too; what
		// stays here is producing the state and the hook that runs a get hook,
		// which var_export does -- and which includes a virtual property that
		// has no slot to read.
		args[0] = resolved
		ctx.SetExportProperties(in.exportProperties(unit, depth))
		fn, _ := php.LookupBuiltin("var_export")
		value, err := fn(ctx, args)
		return value, true, in.materialize(ctx, unit, err)

	case "gc_collect_cycles":
		// PHP frees the objects a cycle keeps alive here, and a destructor that
		// runs as a result is observable. php2go answers the same question by
		// walking the roots rather than by counting references.
		return php.Int(int64(in.collect(ctx, unit))), true, nil

	case "get_class_vars":
		if len(args) == 0 {
			return php.Null(), true, nil
		}
		class, ok := unit.findClass(args[0].Deref().String())
		if !ok {
			return php.Undef(), true, in.materialize(ctx, unit, Raise("TypeError",
				"get_class_vars(): Argument #1 ($class) must be a valid class name, %s given",
				args[0].Deref().String()))
		}
		vars := php.NewArray()
		for _, prop := range unit.visibleProperties(class, unit.callingScope(ctx)) {
			if prop.GetVirtual() {
				// A property whose hooks give it no backing store is not a
				// variable of the class: there is nothing for get_class_vars
				// to report a value for. A *backed* hooked property is listed,
				// and listed with its declared default rather than what the
				// get hook would answer -- no instance exists to run one.
				continue
			}
			value := php.Null()
			if prop.GetStatic() {
				// **What the class declares, not what the program has
				// written.** Measured: after `A::$s = "changed"`,
				// `get_class_vars("A")` still answers `"S"` -- the engine reads
				// the *default* static table, and reading the live slot
				// reported the assignment.
				value = unit.declaredStaticDefault(class, prop)
			} else if prop.GetHasDefault() {
				value = literalValue(prop.GetDefaultValue())
			}
			vars.Set(php.StringKey(prop.GetName()), value)
		}
		return php.Arr(vars), true, nil

	case "var_dump":
		// The rendering lives in runtime/php so that compiled code and the
		// interpreter share one output format; what stays here is the part that
		// needs a class table, installed as the hook. See runtime/php/dump.go.
		ctx.SetObjectProperties(newDumper(in, unit, ctx).properties)
		var rendered strings.Builder
		for _, arg := range args {
			text, err := php.VarDump(ctx, arg)
			if err != nil {
				// A `__debugInfo` that did not return an array has already
				// written its fatal and is ending the request; anything else
				// travels as itself.
				return php.Undef(), true, err
			}
			rendered.WriteString(text)
		}
		ctx.Write(rendered.String())
		return php.Null(), true, nil

	case "debug_zval_dump":
		// The rendering is in runtime/stdlib so compiled code has it too. What
		// stays here is the hook that needs a class table, and the
		// `__debugInfo` fatal -- which ends the request, and which neither
		// runtime/php nor runtime/stdlib can say.
		ctx.SetObjectProperties(newDumper(in, unit, ctx).properties)
		fn, _ := php.LookupBuiltin("debug_zval_dump")
		value, err := fn(ctx, args)
		return value, true, err

	case "count", "sizeof":
		if len(args) > 0 {
			if counted, handled, err := in.countOf(ctx, unit, args[0], depth); handled {
				return counted, true, in.materialize(ctx, unit, err)
			}
		}
		// Anything else is the plain builtin's business.
		return php.Undef(), false, nil

	case "assert":
		// The body is in runtime/stdlib so compiled code has it too; the hook
		// for invoking the callback is the one thing it needs from here.
		in.installCallValue(ctx, unit, depth)
		fn, _ := php.LookupBuiltin("assert")
		value, err := fn(ctx, args)
		return value, true, in.materialize(ctx, unit, err)

	case "register_shutdown_function":
		if len(args) == 0 {
			return php.Null(), true, nil
		}
		// The engine resolves the callable when it validates the argument, not
		// when it runs it: a shutdown function outlives the scope that named
		// it, so `["self", "m"]` has to be settled here or `self` means
		// nothing by the time it is called.
		// The registry moved to the Ctx; what stays here is settleCallable,
		// which needs a class table. See runtime/php/callable.go.
		ctx.RegisterShutdown(unit.settleCallable(ctx, args[0]), args[1:])
		return php.Null(), true, nil

	}
	return php.Undef(), false, nil
}

// settleCallable is what the engine's `callable` parameter parsing leaves
// behind: a callable resolved *where it was written*, not where it will be
// called. Two things have to be settled, and a stored callback outlives both --
// the scope a keyword names, and the instance the caller lends an instance
// method.
func (u *Unit) settleCallable(ctx *php.Ctx, callee php.Value) php.Value {
	resolved := u.resolveCallableScope(ctx, callee)
	switch resolved.Deref().Kind() {
	case php.KindString, php.KindArray:
	default:
		return resolved
	}
	// Quiet: resolveCallableScope has already reported whatever the *form*
	// earns, and there are no keywords left in what it handed back.
	target, ok := u.dynamicTargetQuiet(ctx, resolved.Deref(), true, true)
	if !ok {
		return resolved
	}
	u.forwardThis(ctx, target)
	if target.this == nil || target.method == nil {
		return resolved
	}
	bound := php.NewArray()
	bound.Append(php.Obj(target.this))
	bound.Append(php.String(target.method.GetName()))
	return php.Arr(bound)
}

// resolveCallableScope rewrites `self`, `parent` or `static` in a callable to
// the class the keyword means where it was written. Everything else is handed
// back untouched, including a callable the keyword does not resolve in: that is
// not a callable at all, and the engine says nothing about one it does not
// have.
func (u *Unit) resolveCallableScope(ctx *php.Ctx, callee php.Value) php.Value {
	value := callee.Deref()
	switch value.Kind() {
	case php.KindString:
		class, method, compound := strings.Cut(value.String(), "::")
		if !compound {
			return callee
		}
		resolved, ok := u.callableClassQuiet(ctx, class, false, true)
		if !ok || resolved == class {
			return callee
		}
		return php.String(resolved + "::" + method)
	case php.KindArray:
		items := value.Array()
		if items.Len() != 2 {
			return callee
		}
		first, _ := items.Get(php.IntKey(0))
		if first.Deref().Kind() != php.KindString {
			return callee
		}
		named := first.Deref().String()
		resolved, ok := u.callableClassQuiet(ctx, named, false, true)
		if !ok || resolved == named {
			return callee
		}
		method, _ := items.Get(php.IntKey(1))
		rebuilt := php.NewArray()
		rebuilt.Append(php.String(resolved))
		rebuilt.Append(method.Deref().Copy())
		return php.Arr(rebuilt)
	}
	return callee
}

// runShutdown calls what register_shutdown_function collected, in registration
// order. PHP runs these after the script body, including after a fatal error,
// so a failure in one does not stop the rest.
func (in *Interp) runShutdown(ctx *php.Ctx, unit *Unit) error {
	// The list is walked by index because a shutdown function may register
	// another one, which PHP also runs.
	for i := 0; i < len(ctx.ShutdownCalls()); i++ {
		call := ctx.ShutdownCalls()[i]
		target, ok := unit.dynamicTarget(ctx, call.Callee)
		if !ok {
			continue
		}
		target.args = call.Args
		in.canonicalCallName(target)
		// The body has finished, so anything the *call itself* raises -- a
		// builtin's arity check, for one -- has no script location. The engine
		// spells that `[no active file]` where an exception names its file;
		// the callee's own frame sets real locations as soon as it starts.
		ctx.SetLocation("[no active file]", 0)
		// The engine calls these from C, so a trace taken inside one shows it
		// as a frame with no file and no line.
		ctx.PushCall(php.StackFrame{
			Function: target.traceName(), File: php.InternalFrameFile,
			Args: target.args, This: target.this,
		})
		_, err := in.dispatch(ctx, unit, target, 0)
		ctx.PopCall()
		if err != nil {
			// exit() inside a shutdown function ends the shutdown sequence:
			// the ones after it never run, though destructors still do.
			if _, exited := asExit(err); exited {
				ctx.ClearShutdown()
				return nil
			}
			if escaped, isThrown := asThrown(err); isThrown {
				// A shutdown function's exception reaches the installed
				// handler like any other. Only one nothing handles is fatal --
				// and that one *stops* the sequence, where a handled one lets
				// the remaining functions run.
				if in.callExceptionHandler(ctx, unit, escaped) {
					continue
				}
				in.settleMessages(ctx, unit, escaped)
				ctx.Write(escaped.fatalErrorText())
				ctx.ClearShutdown()
				return nil
			}
			// Anything else is this runtime's own failure -- an unsupported
			// opcode, or the step limit. Swallowing it here would leave a
			// shutdown function that simply stopped part-way through, which
			// reads as wrong *output* rather than as the gap it is.
			ctx.ClearShutdown()
			return err
		}
	}
	ctx.ClearShutdown()
	return nil
}

// reservedClassName lists the words the parser will not accept as a class name.
// They are the type names the language itself uses, which a class may not
// shadow -- and class_alias goes through the same rule at run time.
func reservedClassName(name string) bool {
	switch strings.ToLower(strings.TrimPrefix(name, "\\")) {
	case "bool", "false", "float", "int", "null", "parent", "self", "static",
		"string", "true", "void", "iterable", "object", "mixed", "never":
		return true
	}
	return false
}
