package interp

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// Loader compiles PHP source that was not known when the program was harvested.
//
// include, require and eval all need a compiler at run time, which the
// interpreter deliberately does not contain: it executes IR and knows nothing
// about PHP syntax. Whoever embeds it supplies this -- the test harness backs it
// with the real engine, and a generated binary would either bundle a parser or
// leave it nil and reject the construct honestly.
type Loader interface {
	// CompileFile compiles a file that include or require named.
	CompileFile(path string) (*pb.Unit, error)
	// CompileString compiles the argument of eval, which arrives without an
	// opening tag.
	CompileString(code string) (*pb.Unit, error)
}

// FileResolver lets a loader provide a filesystem other than the host's.
//
// The default loader compiles ordinary files and therefore needs no resolver:
// includeOrEval retains PHP's existing host-path lookup for it. A generated
// binary, on the other hand, keeps its files in an embed.FS. Its loader
// implements FileResolver so resolution, _once identity and compilation all
// use the same canonical virtual path.
type FileResolver interface {
	ResolveFile(requested, from string) (canonical string, found bool)
}

// WorkingDirectoryResolver lets a loader own a working directory in a
// filesystem that the host process cannot enter. Absolute paths outside that
// filesystem and relative paths while it is using the host cwd fall through to
// libc; handled failures remain inside the loader-owned namespace.
type WorkingDirectoryResolver interface {
	WorkingDirectory() (directory string, virtual bool)
	ChangeWorkingDirectory(requested string) (errorNumber int, handled bool)
	UseHostWorkingDirectory()
}

// PathResolver lets a loader resolve a path without compiling or opening it.
// handled distinguishes a missing path in the loader-owned namespace from a
// path that should fall through to the host filesystem.
type PathResolver interface {
	ResolvePath(requested string) (canonical string, found bool, handled bool)
}

// PathStatus is the least filesystem information needed by PHP's path
// predicates. Loaders can report virtual files without exposing full stat data.
type PathStatus struct {
	Exists, File, Dir, Link        bool
	Readable, Writable, Executable bool
}

// PathInspector lets a loader answer type and access predicates for its own
// namespace. handled distinguishes an absent loader-owned path from one whose
// status should fall through to the host filesystem.
type PathInspector interface {
	InspectPath(requested string) (status PathStatus, handled bool)
}

// PathStat is the raw, platform-width-independent metadata used by PHP's stat
// family. Loaders supply it for virtual paths without exposing an os.FileInfo.
type PathStat struct {
	Dev, Ino, Mode, NLink, UID, GID, RDev int64
	Size, ATime, MTime, CTime             int64
	BlockSize, Blocks                     int64
}

// PathStatInspector lets a loader expose stable metadata for its own namespace.
// found distinguishes a missing owned path; handled controls host fallback.
type PathStatInspector interface {
	StatPath(requested string, follow bool) (stat PathStat, found, handled bool)
}

// ScopedFunctionParameter is the call metadata the interpreter needs when a
// generated Go function is called from a newly compiled eval body.
type ScopedFunctionParameter struct {
	Name            string
	ByRef, Variadic bool
}

// ScopedFunctionSignature preserves parameter names and the required prefix
// for named calls. Bodies with by-reference parameters or named variadic
// extras are conservatively refused at the call site.
type ScopedFunctionSignature struct {
	Parameters []ScopedFunctionParameter
	Required   int
}

// DirectoryReader lets a loader enumerate directories in its owned namespace.
// handled distinguishes a loader-owned failure from a path that should fall
// through to the host filesystem. Names include the wrapper-visible dot entries.
type DirectoryReader interface {
	ReadDirectory(requested string) (names []string, err error, handled bool)
}

// SourceReader lets a loader expose exact bytes from a non-host filesystem.
// It is optional: ordinary engine-backed loaders keep using the host, while a
// static bundle can serve its checksum-validated embedded source payloads.
type SourceReader interface {
	ReadSource(path string) ([]byte, error)
}

// SetLoader installs the compiler used for include, require and eval.
func (in *Interp) SetLoader(loader Loader) { in.loader = loader }

// CompileEvalFailure turns Zend's rejected eval string into the PHP exception
// representation understood by generated Go catch blocks.
func (in *Interp) CompileEvalFailure(ctx *php.Ctx, err error) error {
	site, line := ctx.Location()
	name := fmt.Sprintf("%s(%d) : eval()'d code", site, line)
	return in.compileScopedFailure(ctx, err, name)
}

