package stdlib

import (
	"io"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// A stream driven by a wrapper **object** through the Ctx's hooks rather than
// through a back-end's own method dispatch: instantiate with NewInstance, ask
// HasMethod, call with Call. It is what compiled code opens a wrapper the
// program registered with -- it has no interpreter to ask -- and what opens
// php://memory and php://temp in either back-end, whose behaviour is a
// userland class (MemoryStreamWrapperClass).
//
// The protocol is runtime/interp's userStream, which the corpus measured:
// the streams layer reads ahead in chunks of 8192 (fread($s, 2) asks
// stream_read(8192)), keeps what it did not hand out, and seeks the wrapper
// back to the logical position before a write.

// MemoryStreamWrapperClass is the userland class behind php://memory and
// php://temp (internal/userland/ext/streams.php).
const MemoryStreamWrapperClass = `PHP2Go\Extension\Streams\MemoryStreamWrapper`

type hookedStream struct {
	ctx    *php.Ctx
	object *php.Object
	class  string
	owner  *Stream

	unread          []byte
	rawEOF          bool
	logicalPosition int64
	seekable        bool
	closed          bool
	// inner is php://temp's second resource (OpenMemoryStream), and spilled
	// the third, taken when a write reaches spillLimit; -1 is no limit.
	inner, spilled *php.Resource
	spillLimit     int64
}

func (user *hookedStream) call(name string, args []php.Value) (php.Value, bool, error) {
	if !user.ctx.HasMethod(user.object.ClassName, name) {
		return php.Undef(), false, nil
	}
	callable := php.NewArray()
	callable.Append(php.Obj(user.object))
	callable.Append(php.String(name))
	// The method moves the location into its own body; a diagnostic raised
	// after it -- `"W::stream_open" call failed` -- belongs to the caller's
	// line (gh11735_2).
	file, line := user.ctx.Location()
	value, err := user.ctx.Call(php.Arr(callable), args)
	user.ctx.SetLocation(file, line)
	return value, true, err
}

func (user *hookedStream) className() string { return php.DisplayName(user.class) }

// warn raises a wrapper diagnostic as the engine's docref does, under the
// library function that reached the wrapper: `fread(): W::stream_eof is not
// implemented! Assuming EOF`.
func (user *hookedStream) warn(format string, args ...any) {
	if stack := user.ctx.CallStack(); len(stack) > 0 && stack[len(stack)-1].Function != "" {
		format = stack[len(stack)-1].Function + "(): " + format
	}
	user.ctx.Warn(format, args...)
}

func (user *hookedStream) updateEOF() {
	user.owner.SetEOF(user.rawEOF && len(user.unread) == 0)
}

func (user *hookedStream) RefreshEOF() error {
	value, found, err := user.call("stream_eof", nil)
	if err != nil {
		// **A throwing stream_eof is still reported as missing**, then the
		// exception goes on (bug41421): the engine's call failed either way.
		if _, thrown := php.AsThrown(err); thrown {
			user.warn("%s::stream_eof is not implemented! Assuming EOF", user.className())
		}
		user.rawEOF = true
		user.updateEOF()
		return err
	}
	if !found {
		user.warn("%s::stream_eof is not implemented! Assuming EOF", user.className())
		user.rawEOF = true
	} else {
		user.rawEOF = value.Deref().Bool()
	}
	user.updateEOF()
	return nil
}

func (user *hookedStream) Read(output []byte) (int, error) {
	if len(output) == 0 {
		return 0, nil
	}
	if len(user.unread) == 0 {
		request := 8192
		if user.owner.ChunkSize() > 0 {
			request = user.owner.ChunkSize()
		}
		if request < len(output) {
			request = len(output)
		}
		value, found, err := user.call("stream_read", []php.Value{php.Int(int64(request))})
		if err != nil {
			return 0, err
		}
		if !found {
			user.warn("%s::stream_read is not implemented!", user.className())
			return 0, ErrUserStreamOperation
		}
		value = value.Deref()
		if value.Kind() == php.KindFalse {
			return 0, ErrUserStreamOperation
		}
		read := value.String()
		if len(read) > request {
			user.warn(
				"%s::stream_read - read %d bytes more data than requested (%d read, %d max) - excess data will be lost",
				user.className(), len(read)-request, len(read), request,
			)
			read = read[:request]
		}
		user.unread = append(user.unread, read...)
		if err := user.RefreshEOF(); err != nil {
			return 0, err
		}
	}
	n := copy(output, user.unread)
	user.unread = user.unread[n:]
	user.logicalPosition += int64(n)
	user.updateEOF()
	if n == 0 && user.owner.AtEOF() {
		return 0, io.EOF
	}
	return n, nil
}

func (user *hookedStream) Write(text string) (int, error) {
	if len(user.unread) != 0 {
		if _, err := user.Seek(user.logicalPosition, io.SeekStart); err != nil {
			return 0, err
		}
	}
	// **php://temp moves to a file** when a write would reach its limit,
	// counted from the position rather than the size, and the file is a
	// stream of its own (memory_stream).
	if user.spillLimit >= 0 && user.spilled == nil && text != "" &&
		user.logicalPosition+int64(len(text)) >= user.spillLimit {
		user.spilled = user.ctx.NewResource("stream", nil)
	}
	value, found, err := user.call("stream_write", []php.Value{php.String(text)})
	if err != nil {
		return 0, err
	}
	if !found {
		user.warn("%s::stream_write is not implemented!", user.className())
		return 0, ErrUserStreamOperation
	}
	value = value.Deref()
	if value.Kind() == php.KindFalse {
		return 0, ErrUserStreamOperation
	}
	written := int(value.Int())
	if written < 0 {
		return 0, ErrUserStreamOperation
	}
	if written > len(text) {
		user.warn(
			"%s::stream_write wrote %d bytes more data than requested (%d written, %d max)",
			user.className(), written-len(text), written, len(text),
		)
		written = len(text)
	}
	user.logicalPosition += int64(written)
	// A write leaves EOF as it was: the engine sets it only from a read
	// and clears it only on a seek (user_stream_wrapper).
	return written, nil
}

func (user *hookedStream) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekCurrent {
		offset += user.logicalPosition
		whence = io.SeekStart
	}
	value, found, err := user.call("stream_seek", []php.Value{php.Int(offset), php.Int(int64(whence))})
	if err != nil {
		return 0, err
	}
	if !found {
		user.seekable = false
		return 0, ErrUserStreamOperation
	}
	if !value.Deref().Bool() {
		return 0, ErrUserStreamOperation
	}
	tell, found, err := user.call("stream_tell", nil)
	if err != nil {
		return 0, err
	}
	if !found {
		user.warn("%s::stream_tell is not implemented!", user.className())
		return 0, ErrUserStreamOperation
	}
	tell = tell.Deref()
	if tell.Kind() != php.KindInt {
		return 0, ErrUserStreamOperation
	}
	position := tell.Int()
	user.unread = nil
	user.rawEOF = false
	user.logicalPosition = position
	user.updateEOF()
	return position, nil
}

