package php

import (
	"math"
	"strconv"
	"strings"
)

// printf and its family, moved from the interpreter so compiled code has them
// too. 191 corpus tests use one of them, and it is the largest single builtin
// after var_dump by the greedy file cover -- 175 more files.
//
// The move was cheap for a reason worth naming: this file was already free of
// the interpreter. It needed exactly two things from it -- `Raise`, which names
// an error class, and a type name for one TypeError message -- and both were
// really `runtime/php` concerns wearing interpreter clothes. `Raise` moved down
// with the rest; see php.Error in throwable.go.
//
// One behaviour changed, in the direction of the engine. The interpreter used
// its own type name, which answers "object" for an object; the engine names the
// class:
//
//	vsprintf("%d", new stdClass)
//	TypeError: vsprintf(): Argument #2 ($values) must be of type array, stdClass given
//
// `php.TypeName` answers `stdClass`, so the moved code is right where the
// original was wrong. Checked against the engine rather than assumed, and called
// out because a move is supposed to preserve behaviour and this one does not.

func init() {
	RegisterBuiltin("sprintf", func(ctx *Ctx, args []Value) (Value, error) {
		text, err := formatArgs(ctx, args)
		if err != nil {
			return Undef(), err
		}
		return String(text), nil
	})
	RegisterBuiltin("printf", func(ctx *Ctx, args []Value) (Value, error) {
		text, err := formatArgs(ctx, args)
		if err != nil {
			return Undef(), err
		}
		ctx.Write(text)
		return Int(int64(len(text))), nil
	})
	RegisterBuiltin("vsprintf", func(ctx *Ctx, args []Value) (Value, error) {
		text, err := formatList(ctx, args)
		if err != nil {
			return Undef(), err
		}
		return String(text), nil
	})
	RegisterBuiltin("vprintf", func(ctx *Ctx, args []Value) (Value, error) {
		text, err := formatList(ctx, args)
		if err != nil {
			return Undef(), err
		}
		ctx.Write(text)
		return Int(int64(len(text))), nil
	})
	RegisterBuiltin("php2go\\internal\\formatted_print", func(ctx *Ctx, args []Value) (Value, error) {
		if len(args) < 4 || args[1].Deref().Kind() != KindArray {
			return Undef(), Raise("Error", "invalid php2go internal formatted_print call")
		}
		var values []Value
		args[1].Deref().Array().Each(func(_ Key, value Value) bool {
			values = append(values, value.Deref())
			return true
		})
		text, err := formattedPrint(ctx,
			args[0].Deref().String(),
			values,
			int(args[2].Deref().Int()),
			args[3].Deref().Bool(),
		)
		if err != nil {
			return Undef(), err
		}
		return String(text), nil
	})
}

func formatArgs(ctx *Ctx, args []Value) (string, error) {
	if len(args) == 0 {
		return "", Raise("ArgumentCountError",
			"sprintf() expects at least 1 argument, 0 given")
	}
	return formattedPrint(ctx, args[0].Deref().String(), args[1:], 1, false)
}

// formatList is the v- prefixed form, which takes the arguments as one array.
func formatList(ctx *Ctx, args []Value) (string, error) {
	if len(args) < 2 {
		return "", Raise("ArgumentCountError",
			"vsprintf() expects exactly 2 arguments, %d given", len(args))
	}
	list := args[1].Deref()
	if list.Kind() != KindArray {
		return "", Raise("TypeError",
			"vsprintf(): Argument #2 ($values) must be of type array, %s given",
			TypeName(list))
	}
	var values []Value
	list.Array().Each(func(_ Key, v Value) bool {
		values = append(values, v.Deref())
		return true
	})
	return formattedPrint(ctx, args[0].Deref().String(), values, 0, true)
}

// directive is one %-specification, parsed out of the format string.
type directive struct {
	argnum    int // 1-based; 0 means "take the next one"
	leftAlign bool
	plusSign  bool
	pad       byte
	width     int
	precision int // -1 when the format did not give one
	verb      byte
}

