package interp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/moriyoshi/php2go/runtime/libc"
	"github.com/moriyoshi/php2go/runtime/php"
	"github.com/moriyoshi/php2go/runtime/stdlib"
)

// What opening a stream needs that `runtime/stdlib` cannot have: a class table
// for a user-space wrapper, a loader for an include path, and the directory
// handles -- a different resource that happened to share this file with the
// streams before they moved. See `runtime/stdlib/stream.go`.

// directoryObject is the resource a Directory instance holds, and is part of
// why the directory handles stayed on this side: the object is a class
// instance.
type directoryObject struct {
	Handle *php.Resource
}

func (in *Interp) socketClientResult(ctx *php.Ctx, address string, flags int,
	timeout float64, context *stdlib.StreamContext) php.Value {
	if flags&1 != 0 {
		if lease, found := in.persistentSocketLeases[address]; found {
			if lease.Handle != nil && !lease.Handle.Closed() {
				return stdlib.SocketCreateResult(true, php.Res(lease.Handle), 0, "")
			}
			delete(in.persistentSocketLeases, address)
		}
		entry, errno, message := in.persistentSockets.Acquire(
			address,
			func() (*stdlib.Stream, int64, string) {
				return stdlib.CreateSocketClient(address, flags, timeout, context)
			},
		)
		if entry == nil {
			return stdlib.SocketCreateResult(false, php.Bool(false), errno, message)
		}
		handle := ctx.NewResource("stream", entry.Stream)
		in.persistentSocketLeases[address] = stdlib.PersistentSocketLease{
			Entry: entry, Handle: handle,
		}
		return stdlib.SocketCreateResult(true, php.Res(handle), 0, "")
	}
	held, errno, message := stdlib.CreateSocketClient(address, flags, timeout, context)
	if held == nil {
		return stdlib.SocketCreateResult(false, php.Bool(false), errno, message)
	}
	return stdlib.SocketCreateResult(
		true,
		php.Res(ctx.NewResource("stream", held)),
		0,
		"",
	)
}

