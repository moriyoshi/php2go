package main

import (
	"path/filepath"
	"testing"

	"github.com/moriyoshi/php2go/internal/ir"
	"github.com/moriyoshi/php2go/internal/zend"
	"github.com/moriyoshi/php2go/runtime/interp"
)

// Some behaviour only shows itself when a request ends: shutdown functions and
// the exception handler both fire after the script body. The differential
// oracle cannot see any of it, because it drives the engine through eval(),
// which never ends a request -- so these are checked as .phpt cases with
// expectations written against PHP's documented ordering.
func TestRequestLifetime(t *testing.T) {
	paths, err := filepath.Glob("../../testdata/lifetime/*.phpt")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no lifetime tests found: %v", err)
	}

	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	constants, err := engine.Constants()
	if err != nil {
		t.Fatal(err)
	}
	builtinClasses, err := engine.InternalClasses()
	if err != nil {
		t.Fatal(err)
	}
	framelessFunctions, err := engine.FramelessFunctions()
	if err != nil {
		t.Fatal(err)
	}
	manifest := ir.EncodeManifest(ir.Harvest{
		Version: zend.Version(), Layout: zend.DetectLayout(), Opcodes: zend.OpcodeNames(),
		Constants: constants, BuiltinClasses: builtinClasses,
		FramelessFunctions:    framelessFunctions,
		DefaultErrorReporting: engine.DefaultErrorReporting(), Assertions: engine.Assertions(),
	})

	scratch := t.TempDir()
	machine := interp.New(manifest)
	machine.SetLoader(&ir.EngineLoader{Engine: engine, Scratch: scratch})

	for _, path := range paths {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			// Each test starts from a clean engine, or a declaration left
			// behind by an earlier one changes how a later one compiles.
			if err := engine.Reset(); err != nil {
				t.Fatal(err)
			}
			outcome, detail, _, actual := runOne(engine, machine, path)
			if outcome != outcomePass {
				t.Errorf("%s: %v (%s)\ngot:\n%s", name, outcome, detail, actual)
			}
		})
	}
}
