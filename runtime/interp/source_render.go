package interp

import (
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

type sourceTokenKind uint8

const (
	sourceHTML sourceTokenKind = iota
	sourceComment
	sourceDefault
	sourceKeyword
	sourceString
	sourceWhitespace
	sourceHeredocEnd
)

type sourceToken struct {
	kind sourceTokenKind
	text string
}

var sourceKeywords = map[string]bool{
	"__halt_compiler": true, "abstract": true, "and": true, "array": true,
	"as": true, "break": true, "callable": true, "case": true, "catch": true,
	"class": true, "clone": true, "const": true, "continue": true, "declare": true,
	"default": true, "die": true, "do": true, "echo": true, "else": true,
	"elseif": true, "empty": true, "enddeclare": true, "endfor": true,
	"endforeach": true, "endif": true, "endswitch": true, "endwhile": true,
	"enum": true, "eval": true, "exit": true, "extends": true, "final": true,
	"finally": true, "fn": true, "for": true, "foreach": true,
	"function": true, "global": true, "goto": true, "if": true, "implements": true,
	"include": true, "include_once": true, "instanceof": true, "insteadof": true,
	"interface": true, "isset": true, "list": true, "match": true, "namespace": true,
	"new": true, "or": true, "print": true, "private": true, "protected": true,
	"public": true, "readonly": true, "require": true, "require_once": true,
	"return": true, "static": true, "switch": true, "throw": true, "trait": true,
	"try": true, "unset": true, "use": true, "var": true, "while": true,
	"xor": true, "yield": true,
}

func isSourceSpace(char byte) bool {
	switch char {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

func isSourceNameStart(char byte) bool {
	return char == '_' || char >= 0x80 || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
}

func isSourceName(char byte) bool {
	return isSourceNameStart(char) || char >= '0' && char <= '9'
}

func sourceOpenTag(source string, at int) (int, bool) {
	if at+3 <= len(source) && source[at:at+3] == "<?=" {
		return 3, true
	}
	if at+5 > len(source) || !strings.EqualFold(source[at:at+5], "<?php") {
		return 0, false
	}
	if at+5 == len(source) || !isSourceSpace(source[at+5]) {
		return 0, false
	}
	return 6, true // The required first whitespace byte belongs to T_OPEN_TAG.
}

func lexSource(source string) []sourceToken {
	tokens := make([]sourceToken, 0, 32)
	emit := func(kind sourceTokenKind, text string) {
		if text == "" {
			return
		}
		if len(tokens) != 0 && tokens[len(tokens)-1].kind == kind && kind != sourceHeredocEnd {
			tokens[len(tokens)-1].text += text
			return
		}
		tokens = append(tokens, sourceToken{kind: kind, text: text})
	}
	position := 0
	phpMode := false
	for position < len(source) {
		if !phpMode {
			opening := -1
			openingLength := 0
			for search := position; search+2 <= len(source); {
				relative := strings.Index(source[search:], "<?")
				if relative < 0 {
					break
				}
				candidate := search + relative
				if length, ok := sourceOpenTag(source, candidate); ok {
					opening, openingLength = candidate, length
					break
				}
				search = candidate + 2
			}
			if opening < 0 {
				emit(sourceHTML, source[position:])
				break
			}
			emit(sourceHTML, source[position:opening])
			emit(sourceDefault, source[opening:opening+openingLength])
			position = opening + openingLength
			phpMode = true
			continue
		}

		if position+2 <= len(source) && source[position:position+2] == "?>" {
			end := position + 2
			if end < len(source) && source[end] == '\n' {
				end++
			} else if end+1 < len(source) && source[end] == '\r' && source[end+1] == '\n' {
				end += 2
			}
			emit(sourceDefault, source[position:end])
			position, phpMode = end, false
			continue
		}
		char := source[position]
		if isSourceSpace(char) {
			end := position + 1
			for end < len(source) && isSourceSpace(source[end]) {
				end++
			}
			emit(sourceWhitespace, source[position:end])
			position = end
			continue
		}
		if position+2 <= len(source) && source[position:position+2] == "/*" {
			end := strings.Index(source[position+2:], "*/")
			if end < 0 {
				emit(sourceComment, source[position:])
				break
			}
			end += position + 4
			emit(sourceComment, source[position:end])
			position = end
			continue
		}
		if position+2 <= len(source) && source[position:position+2] == "//" || char == '#' && (position+1 >= len(source) || source[position+1] != '[') {
			end := position
			for end < len(source) && source[end] != '\n' && source[end] != '\r' {
				end++
			}
			emit(sourceComment, source[position:end])
			position = end
			continue
		}
		if position+3 <= len(source) && source[position:position+3] == "<<<" {
			if next, heredocTokens, ok := lexHeredoc(source, position); ok {
				for _, token := range heredocTokens {
					emit(token.kind, token.text)
				}
				position = next
				continue
			}
		}
		if char == '\'' {
			end := quotedSourceEnd(source, position, char)
			emit(sourceString, source[position:end])
			position = end
			continue
		}
		if char == '"' || char == '`' {
			end := quotedSourceEnd(source, position, char)
			lexInterpolated(source[position:end], emit)
			position = end
			continue
		}
		if char == '$' && position+1 < len(source) && isSourceNameStart(source[position+1]) {
			end := position + 2
			for end < len(source) && isSourceName(source[end]) {
				end++
			}
			emit(sourceDefault, source[position:end])
			position = end
			continue
		}
		if isSourceNameStart(char) || char == '\\' && position+1 < len(source) && isSourceNameStart(source[position+1]) {
			end := position + 1
			for end < len(source) && (isSourceName(source[end]) || source[end] == '\\') {
				end++
			}
			text := source[position:end]
			kind := sourceDefault
			lower := strings.ToLower(text)
			if sourceKeywords[lower] {
				kind = sourceKeyword
			} else if lower == "from" {
				for previous := len(tokens) - 1; previous >= 0; previous-- {
					if tokens[previous].kind == sourceWhitespace || tokens[previous].kind == sourceComment {
						continue
					}
					if tokens[previous].kind == sourceKeyword && strings.EqualFold(tokens[previous].text, "yield") {
						kind = sourceKeyword
					}
					break
				}
			}
			emit(kind, text)
			position = end
			continue
		}
		if char >= '0' && char <= '9' || char == '.' && position+1 < len(source) && source[position+1] >= '0' && source[position+1] <= '9' {
			end := sourceNumberEnd(source, position)
			emit(sourceDefault, source[position:end])
			position = end
			continue
		}
		emit(sourceKeyword, source[position:position+1])
		position++
	}
	return tokens
}

func sourceNumberEnd(source string, start int) int {
	position := start
	digit := func(char byte) bool { return char >= '0' && char <= '9' || char == '_' }
	if source[position] == '0' && position+2 <= len(source) {
		switch source[position+1] {
		case 'x', 'X':
			position += 2
			for position < len(source) && (digit(source[position]) || source[position] >= 'a' && source[position] <= 'f' || source[position] >= 'A' && source[position] <= 'F') {
				position++
			}
			return position
		case 'b', 'B':
			position += 2
			for position < len(source) && (source[position] == '0' || source[position] == '1' || source[position] == '_') {
				position++
			}
			return position
		case 'o', 'O':
			position += 2
			for position < len(source) && (source[position] >= '0' && source[position] <= '7' || source[position] == '_') {
				position++
			}
			return position
		}
	}
	for position < len(source) && digit(source[position]) {
		position++
	}
	if position < len(source) && source[position] == '.' {
		position++
		for position < len(source) && digit(source[position]) {
			position++
		}
	}
	if position < len(source) && (source[position] == 'e' || source[position] == 'E') {
		exponent := position + 1
		if exponent < len(source) && (source[exponent] == '+' || source[exponent] == '-') {
			exponent++
		}
		first := exponent
		for exponent < len(source) && digit(source[exponent]) {
			exponent++
		}
		if exponent != first {
			position = exponent
		}
	}
	return position
}

func quotedSourceEnd(source string, start int, quote byte) int {
	for position := start + 1; position < len(source); position++ {
		if source[position] == '\\' {
			position++
			continue
		}
		if position < len(source) && source[position] == quote {
			return position + 1
		}
	}
	return len(source)
}

func lexInterpolated(text string, emit func(sourceTokenKind, string)) {
	plain := 0
	for position := 1; position+1 < len(text); {
		if text[position] == '\\' {
			position += 2
			continue
		}
		if text[position] == '$' && position+1 < len(text) && isSourceNameStart(text[position+1]) {
			emit(sourceString, text[plain:position])
			end := position + 2
			for end < len(text) && isSourceName(text[end]) {
				end++
			}
			emit(sourceDefault, text[position:end])
			plain, position = end, end
			continue
		}
		if text[position] == '{' || text[position] == '}' {
			emit(sourceString, text[plain:position])
			emit(sourceKeyword, text[position:position+1])
			position++
			plain = position
			continue
		}
		position++
	}
	emit(sourceString, text[plain:])
}

func lexHeredoc(source string, start int) (int, []sourceToken, bool) {
	lineEnd := strings.IndexByte(source[start:], '\n')
	if lineEnd < 0 {
		return 0, nil, false
	}
	lineEnd += start + 1
	header := strings.TrimSpace(source[start+3 : lineEnd])
	nowdoc := len(header) >= 2 && header[0] == '\'' && header[len(header)-1] == '\''
	if nowdoc || len(header) >= 2 && header[0] == '"' && header[len(header)-1] == '"' {
		header = header[1 : len(header)-1]
	}
	if header == "" || !isSourceNameStart(header[0]) {
		return 0, nil, false
	}
	for _, char := range []byte(header) {
		if !isSourceName(char) {
			return 0, nil, false
		}
	}
	bodyStart := lineEnd
	for line := bodyStart; line <= len(source); {
		end := strings.IndexByte(source[line:], '\n')
		if end < 0 {
			end = len(source)
		} else {
			end += line
		}
		content := strings.TrimSuffix(source[line:end], "\r")
		trimmed := strings.TrimLeft(content, " \t")
		if trimmed == header || trimmed == header+";" {
			labelAt := line + len(content) - len(trimmed)
			tokens := []sourceToken{{kind: sourceKeyword, text: source[start:bodyStart]}}
			body := source[bodyStart:labelAt]
			if nowdoc {
				tokens = append(tokens, sourceToken{kind: sourceString, text: body})
			} else {
				lexInterpolated(body+"\x00", func(kind sourceTokenKind, text string) {
					if strings.HasSuffix(text, "\x00") {
						text = strings.TrimSuffix(text, "\x00")
					}
					tokens = append(tokens, sourceToken{kind: kind, text: text})
				})
			}
			tokens = append(tokens, sourceToken{kind: sourceHeredocEnd, text: source[labelAt : labelAt+len(header)]})
			position := labelAt + len(header)
			return position, tokens, true
		}
		if end == len(source) {
			break
		}
		line = end + 1
	}
	return 0, nil, false
}

func escapeHighlightedSource(text string) string {
	var out strings.Builder
	for position := 0; position < len(text); position++ {
		switch text[position] {
		case '<':
			out.WriteString("&lt;")
		case '>':
			out.WriteString("&gt;")
		case '&':
			out.WriteString("&amp;")
		case '\t':
			out.WriteString("    ")
		default:
			out.WriteByte(text[position])
		}
	}
	return out.String()
}

func highlightSource(unit *Unit, source string) string {
	colors := map[sourceTokenKind]string{
		sourceHTML: unit.setting("highlight.html"), sourceComment: unit.setting("highlight.comment"),
		sourceDefault: unit.setting("highlight.default"), sourceKeyword: unit.setting("highlight.keyword"),
		sourceString: unit.setting("highlight.string"), sourceHeredocEnd: unit.setting("highlight.keyword"),
	}
	last := colors[sourceHTML]
	var out strings.Builder
	out.WriteString(`<pre><code style="color: `)
	out.WriteString(last)
	out.WriteString(`">`)
	for _, token := range lexSource(source) {
		if token.kind == sourceWhitespace {
			out.WriteString(escapeHighlightedSource(token.text))
			continue
		}
		next := colors[token.kind]
		if next != last {
			if last != colors[sourceHTML] {
				out.WriteString("</span>")
			}
			last = next
			if last != colors[sourceHTML] {
				out.WriteString(`<span style="color: `)
				out.WriteString(last)
				out.WriteString(`">`)
			}
		}
		out.WriteString(escapeHighlightedSource(token.text))
	}
	if last != colors[sourceHTML] {
		out.WriteString("</span>")
	}
	out.WriteString("</code></pre>")
	return out.String()
}

func stripSource(source string) string {
	tokens := lexSource(source)
	var out strings.Builder
	previousSpace := false
	for position := 0; position < len(tokens); position++ {
		token := tokens[position]
		switch token.kind {
		case sourceWhitespace:
			if !previousSpace {
				out.WriteByte(' ')
				previousSpace = true
			}
			continue
		case sourceComment:
			continue
		case sourceHeredocEnd:
			out.WriteString(token.text)
			if position+1 < len(tokens) && tokens[position+1].kind != sourceWhitespace {
				position++
				out.WriteString(tokens[position].text)
			}
			out.WriteByte('\n')
			previousSpace = true
			continue
		default:
			out.WriteString(token.text)
			previousSpace = false
		}
	}
	return out.String()
}

func (in *Interp) sourceRenderBuiltin(ctx *php.Ctx, unit *Unit, name string, args []php.Value) (php.Value, bool, error) {
	switch name {
	case "highlight_string", "highlight_file", "show_source", "php_strip_whitespace":
	default:
		return php.Undef(), false, nil
	}
	input := args[0].Deref().String()
	canonical := name
	if canonical == "show_source" {
		canonical = "highlight_file"
	}
	if canonical != "highlight_string" && strings.IndexByte(input, 0) >= 0 {
		return php.Undef(), true, Raise("ValueError", "%s(): Argument #1 ($filename) must not contain any null bytes", name)
	}
	if canonical == "highlight_string" {
		rendered := highlightSource(unit, input)
		if len(args) > 1 && args[1].Deref().Bool() {
			return php.String(rendered), true, nil
		}
		ctx.Write(rendered)
		return php.Bool(true), true, nil
	}
	data, err := in.readFile(ctx, input)
	if err != nil {
		ctx.Warn("%s(%s): Failed to open stream: No such file or directory", canonical, input)
		if canonical == "highlight_file" {
			ctx.Warn("highlight_file(): Failed opening '%s' for highlighting", input)
			return php.Bool(false), true, nil
		}
		return php.String(""), true, nil
	}
	if canonical == "php_strip_whitespace" {
		return php.String(stripSource(string(data))), true, nil
	}
	rendered := highlightSource(unit, string(data))
	if len(args) > 1 && args[1].Deref().Bool() {
		return php.String(rendered), true, nil
	}
	ctx.Write(rendered)
	return php.Bool(true), true, nil
}
