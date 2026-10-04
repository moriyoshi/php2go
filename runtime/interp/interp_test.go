package interp_test

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/moriyoshi/php2go/internal/ir"
	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/internal/userland"
	"github.com/moriyoshi/php2go/internal/zend"
	"github.com/moriyoshi/php2go/runtime/interp"
	"github.com/moriyoshi/php2go/runtime/php"
)

var engine *zend.Engine

func TestMain(m *testing.M) {
	started, err := zend.Start()
	if err != nil {
		panic(err)
	}
	engine = started
	code := m.Run()
	engine.Close()
	os.Exit(code)
}

// run harvests a PHP source and interprets it, returning what it printed.
func run(t *testing.T, source string) (string, error) {
	return runWithSources(t, source, nil)
}

type sourceReadingLoader struct {
	*ir.EngineLoader
	sources map[string][]byte
}

func (l *sourceReadingLoader) ReadSource(path string) ([]byte, error) {
	if data, ok := l.sources[path]; ok {
		return data, nil
	}
	return nil, os.ErrNotExist
}

func runWithSources(t *testing.T, source string, sources map[string][]byte) (string, error) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "case.php")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	unit, err := engine.CompileUnit(path)
	if err != nil {
		t.Fatal(err)
	}

	// Round-trip through the serialized form: the interpreter must work from
	// what is written to disk, not from live engine structures.
	encoded, err := ir.MarshalUnit(ir.EncodeUnit(unit))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ir.UnmarshalUnit(encoded)
	if err != nil {
		t.Fatal(err)
	}
	// The engine's constants go into the manifest so a test can refer to E_ALL
	// and friends with the values this build actually defines.
	constants, err := engine.Constants()
	if err != nil {
		t.Fatal(err)
	}
	framelessFunctions, err := engine.FramelessFunctions()
	if err != nil {
		t.Fatal(err)
	}
	builtinClasses, err := engine.InternalClasses()
	if err != nil {
		t.Fatal(err)
	}
	// The builtin signatures matter even here: they are what tells a call to a
	// function this build does not have from one the runtime has yet to
	// implement, and the two are reported differently.
	builtins, err := engine.Builtins()
	if err != nil {
		t.Fatal(err)
	}
	iniEntries, err := engine.IniEntries()
	if err != nil {
		t.Fatal(err)
	}
	extensions, err := engine.Extensions()
	if err != nil {
		t.Fatal(err)
	}
	manifest := ir.EncodeManifest(ir.Harvest{
		Version: zend.Version(), Layout: zend.DetectLayout(), Opcodes: zend.OpcodeNames(),
		Builtins: builtins, Constants: constants, BuiltinClasses: builtinClasses,
		FramelessFunctions:    framelessFunctions,
		IniEntries:            iniEntries,
		Extensions:            extensions,
		DefaultErrorReporting: engine.DefaultErrorReporting(), Assertions: engine.Assertions(),
	})

	var out bytes.Buffer
	ctx := php.NewCtx(&out)
	machine := interp.New(manifest)
	// include, require and eval compile source that was not part of the
	// harvest. The oracle can do it, so the interpreter side must be able to
	// as well, or a case exercising eval would compare against nothing.
	loader := &ir.EngineLoader{Engine: engine, Scratch: t.TempDir()}
	if sources == nil {
		machine.SetLoader(loader)
	} else {
		machine.SetLoader(&sourceReadingLoader{EngineLoader: loader, sources: sources})
	}
	// RunUnit rather than Run: a program includes the functions it declared.
	loaded := interp.Load(decoded)
	// **The userland library has to be installed here or it is not under
	// test.** A function whose body is PHP -- `str_pad`, `ucwords`,
	// `array_flip` -- is otherwise simply absent, and the harness reports the
	// case as "not yet supported" and skips it. That is an honest bucket and a
	// silent loss of coverage: every function moved from Go to PHP would stop
	// being compared against the engine at the moment it started needing to be.
	for _, library := range userlandLibraries(t) {
		if err := machine.InstallUserlandLibrary(loaded, library.unit, library.aliases); err != nil {
			t.Fatalf("install userland %s: %v", library.path, err)
		}
		for _, initializer := range library.initializers {
			if err := machine.InstallUserlandInitializer(loaded, initializer); err != nil {
				t.Fatalf("initialize userland %s: %v", library.path, err)
			}
		}
	}
	_, err = machine.RunUnit(ctx, loaded)
	return out.String(), err
}

