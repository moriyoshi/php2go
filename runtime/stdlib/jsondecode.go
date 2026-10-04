package stdlib

import (
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/moriyoshi/php2go/runtime/php"
)

// `json_decode`, `json_last_error` and `json_last_error_msg`.
//
// The decoder follows ext/json's scanner and parser rather than a generic JSON
// library, because the answers differ where PHP is particular (all probed):
//
//   - an error is the **first** one met in token order -- a control character
//     (3), malformed UTF-8 in a string (5), a lone UTF-16 surrogate escape
//     (10), a property name starting with NUL in object mode (9), `[1}` or
//     `{"a":1]` (2, state mismatch), nesting at or beyond `$depth` (1), and
//     anything else a syntax error (4);
//   - an integer beyond int64 is a float, or its digits under
//     JSON_BIGINT_AS_STRING; `-0` is int(0), `-0.0` float(-0);
//   - an object is made when its **first member is reduced** -- after that
//     member's value -- or at `}` when empty, which is the order the handles
//     are taken in: `{"b":{"c":2},"a":1}` numbers the inner object first;
//   - array mode keys go through the array's own key coercion ("0" is int
//     0, "07" stays a string), object mode keys stay strings;
//   - a later duplicate key replaces the earlier value in place.

// JSON error codes, as json_last_error() reports them.
const (
	jsonErrorNone          = 0
	jsonErrorDepth         = 1
	jsonErrorStateMismatch = 2
	jsonErrorCtrlChar      = 3
	jsonErrorSyntax        = 4
	jsonErrorUTF8          = 5
	jsonErrorInfOrNaN      = 7
	jsonErrorUnsupported   = 8
	jsonErrorPropertyName  = 9
	jsonErrorUTF16         = 10
	jsonErrorNonBackedEnum = 11
)

const (
	jsonObjectAsArray        = 1
	jsonBigintAsString       = 2
	jsonInvalidUTF8Ignore    = 0x100000
	jsonInvalidUTF8Substitue = 0x200000
)

func jsonErrorMessage(code int64) string {
	switch code {
	case jsonErrorNone:
		return "No error"
	case jsonErrorDepth:
		return "Maximum stack depth exceeded"
	case jsonErrorStateMismatch:
		return "State mismatch (invalid or malformed JSON)"
	case jsonErrorCtrlChar:
		return "Control character error, possibly incorrectly encoded"
	case jsonErrorSyntax:
		return "Syntax error"
	case jsonErrorUTF8:
		return "Malformed UTF-8 characters, possibly incorrectly encoded"
	case jsonErrorInfOrNaN:
		return "Inf and NaN cannot be JSON encoded"
	case jsonErrorUnsupported:
		return "Type is not supported"
	case jsonErrorPropertyName:
		return "The decoded property name is invalid"
	case jsonErrorUTF16:
		return "Single unpaired UTF-16 surrogate in unicode escape"
	case jsonErrorNonBackedEnum:
		return "Non-backed enums have no default serialization"
	}
	return "Unknown error"
}

// jsonLastError is json_last_error()'s state, per request. Kept beside the
// Ctx rather than in it: nothing else reads it.
var jsonLastError sync.Map // *php.Ctx -> int64

func setJSONError(ctx *php.Ctx, code int64) {
	if code == jsonErrorNone {
		jsonLastError.Delete(ctx)
		return
	}
	jsonLastError.Store(ctx, code)
}

func lastJSONError(ctx *php.Ctx) int64 {
	if v, ok := jsonLastError.Load(ctx); ok {
		return v.(int64)
	}
	return jsonErrorNone
}

