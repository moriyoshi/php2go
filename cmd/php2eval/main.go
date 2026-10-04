// Command php2eval runs PHP through the linked engine and, unless told not to,
// through the interpreter as well, printing both outputs.
//
// It is the oracle in interactive form. Deciding what PHP does by reading the
// sources or by memory has been wrong often enough that asking the engine is
// the cheaper habit; this makes asking a single command.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/moriyoshi/php2go/internal/ir"
	"github.com/moriyoshi/php2go/internal/zend"
	"github.com/moriyoshi/php2go/runtime/interp"
	"github.com/moriyoshi/php2go/runtime/php"
)

func main() {
	code := flag.String("c", "", "PHP to run, without the opening tag (default: read stdin)")
	engineOnly := flag.Bool("engine", false, "only run the engine, skipping the interpreter")
	raw := flag.Bool("raw", false, "print only the engine's output, byte for byte (implies -engine)")
	as := flag.String("as", "php2go-eval", "filename the engine reports in diagnostics")
	recordEvals := flag.String("record-evals", "", "write every string eval compiles to this file (zend.ReadEvalRecords)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: php2eval [-engine|-raw] [-c code | < file.php]\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	body := *code
	if body == "" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fail(err)
		}
		// A whole file may carry its own opening tag; the engine's eval must not
		// see one, so strip it and let the rest through unchanged.
		body = stripOpenTag(string(data))
	}
	if err := run(body, *engineOnly || *raw, *raw, *as, *recordEvals); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "php2eval:", err)
	os.Exit(1)
}

func stripOpenTag(source string) string {
	for _, tag := range []string{"<?php\n", "<?php ", "<?php\t", "<?php"} {
		if len(source) >= len(tag) && source[:len(tag)] == tag {
			return source[len(tag):]
		}
	}
	return source
}

// fatalOutput reports whether a failed evaluation ended in a PHP fatal
// error, whose printed output is the program's answer.
func fatalOutput(err error, out string) bool {
	return err != nil && strings.Contains(err.Error(), "evaluation bailed out") &&
		(strings.Contains(out, "Fatal error: ") || strings.Contains(out, "Parse error: "))
}

func run(body string, engineOnly, raw bool, as, recordEvals string) error {
	engine, err := zend.Start()
	if err != nil {
		return err
	}
	defer engine.Close()
	if recordEvals != "" {
		if err := engine.RecordEvals(recordEvals); err != nil {
			return err
		}
	}

	want, err := engine.EvalAs(body, as)
	if err != nil {
		// **A PHP fatal is output, not a failure.** A compile-time fatal --
		// `Cannot redeclare`, an abstract method in a concrete class -- or a
		// run-time one ends the request by bailing out, and what it printed
		// up to then, the `Fatal error:` line included, is exactly what php
		// prints. The raw oracle hands that back; any other bail is still an
		// error, with nothing to compare against.
		if raw && fatalOutput(err, want) {
			_, werr := io.WriteString(os.Stdout, want)
			return werr
		}
		return fmt.Errorf("engine: %w", err)
	}
	if raw {
		// Byte for byte, with no header and no added newline. The third-arm
		// harness diffs a generated program's stdout against this, so a framing
		// character here is a false mismatch in every comparison it makes.
		_, err := io.WriteString(os.Stdout, want)
		return err
	}
	fmt.Printf("--- ENGINE ---\n%s\n", want)
	if engineOnly {
		return nil
	}

	// The interpreter needs the same engine to compile, so it must not inherit
	// the declarations the eval above just made.
	if err := engine.Reset(); err != nil {
		return err
	}
	got, err := viaInterpreter(engine, body)
	if err != nil {
		fmt.Printf("--- INTERPRETER (error) ---\n%v\n", err)
		return nil
	}
	fmt.Printf("--- INTERPRETER ---\n%s\n", got)
	if got == want {
		fmt.Println("--- MATCH ---")
	} else {
		fmt.Println("--- MISMATCH ---")
	}
	return nil
}

func viaInterpreter(engine *zend.Engine, body string) (string, error) {
	// The compiler records the filename and resolves includes against it, so
	// the source has to reach it as a file rather than a string.
	tmp, err := os.CreateTemp("", "php2eval-*.php")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString("<?php " + body); err != nil {
		tmp.Close()
		return "", err
	}
	tmp.Close()

	unit, err := engine.CompileMainUnit(tmp.Name())
	if err != nil {
		return "", err
	}
	constants, err := engine.Constants()
	if err != nil {
		return "", err
	}
	framelessFunctions, err := engine.FramelessFunctions()
	if err != nil {
		return "", err
	}
	builtinClasses, err := engine.InternalClasses()
	if err != nil {
		return "", err
	}
	builtins, err := engine.Builtins()
	if err != nil {
		return "", err
	}
	iniEntries, err := engine.IniEntries()
	if err != nil {
		return "", err
	}
	extensions, err := engine.Extensions()
	if err != nil {
		return "", err
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
	// include, require and eval need a compiler at run time; the engine that
	// produced the IR is already linked in, so it serves as one.
	scratch, err := os.MkdirTemp("", "php2eval-eval")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(scratch)
	machine.SetLoader(&ir.EngineLoader{Engine: engine, Scratch: scratch})
	if _, err := machine.RunUnit(ctx, interp.Load(ir.EncodeUnit(unit))); err != nil {
		return out.String(), err
	}
	return out.String(), nil
}
