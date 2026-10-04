package stdlib

import (
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The session form of serialize and unserialize, for the containers whose
// own `serialize()` / `unserialize()` write or read several values with one
// back-reference table (php.SerialWriter, php.SerialReader).
//
// **Which table a container's session uses** is the engine's rule
// (php_var_serialize_init, php_var_unserialize_init): called while a
// serialize() or unserialize() is running, the session joins that call's
// table -- unless a hook (`__serialize`, `__sleep`, `__unserialize`,
// `__wakeup`) is running, which the engine brackets with serialize_lock and
// so starts a table of its own. The one join this runtime carries is the
// direct one: the container's `serialize()` reached for a `C:` payload, or its
// `unserialize()` handed one. Any other session inside a running call has no
// table to join here, and is refused (a nil writer or reader).

var sessions struct {
	serializeDepth, unserializeDepth int
	locked                           int
	joinWriter                       *serialState
	joinReader                       *serialReader
}

// lockedHook runs a serialization hook under the engine's serialize_lock.
func lockedHook(run func() (php.Value, error)) (php.Value, error) {
	sessions.locked++
	defer func() { sessions.locked-- }()
	return run()
}

// joiningWriter runs a Serializable::serialize() whose session may join state.
func joiningWriter(state *serialState, run func() (php.Value, error)) (php.Value, error) {
	prev := sessions.joinWriter
	sessions.joinWriter = state
	defer func() { sessions.joinWriter = prev }()
	return run()
}

// joiningReader runs a Serializable::unserialize() whose session may join r.
func joiningReader(r *serialReader, run func() (php.Value, error)) (php.Value, error) {
	prev := sessions.joinReader
	sessions.joinReader = r
	defer func() { sessions.joinReader = prev }()
	return run()
}

func init() {
	php.NewSerialWriter = func(ctx *php.Ctx) php.SerialWriter {
		if outer := sessions.joinWriter; outer != nil {
			sessions.joinWriter = nil
			return &sessionWriter{ctx: ctx, state: outer}
		}
		if sessions.serializeDepth > 0 && sessions.locked == 0 {
			return nil
		}
		state := &serialState{}
		state.file, state.line = ctx.Location()
		return &sessionWriter{ctx: ctx, state: state}
	}
	php.NewSerialReader = func(ctx *php.Ctx, text, function string) php.SerialReader {
		r := &serialReader{ctx: ctx, text: text, failedAt: -1, fn: function}
		if outer := sessions.joinReader; outer != nil {
			sessions.joinReader = nil
			return &sessionReader{r: r, outer: outer}
		}
		if sessions.unserializeDepth > 0 && sessions.locked == 0 {
			return nil
		}
		return &sessionReader{r: r}
	}
}

type sessionWriter struct {
	ctx   *php.Ctx
	state *serialState
}

func (w *sessionWriter) Write(v php.Value) (string, error) {
	var out strings.Builder
	if err := serializeValue(w.ctx, &out, v, w.state); err != nil {
		return "", err
	}
	return out.String(), nil
}

// sessionReader reads one text; joined to an outer reader, it numbers its
// values in the outer reader's table, so a back-reference written after the
// payload can name one of them (bug70169).
type sessionReader struct {
	r     *serialReader
	outer *serialReader
}

func (s *sessionReader) Read() (php.Value, bool, error) {
	r := s.r
	from := 0
	if s.outer != nil {
		r.values, r.boxes = s.outer.values, s.outer.boxes
		from = len(r.values)
		if from == 0 {
			from = 1
		}
	}
	r.failedAt, r.truncated, r.refused = -1, false, nil
	v, ok := r.value()
	if s.outer != nil {
		s.outer.values, s.outer.boxes = r.values, r.boxes
		s.outer.backRef = s.outer.backRef || r.backRef
		s.outer.sharedRef = s.outer.sharedRef || r.sharedRef
		s.outer.foreign = append(s.outer.foreign, [2]int{from, len(r.values)})
	}
	if r.refused != nil {
		return php.Undef(), false, r.refused
	}
	return v, ok, nil
}

func (s *sessionReader) At() int          { return s.r.at }
func (s *sessionReader) Skip(n int)       { s.r.at += n }
func (s *sessionReader) SharedRefs() bool { return s.r.sharedRef }

// FailOffset mirrors the top-level unserialize's report: the end of the text
// when it simply ran out, otherwise where the failing value was blamed.
func (s *sessionReader) FailOffset() int {
	if s.r.truncated {
		return len(s.r.text)
	}
	return s.r.failedAt
}
