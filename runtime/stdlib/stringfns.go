package stdlib

import (
	"fmt"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The byte-string library, moved out of the interpreter so both back-ends reach
// one implementation.
//
// It is here because of a measurement. Of the 1,495 corpus files the compiler
// reports `unsupported`, **661 are blocked on a library function the emitter has
// no route to** -- the single largest population left, ahead of every semantic
// gap. `runtime/php` registered 28 functions against the interpreter's ~192, so
// the compiler's standard library was the gap, not any one behaviour.
//
// These were chosen the way `stdlib.go`'s batch was: they are `func(ctx, args)`
// over values, they call nothing that knows what a class is, and none of them
// is slated to become userland PHP. The difference from that batch is that five
// helpers here are used by interpreter files that stay behind, so rather than
// copy them across the boundary they are **exported from this package** and the
// interpreter calls them. One definition, two callers -- which is the direction
// the extraction is going anyway.
//
// Dispatch is preserved by the bridge in `runtime/interp/builtins.go`: a name
// registered here and absent from the interpreter's own table is put back into
// that table, so `verifyBuiltinArgs` still runs ahead of it. That ordering is
// part of the behaviour, not a detail -- see the `printf` entry in the journal.

func init() {
	php.RegisterBuiltin("strtoupper", MapStringBuiltin(ToUpperASCII))
	php.RegisterBuiltin("strtolower", MapStringBuiltin(ToLowerASCII))
	php.RegisterBuiltin("trim", trimBuiltin(strings.Trim))
	php.RegisterBuiltin("ltrim", trimBuiltin(strings.TrimLeft))
	php.RegisterBuiltin("rtrim", trimBuiltin(strings.TrimRight))
	php.RegisterBuiltin("chop", trimBuiltin(strings.TrimRight))

	// strcmp goes through memcmp, whose non-zero return value the C standard
	// leaves to the implementation -- glibc gives the byte difference for some
	// lengths and +/-1 for others, so only the sign is worth reproducing.
	// strcasecmp is PHP's own loop and is exact.
	php.RegisterBuiltin("strcmp", stringCompareBuiltin(func(s string) string { return s }))
	php.RegisterBuiltin("strcasecmp", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		a, b := TwoStringArgs(args)
		return php.Int(int64(FoldedDifference(a, b, len(a), len(b)))), nil
	})

	php.RegisterBuiltin("strpos", positionBuiltin(false))
	php.RegisterBuiltin("strrpos", positionBuiltin(true))
	php.RegisterBuiltin("str_contains", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		haystack, needle := TwoStringArgs(args)
		return php.Bool(strings.Contains(haystack, needle)), nil
	})
	php.RegisterBuiltin("str_starts_with", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		haystack, needle := TwoStringArgs(args)
		return php.Bool(strings.HasPrefix(haystack, needle)), nil
	})
	php.RegisterBuiltin("str_ends_with", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		haystack, needle := TwoStringArgs(args)
		return php.Bool(strings.HasSuffix(haystack, needle)), nil
	})
	php.RegisterBuiltin("substr", substrBuiltin)
	php.RegisterBuiltin("str_replace", replaceBuiltin)
	php.RegisterBuiltin("str_repeat", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 2 {
			return php.Undef(), fmt.Errorf("str_repeat() expects 2 arguments")
		}
		times := args[1].Deref().Int()
		if times < 0 {
			return php.Undef(), php.Raise("ValueError",
				"str_repeat(): Argument #2 ($times) must be greater than or equal to 0")
		}
		if err := Affordable(int64(len(args[0].Deref().String())) * times); err != nil {
			return php.Undef(), err
		}
		return php.String(strings.Repeat(args[0].Deref().String(), int(times))), nil
	})
}

// AllocationCeiling is where php2go stops trying. PHP has memory_limit and
// reports an allocation past it as a fatal error; php2go models neither, so an
// allocation this large is reported as a **gap** rather than as a PHP error --
// the distinction QUALITY_GATE.md requires.
const AllocationCeiling = 1 << 32

// Affordable refuses an allocation php2go cannot model, by size.
func Affordable(size int64) error {
	if size < 0 || size > AllocationCeiling {
		return php.Unsupported("allocation of %d bytes, which needs the memory limit php2go does not model", size)
	}
	return nil
}

// The case functions are ASCII-only, which is PHP's behaviour: a PHP string is
// bytes, and strtoupper does not know about encodings.

// ToUpperASCII folds one byte string the way PHP's case functions do.
func ToUpperASCII(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'a' && c <= 'z' {
			out[i] = c - 32
		}
	}
	return string(out)
}

// ToLowerASCII is ToUpperASCII's other half.
func ToLowerASCII(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'A' && c <= 'Z' {
			out[i] = c + 32
		}
	}
	return string(out)
}

// MapStringBuiltin builds the library functions that are one string in, one
// string out, and answer "" when called with nothing.
func MapStringBuiltin(transform func(string) string) php.Builtin {
	return func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.String(""), nil
		}
		return php.String(transform(args[0].Deref().String())), nil
	}
}

// phpTrimDefault is the character list trim() removes when none is given.
const phpTrimDefault = " \t\n\r\x00\x0B"