func init() {
	php.RegisterBuiltin("json_last_error", func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Int(lastJSONError(ctx)), nil
	})
	php.RegisterBuiltin("json_last_error_msg", func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.String(jsonErrorMessage(lastJSONError(ctx))), nil
	})
	php.RegisterBuiltin("json_decode", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		// An optional argument is present when the list reaches it and a
		// named call did not skip it.
		given := func(n int) (php.Value, bool) {
			if n < len(args) && !args[n].IsUndef() {
				return args[n].Deref(), true
			}
			return php.Value{}, false
		}
		text := php.Arg(args, 0).Deref().String()
		flags := int64(0)
		if f, ok := given(3); ok {
			flags = f.Int()
		}
		assoc := flags&jsonObjectAsArray != 0
		if a, ok := given(1); ok && a.Kind() != php.KindNull {
			assoc = a.Bool()
		}
		depth := int64(512)
		if d, ok := given(2); ok {
			depth = d.Int()
		}
		if depth <= 0 {
			return php.Undef(), php.Raise("ValueError", "json_decode(): Argument #3 ($depth) must be greater than 0")
		}
		if depth > math.MaxInt32 {
			return php.Undef(), php.Raise("ValueError", "json_decode(): Argument #3 ($depth) must be less than %d", math.MaxInt32)
		}
		throw := flags&jsonThrowOnError != 0
		if !throw {
			setJSONError(ctx, jsonErrorNone)
		}
		// An empty string is a syntax error without a scan.
		d := &jsonDecoder{ctx: ctx, src: text, assoc: assoc, maxDepth: depth, flags: flags}
		value, code := d.decode()
		if code != jsonErrorNone {
			if throw {
				return php.Undef(), php.RaiseCode("JsonException", code, "%s", jsonErrorMessage(code))
			}
			setJSONError(ctx, code)
			return php.Null(), nil
		}
		return value, nil
	})
}

type jsonDecoder struct {
	ctx      *php.Ctx
	src      string
	pos      int
	assoc    bool
	maxDepth int64
	depth    int64
	flags    int64
	err      int64
}

// decode parses one value and the end of input.
func (d *jsonDecoder) decode() (php.Value, int64) {
	d.skipSpace()
	v := d.value()
	if d.err != jsonErrorNone {
		return php.Null(), d.err
	}
	d.skipSpace()
	if d.pos < len(d.src) {
		d.fail(d.tokenError())
		return php.Null(), d.err
	}
	return v, jsonErrorNone
}

func (d *jsonDecoder) fail(code int64) {
	if d.err == jsonErrorNone {
		d.err = code
	}
}

func (d *jsonDecoder) skipSpace() {
	for d.pos < len(d.src) {
		switch d.src[d.pos] {
		case ' ', '\t', '\n', '\r':
			d.pos++
		default:
			return
		}
	}
}

// tokenError is the error the scanner reports for a character that starts no
// token: a control character and a byte that is not UTF-8 are errors of their
// own (probed: a bare "\xff" is 5), anything else is syntax.
func (d *jsonDecoder) tokenError() int64 {
	if d.pos < len(d.src) {
		if c := d.src[d.pos]; c < 0x20 {
			return jsonErrorCtrlChar
		} else if c >= 0x80 {
			if r, size := utf8.DecodeRuneInString(d.src[d.pos:]); r == utf8.RuneError && size <= 1 {
				return jsonErrorUTF8
			}
		}
	}
	return jsonErrorSyntax
}

func (d *jsonDecoder) value() php.Value {
	if d.pos >= len(d.src) {
		d.fail(jsonErrorSyntax)
		return php.Null()
	}
	switch c := d.src[d.pos]; {
	case c == '{':
		return d.object()
	case c == '[':
		return d.array()
	case c == '"':
		s, ok := d.str()
		if !ok {
			return php.Null()
		}
		return php.String(s)
	case c == '-' || (c >= '0' && c <= '9'):
		return d.number()
	case strings.HasPrefix(d.src[d.pos:], "true"):
		d.pos += 4
		return php.Bool(true)
	case strings.HasPrefix(d.src[d.pos:], "false"):
		d.pos += 5
		return php.Bool(false)
	case strings.HasPrefix(d.src[d.pos:], "null"):
		d.pos += 4
		return php.Null()
	}
	d.fail(d.tokenError())
	return php.Null()
}

// enter counts a container, failing at or beyond the depth limit before its
// contents are read.
func (d *jsonDecoder) enter() bool {
	d.depth++
	if d.depth >= d.maxDepth {
		d.fail(jsonErrorDepth)
		return false
	}
	return true
}

