package stdlib

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/moriyoshi/php2go/runtime/libc"
	"github.com/moriyoshi/php2go/runtime/php"
)

// The stream itself: a file, a socket, a pipe, an in-memory buffer, or a PHP
// class registered as a wrapper -- and the filter chain that wraps one. It
// moved here from the interpreter because an emitted program has to be able to
// **open** a stream. The `Stream` interface the multiplexer split introduced
// let compiled code operate on a stream somebody else had made, which is no use
// when `fopen` is the thing that makes it.
//
// What stayed behind is what needs a class table or a loader: 29 methods on
// `*Interp` and `*Unit` that open a user-space wrapper, resolve a filter class
// or an include path, and the **directory** handles -- a different resource
// that happened to share the file. The one piece of a stream the interpreter
// still owns is reached through the `UserStream` interface.
//
// The stream **context** is here too, further down. It had a file of its own
// and did not earn one: the two types reach through each other -- every handle
// exposes a stream-local context, and a context is what `fopen`'s fourth
// argument carries -- so the split meant two lookups for one question, which is
// how `AsStreamContext` and `contextOf` came to be the same function twice.

// Stream is what an owned Stream resource carries.
type Stream struct {
	file *os.File
	// user owns the Object behind a registered user-space wrapper.
	user UserStream
	// engineWrapper and engineStreamType present a stream whose wrapper is
	// php2go's own userland class as the engine's native one
	// (PresentAsEngine): php://memory is `PHP`/`MEMORY`, not `user-space`.
	engineWrapper, engineStreamType string
	// pipe owns a libc FILE* and child process. I/O uses its descriptor, but
	// close must go through pclose rather than os.File.Close.
	pipe *libc.Pipe
	// context is allocated lazily when the Stream-context API observes this
	// Handle. PHP exposes Stream-local context state even for ordinary streams.
	context *StreamContext
	// mem is an in-memory Stream, which is what a `data:` URI opens. It reads
	// and seeks like a file and cannot be written to.
	mem *bytes.Reader
	// eof records that a read has already reported the end. PHP's feof answers
	// about the last read, not about the position, so it cannot be derived.
	eof bool
	// The remaining fields are the wrapper-neutral state exposed through
	// Stream options and Metadata. The public wrappers stay in bundled PHP;
	// only a resource payload can answer these facts.
	blocked     bool
	chunkSize   int
	mode        string
	uri         string
	tempName    string
	directWrite func(string)

	readFilters []*StreamFilter
	// filterURL is the php://filter URL this stream was opened through
	// (filterwrapper.go), which stream_get_meta_data does not model.
	filterURL          string
	writeFilters       []*StreamFilter
	filteredRead       []byte
	readFiltersFlushed bool
	FilteredPosition   int64
	FilterPositionSet  bool

	// data marks the RFC2397 wrapper, whose Metadata shape differs from a
	// plain file's even though both payloads implement the same operations.
	data      bool
	mediaType string
	base64    bool

	// socket marks a descriptor created by the Stream socket API.
	socket          bool
	socketDomain    int
	socketType      int
	socketProtocol  int
	socketTransport string
	socketListening bool
	timedOut        bool
	persistentPool  *PersistentSocketPool
	persistentEntry *persistentSocketEntry
}

type DirectoryObject struct {
	Handle *php.Resource
}

// PersistentSocketPool owns connections whose lifetime extends beyond one PHP
// request. A bundle App owns one pool; each interpreter request leases an idle
// connection and returns it when the request finishes.
type PersistentSocketPool struct {
	mu      sync.Mutex
	entries map[string][]*persistentSocketEntry
}

type persistentSocketEntry struct {
	key    string
	Stream *Stream
	busy   bool
}

type PersistentSocketLease struct {
	Entry  *persistentSocketEntry
	Handle *php.Resource
}

// NewPersistentSocketPool creates an application-scoped persistent connection
// pool. It is exported only so runtime/bundle can preserve it across requests.
func NewPersistentSocketPool() *PersistentSocketPool {
	return &PersistentSocketPool{entries: map[string][]*persistentSocketEntry{}}
}

func persistentSocketAlive(held *Stream) bool {
	if held == nil || held.file == nil || !held.socket {
		return false
	}
	buffer := make([]byte, 1)
	n, _, err := syscall.Recvfrom(
		int(held.file.Fd()),
		buffer,
		syscall.MSG_PEEK|syscall.MSG_DONTWAIT,
	)
	if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
		return true
	}
	if err != nil {
		return false
	}
	return held.socketType != syscall.SOCK_STREAM || n != 0
}

func (p *PersistentSocketPool) Acquire(
	key string,
	create func() (*Stream, int64, string),
) (*persistentSocketEntry, int64, string) {
	if p == nil {
		held, errno, Message := create()
		if held == nil {
			return nil, errno, Message
		}
		return &persistentSocketEntry{key: key, Stream: held, busy: true}, 0, ""
	}
	p.mu.Lock()
	entries := p.entries[key]
	kept := entries[:0]
	var available *persistentSocketEntry
	for _, Entry := range entries {
		if Entry.busy {
			kept = append(kept, Entry)
			continue
		}
		if !persistentSocketAlive(Entry.Stream) {
			if Entry.Stream != nil && Entry.Stream.file != nil {
				_ = Entry.Stream.file.Close()
			}
			continue
		}
		kept = append(kept, Entry)
		if available == nil {
			available = Entry
		}
	}
	p.entries[key] = kept
	if available != nil {
		available.busy = true
		p.mu.Unlock()
		return available, 0, ""
	}
	p.mu.Unlock()

	held, errno, Message := create()
	if held == nil {
		return nil, errno, Message
	}
	Entry := &persistentSocketEntry{key: key, Stream: held, busy: true}
	held.persistentPool = p
	held.persistentEntry = Entry
	p.mu.Lock()
	p.entries[key] = append(p.entries[key], Entry)
	p.mu.Unlock()
	return Entry, 0, ""
}

func (p *PersistentSocketPool) Release(Entry *persistentSocketEntry) {
	if p == nil || Entry == nil {
		return
	}
	p.mu.Lock()
	Entry.busy = false
	p.mu.Unlock()
}

func (p *PersistentSocketPool) remove(Entry *persistentSocketEntry) {
	if p == nil || Entry == nil {
		return
	}
	p.mu.Lock()
	entries := p.entries[Entry.key]
	for index, candidate := range entries {
		if candidate != Entry {
			continue
		}
		entries = append(entries[:index], entries[index+1:]...)
		break
	}
	if len(entries) == 0 {
		delete(p.entries, Entry.key)
	} else {
		p.entries[Entry.key] = entries
	}
	p.mu.Unlock()
}

type streamSelectEntry struct {
	key    php.Key
	value  php.Value
	Handle *php.Resource
	Stream *Stream
}

type streamSelectMode uint8

const (
	StreamSelectRead streamSelectMode = iota
	StreamSelectWrite
	StreamSelectExcept
)

// ---- the stream context ----
//
// A context is an options array, an optional notification callback, and the
// flag saying whether the callback was set. Nothing in it needs a class table
// or an open file, so it sits with the stream rather than behind an interface:
// the two are reached through one another -- every handle exposes a
// stream-local context, and a context is what `fopen`'s fourth argument
// carries.
//
// One of the five functions is a constructor, which is why the string selector
// was the **first** argument in the internal entry point and the second
// everywhere else: `create` has no context to take. That inconsistency was
// invisible while the calls all went through one multiplexer and obvious once
// they did not.

// StreamContext is the payload of a stream-context resource.
type StreamContext struct {
	options         *php.Array
	notification    php.Value
	hasNotification bool
}

// NewStreamContext allocates an empty context.
func NewStreamContext() *StreamContext {
	return &StreamContext{options: php.NewArray()}
}

// contextHolder is a stream that carries a context of its own. PHP exposes
// stream-local context state for every handle, so the context is allocated when
// something first asks for it rather than when the stream is opened.
type contextHolder interface {
	StreamContext() *StreamContext
}

// MergeOptions layers an options array over what the context holds, per wrapper
// and then per option, so setting one option does not drop the others.
func (c *StreamContext) MergeOptions(options *php.Array) {
	if c.options == nil {
		c.options = php.NewArray()
	}
	options.Each(func(wrapper php.Key, raw php.Value) bool {
		incoming := raw.Array()
		if incoming == nil {
			return true
		}
		existingValue, found := c.options.Get(wrapper)
		var existing *php.Array
		if found {
			existing = existingValue.Array()
		}
		if existing == nil {
			existing = php.NewArray()
			c.options.Set(wrapper, php.Arr(existing))
		}
		incoming.EachRaw(func(option php.Key, value php.Value) bool {
			existing.Set(option, php.CopyElement(value))
			return true
		})
		return true
	})
}

// Options answers a copy, so a caller cannot reach back into the context
// through the array it was given.
func (c *StreamContext) Options() php.Value {
	if c == nil || c.options == nil {
		return php.Arr(php.NewArray())
	}
	return php.Arr(c.options.Clone())
}

// Params is the context as `stream_context_get_params` reports it: the options
// under their own key, and the notification callback only when one was set.
func (c *StreamContext) Params() php.Value {
	params := php.NewArray()
	if c != nil && c.hasNotification {
		params.Set(php.StringKey("notification"), c.notification.Copy())
	}
	params.Set(php.StringKey("options"), c.Options())
	return php.Arr(params)
}

// SetNotification installs the callback stream_context_set_params takes.
func (c *StreamContext) SetNotification(callback php.Value) {
	c.notification = callback.Copy()
	c.hasNotification = true
}

// Option answers one wrapper's option -- `Option("socket", "backlog")` -- and
// whether it was set. A caller wanting one setting should not have to reach
// through two arrays to find it, which is what exposing the options field
// would have meant.
func (c *StreamContext) Option(wrapper, name string) (php.Value, bool) {
	if c == nil || c.options == nil {
		return php.Null(), false
	}
	section, found := c.options.Get(php.StringKey(wrapper))
	if !found || section.Array() == nil {
		return php.Null(), false
	}
	value, found := section.Array().Get(php.StringKey(name))
	if !found {
		return php.Null(), false
	}
	return value, true
}

