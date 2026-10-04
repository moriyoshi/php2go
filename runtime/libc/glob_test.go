//go:build cgo && !windows

package libc

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGlob(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"alpha.txt", "beta.txt", "literal*.txt"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(directory, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	flags, available := PlatformGlobFlags()
	if !available {
		t.Fatal("platform glob flags are unavailable")
	}
	matches, status, available := Glob(filepath.Join(directory, "*.txt"), 0)
	if !available || status != 0 {
		t.Fatalf("Glob() = %#v, %d, %v", matches, status, available)
	}
	want := []string{
		filepath.Join(directory, "alpha.txt"),
		filepath.Join(directory, "beta.txt"),
		filepath.Join(directory, "literal*.txt"),
	}
	if !reflect.DeepEqual(matches, want) {
		t.Fatalf("Glob() = %#v, want %#v", matches, want)
	}

	matches, status, available = Glob(
		filepath.Join(directory, "missing-*"), flags.NoCheck,
	)
	if !available || status != 0 || !reflect.DeepEqual(matches, []string{filepath.Join(directory, "missing-*")}) {
		t.Fatalf("Glob(NOCHECK) = %#v, %d, %v", matches, status, available)
	}

	matches, status, available = Glob(
		filepath.Join(directory, "*"), flags.OnlyDir,
	)
	if !available || status != 0 || !reflect.DeepEqual(matches, []string{filepath.Join(directory, "nested")}) {
		t.Fatalf("Glob(ONLYDIR) = %#v, %d, %v", matches, status, available)
	}
}

func TestGlobMatch(t *testing.T) {
	for _, test := range []struct {
		pattern, name string
		noEscape      bool
		want          bool
	}{
		{"*.php", "file.php", false, true},
		{"*", ".hidden", false, false},
		{`literal\*.php`, "literal*.php", false, true},
		{`literal\*.php`, "literal*.php", true, false},
	} {
		got, available := GlobMatch(test.pattern, test.name, test.noEscape)
		if !available || got != test.want {
			t.Errorf("GlobMatch(%q, %q, %v) = %v, %v; want %v",
				test.pattern, test.name, test.noEscape, got, available, test.want)
		}
	}
}
