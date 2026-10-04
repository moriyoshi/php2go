package interp

import (
	"math"

	"github.com/moriyoshi/php2go/runtime/php"
)

// fallbackConstants cover the case where a manifest was built without
// harvesting the engine's constant table. The harvested values take precedence,
// so this is a floor rather than the source of truth -- which matters because
// several of these are build-specific.
var fallbackConstants = map[string]php.Value{
	"PHP_EOL":           php.String("\n"),
	"PHP_INT_MAX":       php.Int(math.MaxInt64),
	"PHP_INT_MIN":       php.Int(math.MinInt64),
	"PHP_INT_SIZE":      php.Int(8),
	"PHP_FLOAT_EPSILON": php.Float(2.220446049250313e-16),
	"PHP_FLOAT_MAX":     php.Float(math.MaxFloat64),
	"PHP_FLOAT_MIN":     php.Float(2.2250738585072014e-308),
	"PHP_FLOAT_DIG":     php.Int(15),
	"NAN":               php.Float(math.NaN()),
	"INF":               php.Float(math.Inf(1)),
	"TRUE":              php.Bool(true),
	"FALSE":             php.Bool(false),
	"NULL":              php.Null(),
	"M_PI":              php.Float(math.Pi),
	"M_E":               php.Float(math.E),
}
