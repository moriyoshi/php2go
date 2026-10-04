//go:build !cgo || windows

package libc

type GlobFlags struct {
	Brace, Err, Mark, NoCheck, NoEscape, NoSort, OnlyDir int64
}

func PlatformGlobFlags() (GlobFlags, bool) { return GlobFlags{}, false }

func Glob(string, int64) ([]string, int, bool) { return nil, 0, false }

func GlobMatch(string, string, bool) (bool, bool) { return false, false }
