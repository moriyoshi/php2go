package stdlib

// The preg_* functions over the matcher in pcre_parse.go / pcre_match.go.
// Their loops -- the empty-match retry, the piece bookkeeping of
// preg_split, the replacement walk -- follow ext/pcre/php_pcre.c step for
// step, because the observable answers (which empty matches count, where a
// skipped character goes) are properties of those loops rather than of the
// regular expression.

import (
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/moriyoshi/php2go/runtime/php"
)

const (
	pregPatternOrder      = 1
	pregSetOrder          = 2
	pregOffsetCapture     = 1 << 8
	pregUnmatchedAsNull   = 1 << 9
	pregSplitNoEmpty      = 1
	pregSplitDelimCapture = 2
	pregSplitOffset       = 4

	pregNoError            = 0
	pregInternalError      = 1
	pregBadUTF8Error       = 4
	pregBadUTF8OffsetError = 5
)

var pregErrorMessages = map[int64]string{
	0: "No error",
	1: "Internal error",
	2: "Backtrack limit exhausted",
	3: "Recursion limit exhausted",
	4: "Malformed UTF-8 characters, possibly incorrectly encoded",
	5: "The offset did not correspond to the beginning of a valid UTF-8 code point",
	6: "JIT stack limit exhausted",
}

// The last error is request state. The runtime has one Ctx per request, so it
// is kept by Ctx here rather than widening Ctx for one extension.
var (
	pregErrorsMu sync.Mutex
	pregErrors   = map[*php.Ctx]int64{}
)

func setPregError(ctx *php.Ctx, code int64) {
	pregErrorsMu.Lock()
	pregErrors[ctx] = code
	pregErrorsMu.Unlock()
}

func pregError(ctx *php.Ctx) int64 {
	pregErrorsMu.Lock()
	defer pregErrorsMu.Unlock()
	return pregErrors[ctx]
}

type pcreModifiers struct {
	caseless, multiline, dotall, extended, ungreedy, anchored, dollarEndOnly, utf bool
}

type pregCacheEntry struct {
	prog    *pcreProgram
	warning string // a PHP warning to raise instead of compiling
	refusal error
}

var (
	pregCacheMu sync.Mutex
	pregCache   = map[string]*pregCacheEntry{}
)

// pregCompile turns a PHP regex (delimiters, body, modifiers) into a program.
// A nil program with a nil error means a warning was raised and the caller
// answers its failure value.
func pregCompile(ctx *php.Ctx, fn, regex string) (*pcreProgram, error) {
	pregCacheMu.Lock()
	entry, cached := pregCache[regex]
	pregCacheMu.Unlock()
	if !cached {
		entry = pregParseRegex(regex)
		pregCacheMu.Lock()
		pregCache[regex] = entry
		pregCacheMu.Unlock()
	}
	if entry.refusal != nil {
		return nil, php.Unsupported("%s", entry.refusal.Error())
	}
	if entry.warning != "" {
		ctx.Warn("%s(): %s", fn, entry.warning)
		setPregError(ctx, pregInternalError)
		return nil, nil
	}
	return entry.prog, nil
}

func isCSpace(c byte) bool {
	return c == ' ' || c >= '\t' && c <= '\r'
}

func isCAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// pregParseRegex is pcre_get_compiled_regex_cache_ex's front half.
func pregParseRegex(regex string) *pregCacheEntry {
	p := 0
	for p < len(regex) && isCSpace(regex[p]) {
		p++
	}
	if p >= len(regex) {
		return &pregCacheEntry{warning: "Empty regular expression"}
	}
	delimiter := regex[p]
	p++
	if isCAlnum(delimiter) || delimiter == '\\' || delimiter == 0 {
		return &pregCacheEntry{warning: "Delimiter must not be alphanumeric, backslash, or NUL byte"}
	}
	start := delimiter
	end := delimiter
	if i := strings.IndexByte("([{< )]}> )]}>", delimiter); i >= 0 {
		end = "([{< )]}> )]}>"[i+5]
	}
	pp := p
	if start == end {
		for pp < len(regex) {
			if regex[pp] == '\\' && pp+1 < len(regex) {
				pp++
			} else if regex[pp] == end {
				break
			}
			pp++
		}
	} else {
		brackets := 1
		for pp < len(regex) {
			if regex[pp] == '\\' && pp+1 < len(regex) {
				pp++
			} else if regex[pp] == end {
				brackets--
				if brackets <= 0 {
					break
				}
			} else if regex[pp] == start {
				brackets++
			}
			pp++
		}
	}
	if pp >= len(regex) {
		if start == end {
			return &pregCacheEntry{warning: "No ending delimiter '" + string(end) + "' found"}
		}
		return &pregCacheEntry{warning: "No ending matching delimiter '" + string(end) + "' found"}
	}
	body := regex[p:pp]
	var mods pcreModifiers
	for _, c := range []byte(regex[pp+1:]) {
		switch c {
		case 'i':
			mods.caseless = true
		case 'm':
			mods.multiline = true
		case 's':
			mods.dotall = true
		case 'x':
			mods.extended = true
		case 'A':
			mods.anchored = true
		case 'D':
			mods.dollarEndOnly = true
		case 'S', 'X', ' ', '\n', '\r':
		case 'U':
			mods.ungreedy = true
		case 'u':
			mods.utf = true
		case 'n', 'J', 'r':
			return &pregCacheEntry{refusal: refuse("preg modifier /%c", c)}
		case 0:
			return &pregCacheEntry{warning: "NUL is not a valid modifier"}
		default:
			return &pregCacheEntry{warning: "Unknown modifier '" + string(c) + "'"}
		}
	}
	prog, err := compilePCRE(body, mods)
	if err != nil {
		return &pregCacheEntry{refusal: err}
	}
	return &pregCacheEntry{prog: prog}
}

// pregExec runs one pcre2_match, translating the budget overflow.
func pregExec(prog *pcreProgram, s string, start int, anchored, notEmpty bool) (*pcreResult, error) {
	res, err := prog.exec(s, start, anchored, notEmpty)
	if err != nil {
		return nil, php.Unsupported("%s", err.Error())
	}
	return res, nil
}

// pregCheckSubject is PCRE's UTF check. It answers the PHP error code, 0 when
// the subject is fine. Invalid bytes only before the start offset are a case
// whose answer depends on how far back PCRE looks, so it is refused.
func pregCheckSubject(prog *pcreProgram, s string, offset int) (int64, error) {
	if !prog.utf {
		return pregNoError, nil
	}
	if utf8.ValidString(s) {
		// Probed: an offset inside a character of a valid subject is the
		// offset error, not the encoding one.
		if offset < len(s) && !utf8.RuneStart(s[offset]) {
			return pregBadUTF8OffsetError, nil
		}
		return pregNoError, nil
	}
	if offset == 0 || (offset < len(s) && utf8.RuneStart(s[offset]) && !utf8.ValidString(s[offset:])) {
		return pregBadUTF8Error, nil
	}
	return 0, php.Unsupported("preg match of an invalid UTF-8 subject from a start offset, whose answer depends on how far back PCRE checks")
}

func argOr(args []php.Value, i int) (php.Value, bool) {
	if i < len(args) {
		return args[i].Deref(), true
	}
	return php.Null(), false
}

// pregMatchValue is one group's value in a result array.
func pregMatchValue(s string, ov []int, g int, offsetCapture, unmatchedAsNull bool) php.Value {
	start, end := ov[2*g], ov[2*g+1]
	var v php.Value
	if start < 0 {
		if unmatchedAsNull {
			v = php.Null()
		} else {
			v = php.String("")
		}
	} else {
		v = php.String(s[start:end])
	}
	if !offsetCapture {
		return v
	}
	pair := php.NewArray()
	pair.Append(v)
	pair.Append(php.Int(int64(start)))
	return php.Arr(pair)
}

