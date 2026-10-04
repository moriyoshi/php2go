package php

import (
	"regexp"
	"strconv"
)

// A **DateInterval** is the one date class whose state is what it shows:
// the engine's get_properties writes y, m, d, h, i, s, f, invert, days and
// from_string into the object's table, so var_dump, get_object_vars,
// foreach, the array cast, json_encode and var_export all read ordinary
// properties (probed). This runtime keeps them as ordinary properties,
// stored once by the constructor.
//
// What does not hold is **writing** them: the engine's write handler
// converts a known name to its field's type (`$d->y = "7"` is int(7)),
// ignores an unset, a reference or `.=` on one, keeps `days` and
// `from_string` false while still creating a dynamic property under the same
// name, and places a dynamic property made before the first get_properties
// *ahead* of the ten (all probed). None of that is modelled, so every write
// through a property of a DateInterval is refused (DateIntervalWriteGap)
// except one to a property its program subclass declares.

// dateIntervalSpec is the ISO 8601 period the engine is known to accept:
// each component at most once, in order, with nine digits at most (the
// engine refuses some longer ones, P999999999999999999D, and accepts
// others).
var dateIntervalSpec = regexp.MustCompile(`^P(?:(\d{1,9})Y)?(?:(\d{1,9})M)?(?:(\d{1,9})W)?(?:(\d{1,9})D)?(?:T(?:(\d{1,9})H)?(?:(\d{1,9})M)?(?:(\d{1,9})S)?)?$`)

// dateIntervalRejected is the shape of a spec the engine is known to
// refuse when the canonical form above does not take it: letters and digits
// only -- no recurrence (`R2/P1D`), no ISO date form, no surrounding space,
// all of which it may accept -- and no digit run long enough to be read as
// the basic `PYYYYMMDD` form (probed: P1Y1Y, P1D1Y, PT1D, P1H, PT1W, p1d,
// P00010203, `now`, "" all `Unknown or bad format`).
var (
	dateIntervalAlnum   = regexp.MustCompile(`^[A-QS-Za-qs-z0-9][A-Za-z0-9]*$|^$`)
	dateIntervalLongRun = regexp.MustCompile(`\d{10}`)
)

// DateIntervalNames are the properties the engine shows, in its order.
var DateIntervalNames = []string{"y", "m", "d", "h", "i", "s", "f", "invert", "days", "from_string"}

func dateIntervalName(name string) bool {
	for _, n := range DateIntervalNames {
		if n == name {
			return true
		}
	}
	return false
}

// DateConstructObject is `__construct` of a date class on the object being
// made. Only DateInterval keeps anything; the others check their arguments
// (DateConstruct).
func DateConstructObject(ctx *Ctx, class string, o *Object, args []Value) error {
	if class != "DateInterval" {
		return DateConstruct(ctx, class, args)
	}
	bound, err := BindNamedArgs(ctx, args, []string{"duration"}, false)
	if err != nil {
		return err
	}
	bound = UnboxArgs(bound)
	passed := len(bound)
	for passed > 0 && bound[passed-1].IsUndef() {
		passed--
	}
	if msg, bad := BuiltinArityError(class+"::__construct", passed, 1, 1); bad {
		return RaiseAt(ctx, "ArgumentCountError", "%s", msg)
	}
	spec, err := dateStringArg(ctx, class, 1, "duration", bound[0])
	if err != nil {
		return err
	}
	m := dateIntervalSpec.FindStringSubmatch(spec)
	if m == nil || spec == "P" || spec == "PT" || spec[len(spec)-1] == 'T' {
		if dateIntervalAlnum.MatchString(spec) && !dateIntervalLongRun.MatchString(spec) {
			return RaiseAt(ctx, "DateMalformedIntervalStringException", "Unknown or bad format (%s)", spec)
		}
		return Unsupported("DateInterval::__construct(%q)", spec)
	}
	// A second construction, or a subclass that declares one of the names,
	// would put the ten where this object does not show them.
	for _, n := range DateIntervalNames {
		if _, present := o.Get(n); present {
			return Unsupported("DateInterval::__construct on an object that has $%s", n)
		}
		if _, declared := o.Declared[n]; declared {
			return Unsupported("DateInterval::__construct on an object that declares $%s", n)
		}
	}
	field := func(i int) int64 {
		v, _ := strconv.ParseInt(m[i], 10, 64)
		return v
	}
	o.Set("y", Int(field(1)))
	o.Set("m", Int(field(2)))
	o.Set("d", Int(field(3)*7+field(4)))
	o.Set("h", Int(field(5)))
	o.Set("i", Int(field(6)))
	o.Set("s", Int(field(7)))
	o.Set("f", Float(0))
	o.Set("invert", Int(0))
	o.Set("days", Bool(false))
	o.Set("from_string", Bool(false))
	return nil
}

// DateIntervalWriteGap refuses a write through a property of a
// DateInterval, nil for any other object and for a property the program's
// subclass declares.
func DateIntervalWriteGap(o *Object, name string) error {
	if o == nil || DateClassOf(o.ClassName) != "DateInterval" {
		return nil
	}
	if _, declared := o.Declared[name]; declared && !dateIntervalName(name) {
		return nil
	}
	return Unsupported("property write to %s::$%s", DisplayName(o.ClassName), name)
}
