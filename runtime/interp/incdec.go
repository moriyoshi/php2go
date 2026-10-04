package interp

import (
	"errors"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// `++` and `--` have their own table of behaviours per type, and it now lives in
// `runtime/php` -- compiled code needs the same table, and two copies of it
// would diverge on whichever consumer was exercised less.
//
// What stays here is the part that needs a class table: refusing to step an
// array or an object is a `TypeError`, which `runtime/php` cannot construct, so
// it reports a sentinel and this maps it.

// increment applies `++`, reporting whatever diagnostic the value earns. It
// returns the new value, which for several types is the old one unchanged.
func (in *Interp) increment(ctx *php.Ctx, unit *Unit, value php.Value) (php.Value, error) {
	stepped, err := php.Increment(ctx, value)
	if errors.Is(err, php.ErrCannotIncrement) {
		return value, in.throwError(ctx, unit, "TypeError",
			"Cannot increment %s", php.IncDecTypeName(value))
	}
	return stepped, err
}

// decrement applies `--`.
func (in *Interp) decrement(ctx *php.Ctx, unit *Unit, value php.Value) (php.Value, error) {
	stepped, err := php.Decrement(ctx, value)
	if errors.Is(err, php.ErrCannotDecrement) {
		return value, in.throwError(ctx, unit, "TypeError",
			"Cannot decrement %s", php.IncDecTypeName(value))
	}
	return stepped, err
}

// stepValue applies whichever of ++ or -- the opcode names.
func (in *Interp) stepValue(ctx *php.Ctx, unit *Unit, opcode string, value php.Value) (php.Value, error) {
	if strings.Contains(opcode, "_DEC") {
		return in.decrement(ctx, unit, value)
	}
	return in.increment(ctx, unit, value)
}