// CompileFileFailure reports a runtime include's parser exception at the
// included file's own location.
func (in *Interp) CompileFileFailure(ctx *php.Ctx, err error, path string) error {
	return in.compileScopedFailure(ctx, err, path)
}

func (in *Interp) compileScopedFailure(ctx *php.Ctx, err error, name string) error {
	matches := compileReport.FindAllStringSubmatch(err.Error(), -1)
	if len(matches) == 0 {
		return &UnsupportedError{Opcode: "eval compile failure without a PHP diagnostic"}
	}
	last := matches[len(matches)-1]
	class := "CompileError"
	if last[1] == "Parse" {
		class = "ParseError"
	}
	site, line := ctx.Location()
	at, _ := strconv.Atoi(last[3])
	ctx.SetLocation(name, uint32(at))
	raised := php.RaiseAt(ctx, class, "%s", last[2])
	ctx.SetLocation(site, line)
	return raised
}

// RunScopedEval executes a freshly harvested eval body inside a generated Go
// caller. vars points at the caller's lowered local slots; order records
// names first created by eval for later dynamic-variable enumeration. A nil
// map means file scope, whose variables live in ctx.Globals.
func (in *Interp) RunScopedEval(ctx *php.Ctx, compiled *pb.Unit, vars map[string]*php.Value, order *[]string) (php.Value, error) {
	return in.runScopedBody(ctx, compiled, vars, order, "eval", "")
}

// RunScopedEvalWithFunctions also exposes the generated program's global
// functions to the eval body. The dispatcher owns generated call framing and
// function selection; the interpreter still handles PHP argument evaluation.
func (in *Interp) RunScopedEvalWithFunctions(ctx *php.Ctx, compiled *pb.Unit,
	vars map[string]*php.Value, order *[]string, names []string,
	signatures map[string]ScopedFunctionSignature,
	dispatch func(*php.Ctx, string, []php.Value) (php.Value, error),
) (php.Value, error) {
	in.scopedFunctions = make(map[string]bool, len(names))
	in.scopedSignatures = make(map[string]ScopedFunctionSignature, len(signatures))
	for _, name := range names {
		in.scopedFunctions[functionKey(name)] = true
	}
	for name, signature := range signatures {
		in.scopedSignatures[functionKey(name)] = signature
	}
	in.scopedDispatch = dispatch
	return in.runScopedBody(ctx, compiled, vars, order, "eval", "")
}

// RunScopedInclude executes a compiled file through the same lowered-variable
// bridge. The generated caller owns path resolution and include_once tracking.
func (in *Interp) RunScopedInclude(ctx *php.Ctx, compiled *pb.Unit, vars map[string]*php.Value, order *[]string, kind, path string) (php.Value, error) {
	return in.runScopedBody(ctx, compiled, vars, order, kind, path)
}

