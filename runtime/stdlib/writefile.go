package stdlib

import (
	"os"

	"github.com/moriyoshi/php2go/runtime/php"
)

// Writing a file, and removing one. Both are the host answering directly --
// there is no loader to consult, because a loader serves the program's *own*
// sources and a script writing a file means the filesystem.

func init() {
	php.RegisterBuiltin("file_put_contents", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		path := php.Arg(args, 0).Deref().String()
		if IsFilterURL(path) {
			flags := int64(0)
			if len(args) > 2 {
				flags = php.Arg(args, 2).Deref().Int()
			}
			if len(args) > 3 || flags&^8 != 0 || php.Arg(args, 1).Deref().Kind() == php.KindArray ||
				php.Arg(args, 1).Deref().Kind() == php.KindResource || php.Arg(args, 1).Deref().Kind() == php.KindObject {
				return php.Undef(), php.Unsupported("file_put_contents of php://filter with these arguments")
			}
			return FilterURLPut(ctx, path, php.Arg(args, 1).Deref().String(), flags&8 != 0)
		}
		data := php.Arg(args, 1).Deref().String()
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			return php.Bool(false), nil
		}
		return php.Int(int64(len(data))), nil
	})
	php.RegisterBuiltin("unlink", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		path := php.Arg(args, 0).Deref().String()
		if err := os.Remove(path); err != nil {
			ctx.Warn("unlink(%s): No such file or directory", path)
			return php.Bool(false), nil
		}
		return php.Bool(true), nil
	})
}