func (d *jsonDecoder) array() php.Value {
	d.pos++ // [
	if !d.enter() {
		return php.Null()
	}
	defer func() { d.depth-- }()
	out := php.NewArray()
	defer func() {
		if d.err != jsonErrorNone {
			d.release(php.Arr(out))
		}
	}()
	d.skipSpace()
	if d.closes(']', '}') {
		return php.Arr(out)
	}
	for {
		v := d.value()
		if d.err != jsonErrorNone {
			return php.Null()
		}
		out.Append(v)
		d.skipSpace()
		if d.pos < len(d.src) && d.src[d.pos] == ',' {
			d.pos++
			d.skipSpace()
			continue
		}
		if d.closes(']', '}') {
			return php.Arr(out)
		}
		if d.err == jsonErrorNone {
			d.fail(d.tokenError())
		}
		return php.Null()
	}
}

// closes consumes the container's closing bracket, reporting the other
// bracket as the state mismatch the engine's grammar names.
func (d *jsonDecoder) closes(want, other byte) bool {
	if d.pos >= len(d.src) {
		return false
	}
	switch d.src[d.pos] {
	case want:
		d.pos++
		return true
	case other:
		d.fail(jsonErrorStateMismatch)
	}
	return false
}

func (d *jsonDecoder) object() php.Value {
	d.pos++ // {
	if !d.enter() {
		return php.Null()
	}
	defer func() { d.depth-- }()
	var arr *php.Array
	var obj *php.Object
	made := false
	make := func() bool {
		if made {
			return true
		}
		made = true
		if d.assoc {
			arr = php.NewArray()
			return true
		}
		o, ok := d.ctx.NewObjectOf("stdClass")
		if !ok {
			d.fail(jsonErrorSyntax)
			return false
		}
		obj = o
		return true
	}
	result := func() php.Value {
		if !make() {
			return php.Null()
		}
		if d.assoc {
			return php.Arr(arr)
		}
		return php.Obj(obj)
	}
	// **A failed parse frees what it built**, innermost first, and a
	// container's members before the container: the engine's parser
	// destroys its stack that way, and the numbers go back on the free
	// list in that order -- the next objects are numbered from them.
	defer func() {
		if d.err != jsonErrorNone && obj != nil {
			d.release(php.Obj(obj))
		}
	}()
	d.skipSpace()
	if d.closes('}', ']') {
		return result()
	}
	for {
		if d.pos >= len(d.src) || d.src[d.pos] != '"' {
			d.fail(d.tokenError())
			return php.Null()
		}
		key, ok := d.str()
		if !ok {
			return php.Null()
		}
		d.skipSpace()
		if d.pos >= len(d.src) || d.src[d.pos] != ':' {
			d.fail(d.tokenError())
			return php.Null()
		}
		d.pos++
		d.skipSpace()
		v := d.value()
		if d.err != jsonErrorNone {
			return php.Null()
		}
		if !make() {
			return php.Null()
		}
		if d.assoc {
			arr.Set(php.StringKey(key), v)
		} else {
			if key != "" && key[0] == 0 {
				d.fail(jsonErrorPropertyName)
				return php.Null()
			}
			obj.Set(key, v)
		}
		d.skipSpace()
		if d.pos < len(d.src) && d.src[d.pos] == ',' {
			d.pos++
			d.skipSpace()
			continue
		}
		if d.closes('}', ']') {
			return result()
		}
		if d.err == jsonErrorNone {
			d.fail(d.tokenError())
		}
		return php.Null()
	}
}

// release hands back the object numbers a value built by this decode holds:
// each member first, in order, then the object itself.
func (d *jsonDecoder) release(v php.Value) {
	switch v.Kind() {
	case php.KindArray:
		v.Array().Each(func(_ php.Key, e php.Value) bool {
			d.release(e.Deref())
			return true
		})
	case php.KindObject:
		o := v.Object()
		o.PropEach(func(_ php.Key, e php.Value) bool {
			d.release(e.Deref())
			return true
		})
		d.ctx.ReleaseHandle(o)
	}
}