// harvestedLibrary is one userland source, compiled once for the whole test
// binary. Harvesting `ext/standard.php` is ~10k lines through the engine, so
// doing it per case would dominate a suite of a few thousand.
type harvestedLibrary struct {
	path         string
	unit         *pb.Unit
	aliases      map[string]string
	initializers []string
}

var (
	userlandOnce sync.Once
	userlandLibs []harvestedLibrary
	userlandErr  error
)

func userlandLibraries(t *testing.T) []harvestedLibrary {
	t.Helper()
	userlandOnce.Do(func() {
		scratch, err := os.MkdirTemp("", "php2go-userland")
		if err != nil {
			userlandErr = err
			return
		}
		for _, library := range userland.Libraries() {
			path := filepath.Join(scratch, filepath.Base(library.VirtualPath))
			if err := os.WriteFile(path, library.Source, 0o644); err != nil {
				userlandErr = err
				return
			}
			unit, err := engine.CompileUnit(path)
			if err != nil {
				userlandErr = fmt.Errorf("compile %s: %w", library.VirtualPath, err)
				return
			}
			aliases := make(map[string]string, len(library.Functions))
			for _, function := range library.Functions {
				aliases[function.Name] = function.Implementation
			}
			userlandLibs = append(userlandLibs, harvestedLibrary{
				path:         library.VirtualPath,
				unit:         ir.EncodeUnit(unit),
				aliases:      aliases,
				initializers: library.Initializers,
			})
		}
	})
	if userlandErr != nil {
		t.Fatalf("harvest userland library: %v", userlandErr)
	}
	return userlandLibs
}

func TestGetImageSizeReadsLoaderSource(t *testing.T) {
	image, err := hex.DecodeString("4749463839610b000d0087")
	if err != nil {
		t.Fatal(err)
	}
	got, err := runWithSources(t, `<?php
		$metadata = ["old" => 1];
		$size = getimagesize("/app/assets/pixel.gif", $metadata);
		echo implode(",", array_slice($size, 0, 4)), "|",
			$size["bits"], ",", $size["channels"], ",", $size["mime"], "|",
			count($metadata);
	`, map[string][]byte{"/app/assets/pixel.gif": image})
	if err != nil {
		t.Fatal(err)
	}
	want := `11,13,1,width="11" height="13"|8,3,image/gif|0`
	if got != want {
		t.Fatalf("getimagesize embedded source = %q, want %q", got, want)
	}
}

func TestSourceRenderingReadsLoaderSource(t *testing.T) {
	source := []byte("<?php\n// gone\nfunction f(int $x): string { return \"x{$x}\"; }\n")
	got, err := runWithSources(t, `<?php
		$path = "/app/src/render.php";
		$source = "<?php\n// gone\nfunction f(int \$x): string { return \"x{\$x}\"; }\n";
		var_dump(highlight_file($path, true) === highlight_string($source, true));
		ob_start(); $answer = show_source($path); $written = ob_get_clean();
		var_dump($answer, $written === highlight_string($source, true));
		echo bin2hex(php_strip_whitespace($path));
	`, map[string][]byte{"/app/src/render.php": source})
	if err != nil {
		t.Fatal(err)
	}
	want := "bool(true)\nbool(true)\nbool(true)\n" +
		"3c3f7068700a2066756e6374696f6e206628696e74202478293a20737472696e67207b2072657475726e2022787b24787d223b207d20"
	if got != want {
		t.Fatalf("source rendering embedded source = %q, want %q", got, want)
	}
}

