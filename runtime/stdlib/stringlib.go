package stdlib

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The string library. These are all pure functions of their arguments, so they
// need no interpreter state -- which is why they can live here rather than in
// the interpreter's own dispatch. Every one of them is covered by a
// differential case; where the engine disagreed, the engine won.

func init() {
	php.RegisterBuiltin("stripos", foldedPosition(false))
	php.RegisterBuiltin("strripos", foldedPosition(true))
	php.RegisterBuiltin("strncmp", prefixCompare("strncmp", false))
	php.RegisterBuiltin("strncasecmp", prefixCompare("strncasecmp", true))
	php.RegisterBuiltin("strnatcmp", naturalCompare(func(s string) string { return s }))
	php.RegisterBuiltin("strnatcasecmp", naturalCompare(ToLowerASCII))

	php.RegisterBuiltin("strstr", searchFrom(strings.Index, false))
	php.RegisterBuiltin("stristr", searchFrom(foldedIndex, false))
	php.RegisterBuiltin("strrchr", searchFrom(strings.LastIndex, true))

	php.RegisterBuiltin("strpbrk", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		haystack, characters := TwoStringArgs(args)
		if at := strings.IndexAny(haystack, characters); at >= 0 {
			return php.String(haystack[at:]), nil
		}
		return php.Bool(false), nil
	})
	php.RegisterBuiltin("substr_count", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		haystack, needle := TwoStringArgs(args)
		if needle == "" {
			return php.Undef(), php.Raise("ValueError",
				"substr_count(): Argument #2 ($needle) must not be empty")
		}
		return php.Int(int64(strings.Count(haystack, needle))), nil
	})
	php.RegisterBuiltin("substr_replace", substrReplace)
	php.RegisterBuiltin("substr_compare", substrCompare)
	php.RegisterBuiltin("str_split", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		s := FirstArg(args).String()
		size := 1
		if len(args) > 1 {
			size = int(args[1].Int())
		}
		if size < 1 {
			return php.Undef(), php.Raise("ValueError",
				"str_split(): Argument #2 ($length) must be greater than 0")
		}
		// An empty subject yields no pieces at all, which is what PHP 8.2
		// changed it to; before that it yielded one empty string.
		out := php.NewArray()
		for at := 0; at < len(s); at += size {
			end := at + size
			if end > len(s) {
				end = len(s)
			}
			out.Append(php.String(s[at:end]))
		}
		return php.Arr(out), nil
	})
	php.RegisterBuiltin("chunk_split", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		s := FirstArg(args).String()
		size := 76
		if len(args) > 1 {
			size = int(args[1].Int())
		}
		ending := "\r\n"
		if len(args) > 2 {
			ending = args[2].String()
		}
		if size < 1 {
			return php.Undef(), php.Raise("ValueError",
				"chunk_split(): Argument #2 ($length) must be greater than 0")
		}
		var out strings.Builder
		for at := 0; at < len(s); at += size {
			end := at + size
			if end > len(s) {
				end = len(s)
			}
			out.WriteString(s[at:end])
			out.WriteString(ending)
		}
		if s == "" {
			out.WriteString(ending)
		}
		return php.String(out.String()), nil
	})
	php.RegisterBuiltin("wordwrap", wordwrap)
	php.RegisterBuiltin("strtr", strtr)

	php.RegisterBuiltin("bin2hex", MapStringBuiltin(func(s string) string {
		return hex.EncodeToString([]byte(s))
	}))
	php.RegisterBuiltin("hex2bin", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		decoded, err := hex.DecodeString(FirstArg(args).String())
		if err != nil {
			return php.Bool(false), nil
		}
		return php.String(string(decoded)), nil
	})
	php.RegisterBuiltin("md5", MapStringBuiltin(func(s string) string {
		sum := md5.Sum([]byte(s))
		return hex.EncodeToString(sum[:])
	}))
	php.RegisterBuiltin("sha1", MapStringBuiltin(func(s string) string {
		sum := sha1.Sum([]byte(s))
		return hex.EncodeToString(sum[:])
	}))
	php.RegisterBuiltin("crc32", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		return php.Int(int64(crc32.ChecksumIEEE([]byte(FirstArg(args).String())))), nil
	})
	php.RegisterBuiltin("base64_encode", MapStringBuiltin(func(s string) string {
		return base64.StdEncoding.EncodeToString([]byte(s))
	}))
	php.RegisterBuiltin("base64_decode", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		decoded, err := base64.StdEncoding.DecodeString(FirstArg(args).String())
		if err != nil {
			return php.Bool(false), nil
		}
		return php.String(string(decoded)), nil
	})

	php.RegisterBuiltin("addslashes", MapStringBuiltin(func(s string) string {
		var out strings.Builder
		for i := 0; i < len(s); i++ {
			switch s[i] {
			case '\'', '"', '\\':
				out.WriteByte('\\')
				out.WriteByte(s[i])
			case 0:
				out.WriteString("\\0")
			default:
				out.WriteByte(s[i])
			}
		}
		return out.String()
	}))
	php.RegisterBuiltin("stripslashes", MapStringBuiltin(func(s string) string {
		var out strings.Builder
		for i := 0; i < len(s); i++ {
			if s[i] != '\\' || i+1 >= len(s) {
				if s[i] != '\\' {
					out.WriteByte(s[i])
				}
				continue
			}
			i++
			if s[i] == '0' {
				out.WriteByte(0)
			} else {
				out.WriteByte(s[i])
			}
		}
		return out.String()
	}))
	php.RegisterBuiltin("preg_quote", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		special := `.\+*?[^]$(){}=!<>|:-#`
		if len(args) > 1 && args[1].Deref().Kind() == php.KindString {
			special += args[1].String()
		}
		s := FirstArg(args).String()
		var out strings.Builder
		for i := 0; i < len(s); i++ {
			switch {
			case s[i] == 0:
				out.WriteString(`\000`)
			case strings.IndexByte(special, s[i]) >= 0:
				out.WriteByte('\\')
				out.WriteByte(s[i])
			default:
				out.WriteByte(s[i])
			}
		}
		return php.String(out.String()), nil
	})
	php.RegisterBuiltin("htmlspecialchars", escapeHTMLBuiltin)
	php.RegisterBuiltin("htmlentities", escapeHTMLBuiltin)
	php.RegisterBuiltin("htmlspecialchars_decode", MapStringBuiltin(unescapeHTML))
	php.RegisterBuiltin("html_entity_decode", MapStringBuiltin(unescapeHTML))
	php.RegisterBuiltin("strip_tags", MapStringBuiltin(stripTags))

	php.RegisterBuiltin("levenshtein", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		a, b := TwoStringArgs(args)
		return php.Int(int64(levenshtein(a, b))), nil
	})
	php.RegisterBuiltin("similar_text", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		a, b := TwoStringArgs(args)
		common := similarText(a, b)
		if len(args) > 2 {
			if ref, isRef := args[2].RefOf(); isRef {
				percent := 0.0
				if len(a)+len(b) > 0 {
					percent = float64(common) * 2.0 * 100.0 / float64(len(a)+len(b))
				}
				ref.Set(php.Float(percent))
			}
		}
		return php.Int(int64(common)), nil
	})
	php.RegisterBuiltin("number_format", numberFormat)
	php.RegisterBuiltin("str_word_count", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		words := splitWords(FirstArg(args).String())
		if len(args) > 1 && args[1].Int() != 0 {
			out := php.NewArray()
			for _, w := range words {
				out.Append(php.String(w))
			}
			return php.Arr(out), nil
		}
		return php.Int(int64(len(words))), nil
	})
}