func trimBuiltin(trim func(string, string) string) php.Builtin {
	return func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.String(""), nil
		}
		cutset := phpTrimDefault
		if len(args) > 1 {
			cutset = args[1].Deref().String()
		}
		return php.String(trim(args[0].Deref().String(), cutset)), nil
	}
}

// compareStrings builds strcmp and strcasecmp. Since PHP 8 both return the sign
// of the comparison rather than a byte difference.
func stringCompareBuiltin(normalize func(string) string) php.Builtin {
	return func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		a, b := TwoStringArgs(args)
		return php.Int(int64(strings.Compare(normalize(a), normalize(b)))), nil
	}
}

// FoldedDifference is zend_binary_strcasecmp: the difference between the first
// pair of folded bytes that disagree, or the sign of the length difference when
// one string is a prefix of the other. The *magnitude* is observable -- PHP
// returns it unchanged -- so this is not a three-way comparison.
func FoldedDifference(a, b string, lenA, lenB int) int {
	shortest := lenA
	if lenB < shortest {
		shortest = lenB
	}
	for i := 0; i < shortest; i++ {
		c1, c2 := lowerByte(a[i]), lowerByte(b[i])
		if c1 != c2 {
			return int(c1) - int(c2)
		}
	}
	return signOf(lenA - lenB)
}

// lowerByte folds one ASCII byte, which is the only folding PHP's byte-string
// comparisons do.
func lowerByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

func signOf(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// TwoStringArgs reads the first two arguments as strings, treating a missing one
// as "". A byte-string function with a required parameter still arrives here
// short when the caller is inside a diagnostic path.
func TwoStringArgs(args []php.Value) (string, string) {
	var a, b string
	if len(args) > 0 {
		a = args[0].Deref().String()
	}
	if len(args) > 1 {
		b = args[1].Deref().String()
	}
	return a, b
}

// positionBuiltin builds strpos and strrpos, which answer false rather than -1
// when the needle is absent -- the distinction programs test with ===.
func positionBuiltin(last bool) php.Builtin {
	return func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		haystack, needle := TwoStringArgs(args)
		offset := 0
		if len(args) > 2 {
			offset = int(args[2].Deref().Int())
		}
		if offset < 0 {
			offset += len(haystack)
		}
		if offset < 0 || offset > len(haystack) {
			return php.Undef(), php.Raise("ValueError",
				"strpos(): Argument #3 ($offset) must be contained in argument #1 ($haystack)")
		}
		at := strings.Index(haystack[offset:], needle)
		if last {
			at = strings.LastIndex(haystack[offset:], needle)
		}
		if at < 0 {
			return php.Bool(false), nil
		}
		return php.Int(int64(at + offset)), nil
	}
}

// substrBuiltin implements PHP's offset rules: a negative start counts from the
// end, a negative length stops that many bytes before it.
func substrBuiltin(_ *php.Ctx, args []php.Value) (php.Value, error) {
	if len(args) < 2 {
		return php.String(""), nil
	}
	s := args[0].Deref().String()
	start := int(args[1].Deref().Int())
	if start < 0 {
		start += len(s)
		if start < 0 {
			start = 0
		}
	}
	if start > len(s) {
		return php.String(""), nil
	}
	end := len(s)
	if len(args) > 2 && args[2].Deref().Kind() != php.KindNull {
		length := int(args[2].Deref().Int())
		if length < 0 {
			end = len(s) + length
		} else {
			end = start + length
		}
	}
	if end > len(s) {
		end = len(s)
	}
	if end < start {
		return php.String(""), nil
	}
	return php.String(s[start:end]), nil
}

// replaceBuiltin implements str_replace. Either the search or the replacement
// may be an array, and the subject may be too, in which case each element is
// replaced and an array comes back.
func replaceBuiltin(_ *php.Ctx, args []php.Value) (php.Value, error) {
	if len(args) < 3 {
		return php.Null(), nil
	}
	search, replace, subject := args[0].Deref(), args[1].Deref(), args[2].Deref()

	replaceOne := func(s string) string {
		if search.Kind() != php.KindArray {
			return strings.ReplaceAll(s, search.String(), replace.String())
		}
		replacements := map[int]string{}
		if replace.Kind() == php.KindArray {
			index := 0
			replace.Array().Each(func(_ php.Key, v php.Value) bool {
				replacements[index] = v.Deref().String()
				index++
				return true
			})
		}
		index := 0
		search.Array().Each(func(_ php.Key, v php.Value) bool {
			with := replace.String()
			if replace.Kind() == php.KindArray {
				// A short replacement array pairs the rest with "", which is
				// how PHP drops the extra needles rather than erroring.
				with = replacements[index]
			}
			s = strings.ReplaceAll(s, v.Deref().String(), with)
			index++
			return true
		})
		return s
	}

	if subject.Kind() == php.KindArray {
		result := php.NewArray()
		subject.Array().Each(func(k php.Key, v php.Value) bool {
			result.Set(k, php.String(replaceOne(v.Deref().String())))
			return true
		})
		return php.Arr(result), nil
	}
	return php.String(replaceOne(subject.String())), nil
}
