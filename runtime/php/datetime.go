package php

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The date extension's classes -- DateTime, DateTimeImmutable, DateTimeZone
// -- are modelled as **opaque objects**: a program may make one from a string
// this runtime can prove the engine accepts, extend the class, pass the object
// around and compare it by identity, and nothing more. Their state (the time,
// the zone) is never held, so everything that would show it is refused: the
// methods, the dumps, the array cast, serialization, json_encode, var_export,
// and comparing two of them, which the engine does by time. What is left --
// get_object_vars, foreach, a dynamic property, instanceof, clone, a string
// conversion's Error -- is the ordinary object behaviour the engine gives
// them too (probed: none of those reads the date).

// DateClassOf answers the date class a class is or descends from, "" for any
// other. Compiled code installs it, since the class hierarchy is the
// program's; left unset nothing is a date object.
var DateClassOf = func(class string) string { return "" }

// IsDateObject reports an object whose state this runtime does not hold.
func IsDateObject(o *Object) bool {
	return o != nil && DateClassOf(o.ClassName) != ""
}

// HidesDateState reports a date object whose state is not in its
// properties: every date object but a DateInterval, whose state is exactly
// the properties it shows (dateinterval.go).
func HidesDateState(o *Object) bool {
	d := ""
	if o != nil {
		d = DateClassOf(o.ClassName)
	}
	return d != "" && d != "DateInterval"
}

// DateGap is the refusal for an operation that would show a date object's
// state.
func DateGap(o *Object, what string) error {
	return Unsupported("%s of a %s object", what, DateClassOf(o.ClassName))
}

// AbortGap ends the program on a gap met where no error can be returned --
// deep in a comparison, which has no way to fail. It is what the generated
// program does with an unsupported error that reaches the top.
var AbortGap = func(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(3)
}

// refuseDatePair aborts a comparison of two date objects, which the engine
// makes by their time (DateTime and DateTimeImmutable with each other) or
// their zone (DateTimeZone), neither of which is held. One date object beside
// anything else takes the ordinary object rules, as the engine's handler falls
// back to them.
func refuseDatePair(x, y *Object) {
	if x == nil || y == nil || x == y {
		return
	}
	a, b := DateClassOf(x.ClassName), DateClassOf(y.ClassName)
	if a == "" || b == "" {
		return
	}
	// Two DateIntervals are uncomparable: a warning, which this comparison
	// has no way to raise, and then == false and < false alike (probed:
	// `Cannot compare DateInterval objects`). Refused, and so is one beside
	// another date class, which nothing has probed.
	if a == "DateInterval" || b == "DateInterval" {
		AbortGap(DateGap(x, "comparison"))
	}
	if (a == "DateTimeZone") != (b == "DateTimeZone") {
		return
	}
	AbortGap(DateGap(x, "comparison"))
}

