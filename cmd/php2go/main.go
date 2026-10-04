// Command php2go is the driver for the PHP-to-Go pipeline.
//
// It harvests inspectable IR and can package a whole project into a generated
// Go module whose sources and units are carried by go:embed.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/moriyoshi/php2go/internal/bundlebuild"
	"github.com/moriyoshi/php2go/internal/ir"
	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/internal/zend"
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr))
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	if args[0] == "help" && len(args) > 1 {
		if commandUsage(args[1], stdout) {
			return 0
		}
		fmt.Fprintf(stderr, "php2go: unknown command %q\n\n", args[1])
		usage(stderr)
		return 2
	}
	if len(args) > 1 && (args[1] == "-h" || args[1] == "--help" || args[1] == "help") {
		if commandUsage(args[0], stdout) {
			return 0
		}
	}
	var err error
	switch args[0] {
	case "dump":
		err = dump(args[1:])
	case "bundle":
		err = bundle(args[1:])
	case "profile":
		err = profile(args[1:])
	case "-h", "--help", "help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "php2go: unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "php2go:", err)
		fmt.Fprintf(stderr, "Run 'php2go %s --help' for usage.\n", args[0])
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: php2go <command> [options]

Compile PHP with Zend and inspect or package the harvested program.

Commands:
  dump     Inspect a PHP file or write its harvested IR
  bundle   Package a PHP project as a Go application
  profile  Record computed eval strings from a reference run

Run 'php2go <command> --help' for options and an example.
`)
}

func commandUsage(command string, w io.Writer) bool {
	switch command {
	case "dump":
		fmt.Fprint(w, `Usage: php2go dump [options] <file.php>
       php2go dump -decode <unit.pb>

Inspect Zend's compiled representation of a PHP file.

Options:
  -o <file>         Write encoded IR instead of a text summary
  -manifest <file>  Write the linked engine's manifest
  -decode <file>    Summarize an existing IR file without starting Zend

Example: php2go dump -o program.pb index.php
`)
	case "bundle":
		fmt.Fprint(w, `Usage: php2go bundle -o <new-directory> [options] <entry.php>

Package the entry file and its project into a Go module. The output
directory must not already exist.

