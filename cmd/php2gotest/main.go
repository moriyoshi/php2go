// Command php2gotest is the third arm of the differential oracle.
//
// The existing oracle compares the engine against the interpreter in one
// process. Emitted Go cannot be graded that way: it has to be written to disk,
// compiled by the Go toolchain, and run as a separate program. This does that
// and diffs the result against the engine's own output for the same file.
//
// Nothing about a compiler can be claimed quantitatively without it. Before it
// existed the evidence for the emitter was three hand-picked bodies that were
// run by hand.
//
// # The buckets, and why they are not merged
//
//	unsupported  the emitter refused an opcode -- a known gap, named
//	errored      it emitted Go that the Go compiler rejected -- a compiler bug
//	failed       it ran and disagreed with the engine -- a semantics bug
//	passed       byte-identical to the engine
//
// `unsupported` and `errored` are the load-bearing pair. A refusal is a gap we
// know about; Go rejecting our output is the emitter being wrong about Go, and
// letting the second hide inside the first is how a compiler acquires silent
// holes. Same rule as the .phpt runner's, applied one layer down.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"google.golang.org/protobuf/proto"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moriyoshi/php2go/internal/gogen"
	"github.com/moriyoshi/php2go/internal/ir"
	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/internal/lower"
	"github.com/moriyoshi/php2go/internal/phpt"
	"github.com/moriyoshi/php2go/internal/zend"
	"github.com/moriyoshi/php2go/runtime/php"
	_ "github.com/moriyoshi/php2go/runtime/stdlib"
)

// traitExcludes indexes `A::m insteadof B` by method name, which is how the
// composition needs to ask about it: "when copying this trait, is this method
// the one it lost?"
func traitExcludes(c *pb.Class) map[string]map[string]bool {
	if len(c.GetTraitPrecedences()) == 0 {
		return nil
	}
	out := map[string]map[string]bool{}
	for _, pr := range c.GetTraitPrecedences() {
		m := strings.ToLower(pr.GetMethod())
		if out[m] == nil {
			out[m] = map[string]bool{}
		}
		for _, ex := range pr.GetExcludes() {
			out[m][strings.ToLower(ex)] = true
		}
	}
	return out
}

// setVisibilityOf is the `(set)` half of a property's visibility, empty unless
// the declaration wrote an explicit asymmetry.
//
// A **readonly** property reports `protected(set)` from the engine -- readonly
// is modelled as asymmetric visibility there, which is why its diagnostic says
// `protected(set) readonly property`. That one is covered by the readonly
// rules, so carrying it here as well would refuse the first write twice with
// two different messages.
//
// Two readonly spellings do change the rules, though: `private(set)
// readonly` narrows who may initialize and is refused in the asymmetric words
// (`Cannot modify private(set) property`), and `public(set) readonly` -- no
// set flag at all, where plain readonly carries protected(set) -- may be
// initialized from anywhere. Those two are carried, as "private" and "public".
func setVisibilityOf(pr *pb.Property) string {
	if pr.GetReadonly() {
		switch pr.GetSetVisibility() {
		case "private":
			return "private"
		case "":
			return "public"
		}
		return ""
	}
	return pr.GetSetVisibility()
}

func traitAliases(c *pb.Class) []gogen.TraitAlias {
	var out []gogen.TraitAlias
	for _, a := range c.GetTraitAliases() {
		out = append(out, gogen.TraitAlias{
			Trait: a.GetClass(), Method: a.GetMethod(), Alias: a.GetAlias(),
			Visibility: a.GetVisibility(), Final: a.GetFinal(),
		})
	}
	return out
}

func main() {
	limit := flag.Int("n", 0, "stop after this many files")
	// The census truncated at a fixed 30 rows, which is fine ranking a corpus by
	// how many files each gap blocks and useless over a handful of files, where
	// every count is 1 and the cut is alphabetical. Reading the whole list is
	// the point when the question is "what stops *this* file compiling".
	top := flag.Int("top", 30, "census rows to print (0 for all)")
	timeout := flag.Duration("timeout", 30*time.Second,
		"per-file wall clock for the compiled program and for the oracle")
	verbose := flag.Bool("v", false, "print the reason for every non-passing file")
	keep := flag.String("keep", "", "write generated Go for failing files, and both outputs of a wrong one, into this directory")
	emit := flag.String("emit", "", "write each file's program as a buildable module under this directory, and do not grade")
	root := flag.String("root", ".", "compilation root; reported paths are relative to it")
	oracle := flag.String("php2eval", "php2eval", "path to the php2eval binary used as the oracle")
	evalProfile := flag.String("eval-profile", "", "compile computed eval strings recorded by php2eval -record-evals (explicit profile-guided mode)")
	hybrid := flag.Bool("hybrid", false, "grade generated programs with the php2go_hybrid runtime bridge")
	noMem2Reg := flag.Bool("no-mem2reg", false, "leave places unpromoted; promotion is the default")
	flag.BoolVar(&fusedMath, "fused-math", false, "let Go fuse native float arithmetic (x*y + z as one multiply-add); faster, but results can differ from PHP in the last bit")
	census := flag.Bool("census", false, "report every gap per file rather than grading")
	censusDump := flag.String("census-dump", "", "with -census, also write each file's whole gap set here")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: php2gotest [-n N] [-v] [-keep dir] <file-or-dir>...")
		os.Exit(2)
	}
	php2eval = *oracle
	if *evalProfile != "" {
		data, err := os.ReadFile(*evalProfile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "php2gotest: eval profile:", err)
			os.Exit(1)
		}
		evalProfileRecords = zend.ReadEvalRecords(data)
	}
	emitDir = *emit
	hybridGrading = *hybrid
	if hybridGrading && emitDir != "" {
		fmt.Fprintln(os.Stderr, "php2gotest: -hybrid grades programs; build -emit modules with -tags php2go_hybrid")
		os.Exit(2)
	}
	runTimeout = *timeout
	skipPromotion = *noMem2Reg
	if a, err := filepath.Abs(*root); err == nil {
		sourceRoot = a
	}
	if *census {
		censusDumpPath = *censusDump
		censusTop = *top
		if err := runCensus(flag.Args(), *limit); err != nil {
			fmt.Fprintln(os.Stderr, "php2gotest:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(flag.Args(), *limit, *verbose, *keep); err != nil {
		fmt.Fprintln(os.Stderr, "php2gotest:", err)
		os.Exit(1)
	}
}

// sourceRoot is the compilation root. Diagnostics report paths relative to it,
// so neither the generated program nor the comparison carries the build
// machine's directory layout -- the same reason Go has -trimpath.
var sourceRoot = "."

// rootPolicy records how sourceRoot was chosen, so the artifact can say how its
// paths should be read.
var rootPolicy = "explicit"

// builtinNames is the runtime's library surface, read once. It is the
// compiler's link-time manifest: a call site lowers to a builtin call at emit
// time rather than discovering at run time whether the function exists.
var builtinNames = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range php.BuiltinNames() {
		m[strings.ToLower(n)] = true
	}
	return m
}()

// builtinSignatures is the engine's declared parameter list for every internal
// function, read once. The emitter keeps only the ones a program calls.
var signatureCache map[string]gogen.BuiltinSignature

func builtinSignatures(e *zend.Engine) map[string]gogen.BuiltinSignature {
	if signatureCache != nil {
		return signatureCache
	}
	signatureCache = map[string]gogen.BuiltinSignature{}
	declared, err := e.Builtins()
	if err != nil {
		return signatureCache
	}
	for _, fn := range declared {
		params := make([]gogen.BuiltinParam, 0, len(fn.Args))
		for _, arg := range fn.Args {
			params = append(params, gogen.BuiltinParam{
				Name: arg.Name, Type: arg.Type, ByRef: arg.ByRef, Default: arg.Default,
			})
		}
		signatureCache[strings.ToLower(fn.Name)] = gogen.BuiltinSignature{
			Name:     fn.Name,
			Params:   params,
			Required: int(fn.RequiredArgs),
			Variadic: fn.Variadic,
		}
	}
	return signatureCache
}

// engineFunctions is every global function the engine declares, read once. It
// separates "php2go has not implemented this" from "this function does not
// exist", which QUALITY_GATE.md requires be decided from harvested data rather
// than guessed at.
var engineFnCache map[string]bool

func engineFunctions(e *zend.Engine) map[string]bool {
	if engineFnCache != nil {
		return engineFnCache
	}
	engineFnCache = map[string]bool{}
	bs, err := e.Builtins()
	if err != nil {
		return engineFnCache
	}
	for _, b := range bs {
		engineFnCache[strings.ToLower(b.Name)] = true
	}
	return engineFnCache
}

// engineAttrCache is engineClassAttributes, asked once.
var engineAttrCache map[string][]gogen.Attribute

// engineClassAttributes asks the engine which of its own classes carry
// attributes, and with what arguments -- reflection on Attribute reports
// `#[Attribute(1)]`. An argument other than an int or a string leaves that
// attribute out, which the emitter would not know how to spell.
func engineClassAttributes(e *zend.Engine) map[string][]gogen.Attribute {
	if engineAttrCache != nil {
		return engineAttrCache
	}
	engineAttrCache = map[string][]gogen.Attribute{}
	out, err := e.Eval(`foreach (array_merge(get_declared_classes(), get_declared_interfaces()) as $c) {
		$r = new ReflectionClass($c);
		if ($r->isUserDefined()) { continue; }
		foreach ($r->getAttributes() as $a) {
			echo $c, "\t", $a->getName(), "\t", json_encode($a->getArguments()), "\n";
		}
	}`)
	if err != nil {
		return engineAttrCache
	}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		var args []any
		if json.Unmarshal([]byte(parts[2]), &args) != nil {
			continue
		}
		attr := gogen.Attribute{Name: parts[1]}
		ok := true
		for _, a := range args {
			switch v := a.(type) {
			case float64:
				if v != float64(int64(v)) {
					ok = false
				}
				attr.Args = append(attr.Args, gogen.AttributeArg{Value: strconv.FormatInt(int64(v), 10)})
			case string:
				attr.Args = append(attr.Args, gogen.AttributeArg{Value: strconv.Quote(v)})
			default:
				ok = false
			}
		}
		if ok {
			key := strings.ToLower(parts[0])
			engineAttrCache[key] = append(engineAttrCache[key], attr)
		}
	}
	return engineAttrCache
}

// engineFnAttrCache is engineFunctionAttributed, asked once.
var engineFnAttrCache map[string]bool

// engineFunctionAttributed asks the engine which of its functions carry
// attributes (`#[\Deprecated]` and the like). Reflection answers the rest
// with an empty list and leaves these a gap.
func engineFunctionAttributed(e *zend.Engine) map[string]bool {
	if engineFnAttrCache != nil {
		return engineFnAttrCache
	}
	engineFnAttrCache = map[string]bool{}
	out, err := e.Eval(`foreach (get_defined_functions()['internal'] as $f) {
		if ((new ReflectionFunction($f))->getAttributes()) { echo $f, "\n"; }
	}`)
	if err != nil {
		// Unknown: every function's attributes are a gap.
		engineFnAttrCache = nil
		return map[string]bool{"*": true}
	}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			engineFnAttrCache[strings.ToLower(line)] = true
		}
	}
	return engineFnAttrCache
}

// engineReflectCache is engineReflect, asked once.
var engineReflectCache *gogen.EngineReflect

// engineReflectScript asks the engine's own reflection about every internal
// class and function, one JSON object per line. getClass() is asked too, so
// its rule for unions is the engine's and not a reconstruction.
const engineReflectScript = `
$vis = fn($m) => $m->isPrivate() ? 'private' : ($m->isProtected() ? 'protected' : 'public');
$params = function ($f) {
	$out = [];
	foreach ($f->getParameters() as $p) {
		$t = $p->getType();
		$out[] = ['n' => $p->getName(), 't' => $t === null ? '' : (string)$t, 'ht' => $t !== null,
			'an' => $p->allowsNull(), 'o' => $p->isOptional(), 'va' => $p->isVariadic(),
			'br' => $p->isPassedByReference(), 'd' => $p->isDefaultValueAvailable(),
			'at' => count($p->getAttributes()) > 0, 'cl' => (string)(@$p->getClass()?->name)];
	}
	return $out;
};
foreach (array_merge(get_declared_classes(), get_declared_interfaces(), get_declared_traits()) as $c) {
	$r = new ReflectionClass($c);
	if ($r->isUserDefined()) { continue; }
	$ms = [];
	foreach ($r->getMethods() as $m) {
		$ms[] = ['n' => $m->name, 'c' => $m->class, 'v' => $vis($m), 's' => $m->isStatic(),
			'a' => $m->isAbstract(), 'f' => $m->isFinal(), 'at' => count($m->getAttributes()) > 0,
			'r' => $m->getNumberOfRequiredParameters(), 'ps' => $params($m)];
	}
	$ps = [];
	foreach ($r->getProperties() as $p) {
		$t = $p->getType();
		$ps[] = ['n' => $p->name, 'c' => $p->class, 'v' => $vis($p), 't' => $t === null ? '' : (string)$t,
			'ht' => $t !== null, 'an' => $t === null || $t->allowsNull(), 's' => $p->isStatic(),
			'ro' => $p->isReadOnly(), 'at' => count($p->getAttributes()) > 0];
	}
	$kind = $r->isInterface() ? 'interface' : ($r->isTrait() ? 'trait' : ($r->isEnum() ? 'enum' : 'class'));
	$parent = $r->getParentClass();
	echo "C\t", json_encode(['n' => $r->name, 'k' => $kind, 'p' => $parent ? $parent->name : '',
		'f' => $r->isFinal(), 'a' => $r->isAbstract(), 'at' => count($r->getAttributes()) > 0,
		'm' => $ms, 'pr' => $ps]), "\n";
}
foreach (get_defined_functions()['internal'] as $fn) {
	$f = new ReflectionFunction($fn);
	echo "F\t", json_encode(['n' => $f->name, 'c' => '', 'v' => 'public', 'at' => count($f->getAttributes()) > 0,
		'r' => $f->getNumberOfRequiredParameters(), 'ps' => $params($f)]), "\n";
}
`

// engineReflect is what the engine's reflection says about its own classes
// and functions (gogen.EngineReflect), or nil when the question fails --
// every engine reflector then stays a gap.
func engineReflect(e *zend.Engine) *gogen.EngineReflect {
	if engineReflectCache != nil {
		return engineReflectCache
	}
	out, err := e.Eval(engineReflectScript)
	if err != nil {
		return nil
	}
	er := &gogen.EngineReflect{Classes: map[string]*gogen.EngineReflectClass{}, Functions: map[string]*gogen.EngineReflectMethod{}}
	for _, line := range strings.Split(out, "\n") {
		kind, body, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		switch kind {
		case "C":
			var c gogen.EngineReflectClass
			if json.Unmarshal([]byte(body), &c) == nil {
				er.Classes[strings.ToLower(c.Name)] = &c
			}
		case "F":
			var f gogen.EngineReflectMethod
			if json.Unmarshal([]byte(body), &f) == nil {
				er.Functions[strings.ToLower(f.Name)] = &f
			}
		}
	}
	if len(er.Classes) == 0 {
		return nil
	}
	engineReflectCache = er
	return er
}

// engineFunctionOrder is the engine's function table in its order.
func engineFunctionOrder(e *zend.Engine) []string {
	names, err := e.BuiltinNamesInOrder()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, strings.ToLower(n))
	}
	return out
}

// censusDumpPath is where -census-dump writes, if set.
var censusDumpPath string

// engineConstants is every constant the engine defines, read once and rendered
// as IR literal text. Only the ones that survive constant folding as a
// `const.fetch` are ever looked up, but which those are is per-file.
var engineConstCache map[string]string

func engineConstants(e *zend.Engine) map[string]string {
	if engineConstCache != nil {
		return engineConstCache
	}

	engineConstCache = map[string]string{}
	cs, err := e.Constants()
	if err != nil {
		return engineConstCache
	}
	for _, c := range cs {
		t := lower.LiteralText(ir.EncodeLiteral(c.Value))
		if t == "" || strings.HasPrefix(t, "<") {
			continue
		}
		engineConstCache[c.Name] = t
	}
	return engineConstCache
}

// deprecatedConstants is the engine constants the engine itself flags as
// deprecated, harvested once.
func deprecatedConstants(e *zend.Engine) map[string]bool {
	if deprecatedConstCache != nil {
		return deprecatedConstCache
	}
	deprecatedConstCache = map[string]bool{}
	if cs, err := e.Constants(); err == nil {
		for _, c := range cs {
			if c.Deprecated {
				deprecatedConstCache[c.Name] = true
			}
		}
	}
	return deprecatedConstCache
}

var deprecatedConstCache map[string]bool

// php2eval is the oracle binary. A separate process because the engine writes
// to its own stdout, and the comparison is against exactly those bytes.
var php2eval = "php2eval"

// runTimeout bounds every child process.
//
// A hang is the one failure mode this harness could not report: with no bound,
// one looping generated program leaves the whole run wedged and prints no
// summary at all. That happened -- an hour of wall clock, still in the `b`s,
// with a `prog` spinning on a `throw` routed into its own `finally`. It grades
// as `errored`, the bucket that is supposed to stay empty, so a hang is loud
// rather than silent.
var runTimeout = 30 * time.Second

// emitDir, when set, turns grading into emission: each file's generated
// program is written as a module of its own -- main.go beside the go.mod and
// go.sum every program here builds with -- and nothing is built, run or
// compared. hack/bench.sh is the consumer: a benchmark prints timings, so it
// never passes, and grading it would run the engine a second time for nothing.
var emitDir string

// hybridGrading installs the same tagged hook emitted modules receive, but
// only in the temporary program being graded. The default path remains
// independent of the Zend linked hybrid runtime.
var hybridGrading bool

const hybridBuildTag = "php2go_hybrid"

const hybridSource = `//go:build php2go_hybrid

// Code generated by php2gotest. DO NOT EDIT.
package main

import (
	"github.com/moriyoshi/php2go/runtime/hybrid"
	"github.com/moriyoshi/php2go/runtime/php"
)

func init() {
	evalRuntimeString = func(ctx *php.Ctx, code string, vars map[string]*php.Value, order *[]string) (php.Value, error) {
		signatures := make(map[string]hybrid.FunctionSignature, len(hybridFuncParams))
		for name, names := range hybridFuncParams {
			parameters := make([]hybrid.FunctionParameter, len(names))
			for i, parameter := range names {
				parameters[i] = hybrid.FunctionParameter{
					Name: parameter,
					ByRef: hybridFuncByRef[name][i],
					Variadic: hybridFuncVariadic[name] == i,
				}
			}
			signatures[name] = hybrid.FunctionSignature{Parameters: parameters, Required: hybridFuncRequired[name]}
		}
		return hybrid.EvalScopedWithFunctions(ctx, code, vars, order, hybridFuncOrder, signatures, dispatchHybridUserFunction)
	}
	includeRuntimeFile = hybrid.IncludeScoped
}
`

type outcome int

const (
	passed outcome = iota
	failed
	unsupported
	errored
	// noreference is "the oracle could not produce a reference output", which
	// is not a statement about php2go at all.
	//
	// Splitting it out is not bookkeeping. `030.phpt` is `catch (Exception
	// $this)` -- a *fatal compile error* test. The engine bails at compile time
	// by design, `php2eval` evaluates a string and has nowhere to put that
	// outcome, so the file scored `unsupported` and read as a php2go gap.
	// `php2phpt` runs the same file and passes it. Counting that as a gap
	// overstates what the compiler cannot do and, worse, hides where it is
	// weakest: the tests the oracle cannot ask about are disproportionately the
	// diagnostic tests, which are also the ones the emitter fails.
	noreference
)

func (o outcome) String() string {
	return [...]string{"passed", "failed", "unsupported", "errored", "noreference"}[o]
}

// collect gathers the corpus, shared by grading and the census so the two never
// disagree about which files they are talking about.
func collect(roots []string, limit int) ([]string, error) {
	var paths []string
	for _, root := range roots {
		if err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
			if err == nil && !fi.IsDir() &&
				(strings.HasSuffix(p, ".php") || strings.HasSuffix(p, ".phpt")) {
				paths = append(paths, p)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	sort.Strings(paths)
	if limit > 0 && len(paths) > limit {
		paths = paths[:limit]
	}
	return paths, nil
}

// buildProgram fills a Program from an encoded unit, and is **the only place a
// Program is constructed**. Grading and the census both call it, so a refusal
// cannot be visible to one and invisible to the other.
//
// It exists because they diverged: the class-level refusals -- static
// properties, static methods, property hooks, unrenderable defaults -- lived in
// the grading path alone, so the census reported 964 files as having no gap at
// all while 291 of them refused the moment they were graded. The census
// under-reported, which is the *flattering* direction, and it did so while
// `lowerFile`'s own comment claimed the two paths shared it.
//
// Returns ("", true) when the program was built, and (reason, false) when a
// named gap stopped it.
// suspends reports whether a body yields or suspends, and so has to be emitted
// as a resumable state machine rather than a plain function.
func suspends(fn *lower.Function) bool {
	for _, b := range fn.Blocks {
		// **Both spellings of a suspension**: the lowering's, and the CPS
		// rewrite's `ret.*` that replaces it -- a body asked after the rewrite
		// looked like one that never suspends, and its frame drain, deferred in
		// Step, then ran at every suspension: a fiber a fiber body made was
		// destroyed as the outer one suspended (fiber.php). A suspending call
		// suspends this body too.
		for _, p := range []string{"yield", "fiber.suspend", "generator_create", "call.suspending ",
			"ret.yield", "ret.suspend", "ret.generator", "ret.call"} {
			if strings.HasPrefix(b.Term, p) {
				return true
			}
		}
	}
	return false
}

// Escape-rate tallies, for the question Phase A asks before selective
// refcounting is built: what share of objects needs no refcount at all.
//
// Counted per **`new` site**, not per object created at run time. A site is the
// thing the emitter can act on, and a static answer is the one D3 would use;
// what a site costs dynamically depends on how often it runs, which this does
// not weigh.
var (
	newSites        int
	newConfined     int
	newDisposable   int
	newDiscarded    int
	newClaimed      int
	newInFileBody   int
	newInSuspending int
	bodiesTotal     int
	bodiesOpaque    int
	// Inlining tallies. `newIfInlined` is the per-site upper bound from
	// `ConfinedIfInlined`; the `small*` pairs restrict it to bodies under a
	// size, since only a small function is an inlining candidate.
	newIfInlined   int
	smallSites     [4]int
	smallIfInlined [4]int
	smallConfined  [4]int
)

// inlineSizes are the body sizes an inlining pass might plausibly accept,
// measured in live instructions. Reported as a curve rather than a single
// threshold: the answer to "would inlining help" depends on where the line is
// drawn, and a single number hides that.
var inlineSizes = [4]int{8, 16, 32, 64}

// The ownership trace's tallies, over the `new` sites the frame question is
// asked of: not the file body, whose locals are globals, and not a suspending
// body, which markConfined skips. lower.TraceOwnership is the analysis; this
// only counts what it says.
var (
	ownSites, ownConfined int
	// ownMoved counts sites that are never shared -- one owner at a time, so
	// a refcount would never exceed one -- split by whether they escape.
	ownMovedConfined, ownMovedEscaping int
	// ownViaContainer counts escaping sites that do not escape on their own:
	// every reason reached them through a container.
	ownViaContainer int
	ownReasons      = map[string]int{}
	ownSole         = map[string]int{}
	ownContainer    = map[string]int{}
	// ownDisagree cross-checks the trace against ConfinedNew. The trace is the
	// more conservative of the two on re-entry and less on containment, so a
	// disagreement either way is worth reading.
	ownDisagree = map[string]int{}
)

// escapeDump prints one line per escaping site so a cause can be read back to
// the PHP that produced it.
var escapeDump = os.Getenv("PHP2GO_ESCAPE_DUMP") != ""

// traceOwnership tallies one body's trace.
func traceOwnership(fn *lower.Function, isFileBody bool, opts lower.OwnershipOptions) {
	if suspends(fn) {
		return
	}
	opts.FileBody = isFileBody
	tr := lower.Trace(fn, opts)
	countArrayFacts(fn, isFileBody, tr)
	// The emitter's P3a facts: literals built once, copies not made.
	fn.SharedLiterals = map[string]bool{}
	for _, l := range tr.SharedLiterals {
		fn.SharedLiterals[lower.SharedLiteralKey(l.Block, l.Index, l.Text)] = true
	}
	fn.SharedCopies = map[string]bool{}
	for _, c := range tr.SharedCopies {
		fn.SharedCopies[c] = true
	}
	fn.ReleaseAfter = map[string][]string{}
	for _, r := range tr.ReleaseAfter {
		k := lower.InstPos{Block: r.Block, Index: r.Index}.Key()
		fn.ReleaseAfter[k] = append(fn.ReleaseAfter[k], r.Value)
		releasedCopies++
	}
	fn.ReleasableParams = map[int]bool{}
	for i, ok := range tr.ParamReleasable {
		if ok {
			fn.ReleasableParams[i] = true
			releasableParams++
		}
	}
	fn.SharedStores = map[string]bool{}
	for _, st := range tr.SharedStores {
		fn.SharedStores[st.Key()] = true
	}
	// **Moves** (OWNERSHIP-PERF.md P2) take the same store: the value is
	// fresh, dies at the store and has no other holder, so sharing it would
	// only make the variable's next write copy an array nobody else has.
	for _, m := range tr.Moves {
		fn.SharedStores[lower.InstPos{Block: m.Block, Index: m.Index}.Key()] = true
	}
	sharedStores += len(tr.SharedStores)
	sharedLiterals += len(tr.SharedLiterals)
	sharedCopies += len(tr.SharedCopies)
	if isFileBody {
		return
	}
	for _, o := range tr.Sites {
		if o.Kind != "new" {
			continue
		}
		ownSites++
		claimed := fn.ConfinedNew[o.Site]
		switch {
		case o.Confined() && !claimed:
			ownDisagree["trace confines, ConfinedNew does not"]++
		case !o.Confined() && claimed:
			ownDisagree["ConfinedNew confines, trace does not"]++
			if escapeDump {
				fmt.Fprintf(os.Stderr, "DISAGREE\t%s\t%s:%s\t%s\n",
					strings.Join(o.Escapes, ","), fn.File, o.Line, fn.Name)
			}
		}
		if o.Confined() {
			ownConfined++
			if !o.Shared {
				ownMovedConfined++
			}
			continue
		}
		if !o.Shared {
			ownMovedEscaping++
		}
		for _, r := range o.Escapes {
			ownReasons[r]++
		}
		if len(o.Escapes) == 1 {
			ownSole[o.Escapes[0]]++
		}
		if len(o.Via) > 0 {
			ownViaContainer++
			for _, r := range o.Escapes {
				ownContainer[r]++
			}
		}
		if escapeDump {
			fmt.Fprintf(os.Stderr, "OWN\t%s\tvia=%s\towners=%d\t%s:%s\t%s\n",
				strings.Join(o.Escapes, ","), strings.Join(o.Via, ">"), o.Owners,
				fn.File, o.Line, fn.Name)
		}
	}
}

// countEscapes tallies one body's `new` sites against what the escape analysis
// could claim for them.
//
// Three reasons a site is not claimed are worth keeping apart, because they are
// different work: the analysis **bailed** on the whole body (a variable
// variable, `extract`, anything in `escapeOpaque`), the body is the **file
// body** where a local is a global and frame confinement means nothing, or the
// body **suspends** and `markConfined` skips it. What is left is a site the
// analysis looked at and decided escapes.
func countEscapes(fn *lower.Function, isFileBody bool,
	leaksThis func(string) bool, keepsArg func([]string, int) bool, argKept func(string) bool) {
	sites := 0
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode == "new" && in.Result != nil {
				sites++
			}
		}
	}
	if sites == 0 {
		return
	}
	bodiesTotal++
	newSites += sites
	switch {
	case isFileBody:
		newInFileBody += sites
	case suspends(fn):
		newInSuspending += sites
	case fn.ConfinedNew == nil:
		// The analysis refused the body outright rather than deciding per site.
		bodiesOpaque++
	}
	// The upper bound if this body were inlined: a return stops being an
	// escape, so a site whose only exit is `ret` becomes the caller's to
	// confine. Recomputed here rather than stored on the function, because it
	// is a measurement and not something the emitter acts on.
	ifInlined := lower.ConfinedIfInlined(fn, keepsArg, leaksThis, argKept)
	newIfInlined += len(ifInlined)
	live := 0
	for _, b := range fn.Blocks {
		if !b.Dead {
			live += len(b.Insts)
		}
	}
	for i, limit := range inlineSizes {
		if live <= limit {
			smallSites[i] += sites
			smallIfInlined[i] += len(ifInlined)
			smallConfined[i] += len(fn.ConfinedNew)
		}
	}
	newConfined += len(fn.ConfinedNew)
	newDisposable += len(fn.DisposableNew)
	newDiscarded += len(fn.DiscardedNew)
	// **The union is the number that matters**, not the three separately: a
	// site claimed by any of them needs no refcount, and a site can be claimed
	// twice. Summing the three would double-count and overstate the payoff.
	claimed := map[string]bool{}
	for _, set := range []map[string]bool{fn.ConfinedNew, fn.DiscardedNew} {
		for k := range set {
			claimed[k] = true
		}
	}
	// DisposableNew is keyed the same way but carries the release points as its
	// value, so it needs its own loop rather than the slice above.
	for k := range fn.DisposableNew {
		claimed[k] = true
	}
	newClaimed += len(claimed)
}

