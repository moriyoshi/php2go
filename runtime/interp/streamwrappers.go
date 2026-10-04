package interp

import (
	"io"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
	"github.com/moriyoshi/php2go/runtime/stdlib"
)

var builtinStreamWrapperNames = []string{"php", "file", "glob", "data", "http", "ftp"}

// errUserStreamOperation is stdlib's sentinel, aliased here because the
// wrapper methods that return it are on this side of the interface.
var errUserStreamOperation = stdlib.ErrUserStreamOperation

// userStream is the state PHP's streams layer keeps around an instance of a
// registered wrapper class. The wrapper callbacks operate at the raw stdlib.Stream
// position; logicalPosition and unread model the streams layer's read-ahead
// buffer, which is why fread($stdlib.Stream, 2) invokes stream_read(8192).
type userStream struct {
	in     *Interp
	ctx    *php.Ctx
	unit   *Unit
	object *php.Object
	class  *Class
	depth  int
	owner  *stdlib.Stream

	unread          []byte
	rawEOF          bool
	logicalPosition int64
	rawPosition     int64
	seekable        bool
	closed          bool
}

type userDirectory struct {
	in     *Interp
	ctx    *php.Ctx
	unit   *Unit
	object *php.Object
	class  *Class
	depth  int
	closed bool
}

func (directory *userDirectory) call(name string, args []php.Value) (php.Value, bool, error) {
	body, declaring, found := directory.unit.resolveMethod(directory.class, name)
	if !found {
		return php.Undef(), false, nil
	}
	value, err := directory.in.invokeDriven(
		directory.ctx, directory.unit, body, directory.object, declaring,
		directory.class, args, directory.depth,
	)
	return value, true, err
}

func (directory *userDirectory) className() string {
	return php.DisplayName(directory.class.Name)
}

func (directory *userDirectory) read() (string, bool, error) {
	value, found, err := directory.call("dir_readdir", nil)
	if err != nil {
		return "", false, err
	}
	if !found {
		directory.ctx.Warn("%s::dir_readdir is not implemented!", directory.className())
		return "", false, nil
	}
	value = value.Deref()
	if value.Kind() == php.KindFalse {
		return "", false, nil
	}
	return value.String(), true, nil
}

func (directory *userDirectory) rewind() error {
	value, found, err := directory.call("dir_rewinddir", nil)
	if err != nil {
		return err
	}
	if !found {
		directory.ctx.Warn("%s::dir_rewinddir is not implemented!", directory.className())
		return nil
	}
	if value.Deref().Kind() == php.KindFalse {
		return errUserStreamOperation
	}
	return nil
}

func (directory *userDirectory) close() error {
	if directory.closed {
		return nil
	}
	directory.closed = true
	_, _, err := directory.call("dir_closedir", nil)
	return err
}

func (user *userStream) call(name string, args []php.Value) (php.Value, bool, error) {
	body, declaring, found := user.unit.resolveMethod(user.class, name)
	if !found {
		return php.Undef(), false, nil
	}
	value, err := user.in.invokeDriven(
		user.ctx, user.unit, body, user.object, declaring, user.class, args, user.depth,
	)
	return value, true, err
}

func (user *userStream) className() string {
	return php.DisplayName(user.class.Name)
}

func (user *userStream) updateEOF() {
	user.owner.SetEOF(user.rawEOF && len(user.unread) == 0)
}

func (user *userStream) refreshEOF() error {
	value, found, err := user.call("stream_eof", nil)
	if err != nil {
		user.rawEOF = true
		user.updateEOF()
		return err
	}
	if !found {
		user.ctx.Warn("%s::stream_eof is not implemented! Assuming EOF", user.className())
		user.rawEOF = true
	} else {
		user.rawEOF = value.Deref().Bool()
	}
	user.updateEOF()
	return nil
}

