package stdlib

import (
	"errors"
	"io"
	"os"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The `php://filter` wrapper for compiled code: `php://filter/read=A|B/
// write=C/resource=<target>` opens the target and puts the named filters on
// its chains, the engine's php_stream_url_wrap_php filter arm step for step
// (probed):
//
//   - the target is what follows the first `/resource=`; with none the open
//     throws Error "No URL resource specified", and an empty one is the
//     ValueError "Path must not be empty";
//   - the target is opened first and quietly; failing, the open reports
//     "Failed to open stream: operation failed" against the whole URL and no
//     filter is created;
//   - the part before `/resource=` is split on `/`, empty pieces skipped. A
//     `read=` / `write=` piece (any case) names the read or the write chain,
//     a bare one both -- each only as far as the open mode allows (read for
//     r or +, write for w, a or +). When `/resource=` starts the remainder,
//     the engine's NUL lands on the leading slash and the whole remainder is
//     taken as filter pieces, so `php://filter/resource=X` asks for a filter
//     named `resource=X`;
//   - each piece is split on `|`, empty names skipped, and every name is
//     URL-decoded after the split: `%7C` does not split;
//   - a name nothing registers warns "Unable to locate filter", one whose
//     creation fails "Unable to create or locate filter", and either then
//     "Unable to create filter (name)"; the stream opens without it.
//
// The stream handed back is the target's own, with the filters on it.

// IsFilterURL reports a `php://filter/` URL, scheme and wrapper name in any
// case. Anything else under php:// is the wrapper's other arms, not here.
func IsFilterURL(path string) bool {
	return len(path) >= len("php://filter/") && strings.EqualFold(path[:len("php://filter/")], "php://filter/")
}

const (
	filterURLRead  = 1
	filterURLWrite = 2
)

// OpenFilterURL opens a php://filter URL for function (the name the
// diagnostics carry). forInclude refuses URL wrappers as the target, as the
// engine's allow_url_include=0 does (`data:` fails the open there). report
// says whether a failed open warns, which a nested target's does not. The
// boolean is false when the open failed the PHP way.
func OpenFilterURL(ctx *php.Ctx, url, mode, function string, forInclude, report bool) (*Stream, *php.Resource, bool, error) {
	rest := url[len("php://filter"):]
	at := strings.Index(rest, "/resource=")
	if at < 0 {
		return nil, nil, false, php.Raise("Error", "No URL resource specified")
	}
	target := rest[at+len("/resource="):]
	if target == "" {
		return nil, nil, false, php.Raise("ValueError", "Path must not be empty")
	}
	held, handle, opened, err := openFilterTarget(ctx, target, mode, function, forInclude)
	if err != nil {
		return nil, nil, false, err
	}
	if !opened {
		if report {
			ctx.Warn("%s(%s): Failed to open stream: operation failed", function, url)
		}
		return nil, nil, false, nil
	}
	pieces := rest[1:]
	if at > 0 {
		pieces = rest[1:at]
	}
	rw := 0
	if strings.ContainsAny(mode, "r+") {
		rw |= filterURLRead
	}
	if strings.ContainsAny(mode, "wa+") {
		rw |= filterURLWrite
	}
	for _, piece := range strings.Split(pieces, "/") {
		if piece == "" {
			continue
		}
		chains := rw
		list := piece
		switch {
		case len(piece) >= 5 && strings.EqualFold(piece[:5], "read="):
			chains, list = rw&filterURLRead, piece[5:]
		case len(piece) >= 6 && strings.EqualFold(piece[:6], "write="):
			chains, list = rw&filterURLWrite, piece[6:]
		}
		for _, name := range strings.Split(list, "|") {
			if name == "" {
				continue
			}
			name = phpURLDecode(name)
			for _, chain := range []int{filterURLRead, filterURLWrite} {
				if chains&chain == 0 {
					continue
				}
				filter, err := createURLFilter(ctx, name, held, handle, function)
				if err != nil {
					return nil, nil, false, err
				}
				if filter == nil {
					ctx.Warn("%s(): Unable to create filter (%s)", function, name)
					continue
				}
				filter.SetRead(chain == filterURLRead)
				held.AttachFilter(filter, true)
				if filter.Object != nil {
					ctx.CloseAtRequestEnd(handle, func() error {
						err := held.Close()
						handle.Close()
						if errors.Is(err, ErrStreamFilterFatal) || errors.Is(err, ErrUserStreamOperation) {
							return nil
						}
						return err
					})
				}
			}
		}
	}
	held.filterURL = url
	return held, handle, true, nil
}

// createURLFilter is php_stream_filter_create for one name: the engine's
// filters, then a program's registered class.
func createURLFilter(ctx *php.Ctx, name string, held *Stream, handle *php.Resource, function string) (*StreamFilter, error) {
	if builtin, found := NewBuiltinStreamFilter(ctx, name, php.Undef(), function); found {
		if builtin == nil {
			ctx.Warn("%s(): Unable to create or locate filter %q", function, name)
			return nil, nil
		}
		builtin.Stream = held
		return builtin, nil
	}
	if className, found := ctx.StreamFilterClass(name); found {
		filter, err := newHookedUserStreamFilter(ctx, name, className, php.Undef(), held, handle, function)
		if err != nil {
			return nil, err
		}
		if filter == nil {
			ctx.Warn("%s(): Unable to create or locate filter %q", function, name)
		}
		return filter, nil
	}
	ctx.Warn("%s(): Unable to locate filter %q", function, name)
	return nil, nil
}

// openFilterTarget opens the resource a php://filter URL names, reporting
// nothing: a `data:` URI (not for an include), php://memory or php://temp, a
// nested php://filter, or a local file. Any other wrapper is a gap.
func openFilterTarget(ctx *php.Ctx, target, mode, function string, forInclude bool) (*Stream, *php.Resource, bool, error) {
	if IsFilterURL(target) {
		return OpenFilterURL(ctx, target, mode, function, forInclude, false)
	}
	if data, ok := DataStream(target); ok {
		if forInclude {
			return nil, nil, false, nil
		}
		data.Configure(true, 8192, mode, target)
		handle := ctx.NewResource("stream", data)
		return data, handle, true, nil
	}
	if _, memory := MemoryStreamURI(target); memory {
		value, err := OpenMemoryStream(ctx, target, mode)
		if err != nil {
			return nil, nil, false, err
		}
		held, handle, ok := AsStream(value)
		return held, handle, ok, nil
	}
	path := target
	if scheme, wrapped := StreamScheme(target); wrapped {
		if !strings.EqualFold(scheme, "file") {
			return nil, nil, false, php.Unsupported("php://filter over %s", target)
		}
		local, ok, _ := LocalFilePath(target, false)
		if !ok {
			return nil, nil, false, php.Unsupported("php://filter over %s", target)
		}
		path = local
	}
	flag := os.O_RDONLY
	switch strings.TrimRight(strings.TrimSuffix(mode, "b"), "t") {
	case "r":
	case "r+":
		flag = os.O_RDWR
	case "w":
		flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	case "w+":
		flag = os.O_RDWR | os.O_CREATE | os.O_TRUNC
	case "a":
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	case "a+":
		flag = os.O_RDWR | os.O_CREATE | os.O_APPEND
	default:
		return nil, nil, false, php.Unsupported("php://filter with mode %s", mode)
	}
	file, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		return nil, nil, false, nil
	}
	held := NewFileStream(file, mode, path)
	return held, ctx.NewResource("stream", held), true, nil
}

