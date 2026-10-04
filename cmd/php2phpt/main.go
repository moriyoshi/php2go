// Command php2phpt grades the interpreter against php-src's own test suite.
//
// The pass rate is the honest completeness metric. Equally useful is the
// histogram of what is blocking: it says which opcode to implement next by
// how many tests it unblocks, rather than by guesswork.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/internal/ir"
	"github.com/moriyoshi/php2go/internal/phpt"
	"github.com/moriyoshi/php2go/internal/zend"
	"github.com/moriyoshi/php2go/runtime/interp"
	"github.com/moriyoshi/php2go/runtime/php"
)

type outcome int

const (
	outcomePass outcome = iota
	outcomeFail
	outcomeUnsupported
	// outcomeCompileError is a source PHP itself refuses to compile. Many tests
	// exist precisely to trigger one, so it is not a harness failure and must
	// not be counted against the interpreter.
	outcomeCompileError
	outcomeHarvestError
	outcomeSkipped
)

type tally struct {
	counts map[outcome]int
	// blockers counts tests blocked by each unsupported opcode, which is what
	// makes the next step obvious.
	blockers map[string]int
	failures []string
}

// options are the knobs the command exposes, gathered so run and its helpers
// take one parameter rather than a growing list of bools.
type options struct {
	limit    int
	verbose  bool
	top      int
	progress bool
	// diff prints expected and actual side by side for each mismatch, which is
	// what turns a failure count into something diagnosable.
	diff int
	// diffKind chooses which bucket -diff draws from. A compile error is not an
	// interpreter failure, but a wall of them usually means the harness is
	// feeding the compiler something run-tests.php would not have.
	diffKind string
}

func main() {
	var opts options
	flag.IntVar(&opts.limit, "n", 0, "stop after this many tests (0 for all)")
	flag.BoolVar(&opts.verbose, "v", false, "list failing tests")
	flag.IntVar(&opts.top, "top", 15, "how many blocking opcodes to report")
	flag.BoolVar(&opts.progress, "progress", false, "print each test path before running it")
	flag.IntVar(&opts.diff, "diff", 0, "print expected vs actual for this many mismatches")
	flag.StringVar(&opts.diffKind, "diff-kind", "fail", "which bucket -diff draws from: fail, compile or errored")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: php2phpt [flags] <dir-or-file>...\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(flag.Args(), opts); err != nil {
		fmt.Fprintln(os.Stderr, "php2phpt:", err)
		os.Exit(1)
	}
}

func run(roots []string, opts options) error {
	paths, err := collect(roots)
	if err != nil {
		return err
	}
	if opts.limit > 0 && len(paths) > opts.limit {
		paths = paths[:opts.limit]
	}

	engine, err := zend.Start()
	if err != nil {
		return err
	}
	defer engine.Close()

	// Constants come from the engine rather than a hard-coded table, so a test
	// referring to E_ALL or SORT_STRING sees the value this build defines.
	constants, err := engine.Constants()
	if err != nil {
		return err
	}
	framelessFunctions, err := engine.FramelessFunctions()
	if err != nil {
		return err
	}
	builtinClasses, err := engine.InternalClasses()
	if err != nil {
		return err
	}
	// The builtin signatures are harvested so the interpreter can tell a
	// function this build does not have -- a PHP Error -- from one it has but
	// the runtime has not implemented, which stays an honest gap.
	builtins, err := engine.Builtins()
	if err != nil {
		return err
	}
	iniEntries, err := engine.IniEntries()
	if err != nil {
		return err
	}
	extensions, err := engine.Extensions()
	if err != nil {
		return err
	}
	manifest := ir.EncodeManifest(ir.Harvest{
		Version: zend.Version(), Layout: zend.DetectLayout(), Opcodes: zend.OpcodeNames(),
		Builtins: builtins, Constants: constants, BuiltinClasses: builtinClasses,
		FramelessFunctions:    framelessFunctions,
		IniEntries:            iniEntries,
		Extensions:            extensions,
		DefaultErrorReporting: engine.DefaultErrorReporting(), Assertions: engine.Assertions(),
	})
	machine := interp.New(manifest)
	// include, require and eval need a compiler at run time; the engine that
	// produced the IR is already linked in, so it serves as one.
	scratch, err := os.MkdirTemp("", "php2phpt-eval")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	machine.SetLoader(&ir.EngineLoader{Engine: engine, Scratch: scratch})

	result := &tally{counts: map[outcome]int{}, blockers: map[string]int{}}
	shown := 0
	for _, path := range paths {
		// Each test must start from a clean engine, or a declaration left behind
		// by an earlier one changes how a later one compiles.
		if err := engine.Reset(); err != nil {
			return err
		}
		if opts.progress {
			fmt.Fprintln(os.Stderr, "running", path)
		}
		what, detail, test, actual := runOne(engine, machine, path)
		result.counts[what]++
		switch what {
		case outcomeUnsupported:
			result.blockers[detail]++
		case outcomeFail, outcomeHarvestError:
			if len(result.failures) < 200 {
				result.failures = append(result.failures, path+": "+detail)
			}
		}
		if shown < opts.diff && wantsDiff(opts.diffKind, what) {
			shown++
			printDiff(path, test, actual)
		}
	}
	report(result, len(paths), opts)
	return nil
}

