package main

import (
	"fmt"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/internal/gogen"
	"github.com/moriyoshi/php2go/internal/ir"
	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/internal/lower"
	"github.com/moriyoshi/php2go/internal/userland"
	"github.com/moriyoshi/php2go/internal/zend"
)

// Linking the harvested PHP library into a compiled program.
//
// A generated program is **its own Go module** and cannot import
// `internal/userland`, so the library cannot be handed to it the way the
// interpreter is handed it. The bodies are compiled into the program instead,
// exactly as the program's own functions are -- which is what `str_pad`,
// `print_r` and 270-odd others need to be callable from compiled code at all.
//
// **Only what is reached.** The two libraries declare some six hundred
// functions between them; emitting all of them into every file would be most
// of the build, and the transitive closure from what the program actually calls
// is a handful.
type userlandLibrary struct {
	// bodies is every function the libraries declare, keyed by lower-case name.
	// A public alias is in here under the **engine's** spelling, with the
	// op_array renamed to match: a diagnostic raised inside `print_r_impl`
	// reports `print_r()`, and the name the body carries is what reports it.
	bodies map[string]*pb.OpArray
	// lowered memoises the lowering, which is the expensive half.
	lowered map[string]*lower.Function
	// missing records a body that would not lower, so a second file asking for
	// it is refused with the same reason rather than lowered again.
	missing map[string]string
	// source is the **virtual** path each body reports, keyed by lower-case
	// name. It is the path the interpreter uses too, and it matters for more
	// than tidiness: a diagnostic raised inside a library body names the file
	// it was raised in, and the walk that finds the *caller* of a library
	// function looks for the nearest frame in a different file. With every
	// body claiming the program's own file there was no different file, and
	// `parse_ini_file()`'s warning reported a line eight thousand deep in the
	// generated Go.
	source map[string]string
	// initializers are the libraries' initializer functions, in catalog
	// order: every program links and runs them before its file body.
	initializers []string
	// classes are the libraries' classes -- php://memory's wrapper -- with
	// the virtual path each reports. Every program links them: a stream
	// opens through one wherever a path is formed, which no call graph shows.
	classes []libraryClass
	// written is where the libraries' sources were put for the engine to
	// compile -- beside the case file, in the work directory. They are the
	// library, not files the program could include: the computed-include
	// candidate scan skips them (computedIncludeCandidates).
	written map[string]bool
}

type libraryClass struct {
	decl *pb.Class
	path string
}

// loadUserland harvests the libraries once. The sources are embedded, so they
// are written into the work directory the run already has.
func loadUserland(engine *zend.Engine, work string) (*userlandLibrary, error) {
	lib := &userlandLibrary{
		bodies:  map[string]*pb.OpArray{},
		lowered: map[string]*lower.Function{},
		missing: map[string]string{},
		source:  map[string]string{},
		written: map[string]bool{},
	}
	signatures, err := engine.Builtins()
	if err != nil {
		return nil, err
	}
	spelling := make(map[string]string, len(signatures))
	for _, fn := range signatures {
		spelling[strings.ToLower(fn.Name)] = fn.Name
	}
	for _, library := range userland.Libraries() {
		path := filepath.Join(work, filepath.Base(library.VirtualPath))
		if err := os.WriteFile(path, library.Source, 0o644); err != nil {
			return nil, err
		}
		if real, err := filepath.EvalSymlinks(path); err == nil {
			if abs, err := filepath.Abs(real); err == nil {
				lib.written[abs] = true
			}
		}
		unit, err := engine.CompileUnit(path)
		if err != nil {
			return nil, fmt.Errorf("compile %s: %w", library.VirtualPath, err)
		}
		encoded := ir.EncodeUnit(unit)
		lib.initializers = append(lib.initializers, library.Initializers...)
		for _, c := range encoded.GetClasses() {
			lib.classes = append(lib.classes, libraryClass{decl: c, path: library.VirtualPath})
		}
		own := map[string]*pb.OpArray{}
		for _, fn := range encoded.GetFunctions() {
			own[strings.ToLower(fn.GetName())] = fn
			lib.bodies[strings.ToLower(fn.GetName())] = fn
			lib.source[strings.ToLower(fn.GetName())] = library.VirtualPath
		}
		for _, alias := range library.Functions {
			implementation, found := own[strings.ToLower(alias.Implementation)]
			if !found {
				return nil, fmt.Errorf("userland: implementation %q for %q was not harvested",
					alias.Implementation, alias.Name)
			}
			// **Renamed to the public spelling.** The body is
			// `print_r_impl`, and every diagnostic it raises names the
			// function it is running -- so without this a TypeError from
			// inside one reported a name no program can call.
			body := proto.Clone(implementation).(*pb.OpArray)
			name := alias.Name
			if declared, known := spelling[strings.ToLower(alias.Name)]; known {
				name = declared
			}
			body.Name = name
			lib.bodies[strings.ToLower(alias.Name)] = body
			lib.source[strings.ToLower(alias.Name)] = library.VirtualPath
		}
	}
	return lib, nil
}