// Notification answers the installed callback, and whether there is one.
func (c *StreamContext) Notification() (php.Value, bool) {
	if c == nil || !c.hasNotification {
		return php.Null(), false
	}
	return c.notification, true
}

func init() {
	php.RegisterBuiltin(`php2go\internal\stream_context_create`, func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Res(ctx.NewResource("stream-context", NewStreamContext())), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_context_get_options`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := AsStreamContext(php.Arg(args, 0))
		if !ok {
			return php.Undef(), php.Raise("Error", "stream context: invalid resource")
		}
		return held.Options(), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_context_get_params`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := AsStreamContext(php.Arg(args, 0))
		if !ok {
			return php.Undef(), php.Raise("Error", "stream context: invalid resource")
		}
		return held.Params(), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_context_merge_options`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := AsStreamContext(php.Arg(args, 0))
		if !ok {
			return php.Undef(), php.Raise("Error", "stream context: invalid resource")
		}
		if len(args) > 1 {
			if options := args[1].Deref().Array(); options != nil {
				held.MergeOptions(options)
			}
		}
		return php.Bool(true), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_context_set_notification`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := AsStreamContext(php.Arg(args, 0))
		if !ok {
			return php.Undef(), php.Raise("Error", "stream context: invalid resource")
		}
		if len(args) > 1 {
			held.SetNotification(args[1])
		}
		return php.Bool(true), nil
	})
}

// StreamContext is the stream's own, allocated on first use. PHP exposes
// stream-local context state for every handle, so there is nothing to make
// until something asks.
func (s *Stream) StreamContext() *StreamContext {
	if s.context == nil {
		s.context = NewStreamContext()
	}
	return s.context
}

// AsStreamContext reaches the context behind either a context resource or a
// stream. It is the **only** lookup: `contextOf(args, at)` was a second one
// that differed in taking an argument list and in matching `*Stream` by name
// where this matches the holder interface.
func AsStreamContext(value php.Value) (*StreamContext, bool) {
	handle := value.Deref().Resource()
	if handle == nil || handle.Closed() {
		return nil, false
	}
	switch held := handle.Payload.(type) {
	case *StreamContext:
		return held, true
	case contextHolder:
		return held.StreamContext(), true
	}
	return nil, false
}

// The Stream reads, seeks and closes through whichever of the two it holds.
func (s *Stream) Read(p []byte) (int, error) {
	if s.user != nil {
		return s.user.Read(p)
	}
	if s.mem != nil {
		return s.mem.Read(p)
	}
	if s.pipe != nil {
		n, err := syscall.Read(s.pipe.FD(), p)
		if n == 0 && err == nil {
			err = io.EOF
		}
		return n, err
	}
	if !s.socket {
		return s.file.Read(p)
	}
	total := 0
	for total < len(p) {
		n, err := syscall.Read(int(s.file.Fd()), p[total:])
		total += n
		if err != nil {
			if total > 0 {
				return total, nil
			}
			return 0, err
		}
		if n == 0 {
			return total, io.EOF
		}
	}
	return total, nil
}

func (s *Stream) WriteString(text string) (int, error) {
	if s.user != nil {
		return s.user.Write(text)
	}
	if s.directWrite != nil {
		s.directWrite(text)
		return len(text), nil
	}
	if s.mem != nil {
		// A data: Stream is read-only. The engine answers 0 rather than
		// failing, which is what a write to a full Stream reports.
		return 0, nil
	}
	if s.pipe != nil {
		return syscall.Write(s.pipe.FD(), []byte(text))
	}
	if s.socket {
		return syscall.SendmsgN(
			int(s.file.Fd()),
			[]byte(text),
			nil,
			nil,
			syscall.MSG_NOSIGNAL,
		)
	}
	return s.file.WriteString(text)
}

func (s *Stream) Seek(offset int64, whence int) (int64, error) {
	if s.user != nil {
		return s.user.Seek(offset, whence)
	}
	if s.mem != nil {
		return s.mem.Seek(offset, whence)
	}
	if s.pipe != nil {
		return 0, syscall.ESPIPE
	}
	return s.file.Seek(offset, whence)
}

func (s *Stream) Close() error {
	_, err := s.closeStatusFor("fclose")
	return err
}

func (s *Stream) CloseStatus() (int, error) {
	return s.closeStatusFor("pclose")
}

func (s *Stream) closeStatusFor(operation string) (int, error) {
	filterErr := s.closeStreamFilters(operation)
	if s.user != nil {
		closeErr := s.user.Close()
		s.user = nil
		if closeErr != nil {
			return -1, closeErr
		}
		return 0, filterErr
	}
	if s.mem != nil {
		return 0, filterErr
	}
	if s.pipe != nil {
		status, errorNumber, available := s.pipe.Close()
		s.pipe = nil
		if !available {
			return -1, php.Unsupported("%s", "libc pclose without cgo")
		}
		if errorNumber != 0 && status == -1 {
			return status, syscall.Errno(errorNumber)
		}
		if filterErr != nil {
			return status, filterErr
		}
		return status, nil
	}
	var closeErr error
	if s.file != nil {
		closeErr = s.file.Close()
		s.file = nil
	}
	if s.tempName != "" {
		removeErr := os.Remove(s.tempName)
		s.tempName = ""
		if closeErr == nil && removeErr != nil && !os.IsNotExist(removeErr) {
			return -1, removeErr
		}
	}
	if closeErr != nil {
		return -1, closeErr
	}
	return 0, filterErr
}

func (s *Stream) truncate(size int64) bool {
	if size < 0 {
		return false
	}
	if s.pipe != nil {
		return false
	}
	if s.user != nil {
		truncated, _ := s.user.Truncate(size)
		return truncated
	}
	if s.mem == nil {
		return s.file.Truncate(size) == nil
	}
	if size > int64(^uint(0)>>1) {
		return false
	}
	position, err := s.mem.Seek(0, io.SeekCurrent)
	if err != nil {
		return false
	}
	data := make([]byte, int(size))
	copyLength := size
	if available := s.mem.Size(); copyLength > available {
		copyLength = available
	}
	if copyLength > 0 {
		if _, err := s.mem.ReadAt(data[:copyLength], 0); err != nil && err != io.EOF {
			return false
		}
	}
	s.mem = bytes.NewReader(data)
	if _, err := s.mem.Seek(position, io.SeekStart); err != nil {
		return false
	}
	return true
}

func (s *Stream) statValues() ([]int64, bool) {
	if s.user != nil {
		values, ok, _ := s.user.StatValues()
		return values, ok
	}
	if s.mem != nil {
		return []int64{
			12, 0, 0100666, 1, 0, 0, -1,
			s.mem.Size(), 0, 0, 0, -1, -1,
		}, true
	}
	if s.pipe != nil {
		var stat syscall.Stat_t
		if syscall.Fstat(s.pipe.FD(), &stat) != nil {
			return nil, false
		}
		return []int64{
			int64(stat.Dev), int64(stat.Ino), int64(stat.Mode), int64(stat.Nlink),
			int64(stat.Uid), int64(stat.Gid), -1, stat.Size,
			stat.Atim.Sec, stat.Mtim.Sec, stat.Ctim.Sec, -1, -1,
		}, true
	}
	info, err := s.file.Stat()
	if err != nil {
		return nil, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, false
	}
	return []int64{
		int64(stat.Dev),
		int64(stat.Ino),
		int64(stat.Mode),
		int64(stat.Nlink),
		int64(stat.Uid),
		int64(stat.Gid),
		-1,
		stat.Size,
		stat.Atim.Sec,
		stat.Mtim.Sec,
		stat.Ctim.Sec,
		-1,
		-1,
	}, true
}

func (s *Stream) lock(operation int64) (bool, bool) {
	if s.file == nil {
		return false, false
	}
	lockType := int16(syscall.F_UNLCK)
	switch operation & 3 {
	case 1:
		lockType = syscall.F_RDLCK
	case 2:
		lockType = syscall.F_WRLCK
	}
	command := syscall.F_SETLKW
	if operation&4 != 0 {
		command = syscall.F_SETLK
	}
	err := syscall.FcntlFlock(s.file.Fd(), command, &syscall.Flock_t{
		Type:   lockType,
		Whence: int16(io.SeekStart),
	})
	if err == syscall.EACCES || err == syscall.EAGAIN {
		return false, true
	}
	return err == nil, false
}

func (s *Stream) isTTY() bool {
	fd, ok := s.descriptor()
	if !ok {
		return false
	}
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		uintptr(fd),
		uintptr(syscall.TCGETS),
		uintptr(unsafe.Pointer(&termios)),
	)
	return errno == 0
}

func (s *Stream) descriptor() (int, bool) {
	if s == nil {
		return -1, false
	}
	if s.pipe != nil {
		fd := s.pipe.FD()
		return fd, fd >= 0
	}
	if s.file != nil {
		return int(s.file.Fd()), true
	}
	return -1, false
}

func AsStream(value php.Value) (*Stream, *php.Resource, bool) {
	Handle := value.Deref().Resource()
	if Handle == nil || Handle.Closed() {
		return nil, Handle, false
	}
	held, ok := Handle.Payload.(*Stream)
	return held, Handle, ok
}

func SocketErrno(err error) (int64, string) {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return 0, err.Error()
	}
	Message := errno.Error()
	if Message != "" {
		Message = strings.ToUpper(Message[:1]) + Message[1:]
	}
	return int64(errno), Message
}

func socketAddress(sockaddr syscall.Sockaddr) string {
	switch address := sockaddr.(type) {
	case *syscall.SockaddrUnix:
		return address.Name
	case *syscall.SockaddrInet4:
		return net.JoinHostPort(
			net.IP(address.Addr[:]).String(),
			strconv.Itoa(address.Port),
		)
	case *syscall.SockaddrInet6:
		return net.JoinHostPort(
			net.IP(address.Addr[:]).String(),
			strconv.Itoa(address.Port),
		)
	default:
		return ""
	}
}

