package stdlib

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The path helpers. They travelled with the maths because
// `runtime/interp/mathlib.go` held both, which was never a grouping anyone
// chose -- see the note in `mathfns.go`.

func init() {
	php.RegisterBuiltin("dirname", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		path := FirstArg(a).String()
		levels := 1
		if len(a) > 1 {
			levels = int(a[1].Int())
		}
		for ; levels > 0; levels-- {
			path = filepath.Dir(trimTrailingSlashes(path))
		}
		return php.String(path), nil
	})
	php.RegisterBuiltin("basename", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		name := filepath.Base(trimTrailingSlashes(FirstArg(a).String()))
		if len(a) > 1 {
			name = strings.TrimSuffix(name, a[1].String())
			if name == "" {
				name = filepath.Base(trimTrailingSlashes(FirstArg(a).String()))
			}
		}
		return php.String(name), nil
	})
	php.RegisterBuiltin("realpath", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		resolved, err := filepath.Abs(FirstArg(a).String())
		if err != nil {
			return php.Bool(false), nil
		}
		if _, err := os.Stat(resolved); err != nil {
			return php.Bool(false), nil
		}
		return php.String(resolved), nil
	})
	php.RegisterBuiltin("pathinfo", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		path := FirstArg(a).String()
		out := php.NewArray()
		out.Set(php.StringKey("dirname"), php.String(filepath.Dir(path)))
		base := filepath.Base(path)
		out.Set(php.StringKey("basename"), php.String(base))
		if at := strings.LastIndexByte(base, '.'); at >= 0 {
			out.Set(php.StringKey("extension"), php.String(base[at+1:]))
			base = base[:at]
		}
		out.Set(php.StringKey("filename"), php.String(base))
		return php.Arr(out), nil
	})
}

// trimTrailingSlashes drops the separators PHP ignores at the end of a path,
// so dirname("a/b/") is "a" rather than "a/b".
func trimTrailingSlashes(path string) string {
	trimmed := strings.TrimRight(path, "/")
	if trimmed == "" && path != "" {
		return "/"
	}
	return trimmed
}
