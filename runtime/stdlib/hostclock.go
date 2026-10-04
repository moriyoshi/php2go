package stdlib

import (
	"math"
	"time"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The clock, as the harvested library asks for it: a wall time, a monotonic
// reading and a sleep. Three operations behind one name because they are one
// host fact, and because the public functions built on them -- `time`,
// `microtime`, `hrtime`, `sleep`, `usleep` -- are PHP.
//
// It is here rather than in the interpreter because the library is linked into
// compiled programs now and they need the same host.

// monotonicOrigin is what `hrtime` counts from. Go's monotonic clock has no
// zero a program can name, so the process start is the one this picks -- and it
// only has to be *a* fixed point, since every use is a difference.
var monotonicOrigin = time.Now()

func init() {
	php.RegisterBuiltin(`php2go\internal\clock`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.Undef(), php.Raise("Error", "php2go internal clock requires an operation")
		}
		switch args[0].Deref().String() {
		case "wall":
			now := time.Now()
			result := php.NewArray()
			result.Append(php.Int(now.Unix()))
			result.Append(php.Int(int64(now.Nanosecond() / 1_000)))
			return php.Arr(result), nil
		case "monotonic":
			return php.Int(time.Since(monotonicOrigin).Nanoseconds()), nil
		case "sleep":
			if len(args) < 3 {
				return php.Undef(), php.Raise("Error",
					"php2go internal clock sleep requires seconds and nanoseconds")
			}
			seconds := args[1].Deref().Int()
			nanoseconds := args[2].Deref().Int()
			// A `time.Duration` is nanoseconds in an int64, so a sleep longer
			// than about 292 years does not fit in one. PHP takes the seconds
			// as an int and does not care.
			const maxSeconds = int64(math.MaxInt64) / int64(time.Second)
			for seconds > maxSeconds {
				time.Sleep(time.Duration(maxSeconds) * time.Second)
				seconds -= maxSeconds
			}
			time.Sleep(time.Duration(seconds)*time.Second + time.Duration(nanoseconds))
			return php.Null(), nil
		}
		return php.Undef(), php.Raise("Error",
			"php2go internal clock received an unknown operation")
	})
}