func (user *hookedStream) Close() error {
	if user.closed {
		return nil
	}
	user.closed = true
	user.inner.Close()
	user.spilled.Close()
	_, _, err := user.call("stream_close", nil)
	return err
}

func (user *hookedStream) Flush() (bool, error) {
	value, found, err := user.call("stream_flush", nil)
	if err != nil {
		return false, err
	}
	return found && value.Deref().Bool(), nil
}

func (user *hookedStream) Truncate(size int64) (bool, error) {
	value, found, err := user.call("stream_truncate", []php.Value{php.Int(size)})
	if err != nil {
		return false, err
	}
	if !found {
		user.warn("%s::stream_truncate is not implemented!", user.className())
		return false, nil
	}
	value = value.Deref()
	if value.Kind() != php.KindFalse && value.Kind() != php.KindTrue {
		user.warn("%s::stream_truncate did not return a boolean!", user.className())
		return false, nil
	}
	return value.Bool(), nil
}

func (user *hookedStream) StatValues() ([]int64, bool, error) {
	value, found, err := user.call("stream_stat", nil)
	if err != nil {
		return nil, false, err
	}
	if !found {
		user.warn("%s::stream_stat is not implemented!", user.className())
		return nil, false, nil
	}
	array := value.Deref().Array()
	if array == nil {
		return nil, false, nil
	}
	names := []string{
		"dev", "ino", "mode", "nlink", "uid", "gid", "rdev",
		"size", "atime", "mtime", "ctime", "blksize", "blocks",
	}
	values := []int64{0, 0, 0, 0, 0, 0, -1, 0, 0, 0, 0, -1, -1}
	for index, name := range names {
		if held, exists := array.Get(php.StringKey(name)); exists {
			values[index] = held.Deref().Int()
		}
	}
	return values, true, nil
}

func (user *hookedStream) Lock(operation int64) (bool, bool, error) {
	value, found, err := user.call("stream_lock", []php.Value{php.Int(operation)})
	if err != nil {
		return false, false, err
	}
	if !found {
		return false, false, nil
	}
	return value.Deref().Bool(), false, nil
}

func (user *hookedStream) SetOption(option, first, second int64) (bool, error) {
	value, found, err := user.call("stream_set_option", []php.Value{
		php.Int(option), php.Int(first), php.Int(second),
	})
	if err != nil {
		return false, err
	}
	return found && value.Deref().Bool(), nil
}

func (user *hookedStream) WrapperObject() *php.Object { return user.object }
func (user *hookedStream) Unread() int                { return len(user.unread) }
func (user *hookedStream) Position() int64            { return user.logicalPosition }
func (user *hookedStream) Seekable() bool             { return user.seekable }
func (user *hookedStream) SupportsLock() bool {
	return user.ctx.HasMethod(user.object.ClassName, "stream_lock")
}

