package stdlib

import (
	"os"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// What the process answers about itself: the SAPI it claims to be, its
// environment, and the resource stubs php2go does not model. Split out of the
// maths for the reason `mathfns.go` records.

func init() {
	php.RegisterBuiltin("php_sapi_name", func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		// "cli", not the "embed" the differential harness's own engine reports:
		// the .phpt corpus records what the CLI answers, and that is the SAPI
		// php2go is standing in for. This one is deliberately not covered by a
		// differential case, because the two SAPIs cannot both be right.
		return php.String("cli"), nil
	})
	php.RegisterBuiltin("php_uname", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) > 0 && args[0].String() == "n" {
			hostname, err := os.Hostname()
			if err == nil {
				return php.String(hostname), nil
			}
		}
		return php.String("Linux"), nil
	})
	php.RegisterBuiltin("getenv", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		if len(a) == 0 {
			out := php.NewArray()
			for _, entry := range os.Environ() {
				if name, value, ok := strings.Cut(entry, "="); ok {
					out.Set(php.StringKey(name), php.String(value))
				}
			}
			return php.Arr(out), nil
		}
		value, found := os.LookupEnv(FirstArg(a).String())
		if !found {
			return php.Bool(false), nil
		}
		return php.String(value), nil
	})
	php.RegisterBuiltin("putenv", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		name, value, hasValue := strings.Cut(FirstArg(a).String(), "=")
		if !hasValue {
			return php.Bool(os.Unsetenv(name) == nil), nil
		}
		return php.Bool(os.Setenv(name, value) == nil), nil
	})
	php.RegisterBuiltin("memory_get_usage", constantInt(2097152))
	php.RegisterBuiltin("memory_get_peak_usage", constantInt(2097152))
	php.RegisterBuiltin("set_time_limit", func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Bool(true), nil
	})
}

func constantInt(n int64) php.Builtin {
	return func(_ *php.Ctx, _ []php.Value) (php.Value, error) { return php.Int(n), nil }
}
