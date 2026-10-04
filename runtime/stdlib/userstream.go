package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// UserStream is a stream whose behaviour is a **PHP class**, registered through
// `stream_wrapper_register`. It is the one part of a stream that cannot live
// here: every operation on it is a method call into user code, which needs a
// class table.
//
// So the concrete `Stream` moved to this package and this interface stayed
// behind it -- the same split `StreamContext` and `contextHolder` use. A stream
// with no wrapper has a nil one and never asks.
//
// The method set is exactly what `Stream` asks of it and nothing more. Four of
// the entries are readers rather than operations, because the stream reports
// facts about a user wrapper -- what `stream_get_meta_data` shows, and whether
// `flock` would do anything -- that only the wrapper knows.
type UserStream interface {
	Read(output []byte) (int, error)
	Write(text string) (int, error)
	Seek(offset int64, whence int) (int64, error)
	Close() error
	Flush() (bool, error)
	Truncate(size int64) (bool, error)
	StatValues() ([]int64, bool, error)
	Lock(operation int64) (locked bool, wouldBlock bool, err error)
	SetOption(option, first, second int64) (bool, error)
	// RefreshEOF re-asks the wrapper whether it is at the end, which PHP does
	// before reporting `feof`.
	RefreshEOF() error

	// WrapperObject is the wrapper instance itself, which
	// `stream_get_meta_data` reports under `wrapper_data`.
	WrapperObject() *php.Object
	// Unread is what has been read from the wrapper and not yet handed out.
	Unread() int
	// Position is where the wrapper believes it is, which is not the same as
	// the descriptor's offset: a filtered read consumes more from the wrapper
	// than it hands out, so `ftell` reports this rather than the raw one.
	Position() int64
	// Seekable reports whether the wrapper declares `stream_seek`.
	Seekable() bool
	// SupportsLock reports whether the wrapper declares `stream_lock`, which is
	// what decides whether `flock` on it does anything.
	SupportsLock() bool
}