// The ENT_* bits htmlspecialchars reads. They are language constants rather
// than build settings, so they are written down here.
const (
	entQuoteSingle  = 1
	entQuoteDouble  = 2
	entDoctypeMask  = 16 | 32
	entDoctypeXML1  = 16
	entDoctypeXHTML = 32
	entDoctypeHTML5 = 16 | 32
	// PHP 8.1 made the default ENT_QUOTES|ENT_SUBSTITUTE|ENT_HTML401.
	entDefaultFlags = entQuoteSingle | entQuoteDouble | 8
)

// escapeHTMLBuiltin is htmlspecialchars and htmlentities. The two differ only
// in which characters they encode, and for the ASCII the corpus uses they
// agree -- what they share, and what php2go was ignoring, is the $flags and
// $double_encode parameters.
func escapeHTMLBuiltin(_ *php.Ctx, args []php.Value) (php.Value, error) {
	text := FirstArg(args).Deref().String()
	flags := int64(entDefaultFlags)
	if len(args) > 1 && args[1].Deref().Kind() != php.KindNull {
		flags = args[1].Deref().Int()
	}
	doubleEncode := true
	if len(args) > 3 {
		doubleEncode = args[3].Deref().Bool()
	}
	if !doubleEncode && flags&entDoctypeMask == entDoctypeHTML5 {
		// Deciding whether `&name;` is already an entity needs the doctype's
		// table, and php2go carries HTML 4.01's alone: HTML5 defines some two
		// thousand more, and guessing would silently re-encode them.
		return php.Undef(), php.Unsupported(
			"htmlspecialchars with ENT_HTML5 and $double_encode false")
	}
	return php.String(escapeHTMLWith(text, flags, doubleEncode)), nil
}

