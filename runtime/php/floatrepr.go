package php

import (
	"strconv"
	"strings"
)

// ReprFloat renders a finite float the way the engine does wherever
// serialize_precision (-1) applies -- var_dump, var_export, json_encode,
// serialize: the shortest digits that round-trip, laid out by zend_gcvt.
//
// zend_gcvt writes **fixed** notation unless the decimal exponent is past the
// precision it was given (17) or below -3, so 1e16 is `10000000000000000` and
// 0.0001 is `0.0001`, while 1e17 is `1.0E+17` and 1e-5 is `1.0E-5`: a lone
// mantissa digit still gets `.0`, and the exponent is not padded. Go's shortest
// 'G' switches to an exponent far sooner (1e15 came out `1E+15`) and pads it
// (`1E-05`), which every one of the four renderers inherited.
//
// expChar is 'E' except for JSON's 'e'. zeroFrac appends `.0` to a fixed
// rendering with no fractional part, as var_export (and JSON under
// JSON_PRESERVE_ZERO_FRACTION) do.
func ReprFloat(f float64, expChar byte, zeroFrac bool) string {
	// Shortest round-trip digits and exponent, from Go's 'e' form.
	e := strconv.FormatFloat(f, 'e', -1, 64)
	neg := false
	if e[0] == '-' {
		neg, e = true, e[1:]
	}
	at := strings.IndexByte(e, 'e')
	mantissa, exp := e[:at], e[at+1:]
	digits := strings.Replace(mantissa, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	decpt := x + 1

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	if (decpt < 0 && decpt < -3) || decpt > 17 {
		b.WriteByte(digits[0])
		b.WriteByte('.')
		if len(digits) > 1 {
			b.WriteString(digits[1:])
		} else {
			b.WriteByte('0')
		}
		b.WriteByte(expChar)
		if x < 0 {
			b.WriteByte('-')
			x = -x
		} else {
			b.WriteByte('+')
		}
		b.WriteString(strconv.Itoa(x))
		return b.String()
	}
	switch {
	case decpt <= 0:
		b.WriteString("0.")
		b.WriteString(strings.Repeat("0", -decpt))
		b.WriteString(digits)
	case decpt >= len(digits):
		b.WriteString(digits)
		b.WriteString(strings.Repeat("0", decpt-len(digits)))
		if zeroFrac {
			b.WriteString(".0")
		}
	default:
		b.WriteString(digits[:decpt])
		b.WriteByte('.')
		b.WriteString(digits[decpt:])
	}
	return b.String()
}