// reportEscapes prints the tally.
func reportEscapes() {
	if newSites == 0 {
		return
	}
	pct := func(n int) float64 { return 100 * float64(n) / float64(newSites) }
	fmt.Printf("\nescape analysis over %d `new` sites in %d bodies:\n", newSites, bodiesTotal)
	fmt.Printf("  confined to their frame   %6d  (%.1f%%)\n", newConfined, pct(newConfined))
	fmt.Printf("  disposable temporaries    %6d  (%.1f%%)\n", newDisposable, pct(newDisposable))
	fmt.Printf("  discarded (used by nothing)%5d  (%.1f%%)\n", newDiscarded, pct(newDiscarded))
	fmt.Printf("  ANY of the three          %6d  (%.1f%%)  <- needs no refcount\n",
		newClaimed, pct(newClaimed))
	fmt.Printf("  in the file body          %6d  (%.1f%%)  -- locals are globals, not asked\n",
		newInFileBody, pct(newInFileBody))
	fmt.Printf("  in a suspending body      %6d  (%.1f%%)  -- markConfined skips these\n",
		newInSuspending, pct(newInSuspending))
	fmt.Printf("  bodies the analysis refused outright: %d\n", bodiesOpaque)
	fmt.Printf("\nif a body's return did not count as an escape -- the inlining ceiling:\n")
	fmt.Printf("  confinable                %6d  (%.1f%%)  vs %.1f%% today\n",
		newIfInlined, pct(newIfInlined), pct(newConfined))
	reportOwnership()
	fmt.Printf("\n  by callee size, since only a small body is a candidate:\n")
	for i, limit := range inlineSizes {
		if smallSites[i] == 0 {
			continue
		}
		fmt.Printf("    <=%2d insts: %5d sites, %4d confinable if inlined (%.1f%%), %4d today (%.1f%%)\n",
			limit, smallSites[i], smallIfInlined[i],
			100*float64(smallIfInlined[i])/float64(smallSites[i]),
			smallConfined[i], 100*float64(smallConfined[i])/float64(smallSites[i]))
	}
}

// The array and string facts: what copy elision (OWNERSHIP-PERF.md P2, P3a)
// and in-place appends (P1) would have to work with. Counted over every body
// the trace runs on, file body included -- a global is still a store.
var (
	arrSites                     = map[string]int{}
	arrNeverWritten, arrConfined int
	storesAll, storesMoved       int
	concatsAll, concatsInPlace   int
	bodiesTraced, bodiesFresh    int
	paramsAll, paramsWritten     int
)

func countArrayFacts(fn *lower.Function, isFileBody bool, tr *lower.OwnershipTrace) {
	for _, o := range tr.Sites {
		switch o.Kind {
		case "array", "literal", "call":
		default:
			continue
		}
		arrSites[o.Kind]++
		if o.Kind == "call" {
			continue
		}
		if !o.Written {
			arrNeverWritten++
		}
		if o.Confined() {
			arrConfined++
		}
	}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode != lower.IntrinsicCall || len(in.Operands) == 0 {
				continue
			}
			switch strings.Trim(in.Operands[0], `"`) {
			case "write_var":
				storesAll++
			case "concat":
				concatsAll++
			}
		}
	}
	storesMoved += len(tr.Moves)
	concatsInPlace += len(tr.Appends)
	if !isFileBody {
		bodiesTraced++
		if tr.ReturnsFresh {
			bodiesFresh++
		}
	}
	for _, w := range tr.ParamWritten {
		paramsAll++
		if w {
			paramsWritten++
		}
	}
	if escapeDump {
		for _, m := range tr.Moves {
			fmt.Fprintf(os.Stderr, "MOVE\t%s -> %s\t%s\t%s\n", m.Value, m.Place, fn.File, fn.Name)
		}
		for _, a := range tr.Appends {
			fmt.Fprintf(os.Stderr, "APPEND\t%s\t%s\t%s\n", a.Result, fn.File, fn.Name)
		}
	}
}

// reportArrayFacts prints them.
func reportArrayFacts() {
	total := arrSites["array"] + arrSites["literal"]
	if total == 0 && storesAll == 0 && concatsAll == 0 {
		return
	}
	pct := func(n, of int) float64 { return 100 * float64(n) / float64(max(of, 1)) }
	fmt.Printf("\narray and string facts (OWNERSHIP-PERF.md):\n")
	fmt.Printf("  array sites               %6d  (array.new %d, literal %d)\n",
		total, arrSites["array"], arrSites["literal"])
	fmt.Printf("    never written in place  %6d  (%.1f%%)\n", arrNeverWritten, pct(arrNeverWritten, total))
	fmt.Printf("    confined                %6d  (%.1f%%)\n", arrConfined, pct(arrConfined, total))
	fmt.Printf("    built once, shared      %6d\n", sharedLiterals)
	fmt.Printf("    copies not made         %6d\n", sharedCopies)
	fmt.Printf("    stores not copied       %6d\n", sharedStores)
	fmt.Printf("  calls returning fresh     %6d  (any value, most of them scalars)\n", arrSites["call"])
	fmt.Printf("  stores (write_var)        %6d\n", storesAll)
	fmt.Printf("    moves, no copy needed   %6d  (%.1f%%)\n", storesMoved, pct(storesMoved, storesAll))
	fmt.Printf("  concatenations            %6d\n", concatsAll)
	fmt.Printf("    appends in place        %6d  (%.1f%%)\n", concatsInPlace, pct(concatsInPlace, concatsAll))
	fmt.Printf("    rewritten to *strbuf    %6d\n", inPlaceAppends)
	fmt.Printf("  bodies returning fresh    %6d of %d (%.1f%%)\n", bodiesFresh, bodiesTraced, pct(bodiesFresh, bodiesTraced))
	fmt.Printf("  parameters written        %6d of %d (%.1f%%)\n", paramsWritten, paramsAll, pct(paramsWritten, paramsAll))
	fmt.Printf("  parameters released       %6d of %d (%.1f%%)\n", releasableParams, paramsAll, pct(releasableParams, paramsAll))
	fmt.Printf("  promoted copies released  %6d\n", releasedCopies)
}

// reportOwnership prints the trace's tallies.
func reportOwnership() {
	if ownSites == 0 {
		return
	}
	pct := func(n, of int) float64 { return 100 * float64(n) / float64(of) }
	escaping := ownSites - ownConfined
	fmt.Printf("\nownership trace over %d `new` sites (file and suspending bodies excluded):\n", ownSites)
	fmt.Printf("  confined                  %6d  (%.1f%%)\n", ownConfined, pct(ownConfined, ownSites))
	fmt.Printf("    of which only moved     %6d\n", ownMovedConfined)
	fmt.Printf("  escaping                  %6d  (%.1f%%)\n", escaping, pct(escaping, ownSites))
	fmt.Printf("    of which only moved     %6d  (%.1f%%)  <- refcount never above one\n",
		ownMovedEscaping, pct(ownMovedEscaping, max(escaping, 1)))
	fmt.Printf("    only through a container%6d  (%.1f%%)\n",
		ownViaContainer, pct(ownViaContainer, max(escaping, 1)))
	for _, k := range sortedKeys(ownDisagree) {
		fmt.Printf("  %-40s %5d\n", k, ownDisagree[k])
	}
	top := func(title string, m map[string]int, of int) {
		if len(m) == 0 {
			return
		}
		fmt.Printf("\n%s\n", title)
		keys := sortedKeys(m)
		sort.SliceStable(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] })
		for i, k := range keys {
			if i >= 12 {
				break
			}
			fmt.Printf("  %-40s %5d  (%.1f%%)\n", k, m[k], pct(m[k], max(of, 1)))
		}
	}
	top("why escaping sites escape (a site can have several reasons):", ownReasons, escaping)
	top("sites with exactly one reason:", ownSole, escaping)
	top("where the container goes, for sites that escape only through one:", ownContainer, ownViaContainer)
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// markConfined records which `new` sites make an object that cannot outlive the
// frame, so the emitter can destroy it at return rather than leaving it to
// script-end teardown.
//
// **Not for the file body, and not for a resumable one other than a
// generator's.**
//
// `{main}`'s locals are **globals**: the engine destroys them at script end in
// reverse order, not at the body's return in creation order, and on a fatal
// path it prints the error block first. Claiming them destroyed `$x` before
// `Call to undefined function` was written -- `bug36268` is the test that says
// so, and it is the only file this analysis broke.
//
// A resumable body's Go function is entered once per step, so a destructor tied
// to its return would fire at the first suspension rather than when the frame
// ends. A generator's frame therefore keeps the list itself and drains it
// only from the step that ends the body (gogen body).
//
// The method summary used to be `nil`, which made every method call on an
// object count as an escape -- the difference between claiming 9% and 26% of
// sites. `lower.LeaksThis` supplies it now, computed over the whole unit by
// markLifetimes.
// markDiscarded claims the objects nothing looks at. Run after markConfined so
// a site claimed here can be taken back out of the frame list: destroying it
// twice is guarded, but handing its number back twice is not.
//
// **Every body, not only the file's.** The engine frees a statement-level
// `new C(...);` at the end of its statement in a function, a method or a
// closure too -- probed: `new D("x"); $y = new D("y"); new D("z");` destroys
// x and z at their statements and y at the frame's end. What once made this
// unsafe outside `{main}` is gone: promotion used to erase the store of
// `$a = new Noisy();`, leaving an object that looked used by nothing, but every
// promoted store of a `new` now leaves a `release_prev ... track` marker naming
// it (mem2reg), which DiscardedObjects counts as a use. `destructors_frame`
// guards that shape.
//
// Outside the file body a class whose `__destruct` may store `$this`
// (resurrecting) is left to the frame list, as before. Conservative only: the
// destructor hook now pins such an object's number (gogen SetDestructorRun),
// so claiming it here would no longer hand a live object's number away, and
// the engine does destroy it at the statement.
func markDiscarded(fn *lower.Function, prog *gogen.Program, resurrects map[string]bool) {
	fn.DiscardedNew = lower.DiscardedObjects(fn, func(class string) bool {
		return prog.CtorLeaksThis[class] || fn != prog.File && resurrects[class]
	})
	for v := range fn.DiscardedNew {
		delete(fn.ConfinedNew, v)
	}
}

// markLoopOwned claims the objects a `foreach` holds alone -- `foreach (new A
// as $v)` -- which die at the loop's end rather than the frame's. Every body,
// the file body included: the object is a temporary, not a local.
func markLoopOwned(fn *lower.Function, prog *gogen.Program) {
	classes := map[string]*gogen.Class{}
	for _, c := range prog.Classes {
		classes[strings.ToLower(c.Name)] = c
	}
	// The class and every ancestor compiled, none implementing anything --
	// Traversable is only reachable through an interface -- and no property
	// hooked; the constructor asked of the first one that declares it.
	plain := func(class string) bool {
		for c := classes[class]; ; {
			if c == nil || c.IsEnum || len(c.Interfaces) > 0 {
				return false
			}
			for _, p := range c.Props {
				if p.GetHook != nil || p.SetHook != nil || p.Virtual {
					return false
				}
			}
			if c.Parent == "" {
				return true
			}
			c = classes[strings.ToLower(c.Parent)]
		}
	}
	ctorLeaks := func(class string) bool {
		for c := classes[class]; c != nil; c = classes[strings.ToLower(c.Parent)] {
			if _, ok := c.Methods["__construct"]; ok {
				return prog.CtorLeaksThis[strings.ToLower(c.Name)]
			}
			if c.Parent == "" {
				break
			}
		}
		return false
	}
	fn.LoopOwnedNew = lower.LoopOwnedObjects(fn, ctorLeaks, plain)
}

// hasAttrText reports an instruction carrying attribute attr, spelled as
// the IR prints it.
func hasAttrText(in lower.Inst, attr string) bool {
	for _, a := range in.Attrs {
		if a == attr {
			return true
		}
	}
	return false
}

// scopeIsGlobal reports a body whose locals may be the globals: the file
// body, and an included file's.
func scopeIsGlobal(fn *lower.Function, prog *gogen.Program) bool {
	if fn == prog.File {
		return true
	}
	for _, inc := range prog.Includes {
		if inc.Main == fn {
			return true
		}
	}
	return false
}

// markReassigned claims the locals whose previous object dies when they are
// written again.
func markReassigned(fn *lower.Function, prog *gogen.Program,
	keepsArg func([]string, int) bool, leaksThis func(string) bool,
	argKept func(string) bool, makesGenerator func([]string) bool, caughtFresh bool) {
	text := fn.String()
	if strings.Contains(text, "GLOBALS") {
		return
	}
	reached, everything := globalReach(prog, fn)
	// The file body's locals are the globals, and so are an included file's
	// at the top level: when any of them may be reached by a computed name,
	// none is claimed.
	if everything {
		return
	}
	fn.ReassignReleases = lower.ReassignedLocals(fn, reached,
		keepsArg, leaksThis, argKept, makesGenerator, caughtFresh)
	if suspends(fn) {
		// **A resumable body** -- a generator, a fiber's frames, a file body
		// the split made one. Its locals are frame fields, and nothing reaches
		// them while it is suspended, so a local that owns every object it
		// holds still owns it across a suspension and its release points are
		// a plain body's. The one holder the frame adds is a CPS slot: a
		// value live across a suspension is copied into `$__cpsN`, and one
		// written to a local too has two holders (lower.DropSuspendCopied).
		// The store claims below need no filter: each requires a stored
		// `new` used by its constructor and the store alone, and a CPS copy
		// is a use; and a site interval lies within one block, which a
		// suspension always ends.
		fn.ReassignReleases = lower.DropSuspendCopied(fn, fn.ReassignReleases)
	}
	fn.OverwriteReleases = lower.OverwriteOwners(fn, reached, keepsArg, leaksThis, argKept)
	fn.RebindReleases = lower.RebindOwners(fn, reached, keepsArg, leaksThis, argKept)
	fn.SiteReleaseAt = lower.SiteReleases(fn, reached, keepsArg, leaksThis, argKept)
}

// globalReach answers which of fn's local names another body can reach, and
// whether that is every one of them.
//
// A name any body binds with `global $x` is not this body's alone; asked
// across the whole program because the file body's locals are the globals.
// **`$GLOBALS` reaches globals by name**, from any body: a function reading
// `$GLOBALS['o']` holds the file body's `$o` as surely as one binding `global
// $o` does. It lowers to a global-scope dynamic variable (`dynvar.get "o",
// scope=global`), not to anything spelled GLOBALS: a literal name is one
// global, like `global $o`; a computed one, or the array itself, may be any
// of them -- which matters only to a body whose locals are the globals.
func globalReach(prog *gogen.Program, fn *lower.Function) (func(string) bool, bool) {
	globals := map[string]bool{}
	globalsArray := false
	for _, body := range everyBody(prog) {
		if body == nil {
			continue
		}
		if strings.Contains(body.String(), "GLOBALS") {
			globalsArray = true
		}
		for _, b := range body.Blocks {
			if b.Dead {
				continue
			}
			for _, in := range b.Insts {
				if in.Opcode == "bind_global" && len(in.Operands) > 0 {
					globals[strings.TrimPrefix(strings.TrimSpace(in.Operands[0]), "$")] = true
				}
				if !strings.HasPrefix(in.Opcode, "dynvar") || !hasAttrText(in, "scope=global") {
					continue
				}
				if len(in.Operands) == 0 {
					globalsArray = true
					continue
				}
				name, err := strconv.Unquote(strings.TrimSpace(in.Operands[0]))
				if err != nil {
					globalsArray = true
					continue
				}
				globals[name] = true
			}
		}
	}
	everything := globalsArray && scopeIsGlobal(fn, prog)
	return func(name string) bool { return everything || globals[name] }, everything
}

// throwsOnlyFresh is lower.ThrowsOnlyFresh over the whole program: the
// question whether a caught exception can have another holder is answered by
// every body that could have thrown it, not by the one that catches it.
func throwsOnlyFresh(prog *gogen.Program) bool {
	// CtorLeaksThis already follows an inherited constructor and counts one a
	// trait may supply as leaking; an engine class's constructor keeps nothing.
	ctorLeaks := func(class string) bool {
		return prog.CtorLeaksThis[strings.TrimPrefix(class, "\\")]
	}
	return lower.ThrowsOnlyFresh(everyBody(prog), ctorLeaks)
}

// everyBody is every body the program can run, for the questions that must
// hold of all of them: gogen.Bodies, which leaves static methods and
// property hooks out, and those -- with each one's int version. A
// whole-program guard asked of less would miss a `throw $e` or a kept
// exception in a static method.
func everyBody(prog *gogen.Program) []*lower.Function {
	return gogen.EveryBody(prog)
}

// throwableClass answers whether a class the program names by a lower-case
// name may be a Throwable: its chain through the program's classes ends in
// an engine class the engine names as one -- every engine Throwable is an
// `...Exception` or an `...Error` -- or in a name the program does not
// declare, which is not known not to be one.
func throwableClass(prog *gogen.Program) func(class string) bool {
	parent := map[string]string{}
	for _, c := range prog.Classes {
		parent[strings.ToLower(strings.TrimPrefix(c.Name, "\\"))] = strings.ToLower(strings.TrimPrefix(c.Parent, "\\"))
	}
	return func(class string) bool {
		class = strings.TrimPrefix(class, "\\")
		for depth := 0; depth <= len(parent); depth++ {
			p, declared := parent[class]
			if !declared {
				return strings.HasSuffix(class, "exception") || strings.HasSuffix(class, "error") ||
					class == "throwable"
			}
			if p == "" {
				return false
			}
			class = p
		}
		return true
	}
}

// markCtorFreshProps settles, for each class, which of its properties hold
// objects that die with an instance whose constructor threw
// (gogen.Program.CtorFreshProps): the effective constructor -- its own or
// the nearest compiled ancestor's -- fills them with fresh objects nothing
// else can reach (lower.CtorFreshProps), and each is a declared property
// without hooks that the constructor's class can see, declared once, so
// the store took no detour through `__set` or a hook and reached the slot
// the object keeps under that name. A chain that reaches a class the
// program did not compile before a constructor is left out.
func markCtorFreshProps(prog *gogen.Program) {
	classes := map[string]*gogen.Class{}
	for _, c := range prog.Classes {
		classes[strings.ToLower(strings.TrimPrefix(c.Name, "\\"))] = c
	}
	ctorLeaks := func(class string) bool { return prog.CtorLeaksThis[strings.TrimPrefix(class, "\\")] }
	for key, c := range classes {
		var owner *gogen.Class
		for cur, depth := c, 0; cur != nil && depth <= len(classes); depth++ {
			if cur.Methods["__construct"] != nil {
				owner = cur
				break
			}
			if cur.Parent == "" {
				break
			}
			cur = classes[strings.ToLower(strings.TrimPrefix(cur.Parent, "\\"))]
		}
		if owner == nil {
			continue
		}
		names := lower.CtorFreshProps(owner.Methods["__construct"], ctorLeaks)
		if len(names) == 0 {
			continue
		}
		want := map[string]bool{}
		for _, n := range names {
			want[n] = true
		}
		// The object keeps a declared property under its plain name, unless
		// a private one is shadowed by another of the same name -- and then
		// which one the store reached is not settled here, so such a class
		// is left out.
		var keys []string
		found := map[string]int{}
		for _, p := range c.Props {
			if !want[p.Name] {
				continue
			}
			found[p.Name]++
			if p.GetHook != nil || p.SetHook != nil ||
				p.Visibility == "private" && !strings.EqualFold(p.Class, owner.Name) {
				found[p.Name] = 99
				continue
			}
			keys = append(keys, p.Name)
		}
		ok := len(keys) == len(names)
		for _, n := range names {
			if found[n] != 1 {
				ok = false
			}
		}
		if !ok {
			continue
		}
		if prog.CtorFreshProps == nil {
			prog.CtorFreshProps = map[string][]string{}
		}
		prog.CtorFreshProps[key] = keys
	}
}