var (
	dateOnly     = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	dateAndTime  = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2}) (\d{2}):(\d{2}):(\d{2})$`)
	unixSeconds  = regexp.MustCompile(`^@-?\d{1,12}$`)
	daysInMonths = [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
)

// DateConstruct is `__construct` of a date class: the arguments are checked
// and nothing is kept. A string is accepted only in a form the engine is known
// to parse without complaint -- "", "now", "@<seconds>", "YYYY-MM-DD" and
// "YYYY-MM-DD HH:MM:SS" with a real calendar date and time -- and a zone only
// as "UTC". Anything else is refused rather than guessed: the engine's parser
// accepts far more, and rejects with messages this runtime does not produce.
func DateConstruct(ctx *Ctx, class string, args []Value) error {
	if class == "DateInterval" {
		return Unsupported("DateInterval::__construct without its object")
	}
	names := []string{"datetime", "timezone"}
	if class == "DateTimeZone" {
		names = []string{"timezone"}
	}
	bound, err := BindNamedArgs(ctx, args, names, false)
	if err != nil {
		return err
	}
	bound = UnboxArgs(bound)
	// **The engine's argument checks come first** (probed): DateTimeZone
	// takes exactly one argument and the others at most two, as internal
	// functions count them -- `new DateTimeZone` is an ArgumentCountError,
	// which instantiate_all_classes catches.
	least, most := 0, 2
	if class == "DateTimeZone" {
		least, most = 1, 1
	}
	passed := len(bound)
	for passed > 0 && bound[passed-1].IsUndef() {
		passed--
	}
	if msg, bad := BuiltinArityError(class+"::__construct", passed, least, most); bad {
		return RaiseAt(ctx, "ArgumentCountError", "%s", msg)
	}
	if class == "DateTimeZone" {
		zone, err := dateStringArg(ctx, class, 1, "timezone", bound[0])
		if err != nil {
			return err
		}
		if zone != "UTC" {
			return Unsupported("DateTimeZone::__construct(%q)", zone)
		}
		return nil
	}
	if len(bound) > 0 && !bound[0].IsUndef() {
		text, err := dateStringArg(ctx, class, 1, "datetime", bound[0])
		if err != nil {
			return err
		}
		if !dateStringAccepted(text) {
			return Unsupported("%s::__construct(%q)", class, text)
		}
	}
	if len(bound) > 1 && !bound[1].IsUndef() {
		zone := bound[1].Deref()
		if zone.Kind() == KindNull {
			return nil
		}
		if zone.Kind() != KindObject || DateClassOf(zone.Object().ClassName) != "DateTimeZone" {
			return RaiseAt(ctx, "TypeError", "%s::__construct(): Argument #2 ($timezone) must be of type ?DateTimeZone, %s given",
				class, ValueName(zone))
		}
	}
	return nil
}

// dateStringArg reads a date constructor's `string` parameter as the
// engine's argument parsing does (probed): a string as is; an array, or an
// object with no `__toString`, a TypeError in either mode. What depends on
// strict_types -- null (a deprecation in weak mode, a TypeError in strict)
// and a scalar or stringable object (coerced, or refused) -- is refused, since
// the call site's mode does not reach here.
func dateStringArg(ctx *Ctx, class string, n int, name string, arg Value) (string, error) {
	v := arg.Deref()
	switch v.Kind() {
	case KindString:
		return v.String(), nil
	case KindArray:
		return "", RaiseAt(ctx, "TypeError", "%s::__construct(): Argument #%d ($%s) must be of type string, array given", class, n, name)
	case KindObject:
		if o := v.Object(); !ctx.HasMethod(o.ClassName, "__tostring") {
			return "", RaiseAt(ctx, "TypeError", "%s::__construct(): Argument #%d ($%s) must be of type string, %s given",
				class, n, name, ValueName(v))
		}
	}
	return "", Unsupported("%s::__construct(%s)", class, ValueName(v))
}

func dateStringAccepted(s string) bool {
	if s == "" || s == "now" || unixSeconds.MatchString(s) {
		return true
	}
	if m := dateAndTime.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[4])
		i, _ := strconv.Atoi(m[5])
		sec, _ := strconv.Atoi(m[6])
		return calendarDate(m[1], m[2], m[3]) && h < 24 && i < 60 && sec < 60
	}
	if m := dateOnly.FindStringSubmatch(s); m != nil {
		return calendarDate(m[1], m[2], m[3])
	}
	return false
}

func calendarDate(ys, ms, ds string) bool {
	y, _ := strconv.Atoi(ys)
	m, _ := strconv.Atoi(ms)
	d, _ := strconv.Atoi(ds)
	if m < 1 || m > 12 || d < 1 {
		return false
	}
	last := daysInMonths[m-1]
	if m == 2 && (y%4 == 0 && y%100 != 0 || y%400 == 0) {
		last = 29
	}
	return d <= last
}

// DateClassName spells a date class for a diagnostic.
func DateClassName(class string) string {
	switch strings.ToLower(class) {
	case "datetime":
		return "DateTime"
	case "datetimeimmutable":
		return "DateTimeImmutable"
	case "datetimezone":
		return "DateTimeZone"
	case "dateinterval":
		return "DateInterval"
	}
	return ""
}