// wantsDiff reports whether -diff should print this outcome.
func wantsDiff(kind string, what outcome) bool {
	switch kind {
	case "compile":
		return what == outcomeCompileError
	case "errored":
		return what == outcomeHarvestError
	default:
		return what == outcomeFail
	}
}

// printDiff shows what the test wanted next to what the interpreter produced.
// Both are printed whole: a mismatch is usually in a message's wording or in
// one line of a var_dump, which a truncated form hides.
func printDiff(path string, test *phpt.Test, actual string) {
	if test == nil {
		// A test that could not be parsed or harvested has nothing to compare
		// against. Naming it is still the point of -diff, and printing nothing
		// at all used to take the rest of the listing down with it.
		fmt.Printf("\n=== %s\n--- NO TEST ---\n%s\n", path,
			strings.TrimRight(actual, " \t\r\n"))
		return
	}
	want, kind := test.Expect, "EXPECT"
	if test.ExpectF != "" {
		want, kind = test.ExpectF, "EXPECTF"
	} else if test.ExpectRegex != "" {
		want, kind = test.ExpectRegex, "EXPECTREGEX"
	}
	fmt.Printf("\n=== %s\n--- %s ---\n%s\n--- GOT ---\n%s\n", path, kind,
		strings.TrimRight(want, " \t\r\n"), strings.TrimRight(actual, " \t\r\n"))
}

func runOne(engine *zend.Engine, machine *interp.Interp, path string) (result outcome, detail string, test *phpt.Test, output string) {
	// A panic in the interpreter is one test's failure, not the run's. Losing
	// four thousand results to one bad allocation would make the corpus
	// unusable as a measurement.
	defer func() {
		if recovered := recover(); recovered != nil {
			result, detail = outcomeHarvestError, fmt.Sprintf("panic: %v", recovered)
		}
	}()
	return runTest(engine, machine, path)
}

