package php

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// ContentHash is the hex SHA-256 of a file's bytes: what a computed include
// compares against the hash recorded when the program was built, so a file
// that has changed since -- or was written by the program itself -- is not
// mistaken for the body compiled in for that path.
func ContentHash(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), true
}

// ResolveIncludePath finds the file an include of path opens, the way the
// engine's php_resolve_path does: an absolute path as it is; one starting
// `./` or `../` against the working directory alone; any other relative
// path through each include_path entry, then the directory of the file
// that is running (executing), then the working directory. The answer is
// the first that names a regular file, with its symlinks resolved -- the
// path an `_once` compares and the included file's `__FILE__`. ok is false
// when no candidate exists.
func ResolveIncludePath(path, includePath, executing string) (string, bool) {
	try := func(p string) (string, bool) {
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			return "", false
		}
		if abs, err := filepath.Abs(real); err == nil {
			real = abs
		}
		fi, err := os.Stat(real)
		if err != nil || !fi.Mode().IsRegular() {
			return "", false
		}
		return real, true
	}
	if filepath.IsAbs(path) {
		return try(path)
	}
	if strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") {
		return try(path)
	}
	for _, dir := range strings.Split(includePath, string(os.PathListSeparator)) {
		if dir == "" {
			continue
		}
		if r, ok := try(filepath.Join(dir, path)); ok {
			return r, true
		}
	}
	if executing != "" {
		if r, ok := try(filepath.Join(filepath.Dir(executing), path)); ok {
			return r, true
		}
	}
	return try(path)
}