func newSocketStream(ctx *php.Ctx, fd, domain, socketType, protocol int) php.Value {
	return newAddressedSocketStream(ctx, fd, domain, socketType, protocol, "", nil, false)
}

func addressedSocketStream(fd, domain, socketType, protocol int,
	transport string, context *StreamContext, listening bool) *Stream {
	return &Stream{
		file:            os.NewFile(uintptr(fd), "php2go-socket"),
		context:         context,
		blocked:         true,
		chunkSize:       8192,
		mode:            "r+",
		socket:          true,
		socketDomain:    domain,
		socketType:      socketType,
		socketProtocol:  protocol,
		socketTransport: transport,
		socketListening: listening,
	}
}

func newAddressedSocketStream(ctx *php.Ctx, fd, domain, socketType, protocol int,
	transport string, context *StreamContext, listening bool) php.Value {
	return php.Res(ctx.NewResource("stream", addressedSocketStream(
		fd, domain, socketType, protocol, transport, context, listening,
	)))
}

func SocketPairResult(ctx *php.Ctx, domain, socketType, protocol int) php.Value {
	result := php.NewArray()
	pair, err := syscall.Socketpair(domain, socketType, protocol)
	if err != nil {
		errno, Message := SocketErrno(err)
		result.Append(php.Bool(false))
		result.Append(php.Int(errno))
		result.Append(php.String(Message))
		return php.Arr(result)
	}
	result.Append(php.Bool(true))
	result.Append(newSocketStream(ctx, pair[0], domain, socketType, protocol))
	result.Append(newSocketStream(ctx, pair[1], domain, socketType, protocol))
	return php.Arr(result)
}

type socketTarget struct {
	transport string
	endpoint  string
	domain    int
	typeID    int
	protocol  int
	address   syscall.Sockaddr
}

func socketTargetFor(address string) (socketTarget, int64, string) {
	separator := strings.Index(address, "://")
	if separator <= 0 {
		return socketTarget{}, 0, fmt.Sprintf("Failed to parse address %q", address)
	}
	transport := strings.ToLower(address[:separator])
	endpoint := address[separator+3:]
	target := socketTarget{transport: transport, endpoint: endpoint}
	switch transport {
	case "tcp":
		target.typeID = syscall.SOCK_STREAM
		target.protocol = syscall.IPPROTO_TCP
	case "udp":
		target.typeID = syscall.SOCK_DGRAM
		target.protocol = syscall.IPPROTO_UDP
	case "unix":
		target.domain = syscall.AF_UNIX
		target.typeID = syscall.SOCK_STREAM
	case "udg":
		target.domain = syscall.AF_UNIX
		target.typeID = syscall.SOCK_DGRAM
	default:
		return socketTarget{}, 0, fmt.Sprintf(
			"Unable to find the socket transport %q - did you forget to enable it when you configured PHP?",
			address[:separator],
		)
	}
	if target.domain == syscall.AF_UNIX {
		if endpoint == "" {
			return socketTarget{}, 0, fmt.Sprintf("Failed to parse address %q", endpoint)
		}
		target.address = &syscall.SockaddrUnix{Name: endpoint}
		return target, 0, ""
	}

	host, portText, err := net.SplitHostPort(endpoint)
	if err != nil {
		return socketTarget{}, 0, fmt.Sprintf("Failed to parse address %q", endpoint)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return socketTarget{}, 0, fmt.Sprintf("Failed to parse address %q", endpoint)
	}
	var addresses []net.IP
	if host == "" {
		addresses = []net.IP{net.IPv4zero}
	} else if parsed := net.ParseIP(host); parsed != nil {
		addresses = []net.IP{parsed}
	} else {
		addresses, err = net.LookupIP(host)
		if err != nil || len(addresses) == 0 {
			return socketTarget{}, 0, fmt.Sprintf(
				"php_network_getaddresses: getaddrinfo for %s failed: Name or service not known",
				host,
			)
		}
	}
	for _, address := range addresses {
		if address4 := address.To4(); address4 != nil {
			target.domain = syscall.AF_INET
			var bytes [4]byte
			copy(bytes[:], address4)
			target.address = &syscall.SockaddrInet4{Port: port, Addr: bytes}
			return target, 0, ""
		}
	}
	address16 := addresses[0].To16()
	if address16 == nil {
		return socketTarget{}, 0, fmt.Sprintf("Failed to parse address %q", endpoint)
	}
	target.domain = syscall.AF_INET6
	var bytes [16]byte
	copy(bytes[:], address16)
	target.address = &syscall.SockaddrInet6{Port: port, Addr: bytes}
	return target, 0, ""
}

func SocketCreateResult(Success bool, value php.Value, errno int64, Message string) php.Value {
	result := php.NewArray()
	result.Append(php.Bool(Success))
	result.Append(value)
	result.Append(php.Int(errno))
	result.Append(php.String(Message))
	return php.Arr(result)
}

func socketFailure(err error) (int64, string) {
	if err == nil {
		return 0, ""
	}
	return SocketErrno(err)
}

func socketContextBacklog(context *StreamContext) int {
	backlog, found := context.Option("socket", "backlog")
	if !found || backlog.Int() < 0 {
		return syscall.SOMAXCONN
	}
	return int(backlog.Int())
}

func SocketServerResult(ctx *php.Ctx, address string, flags int,
	context *StreamContext) php.Value {
	target, errno, Message := socketTargetFor(address)
	if Message != "" {
		return SocketCreateResult(false, php.Bool(false), errno, Message)
	}
	fd, err := syscall.Socket(target.domain, target.typeID, target.protocol)
	if err != nil {
		errno, Message = socketFailure(err)
		return SocketCreateResult(false, php.Bool(false), errno, Message)
	}
	failed := true
	defer func() {
		if failed {
			_ = syscall.Close(fd)
		}
	}()
	if target.domain != syscall.AF_UNIX {
		_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	}
	if flags&4 != 0 {
		if err := syscall.Bind(fd, target.address); err != nil {
			errno, Message = socketFailure(err)
			return SocketCreateResult(false, php.Bool(false), errno, Message)
		}
	}
	if flags&8 != 0 {
		if target.typeID == syscall.SOCK_DGRAM {
			return SocketCreateResult(false, php.Bool(false), 0, "")
		}
		if err := syscall.Listen(fd, socketContextBacklog(context)); err != nil {
			errno, Message = socketFailure(err)
			return SocketCreateResult(false, php.Bool(false), errno, Message)
		}
	}
	failed = false
	return SocketCreateResult(
		true,
		newAddressedSocketStream(
			ctx, fd, target.domain, target.typeID, target.protocol,
			target.transport, context, flags&8 != 0,
		),
		0,
		"",
	)
}

func socketTimeout(seconds float64) *syscall.Timeval {
	if seconds < 0 {
		return nil
	}
	whole := int64(seconds)
	microseconds := int64((seconds - float64(whole)) * 1_000_000)
	if microseconds < 0 {
		microseconds = 0
	}
	return &syscall.Timeval{Sec: whole, Usec: microseconds}
}

func socketConnect(fd int, address syscall.Sockaddr, seconds float64,
	asynchronous bool) error {
	if err := syscall.SetNonblock(fd, true); err != nil {
		return err
	}
	err := syscall.Connect(fd, address)
	if err == nil {
		if asynchronous {
			return nil
		}
		return syscall.SetNonblock(fd, false)
	}
	if err != syscall.EINPROGRESS && err != syscall.EALREADY &&
		err != syscall.EWOULDBLOCK {
		return err
	}
	if asynchronous {
		return nil
	}
	var write syscall.FdSet
	if !streamSelectFDSet(fd, &write) {
		return syscall.EINVAL
	}
	ready, err := syscall.Select(fd+1, nil, &write, nil, socketTimeout(seconds))
	if err != nil {
		return err
	}
	if ready == 0 {
		return syscall.ETIMEDOUT
	}
	pending, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_ERROR)
	if err != nil {
		return err
	}
	if pending != 0 {
		return syscall.Errno(pending)
	}
	return syscall.SetNonblock(fd, false)
}

func CreateSocketClient(address string, flags int, timeout float64,
	context *StreamContext) (*Stream, int64, string) {
	target, errno, Message := socketTargetFor(address)
	if Message != "" {
		return nil, errno, Message
	}
	fd, err := syscall.Socket(target.domain, target.typeID, target.protocol)
	if err != nil {
		errno, Message = socketFailure(err)
		return nil, errno, Message
	}
	failed := true
	defer func() {
		if failed {
			_ = syscall.Close(fd)
		}
	}()
	asynchronous := flags&2 != 0
	if flags&(2|4) != 0 {
		if err := socketConnect(fd, target.address, timeout, asynchronous); err != nil {
			errno, Message = socketFailure(err)
			return nil, errno, Message
		}
	}
	failed = false
	return addressedSocketStream(
		fd, target.domain, target.typeID, target.protocol,
		target.transport, context, false,
	), 0, ""
}

func SocketAcceptResult(ctx *php.Ctx, held *Stream, timeout float64) php.Value {
	failure := func(errno int64, Message string) php.Value {
		return SocketCreateResult(false, php.Bool(false), errno, Message)
	}
	if held == nil || !held.socket || held.file == nil || !held.socketListening {
		return failure(0, "Unknown error")
	}
	fd := int(held.file.Fd())
	var read syscall.FdSet
	if !streamSelectFDSet(fd, &read) {
		return failure(int64(syscall.EINVAL), "Invalid argument")
	}
	ready, err := syscall.Select(fd+1, &read, nil, nil, socketTimeout(timeout))
	if err != nil {
		errno, Message := socketFailure(err)
		return failure(errno, Message)
	}
	if ready == 0 {
		return failure(int64(syscall.ETIMEDOUT), "Connection timed out")
	}
	accepted, peer, err := syscall.Accept(fd)
	if err != nil {
		errno, Message := socketFailure(err)
		return failure(errno, Message)
	}
	if !held.blocked {
		_ = syscall.SetNonblock(accepted, true)
	}
	value := newAddressedSocketStream(
		ctx, accepted, held.socketDomain, held.socketType, held.socketProtocol,
		held.socketTransport, held.context, false,
	)
	result := php.NewArray()
	result.Append(php.Bool(true))
	result.Append(value)
	result.Append(php.String(socketAddress(peer)))
	result.Append(php.Int(0))
	result.Append(php.String(""))
	return php.Arr(result)
}