// streamBuiltin implements the file functions. They need the Ctx for the
// request's resource ids, which is why they are not in the plain builtin map.
func (in *Interp) streamBuiltin(ctx *php.Ctx, unit *Unit, name string,
	args []php.Value, depth int) (php.Value, bool, error) {
	// The stream functions live in runtime/stdlib now, and the two things they
	// cannot do for themselves -- call a PHP callable, and open through a
	// wrapper the program registered -- come back here through the hooks.
	// Installed on the way in rather than at the one arm that needed them,
	// because `fopen` reaches the opener without going near a callback.
	in.installCallValue(ctx, unit, depth)
	if value, handled, err := in.streamWrapperRegistryBuiltin(
		ctx, unit, name, args, depth,
	); handled {
		return value, true, err
	}
	argument := func(i int) php.Value {
		if i < len(args) {
			return args[i].Deref()
		}
		return php.Undef()
	}
	switch name {
	case "getimagesize", "getimagesizefromstring":
		value, err := in.getImageSize(ctx, name, args)
		return value, true, err

	case "stream_filter_register":
		filterName := argument(0).String()
		if filterName == "" {
			return php.Undef(), true, Raise(
				"ValueError",
				"stream_filter_register(): Argument #1 ($filter_name) must be a non-empty string",
			)
		}
		className := argument(1).String()
		if className == "" {
			return php.Undef(), true, Raise(
				"ValueError",
				"stream_filter_register(): Argument #2 ($class) must be a non-empty string",
			)
		}
		if !ctx.RegisterStreamFilter(filterName, className) {
			return php.Bool(false), true, nil
		}
		return php.Bool(true), true, nil

	case "stream_filter_append":
		value, err := in.attachStreamFilter(ctx, unit, name, args, true, depth)
		return value, true, err

	case "stream_filter_prepend":
		value, err := in.attachStreamFilter(ctx, unit, name, args, false, depth)
		return value, true, err

	case "stream_filter_remove":
		value, err := in.removeStreamFilter(ctx, argument(0), name)
		return value, true, err

	case "stream_bucket_make_writeable":
		brigade, err := stdlib.BrigadeResource(argument(0), name)
		if err != nil {
			return php.Undef(), true, err
		}
		bucket := brigade.Pop()
		if bucket == nil {
			return php.Null(), true, nil
		}
		value, err := streamBucketValue(ctx, unit, bucket)
		return value, true, err

	case "stream_bucket_append", "stream_bucket_prepend":
		brigade, err := stdlib.BrigadeResource(argument(0), name)
		if err != nil {
			return php.Undef(), true, err
		}
		bucket, err := stdlib.BucketFromObject(argument(1).Object(), name)
		if err != nil {
			return php.Undef(), true, err
		}
		brigade.Attach(bucket, name == "stream_bucket_append")
		return php.Null(), true, nil

	case "stream_bucket_new":
		_, _, ok := stdlib.AsStream(argument(0))
		if !ok {
			return php.Undef(), true, Raise(
				"TypeError", "stream_bucket_new(): supplied resource is not a valid stream resource")
		}
		value, err := streamBucketValue(ctx, unit, &stdlib.StreamBucket{
			Data: argument(1).String(),
		})
		return value, true, err

	// The file functions moved to `runtime/stdlib` with the stream type. They
	// are still dispatched here, because this switch is the path they were on
	// and `verifyBuiltinArgs` runs ahead of it.
	case "fclose", "feof", "fread", "fgets", "fwrite", "fputs", "fseek", "ftell", "rewind":
		fn, ok := php.LookupBuiltin(name)
		if !ok {
			return php.Undef(), false, nil
		}
		value, err := fn(ctx, args)
		return value, true, err

	case "stream_get_filters":
		result := php.NewArray()
		for _, filterName := range streamFilterNames(ctx) {
			result.Append(php.String(filterName))
		}
		return php.Arr(result), true, nil

	case "popen":
		command := argument(0).String()
		if strings.IndexByte(command, 0) >= 0 {
			return php.Undef(), true, Raise(
				"ValueError", "popen(): Argument #1 ($command) must not contain any null bytes")
		}
		mode := argument(1).String()
		nativeMode := mode
		if runtime.GOOS != "windows" {
			if binary := strings.IndexByte(nativeMode, 'b'); binary >= 0 {
				nativeMode = nativeMode[:binary] + nativeMode[binary+1:]
			}
		}
		if len(nativeMode) > 2 ||
			(len(nativeMode) == 1 && nativeMode != "r" && nativeMode != "w") ||
			(len(nativeMode) == 2 && nativeMode != "rb" && nativeMode != "wb") {
			return php.Undef(), true, Raise(
				"ValueError",
				"popen(): Argument #2 ($mode) must be one of \"r\", \"rb\", \"w\", or \"wb\"",
			)
		}
		pipe, errorNumber, available := libc.OpenPipe(command, nativeMode)
		if !available {
			return php.Undef(), true, &UnsupportedError{Opcode: "libc popen without cgo"}
		}
		if pipe == nil {
			_, message := stdlib.SocketErrno(syscall.Errno(errorNumber))
			ctx.Warn("popen(%s,%s): %s", command, nativeMode, message)
			return php.Bool(false), true, nil
		}
		held := stdlib.NewPipeStream(pipe, mode)
		if in.processStreams == nil {
			in.processStreams = map[*stdlib.Stream]struct{}{}
		}
		in.processStreams[held] = struct{}{}
		return php.Res(ctx.NewResource("stream", held)), true, nil

	case "pclose":
		handle := argument(0).Resource()
		if handle == nil || handle.Closed() {
			return php.Undef(), true, Raise(
				"TypeError", "pclose(): supplied resource is not a valid stream resource")
		}
		switch held := handle.Payload.(type) {
		case *stdlib.Stream:
			status, err := held.CloseStatus()
			var unsupported *UnsupportedError
			if errors.As(err, &unsupported) {
				return php.Undef(), true, err
			}
			handle.Close()
			return php.Int(int64(status)), true, nil
		case *directoryStream:
			held.close()
			handle.Close()
			return php.Int(0), true, nil
		default:
			return php.Undef(), true, Raise(
				"TypeError", "pclose(): supplied resource is not a valid stream resource")
		}

	case "fopen":
		// The body moved to runtime/stdlib with the stream type and the wrapper
		// registry; opening through a *user-registered* wrapper is the one part
		// that needs a class table, and it comes back here through the hook.
		fn, _ := php.LookupBuiltin("fopen")
		value, err := fn(ctx, args)
		return value, true, err

	case "stream_get_meta_data":
		if held, _, ok := stdlib.AsStream(argument(0)); ok && held.User() != nil {
			if err := held.User().RefreshEOF(); err != nil {
				return php.Undef(), true, err
			}
		}
		value, err := streamMetadata(argument(0), "stream_get_meta_data")
		return value, true, err

	case "php2go\\internal\\stream_metadata":
		if held, _, ok := stdlib.AsStream(argument(0)); ok && held.User() != nil {
			if err := held.User().RefreshEOF(); err != nil {
				return php.Undef(), true, err
			}
		}
		value, err := streamMetadata(argument(0), argument(1).String())
		return value, true, err

	case "php2go\\internal\\glob":
		paths, status, available, err := in.globPaths(
			argument(0).String(), argument(1).Int())
		if err != nil {
			return php.Undef(), true, err
		}
		if !available {
			return php.Undef(), true, &UnsupportedError{Opcode: "libc glob without cgo"}
		}
		result := php.NewArray()
		result.Append(php.Int(int64(status)))
		matches := php.NewArray()
		for _, path := range paths {
			matches.Append(php.String(path))
		}
		result.Append(php.Arr(matches))
		return php.Arr(result), true, nil

	case "php2go\\internal\\stream_select":
		var readSet, writeSet, exceptSet syscall.FdSet
		read, readMax, readOK := stdlib.StreamSelectEntries(argument(0), &readSet)
		write, writeMax, writeOK := stdlib.StreamSelectEntries(argument(1), &writeSet)
		except, exceptMax, exceptOK := stdlib.StreamSelectEntries(argument(2), &exceptSet)
		if !readOK || !writeOK || !exceptOK {
			return php.Undef(), true, Raise(
				"Error",
				"php2go internal stream_select received an invalid stream",
			)
		}
		maxFD := max(readMax, writeMax, exceptMax)
		immediate := false
		for _, entry := range read {
			immediate = immediate || entry.Stream.IsMemory()
		}
		for _, entry := range write {
			immediate = immediate || entry.Stream.IsMemory()
		}
		timeout := stdlib.StreamSelectTimeout(argument(3), argument(4), immediate)
		if _, err := syscall.Select(
			maxFD+1,
			&readSet,
			&writeSet,
			&exceptSet,
			timeout,
		); err != nil {
			ctx.Warn(
				"stream_select(): Unable to select: %s (max_fd=%d)",
				err.Error(),
				maxFD,
			)
			return php.Bool(false), true, nil
		}
		readResult, readReady := stdlib.StreamSelectResult(read, stdlib.StreamSelectRead, &readSet)
		writeResult, writeReady := stdlib.StreamSelectResult(write, stdlib.StreamSelectWrite, &writeSet)
		exceptResult, exceptReady := stdlib.StreamSelectResult(except, stdlib.StreamSelectExcept, &exceptSet)
		result := php.NewArray()
		result.Append(php.Int(int64(readReady + writeReady + exceptReady)))
		result.Append(php.Arr(readResult))
		result.Append(php.Arr(writeResult))
		result.Append(php.Arr(exceptResult))
		return php.Arr(result), true, nil

	// The four socket operations that **create** a stdlib.Stream. They stay here
	// because building a `*stdlib.Stream` is this package's business; the five that
	// act on an existing one moved to `runtime/stdlib` behind the Stream
	// interface. Each is its own name either way -- what went is the string
	// selector, not the split.
	case "php2go\\internal\\stream_socket_server":
		var context *stdlib.StreamContext
		if argument(3).Kind() != php.KindNull && argument(3).Kind() != php.KindUndef {
			context, _ = stdlib.AsStreamContext(argument(3))
		}
		return stdlib.SocketServerResult(
			ctx,
			argument(0).String(),
			int(argument(1).Int()),
			context,
		), true, nil

	case "php2go\\internal\\stream_socket_client":
		var context *stdlib.StreamContext
		if argument(3).Kind() != php.KindNull && argument(3).Kind() != php.KindUndef {
			context, _ = stdlib.AsStreamContext(argument(3))
		}
		return in.socketClientResult(
			ctx,
			argument(0).String(),
			int(argument(1).Int()),
			argument(2).Float(),
			context,
		), true, nil

	case "php2go\\internal\\stream_socket_accept":
		held, _, _ := stdlib.AsStream(argument(0))
		return stdlib.SocketAcceptResult(ctx, held, argument(1).Float()), true, nil

	case "php2go\\internal\\stream_socket_pair":
		return stdlib.SocketPairResult(
			ctx,
			int(argument(0).Int()),
			int(argument(1).Int()),
			int(argument(2).Int()),
		), true, nil

	case "php2go\\internal\\path_predicate":
		value, known := in.pathPredicate(ctx, argument(0).String(), argument(1).String(), argument(2).String())
		if !known {
			return php.Undef(), true, Raise(
				"Error",
				"php2go internal path_predicate received an unknown predicate",
			)
		}
		return php.Bool(value), true, nil

	case "php2go\\internal\\read_link":
		target, err := in.readLink(argument(0).String())
		result := php.NewArray()
		result.Append(php.Bool(err == nil))
		if err != nil {
			var unsupported *UnsupportedError
			if errors.As(err, &unsupported) {
				return php.Undef(), true, err
			}
			_, message := stdlib.SocketErrno(err)
			result.Append(php.String(message))
		} else {
			result.Append(php.String(target))
		}
		return php.Arr(result), true, nil

	case "php2go\\internal\\path_stat":
		stat, err := in.pathStat(
			ctx,
			argument(0).String(),
			argument(1).Bool(),
			argument(2).Bool(),
			argument(3).String(),
		)
		if err != nil {
			_, message := stdlib.SocketErrno(err)
			return php.String(message), true, nil
		}
		result := php.NewArray()
		for _, value := range pathStatValues(stat) {
			result.Append(php.Int(value))
		}
		return php.Arr(result), true, nil

	case "php2go\\internal\\path_mutation":
		result, err := in.pathMutation(
			ctx,
			argument(0).String(),
			argument(1).String(),
			argument(2),
			argument(3).Bool(),
			argument(4).Int(),
			argument(5).Int(),
		)
		if err != nil {
			return php.Undef(), true, err
		}
		values := php.NewArray()
		values.Append(php.Bool(result.Success))
		values.Append(php.String(result.Stage))
		values.Append(php.String(result.Message))
		return php.Arr(values), true, nil

	case "php2go\\internal\\directory_operation":
		operation := argument(0).String()
		switch operation {
		case "open", "object":
			directory, errorNumber, message, err := in.openRegisteredDirectory(
				ctx, unit, argument(1).String(), argument(2).String(),
				argument(3), depth,
			)
			if err != nil {
				return php.Undef(), true, err
			}
			result := php.NewArray()
			result.Append(php.Bool(errorNumber == 0 && message == ""))
			result.Append(php.Int(errorNumber))
			result.Append(php.String(message))
			if errorNumber == 0 && message == "" {
				handle := ctx.NewResource("stream", directory)
				result.Append(php.Res(handle))
				if operation == "object" {
					object := unit.instantiateBuiltin("Directory")
					if object == nil {
						return php.Undef(), true, &UnsupportedError{
							Opcode: "Directory object without harvested class",
						}
					}
					object.Set("path", php.String(argument(1).String()))
					object.Set("handle", php.Res(handle))
					object.Internal = &directoryObject{Handle: handle}
					result.Append(php.Obj(object))
				}
			}
			return php.Arr(result), true, nil
		case "list":
			directory, errorNumber, message, err := in.openRegisteredDirectory(
				ctx, unit, argument(1).String(), argument(2).String(),
				argument(3), depth,
			)
			if err != nil {
				return php.Undef(), true, err
			}
			var names []string
			if errorNumber == 0 && message == "" {
				for {
					name, found, readErr := directory.read()
					if readErr != nil {
						_ = directory.close()
						return php.Undef(), true, readErr
					}
					if !found {
						break
					}
					names = append(names, name)
				}
				if closeErr := directory.close(); closeErr != nil {
					return php.Undef(), true, closeErr
				}
			}
			result := php.NewArray()
			result.Append(php.Bool(errorNumber == 0 && message == ""))
			result.Append(php.Int(errorNumber))
			result.Append(php.String(message))
			if errorNumber == 0 && message == "" {
				entries := php.NewArray()
				for _, name := range names {
					entries.Append(php.String(name))
				}
				result.Append(php.Arr(entries))
			}
			return php.Arr(result), true, nil
		case "read", "rewind", "close":
			held, handle, err := directoryHandle(argument(1), argument(2).String())
			if err != nil {
				return php.Undef(), true, err
			}
			switch operation {
			case "read":
				name, ok, err := held.read()
				if err != nil {
					return php.Undef(), true, err
				}
				if !ok {
					return php.Bool(false), true, nil
				}
				return php.String(name), true, nil
			case "rewind":
				if err := held.rewind(); err != nil && !errors.Is(err, errUserStreamOperation) {
					return php.Undef(), true, err
				}
				return php.Null(), true, nil
			default:
				if err := held.close(); err != nil {
					return php.Undef(), true, err
				}
				handle.Close()
				return php.Null(), true, nil
			}
		default:
			return php.Undef(), true, Raise(
				"Error", "php2go internal directory_operation received an unknown operation")
		}

	case "php2go\\internal\\path_topology":
		success, message, err := in.pathTopology(
			ctx, argument(0).String(), argument(1).String(),
			argument(2).Int(), argument(3).Bool(), argument(4).String())
		if err != nil {
			return php.Undef(), true, err
		}
		result := php.NewArray()
		result.Append(php.Bool(success))
		result.Append(php.String(message))
		return php.Arr(result), true, nil

	case "php2go\\internal\\path_relation":
		success, message, detail, warning, err := in.pathRelation(
			ctx, argument(0).String(), argument(1).String(), argument(2).String())
		if err != nil {
			return php.Undef(), true, err
		}
		result := php.NewArray()
		result.Append(php.Bool(success))
		result.Append(php.String(message))
		result.Append(php.String(detail))
		result.Append(php.String(warning))
		return php.Arr(result), true, nil

	case "php2go\\internal\\process_umask":
		oldMask, available := libc.Umask(argument(1).Int(), argument(0).Bool())
		if !available {
			return php.Undef(), true, &UnsupportedError{Opcode: "libc umask without cgo"}
		}
		if in.originalUmask == nil {
			in.originalUmask = new(int)
			*in.originalUmask = oldMask
		}
		return php.Int(int64(oldMask)), true, nil

	case "php2go\\internal\\copy_path":
		success, kind, message, err := in.copyPath(
			ctx, argument(0).String(), argument(1).String())
		if err != nil {
			return php.Undef(), true, err
		}
		result := php.NewArray()
		result.Append(php.Bool(success))
		result.Append(php.String(kind))
		result.Append(php.String(message))
		return php.Arr(result), true, nil

	case "php2go\\internal\\temporary_file":
		operation := argument(0).String()
		file, path, fallback, message, err := in.openTemporaryFile(
			argument(1).String(), argument(2).String(), argument(3).String())
		if err != nil {
			return php.Undef(), true, err
		}
		if operation == "name" {
			result := php.NewArray()
			result.Append(php.Bool(file != nil))
			result.Append(php.String(path))
			result.Append(php.Bool(fallback))
			result.Append(php.String(message))
			if file != nil {
				_ = file.Close()
			}
			return php.Arr(result), true, nil
		}
		if operation != "stream" {
			return php.Undef(), true, Raise(
				"Error", "php2go internal temporary_file received an unknown operation")
		}
		if file == nil {
			return php.Bool(false), true, nil
		}
		held := stdlib.NewTemporaryStream(file, path)
		if in.temporaryStreams == nil {
			in.temporaryStreams = map[*stdlib.Stream]struct{}{}
		}
		in.temporaryStreams[held] = struct{}{}
		return php.Res(ctx.NewResource("stream", held)), true, nil

	case "php2go\\internal\\path_space":
		available, total, err := in.pathSpace(argument(0).String())
		if err != nil {
			_, message := stdlib.SocketErrno(err)
			return php.String(message), true, nil
		}
		result := php.NewArray()
		result.Append(php.Float(available))
		result.Append(php.Float(total))
		return php.Arr(result), true, nil

	case "php2go\\internal\\resolve_include_path":
		callerFile, _ := stdlib.LibraryCallerLocation(ctx)
		resolved, found := in.resolveIncludePath(
			ctx,
			argument(0).String(),
			unit.setting("include_path"),
			callerFile,
		)
		if !found {
			return php.Bool(false), true, nil
		}
		return php.String(resolved), true, nil

	case "php2go\\internal\\read_file":
		// The body is in runtime/stdlib so the linked library reaches it from
		// compiled code too; installing the loader is the one thing it needs
		// from here.
		in.installSourceReader(ctx)
		fn, _ := php.LookupBuiltin(name)
		value, err := fn(ctx, args)
		return value, true, err

	case "php2go\\internal\\append_file":
		return in.appendFile(ctx, argument(0).String(), argument(1).String())

	case "file_get_contents":
		// The body is in runtime/stdlib; installing the loader is the one thing
		// it needs from here.
		in.installSourceReader(ctx)
		fn, _ := php.LookupBuiltin(name)
		value, err := fn(ctx, args)
		return value, true, err

	case "settype":
		// The body is in runtime/stdlib so compiled code has it too; making a
		// stdClass is the one thing it needs from here, since handles are the
		// program's to number.
		ctx.SetObjectMaker(func(_ *php.Ctx, class string) *php.Object {
			return unit.born(php.NewObject(class, unit.handle()))
		})
		fn, _ := php.LookupBuiltin("settype")
		value, err := fn(ctx, args)
		return value, true, in.materialize(ctx, unit, err)
	}
	return php.Undef(), false, nil
}

