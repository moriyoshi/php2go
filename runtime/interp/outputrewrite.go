package interp

import (
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// outputRewriter is php-src's request-local URL-Rewriter state. The public PHP
// facade owns string conversion and encoding; this runtime object owns the one
// irreducible part, an internal streaming output handler.
type outputRewriter struct {
	unit    *Unit
	ctx     *php.Ctx
	urlApp  string
	formApp string
	pending string
	active  bool
}

func (r *outputRewriter) add(urlName, urlValue, htmlName, htmlValue string) {
	if r.urlApp != "" {
		r.urlApp += r.unit.setting("arg_separator.output")
	}
	r.urlApp += urlName + "=" + urlValue
	r.formApp += `<input type="hidden" name="` + htmlName +
		`" value="` + htmlValue + `" />`
}

func (r *outputRewriter) reset() {
	r.urlApp = ""
	r.formApp = ""
}

// FilterOutput implements the internal output-handler callback. PHP's scanner
// keeps a partial tag between ordinary CONT calls and flushes it for FLUSH or
// FINAL/END. When reset empties urlApp, any pending fragment passes through.
func (r *outputRewriter) FilterOutput(text string, phase int64) (string, bool, error) {
	if r.urlApp == "" {
		result := r.pending + text
		r.pending = ""
		return result, true, nil
	}
	r.pending += text
	flush := phase&(4|8) != 0
	result, pending := r.rewriteAvailable(r.pending, flush)
	r.pending = pending
	return result, true, nil
}

func asciiAlpha(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func asciiTag(value byte) bool { return asciiAlpha(value) || value == ':' }

func tagSpace(value byte) bool {
	return value == ' ' || value == '\v' || value == '\r' ||
		value == '\t' || value == '\n'
}

func completeTagEnd(text string, start int) (int, bool) {
	var quote byte
	for index := start + 1; index < len(text); index++ {
		value := text[index]
		if quote != 0 {
			if value == quote {
				quote = 0
			}
			continue
		}
		if value == '\'' || value == '"' {
			quote = value
			continue
		}
		if value == '>' {
			return index, true
		}
	}
	return 0, false
}

func (r *outputRewriter) rewriteAvailable(text string, flush bool) (string, string) {
	var result strings.Builder
	position := 0
	for position < len(text) {
		relative := strings.IndexByte(text[position:], '<')
		if relative < 0 {
			result.WriteString(text[position:])
			return result.String(), ""
		}
		start := position + relative
		result.WriteString(text[position:start])
		end, complete := completeTagEnd(text, start)
		if !complete {
			if flush {
				result.WriteString(text[start:])
				return result.String(), ""
			}
			return result.String(), text[start:]
		}
		result.WriteString(r.rewriteTag(text[start : end+1]))
		position = end + 1
	}
	return result.String(), ""
}

type rewriteAttribute struct {
	name             string
	valueStart       int
	valueEnd         int
	hasValue, quoted bool
}

func parseRewriteAttributes(tag string, start int) []rewriteAttribute {
	attributes := []rewriteAttribute{}
	for start < len(tag)-1 {
		for start < len(tag)-1 && tagSpace(tag[start]) {
			start++
		}
		if start >= len(tag)-1 || tag[start] == '>' ||
			tag[start] == '/' && start+1 < len(tag) && tag[start+1] == '>' {
			break
		}
		if !asciiAlpha(tag[start]) {
			break
		}
		nameStart := start
		start++
		for start < len(tag)-1 && (asciiAlpha(tag[start]) || tag[start] == '-') {
			start++
		}
		attribute := rewriteAttribute{name: strings.ToLower(tag[nameStart:start])}
		probe := start
		for probe < len(tag)-1 && tag[probe] == ' ' {
			probe++
		}
		if probe >= len(tag)-1 || tag[probe] != '=' {
			attributes = append(attributes, attribute)
			start = probe
			continue
		}
		probe++
		for probe < len(tag)-1 && tag[probe] == ' ' {
			probe++
		}
		attribute.hasValue = true
		if probe < len(tag)-1 && (tag[probe] == '\'' || tag[probe] == '"') {
			quote := tag[probe]
			attribute.quoted = true
			attribute.valueStart = probe + 1
			probe++
			for probe < len(tag)-1 && tag[probe] != quote {
				probe++
			}
			attribute.valueEnd = probe
			if probe < len(tag)-1 {
				probe++
			}
		} else {
			attribute.valueStart = probe
			for probe < len(tag)-1 && !tagSpace(tag[probe]) &&
				tag[probe] != '>' && tag[probe] != '\'' && tag[probe] != '"' {
				probe++
			}
			attribute.valueEnd = probe
		}
		attributes = append(attributes, attribute)
		start = probe
	}
	return attributes
}

func parseRewriteTags(setting string) map[string]string {
	tags := map[string]string{}
	for _, entry := range strings.Split(setting, ",") {
		name, attribute, found := strings.Cut(entry, "=")
		if !found || name == "" {
			continue
		}
		// OnUpdateOutputTags lowercases only the key. In particular, whitespace
		// around a comma or '=' remains part of the configured token.
		tags[strings.ToLower(name)] = strings.ToLower(attribute)
	}
	return tags
}

func parseRewriteHosts(setting string) map[string]bool {
	hosts := map[string]bool{}
	for _, host := range strings.Split(setting, ",") {
		// OnUpdateOutputHosts lowercases entries but does not trim them.
		host = strings.ToLower(host)
		if host != "" {
			hosts[host] = true
		}
	}
	return hosts
}

func splitURLAuthority(raw string) (scheme, host string, authorityEnd int, ok bool) {
	start := 0
	if colon := strings.IndexByte(raw, ':'); colon >= 0 {
		beforeDelimiter := len(raw)
		for _, delimiter := range []byte{'/', '?', '#'} {
			if index := strings.IndexByte(raw, delimiter); index >= 0 && index < beforeDelimiter {
				beforeDelimiter = index
			}
		}
		if colon < beforeDelimiter {
			scheme = strings.ToLower(raw[:colon])
			if scheme != "http" && scheme != "https" {
				return scheme, "", 0, false
			}
			start = colon + 1
		}
	}
	if strings.HasPrefix(raw[start:], "//") {
		authorityStart := start + 2
		authorityEnd = len(raw)
		for _, delimiter := range []byte{'/', '?', '#'} {
			if index := strings.IndexByte(raw[authorityStart:], delimiter); index >= 0 && authorityStart+index < authorityEnd {
				authorityEnd = authorityStart + index
			}
		}
		authority := raw[authorityStart:authorityEnd]
		if at := strings.LastIndexByte(authority, '@'); at >= 0 {
			authority = authority[at+1:]
		}
		if strings.HasPrefix(authority, "[") {
			if close := strings.IndexByte(authority, ']'); close >= 0 {
				host = authority[1:close]
			} else {
				return scheme, "", 0, false
			}
		} else if colon := strings.LastIndexByte(authority, ':'); colon >= 0 {
			host = authority[:colon]
		} else {
			host = authority
		}
	}
	return scheme, strings.ToLower(host), authorityEnd, true
}

func (r *outputRewriter) hostAllowed(host string) bool {
	if host == "" {
		return true
	}
	return parseRewriteHosts(r.unit.setting("url_rewriter.hosts"))[strings.ToLower(host)]
}

func (r *outputRewriter) requestHost() string {
	server, ok := r.ctx.Globals.Get(php.StringKey("_SERVER"))
	if !ok || server.Deref().Kind() != php.KindArray {
		return ""
	}
	host, ok := server.Deref().Array().Get(php.StringKey("HTTP_HOST"))
	if !ok || host.Deref().Kind() != php.KindString {
		return ""
	}
	value := host.Deref().String()
	if colon := strings.IndexByte(value, ':'); colon >= 0 {
		value = value[:colon]
	}
	return strings.ToLower(value)
}

func (r *outputRewriter) formAllowed(action string) bool {
	if action == "" {
		return true
	}
	_, host, _, valid := splitURLAuthority(action)
	if !valid {
		return false
	}
	if host == "" {
		return true
	}
	hosts := parseRewriteHosts(r.unit.setting("url_rewriter.hosts"))
	if len(hosts) == 0 {
		return host == r.requestHost()
	}
	return hosts[host]
}

func (r *outputRewriter) modifyURL(raw string) string {
	if strings.HasPrefix(raw, "#") {
		return raw
	}
	_, host, authorityEnd, valid := splitURLAuthority(raw)
	if !valid || !r.hostAllowed(host) {
		return raw
	}
	fragment := strings.IndexByte(raw, '#')
	end := len(raw)
	if fragment >= 0 {
		end = fragment
	}
	if host != "" && authorityEnd == end {
		return raw[:end] + "/?" + r.urlApp + raw[end:]
	}
	separator := "?"
	if strings.Contains(raw[:end], "?") {
		separator = r.unit.setting("arg_separator.output")
	}
	return raw[:end] + separator + r.urlApp + raw[end:]
}

func (r *outputRewriter) rewriteTag(tag string) string {
	position := 1
	nameStart := position
	for position < len(tag)-1 && asciiTag(tag[position]) {
		position++
	}
	if position == nameStart {
		return tag
	}
	name := strings.ToLower(tag[nameStart:position])
	target, selected := parseRewriteTags(r.unit.setting("url_rewriter.tags"))[name]
	if !selected {
		return tag
	}
	attributes := parseRewriteAttributes(tag, position)
	action := ""
	var result strings.Builder
	written := 0
	for _, attribute := range attributes {
		if attribute.name == "action" && attribute.hasValue {
			action = tag[attribute.valueStart:attribute.valueEnd]
		}
		if target == "" || attribute.name != target || !attribute.hasValue {
			continue
		}
		result.WriteString(tag[written:attribute.valueStart])
		result.WriteString(r.modifyURL(tag[attribute.valueStart:attribute.valueEnd]))
		written = attribute.valueEnd
	}
	result.WriteString(tag[written:])
	if name == "form" && r.formApp != "" && r.formAllowed(action) {
		result.WriteString(r.formApp)
	}
	return result.String()
}