func socketSendTo(held *Stream, data, address string, flags int) (int, int64, string) {
	if held == nil || !held.socket || held.file == nil {
		return -1, 0, "Unknown error"
	}
	target, errno, Message := socketTargetFor(held.socketTransport + "://" + address)
	if Message != "" {
		return -1, errno, Message
	}
	if target.domain != held.socketDomain || target.typeID != held.socketType {
		return -1, 0, fmt.Sprintf("Failed to parse address %q", address)
	}
	if err := syscall.Sendto(
		int(held.file.Fd()), []byte(data), flags|syscall.MSG_NOSIGNAL, target.address,
	); err != nil {
		errno, Message = socketFailure(err)
		return -1, errno, Message
	}
	return len(data), 0, ""
}

func socketName(held *Stream, remote bool) php.Value {
	if held == nil || !held.socket || held.file == nil {
		return php.Bool(false)
	}
	var (
		address syscall.Sockaddr
		err     error
	)
	if remote {
		address, err = syscall.Getpeername(int(held.file.Fd()))
	} else {
		address, err = syscall.Getsockname(int(held.file.Fd()))
	}
	if err != nil {
		return php.Bool(false)
	}
	formatted := socketAddress(address)
	if formatted == "" || formatted == "@" || formatted == "0.0.0.0:0" || formatted == "[::]:0" {
		return php.Bool(false)
	}
	return php.String(formatted)
}

func socketReceive(held *Stream, length, flags int) php.Value {
	result := php.NewArray()
	if held == nil || !held.socket || held.file == nil {
		result.Append(php.Bool(false))
		return php.Arr(result)
	}
	buffer := make([]byte, length)
	n, address, err := syscall.Recvfrom(int(held.file.Fd()), buffer, flags)
	if err != nil {
		result.Append(php.Bool(false))
		return php.Arr(result)
	}
	result.Append(php.Bool(true))
	result.Append(php.String(string(buffer[:n])))
	result.Append(php.String(socketAddress(address)))
	return php.Arr(result)
}

func socketSend(held *Stream, data string, flags int) php.Value {
	if held == nil || !held.socket || held.file == nil {
		return php.Int(-1)
	}
	n, err := syscall.SendmsgN(
		int(held.file.Fd()),
		[]byte(data),
		nil,
		nil,
		flags|syscall.MSG_NOSIGNAL,
	)
	if err != nil {
		return php.Bool(false)
	}
	return php.Int(int64(n))
}

func streamSelectFDSet(fd int, set *syscall.FdSet) bool {
	width := int(unsafe.Sizeof(set.Bits[0]) * 8)
	word := fd / width
	if fd < 0 || word >= len(set.Bits) {
		return false
	}
	set.Bits[word] |= 1 << uint(fd%width)
	return true
}

func streamSelectFDIsSet(fd int, set *syscall.FdSet) bool {
	width := int(unsafe.Sizeof(set.Bits[0]) * 8)
	word := fd / width
	if fd < 0 || word >= len(set.Bits) {
		return false
	}
	return set.Bits[word]&(1<<uint(fd%width)) != 0
}

func StreamSelectEntries(value php.Value, set *syscall.FdSet) ([]streamSelectEntry, int, bool) {
	array := value.Array()
	if array == nil {
		return nil, -1, true
	}
	entries := make([]streamSelectEntry, 0, array.Len())
	maxFD := -1
	ok := true
	array.EachRaw(func(key php.Key, raw php.Value) bool {
		held, Handle, valid := AsStream(raw)
		if !valid {
			ok = false
			return false
		}
		Entry := streamSelectEntry{
			key:    key,
			value:  raw.Deref().Copy(),
			Handle: Handle,
			Stream: held,
		}
		entries = append(entries, Entry)
		if fd, hasDescriptor := held.descriptor(); hasDescriptor {
			if !streamSelectFDSet(fd, set) {
				ok = false
				return false
			}
			if fd > maxFD {
				maxFD = fd
			}
		}
		return true
	})
	return entries, maxFD, ok
}

func streamSelectReady(Entry streamSelectEntry, mode streamSelectMode, set *syscall.FdSet) bool {
	if Entry.Stream.mem != nil {
		return mode != StreamSelectExcept
	}
	fd, ok := Entry.Stream.descriptor()
	return ok && streamSelectFDIsSet(fd, set)
}

func StreamSelectResult(entries []streamSelectEntry, mode streamSelectMode, set *syscall.FdSet) (*php.Array, int) {
	result := php.NewArray()
	seenFiles := map[int]struct{}{}
	seenMemory := map[*php.Resource]struct{}{}
	ready := 0
	for _, Entry := range entries {
		if !streamSelectReady(Entry, mode, set) {
			continue
		}
		result.Set(Entry.key, Entry.value)
		if Entry.Stream.mem != nil {
			if _, found := seenMemory[Entry.Handle]; !found {
				seenMemory[Entry.Handle] = struct{}{}
				ready++
			}
			continue
		}
		fd, _ := Entry.Stream.descriptor()
		if _, found := seenFiles[fd]; !found {
			seenFiles[fd] = struct{}{}
			ready++
		}
	}
	return result, ready
}

func StreamSelectTimeout(seconds, microseconds php.Value, immediate bool) *syscall.Timeval {
	if immediate {
		return &syscall.Timeval{}
	}
	if seconds.Deref().Kind() == php.KindNull {
		return nil
	}
	sec := seconds.Int()
	usec := microseconds.Int()
	sec += usec / 1_000_000
	usec %= 1_000_000
	return &syscall.Timeval{Sec: sec, Usec: usec}
}

func AppendFileResult(Success bool, diagnostics ...[2]string) php.Value {
	result := php.NewArray()
	result.Append(php.Bool(Success))
	messages := php.NewArray()
	for _, diagnostic := range diagnostics {
		Entry := php.NewArray()
		Entry.Append(php.String(diagnostic[0]))
		Entry.Append(php.String(diagnostic[1]))
		messages.Append(php.Arr(Entry))
	}
	result.Append(php.Arr(messages))
	return php.Arr(result)
}

func HasStreamScheme(candidate string) bool {
	_, found := StreamScheme(candidate)
	return found
}

func StreamScheme(candidate string) (string, bool) {
	length := 0
	for length < len(candidate) {
		character := candidate[length]
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '+' || character == '-' || character == '.') {
			break
		}
		length++
	}
	if length <= 1 || len(candidate) < length+3 || candidate[length:length+3] != "://" {
		return "", false
	}
	return candidate[:length], true
}

func SplitIncludePath(includePath string) []string {
	var result []string
	for start := 0; start < len(includePath); {
		search := start
		if scheme, found := StreamScheme(includePath[start:]); found && scheme != ".." {
			search += len(scheme) + 3
		}
		separator := strings.IndexByte(includePath[search:], byte(os.PathListSeparator))
		if separator < 0 {
			result = append(result, includePath[start:])
			break
		}
		separator += search
		result = append(result, includePath[start:separator])
		start = separator + 1
	}
	return result
}

type PathMutationResult struct {
	Success bool
	Stage   string
	Message string
}

func PathMutationFailure(Stage string, errorNumber int) PathMutationResult {
	_, Message := SocketErrno(syscall.Errno(errorNumber))
	return PathMutationResult{Stage: Stage, Message: Message}
}

func LocalFilePath(candidate string, fromIncludePath bool) (string, bool, string) {
	scheme, found := StreamScheme(candidate)
	if !found || (fromIncludePath && scheme == "..") {
		return candidate, true, ""
	}
	if !strings.EqualFold(scheme, "file") {
		switch strings.ToLower(scheme) {
		case "php", "glob", "data", "http", "ftp":
			return "", false, ""
		default:
			return "", false, scheme
		}
	}
	rest := candidate[len(scheme)+3:]
	if len(rest) >= len("localhost/") &&
		strings.EqualFold(rest[:len("localhost/")], "localhost/") {
		rest = rest[len("localhost"):]
	}
	if !strings.HasPrefix(rest, "/") {
		return "", false, ""
	}
	return rest, true, ""
}

// DataStream opens a `data:` URI, whose payload is the rest of the string:
// `data:<mediatype>[;base64],<payload>`. Neither the media type nor the
// parameters matter to a reader; only whether the payload is base64.
func DataStream(path string) (*Stream, bool) {
	if !strings.HasPrefix(strings.ToLower(path), "data:") {
		return nil, false
	}
	comma := strings.IndexByte(path, ',')
	if comma < 0 {
		return nil, false
	}
	header, payload := path[len("data:"):comma], path[comma+1:]
	mediaType := strings.SplitN(header, ";", 2)[0]
	if mediaType == "" {
		mediaType = "text/plain"
	}
	body := []byte(payload)
	isBase64 := false
	for _, part := range strings.Split(header, ";") {
		if strings.EqualFold(strings.TrimSpace(part), "base64") {
			decoded, err := base64.StdEncoding.DecodeString(payload)
			if err != nil {
				return nil, false
			}
			body = decoded
			isBase64 = true
			break
		}
	}
	if !isBase64 {
		decoded, err := url.QueryUnescape(payload)
		if err != nil {
			return nil, false
		}
		body = []byte(decoded)
	}
	return &Stream{
		mem:       bytes.NewReader(body),
		data:      true,
		mediaType: mediaType,
		base64:    isBase64,
	}, true
}