// markOwnedParams gives each compiled body the parameter positions it may
// take over from a caller (lower.Function.OwnedParams), in a program where
// no trace outlives its frame (lower.TracesDieWithFrames). A position
// qualifies when the body keeps nothing of it, never writes it
// (LiveParams), and takes it by value. What a function keeps is the
// fixpoint's answer (retained); a method's or a closure's is asked of its
// body with the same settled predicates (lower.RetainedParams), which call
// every compiled method a keeper of its arguments. A generator or a
// suspending body is left out: its frame outlives the call that made it;
// so is a body holding an exception it caught (lower.HoldsCaught), which
// frees the argument in that exception's turn.
//
// It then marks the calls that may hand a fresh argument over
// (markHandOverCalls).
func markOwnedParams(prog *gogen.Program, named map[string]*lower.Function,
	retained map[string]map[int]bool, allRetained map[string]bool,
	keepsArg func([]string, int) bool, leaksThis func(string) bool, argKept func(string) bool) {
	if !lower.TracesDieWithFrames(everyBody(prog), prog.File, throwableClass(prog)) {
		return
	}
	own := func(fn *lower.Function, kept map[int]bool) {
		if fn == nil || suspends(fn) || isGeneratorBody(fn) || lower.HoldsCaught(fn) {
			return
		}
		written := map[int]bool{}
		for _, pos := range fn.LiveParams {
			written[pos] = true
		}
		for i, p := range fn.Params {
			if kept[i] || written[i] || strings.Contains(p, "&") || strings.Contains(p, "...") {
				continue
			}
			if fn.OwnedParams == nil {
				fn.OwnedParams = map[int]bool{}
			}
			fn.OwnedParams[i] = true
		}
	}
	for name, fn := range named {
		if allRetained[name] {
			continue
		}
		own(fn, retained[name])
	}
	asked := func(fn *lower.Function) {
		if fn == nil {
			return
		}
		kept, all := lower.RetainedParams(fn, keepsArg, leaksThis, argKept)
		if !all {
			own(fn, kept)
		}
	}
	for _, c := range prog.Classes {
		for _, table := range []map[string]*lower.Function{c.Methods, c.StaticMethods} {
			for _, fn := range table {
				asked(fn)
			}
		}
	}
	for _, fn := range prog.Closures {
		asked(fn)
	}
	markHandOverCalls(prog)
}

// markHandOverCalls records on each method, static and closure call the
// argument positions every body that call may run owns
// (lower.HandOverAttr), so the emitter may hand a fresh object there over
// (php.Ctx.HandOver). The callee is known statically or not at all:
//
//   - `$o->m()` with a receiver bound (`recv=`, a declared class or `$this`'s
//     class, which a subclass may be): every compiled class that is that
//     class or below it, and every `m` along each one's chain -- a private
//     ancestor's runs from its own scope. A class the chain leaves the
//     program for, a name no class there declares, or an abstract body
//     refuses, as does any `class_alias`, which can put a subclass under a
//     name this walk does not see.
//   - `C::m()` with the class written out: the `m` found first up C's chain.
//     `self`, `parent` and `static` are left alone.
//   - `$f()` where `$f` is the closure a `closure` instruction of the same
//     body made: that closure's body.
//
// A position is marked only when every such body owns it and every
// argument is sent in place, by value or undecided -- a named or unpacked
// one could land elsewhere.
func markHandOverCalls(prog *gogen.Program) {
	classes := map[string]*gogen.Class{}
	for _, c := range prog.Classes {
		classes[strings.ToLower(strings.TrimPrefix(c.Name, "\\"))] = c
	}
	for _, fn := range everyBody(prog) {
		if fn != nil && strings.Contains(strings.ToLower(fn.String()), `"class_alias"`) {
			return
		}
	}
	lc := func(s string) string {
		return strings.ToLower(strings.TrimPrefix(strings.Trim(strings.TrimSpace(s), `"`), "\\"))
	}
	var isA func(name, target string, depth int) bool
	isA = func(name, target string, depth int) bool {
		if name == target {
			return true
		}
		c := classes[name]
		if c == nil || depth > len(classes) {
			return false
		}
		for _, i := range c.Interfaces {
			if isA(lc(i), target, depth+1) {
				return true
			}
		}
		return c.Parent != "" && isA(lc(c.Parent), target, depth+1)
	}
	methodOf := func(c *gogen.Class, m string) *lower.Function {
		for _, table := range []map[string]*lower.Function{c.Methods, c.StaticMethods} {
			for name, fn := range table {
				if strings.EqualFold(name, m) {
					return fn
				}
			}
		}
		return nil
	}
	// chain answers the bodies named m up class's chain -- all of them, or
	// just the first -- and false when the chain leaves the program or ends
	// with none.
	chain := func(class, m string, all bool) ([]*lower.Function, bool) {
		var out []*lower.Function
		for cur, depth := classes[class], 0; ; depth++ {
			if cur == nil || depth > len(classes) {
				return nil, false
			}
			if _, abstract := cur.AbstractMethods[m]; abstract && methodOf(cur, m) == nil {
				return nil, false
			}
			if fn := methodOf(cur, m); fn != nil {
				out = append(out, fn)
				if !all {
					return out, true
				}
			}
			if cur.Parent == "" {
				return out, len(out) > 0
			}
			cur = classes[lc(cur.Parent)]
		}
	}
	for _, fn := range everyBody(prog) {
		if fn == nil {
			continue
		}
		for _, b := range fn.Blocks {
			if b.Dead {
				continue
			}
			for i := range b.Insts {
				in := &b.Insts[i]
				var bodies []*lower.Function
				switch in.Opcode {
				case "call.method":
					if len(in.Operands) < 2 {
						continue
					}
					recv := ""
					for _, a := range in.Attrs {
						if strings.HasPrefix(a, "recv=") {
							recv = lc(strings.TrimPrefix(a, "recv="))
						}
					}
					m := lc(in.Operands[1])
					if recv == "" || classes[recv] == nil || !strings.HasPrefix(strings.TrimSpace(in.Operands[1]), `"`) {
						continue
					}
					ok := true
					for name := range classes {
						if !isA(name, recv, 0) || classes[name].IsInterface || classes[name].IsTrait {
							continue
						}
						found, resolved := chain(name, m, true)
						if !resolved {
							ok = false
							break
						}
						bodies = append(bodies, found...)
					}
					if !ok {
						continue
					}
				case "call.static":
					if len(in.Operands) < 2 {
						continue
					}
					class := lc(in.Operands[0])
					if !strings.HasPrefix(strings.TrimSpace(in.Operands[0]), `"`) ||
						!strings.HasPrefix(strings.TrimSpace(in.Operands[1]), `"`) {
						continue
					}
					found, resolved := chain(class, lc(in.Operands[1]), false)
					if !resolved {
						continue
					}
					bodies = found
				case "call.dynamic":
					if len(in.Operands) < 1 {
						continue
					}
					def := lower.Definition(fn, strings.TrimSpace(in.Operands[0]))
					if def == nil || def.Opcode != "closure" || len(def.Operands) == 0 {
						continue
					}
					body := prog.Closures[strings.TrimPrefix(strings.TrimSpace(def.Operands[0]), "@")]
					if body == nil {
						continue
					}
					bodies = []*lower.Function{body}
				default:
					continue
				}
				if len(bodies) == 0 {
					continue
				}
				var positions []string
				for pos, arg := range lower.ArgKinds(in.ArgList) {
					if arg != "byval" && arg != "preferref" {
						positions = nil
						break
					}
					owned := true
					for _, body := range bodies {
						owned = owned && body.OwnedParams[pos]
					}
					if owned {
						positions = append(positions, strconv.Itoa(pos))
					}
				}
				if len(positions) > 0 {
					in.Attrs = append(in.Attrs, lower.HandOverAttr+"="+strings.Join(positions, " "))
				}
			}
		}
	}
}

func markConfined(fn *lower.Function, prog *gogen.Program, leaksThis func(string) bool,
	keepsArg func([]string, int) bool, argKept func(string) bool, delegable func(string) bool) {
	// **A resumable body too**: its frame keeps the list across suspensions
	// and drains it when the body ends (gogen body) -- a generator's and a
	// fiber's frames alike. A value live across a suspension is copied into
	// a CPS slot, which the walk sees as a second binding and refuses.
	fn.ConfinedNew = lower.ConfinedObjectsFor(fn, keepsArg, leaksThis, argKept, delegable)
	// A constructor that keeps its receiver extends its lifetime beyond the frame.
	lower.DropCtorLeaking(fn, func(class string) bool {
		return prog.CtorLeaksThis[class]
	}, func() bool {
		for _, leaks := range prog.CtorLeaksThis {
			if leaks {
				return true
			}
		}
		return false
	}())
}

// markProxyInstances decides which lazy proxies own their instance, so the
// instance dies with the proxy (php.OwnedProxyInstance). The engine frees a
// proxy's instance with the proxy's storage when nothing else holds it; this
// proves "nothing else" in three parts.
//
//   - **The factory keeps nothing.** The initializer is a closure whose every
//     return is an object made there and stored nowhere else
//     (lower.ReturnsFreshObject): FreshFactories, per closure body.
//   - **Nothing running on the instance keeps it.** Every call through the
//     proxy runs with the instance as `$this`, so no method, hook or magic
//     method of the instance's class or an ancestor may keep `$this` -- with
//     every ancestor compiled and no trait (ConfinedClasses).
//   - **Nothing hands the instance out.** `initializeLazyObject` and
//     `markLazyObjectAsInitialized` answer with the instance for a proxy, so
//     a program that uses either's result, or names either anywhere but as
//     a method called for its effect, claims nothing at all.
func markProxyInstances(prog *gogen.Program, byName map[string]*gogen.Class,
	bodyLeaks func(*lower.Function) bool, freshFactory func(*lower.Function) bool) {
	prog.FreshFactories, prog.ConfinedClasses = nil, nil
	for _, fn := range everyBody(prog) {
		if fn == nil {
			continue
		}
		text := strings.ToLower(fn.String())
		for _, name := range []string{"initializelazyobject", "marklazyobjectasinitialized"} {
			mentions := strings.Count(text, `"`+name+`"`)
			discarded := 0
			for _, b := range fn.Blocks {
				for _, in := range b.Insts {
					if in.Opcode == "call.method" && len(in.Operands) > 1 &&
						strings.EqualFold(strings.Trim(in.Operands[1], `"`), name) &&
						(in.Result == nil || !valueUsed(fn, in.Result.String())) {
						discarded++
					}
				}
			}
			if mentions != discarded {
				return
			}
		}
	}
	prog.FreshFactories = map[string]bool{}
	for key, fn := range prog.Closures {
		if fn != nil && freshFactory(fn) {
			prog.FreshFactories[key] = true
		}
	}
	prog.ConfinedClasses = map[string]bool{}
	for _, c := range prog.Classes {
		confined := true
		for cur, depth := c, 0; confined; depth++ {
			if cur == nil || depth > len(prog.Classes) || len(cur.Traits) > 0 {
				confined = false
				break
			}
			for _, body := range cur.Methods {
				if bodyLeaks(body) {
					confined = false
				}
			}
			for _, pr := range cur.Props {
				if (pr.GetHook != nil && bodyLeaks(pr.GetHook)) || (pr.SetHook != nil && bodyLeaks(pr.SetHook)) {
					confined = false
				}
			}
			parent := strings.ToLower(strings.TrimPrefix(cur.Parent, "\\"))
			if parent == "" {
				break
			}
			cur = byName[parent]
		}
		if confined {
			prog.ConfinedClasses[strings.ToLower(c.Name)] = true
		}
	}
}

// valueUsed reports whether an SSA value is read anywhere in fn.
func valueUsed(fn *lower.Function, value string) bool {
	for _, b := range fn.Blocks {
		if strings.Contains(" "+b.Term+" ", value) && lowerTokenIn(b.Term, value) {
			return true
		}
		for _, in := range b.Insts {
			if in.Result != nil && in.Result.String() == value {
				continue
			}
			for _, op := range in.Operands {
				if lowerTokenIn(op, value) {
					return true
				}
			}
			if lowerTokenIn(in.ArgList, value) {
				return true
			}
		}
	}
	return false
}

// lowerTokenIn reports value as a whole token of text (`%1` is not in `%12`).
func lowerTokenIn(text, value string) bool {
	for i := strings.Index(text, value); i >= 0; {
		end := i + len(value)
		if end >= len(text) || !(text[end] >= '0' && text[end] <= '9') {
			return true
		}
		next := strings.Index(text[end:], value)
		if next < 0 {
			return false
		}
		i = end + next
	}
	return false
}

// markCatchReleases claims a function's catch places whose exception dies
// with the frame (lower.CatchFrameReleases). Only when the whole program
// throws fresh objects (caughtFresh), so a caught exception has no other
// holder, and only in a body that returns once -- a resumable's frame
// outlives its steps, and its catch places are fields drained elsewhere.
func markCatchReleases(fn *lower.Function, caughtFresh bool, leaksThis func(string) bool) {
	fn.CatchReleases = nil
	if !caughtFresh || suspends(fn) {
		return
	}
	fn.CatchReleases = lower.CatchFrameReleases(fn, leaksThis)
}

// delegableIterators answers which classes' objects a `yield from` can be
// handed without keeping them (lower.ConfinedObjectsFor): an **Iterator**,
// named directly on the class or an ancestor -- so the delegation calls only
// the five iteration methods, never getIterator -- with every class of the
// chain compiled and composing no trait, and none of those methods storing
// `$this`.
func delegableIterators(prog *gogen.Program, leaksThis func(string) bool) func(string) bool {
	classes := map[string]*gogen.Class{}
	for _, c := range prog.Classes {
		classes[strings.ToLower(c.Name)] = c
	}
	for _, m := range []string{"rewind", "valid", "current", "key", "next"} {
		if leaksThis(m) {
			return func(string) bool { return false }
		}
	}
	return func(class string) bool {
		iterator := false
		for c := classes[strings.ToLower(strings.TrimPrefix(class, "\\"))]; ; {
			if c == nil || c.IsEnum || c.IsTrait || c.IsInterface || len(c.Traits) > 0 {
				return false
			}
			for _, i := range c.Interfaces {
				if strings.EqualFold(strings.TrimPrefix(i, "\\"), "Iterator") {
					iterator = true
				}
			}
			if c.Parent == "" {
				return iterator
			}
			c = classes[strings.ToLower(c.Parent)]
		}
	}
}

// markDisposable is the handle-numbering half, and it is **separate from
// markConfined because it applies to the file body too**. `{main}`'s locals are
// globals, so frame confinement says nothing there -- but a temporary handed to
// `var_dump` and dropped dies at the call whatever frame it sits in, and the
// file body is exactly where such statements are written.
func markDisposable(fn *lower.Function, leaksThis func(string) bool,
	keepsArg func([]string, int) bool, freshReturn func([]string) bool) {
	fn.DisposableNew = lower.DisposableTemporaries(fn, keepsArg, leaksThis, freshReturn)
	fn.ArrayAddTemps = lower.ArrayAddTemporaries(fn, leaksThis, freshReturn)
	fn.PlaceTemps = lower.PlaceTemporaries(fn, leaksThis, freshReturn)
	fn.LiveOnError = lower.LiveOnError(fn, fn.DisposableNew, fn.ArrayAddTemps, fn.PlaceTemps)
	fn.RejectableNew = lower.RejectableObjects(fn)
	fn.SoleUseClosure = lower.SoleUseClosures(fn)
}

// nonRetainingEngineMethod names methods of the **engine's** classes that keep
// no reference to their receiver, for the case a compiled body cannot answer:
// `(new Exception("x"))->getMessage()` drops the exception, and the engine
// hands its handle straight back.
//
// Measured the same way the function list is, with
//
//	(new E(...))->m(); $x = new P(); echo spl_object_id($x);
//
// answering 1 when the receiver was released. Consulted **only** when no
// compiled method of that name exists, so a program declaring its own
// `getTrace` is summarised from its own body rather than from this.
func nonRetainingEngineMethod(method string) bool {
	switch method {
	case "getmessage", "getcode", "getfile", "getline", "gettrace",
		"gettraceasstring", "getprevious", "__tostring",
		"getname", "count", "getarraycopy",
		// `__clone` is here for its **default**, not because anyone calls it:
		// `leaksThis` answers from a compiled body when one of that name
		// exists, and falls back to this list when none does. A program where
		// no class declares `__clone` was getting "unknown method, assume it
		// stores $this", which refused every `clone` site. An engine class's
		// clone handler does not store the copy.
		"__clone",
		// **The reflector surface**, measured the same way on 2026-09-20 with
		//
		//	$o = new C(); (new ReflectionClass(C::class))->m(...);
		//	echo spl_object_id(new C());
		//
		// answering 2 for every one of these -- the reflector's own handle
		// came back at the call, so it keeps no reference to itself. Needed
		// because a reflector is the commonest frame local in the
		// `lazy_objects` corpus, and one unclaimed receiver there costs the
		// numbering of everything after it.
		"initializelazyobject", "isuninitializedlazyobject",
		"marklazyobjectasinitialized", "getshortname", "getparentclass",
		"isinterface", "isabstract", "isinstantiable", "hasmethod",
		"hasproperty", "getconstants", "getproperties", "getmethods",
		"implementsinterface", "issubclassof",
		// Measured the same way on 2026-09-30, each answering 2: the
		// accessors that hand back other reflectors or values keep none of
		// the reflector either (attributes/032_attribute_validation_scope,
		// enum/enum-attributes).
		"getattributes", "getreflectionconstant", "getreflectionconstants",
		"getmethod", "getproperty", "getconstructor", "newinstance",
		"newinstancewithoutconstructor", "getinterfacenames", "gettraitnames",
		"isfinal", "isenum", "getstaticproperties", "getconstant",
		"hasconstant",
		// ReflectionProperty's lazy-object surface and value access, measured
		// the same way through `$r->getProperty("a")->m($o)` on 2026-09-30:
		// each gives the property reflector's handle back.
		"skiplazyinitialization", "setrawvaluewithoutlazyinitialization",
		"getvalue", "setvalue", "isinitialized",
		// getHook, measured 2026-10-01 through
		// `(new ReflectionProperty('C','p'))->getHook(PropertyHookType::Get)`:
		// the property reflector's handle and the hook reflector's both come
		// back at the end of the statement.
		"gethook",
		// getClosure of a library function: the closure it builds holds
		// the function name, not the reflector (probed, 2026-10-01).
		"getclosure",
		// ReflectionMethod/ReflectionFunction::invoke and invokeArgs,
		// measured 2026-10-01 through `(new ReflectionMethod("C",
		// "t"))->invoke($o, $o)`: the reflector's handle comes back at the
		// end of the statement. The invoked body cannot reach the reflector
		// -- a backtrace frame names the object invoked on, not it.
		"invoke", "invokeargs",
		// The list and heap containers, measured 2026-10-01 through
		// `(new SplDoublyLinkedList)->push(1); echo spl_object_id(new
		// stdClass);` and the like: the container's handle comes back at
		// the end of each statement.
		"push", "unshift", "pop", "shift", "top", "bottom", "isempty",
		"setiteratormode", "getiteratormode", "enqueue", "insert",
		"iscorrupted", "recoverfromcorruption", "setextractflags", "getextractflags",
		// The lazy-object factories, measured 2026-10-01 through
		// `(new ReflectionClass(C::class))->newLazyGhost($f)`: the reflector's
		// handle is the next object's. A lazy object keeps its initializer,
		// never the reflector that made it.
		"newlazyghost", "newlazyproxy", "resetaslazyghost", "resetaslazyproxy",
		// Generator's own surface: resuming a generator runs its body, which
		// cannot reach the Generator object itself.
		"current", "next", "valid", "key", "send", "rewind", "getreturn":
		return true
	}
	return false
}

// nonRetainingFiberMethod is Fiber's own surface. Running a fiber runs its
// body, which reaches the Fiber object only through `Fiber::getCurrent()`;
// probed, `$f->start()`, `resume`, `throw`, `getReturn` and the four
// predicates each leave the handle free for the next object once `$f` is
// unset. Claiming the local is what destroys a suspended fiber where the
// engine does -- at the `unset`, the overwrite, the frame's end -- running
// its finally blocks there (fibers/unfinished-fiber-with-finally).
func nonRetainingFiberMethod(method string) bool {
	switch method {
	case "start", "resume", "throw", "issuspended", "isrunning", "isstarted", "isterminated":
		return true
	}
	return false
}

// freshEngineMethod are the engine accessors that build a new object on
// every call, so the result is a temporary its consumer may free:
// `$r->getProperty("a")->getValue($o)` gives both reflectors' handles back
// (measured; lazy_objects/skipLazyInitialization_*). getReturnType is not
// here -- the chain through it keeps a handle.
var freshEngineMethod = map[string]bool{
	"getproperty": true, "getmethod": true, "getreflectionconstant": true, "getconstructor": true,
	"gethook": true,
	// getClosure builds a new closure each time over a function or a method
	// (probed: `(new ReflectionFunction('sin'))->getClosure()` gives both
	// handles back). On a closure reflector the engine returns that same
	// closure, which the runtime holds once more (php.Object.HoldHandle) so
	// this release does not free a closure something else still has.
	"getclosure": true,
}

// freshEngineFunction are the library functions whose result is an object
// nothing else holds. `unserialize` only when the runtime says so: its result
// is pinned (php.Object.PinHandle) whenever a hook, a back-reference or an
// enum case means something else may hold it, which makes the release a
// no-op. The engine's own `unserialize` methods return null, so the name
// answering for them too releases nothing.
var freshEngineFunction = map[string]bool{
	"unserialize": true,
}

// nonRetainingMethodArg names methods that keep no reference to an object
// passed to them as an **argument**. A different question from
// nonRetainingEngineMethod, which is about the receiver, and deliberately a
// different list: `$r->initializeLazyObject($obj)` stores neither, but a method
// can easily keep one and not the other.
//
// Measured 2026-09-20 with a negative control, which is what makes the probe
// mean anything:
//
//	function mk($r, $sink) { $g = $r->newLazyGhost(...); <call>; }
//	mk($r, $sink); echo spl_object_id(new stdClass);
//
// The ghost takes handle 4. Every method below answers 4 -- the handle came
// back when `mk` returned, so nothing kept it -- while the control
// `$sink->keep($g)` answers 5, proving the probe detects retention when it is
// there.
func nonRetainingMethodArg(method string) bool {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "initializelazyobject", "isuninitializedlazyobject",
		"marklazyobjectasinitialized":
		return true
	}
	return false
}

// nonRetainingCallee names the library functions known to keep no reference to
// an object argument, which is what makes releasing its handle at the call safe.
//
// **An allow-list, and it must stay one.** The question is not answerable from
// the harvested signature -- arginfo says how an argument is passed, never
// whether the callee stores it -- so the default has to be "assume it is kept".
// Adding one that squirrels the object away would reuse a live handle, which
// prints as another object's number.
//
// Every name here was **asked of the engine** rather than assumed, with
//
//	f(new P()); $x = new P(); echo spl_object_id($x);
//
// which answers 1 when the argument was released and 2 when it was kept. Three
// candidates were dropped for failing to answer at all rather than for
// answering wrongly: `strlen` and `intdiv` refuse an object outright, and
// `echo` is not a call in the IR. An entry that cannot be measured does not
// belong in a list whose safety is that it fails closed. Later drops for the
// same reason: `iterator_count`, whose probe perturbed its own answer, and
// `get_class_vars`, `print` and `assert`, none of which ever receives the
// object at all.
func nonRetainingCallee(callee string) bool {
	switch strings.Trim(strings.TrimSpace(callee), `"`) {
	case "var_dump", "print_r", "var_export", "gettype", "get_class",
		"get_object_vars", "get_debug_type", "spl_object_id", "spl_object_hash",
		"serialize", "json_encode", "count", "is_object", "method_exists",
		"property_exists", "printf",
		// Second batch, measured the same way.
		"is_a", "is_subclass_of", "get_parent_class", "get_class_methods",
		"strval", "sprintf", "vsprintf", "intval", "boolval", "in_array",
		"str_repeat",
		// Measured 2026-09-30 the same way, and with a local:
		// `$y = new P; debug_zval_dump($y); unset($y); new P` gives the
		// handle back too (weakrefs_001).
		"debug_zval_dump":
		return true
	}
	return false
}

// specialize picks the intrinsic variant for every operation, before emission.
// It is separate from lowering because it is a *choice about the consumer*: an
// interpreter over the same IR would make a different one, or none.
func specialize(fn *lower.Function) {
	got, could := lower.Specialize(fn, constantTypes)
	specializedCalls += got
	totalCalls += could
}

var specializedCalls, totalCalls int

// constantTypes is the engine's constants as IR literal text, so the inference
// can type a `const.fetch` from the value the engine actually registered.
var constantTypes map[string]string

// skipPromotion mirrors -no-mem2reg, so both the grading and census paths build
// their lowerer the same way.
var skipPromotion bool

// body is one lowered function waiting for the passes that need the whole unit.
type body struct {
	// key is the call graph's: `f` for a function, `c::m` for a method. Empty
	// for a body nothing can name -- `{main}` and the closures -- which still
	// needs rewriting but can never be a call's resolved target.
	key string
	fn  *lower.Function
}

// bodies are every function lowered for the unit being compiled, in lowering
// order.
//
// They are collected rather than finished in place because the CPS rewrite now
// needs a **whole-unit** answer: whether a call can suspend is
// `CallGraph.Reach`, and a graph cannot be built until every body exists. So
// lowering fills this, and `finishBodies` runs the two per-body passes
// afterwards, in the order they used to run inline.
var bodies []body

// isGeneratorBody reports whether calling this body makes a Generator object
// rather than running it.
func isGeneratorBody(fn *lower.Function) bool {
	for _, b := range fn.Blocks {
		if strings.HasPrefix(b.Term, "ret.generator") || strings.HasPrefix(b.Term, "ret.yield") ||
			strings.HasPrefix(b.Term, "generator_create") || strings.HasPrefix(b.Term, "yield") {
			return true
		}
	}
	return false
}