Options:
  -o <directory>          Destination for the generated Go module
  -root <directory>       Project root (default: entry file's directory)
  -runtime-root <dir>     Local php2go module (default: find from current directory)

Example: php2go bundle -root ./app -o ./app-bundle index.php
Build:   (cd ./app-bundle && go build .)
`)
	case "profile":
		fmt.Fprint(w, `Usage: php2go profile -o <file> [options] <entry.php>

Run a PHP entry file through Zend and record the eval strings reached
by that run. The profile records observed inputs only.

Options:
  -o <file>          Destination for the eval profile
  -root <directory>  Root used to name eval sites (default: current directory)

Example: php2go profile -root ./app -o eval-profile.json ./app/index.php
`)
	default:
		return false
	}
	return true
}

// profile records the exact strings Zend compiled at eval sites during one
// reference execution. Compilation consumes this only when explicitly given
// the resulting file with php2gotest -eval-profile.
func profile(args []string) error {
	flags := flag.NewFlagSet("profile", flag.ContinueOnError)
	out := flags.String("o", "", "write the eval profile to this file")
	root := flags.String("root", ".", "compilation root used to name eval sites")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 || *out == "" {
		return fmt.Errorf("profile needs -o <file> and exactly one PHP entry file")
	}
	path, err := filepath.Abs(flags.Arg(0))
	if err != nil {
		return err
	}
	base, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	name, err := filepath.Rel(base, path)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	engine, err := zend.Start()
	if err != nil {
		return err
	}
	defer engine.Close()
	if err := engine.RecordEvals(*out); err != nil {
		return err
	}
	result, err := engine.EvalAs(profileSource(string(src)), name)
	if result != "" {
		if _, writeErr := fmt.Fprint(os.Stdout, result); writeErr != nil {
			return writeErr
		}
	}
	return err
}

// profileSource keeps source lines and offsets aligned with file compilation.
func profileSource(src string) string {
	if len(src) >= 5 && strings.EqualFold(src[:5], "<?php") {
		return "     " + src[5:]
	}
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

func bundle(args []string) error {
	flags := flag.NewFlagSet("bundle", flag.ContinueOnError)
	root := flags.String("root", "", "project root (default: entry file's directory)")
	out := flags.String("o", "", "write the generated Go module to this new directory")
	runtimeRoot := flags.String("runtime-root", "", "local php2go module used by the generated go.mod")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("bundle needs exactly one PHP entry file")
	}
	if *out == "" {
		return fmt.Errorf("bundle needs -o <directory>")
	}
	if *runtimeRoot == "" {
		*runtimeRoot = findRuntimeRoot()
	}
	if *runtimeRoot == "" {
		return fmt.Errorf("cannot locate the php2go module; use -runtime-root")
	}
	report, err := bundlebuild.Build(bundlebuild.Options{
		Root: *root, Entry: flags.Arg(0), Output: *out, RuntimeRoot: *runtimeRoot,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "bundled %d files (%d PHP, %d source bytes) to %s\n",
		report.Files, report.PHPFiles, report.Bytes, report.Output)
	if report.HasVendor {
		fmt.Fprintln(os.Stderr, "included installed Composer dependencies from vendor/")
	}
	fmt.Fprintf(os.Stderr, "build with: (cd %s && go build .)\n", report.Output)
	return nil
}

func findRuntimeRoot() string {
	if dir, err := os.Getwd(); err == nil {
		if root := findRuntimeRootFrom(dir); root != "" {
			return root
		}
	}
	if _, source, _, ok := runtime.Caller(0); ok && filepath.IsAbs(source) {
		return findRuntimeRootFrom(filepath.Dir(source))
	}
	return ""
}

func findRuntimeRootFrom(dir string) string {
	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.HasPrefix(string(data), "module github.com/moriyoshi/php2go\n") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func dump(args []string) error {
	flags := flag.NewFlagSet("dump", flag.ContinueOnError)
	out := flags.String("o", "", "write the encoded unit here instead of summarizing it")
	manifest := flags.String("manifest", "", "also write the harvest manifest here")
	decode := flags.String("decode", "", "read a previously encoded unit and summarize it")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *decode != "" && (*out != "" || *manifest != "" || flags.NArg() != 0) {
		return fmt.Errorf("dump -decode cannot be combined with a PHP file, -o, or -manifest")
	}

	// Decoding is deliberately independent of the engine: reading IR must not
	// require a PHP build, only producing it does.
	if *decode != "" {
		data, err := os.ReadFile(*decode)
		if err != nil {
			return err
		}
		unit, err := ir.UnmarshalUnit(data)
		if err != nil {
			return err
		}
		summarize(unit)
		return nil
	}

	if flags.NArg() != 1 {
		return fmt.Errorf("dump needs exactly one PHP file (or -decode)")
	}
	path, err := filepath.Abs(flags.Arg(0))
	if err != nil {
		return err
	}

	engine, err := zend.Start()
	if err != nil {
		return err
	}
	defer engine.Close()

	harvested, err := engine.CompileUnit(path)
	if err != nil {
		return err
	}
	encoded := ir.EncodeUnit(harvested)

	if *manifest != "" {
		builtins, err := engine.Builtins()
		if err != nil {
			return err
		}
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
		iniEntries, err := engine.IniEntries()
		if err != nil {
			return err
		}
		extensions, err := engine.Extensions()
		if err != nil {
			return err
		}
		data, err := ir.MarshalManifest(
			ir.EncodeManifest(ir.Harvest{
				Version: zend.Version(), Layout: zend.DetectLayout(), Opcodes: zend.OpcodeNames(),
				Builtins: builtins, Constants: constants, BuiltinClasses: builtinClasses,
				FramelessFunctions:    framelessFunctions,
				IniEntries:            iniEntries,
				Extensions:            extensions,
				DefaultErrorReporting: engine.DefaultErrorReporting(), Assertions: engine.Assertions(),
			}))
		if err != nil {
			return err
		}
		if err := os.WriteFile(*manifest, data, 0o644); err != nil {
			return err
		}
	}

	if *out == "" {
		summarize(encoded)
		return nil
	}
	data, err := ir.MarshalUnit(encoded)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", *out, len(data))
	return nil
}

func summarize(unit *pb.Unit) {
	file := unit.GetFile()
	fmt.Printf("%s  lines %d-%d\n", file.GetFilename(), file.GetLineStart(), file.GetLineEnd())
	fmt.Printf("  top level: %d oplines, %d literals, %d vars\n",
		len(file.GetOplines()), len(file.GetLiterals()), len(file.GetVarNames()))
	for _, fn := range unit.GetFunctions() {
		fmt.Printf("  function %s: %d args, %d oplines\n",
			fn.GetName(), len(fn.GetArgs()), len(fn.GetOplines()))
	}
	for _, closure := range file.GetClosures() {
		fmt.Printf("  closure: %d args, %d oplines\n", len(closure.GetArgs()), len(closure.GetOplines()))
	}
	for _, class := range unit.GetClasses() {
		fmt.Printf("  %s %s: %d properties, %d methods",
			kindName(class.GetKind()), class.GetName(),
			len(class.GetProperties()), len(class.GetMethods()))
		if class.GetHasGet() || class.GetHasCall() {
			fmt.Print("  [dynamic]")
		}
		fmt.Println()
	}
}

func kindName(kind pb.Class_Kind) string {
	switch kind {
	case pb.Class_KIND_CLASS:
		return "class"
	case pb.Class_KIND_INTERFACE:
		return "interface"
	case pb.Class_KIND_TRAIT:
		return "trait"
	case pb.Class_KIND_ENUM:
		return "enum"
	}
	return "?"
}