func (s *Stream) Metadata() *php.Array {
	result := php.NewArray()
	set := func(Name string, value php.Value) {
		result.Set(php.StringKey(Name), value)
	}
	if s.user != nil && s.engineStreamType != "" {
		// A temp stream reports its own metadata, which leaves out the three
		// the streams layer would otherwise add.
		if s.engineStreamType != "TEMP" {
			set("timed_out", php.Bool(false))
			set("blocked", php.Bool(s.blocked))
			set("eof", php.Bool(s.eof))
		}
		set("wrapper_type", php.String(s.engineWrapper))
		set("stream_type", php.String(s.engineStreamType))
		set("mode", php.String(s.mode))
		set("unread_bytes", php.Int(0))
		set("seekable", php.Bool(true))
		set("uri", php.String(s.uri))
		return result
	}
	if s.user != nil {
		set("timed_out", php.Bool(false))
		set("blocked", php.Bool(s.blocked))
		set("eof", php.Bool(s.eof))
		set("wrapper_data", php.Obj(s.user.WrapperObject()))
		set("wrapper_type", php.String("user-space"))
		set("stream_type", php.String("user-space"))
		set("mode", php.String(s.mode))
		set("unread_bytes", php.Int(int64(s.user.Unread())))
		set("seekable", php.Bool(s.user.Seekable()))
		set("uri", php.String(s.uri))
		return result
	}
	if s.socket {
		set("timed_out", php.Bool(s.timedOut))
		set("blocked", php.Bool(s.blocked))
		set("eof", php.Bool(s.eof))
		streamType := "generic_socket"
		if s.socketTransport != "" {
			streamType = s.socketTransport + "_socket"
		}
		set("stream_type", php.String(streamType))
		set("mode", php.String("r+"))
		set("unread_bytes", php.Int(0))
		set("seekable", php.Bool(false))
		return result
	}
	if s.pipe != nil {
		set("timed_out", php.Bool(false))
		set("blocked", php.Bool(s.blocked))
		set("eof", php.Bool(s.eof))
		set("stream_type", php.String("STDIO"))
		set("mode", php.String(s.mode))
		set("unread_bytes", php.Int(0))
		set("seekable", php.Bool(false))
		return result
	}
	if s.data {
		set("mediatype", php.String(s.mediaType))
		set("base64", php.Bool(s.base64))
		set("wrapper_type", php.String("RFC2397"))
		set("stream_type", php.String("RFC2397"))
	} else {
		set("timed_out", php.Bool(false))
		set("blocked", php.Bool(s.blocked))
		set("eof", php.Bool(s.eof))
		set("wrapper_type", php.String("plainfile"))
		set("stream_type", php.String("STDIO"))
	}
	set("mode", php.String(s.mode))
	set("unread_bytes", php.Int(0))
	set("seekable", php.Bool(true))
	set("uri", php.String(s.uri))
	return result
}

func DirectoryMetadata() *php.Array {
	result := php.NewArray()
	set := func(Name string, value php.Value) {
		result.Set(php.StringKey(Name), value)
	}
	set("timed_out", php.Bool(false))
	set("blocked", php.Bool(true))
	set("eof", php.Bool(false))
	set("wrapper_type", php.String("plainfile"))
	set("stream_type", php.String("dir"))
	set("mode", php.String("r"))
	set("unread_bytes", php.Int(0))
	set("seekable", php.Bool(true))
	return result
}

func GlobPatternBase(pattern string, brace, noEscape bool) (string, string) {
	magic := -1
	escaped := false
	for index := 0; index < len(pattern); index++ {
		character := pattern[index]
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' && !noEscape {
			escaped = true
			continue
		}
		if character == '*' || character == '?' || character == '[' ||
			(brace && character == '{') {
			magic = index
			break
		}
	}
	limit := len(pattern)
	if magic >= 0 {
		limit = magic
	}
	slash := strings.LastIndexByte(pattern[:limit], '/')
	if slash < 0 {
		return ".", pattern
	}
	if slash == 0 {
		return "/", pattern[1:]
	}
	return pattern[:slash], pattern[slash+1:]
}

func GlobBraceExpand(pattern string, escapes bool) []string {
	start := -1
	escaped := false
	for index := 0; index < len(pattern); index++ {
		if escaped {
			escaped = false
			continue
		}
		if escapes && pattern[index] == '\\' {
			escaped = true
			continue
		}
		if pattern[index] == '{' {
			start = index
			break
		}
	}
	if start < 0 {
		return []string{pattern}
	}
	depth := 0
	escaped = false
	end := -1
	for index := start + 1; index < len(pattern); index++ {
		if escaped {
			escaped = false
			continue
		}
		if escapes && pattern[index] == '\\' {
			escaped = true
			continue
		}
		switch pattern[index] {
		case '{':
			depth++
		case '}':
			if depth == 0 {
				end = index
				index = len(pattern)
			} else {
				depth--
			}
		}
	}
	if end < 0 {
		return []string{pattern}
	}
	body := pattern[start+1 : end]
	var alternatives []string
	depth = 0
	escaped = false
	begin := 0
	for index := 0; index <= len(body); index++ {
		if index == len(body) {
			alternatives = append(alternatives, body[begin:])
			break
		}
		if escaped {
			escaped = false
			continue
		}
		if escapes && body[index] == '\\' {
			escaped = true
			continue
		}
		switch body[index] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				alternatives = append(alternatives, body[begin:index])
				begin = index + 1
			}
		}
	}
	if len(alternatives) < 2 {
		return []string{pattern}
	}
	var expanded []string
	for _, alternative := range alternatives {
		candidate := pattern[:start] + alternative + pattern[end+1:]
		expanded = append(expanded, GlobBraceExpand(candidate, escapes)...)
	}
	return expanded
}

// ReadLine reads to the next newline, keeping it, which is what fgets does.
func ReadLine(held *Stream, operation string) (string, error) {
	var out []byte
	buf := make([]byte, 1)
	for {
		n, err := held.ReadFiltered(buf, operation)
		if n == 0 || err != nil {
			if err == io.EOF {
				held.eof = true
			}
			if len(out) == 0 {
				return "", err
			}
			return string(out), nil
		}
		out = append(out, buf[0])
		if buf[0] == '\n' {
			return string(out), nil
		}
	}
}

// StandardStream answers STDIN, STDOUT and STDERR. The CLI defines them as
// resources 1, 2 and 3 before a script runs; they are built on first mention so
// a script that never names one allocates nothing, and the ids are fixed rather
// than handed out, since the script's own resources start above them.
func StandardStream(ctx *php.Ctx, Name string) (php.Value, bool) {
	var id int
	var file *os.File
	switch Name {
	case "STDIN":
		id, file = 1, os.Stdin
	case "STDOUT":
		id, file = 2, os.Stdout
	case "STDERR":
		id, file = 3, os.Stderr
	default:
		return php.Undef(), false
	}
	if held, ok := ctx.StandardStream(Name); ok {
		return php.Res(held), true
	}
	mode := "wb"
	uri := "php://" + strings.ToLower(Name)
	if Name == "STDIN" {
		mode = "rb"
	}
	Handle := &php.Resource{
		ID:   id,
		Type: "stream",
		Payload: &Stream{
			file:      file,
			blocked:   true,
			chunkSize: 8192,
			mode:      mode,
			uri:       uri,
		},
	}
	if file == os.Stdout {
		Handle.Payload.(*Stream).directWrite = ctx.WriteDirect
	}
	ctx.SetStandardStream(Name, Handle)
	return php.Res(Handle), true
}

func (s *Stream) Flush() (bool, error) {
	if s.user != nil {
		return s.user.Flush()
	}
	return true, nil
}

func (s *Stream) Sync(dataOnly bool) (int, error) {
	if s.file == nil || s.socket {
		// Unsupported: the PHP layer owns the public warning, which is why
		// this is a status rather than a bool.
		return 1, nil
	}
	var err error
	if dataOnly {
		err = syscall.Fdatasync(int(s.file.Fd()))
	} else {
		err = s.file.Sync()
	}
	if err != nil {
		// Supported but failed: PHP returns false without the
		// unsupported-capability warning.
		return 2, nil
	}
	return 0, nil
}

func (s *Stream) Truncate(size int64) (bool, error) {
	if s.user != nil {
		return s.user.Truncate(size)
	}
	return s.truncate(size), nil
}

func (s *Stream) Stat() ([]int64, bool, error) {
	if s.user != nil {
		return s.user.StatValues()
	}
	values, ok := s.statValues()
	return values, ok, nil
}

func (s *Stream) SupportsLock() bool {
	if s.user != nil {
		return s.user.SupportsLock()
	}
	return s.file != nil && !s.socket
}

func (s *Stream) IsTTY() bool { return s.isTTY() }

func (s *Stream) Lock(operation int64) (bool, bool, error) {
	if s.user != nil {
		return s.user.Lock(operation)
	}
	locked, wouldBlock := s.lock(operation)
	return locked, wouldBlock, nil
}

func (s *Stream) SetBlocking(blocked bool) (bool, error) {
	if s.user != nil {
		set, err := s.user.SetOption(1, boolInt(blocked), 0)
		if err != nil {
			return false, err
		}
		if !set {
			return false, nil
		}
	}
	if s.socket || s.pipe != nil {
		fd, ok := s.descriptor()
		if !ok || syscall.SetNonblock(fd, !blocked) != nil {
			return false, nil
		}
	}
	s.blocked = blocked
	return true, nil
}

func (s *Stream) SetTimeout(seconds, microseconds int64) (bool, error) {
	if s.user != nil {
		return s.user.SetOption(4, seconds, microseconds)
	}
	if !s.socket || s.file == nil {
		return false, nil
	}
	seconds += microseconds / 1_000_000
	microseconds %= 1_000_000
	timeout := &syscall.Timeval{Sec: seconds, Usec: microseconds}
	if err := syscall.SetsockoptTimeval(
		int(s.file.Fd()),
		syscall.SOL_SOCKET,
		syscall.SO_RCVTIMEO,
		timeout,
	); err != nil {
		return false, nil
	}
	s.timedOut = false
	return true, nil
}