// pregSubpatterns is populate_subpat_array: the groups up to PCRE's count,
// names first, and with PREG_UNMATCHED_AS_NULL every group.
func pregSubpatterns(prog *pcreProgram, s string, res *pcreResult, offsetCapture, unmatchedAsNull bool) *php.Array {
	out := php.NewArray()
	count := res.count
	if unmatchedAsNull {
		count = prog.groups + 1
	}
	for g := 0; g < count; g++ {
		v := pregMatchValue(s, res.ovector, g, offsetCapture, unmatchedAsNull)
		if name, ok := prog.names[g]; ok {
			out.Set(php.StringKey(name), v)
		}
		out.Set(php.IntKey(int64(g)), v)
	}
	return out
}

func pregFlagsError(fn string) error {
	return php.Raise("ValueError", "%s(): Argument #4 ($flags) must be a PREG_* constant", fn)
}

// pregStartOffset normalises the offset argument. ok is false for an offset
// past the end, which PCRE refuses as a bad offset.
func pregStartOffset(offset int64, length int) (int, bool) {
	if offset < 0 {
		if -offset > int64(length) {
			return 0, true
		}
		return length + int(offset), true
	}
	if offset > int64(length) {
		return 0, false
	}
	return int(offset), true
}

func pregMatch(ctx *php.Ctx, args []php.Value, global bool) (php.Value, error) {
	fn := "preg_match"
	if global {
		fn = "preg_match_all"
	}
	pattern, _ := argOr(args, 0)
	subjectV, _ := argOr(args, 1)
	subject := subjectV.String()
	var matchesRef *php.Ref
	if len(args) > 2 {
		if r, ok := args[2].RefOf(); ok {
			matchesRef = r
		}
	}
	flags := int64(0)
	if v, ok := argOr(args, 3); ok && v.Kind() != php.KindNull {
		flags = v.Int()
	}
	offsetArg := int64(0)
	if v, ok := argOr(args, 4); ok {
		offsetArg = v.Int()
	}
	prog, err := pregCompile(ctx, fn, pattern.String())
	if err != nil {
		return php.Null(), err
	}
	if prog == nil {
		return php.Bool(false), nil
	}
	offsetCapture := flags&pregOffsetCapture != 0
	unmatchedAsNull := flags&pregUnmatchedAsNull != 0
	order := int64(0)
	if flags&0xff != 0 {
		order = flags & 0xff
	}
	if (global && (order < pregPatternOrder || order > pregSetOrder) && order != 0) || (!global && order != 0) {
		return php.Null(), pregFlagsError(fn)
	}
	if global && order == 0 {
		order = pregPatternOrder
	}
	if matchesRef != nil {
		matchesRef.Set(php.Arr(php.NewArray()))
	}
	setPregError(ctx, pregNoError)
	start, ok := pregStartOffset(offsetArg, len(subject))
	if !ok {
		setPregError(ctx, pregInternalError)
		return php.Bool(false), nil
	}
	if code, err := pregCheckSubject(prog, subject, start); err != nil {
		return php.Null(), err
	} else if code != pregNoError {
		setPregError(ctx, code)
		return php.Bool(false), nil
	}

	if !global {
		res, err := pregExec(prog, subject, start, false, false)
		if err != nil {
			return php.Null(), err
		}
		if res == nil {
			return php.Int(0), nil
		}
		if matchesRef != nil {
			matchesRef.Set(php.Arr(pregSubpatterns(prog, subject, res, offsetCapture, unmatchedAsNull)))
		}
		return php.Int(1), nil
	}

	sets := make([]*php.Array, prog.groups+1)
	for i := range sets {
		sets[i] = php.NewArray()
	}
	setOrder := php.NewArray()
	matched := 0
	offset := start
	notEmpty := false
	for {
		res, err := pregExec(prog, subject, offset, notEmpty, notEmpty)
		if err != nil {
			return php.Null(), err
		}
		if res == nil {
			if notEmpty && offset < len(subject) {
				offset += prog.unitLen(subject, offset)
				notEmpty = false
				continue
			}
			break
		}
		matched++
		if order == pregPatternOrder {
			for g := 0; g < res.count; g++ {
				sets[g].Append(pregMatchValue(subject, res.ovector, g, offsetCapture, unmatchedAsNull))
			}
			for g := res.count; g <= prog.groups; g++ {
				sets[g].Append(pregMatchValue(subject, res.ovector, g, offsetCapture, unmatchedAsNull))
			}
		} else {
			setOrder.Append(php.Arr(pregSubpatterns(prog, subject, res, offsetCapture, unmatchedAsNull)))
		}
		offset = res.ovector[1]
		notEmpty = res.ovector[1] == res.ovector[0]
	}
	if matchesRef != nil {
		if order == pregPatternOrder {
			out := php.NewArray()
			for g, set := range sets {
				if name, ok := prog.names[g]; ok {
					out.Set(php.StringKey(name), php.Arr(set.Clone()))
				}
				out.Set(php.IntKey(int64(g)), php.Arr(set))
			}
			matchesRef.Set(php.Arr(out))
		} else {
			matchesRef.Set(php.Arr(setOrder))
		}
	}
	return php.Int(int64(matched)), nil
}