func (d *jsonDecoder) number() php.Value {
	start := d.pos
	if d.src[d.pos] == '-' {
		d.pos++
	}
	digits := func() int {
		n := 0
		for d.pos < len(d.src) && d.src[d.pos] >= '0' && d.src[d.pos] <= '9' {
			d.pos++
			n++
		}
		return n
	}
	if d.pos >= len(d.src) {
		d.fail(jsonErrorSyntax)
		return php.Null()
	}
	if d.src[d.pos] == '0' {
		d.pos++
	} else if digits() == 0 {
		// A `-` starts no token without a digit after it.
		d.fail(jsonErrorSyntax)
		return php.Null()
	}
	isFloat := false
	if d.pos < len(d.src) && d.src[d.pos] == '.' {
		d.pos++
		if digits() == 0 {
			d.fail(jsonErrorSyntax)
			return php.Null()
		}
		isFloat = true
	}
	if d.pos < len(d.src) && (d.src[d.pos] == 'e' || d.src[d.pos] == 'E') {
		d.pos++
		if d.pos < len(d.src) && (d.src[d.pos] == '+' || d.src[d.pos] == '-') {
			d.pos++
		}
		if digits() == 0 {
			d.fail(jsonErrorSyntax)
			return php.Null()
		}
		isFloat = true
	}
	text := d.src[start:d.pos]
	if !isFloat {
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			return php.Int(n)
		}
		if d.flags&jsonBigintAsString != 0 {
			return php.String(text)
		}
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil && !isRangeError(err) {
		d.fail(jsonErrorSyntax)
		return php.Null()
	}
	return php.Float(f)
}

func isRangeError(err error) bool {
	ne, ok := err.(*strconv.NumError)
	return ok && ne.Err == strconv.ErrRange
}

// str scans a string token at d.pos, which is its opening quote.
func (d *jsonDecoder) str() (string, bool) {
	d.pos++ // "
	var b strings.Builder
	for {
		if d.pos >= len(d.src) {
			// The scanner reads the terminating NUL as a character of the
			// string: an unterminated string is a control character error.
			d.fail(jsonErrorCtrlChar)
			return "", false
		}
		c := d.src[d.pos]
		switch {
		case c == '"':
			d.pos++
			return b.String(), true
		case c == '\\':
			if !d.escape(&b) {
				return "", false
			}
		case c < 0x20:
			d.fail(jsonErrorCtrlChar)
			return "", false
		case c < 0x80:
			b.WriteByte(c)
			d.pos++
		default:
			r, size := utf8.DecodeRuneInString(d.src[d.pos:])
			if r == utf8.RuneError && size <= 1 {
				switch {
				case d.flags&jsonInvalidUTF8Substitue != 0:
					b.WriteString("�")
				case d.flags&jsonInvalidUTF8Ignore != 0:
				default:
					d.fail(jsonErrorUTF8)
					return "", false
				}
				d.pos++
				continue
			}
			b.WriteString(d.src[d.pos : d.pos+size])
			d.pos += size
		}
	}
}

func (d *jsonDecoder) escape(b *strings.Builder) bool {
	if d.pos+1 >= len(d.src) {
		d.fail(jsonErrorSyntax)
		return false
	}
	c := d.src[d.pos+1]
	d.pos += 2
	switch c {
	case '"', '\\', '/':
		b.WriteByte(c)
	case 'b':
		b.WriteByte('\b')
	case 'f':
		b.WriteByte('\f')
	case 'n':
		b.WriteByte('\n')
	case 'r':
		b.WriteByte('\r')
	case 't':
		b.WriteByte('\t')
	case 'u':
		r, ok := d.hex4()
		if !ok {
			d.fail(jsonErrorSyntax)
			return false
		}
		switch {
		case r >= 0xD800 && r <= 0xDBFF:
			// A high surrogate must be followed by an escaped low one.
			if d.pos+1 < len(d.src) && d.src[d.pos] == '\\' && d.src[d.pos+1] == 'u' {
				save := d.pos
				d.pos += 2
				low, ok := d.hex4()
				if ok && low >= 0xDC00 && low <= 0xDFFF {
					b.WriteRune(0x10000 + (r-0xD800)<<10 + (low - 0xDC00))
					return true
				}
				d.pos = save
			}
			d.fail(jsonErrorUTF16)
			return false
		case r >= 0xDC00 && r <= 0xDFFF:
			d.fail(jsonErrorUTF16)
			return false
		}
		b.WriteRune(r)
	default:
		d.fail(jsonErrorSyntax)
		return false
	}
	return true
}

func (d *jsonDecoder) hex4() (rune, bool) {
	if d.pos+4 > len(d.src) {
		return 0, false
	}
	n, err := strconv.ParseUint(d.src[d.pos:d.pos+4], 16, 32)
	if err != nil {
		return 0, false
	}
	d.pos += 4
	return rune(n), true
}