// escapeHTML is htmlspecialchars with PHP 8.1's default flags, which quote the
// single quote as well as the double.
func escapeHTML(s string) string {
	return escapeHTMLWith(s, entDefaultFlags, true)
}

func escapeHTMLWith(s string, flags int64, doubleEncode bool) string {
	// The single quote is spelled differently by doctype: HTML 4.01 has no
	// `&apos;` and uses the numeric form, every other doctype has it.
	apos := "&#039;"
	if flags&entDoctypeMask != 0 {
		apos = "&apos;"
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			if !doubleEncode {
				if width := existingEntity(s[i:]); width > 0 {
					out.WriteString(s[i : i+width])
					i += width - 1
					continue
				}
			}
			out.WriteString("&amp;")
		case '<':
			out.WriteString("&lt;")
		case '>':
			out.WriteString("&gt;")
		case '"':
			if flags&entQuoteDouble != 0 {
				out.WriteString("&quot;")
				continue
			}
			out.WriteByte('"')
		case '\'':
			if flags&entQuoteSingle != 0 {
				out.WriteString(apos)
				continue
			}
			out.WriteByte('\'')
		default:
			out.WriteByte(s[i])
		}
	}
	return out.String()
}

// existingEntity measures the character reference at the start of s, or 0 when
// there is not one. `$double_encode = false` leaves these alone, and the test
// is not merely syntactic: a *named* reference has to be one the doctype
// defines, so `&lt;` passes and `&notanentity;` does not.
func existingEntity(s string) int {
	if len(s) < 3 || s[0] != '&' {
		return 0
	}
	if s[1] == '#' {
		digits, hex := 2, false
		if len(s) > 2 && (s[2] == 'x' || s[2] == 'X') {
			digits, hex = 3, true
		}
		end := digits
		code := 0
		for end < len(s) && isEntityDigit(s[end], hex) {
			if code <= 0x10FFFF {
				// Stop accumulating once it is certainly out of range, so a
				// long run of digits cannot overflow on the way to the answer.
				code = code*base(hex) + entityDigitValue(s[end])
			}
			end++
		}
		if end == digits || end >= len(s) || s[end] != ';' {
			return 0
		}
		if code > 0x10FFFF {
			// Not a code point, so not a character reference: `&#1114112;` is
			// escaped like any other text.
			return 0
		}
		return end + 1
	}
	end := 1
	for end < len(s) && isEntityNameByte(s[end]) {
		end++
	}
	if end == 1 || end >= len(s) || s[end] != ';' {
		return 0
	}
	if !htmlEntityNames401[s[1:end]] {
		return 0
	}
	return end + 1
}

func base(hex bool) int {
	if hex {
		return 16
	}
	return 10
}

func entityDigitValue(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	default:
		return int(b-'A') + 10
	}
}