func runTest(engine *zend.Engine, machine *interp.Interp, path string) (outcome, string, *phpt.Test, string) {
	test, err := phpt.Parse(path)
	if err != nil {
		return outcomeHarvestError, err.Error(), nil, ""
	}
	if reason := test.SkipReason(); reason != "" {
		return outcomeSkipped, reason, test, ""
	}
	// A --SKIPIF-- section is PHP that decides whether the test applies here.
	// run-tests.php runs it with PHP; php2go runs it with php2go, which is the
	// same question asked of the runtime under test. A condition php2go cannot
	// run is a skip in its own right rather than a licence to run the test.
	if test.SkipIf != "" {
		if reason := evaluateSkipIf(engine, machine, path, test.SkipIf); reason != "" {
			return outcomeSkipped, reason, test, ""
		}
	}

	// The --FILE-- body has to reach the compiler as a file, since PHP records
	// the filename and resolves includes against it. run-tests.php writes it
	// beside the .phpt under the test's own name -- foo.phpt becomes foo.php --
	// and the tests depend on both halves of that: an include resolves against
	// the directory of the file doing the including, so a sibling .inc is only
	// found from there, and an EXPECTF line like "in %sfoo.php on line 3" only
	// matches when the file is named after the test.
	source := strings.TrimSuffix(path, ".phpt") + ".php"
	// The file is normally ours alone, but a run killed part way through leaves
	// one behind, and a corpus can ship a fixture under that name. Either way
	// the original content is put back rather than destroyed -- a stray from a
	// crashed run would otherwise fail its test permanently.
	existing, existed := os.ReadFile(source)
	if err := os.WriteFile(source, []byte(test.File), 0o644); err != nil {
		return outcomeHarvestError, err.Error(), test, ""
	}
	defer func() {
		if existed == nil {
			os.WriteFile(source, existing, 0o644)
			return
		}
		os.Remove(source)
	}()
	// A test that writes a file beside itself normally unlinks it again, but a
	// run that stops early -- an unsupported call, a fatal -- never reaches
	// that line. The corpus is a pristine submodule, so whatever appeared
	// during the test is removed rather than left for the next run to find.
	defer removeNewEntries(filepath.Dir(path), directoryEntries(filepath.Dir(path)))

	// The test's own file is what the request starts from, so a `#!` first
	// line is the SAPI's to skip -- run-tests.php runs these through the CLI.
	unit, err := engine.CompileMainUnit(source)
	if err != nil {
		var compileErr *zend.CompileError
		if asCompile(err, &compileErr) {
			// When the engine printed something, that text is the program's
			// output and the test may well be expecting exactly it.
			if compileErr.Diagnostic != "" && test.Matches(compileErr.Diagnostic) {
				return outcomePass, "", test, compileErr.Diagnostic
			}
			return outcomeCompileError, "source does not compile", test, compileErr.Diagnostic
		}
		return outcomeHarvestError, err.Error(), test, ""
	}

	// run-tests.php runs each test with its own --INI--, and the expected
	// output was recorded under it. error_reporting is the one setting in that
	// section that decides what reaches the output, so it has to be applied
	// rather than disregarded -- otherwise the two sides are compared across
	// different configurations.
	if raw, set := test.IniValue("error_reporting"); set {
		if mask, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil {
			previous := machine.DefaultErrorReporting()
			machine.SetDefaultErrorReporting(mask)
			defer machine.SetDefaultErrorReporting(previous)
		}
	}

	var out bytes.Buffer
	ctx := php.NewCtx(&out)
	// The corpus's expected output was recorded under the CLI, which holds the
	// three standard streams plus one more before a script runs -- so its first
	// resource is 5, and some expectations spell that literally. The engine
	// running in this process starts at 2.
	ctx.SetFirstResourceID(5)
	if _, err := machine.RunUnit(ctx, interp.Load(ir.EncodeUnit(unit))); err != nil {
		var unsupported *interp.UnsupportedError
		if asUnsupported(err, &unsupported) {
			return outcomeUnsupported, unsupported.Opcode, test, out.String()
		}
		return outcomeHarvestError, err.Error(), test, out.String()
	}
	if !test.Matches(out.String()) {
		return outcomeFail, fmt.Sprintf("output mismatch (got %q)", truncate(out.String(), 60)), test, out.String()
	}
	return outcomePass, "", test, out.String()
}

