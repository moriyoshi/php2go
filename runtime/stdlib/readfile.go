package stdlib

import (
	"io"
	"os"

	"github.com/moriyoshi/php2go/runtime/php"
)

// Reading a file's bytes, which is what `file_get_contents`, `file`,
// `readfile` and `parse_ini_file` are all built on in the harvested library.
//
// Three sources, in this order: a `data:` URI carries its own content, a
// **loader** may have the bytes without the host filesystem having them, and
// otherwise it is an ordinary read. The loader is a hook because only the
// interpreter has one -- a bundle serves its PHP from `go:embed`, where
// `os.ReadFile` would find nothing.

func init() {
	php.RegisterBuiltin(`php2go\internal\read_file`, func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if IsFilterURL(php.Arg(args, 0).Deref().String()) {
			// The library's readers name themselves in their diagnostics,
			// which this leaf cannot; file_get_contents has its own arm.
			return php.Undef(), php.Unsupported("reading %s", php.Arg(args, 0).Deref().String())
		}
		data, err := ReadFile(ctx, php.Arg(args, 0).Deref().String())
		if err != nil {
			return php.Bool(false), nil
		}
		return php.String(string(data)), nil
	})
	php.RegisterBuiltin("file_get_contents", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		path := php.Arg(args, 0).Deref().String()
		if IsFilterURL(path) {
			if len(args) > 1 {
				return php.Undef(), php.Unsupported("file_get_contents of php://filter with options")
			}
			return FilterURLContents(ctx, path)
		}
		data, err := ReadFile(ctx, path)
		if err != nil {
			ctx.Warn("file_get_contents(%s): Failed to open stream: No such file or directory", path)
			return php.Bool(false), nil
		}
		return php.String(string(data)), nil
	})
	php.RegisterBuiltin("is_resource", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		handle := php.Arg(args, 0).Deref().Resource()
		return php.Bool(handle != nil && !handle.Closed()), nil
	})
}

// ReadFile is that read, for a caller inside this package as much as for the
// leaf above.
func ReadFile(ctx *php.Ctx, path string) ([]byte, error) {
	if held, ok := DataStream(path); ok {
		body, _ := io.ReadAll(held.MemoryReader())
		return body, nil
	}
	if data, handled, err := ctx.ReadSource(path); handled {
		if err == nil {
			return data, nil
		}
		// **Only "not there" falls through.** A loader that has the file and
		// cannot read it is an error, not an invitation to look somewhere else.
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return os.ReadFile(path)
}