func (s *Stream) SetReadBuffer(size int64) (int, error) {
	if s.user != nil {
		return s.userBufferOption(2, size)
	}
	// The generic Stream layer accepts this even where the wrapper has no
	// buffer of its own.
	return 0, nil
}

func (s *Stream) SetWriteBuffer(size int64) (int, error) {
	if s.user != nil {
		return s.userBufferOption(3, size)
	}
	// The linked engine's stdio and RFC2397 handles both report EOF for this
	// operation in the supported configurations.
	return -1, nil
}

// userBufferOption is the shared half of the two buffer options: a wrapper that
// accepted the option reports 0 and one that did not reports -1.
func (s *Stream) userBufferOption(option int64, size int64) (int, error) {
	set, err := s.user.SetOption(option, size, 0)
	if err != nil {
		return 0, err
	}
	if set {
		return 0, nil
	}
	return -1, nil
}

func (s *Stream) SetChunkSize(size int64) int {
	previous := s.chunkSize
	if previous == 0 {
		previous = 8192
	}
	s.chunkSize = int(size)
	return previous
}

func (s *Stream) SocketName(remote bool) php.Value { return socketName(s, remote) }

func (s *Stream) SocketReceive(length, flags int) php.Value {
	return socketReceive(s, length, flags)
}

func (s *Stream) SocketSend(data string, flags int) php.Value {
	return socketSend(s, data, flags)
}

func (s *Stream) SocketSendTo(data, address string, flags int) (int, int64, string) {
	return socketSendTo(s, data, address, flags)
}

func (s *Stream) SocketShutdown(how int) bool {
	if !s.socket || s.file == nil {
		return false
	}
	return syscall.Shutdown(int(s.file.Fd()), how) == nil
}

const (
	StreamFilterErrFatal = 0
	streamFilterFeedMe   = 1
	streamFilterPassOn   = 2

	StreamFilterRead  = 1
	StreamFilterWrite = 2
)

var ErrStreamFilterFatal = errors.New("Stream filter failed")

var errStreamFilterInvalidSequence = errors.New("Stream filter invalid byte sequence")

var BuiltinStreamFilterNames = []string{
	"string.rot13",
	"string.toupper",
	"string.tolower",
	"convert.*",
	"consumed",
	"dechunk",
}

type StreamBucket struct {
	Data    string
	brigade *StreamBrigade
}

type StreamBrigade struct {
	Buckets []*StreamBucket
}

func (b *StreamBrigade) detach(bucket *StreamBucket) {
	if b == nil || bucket == nil {
		return
	}
	for index, candidate := range b.Buckets {
		if candidate != bucket {
			continue
		}
		b.Buckets = append(b.Buckets[:index], b.Buckets[index+1:]...)
		bucket.brigade = nil
		return
	}
}

func (b *StreamBrigade) Attach(bucket *StreamBucket, appendBucket bool) {
	if b == nil || bucket == nil {
		return
	}
	if bucket.brigade != nil {
		bucket.brigade.detach(bucket)
	}
	bucket.brigade = b
	if appendBucket {
		b.Buckets = append(b.Buckets, bucket)
		return
	}
	b.Buckets = append([]*StreamBucket{bucket}, b.Buckets...)
}

func (b *StreamBrigade) Pop() *StreamBucket {
	if b == nil || len(b.Buckets) == 0 {
		return nil
	}
	bucket := b.Buckets[0]
	b.Buckets = b.Buckets[1:]
	bucket.brigade = nil
	return bucket
}

func (b *StreamBrigade) Text() string {
	var out strings.Builder
	for _, bucket := range b.Buckets {
		out.WriteString(bucket.Data)
	}
	return out.String()
}

type StreamFilterRun func(input string, closing bool, operation string) (
	output string, consumed int, status int, err error,
)

type StreamFilter struct {
	Name   string
	Stream *Stream
	Handle *php.Resource
	Object *php.Object
	// class is the user filter's class, which only the interpreter can make
	// sense of; nothing here reads it. Typed `any` for the same reason
	// `Resource.Payload` is: the owner knows what it put there.
	class     any
	run       StreamFilterRun
	close     func(string) error
	read      bool
	removed   bool
	converter *BuiltinStreamConverter
}

type BuiltinStreamConverter struct {
	Name           string
	pending        []byte
	decodeBits     uint32
	decodeBitCount uint
	lineLength     int
	lineBreak      string
	lineUsed       int
	forceEncode    bool
	chunkRemaining int
	chunkDelimiter bool
	chunkDone      bool
}

func (c *BuiltinStreamConverter) run(input string, closing bool) (string, int, int, error) {
	consumed := len(input)
	switch c.Name {
	case "string.rot13":
		return rot13Bytes(input), consumed, streamFilterPassOn, nil
	case "string.toupper":
		return ToUpperASCII(input), consumed, streamFilterPassOn, nil
	case "string.tolower":
		return ToLowerASCII(input), consumed, streamFilterPassOn, nil
	case "consumed":
		return input, consumed, streamFilterPassOn, nil
	}

	switch strings.ToLower(c.Name) {
	case "convert.base64-encode":
		c.pending = append(c.pending, input...)
		length := len(c.pending) / 3 * 3
		if closing {
			length = len(c.pending)
		}
		if length == 0 {
			return "", consumed, streamFilterFeedMe, nil
		}
		encoded := c.wrapBase64(base64.StdEncoding.EncodeToString(c.pending[:length]))
		c.pending = append(c.pending[:0], c.pending[length:]...)
		return encoded, consumed, streamFilterPassOn, nil
	case "convert.base64-decode":
		decoded, ok := c.decodeBase64(input)
		if !ok {
			return "", consumed, StreamFilterErrFatal,
				errors.Join(ErrStreamFilterFatal, errStreamFilterInvalidSequence)
		}
		if decoded == "" && !closing {
			return "", consumed, streamFilterFeedMe, nil
		}
		return decoded, consumed, streamFilterPassOn, nil
	case "convert.quoted-printable-encode":
		encoded := c.encodeQuotedPrintable(input)
		if encoded == "" && !closing {
			return "", consumed, streamFilterFeedMe, nil
		}
		return encoded, consumed, streamFilterPassOn, nil
	case "convert.quoted-printable-decode":
		decoded, ok := c.decodeQuotedPrintable(input, closing)
		if !ok {
			return "", consumed, StreamFilterErrFatal,
				errors.Join(ErrStreamFilterFatal, errStreamFilterInvalidSequence)
		}
		if decoded == "" && !closing {
			return "", consumed, streamFilterFeedMe, nil
		}
		return decoded, consumed, streamFilterPassOn, nil
	case "dechunk":
		decoded := c.decodeChunks(input, closing)
		if decoded == "" && !closing {
			return "", consumed, streamFilterFeedMe, nil
		}
		return decoded, consumed, streamFilterPassOn, nil
	default:
		return "", consumed, StreamFilterErrFatal, ErrStreamFilterFatal
	}
}

func (c *BuiltinStreamConverter) reset() {
	Name := c.Name
	lineLength := c.lineLength
	lineBreak := c.lineBreak
	forceEncode := c.forceEncode
	*c = BuiltinStreamConverter{
		Name:        Name,
		lineLength:  lineLength,
		lineBreak:   lineBreak,
		forceEncode: forceEncode,
	}
}

func (c *BuiltinStreamConverter) wrapBase64(encoded string) string {
	if c.lineLength < 4 || encoded == "" {
		return encoded
	}
	var out strings.Builder
	for len(encoded) > 0 {
		if c.lineUsed > 0 && c.lineUsed+4 > c.lineLength {
			out.WriteString(c.lineBreak)
			c.lineUsed = 0
		}
		length := 4
		if length > len(encoded) {
			length = len(encoded)
		}
		out.WriteString(encoded[:length])
		encoded = encoded[length:]
		c.lineUsed += length
	}
	return out.String()
}

func (c *BuiltinStreamConverter) encodeQuotedPrintable(input string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for index := 0; index < len(input); index++ {
		char := input[index]
		var token string
		if !c.forceEncode && (char == ' ' || char == '\t' || (char >= 33 && char <= 60) ||
			(char >= 62 && char <= 126)) {
			token = string([]byte{char})
		} else {
			token = string([]byte{'=', hex[char>>4], hex[char&15]})
		}
		if c.lineLength >= 4 && c.lineUsed > 0 &&
			c.lineUsed+len(token) > c.lineLength-1 {
			out.WriteByte('=')
			out.WriteString(c.lineBreak)
			c.lineUsed = 0
		}
		out.WriteString(token)
		c.lineUsed += len(token)
	}
	return out.String()
}

func base64Sextet(char byte) (uint32, bool) {
	switch {
	case char >= 'A' && char <= 'Z':
		return uint32(char - 'A'), true
	case char >= 'a' && char <= 'z':
		return uint32(char-'a') + 26, true
	case char >= '0' && char <= '9':
		return uint32(char-'0') + 52, true
	case char == '+':
		return 62, true
	case char == '/':
		return 63, true
	default:
		return 0, false
	}
}

func (c *BuiltinStreamConverter) decodeBase64(input string) (string, bool) {
	var out strings.Builder
	for index := 0; index < len(input); index++ {
		char := input[index]
		if char == ' ' || char == '\t' || char == '\r' || char == '\n' {
			continue
		}
		if char == '=' {
			if c.decodeBitCount == 6 {
				return "", false
			}
			c.decodeBits = 0
			c.decodeBitCount = 0
			continue
		}
		sextet, ok := base64Sextet(char)
		if !ok {
			continue
		}
		c.decodeBits = c.decodeBits<<6 | sextet
		c.decodeBitCount += 6
		for c.decodeBitCount >= 8 {
			c.decodeBitCount -= 8
			out.WriteByte(byte(c.decodeBits >> c.decodeBitCount))
			if c.decodeBitCount == 0 {
				c.decodeBits = 0
			} else {
				c.decodeBits &= 1<<c.decodeBitCount - 1
			}
		}
	}
	return out.String(), true
}