func isEntityDigit(b byte, hex bool) bool {
	if b >= '0' && b <= '9' {
		return true
	}
	return hex && (b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F')
}

func isEntityNameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func unescapeHTML(s string) string {
	r := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", `"`, "&#039;", "'", "&#39;", "'")
	return r.Replace(s)
}

// stripTags removes anything between < and >, which is as far as PHP's own
// scanner goes for the plain case.
func stripTags(s string) string {
	var out strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '<':
			depth++
		case s[i] == '>' && depth > 0:
			depth--
		case depth == 0:
			out.WriteByte(s[i])
		}
	}
	return out.String()
}

// foldedPosition builds stripos and strripos.
func foldedPosition(last bool) php.Builtin {
	return func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		haystack, needle := TwoStringArgs(args)
		offset := 0
		if len(args) > 2 {
			offset = int(args[2].Int())
			if offset < 0 {
				offset += len(haystack)
			}
			if offset < 0 || offset > len(haystack) {
				return php.Undef(), php.Raise("ValueError",
					"%s(): Argument #3 ($offset) must be contained in argument #1 ($haystack)",
					map[bool]string{true: "strripos", false: "stripos"}[last])
			}
		}
		folded, target := ToLowerASCII(haystack[offset:]), ToLowerASCII(needle)
		at := strings.Index(folded, target)
		if last {
			at = strings.LastIndex(folded, target)
		}
		if at < 0 {
			return php.Bool(false), nil
		}
		return php.Int(int64(at + offset)), nil
	}
}

// prefixCompare builds strncmp and strncasecmp. The case-folding one is PHP's
// own loop and returns the byte difference; the other goes through memcmp, so
// only its sign is reproducible.
func prefixCompare(name string, fold bool) php.Builtin {
	return func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		a, b := TwoStringArgs(args)
		length := 0
		if len(args) > 2 {
			length = int(args[2].Int())
		}
		if length < 0 {
			return php.Undef(), php.Raise("ValueError",
				"%s(): Argument #3 ($length) must be greater than or equal to 0", name)
		}
		clip := func(s string) int {
			if len(s) > length {
				return length
			}
			return len(s)
		}
		lenA, lenB := clip(a), clip(b)
		if fold {
			return php.Int(int64(FoldedDifference(a, b, lenA, lenB))), nil
		}
		return php.Int(int64(strings.Compare(a[:lenA], b[:lenB]))), nil
	}
}

// naturalCompare builds strnatcmp and strnatcasecmp: runs of digits compare as
// numbers, everything else byte by byte.
func naturalCompare(fold func(string) string) php.Builtin {
	return func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		a, b := TwoStringArgs(args)
		return php.Int(int64(naturalOrder(fold(a), fold(b)))), nil
	}
}

// naturalOrder is PHP's own natural comparison. A run of digits compares as a
// number, except that a leading zero makes the run *fractional* -- "x01" sorts
// before "x1" because the comparison then goes left to right rather than by
// magnitude.
func naturalOrder(a, b string) int {
	// Zeros at the very start of either string are skipped outright, so "0001"
	// and "1" are equal -- but a zero anywhere else makes its run fractional.
	at, bt := skipLeadingZeros(a), skipLeadingZeros(b)
	for {
		for at < len(a) && isSpaceByte(a[at]) {
			at++
		}
		for bt < len(b) && isSpaceByte(b[bt]) {
			bt++
		}
		if at >= len(a) || bt >= len(b) {
			return sign((len(a) - at) - (len(b) - bt))
		}
		if isDigitByte(a[at]) && isDigitByte(b[bt]) {
			compare := compareRun
			if a[at] == '0' || b[bt] == '0' {
				compare = compareFraction
			}
			if c := compare(a[at:], b[bt:]); c != 0 {
				return c
			}
			for at < len(a) && isDigitByte(a[at]) {
				at++
			}
			for bt < len(b) && isDigitByte(b[bt]) {
				bt++
			}
			continue
		}
		if a[at] != b[bt] {
			return sign(int(a[at]) - int(b[bt]))
		}
		at++
		bt++
	}
}

// skipLeadingZeros advances past zeros that begin a string and are followed by
// another digit, which is what PHP does before its first comparison.
func skipLeadingZeros(s string) int {
	at := 0
	for at+1 < len(s) && s[at] == '0' && isDigitByte(s[at+1]) {
		at++
	}
	return at
}

// compareFraction compares two digit runs left to right, as a fractional part
// would be: the first differing digit decides and length breaks the tie.
func compareFraction(a, b string) int {
	for i := 0; ; i++ {
		da := i < len(a) && isDigitByte(a[i])
		db := i < len(b) && isDigitByte(b[i])
		switch {
		case !da && !db:
			return 0
		case !da:
			return -1
		case !db:
			return 1
		case a[i] != b[i]:
			return sign(int(a[i]) - int(b[i]))
		}
	}
}

// compareRun compares two digit runs by magnitude: the longer run wins, and
// only if they are the same length does the first differing digit decide.
func compareRun(a, b string) int {
	bias := 0
	for i := 0; ; i++ {
		da := i < len(a) && isDigitByte(a[i])
		db := i < len(b) && isDigitByte(b[i])
		switch {
		case !da && !db:
			return bias
		case !da:
			return -1
		case !db:
			return 1
		case a[i] != b[i] && bias == 0:
			bias = sign(int(a[i]) - int(b[i]))
		}
	}
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

func isDigitByte(b byte) bool { return b >= '0' && b <= '9' }

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// foldedIndex is strings.Index with ASCII case folding, for stristr.
func foldedIndex(haystack, needle string) int {
	return strings.Index(ToLowerASCII(haystack), ToLowerASCII(needle))
}

// searchFrom builds strstr, stristr and strrchr, which all answer with the tail
// of the haystack from where the needle was found.
func searchFrom(find func(string, string) int, firstByteOnly bool) php.Builtin {
	return func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		haystack, needle := TwoStringArgs(args)
		if firstByteOnly && len(needle) > 1 {
			needle = needle[:1]
		}
		at := find(haystack, needle)
		if at < 0 {
			return php.Bool(false), nil
		}
		if !firstByteOnly && len(args) > 2 && args[2].Bool() {
			return php.String(haystack[:at]), nil
		}
		return php.String(haystack[at:]), nil
	}
}

// substrReplace splices a replacement into a byte range.
func substrReplace(_ *php.Ctx, args []php.Value) (php.Value, error) {
	if len(args) < 3 {
		return php.Undef(), fmt.Errorf("substr_replace() expects at least 3 arguments")
	}
	s := args[0].String()
	replacement := args[1].String()
	start := int(args[2].Int())
	if start < 0 {
		start += len(s)
		if start < 0 {
			start = 0
		}
	}
	if start > len(s) {
		start = len(s)
	}
	length := len(s) - start
	if len(args) > 3 && args[3].Deref().Kind() != php.KindNull {
		length = int(args[3].Int())
		if length < 0 {
			length = len(s) + length - start
		}
		if length < 0 {
			length = 0
		}
	}
	if start+length > len(s) {
		length = len(s) - start
	}
	return php.String(s[:start] + replacement + s[start+length:]), nil
}

// substrCompare compares a slice of one string against another.
func substrCompare(_ *php.Ctx, args []php.Value) (php.Value, error) {
	if len(args) < 3 {
		return php.Undef(), fmt.Errorf("substr_compare() expects at least 3 arguments")
	}
	haystack, needle := args[0].String(), args[1].String()
	offset := int(args[2].Int())
	if offset < 0 {
		offset += len(haystack)
	}
	if offset < 0 || offset > len(haystack) {
		return php.Undef(), php.Raise("ValueError",
			"substr_compare(): Argument #3 ($offset) must be contained in argument #1 ($haystack)")
	}
	left, right := haystack[offset:], needle
	if len(args) > 3 && args[3].Deref().Kind() != php.KindNull {
		length := int(args[3].Int())
		if length < 0 {
			return php.Undef(), php.Raise("ValueError",
				"substr_compare(): Argument #4 ($length) must be greater than or equal to 0")
		}
		if len(left) > length {
			left = left[:length]
		}
		if len(right) > length {
			right = right[:length]
		}
	}
	if len(args) > 4 && args[4].Bool() {
		return php.Int(int64(FoldedDifference(left, right, len(left), len(right)))), nil
	}
	return php.Int(int64(strings.Compare(left, right))), nil
}

// wordwrap breaks a string into lines of at most width bytes.
func wordwrap(_ *php.Ctx, args []php.Value) (php.Value, error) {
	s := FirstArg(args).String()
	width := 75
	if len(args) > 1 {
		width = int(args[1].Int())
	}
	brk := "\n"
	if len(args) > 2 {
		brk = args[2].String()
	}
	cut := len(args) > 3 && args[3].Bool()
	if brk == "" {
		return php.Undef(), php.Raise("ValueError",
			"wordwrap(): Argument #3 ($break) must not be empty")
	}
	if width == 0 && cut {
		return php.Undef(), php.Raise("ValueError",
			"wordwrap(): Argument #4 ($cut_long_words) cannot be true when argument #2 ($width) is 0")
	}
	var out strings.Builder
	line := 0
	lastSpace := -1
	pending := []byte(s)
	for i := 0; i < len(pending); i++ {
		if pending[i] == ' ' {
			if i-line >= width {
				out.WriteString(string(pending[line:i]))
				out.WriteString(brk)
				line = i + 1
				lastSpace = -1
				continue
			}
			lastSpace = i
		}
		if i-line >= width {
			switch {
			case lastSpace > line:
				out.WriteString(string(pending[line:lastSpace]))
				out.WriteString(brk)
				line = lastSpace + 1
				lastSpace = -1
			case cut:
				out.WriteString(string(pending[line:i]))
				out.WriteString(brk)
				line = i
			}
		}
	}
	out.WriteString(string(pending[line:]))
	return php.String(out.String()), nil
}

// strtr is two functions under one name: a pair of byte maps, or a table of
// whole-string replacements applied longest-first.
func strtr(_ *php.Ctx, args []php.Value) (php.Value, error) {
	s := FirstArg(args).String()
	if len(args) == 2 {
		table := args[1].Deref()
		if table.Kind() != php.KindArray {
			return php.Undef(), fmt.Errorf("strtr(): Argument #2 ($from) must be of type array")
		}
		pairs := map[string]string{}
		longest := 0
		table.Array().Each(func(k php.Key, v php.Value) bool {
			from := k.String()
			if from != "" {
				pairs[from] = v.String()
				if len(from) > longest {
					longest = len(from)
				}
			}
			return true
		})
		var out strings.Builder
		for i := 0; i < len(s); {
			matched := false
			for size := longest; size > 0; size-- {
				if i+size > len(s) {
					continue
				}
				if to, ok := pairs[s[i:i+size]]; ok {
					out.WriteString(to)
					i += size
					matched = true
					break
				}
			}
			if !matched {
				out.WriteByte(s[i])
				i++
			}
		}
		return php.String(out.String()), nil
	}
	if len(args) < 3 {
		return php.Undef(), fmt.Errorf("strtr() expects 2 or 3 arguments")
	}
	from, to := args[1].String(), args[2].String()
	if len(from) > len(to) {
		from = from[:len(to)]
	} else if len(to) > len(from) {
		to = to[:len(from)]
	}
	out := []byte(s)
	for i := range out {
		if at := strings.IndexByte(from, out[i]); at >= 0 {
			out[i] = to[at]
		}
	}
	return php.String(string(out)), nil
}

// numberFormat renders a number with grouped thousands.
func numberFormat(_ *php.Ctx, args []php.Value) (php.Value, error) {
	value := FirstArg(args).Float()
	decimals := 0
	if len(args) > 1 {
		decimals = int(args[1].Int())
		if decimals < 0 {
			decimals = 0
		}
	}
	point, separator := ".", ","
	if len(args) > 2 && args[2].Deref().Kind() != php.KindNull {
		point = args[2].String()
	}
	if len(args) > 3 && args[3].Deref().Kind() != php.KindNull {
		separator = args[3].String()
	}
	rendered := strconv.FormatFloat(value, 'f', decimals, 64)
	negative := strings.HasPrefix(rendered, "-")
	rendered = strings.TrimPrefix(rendered, "-")
	whole, fraction := rendered, ""
	if at := strings.IndexByte(rendered, '.'); at >= 0 {
		whole, fraction = rendered[:at], rendered[at+1:]
	}
	var grouped strings.Builder
	for i, digit := range []byte(whole) {
		if i > 0 && (len(whole)-i)%3 == 0 {
			grouped.WriteString(separator)
		}
		grouped.WriteByte(digit)
	}
	out := grouped.String()
	if fraction != "" {
		out += point + fraction
	}
	// A value that rounds to zero loses its sign, which is what PHP prints.
	if negative && strings.Trim(out, "0"+point+separator) != "" {
		out = "-" + out
	}
	return php.String(out), nil
}

// levenshtein is the plain edit distance with unit costs.
func levenshtein(a, b string) int {
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			current[j] = minInt(minInt(current[j-1]+1, previous[j]+1), previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// similarText is PHP's own algorithm: find the longest common run, then recurse
// into what is left on each side of it.
func similarText(a, b string) int {
	if a == "" || b == "" {
		return 0
	}
	bestA, bestB, best := 0, 0, 0
	for i := 0; i < len(a); i++ {
		for j := 0; j < len(b); j++ {
			run := 0
			for i+run < len(a) && j+run < len(b) && a[i+run] == b[j+run] {
				run++
			}
			if run > best {
				bestA, bestB, best = i, j, run
			}
		}
	}
	if best == 0 {
		return 0
	}
	return best + similarText(a[:bestA], b[:bestB]) +
		similarText(a[bestA+best:], b[bestB+best:])
}

// splitWords is str_word_count's notion of a word: letters, plus apostrophe and
// hyphen when they are inside one.
func splitWords(s string) []string {
	var words []string
	start := -1
	isWord := func(i int) bool {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			return true
		}
		return (c == '\'' || c == '-') && start >= 0
	}
	for i := 0; i < len(s); i++ {
		if isWord(i) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			words = append(words, strings.Trim(s[start:i], "'-"))
			start = -1
		}
	}
	if start >= 0 {
		words = append(words, strings.Trim(s[start:], "'-"))
	}
	return words
}