// OpenHookedUserStream opens a stream through a wrapper class using only the
// Ctx's hooks, which is the UserStreamOpener compiled code installs. The
// wrapper is made as `new` makes it -- its `context` property set before the
// constructor runs -- and `stream_open` decides whether there is a stream.
func OpenHookedUserStream(ctx *php.Ctx, wrapper *php.StreamWrapper, path, mode string,
	options int64, context php.Value) (php.Value, error) {
	held, ok, err := openHooked(ctx, wrapper.ClassName, path, mode, options, context)
	if err != nil || !ok {
		if err == nil {
			return php.Bool(false), nil
		}
		return php.Undef(), err
	}
	return php.Res(ctx.NewResource("stream", held)), nil
}

func openHooked(ctx *php.Ctx, class, path, mode string, options int64,
	context php.Value) (*Stream, bool, error) {
	object, made, err := ctx.NewInstance(class)
	if err != nil {
		return nil, false, err
	}
	if !made {
		return nil, false, php.Unsupported("stream wrapper class %s", class)
	}
	if context.Deref().Kind() == php.KindResource {
		object.Set("context", context.Deref().Copy())
	} else {
		object.Set("context", php.Null())
	}
	user := &hookedStream{ctx: ctx, object: object, class: object.ClassName, seekable: true, spillLimit: -1}
	if ctx.HasMethod(object.ClassName, "__construct") {
		if _, _, err := user.call("__construct", nil); err != nil {
			return nil, false, err
		}
	}
	openedPath := php.NewRef(php.Null())
	opened, found, err := user.call("stream_open", []php.Value{
		php.String(path), php.String(mode), php.Int(options), php.MakeRef(openedPath),
	})
	if err != nil {
		return nil, false, err
	}
	if !found || !opened.Deref().Bool() {
		ctx.Warn("fopen(%s): Failed to open stream: \"%s::stream_open\" call failed",
			path, user.className())
		return nil, false, nil
	}
	held := NewUserStream(user, mode, path)
	user.owner = held
	return held, true, nil
}

// MemoryStreamURI reports a php://memory or php://temp path, and which. The
// engine matches `memory` whole but `temp` as a prefix: php://tempfoo is a
// temp stream.
func MemoryStreamURI(path string) (streamType string, ok bool) {
	lower := strings.ToLower(path)
	switch {
	case lower == "php://memory":
		return "MEMORY", true
	case strings.HasPrefix(lower, "php://temp"):
		return "TEMP", true
	}
	return "", false
}

// tempMaxMemory is php://temp's limit: `/maxmemory:N` read as strtol reads
// it -- `3abc` is 3 and `x` is 0 -- or 2 MiB without one.
func tempMaxMemory(path string) int64 {
	rest := path[len("php://temp"):]
	const option = "/maxmemory:"
	if len(rest) < len(option) || !strings.EqualFold(rest[:len(option)], option) {
		return 2 * 1024 * 1024
	}
	rest = rest[len(option):]
	negative := false
	if rest != "" && (rest[0] == '-' || rest[0] == '+') {
		negative = rest[0] == '-'
		rest = rest[1:]
	}
	var limit int64
	for i := 0; i < len(rest) && rest[i] >= '0' && rest[i] <= '9'; i++ {
		limit = limit*10 + int64(rest[i]-'0')
	}
	if negative {
		return -limit
	}
	return limit
}

// OpenMemoryStream opens php://memory or php://temp through the userland
// wrapper and presents it as the engine's own stream: `stream_type` MEMORY
// or TEMP, `wrapper_type` PHP, no wrapper_data, nothing unread, and the mode
// the engine gives it -- `rb` read-only, `a+b` appending, `w+b` otherwise.
func OpenMemoryStream(ctx *php.Ctx, path, mode string) (php.Value, error) {
	streamType, _ := MemoryStreamURI(path)
	limit := int64(-1)
	if streamType == "TEMP" {
		// The engine's message names the wrong argument; it is what it says.
		if limit = tempMaxMemory(path); limit < 0 {
			return php.Undef(), php.Raise("ValueError",
				"fopen(): Argument #2 ($mode) must be greater than or equal to 0")
		}
	}
	held, ok, err := openHooked(ctx, MemoryStreamWrapperClass, path, mode, 0, php.Null())
	if err != nil {
		return php.Undef(), err
	}
	if !ok {
		return php.Bool(false), nil
	}
	shown := "w+b"
	switch {
	case !strings.ContainsAny(mode, "wa+"):
		shown = "rb"
	case strings.Contains(mode, "a"):
		shown = "a+b"
	}
	held.PresentAsEngine("PHP", streamType, shown)
	opened := ctx.NewResource("stream", held)
	// **php://temp is two streams to the engine** -- the temp stream and the
	// memory stream inside it, made after it -- so it uses two resource
	// numbers where php://memory uses one (bug70398).
	if streamType == "TEMP" {
		user := held.User().(*hookedStream)
		user.inner = ctx.NewResource("stream", nil)
		user.spillLimit = limit
	}
	return php.Res(opened), nil
}