func formattedPrint(ctx *Ctx, format string, args []Value, argumentOffset int, arrayForm bool) (string, error) {
	var out strings.Builder
	next := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			out.WriteByte(format[i])
			continue
		}
		i++
		if i >= len(format) {
			return "", Raise("ValueError", "Missing format specifier at end of string")
		}
		if format[i] == '%' {
			out.WriteByte('%')
			continue
		}
		spec, end, err := parseDirective(format, i)
		if err != nil {
			return "", err
		}
		i = end

		index := next
		if spec.argnum > 0 {
			index = spec.argnum - 1
		} else {
			next++
		}
		if index < 0 || index >= len(args) {
			required, given := index+1, len(args)
			if arrayForm {
				item := "items"
				if required == 1 {
					item = "item"
				}
				return "", Raise("ValueError",
					"The arguments array must contain %d %s, %d given",
					required, item, given)
			}
			return "", Raise("ArgumentCountError",
				"%d arguments are required, %d given",
				required+argumentOffset, given+argumentOffset)
		}
		rendered, err := spec.render(ctx, args[index])
		if err != nil {
			return "", err
		}
		out.WriteString(rendered)
	}
	return out.String(), nil
}

// parseDirective reads one specification starting just after the '%'. It
// returns the index of the verb, since the caller's loop advances from there.
func parseDirective(format string, i int) (directive, int, error) {
	spec := directive{pad: ' ', precision: -1}

	// A run of digits followed by '$' is a positional argument. It has to be
	// read speculatively, since the same digits are otherwise a width.
	if start := i; isDigit(format[i]) {
		for i < len(format) && isDigit(format[i]) {
			i++
		}
		if i < len(format) && format[i] == '$' {
			spec.argnum, _ = strconv.Atoi(format[start:i])
			i++
		} else {
			i = start
		}
	}

	for i < len(format) {
		switch format[i] {
		case '-':
			spec.leftAlign = true
		case '+':
			spec.plusSign = true
		case ' ':
			spec.pad = ' '
		case '0':
			spec.pad = '0'
		case '\'':
			// The padding character is whatever follows the quote.
			i++
			if i >= len(format) {
				return spec, i, Raise("ValueError", "Missing padding character")
			}
			spec.pad = format[i]
		default:
			goto width
		}
		i++
	}

width:
	for i < len(format) && isDigit(format[i]) {
		spec.width = spec.width*10 + int(format[i]-'0')
		i++
	}
	if i < len(format) && format[i] == '.' {
		i++
		spec.precision = 0
		for i < len(format) && isDigit(format[i]) {
			spec.precision = spec.precision*10 + int(format[i]-'0')
			i++
		}
	}
	if i >= len(format) {
		return spec, i, Raise("ValueError", "Missing format specifier at end of string")
	}
	spec.verb = format[i]
	return spec, i, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// render turns one argument into the text the directive asks for, then pads it.
func (d directive) render(ctx *Ctx, arg Value) (string, error) {
	var body string
	switch d.verb {
	case 's':
		body = arg.String()
		// An object is converted as PHP converts one to a string: its
		// `__toString`, an exception's own rendering, or the Error for one
		// with neither.
		if o := arg.Deref(); o.Kind() == KindObject && ctx != nil {
			converted, err := ToString(ctx, o)
			if err != nil {
				return "", err
			}
			body = converted.String()
		}
		if d.precision >= 0 && d.precision < len(body) {
			body = body[:d.precision]
		}
	case 'd', 'i':
		body = strconv.FormatInt(arg.Int(), 10)
		if d.plusSign && arg.Int() >= 0 {
			body = "+" + body
		}
	case 'u':
		body = strconv.FormatUint(uint64(arg.Int()), 10)
	case 'b':
		body = strconv.FormatUint(uint64(arg.Int()), 2)
	case 'o':
		body = strconv.FormatUint(uint64(arg.Int()), 8)
	case 'x':
		body = strconv.FormatUint(uint64(arg.Int()), 16)
	case 'X':
		body = strings.ToUpper(strconv.FormatUint(uint64(arg.Int()), 16))
	case 'c':
		body = string([]byte{byte(arg.Int())})
	case 'f', 'F':
		body = formatFloatDirective(arg.Float(), 'f', d.decimals(), d.plusSign)
	case 'e', 'E':
		body = formatExponent(arg.Float(), d.verb, d.decimals(), d.plusSign)
	case 'g', 'G':
		body = formatGeneral(arg.Float(), d.verb, d.precision, d.plusSign)
	default:
		return "", Raise("ValueError", "Unknown format specifier \"%s\"", string(d.verb))
	}
	return d.padTo(body), nil
}

// decimals is the digit count after the point, which defaults to six the way C
// and PHP both have it.
func (d directive) decimals() int {
	if d.precision < 0 {
		return 6
	}
	return d.precision
}

func (d directive) padTo(body string) string {
	if len(body) >= d.width {
		return body
	}
	padding := strings.Repeat(string(d.pad), d.width-len(body))
	if d.leftAlign {
		// A custom padding character is used on the right too, but a '0' from
		// the zero flag is not -- trailing zeros would change the number.
		if d.pad == '0' {
			return body + strings.Repeat(" ", d.width-len(body))
		}
		return body + padding
	}
	// Zero padding goes after the sign rather than before it.
	if d.pad == '0' && len(body) > 0 && (body[0] == '-' || body[0] == '+') {
		return body[:1] + padding + body[1:]
	}
	return padding + body
}

func formatFloatDirective(value float64, verb byte, decimals int, plus bool) string {
	if body, exceptional := exceptionalFloat(value); exceptional {
		return body
	}
	body := strconv.FormatFloat(value, verb, decimals, 64)
	if plus && value >= 0 {
		body = "+" + body
	}
	return body
}

// exceptionalFloat spells the three floats that have no digits, which PHP
// prints in capitals from every conversion.
func exceptionalFloat(value float64) (string, bool) {
	switch {
	case math.IsInf(value, 1):
		return "INF", true
	case math.IsInf(value, -1):
		return "-INF", true
	case math.IsNaN(value):
		// "NaN" here but "NAN" from a string cast: printf and the type
		// conversions spell it differently, and both spellings are PHP's own.
		return "NaN", true
	}
	return "", false
}

// formatExponent writes %e. PHP prints the exponent with as few digits as it
// needs -- 1.0e+1, not Go's 1.0e+01 -- so the exponent is rebuilt.
func formatExponent(value float64, verb byte, decimals int, plus bool) string {
	if body, exceptional := exceptionalFloat(value); exceptional {
		return body
	}
	body := strconv.FormatFloat(value, verb|0x20, decimals, 64)
	if at := strings.IndexByte(body, 'e'); at >= 0 {
		mantissa, exponent := body[:at], body[at+1:]
		sign := "+"
		if exponent[0] == '+' || exponent[0] == '-' {
			sign, exponent = string(exponent[0]), exponent[1:]
		}
		exponent = strings.TrimLeft(exponent, "0")
		if exponent == "" {
			exponent = "0"
		}
		body = mantissa + "e" + sign + exponent
	}
	if verb == 'E' {
		body = strings.ToUpper(body)
	}
	if plus && value >= 0 {
		body = "+" + body
	}
	return body
}

// trimTrailingZeros drops the digits %g does not print. It is the one place
// the significant-digit forms differ from the fixed ones, which always pad.
func trimTrailingZeros(body string) string {
	if !strings.ContainsRune(body, '.') {
		return body
	}
	return strings.TrimSuffix(strings.TrimRight(body, "0"), ".")
}

// formatGeneral writes %g, which is %e or %f depending on the exponent.
func formatGeneral(value float64, verb byte, precision int, plus bool) string {
	if precision < 0 {
		precision = 6
	}
	if precision == 0 {
		precision = 1
	}
	exponent := 0
	if value != 0 && !math.IsInf(value, 0) && !math.IsNaN(value) {
		exponent = int(math.Floor(math.Log10(math.Abs(value))))
	}
	var body string
	if exponent < -4 || exponent >= precision {
		body = formatExponent(value, 'e', precision-1, false)
		if at := strings.IndexByte(body, 'e'); at >= 0 {
			mantissa := trimTrailingZeros(body[:at])
			// The exponential form always keeps a fractional digit: PHP prints
			// 1.0e+20, not 1e+20, even though it drops the zeros beyond it.
			if !strings.ContainsRune(mantissa, '.') {
				mantissa += ".0"
			}
			body = mantissa + body[at:]
		}
	} else {
		body = trimTrailingZeros(strconv.FormatFloat(value, 'f', precision-1-exponent, 64))
	}
	if verb == 'G' {
		body = strings.ToUpper(body)
	}
	if plus && value >= 0 {
		body = "+" + body
	}
	return body
}