// pregReplaceOne is php_pcre_replace_impl for one pattern over one subject.
// replacement produces the text for one match. ok is false for an execution
// error, whose code is already recorded.
func pregReplaceOne(ctx *php.Ctx, prog *pcreProgram, subject string, limit int64, replacement func(*pcreResult) (string, error), counted *int64) (string, bool, error) {
	var out strings.Builder
	if code, err := pregCheckSubject(prog, subject, 0); err != nil {
		return "", false, err
	} else if code != pregNoError {
		setPregError(ctx, code)
		return "", false, nil
	}
	lastEnd := 0
	offset := 0
	notEmpty := false
	for {
		var res *pcreResult
		if limit != 0 {
			var err error
			res, err = pregExec(prog, subject, offset, notEmpty, notEmpty)
			if err != nil {
				return "", false, err
			}
		}
		if res == nil {
			if notEmpty && limit != 0 && offset < len(subject) {
				offset += prog.unitLen(subject, offset)
				notEmpty = false
				continue
			}
			out.WriteString(subject[lastEnd:])
			return out.String(), true, nil
		}
		*counted++
		out.WriteString(subject[lastEnd:res.ovector[0]])
		text, err := replacement(res)
		if err != nil {
			return "", false, err
		}
		out.WriteString(text)
		if limit > 0 {
			limit--
		}
		offset = res.ovector[1]
		lastEnd = offset
		notEmpty = res.ovector[1] == res.ovector[0]
	}
}

// pregSubstitute expands a replacement string's references, as the loop in
// php_pcre_replace_impl does.
func pregSubstitute(replace, subject string, res *pcreResult) string {
	var out []byte
	var walkLast byte
	at := func(i int) byte {
		if i < len(replace) {
			return replace[i]
		}
		return 0
	}
	for i := 0; i < len(replace); {
		c := replace[i]
		if c == '\\' || c == '$' {
			if walkLast == '\\' {
				out[len(out)-1] = c
				i++
				walkLast = 0
				continue
			}
			// preg_get_backref
			if at(i+1) != 0 {
				j := i
				inBrace := false
				if c == '$' && at(j+1) == '{' {
					inBrace = true
					j++
				}
				j++
				if d := at(j); d >= '0' && d <= '9' {
					ref := int(d - '0')
					j++
					if d2 := at(j); d2 >= '0' && d2 <= '9' {
						ref = ref*10 + int(d2-'0')
						j++
					}
					good := true
					if inBrace {
						if at(j) != '}' {
							good = false
						} else {
							j++
						}
					}
					if good {
						if ref < res.count && res.ovector[2*ref] >= 0 {
							out = append(out, subject[res.ovector[2*ref]:res.ovector[2*ref+1]]...)
						}
						i = j
						continue
					}
				}
			}
		}
		out = append(out, c)
		i++
		walkLast = c
	}
	return string(out)
}