func (user *userStream) read(output []byte) (int, error) {
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
			user.ctx.Warn("%s::stream_read is not implemented!", user.className())
			return 0, errUserStreamOperation
		}
		value = value.Deref()
		if value.Kind() == php.KindFalse {
			return 0, errUserStreamOperation
		}
		read := value.String()
		if len(read) > request {
			user.ctx.Warn(
				"%s::stream_read - read %d bytes more data than requested (%d read, %d max) - excess data will be lost",
				user.className(), len(read)-request, len(read), request,
			)
			read = read[:request]
		}
		user.unread = append(user.unread, read...)
		user.rawPosition += int64(len(read))
		if err := user.refreshEOF(); err != nil {
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

func (user *userStream) write(text string) (int, error) {
	if len(user.unread) != 0 {
		if _, err := user.seek(user.logicalPosition, io.SeekStart); err != nil {
			return 0, err
		}
	}
	value, found, err := user.call("stream_write", []php.Value{php.String(text)})
	if err != nil {
		return 0, err
	}
	if !found {
		user.ctx.Warn("%s::stream_write is not implemented!", user.className())
		return 0, errUserStreamOperation
	}
	value = value.Deref()
	if value.Kind() == php.KindFalse {
		return 0, errUserStreamOperation
	}
	written := int(value.Int())
	if written < 0 {
		return 0, errUserStreamOperation
	}
	if written > len(text) {
		user.ctx.Warn(
			"%s::stream_write wrote %d bytes more data than requested (%d written, %d max)",
			user.className(), written-len(text), written, len(text),
		)
		written = len(text)
	}
	user.logicalPosition += int64(written)
	user.rawPosition = user.logicalPosition
	// A write leaves EOF as it was: the engine sets it only from a read
	// and clears it only on a seek (user_stream_wrapper).
	return written, nil
}

func (user *userStream) seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekCurrent {
		offset += user.logicalPosition
		whence = io.SeekStart
	}
	value, found, err := user.call("stream_seek", []php.Value{
		php.Int(offset), php.Int(int64(whence)),
	})
	if err != nil {
		return 0, err
	}
	if !found {
		user.seekable = false
		return 0, errUserStreamOperation
	}
	if !value.Deref().Bool() {
		return 0, errUserStreamOperation
	}
	tell, found, err := user.call("stream_tell", nil)
	if err != nil {
		return 0, err
	}
	if !found {
		user.ctx.Warn("%s::stream_tell is not implemented!", user.className())
		return 0, errUserStreamOperation
	}
	tell = tell.Deref()
	if tell.Kind() != php.KindInt {
		return 0, errUserStreamOperation
	}
	position := tell.Int()
	user.unread = nil
	user.rawEOF = false
	user.logicalPosition = position
	user.rawPosition = position
	user.updateEOF()
	return position, nil
}

func (user *userStream) close() error {
	if user.closed {
		return nil
	}
	user.closed = true
	_, _, err := user.call("stream_close", nil)
	return err
}

func (user *userStream) flush() (bool, error) {
	value, found, err := user.call("stream_flush", nil)
	if err != nil {
		return false, err
	}
	return found && value.Deref().Bool(), nil
}

func (user *userStream) truncate(size int64) (bool, error) {
	value, found, err := user.call("stream_truncate", []php.Value{php.Int(size)})
	if err != nil {
		return false, err
	}
	if !found {
		user.ctx.Warn("%s::stream_truncate is not implemented!", user.className())
		return false, nil
	}
	value = value.Deref()
	if value.Kind() != php.KindFalse && value.Kind() != php.KindTrue {
		user.ctx.Warn("%s::stream_truncate did not return a boolean!", user.className())
		return false, nil
	}
	return value.Bool(), nil
}