func (in *Interp) runScopedBody(ctx *php.Ctx, compiled *pb.Unit, vars map[string]*php.Value, order *[]string, kind, path string) (php.Value, error) {
	if len(compiled.GetClasses()) != 0 || kind != "eval" && len(compiled.GetFunctions()) != 0 {
		return php.Undef(), &UnsupportedError{Opcode: "runtime source declarations in generated code"}
	}
	op := compiled.GetFile()
	if op == nil {
		if kind == "eval" {
			return php.Null(), nil
		}
		return php.Int(1), nil
	}
	hasThis := false
	for _, line := range op.GetOplines() {
		if in.name(line.GetOpcode()) == "ZEND_FETCH_THIS" {
			hasThis = true
			break
		}
	}
	for _, line := range op.GetOplines() {
		name := in.name(line.GetOpcode())
		// Generated functions and classes need the caller's tables. The only
		// calls this one-body unit can resolve are direct global engine
		// functions, checked against the harvested manifest below.
		declaration := name == "ZEND_DECLARE_FUNCTION" || name == "ZEND_DECLARE_LAMBDA_FUNCTION"
		if (strings.Contains(name, "CALL") && !in.scopedBuiltinCallAllowed(op, line, name)) ||
			strings.HasPrefix(name, "ZEND_DECLARE_") && !(kind == "eval" && declaration) ||
			name == "ZEND_NEW" || name == "ZEND_INCLUDE_OR_EVAL" ||
			name == "ZEND_FETCH_CLASS" ||
			name == "ZEND_FETCH_CONSTANT" || strings.Contains(name, "STATIC_PROP") {
			return php.Undef(), &UnsupportedError{Opcode: "eval operation outside lowered scope: " + name}
		}
		if hasThis && strings.Contains(name, "OBJ") {
			return php.Undef(), &UnsupportedError{Opcode: "eval object operation requiring generated class tables: " + name}
		}
	}
	if vars != nil {
		for _, name := range op.GetVarNames() {
			if vars[name] == nil && order == nil {
				return php.Undef(), &UnsupportedError{Opcode: "eval variable outside lowered scope"}
			}
		}
		for _, name := range op.GetVarNames() {
			if vars[name] == nil {
				vars[name] = new(php.Value)
				*order = append(*order, name)
			}
		}
	}
	file, line := ctx.Location()
	compiledAs := path
	if kind == "eval" {
		compiledAs = fmt.Sprintf("%s(%d) : eval()'d code", file, line)
		siteDir := ""
		if file != "" {
			siteDir = filepath.Dir(file)
		}
		renameCompiled(compiled, compiledAs, siteDir)
	}
	if diagnostic := compiled.GetCompileDiagnostic(); diagnostic != "" {
		in.reportCompileDiagnostic(ctx, diagnostic, compiledAs)
		if pending := ctx.TakePendingError(); pending != nil {
			return php.Undef(), pending
		}
	}
	unit := Load(compiled)
	if kind == "eval" {
		if in.scopedUnit == nil {
			seen := map[string]*pb.OpArray{}
			for _, fn := range compiled.GetFunctions() {
				key := functionKey(fn.GetName())
				if in.scopedFunctions[key] {
					return php.Undef(), &UnsupportedError{Opcode: "eval declaration collides with generated function"}
				}
				if previous := seen[key]; previous != nil {
					return php.Undef(), in.fatal(ctx, "Cannot redeclare function %s() (previously declared in %s:%d)",
						fn.GetName(), previous.GetFilename(), previous.GetLineStart())
				}
				seen[key] = fn
			}
			in.scopedUnit = unit
		} else {
			unit = in.scopedUnit
			for _, fn := range compiled.GetFunctions() {
				key := functionKey(fn.GetName())
				if in.scopedFunctions[key] {
					return php.Undef(), &UnsupportedError{Opcode: "eval declaration collides with generated function"}
				}
				if previous := unit.functions[key]; previous != nil {
					return php.Undef(), in.fatal(ctx, "Cannot redeclare function %s() (previously declared in %s:%d)",
						fn.GetName(), previous.GetFilename(), previous.GetLineStart())
				}
				unit.functionOrder = append(unit.functionOrder, key)
				unit.functions[key] = fn
			}
		}
	}
	unit.builtins = in.builtinClasses
	unit.iniEntries, unit.iniByName = in.iniEntries, in.iniByName
	unit.extensions, unit.iniRegistryAvailable = in.extensions, in.iniRegistryAvailable
	f := &frame{op: op, slots: make([]php.Value, len(op.GetVarNames())+int(op.GetNumTemps())+1), names: in.opcodeNames, preserveCVRefs: vars != nil}
	stack := ctx.Stack()
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].Function == "eval" || stack[i].Function == "include" ||
			stack[i].Function == "include_once" || stack[i].Function == "require" ||
			stack[i].Function == "require_once" {
			continue
		}
		f.this = stack[i].This
		break
	}
	if vars == nil {
		f.global = true
		for i, name := range op.GetVarNames() {
			f.slots[i] = sharedRef(ctx.Globals.RefAt(php.StringKey(name)))
		}
	} else {
		// A distinct file op keeps UNSET_CV from treating function locals as
		// globals. Copy in and back out also lets an eval rebind a local name.
		unit.file = &pb.OpArray{}
		for i, name := range op.GetVarNames() {
			original := *vars[name]
			if ref, ok := original.RefOf(); ok {
				// Keep an existing alias live. The generated caller still owns
				// its binding after this interpreter frame exits.
				f.slots[i] = original
				f.escaped = append(f.escaped, ref)
			} else {
				f.slots[i] = original.Copy()
			}
		}
	}
	call := php.StackFrame{Function: kind, File: file, Line: uint32(line)}
	if kind != "eval" {
		call.Args = []php.Value{php.String(path)}
	}
	ctx.PushCall(call)
	defer ctx.PopCall()
	defer ctx.SetLocation(file, uint32(line))
	value, err := in.execFrame(ctx, unit, f, 0)
	if vars != nil {
		for i, name := range op.GetVarNames() {
			if oldRef, ok := (*vars[name]).RefOf(); ok {
				if newRef, stillBound := f.slots[i].RefOf(); !stillBound || newRef != oldRef {
					php.Unshare(*vars[name])
				}
			}
			*vars[name] = f.slots[i]
		}
	}
	if err != nil {
		if raised, ok := err.(*thrown); ok {
			return php.Undef(), php.Throw(raised.object)
		}
		return php.Undef(), err
	}
	if value.IsUndef() {
		if kind == "eval" {
			return php.Null(), nil
		}
		return php.Int(1), nil
	}
	return value, nil
}