func pregReplace(ctx *php.Ctx, args []php.Value, callbackMode bool) (php.Value, error) {
	fn := "preg_replace"
	if callbackMode {
		fn = "preg_replace_callback"
	}
	pattern, _ := argOr(args, 0)
	replace, _ := argOr(args, 1)
	subject, _ := argOr(args, 2)
	limit := int64(-1)
	if v, ok := argOr(args, 3); ok && v.Kind() != php.KindNull {
		limit = v.Int()
	}
	var countRef *php.Ref
	if len(args) > 4 {
		if r, ok := args[4].RefOf(); ok {
			countRef = r
		}
	}
	flags := int64(0)
	if callbackMode {
		if v, ok := argOr(args, 5); ok {
			flags = v.Int()
		}
	}
	if !callbackMode && pattern.Kind() != php.KindArray && replace.Kind() == php.KindArray {
		return php.Null(), php.Raise("TypeError", "preg_replace(): Argument #1 ($pattern) must be of type array when argument #2 ($replacement) is an array, string given")
	}
	type pair struct {
		regex string
		repl  string
	}
	var pairs []pair
	if pattern.Kind() == php.KindArray {
		var repls []string
		if !callbackMode && replace.Kind() == php.KindArray {
			replace.Array().Each(func(_ php.Key, v php.Value) bool {
				repls = append(repls, v.Deref().String())
				return true
			})
		}
		i := 0
		var failed error
		pattern.Array().Each(func(_ php.Key, v php.Value) bool {
			s, err := php.ToString(ctx, v.Deref())
			if err != nil {
				failed = err
				return false
			}
			r := ""
			if !callbackMode {
				if replace.Kind() == php.KindArray {
					if i < len(repls) {
						r = repls[i]
					}
				} else {
					r = replace.String()
				}
			}
			pairs = append(pairs, pair{s.String(), r})
			i++
			return true
		})
		if failed != nil {
			return php.Null(), failed
		}
	} else {
		r := ""
		if !callbackMode {
			r = replace.String()
		}
		pairs = []pair{{pattern.String(), r}}
	}
	offsetCapture := flags&pregOffsetCapture != 0
	unmatchedAsNull := flags&pregUnmatchedAsNull != 0
	var total int64
	setPregError(ctx, pregNoError)

	one := func(s string) (php.Value, error) {
		for _, pr := range pairs {
			prog, err := pregCompile(ctx, fn, pr.regex)
			if err != nil {
				return php.Null(), err
			}
			if prog == nil {
				return php.Null(), nil
			}
			subjectNow := s
			repl := pr.repl
			produce := func(res *pcreResult) (string, error) {
				if !callbackMode {
					return pregSubstitute(repl, subjectNow, res), nil
				}
				matches := pregSubpatterns(prog, subjectNow, res, offsetCapture, unmatchedAsNull)
				v, err := ctx.Call(replace, []php.Value{php.Arr(matches)})
				if err != nil {
					return "", err
				}
				sv, err := php.ToString(ctx, v)
				if err != nil {
					return "", err
				}
				return sv.String(), nil
			}
			out, ok, err := pregReplaceOne(ctx, prog, s, limit, produce, &total)
			if err != nil {
				return php.Null(), err
			}
			if !ok {
				return php.Null(), nil
			}
			s = out
		}
		return php.String(s), nil
	}

	var result php.Value
	if subject.Kind() == php.KindArray {
		out := php.NewArray()
		var failed error
		subject.Array().Each(func(k php.Key, v php.Value) bool {
			sv, err := php.ToString(ctx, v.Deref())
			if err != nil {
				failed = err
				return false
			}
			r, err := one(sv.String())
			if err != nil {
				failed = err
				return false
			}
			if r.Kind() != php.KindNull {
				out.Set(k, r)
			}
			return true
		})
		if failed != nil {
			return php.Null(), failed
		}
		result = php.Arr(out)
	} else {
		r, err := one(subject.String())
		if err != nil {
			return php.Null(), err
		}
		result = r
	}
	if countRef != nil {
		countRef.Set(php.Int(total))
	}
	return result, nil
}