// evaluateSkipIf runs a test's --SKIPIF-- body and reports why the test should
// be skipped, or "" if it should run. run-tests.php skips when the output
// begins with "skip"; anything else means the test applies.
func evaluateSkipIf(engine *zend.Engine, machine *interp.Interp, path, body string) string {
	source := strings.TrimSuffix(path, ".phpt") + ".skip.php"
	if err := os.WriteFile(source, []byte(body), 0o644); err != nil {
		return "cannot write --SKIPIF--: " + err.Error()
	}
	defer os.Remove(source)

	unit, err := engine.CompileUnit(source)
	if err != nil {
		return "--SKIPIF-- does not compile"
	}
	var out bytes.Buffer
	ctx := php.NewCtx(&out)
	// The corpus's expected output was recorded under the CLI, which holds the
	// three standard streams plus one more before a script runs -- so its first
	// resource is 5, and some expectations spell that literally. The engine
	// running in this process starts at 2.
	ctx.SetFirstResourceID(5)
	if _, err := machine.RunUnit(ctx, interp.Load(ir.EncodeUnit(unit))); err != nil {
		var unsupported *interp.UnsupportedError
		if asUnsupported(err, &unsupported) {
			return "--SKIPIF-- needs " + unsupported.Opcode
		}
		return "--SKIPIF-- failed: " + err.Error()
	}
	text := strings.TrimSpace(out.String())
	if strings.HasPrefix(strings.ToLower(text), "skip") {
		return strings.TrimSpace(text[len("skip"):])
	}
	return ""
}

func asCompile(err error, target **zend.CompileError) bool {
	for err != nil {
		if e, ok := err.(*zend.CompileError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func asUnsupported(err error, target **interp.UnsupportedError) bool {
	for err != nil {
		if e, ok := err.(*interp.UnsupportedError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func report(t *tally, total int, opts options) {
	verbose, top := opts.verbose, opts.top
	// Only tests whose source actually compiles are the interpreter's to pass.
	// Counting compile-error tests against it would understate progress, and
	// counting them as passes would overstate it.
	attempted := total - t.counts[outcomeSkipped] - t.counts[outcomeCompileError]
	fmt.Printf("%d tests: %d passed, %d failed, %d unsupported, %d compile-error, %d errored, %d skipped\n",
		total, t.counts[outcomePass], t.counts[outcomeFail], t.counts[outcomeUnsupported],
		t.counts[outcomeCompileError], t.counts[outcomeHarvestError], t.counts[outcomeSkipped])
	if attempted > 0 {
		fmt.Printf("pass rate: %.1f%% of %d attempted\n",
			100*float64(t.counts[outcomePass])/float64(attempted), attempted)
	}

	if len(t.blockers) > 0 {
		type entry struct {
			opcode string
			count  int
		}
		entries := make([]entry, 0, len(t.blockers))
		for opcode, count := range t.blockers {
			entries = append(entries, entry{opcode, count})
		}
		// Most-blocking first; ties broken by name so runs are reproducible.
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].count != entries[j].count {
				return entries[i].count > entries[j].count
			}
			return entries[i].opcode < entries[j].opcode
		})
		fmt.Printf("\ntop blocking opcodes:\n")
		for i, e := range entries {
			if i >= top {
				fmt.Printf("  ... and %d more\n", len(entries)-top)
				break
			}
			fmt.Printf("  %5d  %s\n", e.count, e.opcode)
		}
	}

	if verbose && len(t.failures) > 0 {
		fmt.Printf("\nfailures:\n")
		for _, failure := range t.failures {
			fmt.Printf("  %s\n", failure)
		}
	}
}

func collect(roots []string) ([]string, error) {
	var paths []string
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			paths = append(paths, root)
			continue
		}
		err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".phpt") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// directoryEntries snapshots the names in a directory, so anything that appears
// while a test runs can be told from what was already there.
func directoryEntries(dir string) map[string]bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		names[entry.Name()] = true
	}
	return names
}

// removeNewEntries deletes the files a test left behind. Only plain files are
// removed: a directory would be the test's own fixture tree, and deleting one
// blind is a bigger promise than this makes.
func removeNewEntries(dir string, before map[string]bool) {
	if before == nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || before[entry.Name()] {
			continue
		}
		os.Remove(filepath.Join(dir, entry.Name()))
	}
}