func (user *userStream) statValues() ([]int64, bool, error) {
	value, found, err := user.call("stream_stat", nil)
	if err != nil {
		return nil, false, err
	}
	if !found {
		user.ctx.Warn("%s::stream_stat is not implemented!", user.className())
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

func (user *userStream) lock(operation int64) (bool, bool, error) {
	value, found, err := user.call("stream_lock", []php.Value{php.Int(operation)})
	if err != nil {
		return false, false, err
	}
	if !found {
		return false, false, nil
	}
	return value.Deref().Bool(), false, nil
}

func (user *userStream) setOption(option, first, second int64) (bool, error) {
	value, found, err := user.call("stream_set_option", []php.Value{
		php.Int(option), php.Int(first), php.Int(second),
	})
	if err != nil {
		return false, err
	}
	return found && value.Deref().Bool(), nil
}

func (in *Interp) openUserStream(ctx *php.Ctx, unit *Unit,
	registration *php.StreamWrapper, path, mode string, options int64,
	context php.Value, depth int,
) (php.Value, bool, error) {
	object, err := in.instantiateUserWrapper(ctx, unit, registration, context, depth)
	if err != nil {
		return php.Undef(), true, err
	}
	user := &userStream{
		in: in, ctx: ctx, unit: unit, object: object,
		class: registration.Class.(*Class), depth: depth, seekable: true,
	}
	openedPath := php.NewRef(php.Undef())
	opened, found, err := user.call("stream_open", []php.Value{
		php.String(path), php.String(mode), php.Int(options), php.MakeRef(openedPath),
	})
	if err != nil {
		return php.Undef(), true, err
	}
	if !found || !opened.Deref().Bool() {
		ctx.Warn(
			"fopen(%s): Failed to open stream: \"%s::stream_open\" call failed",
			path, user.className(),
		)
		return php.Bool(false), true, nil
	}
	held := stdlib.NewUserStream(user, mode, path)
	user.owner = held
	return php.Res(ctx.NewResource("stream", held)), true, nil
}

func (in *Interp) instantiateUserWrapper(ctx *php.Ctx, unit *Unit,
	registration *php.StreamWrapper, context php.Value, depth int,
) (*php.Object, error) {
	object := unit.instantiate(registration.Class.(*Class))
	if context.Deref().Kind() == php.KindResource {
		object.Set("context", context.Deref().Copy())
	} else {
		object.Set("context", php.Null())
	}
	if constructor, declaring, found := unit.resolveMethod(registration.Class.(*Class), "__construct"); found {
		if _, err := in.invokeDriven(
			ctx, unit, constructor, object, declaring, registration.Class.(*Class), nil, depth,
		); err != nil {
			return nil, err
		}
	}
	return object, nil
}

func (in *Interp) openUserDirectory(ctx *php.Ctx, unit *Unit,
	registration *php.StreamWrapper, path string, options int64,
	context php.Value, depth int,
) (*directoryStream, bool, error) {
	object, err := in.instantiateUserWrapper(ctx, unit, registration, context, depth)
	if err != nil {
		return nil, false, err
	}
	directory := &userDirectory{
		in: in, ctx: ctx, unit: unit, object: object,
		class: registration.Class.(*Class), depth: depth,
	}
	opened, found, err := directory.call("dir_opendir", []php.Value{
		php.String(path), php.Int(options),
	})
	if err != nil {
		return nil, false, err
	}
	if !found || !opened.Deref().Bool() {
		return nil, false, nil
	}
	return &directoryStream{user: directory}, true, nil
}

// The wrapper registry moved to the Ctx (see runtime/php/streamregistry.go),
// because `fopen` has to consult it and an emitted program has to be able to
// `fopen`. What stays on this side is resolving the class a registration names,
// which needs the class table.

func validStreamWrapperProtocol(protocol string) bool {
	for index := 0; index < len(protocol); index++ {
		char := protocol[index]
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '+' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func streamWrapperDiagnosticProtocol(protocol string) string {
	if nul := strings.IndexByte(protocol, 0); nul >= 0 {
		return protocol[:nul]
	}
	return protocol
}

func (in *Interp) streamWrapperRegistryBuiltin(ctx *php.Ctx, unit *Unit,
	name string, args []php.Value, depth int,
) (php.Value, bool, error) {
	argument := func(index int) php.Value {
		if index < len(args) {
			return args[index].Deref()
		}
		return php.Undef()
	}
	switch name {
	case "stream_wrapper_register", "stream_register_wrapper":
		protocol := argument(0).String()
		className := argument(1).String()
		class, found, err := in.resolveClass(ctx, unit, className, depth)
		if err != nil {
			return php.Undef(), true, err
		}
		if !found {
			return php.Undef(), true, Raise(
				"TypeError",
				"%s(): Argument #2 ($class) must be a valid class name, %s given",
				name, className,
			)
		}
		factory := ctx.NewStreamFactory()
		if _, exists := ctx.StreamWrapperFor(protocol); exists {
			factory.Close()
			ctx.Warn("%s(): Protocol %s:// is already defined.", name,
				streamWrapperDiagnosticProtocol(protocol))
			return php.Bool(false), true, nil
		}
		if !validStreamWrapperProtocol(protocol) {
			factory.Close()
			ctx.Warn(
				"%s(): Invalid protocol scheme specified. Unable to register wrapper class %s to %s://",
				name, class.Name, streamWrapperDiagnosticProtocol(protocol),
			)
			return php.Bool(false), true, nil
		}
		flags := int64(0)
		if len(args) > 2 {
			flags = argument(2).Int()
		}
		ctx.RegisterStreamWrapper(protocol, &php.StreamWrapper{
			Class: class, ClassName: class.Name, Flags: flags, Factory: factory,
		})
		return php.Bool(true), true, nil

	case "stream_wrapper_unregister":
		protocol := argument(0).String()
		if !ctx.UnregisterStreamWrapper(protocol) {
			ctx.Warn("stream_wrapper_unregister(): Unable to unregister protocol %s://",
				streamWrapperDiagnosticProtocol(protocol))
			return php.Bool(false), true, nil
		}
		return php.Bool(true), true, nil

	case "stream_wrapper_restore":
		protocol := argument(0).String()
		builtin := false
		for _, candidate := range builtinStreamWrapperNames {
			if candidate == protocol {
				builtin = true
				break
			}
		}
		if !builtin {
			ctx.Warn("stream_wrapper_restore(): %s:// never existed, nothing to restore",
				streamWrapperDiagnosticProtocol(protocol))
			return php.Bool(false), true, nil
		}
		if held, found := ctx.StreamWrapperFor(protocol); found && held.Builtin {
			ctx.Notice("stream_wrapper_restore(): %s:// was never changed, nothing to restore",
				streamWrapperDiagnosticProtocol(protocol))
			return php.Bool(true), true, nil
		}
		ctx.UnregisterStreamWrapper(protocol)
		ctx.RegisterStreamWrapper(protocol, &php.StreamWrapper{Builtin: true})
		return php.Bool(true), true, nil

	case "php2go\\internal\\stream_wrapper_registry":
		if argument(0).String() != "list" {
			return php.Undef(), true, Raise(
				"Error", "php2go internal stream_wrapper_registry received an invalid operation")
		}
		result := php.NewArray()
		for _, wrapper := range ctx.StreamWrapperSchemes() {
			result.Append(php.String(wrapper))
		}
		return php.Arr(result), true, nil
	}
	return php.Undef(), false, nil
}

// The stdlib.UserStream implementation. The methods themselves are unexported
// here because they are the wrapper's own vocabulary; these are the names the
// moved Stream type asks for, and the four readers are facts about the wrapper
// that only it can answer.

func (user *userStream) Read(output []byte) (int, error) { return user.read(output) }

func (user *userStream) Write(text string) (int, error) { return user.write(text) }

func (user *userStream) Seek(offset int64, whence int) (int64, error) {
	return user.seek(offset, whence)
}

func (user *userStream) Close() error { return user.close() }

func (user *userStream) Flush() (bool, error) { return user.flush() }

func (user *userStream) Truncate(size int64) (bool, error) { return user.truncate(size) }

func (user *userStream) StatValues() ([]int64, bool, error) { return user.statValues() }

func (user *userStream) Lock(operation int64) (bool, bool, error) { return user.lock(operation) }

func (user *userStream) SetOption(option, first, second int64) (bool, error) {
	return user.setOption(option, first, second)
}

func (user *userStream) RefreshEOF() error { return user.refreshEOF() }

func (user *userStream) WrapperObject() *php.Object { return user.object }

func (user *userStream) Unread() int { return len(user.unread) }

func (user *userStream) Position() int64 { return user.logicalPosition }

func (user *userStream) Seekable() bool { return user.seekable }

// SupportsLock is whether the wrapper declares stream_lock. It needs the class
// table, which is why the whole interface exists.
func (user *userStream) SupportsLock() bool {
	_, _, found := user.unit.resolveMethod(user.class, "stream_lock")
	return found
}