func TestRealpathCacheTracksSymlinkTargets(t *testing.T) {
	directory := t.TempDir()
	subdirectory := filepath.Join(directory, "sub")
	if err := os.Mkdir(subdirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(subdirectory, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink("sub/file", link); err != nil {
		t.Fatal(err)
	}
	got, err := run(t, fmt.Sprintf(`<?php
		$resolved = realpath(%q);
		$cache = realpath_cache_get();
		$expected_size = 0;
		foreach ($cache as $path => $entry) {
			$expected_size += 48 + strlen($path) + 1;
			if ($path !== $entry["realpath"]) {
				$expected_size += strlen($entry["realpath"]) + 1;
			}
		}
		var_dump(
			$resolved === %q,
			isset($cache[%q]),
			$cache[%q]["realpath"] === %q,
			$cache[%q]["is_dir"] === false,
			isset($cache[%q]),
			$cache[%q]["realpath"] === %q,
			realpath_cache_size() === $expected_size
		);
	`, link, file, link, link, file, link, file, file, file))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Repeat("bool(true)\n", 7)
	if got != want {
		t.Fatalf("realpath cache symlink output = %q, want %q", got, want)
	}
}

// mustRun fails the test on any interpreter error.
func mustRun(t *testing.T, source string) string {
	t.Helper()
	out, err := run(t, source)
	if err != nil {
		t.Fatalf("%v\nsource:\n%s", err, source)
	}
	return out
}

func TestUserlandCanRegisterSuperglobal(t *testing.T) {
	got := mustRun(t, `<?php
		$_RESP_HEADERS = ["pre"];
		function install_response_headers() {
			\PHP2Go\Internal\register_superglobal("_RESP_HEADERS", ["unused"]);
			$_RESP_HEADERS[] = "inside";
		}
		function append_response_header() {
			$_RESP_HEADERS[] = "later";
		}
		install_response_headers();
		append_response_header();
		echo implode(",", $_RESP_HEADERS);
	`)
	if got != "pre,inside,later" {
		t.Fatalf("registered superglobal output = %q, want %q", got, "pre,inside,later")
	}
}

// The library's initializer defines the request's superglobals before the
// program runs, and the first definition wins: a later registration of `$_GET`
// reports that it did nothing and leaves the value alone. A php2go-private name
// is defined but stays out of `$GLOBALS`, where no program on the engine could
// see it.
func TestLibraryDefinesSuperglobalsFirstWins(t *testing.T) {
	got := mustRun(t, `<?php
		function redefine() {
			var_dump(\PHP2Go\Internal\register_superglobal("_GET", ["late"]));
			var_dump(\PHP2Go\Internal\register_superglobal("_PRIVATE_STATE", [1]));
		}
		function read_get() { return count($_GET); }
		redefine();
		echo read_get(), "\n";
		echo implode(",", array_keys($GLOBALS)), "\n";
	`)
	want := "bool(false)\nbool(true)\n0\n_GET,_POST,_COOKIE,_FILES\n"
	if got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestArithmeticAndEcho(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`<?php echo 6 * 7;`, "42"},
		{`<?php $a = 3; $b = 4; echo $a + $b;`, "7"},
		{`<?php echo 7 - 9;`, "-2"},
		{`<?php echo "a" . "b" . "c";`, "abc"},
		{`<?php echo 7 / 2;`, "3.5"},
		{`<?php echo 6 / 3;`, "2"},
		{`<?php $x = 5; $x = $x + 1; echo $x;`, "6"},
	} {
		if got := mustRun(t, tc.source); got != tc.want {
			t.Errorf("%s => %q, want %q", tc.source, got, tc.want)
		}
	}
}

// Control flow exercises the normalized jump targets: a raw byte offset would
// send the program counter somewhere arbitrary.
func TestControlFlow(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`<?php $i = 0; while ($i < 3) { $i = $i + 1; } echo $i;`, "3"},
		{`<?php $i = 0; while ($i < 0) { $i = $i + 1; } echo $i;`, "0"},
		{`<?php if (1 < 2) { echo "yes"; } else { echo "no"; }`, "yes"},
		{`<?php if (2 < 1) { echo "yes"; } else { echo "no"; }`, "no"},
		{`<?php $t = 0; for ($i = 0; $i < 5; $i = $i + 1) { $t = $t + $i; } echo $t;`, "10"},
	} {
		if got := mustRun(t, tc.source); got != tc.want {
			t.Errorf("%s => %q, want %q", tc.source, got, tc.want)
		}
	}
}