func quotedPrintableHex(char byte) (byte, bool) {
	switch {
	case char >= '0' && char <= '9':
		return char - '0', true
	case char >= 'A' && char <= 'F':
		return char - 'A' + 10, true
	case char >= 'a' && char <= 'f':
		return char - 'a' + 10, true
	default:
		return 0, false
	}
}

func (c *BuiltinStreamConverter) decodeQuotedPrintable(input string, closing bool) (string, bool) {
	c.pending = append(c.pending, input...)
	var out strings.Builder
	index := 0
	for index < len(c.pending) {
		if c.pending[index] != '=' {
			out.WriteByte(c.pending[index])
			index++
			continue
		}
		if index+1 >= len(c.pending) {
			break
		}
		if c.pending[index+1] == '\n' {
			index += 2
			continue
		}
		if c.pending[index+1] == '\r' {
			if index+2 >= len(c.pending) {
				break
			}
			if c.pending[index+2] == '\n' {
				index += 3
				continue
			}
		}
		if index+2 >= len(c.pending) {
			if _, validPrefix := quotedPrintableHex(c.pending[index+1]); !validPrefix {
				c.pending = nil
				return "", false
			}
			break
		}
		high, highOK := quotedPrintableHex(c.pending[index+1])
		low, lowOK := quotedPrintableHex(c.pending[index+2])
		if highOK && lowOK {
			out.WriteByte(high<<4 | low)
			index += 3
			continue
		}
		c.pending = nil
		return "", false
	}
	c.pending = append(c.pending[:0], c.pending[index:]...)
	if closing {
		c.pending = nil
	}
	return out.String(), true
}

