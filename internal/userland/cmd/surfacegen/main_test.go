package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedSurfaceIsCurrent(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	stub := filepath.Join(root, "third_party", "php-src", "ext", "standard", "basic_functions.stub.php")
	if _, err := os.Stat(stub); err != nil {
		t.Skip("php-src is not available in this test sandbox")
	}
	got, err := generateSurface(root)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(root, "internal", "userland", "surface.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("surface.tsv is stale; run go generate ./internal/userland")
	}
}

func TestRegisteredFunctionsFindsSharedBuiltins(t *testing.T) {
	dir := t.TempDir()
	source := `package php
func init() {
	RegisterBuiltin("count", nil)
	RegisterBuiltin("php2go\\internal\\helper", nil)
}
`
	if err := os.WriteFile(filepath.Join(dir, "builtins.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored_test.go"), []byte(
		`package php
func init() { RegisterBuiltin("test_only", nil) }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := registeredFunctions(dir, "php")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"count", "php2go\\internal\\helper"} {
		if !got[name] {
			t.Errorf("registeredFunctions() omitted %q: %+v", name, got)
		}
	}
	if got["test_only"] {
		t.Errorf("registeredFunctions() included a test-only registration: %+v", got)
	}
}