// classOfBody is the class a method belongs to, from its graph key, so `$this`
// types inside it. Empty for a plain function, a closure or `{main}`.
func classOfBody(key string) string {
	if i := strings.Index(key, "::"); i >= 0 {
		return key[:i]
	}
	return ""
}

// record keeps a lowered body for `finishBodies`.
func record(key string, fn *lower.Function) {
	bodies = append(bodies, body{key: key, fn: fn})
}

// finishBodies runs the CPS rewrite and the specialization over everything
// lowered for this unit.
//
// **The graph is built before anything is rewritten**, for two reasons: the
// rewrite turns a suspension terminator into `ret.suspend`, and -- the one that
// actually bites -- it turns a suspending *call* into a terminator, so the call
// instruction the graph reads its edges from is no longer there.
func finishBodies() (string, bool) {
	// **Receiver inference, so a driver call can be recognised.** `$g = gen();
	// $g->next();` is a call on a Generator, and saying so is what lets the
	// rewrite treat it as a suspension point. Nothing else in this path needed
	// it, which is why it ran only in `php2lower`'s report until now.
	world := lower.NewWorld()
	for _, b := range bodies {
		if b.key != "" && isGeneratorBody(b.fn) {
			world.Generators[b.key] = true
		}
	}
	world.Close()
	for _, b := range bodies {
		lower.InferReceivers(b.fn, classOfBody(b.key), world)
	}
	cg := lower.NewCallGraph()
	for _, b := range bodies {
		if b.key == "" {
			continue
		}
		cg.Add(b.key, b.fn)
	}
	// **Resolve the call sites into edges.** Without this the graph has nodes
	// and no edges, so `Reach` answers only the functions that suspend
	// *directly* -- `inner` but not `outer` -- and a caller one level up never
	// splits. It is a separate step because a site can only be resolved once
	// every definition is known.
	cg.Link()
	// `yield from` carries the marker up a chain that says nothing about itself,
	// which is `gh15108-004`'s shape.
	cg.PropagateMarks()
	// **Sound, and with method edges.** An unresolvable callee has to be
	// assumed to reach anything, and a method call with no inferred receiver
	// reaches every method of that name. Measured over the suspending corpus:
	// opacity costs 6.6% of functions and receiver knowledge 0.0%, so the
	// conservative answer is the one to take.
	reach := cg.ReachFiber(true, true)
	// **A closure whose objects may run a suspending destructor as it
	// returns** gets its frame drain as a suspending call (lower
	// InsertFrameDrains), so `Fiber::suspend()` inside that `__destruct`
	// parks the fiber rather than having nowhere to go (fibers/
	// destructors_007). Only closures: a named function made resumable here
	// would need every caller split too, and nothing seeds the call graph
	// with it. A class that declares no `__destruct` of its own may inherit
	// one -- the parents are not known here -- so it counts whenever any
	// destructor may suspend.
	declared := map[string]bool{}
	anyDtorSuspends := false
	for _, b := range bodies {
		if b.key != "" {
			declared[b.key] = true
		}
	}
	for k := range reach {
		if strings.HasSuffix(k, "::__destruct") {
			anyDtorSuspends = true
		}
	}
	dtorMaySuspend := func(class string) bool {
		if reach[class+"::__destruct"] {
			return true
		}
		if declared[class+"::__destruct"] {
			return false
		}
		return anyDtorSuspends
	}
	for _, b := range bodies {
		if !anyDtorSuspends || b.key != "" || !strings.HasPrefix(b.fn.Name, "{closure") || isGeneratorBody(b.fn) {
			continue
		}
		for _, class := range lower.NewsClass(b.fn) {
			if dtorMaySuspend(class) {
				// A statement's object whose destructor may suspend dies
				// through its drain, a suspension point, not synchronously
				// as a discarded one does: the claim moves to the frame
				// list, which the drain takes it from.
				for _, v := range lower.InsertSiteDrains(b.fn, dtorMaySuspend) {
					if b.fn.DiscardedNew[v] {
						delete(b.fn.DiscardedNew, v)
						if b.fn.ConfinedNew == nil {
							b.fn.ConfinedNew = map[string]bool{}
						}
						b.fn.ConfinedNew[v] = true
					}
				}
				lower.InsertFrameDrains(b.fn)
				break
			}
		}
	}
	for _, b := range bodies {
		if why, ok := resumable(b.fn, reach); !ok {
			return why, false
		}
		specialize(b.fn)
		// **In-place appends** (OWNERSHIP-PERF.md P1), after the variant
		// choice so the concatenations it rewrites are the final ones. A
		// resumable body is left alone; StringChains refuses one anyway.
		inPlaceAppends += lower.StringChains(b.fn)
		// **Int versions** before the representations, which they clone from:
		// functions and methods, which the emitter versions; not closures or
		// the file body, which have no key.
		if b.key != "" {
			if lower.IntVersions(b.fn, constantTypes) > 0 {
				intVersions++
			}
		}
		// **Unboxed ints and floats** last: the representation is a choice
		// about the final instructions, so nothing after it may rewrite them.
		// Ints first, since an int64 boxes exactly and so can go anywhere.
		intValues += lower.IntReps(b.fn, constantTypes)
		floatValues += lower.FloatReps(b.fn, constantTypes)
	}
	return "", true
}

// intValues and floatValues count the values IntReps and FloatReps carried
// unboxed.
var intValues, floatValues int

// intVersions counts the functions IntVersions gave an int version.
var intVersions int

// inPlaceAppends counts the concatenations StringChains rewrote.
var inPlaceAppends int

// sharedLiterals and sharedCopies count the P3a facts handed to the emitter.
var sharedLiterals, sharedCopies, sharedStores, releasableParams, releasedCopies int

// resumable runs the CPS rewrite on a body that needs it. It is a separate pass
// rather than part of lowering because it is a *choice about the emitter*: an
// interpreter over the same IR would not want it.
func resumable(fn *lower.Function, reach map[string]bool) (string, bool) {
	suspending := suspendingCallee(reach)
	if !suspends(fn) && !callsSuspending(fn, suspending) {
		return "", true
	}
	if err := lower.Resumable(fn, suspending); err != nil {
		return "cps " + fn.Name + ": " + short(err.Error()), false
	}
	return "", true
}

// suspendingCallee turns the Reach set into the predicate the rewrite takes,
// and answers nil for an empty set so nothing splits.
func suspendingCallee(reach map[string]bool) lower.SuspendingCallee {
	// A method call site carries the bare method name; the set is keyed
	// `class::method`. With a receiver the answer is exact, and **without one
	// every method of that name is a candidate** -- the same conservative
	// reading `Reach(methods: true)` gives the graph's own edges, and the one
	// the measurement priced at 0.0% against the exact answer.
	byMethod := map[string]bool{}
	for k := range reach {
		if i := strings.Index(k, "::"); i >= 0 {
			byMethod[k[i+2:]] = true
		}
	}
	// Whether any generator in this unit suspends a fiber from inside its body.
	// If one does, a driver call on a Generator may suspend, and which
	// generator a given `$g` holds is not a static fact -- so the conservative
	// answer covers them all. If none does, no driver call can suspend and
	// nothing here fires.
	generatorSuspends := false
	// An Iterator method that may suspend makes a generator `yield from`-ing
	// an object one too: the driver enters the method as a frame, and the
	// suspension parks the generator (gh9916-007). Which object a `yield
	// from` is handed is not a static fact, so any delegation counts.
	iteratorSuspends := false
	for _, m := range []string{"rewind", "valid", "current", "key", "next"} {
		iteratorSuspends = iteratorSuspends || byMethod[m]
	}
	for _, b := range bodies {
		if isGeneratorBody(b.fn) && (suspendsAFiber(b.fn) || iteratorSuspends && delegates(b.fn)) {
			generatorSuspends = true
			break
		}
	}
	// **An empty reach set does not mean nothing splits.** A generator that
	// suspends is deliberately not in it -- calling one makes a Generator and
	// suspends nobody -- but *driving* one does suspend, and that is the case
	// this predicate is for. Short-circuiting on the set alone left the driver
	// calls unsplit and the predicate never consulted.
	if len(reach) == 0 && !generatorSuspends {
		return nil
	}
	return func(kind, name, recv string) bool {
		switch kind {
		case "method":
			// **A driver call on a Generator.** `$g->next()` is an ordinary
			// call, and a `Fiber::suspend()` inside the body has to travel out
			// through it -- which only works if this frame is a state machine.
			// That is rule 2 in `internal/lower/ir.go`, and this is where it
			// finally fires.
			// The receiver's class is often unknown -- a closure `use`
			// variable, a property, anything the inference does not reach --
			// so a driver-named call splits whether or not it was resolved.
			// That is safe because a receiver with no frame of its own gets one
			// that performs the ordinary call, so an over-eager split costs a
			// resumption and changes no behaviour.
			if generatorSuspends && isGeneratorDriver(name) &&
				(recv == "" || strings.EqualFold(recv, "Generator")) {
				return true
			}
			// An inferred receiver is an *exact* class, and a method it
			// reaches may be declared in a parent -- `Sub extends Direct`
			// answers `direct::work`, not `sub::work`. So the exact key is
			// tried and the name is the fallback, rather than the exact key
			// being the answer: inference that narrowed the lookup without
			// walking the chain made the split *miss* calls it used to catch.
			if recv != "" && reach[strings.ToLower(recv)+"::"+name] {
				return true
			}
			return byMethod[name]
		default:
			// `fn` carries a function name and `static` already carries
			// `class::method`, so both are the key as it stands.
			return reach[name]
		}
	}
}

// suspendsAFiber reports whether the body contains a `Fiber::suspend()` of its
// own, as opposed to yielding.
func suspendsAFiber(fn *lower.Function) bool {
	for _, b := range fn.Blocks {
		if strings.HasPrefix(b.Term, "fiber.suspend") || strings.HasPrefix(b.Term, "ret.suspend") {
			return true
		}
	}
	return false
}

// delegates reports whether the body contains a `yield from`.
func delegates(fn *lower.Function) bool {
	for _, b := range fn.Blocks {
		if strings.HasPrefix(b.Term, "yield_from") || strings.HasPrefix(b.Term, "ret.yield_from") {
			return true
		}
	}
	return false
}

// isGeneratorDriver reports whether a method name advances a generator. Every
// one of these enters the body, so every one of them can meet a suspension --
// `current()` and `valid()` prime it just as `next()` does.
func isGeneratorDriver(name string) bool {
	switch strings.ToLower(name) {
	case "next", "send", "current", "key", "valid", "rewind", "throw":
		return true
	}
	return false
}

// callsSuspending reports whether the body calls anything that may suspend,
// which is what makes a body that does not suspend itself need the rewrite.
func callsSuspending(fn *lower.Function, suspending lower.SuspendingCallee) bool {
	if suspending == nil {
		return false
	}
	return lower.CallsSuspending(fn, suspending)
}

// collectClosures lowers every closure body reachable from an op_array. They
// nest, so this recurses: a closure declared inside a closure hangs off *its*
// op_array rather than the file's.
func collectClosures(lw *lower.Lowerer, oa *pb.OpArray, prog *gogen.Program, scope string) (string, bool) {
	parent := oa.GetName()
	if parent == "" {
		parent = "{main}"
	}
	for i, c := range oa.GetClosures() {
		fn, err := lw.Lower(c)
		if err != nil {
			return "lower closure " + c.GetName() + ": " + short(err.Error()), false
		}
		// A closure is not a call target, so it carries no graph key.
		record("", fn)
		// **Keyed by index, not by name**: two closures written on one line
		// share `{closure:file:line}`, and keying by that kept only the last.
		key := lower.ClosureKey(parent, i, c.GetName())
		prog.Closures[key] = fn
		prog.ClosureScope[key] = scope
		// **A closure's `#[...]` too**, which `ReflectionFunction` of the
		// closure reads by the closure's name (006_filter). Two closures on
		// one line share that name, so the later one's list wins there.
		if attrs := attributesOf(c.GetAttributes()); len(attrs) > 0 {
			if prog.FunctionAttributes == nil {
				prog.FunctionAttributes = map[string][]gogen.Attribute{}
			}
			prog.FunctionAttributes[strings.ToLower(c.GetName())] = attrs
		}
		// Keyed by the closure's own key: two closures on one line share
		// the name.
		addParamAttributes(prog, key, c.GetArgs())
		if why, ok := collectClosures(lw, c, prog, scope); !ok {
			return why, false
		}
	}
	return "", true
}

func buildProgram(lw *lower.Lowerer, encoded *pb.Unit, prog *gogen.Program) (string, bool) {
	// **Per unit, not per process.** This binary compiles thousands of files in
	// one run, and a body left here from the previous one would be rewritten a
	// second time -- a CPS rewrite over an already-rewritten body, silently.
	bodies = nil
	prog.Closures = map[string]*lower.Function{}
	prog.ClosureScope = map[string]string{}
	if f := encoded.GetFile(); f != nil {
		fn, err := lw.Lower(f)
		if err != nil {
			return "lower {main}: " + short(err.Error()), false
		}
		record("", fn)
		prog.File = fn
		if why, ok := collectClosures(lw, f, prog, ""); !ok {
			return why, false
		}
	}
	for _, oa := range encoded.GetFunctions() {
		if oa.GetAbstract() {
			continue
		}
		fn, err := lw.Lower(oa)
		if err != nil {
			return "lower " + oa.GetName() + ": " + short(err.Error()), false
		}
		record(strings.ToLower(oa.GetName()), fn)
		prog.Functions = append(prog.Functions, fn)
		if attrs := attributesOf(oa.GetAttributes()); len(attrs) > 0 {
			if prog.FunctionAttributes == nil {
				prog.FunctionAttributes = map[string][]gogen.Attribute{}
			}
			prog.FunctionAttributes[strings.ToLower(oa.GetName())] = attrs
		}
		addParamAttributes(prog, strings.ToLower(oa.GetName()), oa.GetArgs())
		if why, ok := collectClosures(lw, oa, prog, ""); !ok {
			return why, false
		}
	}
	for _, c := range encoded.GetClasses() {
		if why, ok := addClass(lw, c, prog); !ok {
			return why, false
		}
	}
	if why, ok := compileIncludes(lw, prog); !ok {
		return why, false
	}
	// Everything is lowered, so the whole-unit passes can run: the call graph
	// is complete, and with it the answer to whether a given call can suspend.
	if why, ok := finishBodies(); !ok {
		return why, false
	}
	markLifetimes(prog)
	return "", true
}

// evalParseError is the diagnostic an eval'd string that does not parse
// compiles to.
var evalParseError = regexp.MustCompile(`^\s*Parse error: (.*) in (.*) on line (\d+)\s*$`)

// evalThrownError is the diagnostic of another exception the compile threw,
// a CompileError, which the engine reports as a fatal when nothing catches
// it.
var evalThrownError = regexp.MustCompile(`^\s*Fatal error: (.*) in (.*) on line (\d+)\s*$`)

// includeEngine and includeDir are what compileIncludes needs from the file
// being graded: the engine that compiles the included files, and the
// directory a relative include resolves against. Unset, nothing is included.
var (
	includeEngine *zend.Engine
	includeDir    string
	// includeMainAbs is the path the file being graded was compiled under,
	// which its bodies' `.file` carry, and includeMainRel what the program
	// calls it: an eval's name is built from the second.
	includeMainAbs, includeMainRel string
)

// compileIncludes compiles in the files the program includes statically --
// an include whose operand is a literal naming a file that exists now --
// and whatever those include in turn (gogen.Program.Includes).
//
// A relative operand resolves against the **working directory alone**, as the
// oracle's does with its empty include_path; the program runs there. The path
// kept is the real one, which is what __FILE__ says inside the file.
func compileIncludes(lw *lower.Lowerer, prog *gogen.Program) (string, bool) {
	if includeEngine == nil || prog.File == nil {
		return "", true
	}
	done := map[string]bool{}
	// evalKeys names each eval's compiled body, by its site's name and its
	// source. **Two evals on one line share the name the engine gives them**
	// -- `case.php(5) : eval()'d code` -- but not their code, so the second
	// is its own body under a key of its own (use_no_eval_conflict); the
	// name stays what its code reports.
	evalKeys := map[string]string{}
	evalCount := map[string]int{}
	// candidatesDone: the files a computed include may name are compiled
	// in at most once, when the first such include is met.
	candidatesDone := false
	if real, err := filepath.EvalSymlinks(includeMainAbs); err == nil {
		if abs, err := filepath.Abs(real); err == nil {
			prog.MainReal = abs
		}
	}
	// Every body may include: a function's include runs over its locals.
	queue := []*lower.Function{prog.File}
	queue = append(queue, prog.Functions...)
	// The class each method runs in, for an eval written in one.
	methodClass := map[*lower.Function]string{}
	for _, c := range prog.Classes {
		for _, m := range c.Methods {
			queue = append(queue, m)
			methodClass[m] = c.Name
		}
		for _, m := range c.StaticMethods {
			queue = append(queue, m)
			methodClass[m] = c.Name
		}
	}
	for _, fn := range prog.Closures {
		queue = append(queue, fn)
	}
	for len(queue) > 0 {
		fn := queue[0]
		queue = queue[1:]
		// The last position seen: a block need not open with its own.
		line := "0"
		for _, b := range fn.Blocks {
			for i := range b.Insts {
				in := &b.Insts[i]
				if in.Directive() && len(in.Operands) > 0 {
					line = in.Operands[0]
					continue
				}
				if in.Opcode != "include" || len(in.Operands) != 2 {
					continue
				}
				operand, err := strconv.Unquote(strings.TrimSpace(in.Operands[1]))
				if err != nil {
					if kind, _ := strconv.Unquote(strings.TrimSpace(in.Operands[0])); kind == "eval" {
						site := fn.File
						if fn == prog.File {
							site = includeMainAbs
						}
						if why, ok := computedEval(lw, prog, in, site+"("+line+") : eval()'d code", methodClass[fn], done, &queue); !ok {
							return why, false
						}
					}
					// **A computed path**: compile in the files it could
					// name, once, for the run-time check to pick from
					// (computedIncludeCandidates).
					if kind, _ := strconv.Unquote(strings.TrimSpace(in.Operands[0])); kind != "eval" && !candidatesDone {
						candidatesDone = true
						for _, real := range computedIncludeCandidates(prog) {
							if !done[real] {
								before := make(map[string]bool, len(prog.Closures))
								for k := range prog.Closures {
									before[k] = true
								}
								main, ok, why := addCandidateInclude(lw, prog, real)
								if why != "" {
									return why, false
								}
								if !ok {
									continue
								}
								done[real] = true
								queue = append(queue, main)
								for k, cl := range prog.Closures {
									if !before[k] {
										queue = append(queue, cl)
									}
								}
							}
							hash, ok := php.ContentHash(real)
							if !ok {
								continue
							}
							if prog.ComputedIncludes == nil {
								prog.ComputedIncludes = map[string]string{}
							}
							prog.ComputedIncludes[real] = hash
						}
					}
					continue
				}
				if kind, _ := strconv.Unquote(strings.TrimSpace(in.Operands[0])); kind == "eval" {
					// **An eval of a literal is compiled like an included
					// file**, under the name the engine gives eval'd code:
					// the site's file and line. Code that uses $this stays
					// refused -- an included body has none -- and so does
					// code that does not compile (a ParseError at run time).
					// Named after the site as it was compiled -- the absolute
					// path, like every other literal of this file -- so the
					// emitter's one rewrite gives it the program's spelling.
					site := fn.File
					if fn == prog.File {
						site = includeMainAbs
					}
					name := site + "(" + line + ") : eval()'d code"
					key, seen := evalKeys[name+"\x00"+operand]
					if !seen {
						key = name
						if n := evalCount[name]; n > 0 {
							key = fmt.Sprintf("%s#%d", name, n)
						}
						evalCount[name]++
						evalKeys[name+"\x00"+operand] = key
					}
					if !done[key] {
						unit, err := includeEngine.CompileEvalString(operand, name)
						if err != nil {
							// **Code that does not parse is a ParseError the
							// program can catch**, thrown where the eval runs
							// and located in the eval'd code (probed).
							var ce *zend.CompileError
							if errors.As(err, &ce) {
								if m := evalParseError.FindStringSubmatch(ce.Diagnostic); m != nil {
									in.Attrs = append(in.Attrs, "parseerror.name="+gogen.TrimBuildPath(m[2], includeMainAbs, includeMainRel),
										"parseerror.line="+m[3], "parseerror.msg="+m[1])
								} else if m := evalThrownError.FindStringSubmatch(ce.Diagnostic); m != nil && ce.Thrown != "" {
									// **A thrown CompileError is not a
									// fatal**, though the diagnostic reads
									// "Fatal error:" all the same: the
									// eval's caller can catch it (bug75218).
									in.Attrs = append(in.Attrs, "parseerror.name="+gogen.TrimBuildPath(m[2], includeMainAbs, includeMainRel),
										"parseerror.line="+m[3], "parseerror.msg="+m[1], "parseerror.class="+ce.Thrown)
								} else if strings.HasPrefix(strings.TrimSpace(ce.Diagnostic), "Fatal error: ") {
									// **Anything else the compiler refuses is
									// fatal**, printed where the eval runs --
									// `@` does not hide it -- and the shutdown
									// functions still run (probed).
									in.Attrs = append(in.Attrs, "compilefatal="+gogen.TrimBuildPath(ce.Diagnostic, includeMainAbs, includeMainRel))
								}
							}
							continue
						}
						encoded := ir.EncodeUnit(unit)
						if encoded.GetFile().GetUsesThis() {
							continue
						}
						before := make(map[string]bool, len(prog.Closures))
						for k := range prog.Closures {
							before[k] = true
						}
						main, why, ok := addIncluded(lw, encoded, prog, key, name)
						if !ok {
							return why, false
						}
						prog.Includes[len(prog.Includes)-1].Scope = methodClass[fn]
						done[key] = true
						queue = append(queue, main)
						for k, cl := range prog.Closures {
							if !before[k] {
								queue = append(queue, cl)
							}
						}
					}
					in.Attrs = append(in.Attrs, "target="+key)
					continue
				}
				if _, seen := prog.IncludeTargets[operand]; seen {
					continue
				}
				candidate := operand
				if !filepath.IsAbs(candidate) {
					candidate = filepath.Join(includeDir, candidate)
				}
				real, err := filepath.EvalSymlinks(candidate)
				if err != nil {
					continue
				}
				if fi, err := os.Stat(real); err != nil || !fi.Mode().IsRegular() {
					continue
				}
				if !done[real] {
					unit, err := includeEngine.CompileUnit(real)
					if err != nil {
						// **A file that does not compile fails where the
						// include runs**, as an eval does: a ParseError the
						// program can catch, located in the file, or a fatal
						// printed through `@` before the shutdown functions
						// (probed; gh11108).
						var ce *zend.CompileError
						if errors.As(err, &ce) {
							if m := evalParseError.FindStringSubmatch(ce.Diagnostic); m != nil {
								in.Attrs = append(in.Attrs, "parseerror.name="+gogen.TrimBuildPath(m[2], includeMainAbs, includeMainRel),
									"parseerror.line="+m[3], "parseerror.msg="+m[1])
							} else if strings.HasPrefix(strings.TrimSpace(ce.Diagnostic), "Fatal error: ") {
								in.Attrs = append(in.Attrs, "compilefatal="+gogen.TrimBuildPath(ce.Diagnostic, includeMainAbs, includeMainRel))
							}
						}
						continue
					}
					before := make(map[string]bool, len(prog.Closures))
					for k := range prog.Closures {
						before[k] = true
					}
					main, why, ok := addIncluded(lw, ir.EncodeUnit(unit), prog, real, real)
					if !ok {
						return why, false
					}
					done[real] = true
					// The file's body and everything it declared may include
					// in turn.
					queue = append(queue, main)
					for k, fn := range prog.Closures {
						if !before[k] {
							queue = append(queue, fn)
						}
					}
				}
				if prog.IncludeTargets == nil {
					prog.IncludeTargets = map[string]string{}
				}
				prog.IncludeTargets[operand] = real
			}
		}
	}
	return "", true
}