func (c *BuiltinStreamConverter) decodeChunks(input string, closing bool) string {
	c.pending = append(c.pending, input...)
	var out strings.Builder
	for !c.chunkDone {
		if c.chunkDelimiter {
			if len(c.pending) == 0 {
				break
			}
			if c.pending[0] == '\r' {
				if len(c.pending) == 1 {
					break
				}
				if c.pending[1] == '\n' {
					c.pending = c.pending[2:]
				} else {
					c.pending = c.pending[1:]
				}
			} else if c.pending[0] == '\n' {
				c.pending = c.pending[1:]
			}
			c.chunkDelimiter = false
			continue
		}
		if c.chunkRemaining > 0 {
			length := c.chunkRemaining
			if length > len(c.pending) {
				length = len(c.pending)
			}
			out.Write(c.pending[:length])
			c.pending = c.pending[length:]
			c.chunkRemaining -= length
			if c.chunkRemaining == 0 {
				c.chunkDelimiter = true
			}
			if length == 0 {
				break
			}
			continue
		}
		lineEnd := bytes.IndexByte(c.pending, '\n')
		if lineEnd < 0 {
			break
		}
		line := strings.TrimSuffix(string(c.pending[:lineEnd]), "\r")
		c.pending = c.pending[lineEnd+1:]
		if semi := strings.IndexByte(line, ';'); semi >= 0 {
			line = line[:semi]
		}
		var size int
		if _, err := fmt.Sscanf(strings.TrimSpace(line), "%x", &size); err != nil {
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		if size == 0 {
			c.chunkDone = true
			c.pending = nil
			break
		}
		c.chunkRemaining = size
	}
	if closing && !c.chunkDone && len(c.pending) > 0 {
		out.Write(c.pending)
		c.pending = nil
	}
	return out.String()
}

func rot13Bytes(input string) string {
	data := []byte(input)
	for index, item := range data {
		switch {
		case item >= 'a' && item <= 'z':
			data[index] = 'a' + (item-'a'+13)%26
		case item >= 'A' && item <= 'Z':
			data[index] = 'A' + (item-'A'+13)%26
		}
	}
	return string(data)
}

func NewBuiltinStreamFilter(ctx *php.Ctx, Name string, params php.Value,
	operation string,
) (*StreamFilter, bool) {
	found := false
	switch Name {
	case "string.rot13", "string.toupper", "string.tolower", "consumed", "dechunk":
		found = true
	default:
		if strings.HasPrefix(Name, "convert.") {
			found = true
			switch strings.ToLower(strings.TrimPrefix(Name, "convert.")) {
			case "base64-encode", "base64-decode",
				"quoted-printable-encode", "quoted-printable-decode":
			default:
				return nil, true
			}
		}
	}
	if !found {
		return nil, false
	}
	converter := &BuiltinStreamConverter{Name: Name}
	if strings.HasPrefix(Name, "convert.") && params.Kind() != php.KindUndef {
		if params.Kind() != php.KindArray {
			ctx.Warn("%s(): Stream filter (%s): invalid filter parameter", operation, Name)
			return nil, true
		}
		options := params.Array()
		if value, ok := options.Get(php.StringKey("line-length")); ok {
			converter.lineLength = int(value.Int())
		}
		if value, ok := options.Get(php.StringKey("line-break-chars")); ok {
			converter.lineBreak = value.String()
		} else {
			converter.lineBreak = "\r\n"
		}
		if converter.lineLength < 4 {
			converter.lineLength = 0
			converter.lineBreak = ""
		} else if strings.EqualFold(Name, "convert.base64-encode") {
			converter.lineLength = converter.lineLength / 4 * 4
		}
		if value, ok := options.Get(php.StringKey("force-encode-first")); ok {
			converter.forceEncode = value.Bool()
		}
	}
	filter := &StreamFilter{Name: Name, converter: converter}
	filter.run = func(input string, closing bool, operation string) (string, int, int, error) {
		output, consumed, status, err := converter.run(input, closing)
		if errors.Is(err, errStreamFilterInvalidSequence) {
			ctx.Warn("%s(): Stream filter (%s): invalid byte sequence", operation, Name)
		}
		return output, consumed, status, err
	}
	return filter, true
}

func BrigadeResource(value php.Value, function string) (*StreamBrigade, error) {
	Handle := value.Deref().Resource()
	if Handle == nil || Handle.Closed() {
		return nil, php.Raise("TypeError", "%s(): supplied resource is not a valid userfilter.bucket brigade resource", function)
	}
	brigade, ok := Handle.Payload.(*StreamBrigade)
	if !ok || brigade == nil {
		return nil, php.Raise("TypeError", "%s(): supplied resource is not a valid userfilter.bucket brigade resource", function)
	}
	return brigade, nil
}

func BucketFromObject(Object *php.Object, function string) (*StreamBucket, error) {
	if Object == nil {
		return nil, php.Raise("TypeError", "%s(): Argument #2 ($bucket) must be of type StreamBucket, null given", function)
	}
	resourceValue, found := Object.Get("bucket")
	if !found {
		return nil, php.Raise("ValueError", "%s(): Argument #2 ($bucket) must be an Object that has a \"bucket\" property", function)
	}
	Handle := resourceValue.Deref().Resource()
	if Handle == nil || Handle.Closed() {
		return nil, php.Raise("TypeError", "%s(): supplied resource is not a valid userfilter.bucket resource", function)
	}
	bucket, ok := Handle.Payload.(*StreamBucket)
	if !ok || bucket == nil {
		return nil, php.Raise("TypeError", "%s(): supplied resource is not a valid userfilter.bucket resource", function)
	}
	if data, found := Object.Get("data"); found {
		bucket.Data = data.Deref().String()
	}
	return bucket, nil
}

func (s *Stream) applyFilterChain(chain []*StreamFilter, input string, closing bool,
	operation string,
) (string, int, error) {
	consumed := len(input)
	current := input
	for index, filter := range chain {
		output, used, status, err := filter.run(current, closing, operation)
		if index == 0 {
			consumed = used
		}
		if err != nil {
			return "", consumed, err
		}
		switch status {
		case streamFilterPassOn:
			current = output
		case streamFilterFeedMe:
			return "", consumed, nil
		default:
			return "", consumed, ErrStreamFilterFatal
		}
	}
	return current, consumed, nil
}

func (s *Stream) ReadFiltered(p []byte, operation string) (int, error) {
	n, err := s.readFiltered(p, operation)
	if s.FilterPositionSet {
		s.FilteredPosition += int64(n)
	}
	return n, err
}

func (s *Stream) readFiltered(p []byte, operation string) (int, error) {
	if len(s.readFilters) == 0 {
		written := copy(p, s.filteredRead)
		s.filteredRead = s.filteredRead[written:]
		if written == len(p) {
			return written, nil
		}
		n, err := s.Read(p[written:])
		return written + n, err
	}
	written := 0
	for written < len(p) {
		if len(s.filteredRead) > 0 {
			n := copy(p[written:], s.filteredRead)
			s.filteredRead = s.filteredRead[n:]
			written += n
			if written == len(p) {
				return written, nil
			}
		}
		if s.readFiltersFlushed {
			if written > 0 {
				return written, nil
			}
			return 0, io.EOF
		}
		size := s.chunkSize
		if size <= 0 {
			size = 8192
		}
		if size < len(p)-written {
			size = len(p) - written
		}
		raw := make([]byte, size)
		n, err := s.Read(raw)
		if n > 0 {
			output, _, filterErr := s.applyFilterChain(s.readFilters, string(raw[:n]), false, operation)
			if filterErr != nil {
				return written, filterErr
			}
			s.filteredRead = append(s.filteredRead, output...)
		}
		if err == io.EOF || n == 0 {
			output, _, filterErr := s.applyFilterChain(s.readFilters, "", true, operation)
			s.readFiltersFlushed = true
			if filterErr != nil {
				return written, filterErr
			}
			s.filteredRead = append(s.filteredRead, output...)
			if n == 0 && len(s.filteredRead) == 0 {
				if written > 0 {
					return written, nil
				}
				return 0, io.EOF
			}
		} else if err != nil {
			return written, err
		}
	}
	return written, nil
}

func (s *Stream) ResetReadFilters() {
	s.filteredRead = nil
	s.readFiltersFlushed = false
	if s.user != nil {
		s.FilteredPosition = s.user.Position()
		s.FilterPositionSet = true
	} else if position, err := s.Seek(0, io.SeekCurrent); err == nil {
		s.FilteredPosition = position
		s.FilterPositionSet = true
	}
	for _, filter := range s.readFilters {
		if filter.converter != nil {
			filter.converter.reset()
		}
	}
}

func (s *Stream) WriteFiltered(text, operation string) (int, error) {
	if len(s.writeFilters) == 0 {
		written, err := s.WriteString(text)
		if s.user != nil && s.FilterPositionSet {
			s.FilteredPosition = s.user.Position()
		}
		return written, err
	}
	output, consumed, err := s.applyFilterChain(s.writeFilters, text, false, operation)
	if err != nil {
		return 0, err
	}
	if output != "" {
		if _, err := s.WriteString(output); err != nil {
			return 0, err
		}
	}
	if s.user != nil && s.FilterPositionSet {
		s.FilteredPosition = s.user.Position()
	}
	if consumed < 0 {
		consumed = 0
	}
	if consumed > len(text) {
		consumed = len(text)
	}
	return consumed, nil
}

func (s *Stream) RemoveFilter(filter *StreamFilter, operation string) error {
	chain := &s.writeFilters
	if filter.read {
		chain = &s.readFilters
	}
	index := -1
	for at, candidate := range *chain {
		if candidate == filter {
			index = at
			break
		}
	}
	if index < 0 {
		return ErrStreamFilterFatal
	}
	output, _, status, err := filter.run("", true, operation)
	if err != nil {
		return err
	}
	if status == StreamFilterErrFatal {
		return ErrStreamFilterFatal
	}
	if status == streamFilterPassOn && output != "" {
		if index+1 < len(*chain) {
			output, _, err = s.applyFilterChain((*chain)[index+1:], output, false, operation)
			if err != nil {
				return err
			}
		}
		if filter.read {
			s.filteredRead = append(s.filteredRead, output...)
		} else if _, err := s.WriteString(output); err != nil {
			return err
		}
	}
	*chain = append((*chain)[:index], (*chain)[index+1:]...)
	filter.removed = true
	if filter.close != nil {
		return filter.close(operation)
	}
	return nil
}

func (s *Stream) closeStreamFilters(operation string) error {
	var first error
	if len(s.writeFilters) > 0 {
		output, _, err := s.applyFilterChain(s.writeFilters, "", true, operation)
		if err == nil && output != "" {
			_, err = s.WriteString(output)
		}
		if err != nil {
			first = err
		}
	}
	if len(s.readFilters) > 0 && !s.readFiltersFlushed {
		_, _, err := s.applyFilterChain(s.readFilters, "", true, operation)
		if first == nil && err != nil {
			first = err
		}
		s.readFiltersFlushed = true
	}
	for _, chain := range [][]*StreamFilter{s.readFilters, s.writeFilters} {
		for _, filter := range chain {
			if filter.removed {
				continue
			}
			filter.removed = true
			if filter.close != nil {
				if err := filter.close(operation); first == nil && err != nil {
					first = err
				}
			}
			if filter.Handle != nil {
				filter.Handle.Close()
			}
		}
	}
	s.readFilters = nil
	s.writeFilters = nil
	return first
}

// The constructors the interpreter uses. A stream's fields are private because
// nothing outside this package should decide what one *is* -- but opening a
// user-space wrapper, a process pipe or a temporary file are all things only
// the interpreter can do, so it needs a way to say which kind it made.

// NewFileStream is a stream over an open file or socket descriptor.
func NewFileStream(file *os.File, mode, uri string) *Stream {
	return &Stream{file: file, blocked: true, chunkSize: 8192, mode: mode, uri: uri}
}

// NewTemporaryStream is NewFileStream for a file that is deleted with the
// stream, which is what `tmpfile()` and an overflowing `php://temp` produce.
func NewTemporaryStream(file *os.File, path string) *Stream {
	return &Stream{
		file: file, blocked: true, chunkSize: 8192,
		mode: "r+b", uri: path, tempName: path,
	}
}

// NewPipeStream is a stream over a child process, whose close has to go through
// pclose rather than the descriptor.
func NewPipeStream(pipe *libc.Pipe, mode string) *Stream {
	return &Stream{pipe: pipe, blocked: true, chunkSize: 8192, mode: mode}
}

// NewUserStream is a stream whose behaviour is a registered PHP class.
func NewUserStream(user UserStream, mode, uri string) *Stream {
	return &Stream{user: user, blocked: true, chunkSize: 8192, mode: mode, uri: uri}
}

// PresentAsEngine makes a stream behind one of php2go's own userland wrappers
// report itself as the engine's native stream of that kind -- the wrapper and
// stream types, and the mode the engine gives it -- with no wrapper_data.
func (s *Stream) PresentAsEngine(wrapper, streamType, mode string) {
	s.engineWrapper, s.engineStreamType, s.mode = wrapper, streamType, mode
}

// PresentsAsEngine reports a stream PresentAsEngine dressed. fread fills its
// buffer from one as it does from a plain file: the engine reads php://memory
// and php://temp greedily, and a user wrapper once.
func (s *Stream) PresentsAsEngine() bool { return s.engineStreamType != "" }

// The few facts about a stream the interpreter still has to read. They are
// methods rather than exported fields because what a stream *is* stays this
// package's business -- the interpreter only needs to know whether it is at the
// end, whether it is a socket, and whether a user wrapper is behind it.

// AtEOF reports whether the last read reached the end. PHP's `feof` answers
// about the last read rather than the position, so this cannot be derived.
func (s *Stream) AtEOF() bool { return s.eof }

// IsSocket reports whether the descriptor is a socket, which decides whether
// several operations are supported at all.
func (s *Stream) IsSocket() bool { return s.socket }

// IsBlocking reports the blocking mode, which `stream_get_meta_data` shows.
func (s *Stream) IsBlocking() bool { return s.blocked }

// User is the registered PHP class behind this stream, or nil.
func (s *Stream) User() UserStream { return s.user }

// SetEOF records that a read reached the end, which the caller that performed
// the read is the only one that knows.
func (s *Stream) SetEOF(at bool) { s.eof = at }

// TimedOut reports and clears the socket read timeout, which
// `stream_get_meta_data` shows and a successful read resets.
func (s *Stream) TimedOut() bool { return s.timedOut }

// SetTimedOut records that a socket read hit its timeout.
func (s *Stream) SetTimedOut(at bool) { s.timedOut = at }

// IsMemory reports an in-memory stream, which `select` treats as always ready
// because there is no descriptor to wait on.
func (s *Stream) IsMemory() bool { return s.mem != nil }

// MemoryReader is the buffer behind an in-memory stream, for a caller that
// wants to read the whole thing rather than go through the stream.
func (s *Stream) MemoryReader() *bytes.Reader { return s.mem }

// SetBlockingFlag records the blocking mode without touching the descriptor,
// for a caller that has already set it there.
func (s *Stream) SetBlockingFlag(blocked bool) { s.blocked = blocked }

// ChunkSize is the read granularity `stream_set_chunk_size` last set.
func (s *Stream) ChunkSize() int { return s.chunkSize }

// Configure sets the facts a caller that opened the stream elsewhere already
// knows: how it was opened and what it was opened on.
func (s *Stream) Configure(blocked bool, chunkSize int, mode, uri string) {
	s.blocked = blocked
	s.chunkSize = chunkSize
	s.mode = mode
	s.uri = uri
}

// NewUserStreamFilter is a filter whose behaviour is a registered PHP class.
// The run function is installed separately, because building it needs to call
// into that class.
func NewUserStreamFilter(name string, held *Stream, object *php.Object, class any) *StreamFilter {
	return &StreamFilter{Name: name, Stream: held, Object: object, class: class}
}

// SetRun installs the filter's transform, which for a user filter is a method
// call and so can only be built by the layer with a class table.
func (f *StreamFilter) SetRun(run StreamFilterRun) { f.run = run }

// SetClose installs what to do when the filter is removed.
func (f *StreamFilter) SetClose(close func(string) error) { f.close = close }

// Mode is how the stream was opened, which `stream_get_meta_data` shows and the
// filter attachment reads to decide which chain a filter joins.
func (s *Stream) Mode() string { return s.mode }

// IsRead reports which chain the filter joined, which decides where it is
// attached and detached.
func (f *StreamFilter) IsRead() bool { return f.read }

// SetRead records which chain the filter joins.
func (f *StreamFilter) SetRead(read bool) { f.read = read }

// AttachFilter puts a filter on the read or write chain, at the end or the
// front. Which chain is the filter's own business -- it recorded that when it
// was created -- and the caller only says where in the chain.
//
// Attaching a *read* filter fixes the position `ftell` reports: from then on a
// filtered read consumes more from the descriptor than it hands out, so the
// stream reports where the **filtered** view is rather than where the
// descriptor is.
func (s *Stream) AttachFilter(filter *StreamFilter, atEnd bool) {
	chain := &s.writeFilters
	if filter.IsRead() {
		if !s.FilterPositionSet {
			if position, err := s.Seek(0, io.SeekCurrent); err == nil {
				s.FilteredPosition = position
				s.FilterPositionSet = true
			}
		}
		chain = &s.readFilters
	}
	if atEnd {
		*chain = append(*chain, filter)
		return
	}
	*chain = append([]*StreamFilter{filter}, (*chain)...)
}

// Removed reports a filter already taken off its chain, which `stream_filter_remove`
// answers false for a second time.
func (f *StreamFilter) Removed() bool { return f.removed }

// MarkRemoved records that the filter has been taken off.
func (f *StreamFilter) MarkRemoved() { f.removed = true }

// ErrUserStreamOperation is what a user-space wrapper's method returns when the
// PHP behind it refused the operation -- as opposed to failing. It crosses the
// UserStream interface in both directions, so it belongs with the interface
// rather than with either implementation: the wrapper returns it and the
// library functions read it to tell "the wrapper said no", which is `false`,
// from "something went wrong", which propagates.
var ErrUserStreamOperation = errors.New("user stream operation failed")