func pregSplit(ctx *php.Ctx, args []php.Value) (php.Value, error) {
	pattern, _ := argOr(args, 0)
	subjectV, _ := argOr(args, 1)
	subject := subjectV.String()
	limit := int64(-1)
	if v, ok := argOr(args, 2); ok && v.Kind() != php.KindNull {
		limit = v.Int()
	}
	flags := int64(0)
	if v, ok := argOr(args, 3); ok {
		flags = v.Int()
	}
	prog, err := pregCompile(ctx, "preg_split", pattern.String())
	if err != nil {
		return php.Null(), err
	}
	if prog == nil {
		return php.Bool(false), nil
	}
	setPregError(ctx, pregNoError)
	noEmpty := flags&pregSplitNoEmpty != 0
	delimCapture := flags&pregSplitDelimCapture != 0
	offsetCapture := flags&pregSplitOffset != 0
	out := php.NewArray()
	add := func(from, to int) {
		v := php.String(subject[from:to])
		if offsetCapture {
			pair := php.NewArray()
			pair.Append(v)
			pair.Append(php.Int(int64(from)))
			out.Append(php.Arr(pair))
			return
		}
		out.Append(v)
	}
	lastMatch := 0
	if limit == 0 {
		limit = -1
	}
	if limit == -1 || limit > 1 {
		if code, err := pregCheckSubject(prog, subject, 0); err != nil {
			return php.Null(), err
		} else if code != pregNoError {
			setPregError(ctx, code)
			return php.Bool(false), nil
		}
		offset := 0
		notEmpty := false
		for limit == -1 || limit > 1 {
			res, err := pregExec(prog, subject, offset, notEmpty, notEmpty)
			if err != nil {
				return php.Null(), err
			}
			if res == nil {
				if notEmpty && offset < len(subject) {
					offset += prog.unitLen(subject, offset)
					notEmpty = false
					continue
				}
				break
			}
			ov := res.ovector
			if !noEmpty || ov[0] != lastMatch {
				add(lastMatch, ov[0])
				if limit != -1 {
					limit--
				}
			}
			if delimCapture {
				for g := 1; g < res.count; g++ {
					if !noEmpty || ov[2*g] != ov[2*g+1] {
						s, e := ov[2*g], ov[2*g+1]
						if s < 0 {
							s, e = 0, 0
						}
						if offsetCapture {
							pair := php.NewArray()
							pair.Append(php.String(subject[s:e]))
							pair.Append(php.Int(int64(ov[2*g])))
							out.Append(php.Arr(pair))
						} else {
							out.Append(php.String(subject[s:e]))
						}
					}
				}
			}
			offset = ov[1]
			lastMatch = ov[1]
			notEmpty = false
			if ov[1] == ov[0] {
				if limit != -1 && limit <= 1 {
					break
				}
				notEmpty = true
			}
		}
	}
	if !noEmpty || lastMatch < len(subject) {
		add(lastMatch, len(subject))
	}
	return php.Arr(out), nil
}

func init() {
	php.RegisterBuiltin("preg_match", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		return pregMatch(ctx, args, false)
	})
	php.RegisterBuiltin("preg_match_all", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		return pregMatch(ctx, args, true)
	})
	php.RegisterBuiltin("preg_replace", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		return pregReplace(ctx, args, false)
	})
	php.RegisterBuiltin("preg_replace_callback", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		return pregReplace(ctx, args, true)
	})
	php.RegisterBuiltin("preg_split", pregSplit)
	php.RegisterBuiltin("preg_last_error", func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Int(pregError(ctx)), nil
	})
	php.RegisterBuiltin("preg_last_error_msg", func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.String(pregErrorMessages[pregError(ctx)]), nil
	})
}