// quotedInArgList finds the string literals in a call's argument list.
var quotedInArgList = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// computedIncludeCandidates are the files an include with a computed path
// may be allowed to run: the `.inc` and `.php` files directly in the
// directory the program runs in and in the one the file being compiled
// sits in, and in any immediate subdirectory of those whose name a string
// literal of the program spells followed by `/`. A directory with more
// than maxIncludeCandidates such files contributes none -- the build does
// not compile a whole test suite on the chance one is named -- and neither
// does the file being compiled itself. Each is a real path.
//
// Nothing here decides what runs: the run-time check (includeComputed)
// runs one only when the path the program computes resolves to it and
// its content is still what was compiled.
func computedIncludeCandidates(prog *gogen.Program) []string {
	const maxIncludeCandidates = 16
	literals := map[string]bool{}
	for _, fn := range everyBody(prog) {
		for _, b := range fn.Blocks {
			for _, in := range b.Insts {
				for _, o := range in.Operands {
					if v, err := strconv.Unquote(strings.TrimSpace(o)); err == nil && v != "" {
						literals[v] = true
					}
				}
				// A call's arguments are its ArgList: `"write_var"($d, "x/")`.
				for _, q := range quotedInArgList.FindAllString(in.ArgList, -1) {
					if v, err := strconv.Unquote(q); err == nil && v != "" {
						literals[v] = true
					}
				}
			}
		}
	}
	named := func(dir string) bool {
		for l := range literals {
			if strings.Contains(l, dir+"/") {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{}
	var out []string
	scan := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		var here []string
		for _, ent := range entries {
			name := ent.Name()
			if !strings.HasSuffix(name, ".inc") && !strings.HasSuffix(name, ".php") {
				continue
			}
			real, err := filepath.EvalSymlinks(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			if abs, err := filepath.Abs(real); err == nil {
				real = abs
			}
			if fi, err := os.Stat(real); err != nil || !fi.Mode().IsRegular() || real == prog.MainReal {
				continue
			}
			// **The harvested library's own sources are not candidates.**
			// They are written into the work directory the case file is
			// extracted to, and compiling them in made every library body a
			// program function: its calls to `PHP2Go\Internal` leaves the
			// compiled runtime lacks became mandatory, and the file was
			// refused (offsets/*_container_offset_behaviour, bug67436).
			if harvestedLibrary != nil && harvestedLibrary.written[real] {
				continue
			}
			here = append(here, real)
		}
		// **The limit is per real directory**, not per directory scanned.
		// A `.phpt` case's work directory holds symlinks to its siblings,
		// and they accumulate across the cases of one run: counted
		// together, bug67436's three files drowned among fifty-odd
		// Zend/tests links and the whole directory was dropped, so the
		// same file passed alone and was refused in a batch.
		byDir := map[string][]string{}
		for _, r := range here {
			byDir[filepath.Dir(r)] = append(byDir[filepath.Dir(r)], r)
		}
		for _, r := range here {
			if len(byDir[filepath.Dir(r)]) > maxIncludeCandidates || seen[r] {
				continue
			}
			seen[r] = true
			out = append(out, r)
		}
	}
	roots := []string{includeDir}
	if prog.MainReal != "" {
		roots = append(roots, filepath.Dir(prog.MainReal))
	}
	if includeMainAbs != "" {
		roots = append(roots, filepath.Dir(includeMainAbs))
	}
	scanned := map[string]bool{}
	for _, root := range roots {
		if root == "" || scanned[root] {
			continue
		}
		scanned[root] = true
		scan(root)
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			sub := filepath.Join(root, ent.Name())
			if fi, err := os.Stat(sub); err != nil || !fi.IsDir() || !named(ent.Name()) {
				continue
			}
			scan(sub)
		}
	}
	sort.Strings(out)
	return out
}

// replayableDiagnostic reports whether a candidate's compile diagnostics
// are only ones an include replays and then runs past: warnings, notices
// and deprecations. The engine raises them each time it compiles the file
// -- every `include`, not an `include_once` that finds it done -- through
// the error handler, before any of the file's code (probed); runInclude
// replays IncludedFile.Diagnostic at that point. Anything else (a fatal, a
// parse error, a line this cannot read) keeps the file out.
func replayableDiagnostic(text string) bool {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		level, _, ok := strings.Cut(l, ": ")
		if !ok || (level != "Warning" && level != "Notice" && level != "Deprecated") {
			return false
		}
	}
	return true
}

// addCandidateInclude compiles one computedIncludeCandidates file into the
// program. A file that does not compile, or that declares a class or a
// function the program already has under that name, is left out (ok
// false) rather than guessed at: an include that reaches it stays a gap.
// One that passes those checks and then fails to lower has already been
// partly added, so the whole program is refused (why non-empty).
func addCandidateInclude(lw *lower.Lowerer, prog *gogen.Program, real string) (*lower.Function, bool, string) {
	unit, err := includeEngine.CompileUnit(real)
	if err != nil {
		return nil, false, ""
	}
	encoded := ir.EncodeUnit(unit)
	if encoded.GetFile() == nil || encoded.GetFile().GetUsesThis() || !replayableDiagnostic(encoded.GetCompileDiagnostic()) {
		return nil, false, ""
	}
	for _, c := range encoded.GetClasses() {
		if programClass(prog, strings.ToLower(c.GetName())) != nil {
			return nil, false, ""
		}
	}
	have := map[string]bool{}
	for _, fn := range prog.Functions {
		have[strings.ToLower(fn.Name)] = true
	}
	for k := range prog.Closures {
		if rest, ok := strings.CutPrefix(k, "include:"); ok {
			if i := strings.LastIndex(rest, ":"); i >= 0 {
				have[rest[i+1:]] = true
			}
		}
	}
	for _, oa := range encoded.GetFunctions() {
		if have[strings.ToLower(oa.GetName())] {
			return nil, false, ""
		}
	}
	main, why, ok := addIncluded(lw, encoded, prog, real, real)
	if !ok {
		return nil, false, why
	}
	return main, true, ""
}

// computedEval compiles in an eval whose string is computed: **the strings
// the engine evaluated at that site** when it ran the file (evalRecords),
// each compiled under the site's name as a literal eval's is. The program
// picks the body whose source is the string it computed, byte for byte, so
// a string the engine's run never produced -- the program took another path
// -- is still refused at run time rather than guessed at (gogen.EvalCase).
//
// A string that does not compile is recorded as what it does instead: the
// ParseError thrown where the eval runs, or the fatal. Code that uses $this
// is left out, as a literal eval's is, and so refused.
func computedEval(lw *lower.Lowerer, prog *gogen.Program, in *lower.Inst, name, scope string, done map[string]bool, queue *[]*lower.Function) (string, bool) {
	codes := evalRecords()[name]
	if len(codes) == 0 {
		return "", true
	}
	var cases []gogen.EvalCase
	for _, code := range codes {
		c := gogen.EvalCase{Source: code}
		// The same string at the same site is the same body, whichever
		// site on that line or pass over it asked.
		key := fmt.Sprintf("%s#%x", name, sha256.Sum256([]byte(code)))
		if !done[key] {
			unit, err := includeEngine.CompileEvalString(code, name)
			if err != nil {
				var ce *zend.CompileError
				if !errors.As(err, &ce) {
					continue
				}
				// **A thrown CompileError is not a fatal**, though the
				// diagnostic reads "Fatal error:" all the same: the eval's
				// caller can catch it (bug75218).
				m := evalParseError.FindStringSubmatch(ce.Diagnostic)
				if m == nil && ce.Thrown != "" {
					m = evalThrownError.FindStringSubmatch(ce.Diagnostic)
				}
				switch {
				case m != nil && (ce.Thrown != "" || strings.HasPrefix(strings.TrimSpace(ce.Diagnostic), "Parse error: ")):
					c.Thrown = ce.Thrown
					if c.Thrown == "" {
						c.Thrown = "ParseError"
					}
					c.ParseName = gogen.TrimBuildPath(m[2], includeMainAbs, includeMainRel)
					c.ParseLine, c.ParseMsg = m[3], m[1]
				case ce.Thrown == "" && strings.HasPrefix(strings.TrimSpace(ce.Diagnostic), "Fatal error: "):
					c.CompileFatal = gogen.TrimBuildPath(ce.Diagnostic, includeMainAbs, includeMainRel)
				default:
					continue
				}
				cases = append(cases, c)
				continue
			}
			encoded := ir.EncodeUnit(unit)
			if encoded.GetFile().GetUsesThis() {
				continue
			}
			before := make(map[string]*lower.Function, len(prog.Closures))
			for k, fn := range prog.Closures {
				before[k] = fn
			}
			scopes := make(map[string]string, len(prog.ClosureScope))
			for k, v := range prog.ClosureScope {
				scopes[k] = v
			}
			params := make(map[string][][]gogen.Attribute, len(prog.ParamAttributes))
			for k, v := range prog.ParamAttributes {
				params[k] = v
			}
			classes := len(prog.Classes)
			main, why, ok := addIncluded(lw, encoded, prog, key, name)
			if !ok {
				return why, false
			}
			rekeyEvalClosures(prog, main, before, scopes, params, key)
			// Keyed apart, but the code is the site's: its classes are
			// declared in the file the engine names.
			for _, cl := range prog.Classes[classes:] {
				cl.File = name
			}
			prog.Includes[len(prog.Includes)-1].Scope = scope
			done[key] = true
			*queue = append(*queue, main)
			for k, cl := range prog.Closures {
				if _, had := before[k]; !had {
					*queue = append(*queue, cl)
				}
			}
		}
		c.Target = key
		cases = append(cases, c)
	}
	if prog.EvalSites == nil {
		prog.EvalSites = map[string][]gogen.EvalCase{}
	}
	id := strconv.Itoa(len(prog.EvalSites))
	prog.EvalSites[id] = cases
	in.Attrs = append(in.Attrs, "evalsite="+id)
	return "", true
}

// rekeyEvalClosures gives the closures of one eval'd string keys of their
// own. Every string evaluated at a site is compiled under the site's name,
// so their closures are keyed alike -- `{main}#0 {closure:NAME:1}` -- and
// the later one's replaced the earlier's: each eval returned the last
// string's closure (type_checking_weak). The earlier one is put back, and
// the later renamed, with the references its own bodies make to it.
func rekeyEvalClosures(prog *gogen.Program, main *lower.Function, before map[string]*lower.Function, scopes map[string]string, params map[string][][]gogen.Attribute, key string) {
	renamed := map[string]string{}
	for k, fn := range prog.Closures {
		if old, had := before[k]; had && old != fn {
			renamed["@"+k] = "@" + k + " " + key
		}
	}
	if len(renamed) == 0 {
		return
	}
	// The bodies this string added: its main, and every closure or
	// function it declared -- the new keys, and the ones it overwrote.
	bodies := []*lower.Function{main}
	for k, fn := range prog.Closures {
		if old, had := before[k]; !had || old != fn {
			bodies = append(bodies, fn)
		}
	}
	for old := range renamed {
		k := strings.TrimPrefix(old, "@")
		nk := strings.TrimPrefix(renamed[old], "@")
		prog.Closures[nk], prog.Closures[k] = prog.Closures[k], before[k]
		prog.ClosureScope[nk], prog.ClosureScope[k] = prog.ClosureScope[k], scopes[k]
		if list, ok := prog.ParamAttributes[k]; ok {
			prog.ParamAttributes[nk] = list
			if prev, had := params[k]; had {
				prog.ParamAttributes[k] = prev
			} else {
				delete(prog.ParamAttributes, k)
			}
		}
	}
	for _, fn := range bodies {
		for _, b := range fn.Blocks {
			for i := range b.Insts {
				for j, op := range b.Insts[i].Operands {
					if nk, ok := renamed[strings.TrimSpace(op)]; ok {
						b.Insts[i].Operands[j] = nk
					}
				}
			}
		}
	}
}

// evalRecordsOf caches evalRecords for the file being graded; the file's
// setup clears it.
var evalRecordsOf struct {
	records map[string][]string
}

// evalProfileRecords are supplied explicitly by -eval-profile. Ordinary
// compilation has no computed eval inputs and keeps those sites unsupported.
var evalProfileRecords []zend.EvalRecord

// evalRecords maps the supplied profile's names to this compilation's source
// spelling. Each string is kept once, in the order first observed.
func evalRecords() map[string][]string {
	if evalRecordsOf.records != nil {
		return evalRecordsOf.records
	}
	out := map[string][]string{}
	evalRecordsOf.records = out
	seen := map[string]bool{}
	for _, r := range evalProfileRecords {
		if !strings.HasSuffix(r.Name, " : eval()'d code") {
			continue
		}
		name := r.Name
		if rest, ok := strings.CutPrefix(name, includeMainRel+"("); ok {
			name = includeMainAbs + "(" + rest
		}
		if seen[name+"\x00"+r.Code] {
			continue
		}
		seen[name+"\x00"+r.Code] = true
		out[name] = append(out[name], r.Code)
	}
	return out
}

// addIncluded lowers an included file into the program and returns its body.
// `path` identifies the body; `file` is the name its code reports, which is
// the same but for a second eval on one line (evalKeys).
//
// The body's **prologue is what compiling the file does** when the include
// runs: its functions are declared (as a nested declaration is, so a call
// before the include is `Call to undefined function`), then the classes the
// engine bound at compile time, and its variables are bound to the file
// body's, as `global` binds them -- the file runs at global scope.
func addIncluded(lw *lower.Lowerer, encoded *pb.Unit, prog *gogen.Program, path, file string) (*lower.Function, string, bool) {
	f := encoded.GetFile()
	if f == nil {
		return nil, "include of " + path + " with no body", false
	}
	lw.GlobalScope = true
	main, err := lw.Lower(f)
	lw.GlobalScope = false
	if err != nil {
		return nil, "lower include " + filepath.Base(path) + ": " + short(err.Error()), false
	}
	record("{include:"+path+"}", main)
	if why, ok := collectClosures(lw, f, prog, ""); !ok {
		return nil, why, false
	}
	var prologue []lower.Inst
	for _, oa := range encoded.GetFunctions() {
		if oa.GetAbstract() {
			continue
		}
		fn, err := lw.Lower(oa)
		if err != nil {
			return nil, "lower " + oa.GetName() + ": " + short(err.Error()), false
		}
		record(strings.ToLower(oa.GetName()), fn)
		key := "include:" + path + ":" + strings.ToLower(oa.GetName())
		prog.Closures[key] = fn
		prog.ClosureScope[key] = ""
		if attrs := attributesOf(oa.GetAttributes()); len(attrs) > 0 {
			if prog.FunctionAttributes == nil {
				prog.FunctionAttributes = map[string][]gogen.Attribute{}
			}
			prog.FunctionAttributes[strings.ToLower(oa.GetName())] = attrs
		}
		addParamAttributes(prog, strings.ToLower(oa.GetName()), oa.GetArgs())
		if why, ok := collectClosures(lw, oa, prog, ""); !ok {
			return nil, why, false
		}
		prologue = append(prologue, lower.Inst{Opcode: "declare_function", Operands: []string{"@" + key}, Attrs: []string{"at_declaration"}})
	}
	declared := map[string]bool{}
	for _, b := range main.Blocks {
		for _, in := range b.Insts {
			if in.Opcode == "declare_class" && len(in.Operands) > 0 {
				if n, err := strconv.Unquote(strings.TrimSpace(in.Operands[0])); err == nil {
					declared[strings.ToLower(n)] = true
				}
			}
		}
	}
	// **A class the program already has, declared the same way**, is not
	// added again: the file is the program's own included into itself
	// (bug35634), or it repeats an empty declaration it guards with
	// class_exists (no_early_binding_if_already_declared). Its declarations
	// here become redeclarations, the engine's `Cannot redeclare` fatal if
	// one is reached while the class exists.
	redeclared := map[string]bool{}
	for _, c := range encoded.GetClasses() {
		low := strings.ToLower(c.GetName())
		if kept := programClass(prog, low); kept != nil {
			if h, ok := kept.Harvested.(*pb.Class); ok && kept.File == "" && sameDeclaration(h, c) {
				redeclared[low] = true
				if !declared[low] {
					prologue = append(prologue, lower.Inst{Opcode: "declare_class", Operands: []string{strconv.Quote(c.GetName())}, Attrs: []string{"redeclare"}})
				}
				continue
			}
		}
		if why, ok := addClass(lw, c, prog); !ok {
			return nil, why, false
		}
		prog.Classes[len(prog.Classes)-1].File = file
		if !declared[low] {
			prologue = append(prologue, lower.Inst{Opcode: "declare_class", Operands: []string{strconv.Quote(c.GetName())}})
		}
	}
	for _, b := range main.Blocks {
		for i := range b.Insts {
			in := &b.Insts[i]
			if in.Opcode == "declare_class" && len(in.Operands) > 0 {
				if n, err := strconv.Unquote(strings.TrimSpace(in.Operands[0])); err == nil && redeclared[strings.ToLower(n)] {
					in.Attrs = append(in.Attrs, "redeclare")
				}
			}
		}
	}
	if len(main.Blocks) > 0 {
		main.Blocks[0].Insts = append(prologue, main.Blocks[0].Insts...)
	}
	hash, _ := php.ContentHash(path)
	prog.Includes = append(prog.Includes, &gogen.IncludedFile{Path: path, Hash: hash, Main: main,
		Diagnostic: gogen.TrimBuildPath(encoded.GetCompileDiagnostic(), includeMainAbs, includeMainRel)})
	return main, "", true
}

// programClass is the program's class of that lowercased name, or nil.
func programClass(prog *gogen.Program, low string) *gogen.Class {
	for _, c := range prog.Classes {
		if strings.ToLower(c.Name) == low {
			return c
		}
	}
	return nil
}

// sameDeclaration reports two harvested declarations of one class that are
// the same: the same source, or -- with no methods, which carry positions --
// equal but for where they were written.
func sameDeclaration(a, b *pb.Class) bool {
	if a.GetFilename() == b.GetFilename() && a.GetLineStart() == b.GetLineStart() {
		return true
	}
	if len(a.GetMethods()) > 0 || len(b.GetMethods()) > 0 {
		return false
	}
	x, y := proto.Clone(a).(*pb.Class), proto.Clone(b).(*pb.Class)
	for _, c := range []*pb.Class{x, y} {
		c.Filename, c.LineStart, c.LineEnd = "", 0, 0
	}
	return proto.Equal(x, y)
}

// addClass lowers one class of a unit into the program: its constants,
// properties and hooks, and its methods.
func addClass(lw *lower.Lowerer, c *pb.Class, prog *gogen.Program) (string, bool) {
	cl := &gogen.Class{
		Name:                   c.GetName(),
		Harvested:              c,
		Parent:                 c.GetParent(),
		Abstract:               c.GetAbstract(),
		Final:                  c.GetFinal(),
		Attributes:             attributesOf(c.GetAttributes()),
		Interfaces:             c.GetInterfaces(),
		Traits:                 c.GetTraits(),
		TraitExcludes:          traitExcludes(c),
		TraitAliases:           traitAliases(c),
		AllowDynamicProperties: c.GetAllowDynamicProperties(),
		Readonly:               c.GetReadonly(),
		LineStart:              c.GetLineStart(),
		LineEnd:                c.GetLineEnd(),
		DocComment:             c.GetDocComment(),
		Methods:                map[string]*lower.Function{},
		StaticMethods:          map[string]*lower.Function{},
	}
	cl.EnumBacking = c.GetEnumBackingType()
	cl.IsEnum = c.GetKind() == pb.Class_KIND_ENUM
	cl.IsTrait = c.GetKind() == pb.Class_KIND_TRAIT
	cl.IsInterface = c.GetKind() == pb.Class_KIND_INTERFACE
	for _, k := range c.GetConstants() {
		cl.ConstOrder = append(cl.ConstOrder, k.GetName())
		if k.GetIsCase() {
			// A case's *value* is the backing value, not the case object:
			// the object is built by the runtime. A pure enum's case has
			// none at all.
			v := ""
			var expr *pb.Literal
			if k.GetHasValue() {
				v = lower.LiteralText(k.GetValue())
				if v == "" || strings.HasPrefix(v, "<") {
					// **A constant expression the compiler could not fold**
					// -- `case C = I::A`, `case E = A::B->name` -- is kept
					// as its tree and evaluated when the enum is first
					// used, as the engine does; anything else stays
					// refused.
					if k.GetValue().GetKind() != pb.Literal_KIND_EXPR {
						return "enum case value " + v, false
					}
					v, expr = "", k.GetValue()
				}
			}
			cl.EnumCases = append(cl.EnumCases, gogen.ClassConst{
				Name: k.GetName(), Value: v, Visibility: k.GetVisibility(),
				Literal:               expr,
				Deprecated:            k.GetDeprecated(),
				DeprecatedMessage:     k.GetDeprecatedMessage().GetStringValue(),
				DeprecatedMessageExpr: k.GetDeprecatedMessage(),
				DeprecatedSince:       k.GetDeprecatedSince().GetStringValue(),
				Attributes:            attributesOf(k.GetAttributes()),
			})
			continue
		}
		if !k.GetHasValue() {
			// A deferred initializer -- one referring to another constant --
			// is left as an AST and evaluated on first use, so there is
			// nothing to record.
			return "deferred class constant " + k.GetName(), false
		}
		// A **deferred** initializer has no renderable text: the compiler
		// kept it as an AST. The tree travels instead, and the emitter
		// decides whether it can evaluate it.
		t := lower.LiteralText(k.GetValue())
		if t == "" || strings.HasPrefix(t, "<") {
			t = ""
		}
		cl.Constants = append(cl.Constants, gogen.ClassConst{
			Name: k.GetName(), Value: t, Visibility: k.GetVisibility(),
			Deprecated:            k.GetDeprecated(),
			DeprecatedMessage:     k.GetDeprecatedMessage().GetStringValue(),
			DeprecatedMessageExpr: k.GetDeprecatedMessage(),
			DeprecatedSince:       k.GetDeprecatedSince().GetStringValue(),
			Literal:               k.GetValue(),
			Type:                  k.GetType(),
			DeclaringClass:        k.GetDeclaringClass(),
			Final:                 k.GetFinal(),
			Attributes:            attributesOf(k.GetAttributes()),
			DocComment:            k.GetDocComment(),
		})
	}
	// A magic accessor means the class cannot be reduced to plain field
	// access -- `ir.proto` carries these flags for exactly that reason. The
	// emitter has no dispatch for them, so emitting the class as plain
	// fields reads the *real* property where PHP would have called the
	// hook: `private $h = 7` with a `__get` printed `7` instead of the
	// hook's answer. A wrong answer, not a missing feature, which is the
	// property-hook defect one member kind over.
	// A **readonly class** is not a feature of its own: the engine marks
	// every declared property readonly and forbids dynamic ones, and both
	// of those are already modelled per property. Measured:
	//
	//	readonly class R { public function __construct(public int $x) {} }
	//	$r->x = 2   Error: Cannot modify readonly property R::$x
	//	$r->y = 3   Error: Cannot create dynamic property R::$y
	//
	// The first is the ordinary readonly rule and the second is what
	// AllowDynamicProperties already decides, so the refusal was covering
	// nothing the class table could not say.
	readonlyClass := c.GetReadonly()
	for _, pr := range c.GetProperties() {
		// PHP 8.4 asymmetric visibility: `public private(set) $p` reads
		// publicly and writes only from inside. Dropped, every write
		// succeeded.
		//
		// A **readonly property always reports `protected(set)`** -- the
		// engine models readonly as asymmetric visibility, which is why the
		// diagnostic says `Cannot modify protected(set) readonly property`.
		// That implied one is covered by the readonly rules, so only an
		// *explicit* asymmetry on a non-readonly property is still refused.

		var getHook, setHook *lower.Function
		var getHookAttrs, setHookAttrs []gogen.Attribute
		var setHookParamAttrs [][]gogen.Attribute
		if h := pr.GetSetHook(); h != nil {
			setHookAttrs = attributesOf(h.GetAttributes())
			for i, a := range h.GetArgs() {
				if attrs := attributesOf(a.GetAttributes()); len(attrs) > 0 {
					for len(setHookParamAttrs) <= i {
						setHookParamAttrs = append(setHookParamAttrs, nil)
					}
					setHookParamAttrs[i] = attrs
				}
			}
		}
		if h := pr.GetGetHook(); h != nil {
			getHookAttrs = attributesOf(h.GetAttributes())
		}
		if h := pr.GetGetHook(); h != nil {
			fn, err := lw.Lower(h)
			if err != nil {
				return "lower hook " + h.GetName() + ": " + short(err.Error()), false
			}
			if why, ok := collectClosures(lw, h, prog, c.GetName()); !ok {
				return why, false
			}
			// **A hook that yields is a generator**, and its body is a
			// state machine like any generator method's (generator_hook).
			if isGeneratorBody(fn) {
				if err := lower.Resumable(fn, nil); err != nil {
					return "cps hook " + h.GetName() + ": " + short(err.Error()), false
				}
			}
			specialize(fn)
			getHook = fn
		}
		if h := pr.GetSetHook(); h != nil {
			fn, err := lw.Lower(h)
			if err != nil {
				return "lower hook " + h.GetName() + ": " + short(err.Error()), false
			}
			if why, ok := collectClosures(lw, h, prog, c.GetName()); !ok {
				return why, false
			}
			specialize(fn)
			setHook = fn
		}
		// An **inherited static property is the same slot**, not a copy:
		// the engine stores its default as an indirection to the declaring
		// class's entry, which arrives here as `KIND_OTHER`. Rendering it
		// is neither possible nor wanted -- the owner holds the value -- so
		// the default is read only from the class that declared it.
		inheritedStatic := pr.GetStatic() && pr.GetDeclaringClass() != "" &&
			!strings.EqualFold(pr.GetDeclaringClass(), c.GetName())
		d := ""
		var deferred *pb.Literal
		if pr.GetHasDefault() && !inheritedStatic {
			d = lower.LiteralText(pr.GetDefaultValue())
			if d == "" || strings.HasPrefix(d, "<") {
				d, deferred = "", pr.GetDefaultValue()
			}
		}
		slot := gogen.Prop{
			Name:                   pr.GetName(),
			Visibility:             pr.GetVisibility(),
			Type:                   pr.GetType(),
			Class:                  pr.GetDeclaringClass(),
			Readonly:               pr.GetReadonly() || readonlyClass,
			SetVisibility:          setVisibilityOf(pr),
			SetFlag:                pr.GetSetVisibility(),
			Final:                  pr.GetFinal(),
			DocComment:             pr.GetDocComment(),
			Default:                d,
			DefaultLiteral:         deferred,
			GetHook:                getHook,
			SetHook:                setHook,
			AbstractGet:            pr.GetGetHook().GetAbstract(),
			AbstractSet:            pr.GetSetHook().GetAbstract(),
			GetHookAttributes:      getHookAttrs,
			SetHookAttributes:      setHookAttrs,
			SetHookParamAttributes: setHookParamAttrs,
			Virtual:                pr.GetVirtual(),
			Order:                  pr.GetOrder(),
			Attributes:             attributesOf(pr.GetAttributes()),
		}
		if pr.GetStatic() {
			cl.Statics = append(cl.Statics, slot)
			continue
		}
		cl.Props = append(cl.Props, slot)
	}
	for _, m := range c.GetMethods() {
		// An abstract method has no body to emit, but the name is still
		// reachable: `C::foo()` on an abstract class and `parent::inst()`
		// both find one, and PHP answers `Cannot call abstract method`
		// rather than `Call to undefined method`. The spelling is kept
		// because that message quotes the **declaration's**, not the call
		// site's -- the opposite of the visibility refusals.
		// The declaration order, which the maps below cannot keep and
		// `get_class_methods` reports. Recorded before the abstract arm
		// returns, because an abstract declaration is still a method name.
		cl.MethodOrder = append(cl.MethodOrder, m.GetName())
		if attrs := attributesOf(m.GetAttributes()); len(attrs) > 0 {
			if cl.MethodAttributes == nil {
				cl.MethodAttributes = map[string][]gogen.Attribute{}
			}
			cl.MethodAttributes[strings.ToLower(m.GetName())] = attrs
		}
		addParamAttributes(prog, strings.ToLower(c.GetName()+"::"+m.GetName()), m.GetArgs())
		if m.GetAbstract() {
			if cl.AbstractMethods == nil {
				cl.AbstractMethods = map[string]string{}
				cl.AbstractSigs = map[string]gogen.AbstractSig{}
			}
			cl.AbstractMethods[strings.ToLower(m.GetName())] = m.GetName()
			cl.AbstractSigs[strings.ToLower(m.GetName())] = abstractSig(m)
			continue
		}
		fn, err := lw.Lower(m)
		if err != nil {
			return "lower " + c.GetName() + "::" + m.GetName() + ": " + short(err.Error()), false
		}
		record(strings.ToLower(c.GetName()+"::"+m.GetName()), fn)
		if why, ok := collectClosures(lw, m, prog, c.GetName()); !ok {
			return why, false
		}
		if m.GetStatic() {
			cl.StaticMethods[strings.ToLower(m.GetName())] = fn
			continue
		}
		cl.Methods[strings.ToLower(m.GetName())] = fn
	}
	prog.Classes = append(prog.Classes, cl)
	return "", true
}

// markLifetimes runs the escape analysis once every body exists.
//
// It has to be a post-pass rather than part of the per-body walk, because
// `leaksThis` is a **whole-unit** question: `ConfinedObjects` asks it about a
// method *name*, and the body answering that name may not have been lowered
// yet when the first `new` site is examined.
//
// The name is all it gets, so two classes with a method of the same name fold
// together and the answer is "leaks if any of them does". That is the
// fail-closed direction: a name wrongly called leaking costs a missed claim,
// while a name wrongly called safe frees a live object.
func markLifetimes(prog *gogen.Program) {
	// The receiver summary is a fixpoint over the unit's methods: a method
	// calling `$this->m()` leaks only if `m` does (lower.MethodThisLeaks).
	bodyLeaks, methodLeaks := prog.ThisLeaks()
	// Static methods count: `$o->m()` reaches a static `m` as surely as an
	// instance one, and a static method of an engine method's name answered
	// by the engine's rule would call a body that stores its argument one
	// that keeps nothing.
	leaks := map[string]bool{}
	for _, c := range prog.Classes {
		for _, table := range []map[string]*lower.Function{c.Methods, c.StaticMethods} {
			for name := range table {
				if methodLeaks(strings.ToLower(name)) {
					leaks[strings.ToLower(name)] = true
				}
			}
		}
	}
	// An unknown name leaks. A method this unit never declared -- one reached
	// through an interface, a parent it does not compile, or `__call` -- has no
	// body here to summarise.
	known := map[string]bool{}
	for _, c := range prog.Classes {
		for _, table := range []map[string]*lower.Function{c.Methods, c.StaticMethods} {
			for name := range table {
				known[strings.ToLower(name)] = true
			}
		}
	}
	// Per-class, for the constructor. Keyed by class because every constructor
	// shares one name, so the name-keyed summary above would fold them all
	// together and one leaking constructor would refuse every one.
	//
	// **The constructor that runs is the inherited one** when a class has
	// none of its own, so the chain is walked; a trait may supply one this
	// cannot see, which counts as leaking.
	prog.CtorLeaksThis = map[string]bool{}
	byName := map[string]*gogen.Class{}
	for _, c := range prog.Classes {
		byName[strings.ToLower(c.Name)] = c
	}
	for _, c := range prog.Classes {
		for cur, depth := c, 0; cur != nil && depth < len(prog.Classes); cur, depth = byName[strings.ToLower(strings.TrimPrefix(cur.Parent, "\\"))], depth+1 {
			if body, ok := cur.Methods["__construct"]; ok {
				prog.CtorLeaksThis[strings.ToLower(c.Name)] = bodyLeaks(body)
				break
			}
			// **A trait supplies one only if it declares one.** Every
			// trait compiled here is looked into (and the traits it uses);
			// one that is not, or an `as` alias that could name a method
			// `__construct`, counts as leaking. A trait with no constructor
			// leaves the question to the parent, as the class's own
			// absence does -- claiming `ViaTrait` with only a trait
			// `__destruct` back (no_destructor_flag).
			if len(cur.Traits) > 0 {
				bodies, unknown := traitCtors(cur, byName)
				if unknown || len(bodies) > 0 {
					leaks := unknown
					for _, body := range bodies {
						leaks = leaks || bodyLeaks(body)
					}
					prog.CtorLeaksThis[strings.ToLower(c.Name)] = leaks
					break
				}
			}
		}
	}
	// Reading a property can run code: a compiled `__get` or get hook. The
	// read is asked about as `prop.get`, and leaks if any of those does.
	propReadLeaks := false
	for _, c := range prog.Classes {
		if body, ok := c.Methods["__get"]; ok && bodyLeaks(body) {
			propReadLeaks = true
		}
		for _, p := range c.Props {
			if p.GetHook != nil && bodyLeaks(p.GetHook) {
				propReadLeaks = true
			}
		}
	}
	// Writing one can too: a compiled `__set` or set hook, asked about as
	// `prop.set`.
	propWriteLeaks := false
	for _, c := range prog.Classes {
		if body, ok := c.Methods["__set"]; ok && bodyLeaks(body) {
			propWriteLeaks = true
		}
		for _, p := range c.Props {
			if p.SetHook != nil && bodyLeaks(p.SetHook) {
				propWriteLeaks = true
			}
		}
	}
	// **A fiber's body can reach its own Fiber** through
	// `Fiber::getCurrent()`, and store it wherever it likes. Only a program
	// that never asks may take the Fiber surface as keeping nothing.
	fiberSelfReachable := false
	for _, b := range everyBody(prog) {
		// `Fiber::getCurrent()` lowers to `fiber.current()`; the name
		// survives only in a callable string or a dynamic call.
		if b == nil {
			continue
		}
		if text := strings.ToLower(b.String()); strings.Contains(text, "getcurrent") || strings.Contains(text, "fiber.current") {
			fiberSelfReachable = true
			break
		}
	}
	leaksThis := func(method string) bool {
		m := strings.ToLower(method)
		if m == "prop.get" {
			return propReadLeaks
		}
		if m == "prop.set" {
			return propWriteLeaks
		}
		// **Converting an object of an exact class to a string**
		// (`string:<class>`, asked for `echo $local`): the most-derived
		// `__toString` runs with the object as `$this`, so it keeps the
		// object when that body does. A chain that declares none, down to a
		// root with no engine parent, only throws "could not be converted
		// to string" -- no call frame, so no trace argument holds it. An
		// engine ancestor, a trait, or a class this unit does not compile
		// counts as keeping it.
		if cls, ok := strings.CutPrefix(m, "string:"); ok {
			for cur, depth := byName[cls], 0; ; depth++ {
				if cur == nil || depth > len(prog.Classes) || len(cur.Traits) > 0 {
					return true
				}
				for name, body := range cur.Methods {
					if strings.EqualFold(name, "__tostring") {
						return bodyLeaks(body)
					}
				}
				parent := strings.ToLower(strings.TrimPrefix(cur.Parent, "\\"))
				if parent == "" {
					return false
				}
				cur = byName[parent]
			}
		}
		// A method the engine runs on its own (`__toString` for a string
		// conversion) is answered by the name summary, which takes an
		// undeclared one as the engine's own, keeping nothing.
		if strings.HasPrefix(m, lower.ImpliedMethod) {
			return methodLeaks(m)
		}
		if known[m] {
			// A compiled body of that name decides, whatever an engine class
			// of the same name would do. A program that declares its own
			// `getMessage` gets its own summary.
			return leaks[m]
		}
		if nonRetainingFiberMethod(m) {
			return fiberSelfReachable
		}
		return !nonRetainingEngineMethod(m)
	}
	// Whether a method keeps an object handed to it as an argument. A compiled
	// body of that name decides, exactly as leaksThis does -- a program that
	// declares its own `initializeLazyObject` is summarised from its own body.
	argKept := func(method string) bool {
		m := strings.ToLower(strings.TrimSpace(method))
		if known[m] {
			// **A compiled body means "assume it keeps".** The obvious thing
			// is to reuse `leaks[m]` here the way `leaksThis` does, and it is
			// wrong: that summary answers whether the method stores `$this`,
			// and a method can store an argument without storing its receiver.
			// There is no per-method argument summary yet -- `RetainedParams`
			// does it for functions only -- so the honest answer is the safe
			// one.
			return true
		}
		return !nonRetainingMethodArg(m)
	}
	// **The callee half of handle numbering.** `nonRetainingCallee` names
	// library functions by hand; a compiled body can be *asked*. Run to a
	// fixpoint because the answer is compositional -- `f` keeps nothing only if
	// everything `f` passes its parameter to keeps nothing.
	//
	// Iterated from the **optimistic** end -- every user function starts out
	// keeping nothing, and a pass removes the ones a real storing use refutes,
	// until nothing changes.
	//
	// That direction looks unsafe and is not, because escape is a reachability
	// property: an argument passed around a cycle of bodies that none of them
	// *stores* has still gone nowhere. `f($o) { f($o); }` keeps nothing, and
	// the pessimistic iteration -- which only ever adds proofs, so a recursive
	// body can never supply its own premise -- cannot say so. Anything that
	// does store propagates outward monotonically on the next pass, which is
	// what makes the two ends agree wherever a proof exists at all.
	named := map[string]*lower.Function{}
	for _, fn := range prog.Functions {
		if fn != nil {
			named[strings.ToLower(fn.Name)] = fn
		}
	}
	retained := map[string]map[int]bool{}
	allRetained := map[string]bool{}
	// **Resolution, not disjunction.** `callees` is the name written followed by
	// the fallback a namespaced call drops to. The first one this program
	// recognises is the one that actually runs, so it is the one that answers;
	// a namespaced name that exists nowhere is not an unknown callee, it is not
	// a callee at all.
	keepsArg := func(callees []string, param int) bool {
		for _, callee := range callees {
			name := strings.ToLower(strings.Trim(strings.TrimSpace(callee), `"`))
			if nonRetainingCallee(name) {
				return false
			}
			if _, ok := named[name]; ok {
				return allRetained[name] || retained[name][param]
			}
		}
		// No candidate has a compiled body here and none is on the allow-list:
		// an engine function this does not summarise, or a name declared
		// somewhere it cannot see. It keeps everything.
		return true
	}
	// Whether a compiled function hands back an object nobody else holds, so a
	// caller may free its result at the statement that consumed it. Same
	// optimistic iteration and for the same reason as the retention summary.
	fresh := map[string]bool{}
	// **Calling a generator function makes a new Generator**, and runs none of
	// the body: the object is fresh whatever the body does later.
	for name, fn := range named {
		if isGeneratorBody(fn) {
			fresh[name] = true
		}
	}
	// Methods are summarised by **name**, like `leaks`, because a call site
	// often cannot say which class it reaches. A name therefore answers true
	// only when *every* compiled method of that name does -- one that keeps
	// what it returns poisons the name, which is the safe direction.
	freshMethod := map[string]bool{}
	methodSeen := map[string]bool{}
	closureFresh := map[string]bool{}
	freshReturn := func(callees []string) bool {
		for _, callee := range callees {
			// **A closure literal called where it is written** is named by
			// its target, `@<parent>#<n> <name>`: its own body answers.
			if key, ok := strings.CutPrefix(strings.TrimSpace(callee), "@"); ok {
				got, seen := closureFresh[key]
				if !seen {
					fn := prog.Closures[key]
					got = fn != nil && !isGeneratorBody(fn) && lower.ReturnsFreshObject(fn, keepsArg, leaksThis, argKept)
					closureFresh[key] = got
				}
				return got
			}
			name := strings.ToLower(strings.Trim(strings.TrimSpace(callee), `"`))
			if _, ok := named[name]; ok {
				return fresh[name]
			}
			if methodSeen[name] {
				return freshMethod[name]
			}
			// An engine accessor that builds a new reflector on every call
			// -- the program declares no method of that name.
			if freshEngineMethod[name] || freshEngineFunction[name] {
				return true
			}
			// **A backtrace array is the engine's own fresh value**: the
			// SensitiveParameterValue objects in it were made for it and
			// nothing else holds them, so `var_dump(debug_backtrace())`
			// frees them at the end of the statement (sensitive_parameter).
			// disposeTemp gives back only those wrappers.
			if name == "debug_backtrace" {
				return true
			}
		}
		return false
	}
	for changed := true; changed; {
		changed = false
		for _, c := range prog.Classes {
			// **Static methods live in their own map.** `Foo::make()` is in
			// `StaticMethods`, not `Methods`, and a loop over the latter alone
			// summarised nothing for the commonest factory shape there is.
			bodies := make(map[string]*lower.Function, len(c.Methods)+len(c.StaticMethods))
			for n, b := range c.Methods {
				bodies[n] = b
			}
			for n, b := range c.StaticMethods {
				bodies[n] = b
			}
			for mname, body := range bodies {
				m := strings.ToLower(mname)
				got := lower.ReturnsFreshObject(body, keepsArg, leaksThis, argKept)
				if !methodSeen[m] {
					methodSeen[m] = true
					freshMethod[m] = got
					changed = true
					continue
				}
				if freshMethod[m] && !got {
					freshMethod[m] = false
					changed = true
				}
			}
		}
		for name, fn := range named {
			if !fresh[name] && lower.ReturnsFreshObject(fn, keepsArg, leaksThis, argKept) {
				fresh[name] = true
				changed = true
			}
			if allRetained[name] {
				continue
			}
			got, all := lower.RetainedParams(fn, keepsArg, leaksThis, argKept)
			if all {
				allRetained[name] = true
				changed = true
				continue
			}
			for i := range got {
				if !retained[name][i] {
					if retained[name] == nil {
						retained[name] = map[int]bool{}
					}
					retained[name][i] = true
					changed = true
				}
			}
		}
	}
	// **Implicit re-entry, answered for the whole unit.** `echo $o`,
	// `$o[$k]` and `$o->p` can run user code with `$o` as `$this`; which code
	// depends on the class, and the trace does not know the class. So the
	// answer is the unit's: re-entry keeps nothing only if no compiled body it
	// can reach stores `$this`, and none takes an argument it might store --
	// `__set`, `offsetSet` and a set hook do, and there is no per-method
	// argument summary. The same closed-world assumption `leaksThis` makes.
	reentryLeaks := false
	for _, c := range prog.Classes {
		for name, body := range c.Methods {
			switch strings.ToLower(name) {
			case "__set", "offsetset":
				reentryLeaks = true
			case "__tostring", "__get", "__isset", "__unset", "offsetget",
				"offsetexists", "offsetunset", "count", "getiterator",
				"current", "key", "next", "valid", "rewind":
				if bodyLeaks(body) {
					reentryLeaks = true
				}
			}
		}
		for _, p := range c.Props {
			if p.SetHook != nil || p.GetHook != nil && bodyLeaks(p.GetHook) {
				reentryLeaks = true
			}
		}
	}
	ownOpts := lower.OwnershipOptions{
		KeepsArg:  keepsArg,
		LeaksThis: leaksThis,
		ArgKept:   argKept,
		CtorLeaksThis: func(class string) bool {
			return prog.CtorLeaksThis[strings.ToLower(class)]
		},
		ReEntry: func(string) bool { return reentryLeaks },
	}
	// **Which bodies hand back something nothing else holds**, by the trace's
	// own answer, so a call to one is an allocation site in its caller. Iterated
	// from "not fresh": a body's answer can only improve as its callees'
	// do, so the fixpoint is the least one and every proof has a premise. A
	// method name is fresh only when every compiled body of that name is.
	trFresh := map[string]bool{}
	trFreshMethod := map[string]bool{}
	ownOpts.FnReturnsFresh = func(callees []string) bool {
		for _, callee := range callees {
			name := strings.ToLower(strings.Trim(strings.TrimSpace(callee), `"`))
			if _, ok := named[name]; ok {
				return trFresh[name]
			}
		}
		return false
	}
	ownOpts.MethodReturnsFresh = func(method string) bool {
		return trFreshMethod[strings.ToLower(method)]
	}
	// **Which calls take an argument by reference**, for the `preferref` sends
	// the trace would otherwise count as writes. A compiled body answers from
	// its parameter list, a builtin from its harvested signature, and a
	// method by name over every compiled body of that name -- the same
	// closed-world reading `leaksThis` makes. Anything else is by reference.
	byRefAt := func(params []string, i int) bool {
		if i < len(params) {
			return lower.ParamIsByRef(params[i])
		}
		// Past the declared list: a by-reference variadic is the only way an
		// extra argument binds, and it is the last parameter.
		return len(params) > 0 && lower.ParamIsByRef(params[len(params)-1])
	}
	methodBodies := map[string][]*lower.Function{}
	for _, c := range prog.Classes {
		for _, bodies := range []map[string]*lower.Function{c.Methods, c.StaticMethods} {
			for mname, body := range bodies {
				m := strings.ToLower(mname)
				methodBodies[m] = append(methodBodies[m], body)
			}
		}
	}
	ownOpts.ByRefArg = func(in lower.Inst, i int) bool {
		method := ""
		switch in.Opcode {
		case "call.fn":
			for _, callee := range lower.CalleeNames(in) {
				name := strings.ToLower(strings.Trim(strings.TrimSpace(callee), `"`))
				if fn, ok := named[name]; ok {
					return byRefAt(fn.Params, i)
				}
				if sig, ok := prog.BuiltinSignatures[name]; ok {
					if i < len(sig.Params) {
						return sig.Params[i].ByRef
					}
					return sig.Variadic && len(sig.Params) > 0 && sig.Params[len(sig.Params)-1].ByRef
				}
			}
			return true
		case "call.method":
			if len(in.Operands) > 1 {
				method = strings.ToLower(strings.Trim(in.Operands[1], `"`))
			}
		case "call.ctor":
			method = "__construct"
		default:
			return true
		}
		bodies := methodBodies[method]
		if len(bodies) == 0 {
			return true
		}
		for _, body := range bodies {
			if byRefAt(body.Params, i) {
				return true
			}
		}
		return false
	}
	for changed := true; changed; {
		changed = false
		for name, fn := range named {
			if !trFresh[name] && !suspends(fn) && lower.Trace(fn, ownOpts).ReturnsFresh {
				trFresh[name] = true
				changed = true
			}
		}
		all := map[string]bool{}
		for _, c := range prog.Classes {
			for _, bodies := range []map[string]*lower.Function{c.Methods, c.StaticMethods} {
				for mname, body := range bodies {
					m := strings.ToLower(mname)
					got := !suspends(body) && lower.Trace(body, ownOpts).ReturnsFresh
					if v, seen := all[m]; seen {
						all[m] = v && got
					} else {
						all[m] = got
					}
				}
			}
		}
		for m, v := range all {
			if v && !trFreshMethod[m] {
				trFreshMethod[m] = true
				changed = true
			}
		}
	}
	// A direct call to a generator function answers a Generator nobody else
	// holds; the first candidate name that is compiled decides, as in
	// freshReturn.
	makesGenerator := func(callees []string) bool {
		for _, callee := range callees {
			name := strings.ToLower(strings.TrimPrefix(strings.Trim(strings.TrimSpace(callee), `"`), "\\"))
			if fn, ok := named[name]; ok {
				return isGeneratorBody(fn)
			}
		}
		return false
	}
	caughtFresh := throwsOnlyFresh(prog)
	prog.ThrowsOnlyFresh = caughtFresh
	if caughtFresh {
		markOwnedParams(prog, named, retained, allRetained, keepsArg, leaksThis, argKept)
	}
	markCtorFreshProps(prog)
	markProxyInstances(prog, byName, bodyLeaks, func(fn *lower.Function) bool {
		return !isGeneratorBody(fn) && lower.ReturnsFreshObject(fn, keepsArg, leaksThis, argKept)
	})
	delegable := delegableIterators(prog, leaksThis)
	resurrects := destructorResurrects(prog)
	for _, fn := range gogen.Bodies(prog) {
		if !suspends(fn) {
			reached, _ := globalReach(prog, fn)
			fn.ClosureCallees = lower.ClosureCallees(fn, reached)
		}
		// **Disposables apply to the file body too**, where confinement does
		// not: `{main}`'s locals are globals, but a temporary handed to a call
		// and dropped dies at the call whatever frame it sits in.
		markDisposable(fn, leaksThis, keepsArg, freshReturn)
		if fn != prog.File {
			// `{main}`'s locals are globals; see markConfined.
			markConfined(fn, prog, leaksThis, keepsArg, argKept, delegable)
			markCatchReleases(fn, caughtFresh, leaksThis)
		}
		countEscapes(fn, fn == prog.File, leaksThis, keepsArg, argKept)
		traceOwnership(fn, fn == prog.File, ownOpts)
		// **A value consumed by nothing**, which neither of the other two
		// claims. Last, so it can take its sites back out of the frame list.
		markDiscarded(fn, prog, resurrects)
		markDiscardedResults(fn, freshReturn, leaksThis)
		markLoopOwned(fn, prog)
		// The reassignment release applies to a function body too: unlike the
		// discarded claim it hangs on an explicit write instruction, so a slot
		// promotion that erases the write costs a release rather than
		// inventing one. `$b = null` inside a function destroys what `$b` held
		// at that line, exactly as it does at file scope.
		markReassigned(fn, prog, keepsArg, leaksThis, argKept, makesGenerator, caughtFresh)
	}
	// **Static methods are functions too**, and gogen.Bodies leaves them
	// out: their locals were never claimed, so every one lived to teardown.
	// The lifetime claims run on them as on any function body; the
	// ownership trace, which only saves copies, is left alone.
	for _, c := range prog.Classes {
		for _, fn := range c.StaticMethods {
			if fn == nil {
				continue
			}
			markDisposable(fn, leaksThis, keepsArg, freshReturn)
			markConfined(fn, prog, leaksThis, keepsArg, argKept, delegable)
			markCatchReleases(fn, caughtFresh, leaksThis)
			markDiscarded(fn, prog, resurrects)
			markDiscardedResults(fn, freshReturn, leaksThis)
			markLoopOwned(fn, prog)
			markReassigned(fn, prog, keepsArg, leaksThis, argKept, makesGenerator, caughtFresh)
		}
	}
}

// programFor fills the fields grading and the census both need.
//
// **One constructor, because two drifted.** The census had a literal of its own
// and it was missing `Opcodes` -- which `constOp` reads to name the operator in
// a deferred constant expression -- so `const baz = bar + 1;` was reported as
// `unrenderable operand <lit:0>` by the census while grading compiled and
// passed the very same file. Six files of gap that were not there, pointing
// work at a bug that did not exist.
func programFor(engine *zend.Engine, reported string) *gogen.Program {
	return &gogen.Program{
		Filename:                 reported,
		Builtins:                 builtinNames,
		BuiltinSignatures:        builtinSignatures(engine),
		EngineFunctions:          engineFunctions(engine),
		EngineFunctionOrder:      engineFunctionOrder(engine),
		EngineConstants:          engineConstants(engine),
		DeprecatedConstants:      deprecatedConstants(engine),
		EngineClasses:            engineClassNames(engine),
		EngineClassAttributes:    engineClassAttributes(engine),
		EngineFunctionAttributed: engineFunctionAttributed(engine),
		EngineMethodNames:        engineMethodNames(engine),
		EngineStaticMethods:      engineStaticMethods(engine),
		EngineMethodTables:       engineMethodTables(engine),
		EngineCustomCreate:       engineCustomCreate(engine),
		EngineProperties:         engineProperties(engine),
		EngineClassConstants:     engineClassConstants(engine),
		EngineInterfaces:         engineInterfaceList(engine),
		EngineReflect:            engineReflect(engine),
		Opcodes:                  zend.OpcodeNames(),
		FusedMath:                fusedMath,
	}
}

// fusedMath mirrors -fused-math; see gogen.Program.FusedMath.
var fusedMath bool

// engineClassNames is every class the linked engine provides, harvested once.
//
// Read from the engine rather than written down, because the set depends on
// which extensions this build links -- a transcribed list would be right for
// one build and quietly wrong for every other.
func engineClassNames(engine *zend.Engine) []string {
	if cachedEngineClasses != nil {
		return cachedEngineClasses
	}
	// **Order matters here**, so not `InternalClasses`, which sorts:
	// `get_declared_classes()` reports the engine's own in registration order.
	classes, err := engine.InternalClassOrder()
	if err != nil {
		// An empty set means every unresolved name keeps its refusal, which is
		// the behaviour before this existed.
		return nil
	}
	names := make([]string, 0, len(classes))
	var interfaces []string
	for _, c := range classes {
		// **Classes only.** The harvest hands back interfaces, traits and enums
		// from the same table, and `get_declared_classes()` counts none of
		// them -- `get_declared_interfaces()` is a separate answer. Keeping
		// them gave 156 where the engine says 136.
		// An **enum is a class** and is counted; an interface and a trait are
		// not, and have their own `get_declared_*`. Keeping everything gave 156
		// where the engine says 136, and keeping only "class" gave 133.
		switch c.Kind {
		case "interface":
			interfaces = append(interfaces, c.Name)
		case "trait":
		default:
			names = append(names, c.Name)
		}
	}
	// Spellings alongside the names, from the same single harvest. Two
	// questions are answered by one table: how a frame spells an engine method
	// -- the dispatch that pushes one has only the folded form -- and **which
	// engine class declares it**, since a frame names the declaring class and
	// not the receiver's. So every method is carried, lower-case declarations
	// included: they spell themselves, but their presence is what says who
	// owns them, and `__construct` is exactly such a name.
	spellings := map[string]map[string]string{}
	statics := map[string]map[string]bool{}
	for _, c := range classes {
		for _, m := range c.EngineMethods {
			key := strings.ToLower(c.Name)
			if spellings[key] == nil {
				spellings[key] = map[string]string{}
			}
			spellings[key][strings.ToLower(m.Name)] = m.Name
			if m.Static {
				if statics[key] == nil {
					statics[key] = map[string]bool{}
				}
				statics[key][strings.ToLower(m.Name)] = true
			}
		}
	}
	cachedEngineStaticMethods = statics
	cachedEngineMethodTables = map[string][]gogen.EngineMethodSig{}
	cachedEngineCustomCreate = map[string]bool{}
	for _, c := range classes {
		key := strings.ToLower(c.Name)
		if c.CustomCreate {
			cachedEngineCustomCreate[key] = true
		}
		for _, m := range c.EngineTable {
			cachedEngineMethodTables[key] = append(cachedEngineMethodTables[key], gogen.EngineMethodSig{
				Name: m.Name, Scope: m.Scope, Return: m.ReturnType,
				Declaration: engineDeclaration(m),
				Tentative:   m.TentativeReturn, Private: m.Visibility == "private",
				Static: m.Static, Final: m.Final,
			})
		}
	}
	cachedEngineProperties = map[string][]string{}
	for _, c := range classes {
		for _, p := range c.Properties {
			if !p.Static {
				key := strings.ToLower(c.Name)
				cachedEngineProperties[key] = append(cachedEngineProperties[key], p.Name)
			}
		}
	}
	cachedEngineClasses, cachedEngineInterfaces = names, interfaces
	cachedEngineMethodNames = spellings
	// Each engine class's constants whose value the harvest has, rendered as
	// the program's own are. An enum case is an object the runtime builds, and
	// a deferred one has no text.
	cachedEngineConstants = map[string][]gogen.ClassConst{}
	for _, c := range classes {
		for _, k := range c.Constants {
			if !k.HasValue || k.Case {
				continue
			}
			t := lower.LiteralText(ir.EncodeLiteral(k.Value))
			if t == "" || strings.HasPrefix(t, "<") {
				continue
			}
			cachedEngineConstants[c.Name] = append(cachedEngineConstants[c.Name], gogen.ClassConst{
				Name: k.Name, Value: t, Visibility: k.Visibility, Type: k.Type, DeclaringClass: k.DeclaringClass,
			})
		}
	}
	return names
}

// engineMethodNames is the spelling table built by the harvest above.
func engineMethodNames(engine *zend.Engine) map[string]map[string]string {
	if cachedEngineClasses == nil {
		engineClassNames(engine)
	}
	return cachedEngineMethodNames
}

// engineStaticMethods is which of those methods are static, from the same
// harvest.
func engineStaticMethods(engine *zend.Engine) map[string]map[string]bool {
	if cachedEngineClasses == nil {
		engineClassNames(engine)
	}
	return cachedEngineStaticMethods
}

// engineInterfaceList is the same harvest read the other way: the interfaces
// the engine registers, in registration order, which is what
// `get_declared_interfaces()` lists before the program's own.
func engineInterfaceList(engine *zend.Engine) []string {
	if cachedEngineClasses == nil {
		engineClassNames(engine)
	}
	return cachedEngineInterfaces
}

// engineClassConstants is the harvest's constants of each engine class.
func engineClassConstants(engine *zend.Engine) map[string][]gogen.ClassConst {
	if cachedEngineClasses == nil {
		engineClassNames(engine)
	}
	return cachedEngineConstants
}

// engineMethodTables is the method-table harvest above.
func engineMethodTables(engine *zend.Engine) map[string][]gogen.EngineMethodSig {
	if cachedEngineClasses == nil {
		engineClassNames(engine)
	}
	return cachedEngineMethodTables
}

// engineCustomCreate is which engine classes have a create handler.
func engineCustomCreate(engine *zend.Engine) map[string]bool {
	if cachedEngineClasses == nil {
		engineClassNames(engine)
	}
	return cachedEngineCustomCreate
}

// engineDeclaration spells an internal method as the engine's
// zend_get_function_declaration does: `Scope::name(type $a = default, ...):
// return`, with an internal parameter's default as its arginfo text and
// `<default>` where it has none.
func engineDeclaration(m zend.EngineMethod) string {
	var b strings.Builder
	b.WriteString(m.Scope + "::" + m.Name + "(")
	for i, a := range m.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		if a.Type != "" {
			b.WriteString(a.Type + " ")
		}
		if a.ByRef {
			b.WriteString("&")
		}
		if a.Variadic {
			b.WriteString("...")
		}
		b.WriteString("$" + a.Name)
		if uint32(i) >= m.RequiredArgs && !a.Variadic {
			b.WriteString(" = ")
			if a.Default != "" {
				b.WriteString(a.Default)
			} else {
				b.WriteString("<default>")
			}
		}
	}
	b.WriteString(")")
	if m.ReturnType != "" {
		b.WriteString(": " + m.ReturnType)
	}
	return b.String()
}