// scopedBuiltinCallAllowed permits direct global engine-function calls in an
// eval body. Direct calls resolve in the persistent eval unit or fall
// through to the harvested engine manifest. Generated program functions
// are dispatched by the generated program hook.
func (in *Interp) scopedBuiltinCallAllowed(op *pb.OpArray, line *pb.Opline, name string) bool {
	switch name {
	case "ZEND_INIT_FCALL", "ZEND_INIT_FCALL_BY_NAME":
		if line.GetOp2Type() != opConst || int(line.GetOp2()) >= len(op.GetLiterals()) {
			return false
		}
		literal := op.GetLiterals()[line.GetOp2()]
		if literal.GetKind() != pb.Literal_KIND_STRING {
			return false
		}
		return true
	case "ZEND_DO_FCALL", "ZEND_DO_UCALL", "ZEND_DO_ICALL", "ZEND_DO_FCALL_BY_NAME":
		// The corresponding INIT was checked above. Other INIT forms are
		// refused, so a call finalizer cannot reach a generated callable.
		return true
	default:
		// Argument sends accompany a checked direct call. They do not select
		// the callee and are interpreted with the engine's ordinary send rules.
		return strings.HasPrefix(name, "ZEND_SEND_")
	}
}

// Flavours of ZEND_INCLUDE_OR_EVAL, from the engine's own numbering. They are
// distinct bits, but the field holds exactly one of them.
const (
	includeEval        = 1 << 0
	includePlain       = 1 << 1
	includeOnce        = 1 << 2
	requirePlain       = 1 << 3
	requireOnce        = 1 << 4
	includeMandatory   = requirePlain | requireOnce
	includeOnlyOnceSet = includeOnce | requireOnce
)

// merge folds a compiled unit's declarations into the running program. A file
// pulled in by include contributes its functions and classes to the same tables
// the includer uses.
func (u *Unit) merge(other *pb.Unit) (*pb.OpArray, *pb.OpArray) {
	for _, fn := range other.GetFunctions() {
		key := strings.ToLower(fn.GetName())
		if previous, taken := u.functions[key]; taken {
			// Including the same file twice claims its names twice, which is a
			// fatal rather than a silent replacement.
			return fn, previous
		}
		u.functionOrder = append(u.functionOrder, key)
		u.functions[key] = fn
	}
	for _, decl := range other.GetClasses() {
		key := strings.ToLower(decl.GetName())
		if _, seen := u.classes[key]; !seen {
			u.classOrder = append(u.classOrder, key)
		}
		u.classes[key] = newClass(decl)
	}
	return nil, nil
}