// link pulls into the program every library function it reaches, and every one
// those reach in turn. It reports the first body that would not lower, which is
// a gap in the same sense a refused opcode is.
func (lib *userlandLibrary) link(lw *lower.Lowerer, prog *gogen.Program) (string, bool) {
	if lib == nil {
		return "", true
	}
	have := map[string]bool{}
	for _, fn := range prog.Functions {
		have[strings.ToLower(fn.Name)] = true
	}
	// **The initializers are roots too**: nothing in the program calls
	// them, and the request's superglobals are defined in one.
	prog.Initializers = append([]string(nil), lib.initializers...)
	for _, c := range lib.classes {
		if why, ok := addClass(lw, c.decl, prog); !ok {
			return "link library class " + c.decl.GetName() + ": " + why, false
		}
		cl := prog.Classes[len(prog.Classes)-1]
		// The whole-unit passes have run by now, so the methods get the
		// per-body ones here, as a linked function does; and each reports
		// the library's file.
		for _, m := range cl.Methods {
			m.File = c.path
			specialize(m)
		}
		for _, m := range cl.StaticMethods {
			m.File = c.path
			specialize(m)
		}
		if prog.LibraryPaths == nil {
			prog.LibraryPaths = map[string]bool{}
		}
		prog.LibraryPaths[c.path] = true
	}
	pending := append(calledNames(prog), lib.initializers...)
	called := map[string]bool{}
	for _, name := range pending {
		called[strings.ToLower(name)] = true
	}
	// **A name the program only spells as a string** -- `$f = 'ucfirst';
	// $f('x')`, `array_map('ucfirst', ...)` -- is a callee no call graph
	// shows, and without its body the dynamic call reached the engine-only
	// refusal. Such a seed is *optional*: the program may never call it, so
	// a body that will not lower leaves the call to that refusal rather than
	// refusing the whole file.
	optional := map[string]bool{}
	for _, name := range literalNames(prog) {
		key := strings.ToLower(name)
		// A name the program also calls by name is linked as it always was:
		// its refusal is the program's.
		if _, known := lib.bodies[key]; !known || called[key] || strings.HasPrefix(key, `php2go\internal\`) {
			continue
		}
		if !optional[key] {
			optional[key] = true
			pending = append(pending, name)
		}
	}
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		key := strings.ToLower(name)
		if have[key] || prog.Builtins[key] || gogen.IsLocalBuiltin(key) {
			continue
		}
		if why, refused := lib.missing[key]; refused {
			if optional[key] {
				continue
			}
			return why, false
		}
		if optional[key] {
			// Lowered and linked alone first: anything it reaches that
			// cannot be linked must not refuse a program that only named it.
			if _, ok := lib.probeLink(lw, prog, key); !ok {
				continue
			}
		}
		body, known := lib.bodies[key]
		if !known {
			// **A `PHP2Go\Internal` leaf the runtime does not provide is our
			// gap, not the program's.** Left alone it reaches the emitted
			// program as `Call to undefined function PHP2Go\Internal\...` --
			// a fatal php2go invented, which is a wrong answer where the file
			// should read `unsupported`. Several of these still live in
			// `runtime/interp`, which compiled code cannot see.
			if strings.HasPrefix(key, `php2go\internal\`) {
				why := "userland leaf " + name + " is not in the compiled runtime"
				lib.missing[key] = why
				return why, false
			}
			// Anything else is not ours: the emitter reports it as the gap it
			// is, or as the program calling something that does not exist.
			continue
		}
		have[key] = true
		fn, ok := lib.lowered[key]
		if !ok {
			var err error
			fn, err = lw.Lower(body)
			if err != nil {
				why := "lower userland " + body.GetName() + ": " + short(err.Error())
				lib.missing[key] = why
				return why, false
			}
			specialize(fn)
			lib.lowered[key] = fn
		}
		// **And its closures.** A body that declares one references it by the
		// name the `closure` opcode uses, and nothing else emits it -- so a
		// linked `array_multisort_impl` produced a Go file naming a function
		// that was never written, which is `errored` rather than a gap.
		before := make(map[string]bool, len(prog.Closures))
		for n := range prog.Closures {
			before[n] = true
		}
		if why, ok := collectClosures(lw, body, prog, ""); !ok {
			lib.missing[key] = why
			return why, false
		}
		// **What a closure calls is reached too.** `array_multisort_impl`
		// calls `array_multisort_compare_impl` only from inside its usort
		// callback, so walking the body alone left it out and the program
		// said `Call to undefined function` for a function the library has.
		for n, c := range prog.Closures {
			if !before[n] {
				pending = append(pending, calleesOf(c)...)
			}
		}
		prog.Functions = append(prog.Functions, fn)
		if prog.LibraryFile == nil {
			prog.LibraryFile = map[string]string{}
		}
		prog.LibraryFile[strings.ToLower(fn.Name)] = lib.source[key]
		pending = append(pending, calleesOf(fn)...)
	}
	return "", true
}

// probeLink reports whether a library function and everything it calls by name
// can be linked -- every body lowers and no `PHP2Go\Internal` leaf is missing
// -- without adding anything to the program. It is the check an *optional*
// seed passes before it is linked.
func (lib *userlandLibrary) probeLink(lw *lower.Lowerer, prog *gogen.Program, key string) (string, bool) {
	seen := map[string]bool{}
	pending := []string{key}
	for len(pending) > 0 {
		k := strings.ToLower(pending[0])
		pending = pending[1:]
		if seen[k] || prog.Builtins[k] || gogen.IsLocalBuiltin(k) {
			continue
		}
		seen[k] = true
		if why, refused := lib.missing[k]; refused {
			return why, false
		}
		body, known := lib.bodies[k]
		if !known {
			if strings.HasPrefix(k, `php2go\internal\`) {
				return "userland leaf " + k + " is not in the compiled runtime", false
			}
			continue
		}
		fn, ok := lib.lowered[k]
		if !ok {
			var err error
			fn, err = lw.Lower(body)
			if err != nil {
				why := "lower userland " + body.GetName() + ": " + short(err.Error())
				lib.missing[k] = why
				return why, false
			}
			specialize(fn)
			lib.lowered[k] = fn
		}
		pending = append(pending, calleesOf(fn)...)
	}
	return "", true
}

// literalNames is every string literal a program's bodies spell as a value --
// an operand, or an argument in a call's list -- which is every name a dynamic
// call may reach. A call's own name (`call.fn "f"`, `call.intrinsic
// "write_var"`) is not a value and is left out.
func literalNames(prog *gogen.Program) []string {
	var out []string
	take := func(text string) {
		text = strings.TrimSpace(text)
		if s, err := strconv.Unquote(text); err == nil && s != "" && len(s) < 64 {
			out = append(out, strings.TrimPrefix(s, `\`))
		}
	}
	add := func(fn *lower.Function) {
		if fn == nil {
			return
		}
		for _, block := range fn.Blocks {
			for _, in := range block.Insts {
				for i, op := range in.Operands {
					if i == 0 && strings.HasPrefix(in.Opcode, "call.") {
						continue
					}
					if strings.HasPrefix(strings.TrimSpace(op), `"`) {
						take(op)
					}
				}
				for rest := in.ArgList; ; {
					i := strings.IndexByte(rest, '"')
					if i < 0 {
						break
					}
					quoted, err := strconv.QuotedPrefix(rest[i:])
					if err != nil {
						break
					}
					take(quoted)
					rest = rest[i+len(quoted):]
				}
			}
		}
	}
	// Every body, property hooks included: a name spelled only in a hook
	// is reached from there.
	for _, fn := range gogen.EveryBody(prog) {
		add(fn)
	}
	return out
}

// calledNames is every statically named callee in a program, from every body it
// carries.
func calledNames(prog *gogen.Program) []string {
	var out []string
	add := func(fn *lower.Function) {
		if fn != nil {
			out = append(out, calleesOf(fn)...)
		}
	}
	add(prog.File)
	for _, fn := range prog.Functions {
		add(fn)
	}
	for _, c := range prog.Classes {
		for _, m := range c.Methods {
			add(m)
		}
		for _, m := range c.StaticMethods {
			add(m)
		}
	}
	for _, fn := range prog.Closures {
		add(fn)
	}
	return out
}

// calleesOf is the names a body calls by literal name. A dynamic call names
// nothing here, which is why a program reaching a library function only through
// `$f = "str_pad"; $f()` still cannot: there is nothing to pull in.
func calleesOf(fn *lower.Function) []string {
	var out []string
	for _, block := range fn.Blocks {
		for _, in := range block.Insts {
			if in.Opcode != "call.fn" || len(in.Operands) == 0 {
				continue
			}
			name, err := strconv.Unquote(strings.TrimSpace(in.Operands[0]))
			if err != nil {
				continue
			}
			out = append(out, name)
			// **And the namespace fallback.** `namespace Foo; print_r($x);`
			// compiles to a call to `Foo\print_r` carrying `fallback="print_r"`,
			// and PHP resolves the namespaced name first and the global one
			// after. Reading only the operand pulled in neither, so the
			// emitter reached the fallback and found nothing there.
			for _, attr := range in.Attrs {
				text, isFallback := strings.CutPrefix(attr, "fallback=")
				if !isFallback {
					continue
				}
				if fallback, err := strconv.Unquote(strings.TrimSpace(text)); err == nil {
					out = append(out, fallback)
				}
			}
		}
	}
	return out
}