var (
	cachedEngineMethodTables  map[string][]gogen.EngineMethodSig
	cachedEngineCustomCreate  map[string]bool
	cachedEngineConstants     map[string][]gogen.ClassConst
	cachedEngineClasses       []string
	cachedEngineMethodNames   map[string]map[string]string
	cachedEngineStaticMethods map[string]map[string]bool
	cachedEngineInterfaces    []string
	cachedEngineProperties    map[string][]string
)

// engineProperties is each engine class's instance properties by name, from
// the same harvest.
func engineProperties(engine *zend.Engine) map[string][]string {
	if cachedEngineClasses == nil {
		engineClassNames(engine)
	}
	return cachedEngineProperties
}

// lowerFile compiles and lowers one file into a Program, or reports that it
// could not. The census needs exactly what grading needs up to the point of
// emission, so they share this rather than keeping two copies that drift.
func lowerFile(engine *zend.Engine, lw *lower.Lowerer, work, path string) (*gogen.Program, string, bool) {
	if a, err := filepath.Abs(path); err == nil {
		path = a
	}
	if strings.HasSuffix(path, ".phpt") {
		t, err := phpt.Parse(path)
		if err != nil || t.SkipReason() != "" {
			return nil, "", false
		}
		extracted := filepath.Join(work, "case.php")
		if os.WriteFile(extracted, []byte(t.File), 0o644) != nil {
			return nil, "", false
		}
		path = extracted
	}
	unit, err := engine.CompileMainUnit(path)
	if err != nil {
		return nil, "", false
	}
	encoded := ir.EncodeUnit(unit)
	prog := programFor(engine, path)
	if why, ok := fillProgram(lw, encoded, prog); !ok {
		return nil, why, false
	}
	foldEngineCalls(engine, prog)
	return prog, "", true
}