// resolveInclude turns the operand into a path to open. PHP searches the
// include path; the directory of the file doing the including is what matters
// for the .inc fixtures a test suite uses.
func resolveInclude(requested, from string) (string, bool) {
	if filepath.IsAbs(requested) {
		if _, err := os.Stat(requested); err == nil {
			return requested, true
		}
		return "", false
	}
	candidates := []string{requested}
	if from != "" {
		candidates = append([]string{filepath.Join(filepath.Dir(from), requested)}, candidates...)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}

// includeOrEval performs one of include, require or eval. All three compile
// source that was not part of the harvest and run it in the caller's scope.
func (in *Interp) includeOrEval(ctx *php.Ctx, unit *Unit, caller *frame, operand string, flavour uint32, depth int) (php.Value, error) {
	if in.loader == nil {
		return php.Undef(), &UnsupportedError{
			Opcode: "include, require or eval without a compiler",
		}
	}
	if depth >= maxDepth {
		return php.Undef(), Raise("Error", "maximum include depth %d exceeded", maxDepth)
	}

	var compiled *pb.Unit
	var err error
	compiledAs := ""
	if flavour&includeEval != 0 {
		// The engine names eval'd code after the site that ran it, so a
		// diagnostic points at the eval rather than at the temporary the
		// compiler was handed.
		site, line := ctx.Location()
		compiledAs = fmt.Sprintf("%s(%d) : eval()'d code", site, line)
		siteDir := ""
		if site != "" {
			siteDir = filepath.Dir(site)
		}
		compiled, err = in.loader.CompileString(operand)
		if err != nil {
			return php.Undef(), in.compileFailure(ctx, unit, err, compiledAs)
		}
		// The harvester compiled a temporary file, and every op_array in the
		// result names it. The engine's eval'd code carries the site's name
		// instead, and that is what __FILE__ and every diagnostic below report.
		renameCompiled(compiled, compiledAs, siteDir)
	} else {
		path, found := "", false
		if resolver, ok := in.loader.(FileResolver); ok {
			path, found = resolver.ResolveFile(operand, caller.op.GetFilename())
		} else {
			path, found = resolveInclude(operand, caller.op.GetFilename())
		}
		if !found {
			// Two diagnostics, always in this order: the stdlib.Stream failure --
			// named for the keyword as written -- and then the include
			// failure, which is a warning for the optional forms and the
			// Error for the mandatory ones. An error handler that turns the
			// first into a throw therefore wins over the second.
			ctx.Warn("%s(%s): Failed to open stream: No such file or directory",
				includeWord(flavour), operand)
			if pending := ctx.TakePendingError(); pending != nil {
				return php.Undef(), pending
			}
			// php2go resolves an include against the including file and the
			// working directory and models no search list, so there is nothing
			// to report but the empty one.
			searchPath := unit.setting("include_path")
			if flavour&includeMandatory != 0 {
				return php.Undef(), Raise("Error",
					"Failed opening required '%s' (include_path='%s')", operand, searchPath)
			}
			// The second warning names `include()` whichever optional spelling
			// was written: it comes from one php_error_docref that does not
			// distinguish them.
			ctx.Warn("include(): Failed opening '%s' for inclusion (include_path='%s')",
				operand, searchPath)
			if pending := ctx.TakePendingError(); pending != nil {
				return php.Undef(), pending
			}
			return php.Bool(false), nil
		}
		if flavour&includeOnlyOnceSet != 0 && unit.included[path] {
			// A second _once include is not an error and yields true.
			return php.Bool(true), nil
		}
		// Every include is listed by get_included_files(), not only the _once
		// forms -- what the flag decides is whether a second one runs, not
		// whether the file counts as included.
		if !unit.included[path] {
			if unit.included == nil {
				unit.included = map[string]bool{}
			}
			unit.included[path] = true
			unit.includeOrder = append(unit.includeOrder, path)
		}
		compiled, err = in.loader.CompileFile(path)
		if err != nil {
			return php.Undef(), in.compileFailure(ctx, unit, err, path)
		}
		compiledAs = path
	}
	// The engine creates a just-in-time superglobal when it compiles a
	// reference to one, which for an included file is now.
	if err := in.defineAutoGlobals(ctx, unit, compiled.GetAutoGlobals(), depth); err != nil {
		return php.Undef(), err
	}
	// Compiling can report without failing -- a magic method with the wrong
	// visibility, for one -- and the report belongs to the running program.
	if diagnostic := compiled.GetCompileDiagnostic(); diagnostic != "" {
		in.reportCompileDiagnostic(ctx, diagnostic, compiledAs)
	}

	// Functions and classes the source declared join the running program before
	// its body runs, so the body can use them.
	if again, previous := unit.merge(compiled); again != nil {
		return php.Undef(), in.fatalAt(ctx, again.GetFilename(), int(again.GetLineStart()),
			"Cannot redeclare function %s() (previously declared in %s:%d)",
			again.GetName(), previous.GetFilename(), previous.GetLineStart())
	}

	body := compiled.GetFile()
	callee := &frame{
		op:    body,
		slots: make([]php.Value, len(body.GetVarNames())+int(body.GetNumTemps())+1),
		this:  caller.this,
		class: caller.class,
		names: in.opcodeNames,
	}
	shareScope(caller, callee)
	callee.sharedScope = caller != nil

	// An include is a frame in a stack trace: `#1 caller.php(6):
	// require('/path/inc')`, named for the form that was written and carrying
	// the path it resolved. So is an eval -- `#3 caller.php(14): eval()` -- but
	// it carries no argument: what it ran is not a path, and the trace does not
	// repeat the source.
	site, line := ctx.Location()
	frame := php.StackFrame{
		Function: includeWord(flavour), File: site, Line: uint32(line),
	}
	if flavour&includeEval == 0 {
		frame.Args = []php.Value{php.String(compiledAs)}
	}
	ctx.PushCall(frame)
	defer ctx.PopCall()
	returned, err := in.execFrame(ctx, unit, callee, depth+1)
	applyUnsets(ctx, unit, caller, callee)
	if err != nil {
		return php.Undef(), err
	}
	// A file that returns nothing yields 1, which is what `if (include ...)`
	// tests against -- but the compiler already says so: a file's op_array ends
	// in `RETURN 1` where an ordinary function's ends in `RETURN null`. So an
	// explicit `return null;` or a bare `return;` really does yield NULL, and
	// mapping null to 1 here would hide that. Probed against the engine, for
	// both include and eval.
	if returned.Kind() == php.KindUndef {
		if flavour&includeEval != 0 {
			return php.Null(), nil
		}
		return php.Int(1), nil
	}
	return returned, nil
}

// includeWord names the form a stack trace reports an include under.
func includeWord(flavour uint32) string {
	switch {
	case flavour&includeEval != 0:
		return "eval"
	case flavour&requireOnce != 0:
		return "require_once"
	case flavour&requirePlain != 0:
		return "require"
	case flavour&includeOnce != 0:
		return "include_once"
	}
	return "include"
}

// renameCompiled gives every op_array a freshly compiled unit holds the same
// filename. Only eval needs it: the name the engine uses is not a path at all.
//
// dir is what __DIR__ should have been -- the directory of the file that ran
// the eval, which is not derivable from the name, since that name is a sentence
// rather than a path.
func renameCompiled(compiled *pb.Unit, name, dir string) {
	// __FILE__ and __DIR__ are folded into literals while compiling, so the
	// temporary's name is baked into the code as well as recorded beside it.
	was := compiled.GetFile().GetFilename()
	wasDir := ""
	if was != "" && dir != "" {
		wasDir = filepath.Dir(was)
	}
	var rename func(op *pb.OpArray)
	rename = func(op *pb.OpArray) {
		if op == nil {
			return
		}
		op.Filename = name
		for _, literal := range op.GetLiterals() {
			if literal.GetKind() != pb.Literal_KIND_STRING {
				continue
			}
			// `__DIR__ . "\n"` is folded into one literal, so the name is a
			// substring rather than the whole of it. The temporary's path is
			// unique, so any occurrence of it came from the fold.
			text := literal.GetStringValue()
			if was != "" {
				text = strings.ReplaceAll(text, was, name)
			}
			if wasDir != "" {
				text = strings.ReplaceAll(text, wasDir, dir)
			}
			literal.StringValue = text
		}
		for _, closure := range op.GetClosures() {
			rename(closure)
		}
	}
	rename(compiled.GetFile())
	for _, function := range compiled.GetFunctions() {
		rename(function)
	}
	for _, class := range compiled.GetClasses() {
		class.Filename = name
		// An anonymous class is named after the file it was written in --
		// `class@anonymous<file>:<line>$<hash>` -- and the opline that declares
		// it carries that name as a literal. Both sides have to move together
		// or the declaration no longer finds its class.
		if was != "" {
			class.Name = strings.ReplaceAll(class.GetName(), was, name)
		}
		for _, method := range class.GetMethods() {
			rename(method)
		}
	}
}

// shareScope aliases the caller's variables into an included or eval'd body.
//
// PHP runs both in the caller's variable scope, but compiled variables are
// numbered per op_array, so the two frames have unrelated slots. Binding the
// shared names to one box makes assignment visible in both directions. A
// variable the caller never compiled a slot for cannot be shared, which is why
// a name first created inside eval stays there.
// applyUnsets carries an included file's unset() back to the caller, whose
// variables those were.
func applyUnsets(ctx *php.Ctx, unit *Unit, caller, callee *frame) {
	if caller == nil {
		return
	}
	for _, name := range callee.unsetNames {
		if slot, ok := caller.slotNamed(name); ok {
			caller.bindSlot(opCV, slot, php.Undef())
		}
		if caller.op == unit.file {
			unit.unbindGlobal(ctx, name)
		}
	}
}

func shareScope(caller, callee *frame) {
	if caller == nil {
		return
	}
	for i, name := range callee.op.GetVarNames() {
		slot, ok := caller.slotNamed(name)
		if !ok || i >= len(callee.slots) {
			continue
		}
		if ref, made := caller.slotRef(opCV, slot); made {
			callee.slots[i] = php.MakeRef(ref)
		}
	}
}

// compileReport picks the engine's own diagnostic out of what the compiler
// reported. The text arrives as `zend: <path>: <Kind> error: <message> in
// <path> on line <N>`, and it is the last such line that describes the failure.
var compileReport = regexp.MustCompile(`(?m)^.*?(Parse|Fatal) error: (.*) in .* on line (\d+)\s*$`)

// compileDiagnostics picks the non-fatal diagnostics out of what the compiler
// printed. Unlike a failure, these do not stop the compile and the source still
// runs.
var compileDiagnostics = regexp.MustCompile(
	`(?m)^(Warning|Deprecated|Notice): (.*) in .* on line (\d+)\s*$`)

// reportCompileDiagnostic re-raises what compiling included or eval'd source
// printed, as a diagnostic of the running program rather than as plain text.
// That is what the engine does and it is observable: a handler installed before
// the eval receives the compiler's warnings, and receives them attributed to
// the eval site rather than to whatever file the compiler was handed.
func (in *Interp) reportCompileDiagnostic(ctx *php.Ctx, text, file string) {
	where, at := ctx.Location()
	defer ctx.SetLocation(where, at)
	for _, match := range compileDiagnostics.FindAllStringSubmatch(text, -1) {
		line, _ := strconv.Atoi(match[3])
		ctx.SetLocation(file, uint32(line))
		switch match[1] {
		case "Deprecated":
			ctx.Deprecated("%s", match[2])
		case "Notice":
			ctx.Notice("%s", match[2])
		default:
			ctx.Warn("%s", match[2])
		}
	}
}

// compileFailure turns a failure to compile into the throwable the engine
// raises for it. A syntax error is a ParseError; anything else the compiler
// refuses -- `break` outside a loop, a void function returning a value -- is a
// CompileError, which is its parent and equally catchable. Both carry the bare
// reason as their message and the position within the code that was compiled,
// not the position of the eval that ran it.
func (in *Interp) compileFailure(ctx *php.Ctx, unit *Unit, err error, file string) error {
	matches := compileReport.FindAllStringSubmatch(err.Error(), -1)
	if len(matches) == 0 {
		// Not a diagnostic this can read: it is a gap rather than a program
		// error, and must not become catchable.
		return err
	}
	last := matches[len(matches)-1]
	className := "CompileError"
	if last[1] == "Parse" {
		className = "ParseError"
	}
	class, known := unit.findClass(className)
	if !known {
		return Raise(className, "%s", last[2])
	}
	object := newThrowable(ctx, unit, class, []php.Value{php.String(last[2])})
	object.Set("file", php.String(file))
	line, _ := strconv.Atoi(last[3])
	object.Set("line", php.Int(int64(line)))
	return &thrown{object: object}
}

// autoGlobalDefinition is the library function that answers a just-in-time
// superglobal's value -- the one `register_superglobals_impl` uses for the
// main program -- asked again when an included file names one first.
const autoGlobalDefinition = "PHP2Go\\Extension\\Standard\\auto_global_impl"

// defineAutoGlobals defines the just-in-time superglobals an included or
// eval'd unit names that this request has not defined yet. Without the library
// installed there is nothing to define them with, and none is defined.
func (in *Interp) defineAutoGlobals(ctx *php.Ctx, unit *Unit, names []string, depth int) error {
	definition, installed := unit.libraryFunctions[functionKey(autoGlobalDefinition)]
	if !installed {
		return nil
	}
	for _, name := range names {
		if ctx.SuperglobalRegistered(name) {
			continue
		}
		value, err := in.exec(ctx, unit, definition, []php.Value{php.String(name)}, depth+1)
		if err != nil {
			return err
		}
		if ctx.DefineSuperglobal(name, value.Deref().Copy()) {
			for _, frame := range unit.frames {
				in.bindRegisteredSuperglobals(ctx, frame)
			}
		}
	}
	return nil
}