// phpURLDecode is php_url_decode: `+` is a space and `%XX` a byte; a `%`
// not followed by two hex digits stays.
func phpURLDecode(s string) string {
	if !strings.ContainsAny(s, "+%") {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '+':
			out = append(out, ' ')
		case c == '%' && i+2 < len(s) && urlHexDigit(s[i+1]) && urlHexDigit(s[i+2]):
			out = append(out, urlHexValue(s[i+1])<<4|urlHexValue(s[i+2]))
			i += 2
		default:
			out = append(out, c)
		}
	}
	return string(out)
}

func urlHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func urlHexValue(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

// readAllFiltered drains a stream through its read chain. A filter failing
// stops the read with what came before it (probed: file_get_contents over a
// filter that answers PSFS_ERR_FATAL is "" plus the filter's warning).
func readAllFiltered(held *Stream, function string) (string, error) {
	var out []byte
	buf := make([]byte, 8192)
	for {
		n, err := held.ReadFiltered(buf, function)
		out = append(out, buf[:n]...)
		if err != nil {
			if err == io.EOF {
				return string(out), nil
			}
			return string(out), err
		}
		if n == 0 {
			return string(out), nil
		}
	}
}

// FilterURLContents is file_get_contents over a php://filter URL: false when
// the open failed, the filtered bytes otherwise.
func FilterURLContents(ctx *php.Ctx, url string) (php.Value, error) {
	held, handle, opened, err := OpenFilterURL(ctx, url, "rb", "file_get_contents", false, true)
	if err != nil || !opened {
		return php.Bool(false), err
	}
	text, err := readAllFiltered(held, "file_get_contents")
	if err != nil && !errors.Is(err, ErrStreamFilterFatal) {
		return php.Undef(), err
	}
	if err := closeFilterURL(held, handle); err != nil {
		return php.Undef(), err
	}
	return php.String(text), nil
}

// FilterURLPut is file_put_contents over a php://filter URL: the bytes the
// write chain took.
func FilterURLPut(ctx *php.Ctx, url, data string, appendTo bool) (php.Value, error) {
	mode := "wb"
	if appendTo {
		mode = "ab"
	}
	held, handle, opened, err := OpenFilterURL(ctx, url, mode, "file_put_contents", false, true)
	if err != nil || !opened {
		return php.Bool(false), err
	}
	written, err := held.WriteFiltered(data, "file_put_contents")
	if err != nil && !errors.Is(err, ErrStreamFilterFatal) {
		return php.Undef(), err
	}
	if err := closeFilterURL(held, handle); err != nil {
		return php.Undef(), err
	}
	return php.Int(int64(written)), nil
}

func closeFilterURL(held *Stream, handle *php.Resource) error {
	err := held.Close()
	handle.Close()
	if errors.Is(err, ErrStreamFilterFatal) || errors.Is(err, ErrUserStreamOperation) {
		return nil
	}
	return err
}

// IncludeFilterURL is the open and read an include makes of a php://filter
// URL, kind being the include form. It answers failed when the open or the
// read failed the PHP way -- the diagnostics are already out and the caller
// reports the inclusion failing. Code that did come through cannot be run:
// no compiled body is this URL's (its `__FILE__` is the URL), so it is a gap.
func IncludeFilterURL(ctx *php.Ctx, url, kind string) (bool, error) {
	held, handle, opened, err := OpenFilterURL(ctx, url, "rb", kind, true, true)
	if err != nil {
		return false, err
	}
	if !opened {
		return true, nil
	}
	_, readErr := readAllFiltered(held, kind)
	if readErr != nil && !errors.Is(readErr, ErrStreamFilterFatal) {
		return false, readErr
	}
	if err := closeFilterURL(held, handle); err != nil {
		return false, err
	}
	if readErr != nil {
		return true, nil
	}
	return false, php.Unsupported("include of %s", url)
}