// readFile is the shared read with this interpreter's loader installed. The
// three sources and their order are in runtime/stdlib, because the harvested
// library reaches them from compiled code too.
func (in *Interp) readFile(ctx *php.Ctx, path string) ([]byte, error) {
	in.installSourceReader(ctx)
	return stdlib.ReadFile(ctx, path)
}

// installSourceReader hands the loader's bytes to whatever asks. A loader that
// is not a SourceReader has nothing to add, and the hook stays uninstalled so
// the host answers.
func (in *Interp) installSourceReader(ctx *php.Ctx) {
	reader, ok := in.loader.(SourceReader)
	if !ok {
		ctx.SetSourceReader(nil)
		return
	}
	ctx.SetSourceReader(reader.ReadSource)
}

func (in *Interp) appendFile(ctx *php.Ctx, requested, message string) (php.Value, bool, error) {
	// A path the loader serves is read-only to the program; everything else
	// is the host write compiled code does too (runtime/stdlib AppendFile).
	// A wrapped URL (data:, php://, ...) is never a loader path: stdlib
	// answers it with the engine's own diagnostic.
	if local, usable, unknown := stdlib.LocalFilePath(requested, false); usable && unknown == "" && !stdlib.HasStreamScheme(requested) &&
		!strings.HasPrefix(strings.ToLower(requested), "data:") {
		if resolver, ok := in.loader.(PathResolver); ok {
			if _, _, handled := resolver.ResolvePath(local); handled {
				return stdlib.AppendFileResult(false, [2]string{"warning", fmt.Sprintf(
					"error_log(%s): Failed to open stream: Read-only file system",
					requested,
				)}), true, nil
			}
		}
	}
	value, err := stdlib.AppendFile(ctx, requested, message)
	return value, true, err
}