// fillProgram is everything between an encoded unit and an emittable Program:
// the bodies the file declares, and the harvested library functions they reach.
//
// **One function because there are two callers**, and every time a step was
// added to only one of them it cost a measurement. `Opcodes` missing from the
// census's Program reported six files as gaps that grading compiled and passed;
// the library linking had to be added twice; and the closures a linked body
// declares were collected by `buildProgram` and not by the linker, which is
// three generated programs that named a function nobody wrote.
func fillProgram(lw *lower.Lowerer, encoded *pb.Unit, prog *gogen.Program) (string, bool) {
	prog.AutoGlobals = encoded.GetAutoGlobals()
	// Undecided sends to this unit's own functions are settled while lowering
	// it (lower.Lowerer.ByValueArg), and not while lowering the library.
	lw.ByValueArg, lw.ByRefArg = byValueArgs(encoded), byRefArgs(encoded, prog.BuiltinSignatures)
	lw.DeclaresFunction = declaresFunction(encoded)
	defer func() { lw.ByValueArg, lw.ByRefArg, lw.DeclaresFunction = nil, nil, nil }()
	if why, ok := buildProgram(lw, encoded, prog); !ok {
		return why, false
	}
	lw.ByValueArg, lw.ByRefArg, lw.DeclaresFunction = nil, nil, nil
	// A generated program cannot import `internal/userland` -- it is its own Go
	// module -- so what it reaches of the harvested library is compiled into it.
	return harvestedLibrary.link(lw, prog)
}

// byValueArgs answers, for the unit's own functions, whether a parameter is an
// ordinary by-value one: the function is declared by the unit, has no variadic
// parameter, and the position is neither by-reference nor past the list.
func byValueArgs(unit *pb.Unit) func(string, int) bool {
	params := map[string][]*pb.ArgInfo{}
	for _, oa := range unit.GetFunctions() {
		params[strings.ToLower(oa.GetName())] = oa.GetArgs()
	}
	return func(callee string, pos int) bool {
		args, ok := params[strings.ToLower(callee)]
		if !ok || pos >= len(args) {
			return false
		}
		for _, a := range args {
			if a.GetVariadic() {
				return false
			}
		}
		return !args[pos].GetByRef()
	}
}

// byRefArgs is byValueArgs' other half: position pos of a function the unit
// declares is by reference, directly or as part of a by-reference variadic.
//
// A library function answers from its harvested signature when the unit
// declares nothing of that name: `usort($a = [...], ...)` sends a value
// where the library takes a reference, which is an Error at the send.
// declaresFunction answers whether the unit declares a function by name.
func declaresFunction(unit *pb.Unit) func(string) bool {
	names := map[string]bool{}
	for _, oa := range unit.GetFunctions() {
		names[strings.ToLower(oa.GetName())] = true
	}
	return func(name string) bool { return names[strings.ToLower(strings.TrimPrefix(name, "\\"))] }
}

// preferRefBuiltins are the library functions whose by-reference
// parameters are ZEND_SEND_PREFER_REF.
var preferRefBuiltins = map[string]bool{"array_multisort": true, "extract": true}

func byRefArgs(unit *pb.Unit, builtins map[string]gogen.BuiltinSignature) func(string, int) bool {
	params := map[string][]*pb.ArgInfo{}
	for _, oa := range unit.GetFunctions() {
		params[strings.ToLower(oa.GetName())] = oa.GetArgs()
	}
	// Methods too, as `Class::method`: a static call names both halves.
	for _, c := range unit.GetClasses() {
		for _, m := range c.GetMethods() {
			params[strings.ToLower(c.GetName()+"::"+m.GetName())] = m.GetArgs()
		}
	}
	return func(callee string, pos int) bool {
		args, ok := params[strings.ToLower(callee)]
		// **Prefer-ref is not by-ref**: array_multisort's and extract's
		// parameters take a reference when given a variable and a value
		// otherwise, so a value sent there is no refusal (bug69532). The
		// harvest collapses the flag into by_ref, so the two are named --
		// the same pair warnByValue exempts.
		if !ok && preferRefBuiltins[strings.ToLower(callee)] {
			return false
		}
		if !ok {
			if sig, known := builtins[strings.ToLower(callee)]; known {
				if pos < len(sig.Params) {
					return sig.Params[pos].ByRef
				}
				return sig.Variadic && len(sig.Params) > 0 && sig.Params[len(sig.Params)-1].ByRef
			}
		}
		if !ok || len(args) == 0 {
			return false
		}
		if pos >= len(args) {
			last := args[len(args)-1]
			return last.GetVariadic() && last.GetByRef()
		}
		return args[pos].GetByRef()
	}
}

// harvestedLibrary is the userland library, loaded once per process. Nil until
// the run sets it up, which grading and the census both do before lowering
// anything -- a nil one links nothing, so a caller that forgets gets the old
// behaviour rather than a crash.
var harvestedLibrary *userlandLibrary

func run(roots []string, limit int, verbose bool, keep string) error {
	paths, err := collect(roots, limit)
	if err != nil {
		return err
	}

	engine, err := zend.Start()
	if err == nil {
		// The inference types a `const.fetch` from the value the engine
		// registered, so the table has to exist before anything is lowered --
		// not as a side effect of whichever path happens to ask for it first.
		constantTypes = engineConstants(engine)
	}
	if err != nil {
		return err
	}
	defer engine.Close()
	frameless, err := engine.FramelessFunctions()
	if err != nil {
		return err
	}
	lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless}
	lw.SkipMem2Reg = skipPromotion

	work, err := os.MkdirTemp("", "php2gotest-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	// **Harvested once.** Compiling the two libraries is a second engine parse
	// of twelve thousand lines; doing it per file would dominate the run.
	if harvestedLibrary, err = loadUserland(engine, work); err != nil {
		return err
	}
	root, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(work, "go.mod"), []byte(fmt.Sprintf(
		"module php2gotest\n\ngo 1.21\n\nrequire github.com/moriyoshi/php2go v0.0.0\n\nreplace github.com/moriyoshi/php2go => %s\n",
		root)), 0o644); err != nil {
		return err
	}
	// Seed a main.go that imports the runtime before tidying: `go mod tidy`
	// records requirements from the code it can see, and an empty directory
	// leaves the replace directive unrequired.
	if err := os.WriteFile(filepath.Join(work, "main.go"), []byte(
		"package main\n\nimport \"github.com/moriyoshi/php2go/runtime/php\"\n\nfunc main() { _ = php.Null() }\n"),
		0o644); err != nil {
		return err
	}
	// Tidy once for both kinds of emitted module. The tagged import is ignored
	// by ordinary grading builds but keeps the hybrid dependencies in go.mod.
	if err := os.WriteFile(filepath.Join(work, "hybrid_seed.go"), []byte(
		"//go:build php2go_hybrid\n\npackage main\n\nimport \"github.com/moriyoshi/php2go/runtime/hybrid\"\n\nvar _ = hybrid.EvalScoped\n"),
		0o644); err != nil {
		return err
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = work
	if out, err := tidy.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod tidy: %w\n%s", err, out)
	}

	// Refuse to produce a number if the measurement's own dependency path is
	// broken -- and only then.
	//
	// "Measure only on a green tree" is a sound default, but the *reason* is
	// that a break can reach the measurement, and whether it can is checkable.
	// Both directions are errors: measuring through a break that reaches you
	// (a half-written `runtime/php` once made 25 files fail to build and read
	// as a compiler regression), and discarding a good result because of a RED
	// somewhere it cannot reach. A generated program imports only
	// `runtime/php`, so that -- not the whole tree -- is what has to build.
	seed := exec.Command("go", "build", "-o", filepath.Join(work, "seed"), ".")
	seed.Dir = work
	seed.Env = buildEnv()
	if out, err := seed.CombinedOutput(); err != nil {
		return fmt.Errorf("the measurement's dependency path does not build, so any "+
			"number from this run would be meaningless.\nA generated program imports only "+
			"runtime/php; fix that and re-run.\n%s", out)
	}

	if emitDir != "" {
		// One line per file: where its module went, or why there is none.
		failures := 0
		for _, path := range paths {
			o, reason := one(engine, lw, work, path, keep)
			if o == passed {
				fmt.Printf("emitted\t%s\t%s\n", path, reason)
				continue
			}
			failures++
			fmt.Printf("%s\t%s\t%s\n", o, path, reason)
		}
		if failures > 0 {
			return fmt.Errorf("%d of %d files were not emitted", failures, len(paths))
		}
		return nil
	}
	counts := map[outcome]int{}
	reasons := map[string]int{}
	for i, path := range paths {
		if !verbose && i%50 == 0 && i > 0 {
			fmt.Fprintf(os.Stderr, "  %d/%d\n", i, len(paths))
		}
		o, reason := one(engine, lw, work, path, keep)
		counts[o]++
		if o != passed {
			reasons[o.String()+": "+reason]++
			if verbose {
				fmt.Printf("%-8s %s\n         %s\n", o, path, reason)
			}
		}
	}

	total := len(paths)
	fmt.Printf("\nfiles %d\n", total)
	for _, o := range []outcome{passed, failed, unsupported, errored, noreference} {
		fmt.Printf("  %-12s %5d  (%.1f%%)\n", o, counts[o], 100*float64(counts[o])/float64(max(total, 1)))
	}
	// The denominator that means something. `noreference` files were never put
	// to the compiler, so including them makes every rate a statement about the
	// oracle's reach as much as about php2go.
	graded := total - counts[noreference]
	fmt.Printf("\nof the %d files the oracle could grade:\n", graded)
	for _, o := range []outcome{passed, failed, unsupported, errored} {
		fmt.Printf("  %-12s %5d  (%.1f%%)\n", o, counts[o], 100*float64(counts[o])/float64(max(graded, 1)))
	}
	reportEscapes()
	reportArrayFacts()
	type row struct {
		r string
		n int
	}
	var rows []row
	for r, n := range reasons {
		rows = append(rows, row{r, n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].r < rows[j].r
	})
	fmt.Println("\ntop reasons")
	for i, r := range rows {
		if i >= 25 {
			fmt.Printf("  ... and %d more\n", len(rows)-25)
			break
		}
		fmt.Printf("  %5d  %s\n", r.n, r.r)
	}
	return nil
}

// runCensus counts how many files each gap appears in, rather than which gap
// stops each file first.
//
// A first-blocker table understates anything that commonly appears later: a file
// stopped by a `catch` may also call three library functions, and only the catch
// is ever counted. Deciding what to build next needs populations.
// censusTop is how many rows the census prints, from the -top flag.
var censusTop = 30

func runCensus(roots []string, limit int) error {
	paths, err := collect(roots, limit)
	if err != nil {
		return err
	}
	engine, err := zend.Start()
	if err == nil {
		// The inference types a `const.fetch` from the value the engine
		// registered, so the table has to exist before anything is lowered --
		// not as a side effect of whichever path happens to ask for it first.
		constantTypes = engineConstants(engine)
	}
	if err != nil {
		return err
	}
	defer engine.Close()
	frameless, err := engine.FramelessFunctions()
	if err != nil {
		return err
	}
	lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless}
	lw.SkipMem2Reg = skipPromotion
	work, err := os.MkdirTemp("", "php2gocensus-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	// The same library grading links, so a gap cannot be visible to one and
	// invisible to the other.
	if harvestedLibrary, err = loadUserland(engine, work); err != nil {
		return err
	}

	// A frequency table ranks *opcodes*; it cannot say how many files a
	// capability would UNBLOCK, because a file passes only when its whole gap
	// set is closed. The dump carries the per-file sets so that question can be
	// answered without re-running the corpus.
	var dump *os.File
	if censusDumpPath != "" {
		var err error
		if dump, err = os.Create(censusDumpPath); err != nil {
			return err
		}
		defer dump.Close()
	}

	files := map[string]int{}
	var seen int
	for i, path := range paths {
		if i%250 == 0 && i > 0 {
			fmt.Fprintf(os.Stderr, "  %d/%d\n", i, len(paths))
		}
		prog, why, ok := lowerFile(engine, lw, work, path)
		engine.Reset()
		if !ok {
			// A refusal is a *gap*, and dropping the file here is what let the
			// census report 964 ready files when 291 of them refused on the
			// first grading run.
			if why != "" {
				seen++
				files[why]++
				if dump != nil {
					fmt.Fprintf(dump, "%s\t%s\n", path, why)
				}
			}
			continue
		}
		seen++
		anyBuiltin := false
		var set []string
		for gap := range gogen.Gaps(prog) {
			files[gap]++
			set = append(set, gap)
			if strings.HasPrefix(gap, "library function ") {
				anyBuiltin = true
			}
		}
		if dump != nil {
			sort.Strings(set)
			fmt.Fprintf(dump, "%s\t%s\n", path, strings.Join(set, "|"))
		}
		if anyBuiltin {
			// The aggregate is the number that decides whether the standard
			// library is one gap or many: individual builtins are a long tail,
			// but a file blocked on *any* of them is blocked on the same missing
			// capability.
			files["ANY library function"]++
		}
	}
	fmt.Printf("files whose bodies lowered: %d\n", seen)
	if totalCalls > 0 {
		// What the inference is actually buying. Reported rather than assumed:
		// a specialization mechanism that never fires is inert, and the only
		// way to know is to count.
		//
		// Of the calls that *have* a narrower variant to reach. Operations with
		// only one variant are excluded: counting them measures the corpus
		// rather than the inference.
		fmt.Printf("specialized %d of %d specializable intrinsic calls (%.1f%%)\n\n",
			specializedCalls, totalCalls, 100*float64(specializedCalls)/float64(totalCalls))
		reportEscapes()
		reportArrayFacts()
	} else {
		fmt.Println()
	}
	type row struct {
		g string
		n int
	}
	var rows []row
	for g, n := range files {
		rows = append(rows, row{g, n})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].g < rows[j].g
	})
	fmt.Printf("%-46s %s\n", "gap", "files containing it")
	for i, r := range rows {
		if censusTop > 0 && i >= censusTop {
			fmt.Printf("... and %d more\n", len(rows)-censusTop)
			break
		}
		fmt.Printf("%-46s %6d\n", r.g, r.n)
	}
	return nil
}

// one grades a single file. The engine runs first: if the engine itself cannot
// run the file there is nothing to compare against, and grading our output
// against nothing would count a hole as a pass.
func one(engine *zend.Engine, lw *lower.Lowerer, work, path, keep string) (out outcome, why string) {
	// A panic in the emitter is an **emitter bug**, which is `errored` -- never
	// a gap, and never silence. Without this one panic ended the whole corpus
	// run at whatever file it reached, and the run still exited 0 with no
	// summary printed: a truncated measurement that looked like a finished one.
	defer func() {
		if r := recover(); r != nil {
			out, why = errored, fmt.Sprintf("panic: %v", r)
		}
	}()
	return grade(engine, lw, work, path, keep)
}