func TestIncrementDecrement(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		// Post-increment yields the value from before the change.
		{`<?php $i = 5; echo $i++; echo $i;`, "56"},
		{`<?php $i = 5; echo ++$i; echo $i;`, "66"},
		{`<?php $i = 5; echo $i--; echo $i;`, "54"},
	} {
		if got := mustRun(t, tc.source); got != tc.want {
			t.Errorf("%s => %q, want %q", tc.source, got, tc.want)
		}
	}
}

// PHP 8 comparison semantics, running through the whole pipeline rather than
// against the runtime directly.
func TestComparisons(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{`<?php echo (0 == "abc") ? "T" : "F";`, "F"},
		{`<?php echo (0 == "0") ? "T" : "F";`, "T"},
		{`<?php echo ("10" == "1e1") ? "T" : "F";`, "T"},
		{`<?php echo (1 === "1") ? "T" : "F";`, "F"},
		{`<?php echo (2 <= 2) ? "T" : "F";`, "T"},
	} {
		if got := mustRun(t, tc.source); got != tc.want {
			t.Errorf("%s => %q, want %q", tc.source, got, tc.want)
		}
	}
}

// An opcode with no handler must fail by name and line rather than quietly
// producing a wrong answer. The set of these is the completeness metric.
//
// The subject is found by scanning rather than named outright. Two earlier
// versions of this test picked a feature that had no handler -- generators,
// then match() -- and both were implemented within the day, so the test broke
// for the best possible reason and told the reader nothing. Scanning keeps it
// meaningful for as long as any gap remains, and skips once none does.
func TestUnsupportedOpcodeIsReported(t *testing.T) {
	machine, unit := unhandledOpcodeProbe(t)

	for opcode := uint32(1); opcode < 256; opcode++ {
		name := zend.OpcodeNames()
		if int(opcode) >= len(name) || name[opcode] == "" {
			continue
		}
		err := probeOpcode(machine, unit, opcode)
		var unsupported *interp.UnsupportedError
		if err == nil || !errorsAs(err, &unsupported) {
			continue
		}
		if unsupported.Opcode == "" || !strings.HasPrefix(unsupported.Opcode, "ZEND_") {
			t.Errorf("opcode %d not named: %+v", opcode, unsupported)
		}
		if unsupported.Line == 0 {
			t.Errorf("opcode %d has no source line: %+v", opcode, unsupported)
		}
		return
	}
	t.Skip("every opcode the engine defines now has a handler")
}

// unhandledOpcodeProbe builds the machinery to run a single synthetic opline.
func unhandledOpcodeProbe(t *testing.T) (*interp.Interp, *pb.OpArray) {
	t.Helper()
	constants, err := engine.Constants()
	if err != nil {
		t.Fatal(err)
	}
	manifest := ir.EncodeManifest(ir.Harvest{
		Version: zend.Version(), Layout: zend.DetectLayout(), Opcodes: zend.OpcodeNames(),
		Constants:             constants,
		DefaultErrorReporting: engine.DefaultErrorReporting(), Assertions: engine.Assertions(),
	})
	// One opline, no operands: enough to reach the dispatch switch, which is
	// what this test is about.
	return interp.New(manifest), &pb.OpArray{
		Filename: "probe.php",
		Oplines:  []*pb.Opline{{Line: 1}},
	}
}

// probeOpcode runs the synthetic opline under one opcode. A handler reached
// with no operands may well panic on the garbage it finds, which is not what
// this test is measuring, so that is recovered and treated as "handled".
func probeOpcode(machine *interp.Interp, unit *pb.OpArray, opcode uint32) (err error) {
	defer func() {
		if recover() != nil {
			err = nil
		}
	}()
	unit.Oplines[0].Opcode = opcode
	_, err = machine.Run(php.NewCtx(io.Discard), unit)
	return err
}

func errorsAs(err error, target **interp.UnsupportedError) bool {
	for err != nil {
		if e, ok := err.(*interp.UnsupportedError); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