func (in *Interp) resolveIncludePath(
	ctx *php.Ctx,
	filename string,
	includePath string,
	callerFile string,
) (string, bool) {
	direct := filepath.IsAbs(filename) ||
		strings.HasPrefix(filename, "./") ||
		strings.HasPrefix(filename, "../") ||
		stdlib.HasStreamScheme(filename)
	type candidate struct {
		path        string
		includePath bool
	}
	var candidates []candidate
	if direct || includePath == "" {
		candidates = []candidate{{path: filename}}
	} else {
		for _, directory := range stdlib.SplitIncludePath(includePath) {
			candidates = append(candidates, candidate{
				path:        directory + string(os.PathSeparator) + filename,
				includePath: true,
			})
		}
	}
	for _, candidate := range candidates {
		if canonical, found := in.resolvePathCandidate(ctx, candidate.path, candidate.includePath); found {
			return canonical, true
		}
	}
	if !direct && includePath != "" && callerFile != "" {
		fallback := filepath.Join(filepath.Dir(callerFile), filename)
		if canonical, found := in.resolvePathCandidate(ctx, fallback, false); found {
			return canonical, true
		}
	}
	return "", false
}

func (in *Interp) resolvePathCandidate(
	ctx *php.Ctx,
	candidate string,
	fromIncludePath bool,
) (string, bool) {
	local, usable, unknown := stdlib.LocalFilePath(candidate, fromIncludePath)
	if unknown != "" {
		warnUnknownWrapper(ctx, "stream_resolve_include_path", unknown)
	}
	if !usable {
		return "", false
	}
	if resolver, ok := in.loader.(PathResolver); ok {
		canonical, found, handled := resolver.ResolvePath(local)
		if handled {
			return canonical, found
		}
	}
	absolute, err := filepath.Abs(local)
	if err != nil {
		return "", false
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	return canonical, err == nil
}

func (in *Interp) pathPredicate(ctx *php.Ctx, candidate, predicate, function string) (bool, bool) {
	local, usable, unknown := stdlib.LocalFilePath(candidate, false)
	if unknown != "" {
		warnUnknownWrapper(ctx, function, unknown)
	}
	if !usable {
		return false, true
	}
	if inspector, ok := in.loader.(PathInspector); ok {
		status, handled := inspector.InspectPath(local)
		if handled {
			return pathStatusPredicate(status, predicate)
		}
	}
	return pathStatusPredicate(hostPathStatus(local), predicate)
}

func (in *Interp) readLink(candidate string) (string, error) {
	if inspector, ok := in.loader.(PathInspector); ok {
		status, handled := inspector.InspectPath(candidate)
		if handled {
			if status.Link {
				return "", &UnsupportedError{Opcode: "readlink on a loader-owned symbolic link"}
			}
			if status.Exists {
				return "", syscall.EINVAL
			}
			return "", syscall.ENOENT
		}
	}
	target, errorNumber, available := libc.ReadLink(candidate)
	if !available {
		return "", &UnsupportedError{Opcode: "libc readlink without cgo"}
	}
	if errorNumber != 0 {
		return "", syscall.Errno(errorNumber)
	}
	return target, nil
}

func (in *Interp) pathStat(ctx *php.Ctx, candidate string, follow, wrappers bool, function string) (PathStat, error) {
	local := candidate
	if wrappers {
		var usable bool
		var unknown string
		local, usable, unknown = stdlib.LocalFilePath(candidate, false)
		if unknown != "" {
			warnUnknownWrapper(ctx, function, unknown)
		}
		if !usable {
			return PathStat{}, syscall.ENOENT
		}
	}
	if inspector, ok := in.loader.(PathStatInspector); ok {
		stat, found, handled := inspector.StatPath(local, follow)
		if handled {
			if !found {
				return PathStat{}, syscall.ENOENT
			}
			return stat, nil
		}
	}
	var information os.FileInfo
	var err error
	if follow {
		information, err = os.Stat(local)
	} else {
		information, err = os.Lstat(local)
	}
	if err != nil {
		return PathStat{}, err
	}
	raw, ok := information.Sys().(*syscall.Stat_t)
	if !ok {
		return PathStat{}, errors.New("stat metadata is unavailable")
	}
	return PathStat{
		Dev: int64(raw.Dev), Ino: int64(raw.Ino), Mode: int64(raw.Mode),
		NLink: int64(raw.Nlink), UID: int64(raw.Uid), GID: int64(raw.Gid),
		RDev: -1, Size: raw.Size, ATime: raw.Atim.Sec, MTime: raw.Mtim.Sec,
		CTime: raw.Ctim.Sec, BlockSize: -1, Blocks: -1,
	}, nil
}

func (in *Interp) pathMutation(
	ctx *php.Ctx,
	operation string,
	candidate string,
	identity php.Value,
	setTimes bool,
	modificationTime int64,
	accessTime int64,
) (stdlib.PathMutationResult, error) {
	lowerCandidate := strings.ToLower(candidate)
	if strings.HasPrefix(lowerCandidate, "data:") {
		if operation == "touch" && !setTimes {
			return stdlib.PathMutationResult{Success: true}, nil
		}
		return stdlib.PathMutationResult{Stage: "wrapper"}, nil
	}

	local, usable, unknown := stdlib.LocalFilePath(candidate, false)
	if unknown != "" {
		warnUnknownWrapper(ctx, operation, unknown)
		stage := "system"
		if operation == "touch" {
			stage = "create"
		}
		return stdlib.PathMutationFailure(stage, int(syscall.ENOENT)), nil
	}
	if !usable {
		return stdlib.PathMutationResult{Stage: "wrapper"}, nil
	}

	identifier := identity.Int()
	if identity.Kind() == php.KindString && operation != "chmod" && operation != "touch" {
		var found, available bool
		if operation == "chown" || operation == "lchown" {
			identifier, found, available = libc.UserID(identity.String())
			if !found && available {
				return stdlib.PathMutationResult{Stage: "user"}, nil
			}
		} else {
			identifier, found, available = libc.GroupID(identity.String())
			if !found && available {
				return stdlib.PathMutationResult{Stage: "group"}, nil
			}
		}
		if !available {
			return stdlib.PathMutationResult{}, &UnsupportedError{Opcode: "libc account database without cgo"}
		}
	}

	if resolver, ok := in.loader.(PathResolver); ok {
		_, found, handled := resolver.ResolvePath(local)
		if handled {
			stage := "system"
			if operation == "touch" {
				if found {
					stage = "utime"
				} else {
					stage = "create"
				}
			}
			return stdlib.PathMutationFailure(stage, int(syscall.EROFS)), nil
		}
	}

	var errorNumber int
	var available bool
	stage := "system"
	switch operation {
	case "chmod":
		errorNumber, available = libc.Chmod(local, identity.Int())
	case "chown", "lchown", "chgrp", "lchgrp":
		group := operation == "chgrp" || operation == "lchgrp"
		follow := operation == "chown" || operation == "chgrp"
		errorNumber, available = libc.Chown(local, identifier, group, follow)
	case "touch":
		var touchStage libc.TouchStage
		errorNumber, touchStage, available = libc.Touch(
			local, setTimes, modificationTime, accessTime)
		switch touchStage {
		case libc.TouchCreate:
			stage = "create"
		case libc.TouchUtime:
			stage = "utime"
		}
	default:
		return stdlib.PathMutationResult{}, Raise(
			"Error", "php2go internal path_mutation received an unknown operation")
	}
	if !available {
		return stdlib.PathMutationResult{}, &UnsupportedError{Opcode: "libc filesystem metadata without cgo"}
	}
	if errorNumber != 0 {
		return stdlib.PathMutationFailure(stage, errorNumber), nil
	}
	return stdlib.PathMutationResult{Success: true}, nil
}

func (in *Interp) pathSpace(candidate string) (float64, float64, error) {
	local := candidate
	if inspector, ok := in.loader.(PathInspector); ok {
		status, handled := inspector.InspectPath(local)
		if handled {
			if !status.Exists {
				return 0, 0, syscall.ENOENT
			}
			// Embedded files occupy the filesystem containing the generated
			// executable, even though they have no independently statfs-able path.
			executable, err := os.Executable()
			if err != nil {
				return 0, 0, err
			}
			local = executable
		}
	}
	var information syscall.Statfs_t
	if err := syscall.Statfs(local, &information); err != nil {
		return 0, 0, err
	}
	blockSize := float64(information.Bsize)
	return float64(information.Bavail) * blockSize,
		float64(information.Blocks) * blockSize, nil
}

func (in *Interp) directoryNames(
	ctx *php.Ctx, candidate, function string,
) ([]string, int64, string, error) {
	if strings.HasPrefix(strings.ToLower(candidate), "data:") {
		return nil, 0, "not implemented", nil
	}
	local, usable, unknown := stdlib.LocalFilePath(candidate, false)
	if unknown != "" {
		warnUnknownWrapper(ctx, function, unknown)
		return nil, int64(syscall.ENOENT), "No such file or directory", nil
	}
	if !usable {
		return nil, 0, "not implemented", nil
	}
	if reader, ok := in.loader.(DirectoryReader); ok {
		names, err, handled := reader.ReadDirectory(local)
		if handled {
			if err == nil {
				return names, 0, "", nil
			}
			errorNumber, message := stdlib.SocketErrno(err)
			return nil, errorNumber, message, nil
		}
	}
	names, errorNumber, available := libc.ReadDirectory(local)
	if !available {
		return nil, 0, "", &UnsupportedError{Opcode: "libc directory iteration without cgo"}
	}
	if errorNumber != 0 {
		_, message := stdlib.SocketErrno(syscall.Errno(errorNumber))
		return nil, int64(errorNumber), message, nil
	}
	return names, 0, "", nil
}

func (in *Interp) openDirectory(
	ctx *php.Ctx, candidate, function string,
) (*directoryStream, int64, string, error) {
	if strings.HasPrefix(strings.ToLower(candidate), "data:") {
		return nil, 0, "not implemented", nil
	}
	local, usable, unknown := stdlib.LocalFilePath(candidate, false)
	if unknown != "" {
		warnUnknownWrapper(ctx, function, unknown)
		return nil, int64(syscall.ENOENT), "No such file or directory", nil
	}
	if !usable {
		return nil, 0, "not implemented", nil
	}
	if reader, ok := in.loader.(DirectoryReader); ok {
		names, err, handled := reader.ReadDirectory(local)
		if handled {
			if err == nil {
				return &directoryStream{names: names}, 0, "", nil
			}
			errorNumber, message := stdlib.SocketErrno(err)
			return nil, errorNumber, message, nil
		}
	}
	native, errorNumber, available := libc.OpenDirectory(local)
	if !available {
		return nil, 0, "", &UnsupportedError{Opcode: "libc opendir without cgo"}
	}
	if errorNumber != 0 {
		_, message := stdlib.SocketErrno(syscall.Errno(errorNumber))
		return nil, int64(errorNumber), message, nil
	}
	return &directoryStream{native: native}, 0, "", nil
}

func (in *Interp) openRegisteredDirectory(ctx *php.Ctx, unit *Unit,
	candidate, function string, context php.Value, depth int,
) (*directoryStream, int64, string, error) {
	scheme, found := stdlib.StreamScheme(candidate)
	if !found {
		scheme = "file"
	}
	registration, registered := ctx.StreamWrapperFor(scheme)
	if !registered {
		warnUnknownWrapper(ctx, function, scheme)
		return nil, int64(syscall.ENOENT), "No such file or directory", nil
	}
	if registration.Builtin {
		return in.openDirectory(ctx, candidate, function)
	}
	directory, opened, err := in.openUserDirectory(
		ctx, unit, registration, candidate, 0, context, depth,
	)
	if err != nil {
		return nil, 0, "", err
	}
	if !opened {
		return nil, 0,
			fmt.Sprintf("\"%s::dir_opendir\" call failed", php.DisplayName(registration.Class.(*Class).Name)), nil
	}
	return directory, 0, "", nil
}

func (in *Interp) pathTopology(
	ctx *php.Ctx, operation, candidate string, mode int64, recursive bool, function string,
) (bool, string, error) {
	if strings.HasPrefix(strings.ToLower(candidate), "data:") {
		return false, "", nil
	}
	local, usable, unknown := stdlib.LocalFilePath(candidate, false)
	if unknown != "" {
		warnUnknownWrapper(ctx, function, unknown)
		return false, "No such file or directory", nil
	}
	if !usable {
		return false, "", nil
	}
	call := func(path string) (int, bool) {
		if operation == "mkdir" {
			return libc.Mkdir(path, mode)
		}
		return libc.Rmdir(path)
	}
	if candidate == "" {
		errorNumber, available := call(local)
		if !available {
			return false, "", &UnsupportedError{Opcode: "libc path topology without cgo"}
		}
		if errorNumber != 0 {
			_, message := stdlib.SocketErrno(syscall.Errno(errorNumber))
			return false, message, nil
		}
		return true, "", nil
	}
	if resolver, ok := in.loader.(PathResolver); ok {
		_, _, handled := resolver.ResolvePath(local)
		if handled {
			return false, "Read-only file system", nil
		}
	}
	if operation == "mkdir" && recursive {
		var missing []string
		for path := filepath.Clean(local); ; path = filepath.Dir(path) {
			if _, err := os.Stat(path); err == nil {
				break
			}
			missing = append(missing, path)
			parent := filepath.Dir(path)
			if parent == path {
				break
			}
		}
		if len(missing) == 0 {
			errorNumber, available := libc.Mkdir(local, mode)
			if !available {
				return false, "", &UnsupportedError{Opcode: "libc mkdir without cgo"}
			}
			if errorNumber != 0 {
				_, message := stdlib.SocketErrno(syscall.Errno(errorNumber))
				return false, message, nil
			}
			return true, "", nil
		}
		for index := len(missing) - 1; index >= 0; index-- {
			errorNumber, available := libc.Mkdir(missing[index], mode)
			if !available {
				return false, "", &UnsupportedError{Opcode: "libc mkdir without cgo"}
			}
			if errorNumber != 0 {
				_, message := stdlib.SocketErrno(syscall.Errno(errorNumber))
				return false, message, nil
			}
		}
		return true, "", nil
	}
	errorNumber, available := call(local)
	if !available {
		return false, "", &UnsupportedError{Opcode: "libc path topology without cgo"}
	}
	if errorNumber != 0 {
		_, message := stdlib.SocketErrno(syscall.Errno(errorNumber))
		return false, message, nil
	}
	return true, "", nil
}

func (in *Interp) pathRelation(
	ctx *php.Ctx, operation, target, linkPath string,
) (bool, string, string, string, error) {
	loaderOwns := func(candidate string) bool {
		if candidate == "" {
			return false
		}
		if strings.HasPrefix(strings.ToLower(candidate), "data:") {
			return false
		}
		if _, wrapped := stdlib.StreamScheme(candidate); wrapped {
			return false
		}
		if resolver, ok := in.loader.(PathResolver); ok {
			_, _, handled := resolver.ResolvePath(candidate)
			return handled
		}
		return false
	}
	if operation == "rename" {
		type wrapperPath struct {
			kind  string
			label string
			local string
		}
		classify := func(candidate string) wrapperPath {
			if strings.HasPrefix(strings.ToLower(candidate), "data:") {
				return wrapperPath{kind: "data", label: "RFC2397", local: candidate}
			}
			scheme, wrapped := stdlib.StreamScheme(candidate)
			if !wrapped {
				return wrapperPath{kind: "file", label: "plainfile", local: candidate}
			}
			switch strings.ToLower(scheme) {
			case "file":
				local, usable, _ := stdlib.LocalFilePath(candidate, false)
				if usable {
					return wrapperPath{kind: "file", label: "plainfile", local: local}
				}
				return wrapperPath{kind: "file", label: "plainfile", local: candidate}
			case "data":
				return wrapperPath{kind: "data", label: "RFC2397", local: candidate}
			case "php":
				return wrapperPath{kind: "php", label: "PHP", local: candidate}
			case "http":
				return wrapperPath{kind: "http", label: "HTTP", local: candidate}
			case "ftp":
				return wrapperPath{kind: "ftp", label: "FTP", local: candidate}
			default:
				warnUnknownWrapper(ctx, "rename", scheme)
				return wrapperPath{kind: "file", label: "plainfile", local: candidate}
			}
		}
		from := classify(target)
		if from.kind != "file" {
			return false, from.label + " wrapper does not support renaming",
				"function", "", nil
		}
		to := classify(linkPath)
		if to.kind != from.kind {
			return false, "Cannot rename a file across wrapper types",
				"function", "", nil
		}
		if loaderOwns(from.local) || loaderOwns(to.local) {
			return false, "Read-only file system", "", "", nil
		}
		errorNumber, sourceDirectory, warningNumber, available :=
			libc.Rename(from.local, to.local)
		if !available {
			return false, "", "", "", &UnsupportedError{Opcode: "libc rename without cgo"}
		}
		warning := ""
		if warningNumber != 0 {
			_, warning = stdlib.SocketErrno(syscall.Errno(warningNumber))
		}
		if errorNumber != 0 {
			_, message := stdlib.SocketErrno(syscall.Errno(errorNumber))
			detail := ""
			if sourceDirectory {
				detail = "directory"
			}
			return false, message, detail, warning, nil
		}
		return true, "", "", warning, nil
	}
	if loaderOwns(linkPath) || (operation == "link" && loaderOwns(target)) {
		return false, "Read-only file system", "", "", nil
	}
	var errorNumber int
	var available bool
	if operation == "link" {
		errorNumber, available = libc.Link(target, linkPath)
	} else {
		errorNumber, available = libc.Symlink(target, linkPath)
	}
	if !available {
		return false, "", "", "",
			&UnsupportedError{Opcode: "libc " + operation + " without cgo"}
	}
	if errorNumber != 0 {
		_, message := stdlib.SocketErrno(syscall.Errno(errorNumber))
		return false, message, "", "", nil
	}
	return true, "", "", "", nil
}

func (in *Interp) restoreProcessUmask() {
	if in.originalUmask == nil {
		return
	}
	_, _ = libc.Umask(int64(*in.originalUmask), true)
	in.originalUmask = nil
}

func (in *Interp) cleanupTemporaryStreams() {
	for held := range in.temporaryStreams {
		_ = held.Close()
	}
	clear(in.temporaryStreams)
}

func (in *Interp) cleanupProcessStreams() {
	for held := range in.processStreams {
		_ = held.Close()
	}
	clear(in.processStreams)
}

func (in *Interp) openTemporaryFile(
	directory, prefix, systemDirectory string,
) (*os.File, string, bool, string, error) {
	try := func(candidate string) (*os.File, string, int, bool) {
		if resolver, ok := in.loader.(PathResolver); ok {
			if _, _, handled := resolver.ResolvePath(candidate); handled {
				return nil, "", int(syscall.EROFS), true
			}
		}
		return libc.TemporaryFile(candidate, prefix)
	}

	candidate := directory
	fallback := false
	if candidate == "" {
		candidate = systemDirectory
	}
	file, path, errorNumber, available := try(candidate)
	if !available {
		return nil, "", false, "",
			&UnsupportedError{Opcode: "libc mkstemp without cgo"}
	}
	if file != nil {
		return file, path, false, "", nil
	}
	if directory != "" {
		fallback = true
		file, path, errorNumber, available = try(systemDirectory)
		if !available {
			return nil, "", fallback, "",
				&UnsupportedError{Opcode: "libc mkstemp without cgo"}
		}
		if file != nil {
			return file, path, fallback, "", nil
		}
	}
	_, message := stdlib.SocketErrno(syscall.Errno(errorNumber))
	return nil, "", fallback, message, nil
}

func (in *Interp) copyPath(
	ctx *php.Ctx, source, target string,
) (bool, string, string, error) {
	type copyPath struct {
		kind    string
		local   string
		unknown string
	}
	classify := func(candidate string) copyPath {
		if strings.HasPrefix(strings.ToLower(candidate), "data:") {
			return copyPath{kind: "data", local: candidate}
		}
		scheme, wrapped := stdlib.StreamScheme(candidate)
		if !wrapped {
			return copyPath{kind: "file", local: candidate}
		}
		if strings.EqualFold(scheme, "file") {
			local, usable, _ := stdlib.LocalFilePath(candidate, false)
			if usable {
				return copyPath{kind: "file", local: local}
			}
			return copyPath{kind: "file", local: candidate}
		}
		switch strings.ToLower(scheme) {
		case "data":
			return copyPath{kind: "data", local: candidate}
		case "php", "http", "ftp":
			return copyPath{kind: strings.ToLower(scheme), local: candidate}
		default:
			return copyPath{kind: "unknown", local: candidate, unknown: scheme}
		}
	}
	from := classify(source)
	to := classify(target)

	if from.unknown != "" {
		for range 3 {
			warnUnknownWrapper(ctx, "copy", from.unknown)
		}
		return false, "source", "No such file or directory", nil
	}

	type pathFact struct {
		exists    bool
		directory bool
		canonical string
		info      os.FileInfo
	}
	inspect := func(candidate copyPath) pathFact {
		if candidate.kind != "file" {
			return pathFact{}
		}
		if inspector, ok := in.loader.(PathInspector); ok {
			status, handled := inspector.InspectPath(candidate.local)
			if handled {
				canonical := candidate.local
				if resolver, ok := in.loader.(PathResolver); ok {
					if resolved, _, owned := resolver.ResolvePath(candidate.local); owned {
						canonical = resolved
					}
				}
				return pathFact{
					exists:    status.Exists || status.Dir,
					directory: status.Dir,
					canonical: canonical,
				}
			}
		}
		information, err := os.Stat(candidate.local)
		if err != nil {
			return pathFact{canonical: candidate.local}
		}
		return pathFact{
			exists: true, directory: information.IsDir(),
			canonical: candidate.local, info: information,
		}
	}
	fromFact := inspect(from)
	if fromFact.directory {
		return false, "function",
			"The first argument to copy() function cannot be a directory", nil
	}
	toFact := inspect(to)
	if toFact.directory {
		return false, "function",
			"The second argument to copy() function cannot be a directory", nil
	}
	if from.kind == "file" && to.kind == "file" {
		if fromFact.info != nil && toFact.info != nil &&
			os.SameFile(fromFact.info, toFact.info) {
			return false, "silent", "", nil
		}
		if fromFact.canonical != "" && fromFact.canonical == toFact.canonical &&
			(fromFact.exists || toFact.exists) {
			return false, "silent", "", nil
		}
	}

	var contents []byte
	var err error
	switch from.kind {
	case "data", "file":
		contents, err = in.readFile(ctx, from.local)
	default:
		return false, "source", "No such file or directory", nil
	}
	if err != nil {
		_, message := stdlib.SocketErrno(err)
		return false, "source", message, nil
	}

	if to.unknown != "" {
		warnUnknownWrapper(ctx, "copy", to.unknown)
		warnUnknownWrapper(ctx, "copy", to.unknown)
		return false, "target", "No such file or directory", nil
	}
	if to.kind != "file" {
		return false, "function", "Stream is not writable", nil
	}
	if resolver, ok := in.loader.(PathResolver); ok {
		_, _, handled := resolver.ResolvePath(to.local)
		if handled {
			return false, "target", "Read-only file system", nil
		}
	}
	destination, err := os.OpenFile(
		to.local, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		_, message := stdlib.SocketErrno(err)
		return false, "target", message, nil
	}
	_, writeErr := io.Copy(destination, bytes.NewReader(contents))
	closeErr := destination.Close()
	if writeErr != nil {
		_, message := stdlib.SocketErrno(writeErr)
		return false, "target", message, nil
	}
	if closeErr != nil {
		_, message := stdlib.SocketErrno(closeErr)
		return false, "target", message, nil
	}
	return true, "", "", nil
}

func (in *Interp) globPaths(
	pattern string, flags int64,
) ([]string, int, bool, error) {
	platform, available := libc.PlatformGlobFlags()
	if !available {
		return nil, 0, false, nil
	}
	reader, hasReader := in.loader.(DirectoryReader)
	if !hasReader {
		paths, status, available := libc.Glob(pattern, flags)
		return paths, status, available, nil
	}
	brace := platform.Brace != 0 && flags&platform.Brace != 0
	noEscape := platform.NoEscape != 0 && flags&platform.NoEscape != 0
	base, _ := stdlib.GlobPatternBase(pattern, brace, noEscape)
	_, _, handled := reader.ReadDirectory(base)
	if !handled {
		paths, status, available := libc.Glob(pattern, flags)
		return paths, status, available, nil
	}

	patterns := []string{pattern}
	if brace {
		patterns = stdlib.GlobBraceExpand(pattern, !noEscape)
	}
	var matches []string
	for _, expanded := range patterns {
		found, err := in.globLoaderPattern(reader, expanded, noEscape, platform, flags)
		if err != nil {
			return nil, 0, true, err
		}
		matches = append(matches, found...)
	}
	if len(matches) == 0 && platform.NoCheck != 0 && flags&platform.NoCheck != 0 {
		matches = append(matches, pattern)
	}
	return matches, 0, true, nil
}

func (in *Interp) globLoaderPattern(
	reader DirectoryReader,
	pattern string,
	noEscape bool,
	platform libc.GlobFlags,
	flags int64,
) ([]string, error) {
	base, remaining := stdlib.GlobPatternBase(pattern, false, noEscape)
	segments := strings.Split(remaining, "/")
	var walk func(string, int) ([]string, error)
	walk = func(directory string, index int) ([]string, error) {
		names, err, handled := reader.ReadDirectory(directory)
		if !handled || err != nil {
			return nil, nil
		}
		var matches []string
		for _, name := range names {
			if name == "." || name == ".." {
				continue
			}
			matched, available := libc.GlobMatch(segments[index], name, noEscape)
			if !available {
				return nil, &UnsupportedError{Opcode: "libc fnmatch without cgo"}
			}
			if !matched {
				continue
			}
			candidate := name
			if directory != "." {
				candidate = strings.TrimSuffix(directory, "/") + "/" + name
			}
			if index+1 < len(segments) {
				nested, err := walk(candidate, index+1)
				if err != nil {
					return nil, err
				}
				matches = append(matches, nested...)
				continue
			}
			status, handled := PathStatus{}, false
			if inspector, ok := in.loader.(PathInspector); ok {
				status, handled = inspector.InspectPath(candidate)
			}
			if platform.OnlyDir != 0 && flags&platform.OnlyDir != 0 &&
				(!handled || !status.Dir) {
				continue
			}
			if platform.Mark != 0 && flags&platform.Mark != 0 && handled && status.Dir {
				candidate += "/"
			}
			matches = append(matches, candidate)
		}
		return matches, nil
	}
	return walk(base, 0)
}

// settype rewrites a variable's type in place. It is the one conversion that
// takes its subject by reference, so it needs the slot rather than the value --
// and the type names it accepts are the old `gettype` spellings, not the ones
// the language uses in declarations.
type directoryStream struct {
	names    []string
	position int
	native   *libc.Directory
	user     *userDirectory
}

func (directory *directoryStream) read() (string, bool, error) {
	if directory.user != nil {
		return directory.user.read()
	}
	if directory.native != nil {
		name, _, ok := directory.native.Read()
		return name, ok, nil
	}
	if directory.position >= len(directory.names) {
		return "", false, nil
	}
	name := directory.names[directory.position]
	directory.position++
	return name, true, nil
}

func (directory *directoryStream) rewind() error {
	if directory.user != nil {
		return directory.user.rewind()
	}
	if directory.native != nil {
		directory.native.Rewind()
		return nil
	}
	directory.position = 0
	return nil
}

func (directory *directoryStream) close() error {
	if directory.user != nil {
		err := directory.user.close()
		directory.user = nil
		return err
	}
	if directory.native != nil {
		directory.native.Close()
		directory.native = nil
	}
	return nil
}

func warnUnknownWrapper(ctx *php.Ctx, function, scheme string) {
	file, line := ctx.Location()
	callerFile, callerLine := stdlib.LibraryCallerLocation(ctx)
	ctx.SetLocation(callerFile, callerLine)
	ctx.Warn(
		function+"(): Unable to find the wrapper %q - "+
			"did you forget to enable it when you configured PHP?",
		scheme,
	)
	ctx.SetLocation(file, line)
}

func directoryHandle(value php.Value, function string) (*directoryStream, *php.Resource, error) {
	handle := value.Resource()
	if handle == nil {
		return nil, nil, Raise("TypeError",
			"%s(): Argument #1 ($dir_handle) must be a valid Directory resource", function)
	}
	if handle.Closed() {
		return nil, nil, Raise("TypeError",
			"%s(): supplied resource is not a valid Directory resource", function)
	}
	held, ok := handle.Payload.(*directoryStream)
	if !ok {
		return nil, nil, Raise("TypeError",
			"%s(): Argument #1 ($dir_handle) must be a valid Directory resource", function)
	}
	return held, handle, nil
}

func directoryObjectMethod(call *pendingCall) (php.Value, bool, error) {
	if call == nil || call.this == nil ||
		!strings.EqualFold(call.this.ClassName, "Directory") {
		return php.Undef(), false, nil
	}
	method := strings.ToLower(call.builtinMethod)
	if method != "read" && method != "rewind" && method != "close" {
		return php.Undef(), false, nil
	}
	if len(call.named) > 0 {
		return php.Undef(), true, Raise(
			"Error", "Unknown named parameter $%s", call.named[0].name)
	}
	if len(call.args) != 0 {
		return php.Undef(), true, Raise(
			"ArgumentCountError",
			"Directory::%s() expects exactly 0 arguments, %d given",
			method, len(call.args))
	}
	heldObject, ok := call.this.Internal.(*directoryObject)
	if !ok || heldObject == nil || heldObject.Handle == nil {
		return php.Undef(), true, Raise("Error", "Unable to find my handle property")
	}
	held, handle, err := directoryHandle(
		php.Res(heldObject.Handle), "Directory::"+method)
	if err != nil {
		return php.Undef(), true, err
	}
	switch method {
	case "read":
		name, available, err := held.read()
		if err != nil {
			return php.Undef(), true, err
		}
		if !available {
			return php.Bool(false), true, nil
		}
		return php.String(name), true, nil
	case "rewind":
		if err := held.rewind(); err != nil && !errors.Is(err, errUserStreamOperation) {
			return php.Undef(), true, err
		}
		return php.Null(), true, nil
	default:
		if err := held.close(); err != nil {
			return php.Undef(), true, err
		}
		handle.Close()
		return php.Null(), true, nil
	}
}

func pathStatValues(stat PathStat) []int64 {
	return []int64{
		stat.Dev, stat.Ino, stat.Mode, stat.NLink, stat.UID, stat.GID,
		stat.RDev, stat.Size, stat.ATime, stat.MTime, stat.CTime,
		stat.BlockSize, stat.Blocks,
	}
}

func pathStatusPredicate(status PathStatus, predicate string) (bool, bool) {
	switch predicate {
	case "exists":
		return status.Exists, true
	case "file":
		return status.File, true
	case "dir":
		return status.Dir, true
	case "link":
		return status.Link, true
	case "readable":
		return status.Readable, true
	case "writable":
		return status.Writable, true
	case "executable":
		return status.Executable, true
	default:
		return false, false
	}
}

func hostPathStatus(name string) PathStatus {
	information, err := os.Lstat(name)
	if err != nil {
		return PathStatus{}
	}
	status := PathStatus{Link: information.Mode()&os.ModeSymlink != 0}
	information, err = os.Stat(name)
	if err != nil {
		return status
	}
	status.Exists = true
	status.File = information.Mode().IsRegular()
	status.Dir = information.IsDir()
	status.Readable = syscall.Access(name, 4) == nil
	status.Writable = syscall.Access(name, 2) == nil
	status.Executable = syscall.Access(name, 1) == nil
	return status
}

func streamMetadata(value php.Value, function string) (php.Value, error) {
	value = value.Deref()
	handle := value.Resource()
	if handle == nil {
		return php.Undef(), Raise(
			"TypeError",
			"%s(): Argument #1 ($stream) must be of type resource, %s given",
			function,
			givenTypeName(value),
		)
	}
	if handle.Closed() {
		return php.Undef(), Raise(
			"TypeError",
			"%s(): supplied resource is not a valid stream resource",
			function,
		)
	}
	switch held := handle.Payload.(type) {
	case *stdlib.Stream:
		return php.Arr(held.Metadata()), nil
	case *directoryStream:
		return php.Arr(stdlib.DirectoryMetadata()), nil
	default:
		return php.Undef(), Raise(
			"TypeError",
			"%s(): supplied resource is not a valid stream resource",
			function,
		)
	}
}

// streamFilterNames is what `stream_get_filters` reports: the engine's own,
// then whatever the request registered. The registry is the Ctx's now.
func streamFilterNames(ctx *php.Ctx) []string {
	names := append([]string(nil), stdlib.BuiltinStreamFilterNames...)
	return append(names, ctx.StreamFilterNames()...)
}

func streamBucketValue(ctx *php.Ctx, unit *Unit, bucket *stdlib.StreamBucket) (php.Value, error) {
	object := unit.instantiateBuiltin("StreamBucket")
	if object == nil {
		return php.Undef(), &UnsupportedError{Opcode: "StreamBucket internal class"}
	}
	resource := ctx.NewResource("userfilter.bucket", bucket)
	object.Set("bucket", php.Res(resource))
	object.Set("data", php.String(bucket.Data))
	object.Set("datalen", php.Int(int64(len(bucket.Data))))
	object.Set("dataLength", php.Int(int64(len(bucket.Data))))
	return php.Obj(object), nil
}

func (in *Interp) newUserStreamFilter(ctx *php.Ctx, unit *Unit, name, className string,
	params php.Value, held *stdlib.Stream, handle *php.Resource, operation string, depth int,
) (*stdlib.StreamFilter, error) {
	class, found, err := in.resolveClass(ctx, unit, className, depth)
	if err != nil {
		return nil, err
	}
	if !found {
		ctx.Warn("%s(): User-filter %q requires class %q, but that class is not defined", operation, name, className)
		return nil, nil
	}
	object := unit.instantiate(class)
	object.Set("filtername", php.String(name))
	if params.Kind() == php.KindUndef {
		object.Set("params", php.Null())
	} else {
		object.Set("params", params.Copy())
	}
	object.Set("stream", php.Null())

	if body, declaring, hasCreate := unit.resolveMethod(class, "onCreate"); hasCreate {
		created, err := in.invokeDriven(ctx, unit, body, object, declaring, class, nil, depth)
		if err != nil {
			return nil, err
		}
		if created.Deref().Kind() == php.KindFalse {
			return nil, nil
		}
	}

	filter := stdlib.NewUserStreamFilter(name, held, object, class)
	filterRun := func(input string, closing bool, operation string) (string, int, int, error) {
		inputBrigade := &stdlib.StreamBrigade{}
		if input != "" {
			inputBrigade.Attach(&stdlib.StreamBucket{Data: input}, true)
		}
		outputBrigade := &stdlib.StreamBrigade{}
		inputResource := ctx.NewResource("userfilter.bucket brigade", inputBrigade)
		outputResource := ctx.NewResource("userfilter.bucket brigade", outputBrigade)
		defer inputResource.Close()
		defer outputResource.Close()
		consumed := php.NewRef(php.Int(0))
		object.Set("stream", php.Res(handle))
		body, declaring, hasFilter := unit.resolveMethod(class, "filter")
		if !hasFilter {
			object.Set("stream", php.Null())
			ctx.Warn("%s(): Failed to call filter function", operation)
			return "", 0, stdlib.StreamFilterErrFatal, stdlib.ErrStreamFilterFatal
		}
		status, err := in.invokeDriven(ctx, unit, body, object, declaring, class, []php.Value{
			php.Res(inputResource),
			php.Res(outputResource),
			php.MakeRef(consumed),
			php.Bool(closing),
		}, depth)
		object.Set("stream", php.Null())
		if err != nil {
			return "", int(consumed.Get().Int()), stdlib.StreamFilterErrFatal, err
		}
		if len(inputBrigade.Buckets) != 0 {
			ctx.Warn("%s(): Unprocessed filter buckets remaining on input brigade", operation)
		}
		return outputBrigade.Text(), int(consumed.Get().Int()), int(status.Deref().Int()), nil
	}
	filter.SetRun(filterRun)
	filterClose := func(_ string) error {
		body, declaring, hasClose := unit.resolveMethod(class, "onClose")
		if !hasClose {
			return nil
		}
		_, err := in.invokeDriven(ctx, unit, body, object, declaring, class, nil, depth)
		return err
	}
	filter.SetClose(filterClose)
	return filter, nil
}

func (in *Interp) createStreamFilter(ctx *php.Ctx, unit *Unit, name string,
	params php.Value, held *stdlib.Stream, handle *php.Resource, operation string, depth int,
) (*stdlib.StreamFilter, bool, error) {
	if builtin, found := stdlib.NewBuiltinStreamFilter(ctx, name, params, operation); found {
		if builtin == nil {
			return nil, true, nil
		}
		builtin.Stream = held
		return builtin, true, nil
	}
	className, found := ctx.StreamFilterClass(name)
	if !found {
		return nil, false, nil
	}
	filter, err := in.newUserStreamFilter(
		ctx, unit, name, className, params, held, handle, operation, depth,
	)
	return filter, true, err
}

func (in *Interp) attachStreamFilter(ctx *php.Ctx, unit *Unit, function string,
	args []php.Value, appendFilter bool, depth int,
) (php.Value, error) {
	streamValue := args[0].Deref()
	held, handle, ok := stdlib.AsStream(streamValue)
	if !ok {
		return php.Undef(), Raise("TypeError", "%s(): supplied resource is not a valid stream resource", function)
	}
	name := args[1].Deref().String()
	mode := int64(0)
	if len(args) > 2 {
		mode = args[2].Deref().Int()
	}
	params := php.Undef()
	if len(args) > 3 {
		params = args[3].Deref()
	}
	if mode&3 == 0 {
		if strings.ContainsAny(held.Mode(), "r+") {
			mode |= stdlib.StreamFilterRead
		}
		if strings.ContainsAny(held.Mode(), "wa+") {
			mode |= stdlib.StreamFilterWrite
		}
	}
	var last *stdlib.StreamFilter
	for _, direction := range []int64{stdlib.StreamFilterRead, stdlib.StreamFilterWrite} {
		if mode&direction == 0 {
			continue
		}
		filter, located, err := in.createStreamFilter(ctx, unit, name, params, held, handle, function, depth)
		if err != nil {
			return php.Undef(), err
		}
		if !located {
			ctx.Warn("%s(): Unable to locate filter %q", function, name)
			return php.Bool(false), nil
		}
		if filter == nil {
			ctx.Warn("%s(): Unable to create or locate filter %q", function, name)
			return php.Bool(false), nil
		}
		filter.SetRead(direction == stdlib.StreamFilterRead)
		held.AttachFilter(filter, appendFilter)
		last = filter
	}
	if last == nil {
		return php.Bool(false), nil
	}
	filterResource := ctx.NewResource("stream filter", last)
	last.Handle = filterResource
	return php.Res(filterResource), nil
}

func (in *Interp) removeStreamFilter(ctx *php.Ctx, value php.Value, operation string) (php.Value, error) {
	handle := value.Deref().Resource()
	if handle == nil || handle.Closed() {
		return php.Undef(), Raise("TypeError", "%s(): supplied resource is not a valid stream filter resource", operation)
	}
	filter, ok := handle.Payload.(*stdlib.StreamFilter)
	if !ok || filter == nil || filter.Removed() || filter.Stream == nil {
		return php.Undef(), Raise("TypeError", "%s(): supplied resource is not a valid stream filter resource", operation)
	}
	if err := filter.Stream.RemoveFilter(filter, operation); err != nil {
		if errors.Is(err, stdlib.ErrStreamFilterFatal) {
			ctx.Warn("%s(): Unable to flush filter, not removing", operation)
			return php.Bool(false), nil
		}
		return php.Undef(), err
	}
	handle.Close()
	return php.Bool(true), nil
}