func grade(engine *zend.Engine, lw *lower.Lowerer, work, path, keep string) (result outcome, detail string) {
	defer engine.Reset()

	// Both arms must name the file identically. Zend resolves a path when it
	// compiles, so a diagnostic captured at compile time carries the *absolute*
	// path, while the oracle reports whatever name it was handed -- and a
	// relative path on the command line made every compile-time diagnostic
	// differ in its filename alone.
	if a, err := filepath.Abs(path); err == nil {
		path = a
	}
	// Resolve symlinks too: the engine canonicalises, so a path that differs
	// only by a symlink would not be rewritten out of the diagnostic.
	if c, err := filepath.EvalSymlinks(path); err == nil {
		path = c
	}

	// A .phpt is a test file, not a PHP file: the source is one section of it.
	// Extract it to a real file so the engine and the compiler see the same
	// bytes, rather than teaching each of them the .phpt format.
	var test *phpt.Test
	if strings.HasSuffix(path, ".phpt") {
		t, err := phpt.Parse(path)
		test = t
		if err != nil {
			return noreference, "phpt parse: " + short(err.Error())
		}
		if reason := t.SkipReason(); reason != "" {
			return noreference, "phpt skip: " + short(reason)
		}
		extracted := filepath.Join(work, "case.php")
		if err := os.WriteFile(extracted, []byte(t.File), 0o644); err != nil {
			return noreference, "phpt extract: " + short(err.Error())
		}
		linkSiblings(path, work, t.File)
		path = extracted
	}

	// What both arms call the file. Relative to the compilation root where that
	// is possible, so the build machine's layout appears in neither.
	// A `../` prefix is kept rather than treated as failure. Falling back to
	// the absolute path for anything outside the root reintroduced the exact
	// disclosure this is here to prevent -- and `../x.php` is honest: it says
	// the project is shaped that way.
	reported := path
	if r, err := filepath.Rel(sourceRoot, path); err == nil {
		reported = r
	}

	var want string
	if emitDir == "" {
		w, err := engineOutput(path, reported)
		if err != nil {
			return noreference, "engine bailed (often a fatal-compile-error test): " + short(err.Error())
		}
		// **The oracle is not the CLI**: the embedded engine defines no
		// STDIN, STDOUT or STDERR, so where the program and the oracle
		// disagree over one, there is no reference -- the answer is the
		// oracle's gap, not the program's (offsets/*,
		// exit_string_with_buffer_output). Where they agree it still passes.
		if m := cliStreamConstant.FindString(w); m != "" {
			defer func() {
				if result == failed {
					result, detail = noreference, "oracle has no CLI stream constants: "+m
				}
			}()
		}
		want = w
	}

	unit, err := engine.CompileMainUnit(path)
	if err != nil {
		// **The engine could not compile this file, and that is an answer
		// rather than a gap.** PHP's own behaviour for such a source is to
		// print the diagnostic and run nothing at all, so the artifact that
		// reproduces it is the *correct* artifact -- exactly the reasoning
		// `Program.CompileDiagnostic` already records for the case where
		// compilation succeeds and still prints.
		//
		// Bounded deliberately. A **bailout** is the engine giving up rather
		// than diagnosing, and an empty diagnostic says nothing was reported;
		// neither is an answer to replay, so both stay `unsupported`.
		// A bailout that printed a **fatal error** is the engine's answer
		// all the same: a compile-time fatal (`Cannot redeclare`) is
		// reported by bailing out, and php prints the diagnostic and runs
		// nothing -- the oracle hands back the same text.
		var ce *zend.CompileError
		if !errors.As(err, &ce) || strings.TrimSpace(ce.Diagnostic) == "" ||
			(ce.Bailed && !strings.Contains(ce.Diagnostic, "Fatal error: ")) {
			return unsupported, "compile: " + short(err.Error())
		}
		prog := programFor(engine, reported)
		prog.RootPolicy = rootPolicy
		prog.EntryFile = reported
		prog.CompileDiagnostic = gogen.TrimBuildPath(ce.Diagnostic, path, reported)
		// No file body, no functions, no classes: nothing ran, so there is
		// nothing else for the program to do once the diagnostic is out.
		src, err := gogen.Emit(prog)
		if err != nil {
			return unsupported, "emit: " + short(err.Error())
		}
		return buildAndRun(src, work, path, reported, test, want, keep)
	}
	encoded := ir.EncodeUnit(unit)
	prog := programFor(engine, reported)
	prog.RootPolicy = rootPolicy
	prog.EntryFile = reported
	prog.CompileDiagnostic = gogen.TrimBuildPath(encoded.GetCompileDiagnostic(), path, reported)
	// A class declaration the engine could not link is where the program
	// stops, with what the engine printed (zend.Unit.LinkFatal).
	if unit.LinkFatal != "" {
		prog.LinkFatal = gogen.TrimBuildPath(unit.LinkFatal, path, reported)
		prog.LinkFatalUnless = unit.LinkFatalUnless
		lw.LinkFatal, lw.LinkFatalOpline = true, unit.LinkFatalOpline
	}
	ok := true
	why := ""
	func() {
		// Includes resolve against the directory the program runs in.
		includeEngine, includeDir = engine, oracleDir(path)
		includeMainAbs, includeMainRel = path, reported
		// Every .phpt case is extracted to the same path: what the last
		// file evaluated is not this one's.
		evalRecordsOf.records = nil
		defer func() {
			lw.LinkFatal = false
			includeEngine, includeDir, includeMainAbs, includeMainRel = nil, "", "", ""
		}()
		why, ok = fillProgram(lw, encoded, prog)
	}()
	if !ok {
		return unsupported, why
	}
	foldEngineCalls(engine, prog)

	src, err := gogen.Emit(prog)
	if err != nil {
		var u *gogen.Unsupported
		if errors.As(err, &u) {
			return unsupported, "emit: " + u.Opcode
		}
		return unsupported, "emit: " + short(err.Error())
	}

	return buildAndRun(src, work, path, reported, test, want, keep)
}

// emitModule writes one program as a module of its own under emitDir.
func emitModule(src, work, path, reported string) (outcome, string) {
	dir := filepath.Join(emitDir, sanitize(path))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errored, "emit: " + err.Error()
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		data, err := os.ReadFile(filepath.Join(work, name))
		if errors.Is(err, os.ErrNotExist) && name == "go.sum" {
			// A runtime with no external dependencies tidies to no go.sum.
			continue
		}
		if err != nil {
			return errored, "emit: " + err.Error()
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return errored, "emit: " + err.Error()
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		return errored, "emit: " + err.Error()
	}
	if err := os.WriteFile(filepath.Join(dir, "hybrid.go"), []byte(hybridSource), 0o644); err != nil {
		return errored, "emit: " + err.Error()
	}
	return passed, dir
}

// buildAndRun is the half of grading that starts once there is Go source:
// build it, run it, and compare stdout against the engine's. Extracted
// because a file the **engine** could not compile takes the same path from
// here -- its program is only the diagnostic, but it is built, run and
// graded exactly like any other.
func buildAndRun(src, work, path, reported string, test *phpt.Test, want, keep string) (outcome, string) {
	if emitDir != "" {
		return emitModule(src, work, path, reported)
	}
	if err := os.WriteFile(filepath.Join(work, "main.go"), []byte(src), 0o644); err != nil {
		return errored, "write: " + err.Error()
	}
	if hybridGrading {
		if err := os.WriteFile(filepath.Join(work, "hybrid.go"), []byte(hybridSource), 0o644); err != nil {
			return errored, "write hybrid bridge: " + err.Error()
		}
	}
	buildArgs := []string{"build"}
	if hybridGrading {
		buildArgs = append(buildArgs, "-tags", hybridBuildTag)
	}
	buildArgs = append(buildArgs, gradingBuildFlags...)
	buildArgs = append(buildArgs, "-o", filepath.Join(work, "prog"), ".")
	build := exec.Command("go", buildArgs...)
	build.Dir = work
	build.Env = buildEnv()
	if out, err := build.CombinedOutput(); err != nil {
		if keep != "" {
			os.MkdirAll(keep, 0o755)
			os.WriteFile(filepath.Join(keep, sanitize(path)+".go"), []byte(src), 0o644)
		}
		// Go rejecting our own output is a bug in the emitter, never a gap in
		// PHP coverage, so it must not land in `unsupported`.
		//
		// But "our output" means the *generated file*. A generated program links
		// against `runtime/php`, so a compile error inside the runtime is not an
		// emitter bug at all -- and on a shared tree it happens: 25 files scored
		// `errored` on one line of a half-written `iter.go`, which would have
		// read as a compiler regression. That is the two-populations problem
		// again, one system over, and again the misleading reading is the one
		// that needs no explanation.
		msg := firstError(string(out))
		if !strings.Contains(msg, "main.go") && strings.Contains(msg, ".go:") {
			return noreference, "the runtime failed to build: " + short(msg)
		}
		return errored, "go build: " + short(msg)
	}

	var stdout bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(work, "prog"))
	// **Where the oracle ran** (oracleDir): an extracted case's own
	// directory, and the harness's for a plain file. A path the program
	// computes relative to its `__DIR__` -- reported relative to the root
	// -- then resolves on disk as the engine's did; from the build
	// directory it named nothing, and a computed include of an existing
	// file failed to open where the engine's ran.
	cmd.Dir = oracleDir(path)
	cmd.Stdout = &stdout
	// stderr joins stdout because a diagnostic the engine prints to stdout must
	// line up, and anything the generated program writes to stderr is a defect
	// that should be *visible* in the diff rather than silently dropped.
	var stderr bytes.Buffer
	cmd.Stderr = io.MultiWriter(&stdout, &stderr)
	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		// The source too: a program that does not terminate is the hardest
		// thing here to look at, and re-running to capture it is not always
		// possible -- the timeout is what makes it expensive.
		if keep != "" {
			os.MkdirAll(keep, 0o755)
			os.WriteFile(filepath.Join(keep, sanitize(path)+".go"), []byte(src), 0o644)
		}
		// Not `failed`: there is no output to compare, and a program that does
		// not terminate is not a wrong answer. Not `unsupported` either -- it
		// is not a gap the compiler declared.
		return errored, fmt.Sprintf("timed out after %s", runTimeout)
	}
	// Exit 3 is the generated program reporting a capability gap it found at
	// run time -- a declared parameter type it cannot coerce, say. A gap and a
	// wrong answer are different results and stdout cannot distinguish them,
	// so the exit code carries it: without this the gap inflates `failed` and
	// reads as a compiler that gets things wrong rather than one with a hole.
	//
	// **The code alone is not enough.** A PHP program can pick its own status,
	// and `exit(3)` is a perfectly ordinary thing to write -- it was read as a
	// compiler gap the moment `exit` started working. The stderr marker is what
	// only the generated program writes, so both are required.
	var exit *exec.ExitError
	if errors.As(runErr, &exit) && exit.ExitCode() == 3 &&
		strings.HasPrefix(strings.TrimSpace(stderr.String()), unsupportedMarker) {
		return unsupported, "runtime: " + short(strings.TrimSpace(stderr.String()))
	}
	if got := stdout.String(); got != want {
		// **A file whose own output is not reproducible has no reference to
		// grade against.** `attributes/deprecated/message_004.phpt` builds its
		// deprecation message from `random_int(1, 2)`; the engine arm and the
		// compiled arm each roll their own, so the two disagree about half the
		// time and the file flips bucket between runs. PHP's own harness never
		// sees this -- it matches one run against an `--EXPECTF--` with a `%c`
		// where the randomness sits -- but a differential oracle compares two
		// runs and has nowhere to put a wildcard.
		//
		// Asked only on a mismatch, and asked of the **engine against itself**:
		// each `engineOutput` is a fresh php2eval process, so a disagreement
		// between two of them is the file's own randomness and not leaked
		// request state. A file that reproduces its output stays `failed`.
		if unreferenceable(test, path, reported, want, got) {
			return noreference, "the engine does not reproduce its own output"
		}
		// Both outputs too: the reason line shows only the first difference,
		// and a file is usually understood from the whole of it.
		if keep != "" {
			os.MkdirAll(keep, 0o755)
			os.WriteFile(filepath.Join(keep, sanitize(path)+".go"), []byte(src), 0o644)
			os.WriteFile(filepath.Join(keep, sanitize(path)+".want"), []byte(want), 0o644)
			os.WriteFile(filepath.Join(keep, sanitize(path)+".got"), []byte(got), 0o644)
		}
		return failed, fmt.Sprintf("output differs (want %dB, got %dB): %s",
			len(want), len(got), short(firstDiff(want, got)))
	}
	// **A match by chance is no pass.** A file that rolls its own random
	// value agrees with the engine's roll about half the time, so it
	// counted as passed in one batch and noreference in the next
	// (message_004, value_unknown_at_compile_time_001 alternated for tens
	// of batches). A file that calls a random source is asked the same
	// question on a match as on a mismatch.
	if callsRandomness(path) && unreferenceable(test, path, reported, want, stdout.String()) {
		return noreference, "the engine does not reproduce its own output"
	}
	return passed, ""
}

// randomSources are the calls whose results differ from run to run.
var randomSources = regexp.MustCompile(`(?i)\b(random_int|random_bytes|mt_rand|rand|lcg_value|uniqid|shuffle|str_shuffle|array_rand)\s*\(`)

// callsRandomness reports a source that calls a random source.
func callsRandomness(path string) bool {
	src, err := os.ReadFile(path)
	return err == nil && randomSources.Match(src)
}

// gradingBuildFlags build a generated program **for grading**: its package
// unoptimized and uninlined, the binary without a symbol table or DWARF.
//
// The generated package is ~14,000 lines, most of them helpers every program
// carries, and compiling it was nearly all of a corpus file's cost. Measured on
// one program with the compiler's GC off (buildEnv): 1.83 s wall and 2.03 s of
// CPU by default, 0.82 s and 1.01 s with these. `-gcflags` without `all=`
// applies only to the package named on the command line, so `runtime/php`
// stays optimized and comes from the build cache.
//
// What grading asks is the program's **output**, and neither flag changes
// behaviour PHP can see: float arithmetic is already kept unfused by the
// emitter, and object lifetime is explicit rather than the Go collector's. A
// program runs slower unoptimized, which only matters against -timeout. The
// -emit modules and hack/bench.sh build normally.
var gradingBuildFlags = []string{"-gcflags=-N -l", "-ldflags=-s -w"}

// buildEnv is the environment the Go toolchain builds a generated program in:
// the caller's, with the **compiler's** collector off below a soft limit.
//
// Compiling the generated package is nearly all of a file's cost, and half of
// that was the compiler collecting garbage -- measured on a 14,000-line
// program, 3.4 s of CPU with the default GOGC and 1.9 s with it off, the wall
// clock unchanged because the collector ran on other cores. On a shared,
// oversubscribed machine the CPU is what the corpus waits for. The peak rises
// from 0.44 GB to about 1.1 GB, so GOMEMLIMIT keeps an unusually large program
// collecting rather than growing without bound.
//
// Only the build. The generated program runs with the caller's environment:
// it sets its own GC target, and a PHP loop that allocates must collect.
func buildEnv() []string {
	return append(os.Environ(), "GOGC=off", "GOMEMLIMIT=3GiB")
}

// unreferenceable reports that a file has no byte-exact reference to grade
// against, because its own output is random.
//
// `attributes/deprecated/message_004.phpt` builds its deprecation message from
// `random_int(1, 2)`. PHP's own harness never sees the problem: it matches one
// run against an `--EXPECTF--` carrying a `%c` exactly where the randomness
// sits. A differential oracle compares **two runs** and has nowhere to put a
// wildcard, so the file flips bucket between measurements -- which is how it
// was read once as a gain and once as a regression.
//
// Two conditions, both required, so that a wildcard cannot be used to excuse a
// real difference:
//
//   - the test declares an expectation that **both** arms satisfy, so whatever
//     they disagree about is a region the test itself leaves open;
//   - the engine disagrees with **itself** across independent runs. Each
//     `engineOutput` is a fresh php2eval process, so that is the file's own
//     randomness rather than leaked request state.
//
// A random choice between two values is only caught about seven times in eight,
// so a file like this can still be graded `failed` on some runs. The check
// never fires wrongly -- a reproducible file never disagrees with itself -- and
// the residual noise is one file rather than a bucket.
func unreferenceable(test *phpt.Test, path, reported, want, got string) bool {
	if test == nil || !test.Matches(want) || !test.Matches(got) {
		return false
	}
	// A two-valued random agrees with itself three times in eight: a file
	// that calls one is asked more often.
	tries := 3
	if callsRandomness(path) {
		tries = 8
	}
	for i := 0; i < tries; i++ {
		again, err := engineOutput(path, reported)
		if err != nil {
			return false
		}
		if again != want {
			return true
		}
	}
	return false
}

// engineOutput runs the file through the engine, telling it to report the same
// filename the compiled program will report. Without that every diagnostic
// differs in its filename and no diagnostic test can pass, however correct the
// compiler is.
func engineOutput(path, reported string) (string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	bin, err := filepath.Abs(php2eval)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, bin, "-raw", "-as", reported)
	cmd.Dir = oracleDir(path)
	cmd.Stdin = strings.NewReader(sameLines(string(src)))
	var out, errs bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errs
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, short(errs.String()))
	}
	return out.String(), nil
}

// oracleDir is where the oracle runs, which is also what a relative include
// resolves against in both arms (its include_path is empty, so the cwd alone
// is searched -- probed).
//
// **An extracted .phpt case runs in its own directory**, where the compiled
// program runs too: the siblings are linked there, and the reported
// `../../../../tmp/...` names the case from either directory. A plain .php
// file is reported relative to the root, so its `__DIR__` is too, and only
// resolves from where the harness itself runs.
func oracleDir(path string) string {
	if filepath.Base(path) == "case.php" && strings.HasPrefix(filepath.Base(filepath.Dir(path)), "php2gotest-") {
		return filepath.Dir(path)
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// cliStreamConstant is the oracle refusing a CLI-only constant.
var cliStreamConstant = regexp.MustCompile(`Undefined constant "STD(IN|OUT|ERR)"`)

// includesFile spots a test that includes or requires another file.
var includesFile = regexp.MustCompile(`(?i)\b(include|require)(_once)?\b`)

// linkSiblings puts the test directory's entries beside the extracted case,
// as symlinks, when the source includes something. run-tests.php writes the
// case into the test's own directory, where `require 'nowdoc.inc'` finds its
// sibling; the case here is extracted elsewhere, and third_party stays
// untouched. The included file's own path is the real one either way: both
// arms resolve through the link to it.
func linkSiblings(phptPath, work, source string) {
	if !includesFile.MatchString(source) {
		return
	}
	entries, err := os.ReadDir(filepath.Dir(phptPath))
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if name == "case.php" || strings.HasSuffix(name, ".phpt") {
			continue
		}
		_ = os.Symlink(filepath.Join(filepath.Dir(phptPath), name), filepath.Join(work, name))
	}
}

// sameLines removes the opening tag but keeps the line it occupied.
//
// The oracle evaluates a string and the compiler compiles a file, so the two
// arms must agree on line numbering or every `__LINE__` and every diagnostic
// carrying a line number is graded against a source numbered differently.
// Dropping "<?php\n" outright shifts everything up by one -- `bug36037.phpt`
// wanted 1 and 6 and got 2 and 7, which read as a compiler bug and was a
// harness bug.
//
// The tag cannot simply be dropped, because text before an opening tag is
// output: that would make the whole file inline HTML. It is overwritten with
// spaces instead, which the oracle reads as PHP whitespace, so byte offsets
// agree as well as lines -- `__COMPILER_HALT_OFFSET__` counts bytes from the
// start of the file, and a cut tag made the oracle's six short
// (halt_compiler1).
//
// **A file need not open with the tag.** The oracle starts in PHP mode, where
// a file starts in HTML mode: text before the tag is output (`hi<?php`), the
// tag's case is free (`<?PHP`), and the CLI skips a `#!` first line. Leading
// text gets a `?>` in front, which switches the oracle to HTML mode.
func sameLines(src string) string {
	if len(src) >= 5 && strings.EqualFold(src[:5], "<?php") {
		return "     " + src[5:]
	}
	// A shebang line is a `#` comment in PHP mode, of the same length, so it
	// stays as it is and only the tag after it is padded: every byte offset
	// agrees (bug48930's `__COMPILER_HALT_OFFSET__`).
	if strings.HasPrefix(src, "#!") {
		nl := strings.IndexByte(src, '\n')
		if nl < 0 {
			return ""
		}
		if rest := src[nl+1:]; len(rest) >= 5 && strings.EqualFold(rest[:5], "<?php") {
			return src[:nl+1] + "     " + rest[5:]
		}
		return "?>" + src[nl:]
	}
	if src != "" && src[0] != '\n' && !strings.HasPrefix(src, "<?") {
		return "?>" + src
	}
	return src
}

func hasClasses(u *pb.Unit) bool { return len(u.GetClasses()) > 0 }

func firstError(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, ".go:") {
			return strings.TrimSpace(line)
		}
	}
	return strings.TrimSpace(out)
}

func firstDiff(want, got string) string {
	w := strings.Split(want, "\n")
	g := strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		a, b := "", ""
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return fmt.Sprintf("line %d: want %q got %q", i+1, a, b)
		}
	}
	return ""
}

func short(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " / "))
	if len(s) > 140 {
		return s[:140] + "..."
	}
	return s
}

func sanitize(p string) string {
	return strings.NewReplacer("/", "_", ".", "_").Replace(strings.TrimPrefix(p, "./"))
}

// unsupportedMarker is how a generated program says on stderr that it hit a
// capability gap. It is `(*php.UnsupportedError).Error`'s own prefix, so the two
// cannot drift without this constant being wrong in an obvious way.
const unsupportedMarker = "php2go: unsupported:"

// attributesOf carries a declaration's `#[...]` list into the emitter's own
// records. The arguments stay unevaluated -- rendered to IR literal text, with
// the literal kept beside it -- because an attribute's constructor does not run
// until something reflects on it.
// abstractSig is an abstract method's parameters in lower.Function's form,
// for reflection.
func abstractSig(m *pb.OpArray) gogen.AbstractSig {
	sig := gogen.AbstractSig{Required: int(m.GetRequiredArgs()), Returns: m.GetReturnType()}
	for i, a := range m.GetArgs() {
		amp := ""
		if a.GetByRef() {
			amp = "&"
		}
		sig.Params = append(sig.Params, fmt.Sprintf("%d %s$%s %q", i, amp, a.GetName(), a.GetType()))
		sig.Variadic = a.GetVariadic()
	}
	return sig
}

// addParamAttributes records a callable's parameter attributes by position,
// when it has any.
func addParamAttributes(prog *gogen.Program, key string, args []*pb.ArgInfo) {
	var list [][]gogen.Attribute
	for i, a := range args {
		if attrs := attributesOf(a.GetAttributes()); len(attrs) > 0 {
			for len(list) <= i {
				list = append(list, nil)
			}
			list[i] = attrs
		}
	}
	if list == nil {
		return
	}
	if prog.ParamAttributes == nil {
		prog.ParamAttributes = map[string][][]gogen.Attribute{}
	}
	prog.ParamAttributes[key] = list
}

func attributesOf(list []*pb.Attribute) []gogen.Attribute {
	if len(list) == 0 {
		return nil
	}
	out := make([]gogen.Attribute, 0, len(list))
	for _, a := range list {
		attr := gogen.Attribute{Name: a.GetName(), Line: int64(a.GetLine())}
		for _, arg := range a.GetArgs() {
			attr.Args = append(attr.Args, gogen.AttributeArg{
				Name:    arg.GetName(),
				Value:   lower.LiteralText(arg.GetValue()),
				Literal: arg.GetValue(),
			})
		}
		out = append(out, attr)
	}
	return out
}

// traitCtors are the `__construct` bodies c's traits could give it -- every
// one, since `insteadof` picks among them -- looked for through the traits
// they use in turn, or unknown when a trait is not compiled here or an alias
// could rename a method to it.
func traitCtors(c *gogen.Class, byName map[string]*gogen.Class) (bodies []*lower.Function, unknown bool) {
	seen := map[string]bool{}
	var walk func(c *gogen.Class) bool
	walk = func(c *gogen.Class) bool {
		if len(c.TraitAliases) > 0 {
			return false
		}
		for _, t := range c.Traits {
			name := strings.ToLower(strings.TrimPrefix(t, "\\"))
			if seen[name] {
				continue
			}
			seen[name] = true
			tc := byName[name]
			if tc == nil {
				return false
			}
			if b, ok := tc.Methods["__construct"]; ok {
				bodies = append(bodies, b)
				continue
			}
			if !walk(tc) {
				return false
			}
		}
		return true
	}
	if !walk(c) {
		return nil, true
	}
	return bodies, false
}

// markDiscardedResults claims the fresh call and `clone` results nothing
// reads, which die at their statement (lower.DiscardedResultsOf). Run after
// markConfined, and like markDiscarded it takes a site claimed here back out
// of the frame list -- an unread `clone`, a lazy factory's object -- so its
// number is not handed back twice.
func markDiscardedResults(fn *lower.Function, freshReturn func([]string) bool, leaksThis func(string) bool) {
	fn.DiscardedResults = lower.DiscardedResultsOf(fn, freshReturn, leaksThis)
	for v := range fn.DiscardedResults {
		delete(fn.ConfinedNew, v)
	}
}

// destructorResurrects is gogen's mayResurrect: the classes whose
// `__destruct` -- their own or inherited -- may store `$this`, keyed by the
// folded name. One a trait may supply counts.
func destructorResurrects(prog *gogen.Program) map[string]bool {
	byName := map[string]*gogen.Class{}
	for _, c := range prog.Classes {
		byName[strings.ToLower(strings.TrimPrefix(c.Name, `\`))] = c
	}
	leaks, _ := prog.ThisLeaks()
	out := map[string]bool{}
	for _, c := range prog.Classes {
		for cur, depth := c, 0; cur != nil && depth <= len(prog.Classes); depth++ {
			if body, ok := cur.Methods["__destruct"]; ok {
				if leaks(body) {
					out[strings.ToLower(strings.TrimPrefix(c.Name, `\`))] = true
				}
				break
			}
			if len(cur.Traits) > 0 {
				out[strings.ToLower(strings.TrimPrefix(c.Name, `\`))] = true
				break
			}
			if cur.Parent == "" {
				break
			}
			cur = byName[strings.ToLower(strings.TrimPrefix(cur.Parent, `\`))]
		}
	}
	return out
}
