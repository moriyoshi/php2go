package stdlib

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// `json_encode`. Everything it needs about an object it asks the Ctx for: the
// property table, whether the class implements `JsonSerializable`, the call
// that replaces the value when it does, and the initializer a lazy object owes
// before any of that. So it is a library function like any other rather than an
// interpreter-aware one, and the compiled back-end gets it for free.

func init() {
	php.RegisterBuiltin("json_encode", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.String("null"), nil
		}
		subject, err := ctx.Materialize(php.Arg(args, 0))
		if err != nil {
			return php.Undef(), err
		}
		flags := int64(0)
		if len(args) > 1 {
			flags = args[1].Deref().Int()
		}
		var out strings.Builder
		throw := flags&jsonThrowOnError != 0
		if err := encodeJSON(ctx, &out, subject, flags); err != nil {
			refusal, unsupported := err.(*jsonRefusal)
			if !unsupported {
				return php.Undef(), err
			}
			if throw {
				// Thrown with its code, and json_last_error() left as it was.
				return php.Undef(), php.RaiseCode("JsonException", refusal.code(), "%s", refusal.message())
			}
			// PHP returns false rather than throwing.
			setJSONError(ctx, refusal.code())
			return php.Bool(false), nil
		}
		if !throw {
			setJSONError(ctx, jsonErrorNone)
		}
		return php.String(out.String()), nil
	})
}

// jsonRefusal is a value JSON cannot represent, which json_encode reports by
// returning false rather than by failing.
type jsonRefusal struct{ what string }

// code is the JSON_ERROR_* the refusal is.
func (r *jsonRefusal) code() int64 {
	switch r.what {
	case "inf or nan":
		return jsonErrorInfOrNaN
	case "non-backed enum":
		return jsonErrorNonBackedEnum
	}
	return jsonErrorUnsupported
}

// message is what JSON_THROW_ON_ERROR puts in the JsonException, and what
// json_last_error_msg() answers.
func (r *jsonRefusal) message() string {
	return jsonErrorMessage(r.code())
}

func (r *jsonRefusal) Error() string { return "json_encode: " + r.what }

func encodeJSON(ctx *php.Ctx, out *strings.Builder, value php.Value, flags int64) error {
	value = value.Deref()
	switch value.Kind() {
	case php.KindUndef, php.KindNull:
		out.WriteString("null")
	case php.KindTrue:
		out.WriteString("true")
	case php.KindFalse:
		out.WriteString("false")
	case php.KindInt:
		out.WriteString(strconv.FormatInt(value.Int(), 10))
	case php.KindFloat:
		number := value.Float()
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return &jsonRefusal{what: "inf or nan"}
		}
		out.WriteString(jsonFloat(number, flags))
	case php.KindString:
		out.WriteString(jsonString(value.String()))
	case php.KindArray:
		return encodeJSONArray(ctx, out, value.Array(), flags)
	case php.KindObject:
		return encodeJSONObject(ctx, out, value, flags)
	default:
		return &jsonRefusal{what: "unsupported type"}
	}
	return nil
}

// encodeJSONArray writes a list when the keys are exactly 0..n-1 and an object
// otherwise, which is the rule that makes PHP's arrays round-trip.
func encodeJSONArray(ctx *php.Ctx, out *strings.Builder, array *php.Array, flags int64) error {
	list := true
	next := int64(0)
	array.Each(func(k php.Key, _ php.Value) bool {
		if !k.IsInt || k.Int != next {
			list = false
			return false
		}
		next++
		return true
	})

	open, close := "{", "}"
	if list {
		open, close = "[", "]"
	}
	out.WriteString(open)
	first := true
	var failed error
	array.Each(func(k php.Key, v php.Value) bool {
		if !first {
			out.WriteString(",")
		}
		first = false
		if !list {
			out.WriteString(jsonString(k.String()) + ":")
		}
		if err := encodeJSON(ctx, out, v, flags); err != nil {
			failed = err
			return false
		}
		return true
	})
	if failed != nil {
		return failed
	}
	out.WriteString(close)
	return nil
}

func encodeJSONObject(ctx *php.Ctx, out *strings.Builder, value php.Value, flags int64) error {
	object := value.Object()
	if object == nil {
		out.WriteString("null")
		return nil
	}
	// JsonSerializable replaces the object with whatever it returns.
	if ctx.InstanceOfClass(value, "JsonSerializable") && ctx.CanCall() {
		callee := php.NewArray()
		callee.Append(value)
		callee.Append(php.String("jsonSerialize"))
		replaced, err := ctx.Call(php.Arr(callee), nil)
		if err != nil {
			return err
		}
		return encodeJSON(ctx, out, replaced, flags)
	}
	// An enum is its backing value, and a pure one has none to write.
	if object.EnumCase != "" {
		backing, ok := object.Get("value")
		if !ok {
			return &jsonRefusal{what: "non-backed enum"}
		}
		return encodeJSON(ctx, out, backing, flags)
	}
	// A date object writes its date, zone type and zone, which are not held.
	if php.HidesDateState(object) {
		return php.DateGap(object, "json_encode")
	}
	// The public properties as the object presents them, which for a hooked one
	// means what its get hook returns -- a virtual property has no slot to walk
	// and would otherwise be left out. The **empty** scope, because the
	// engine's encoder sees exactly what global scope sees; the mangling check
	// below is the second half of that, for a back-end whose table carries the
	// non-public names rather than dropping them.
	visible, err := ctx.VisibleProperties(object, "")
	if err != nil {
		return err
	}
	out.WriteString("{")
	first := true
	var failed error
	visible.Each(func(k php.Key, v php.Value) bool {
		if v.Deref().Kind() == php.KindUndef {
			return true
		}
		if _, _, mangled := php.UnmangleProperty(k.String()); mangled {
			// A key carrying the visibility mangling is not public, whatever
			// the class table says about the name inside it.
			return true
		}
		if !first {
			out.WriteString(",")
		}
		first = false
		out.WriteString(jsonString(k.String()) + ":")
		if err := encodeJSON(ctx, out, v, flags); err != nil {
			failed = err
			return false
		}
		return true
	})
	if failed != nil {
		return failed
	}
	out.WriteString("}")
	return nil
}

// jsonString quotes a string the way PHP's encoder does by default: slashes are
// escaped, and anything outside ASCII becomes a \u escape.
func jsonString(s string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '/':
			out.WriteString(`\/`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		default:
			switch {
			case r < 0x20:
				fmt.Fprintf(&out, `\u%04x`, r)
			case r < 0x80:
				out.WriteRune(r)
			case r > 0xFFFF:
				// Outside the basic plane, PHP writes a surrogate pair.
				r -= 0x10000
				fmt.Fprintf(&out, `\u%04x\u%04x`, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
			default:
				fmt.Fprintf(&out, `\u%04x`, r)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}

// json_encode's flags. Only the two the corpus uses are modelled; the rest are
// accepted and ignored rather than refused, which is what they were before
// there was a flags argument at all.
const (
	jsonPreserveZeroFraction = 1024
	jsonThrowOnError         = 4194304
)

// jsonFloat writes a float the way PHP's encoder does: the shortest form that
// round-trips, with no exponent for values an integer literal would cover.
func jsonFloat(number float64, flags int64) string {
	// A whole number loses its fractional part -- json_encode(1.0) is "1" --
	// unless JSON_PRESERVE_ZERO_FRACTION asks for it back.
	return php.ReprFloat(number, 'e', flags&jsonPreserveZeroFraction != 0)
}
