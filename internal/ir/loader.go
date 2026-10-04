package ir

import (
	"path/filepath"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/internal/zend"
)

// EngineLoader compiles the source that include, require and eval name, using
// the same engine that produced the rest of the IR. It satisfies the
// interpreter's Loader interface.
//
// This is the harness's answer to a question the interpreter deliberately does
// not answer for itself: it executes IR and contains no PHP parser. A generated
// binary would need a bundled parser or would reject these constructs; here the
// real compiler is already linked in, so it may as well be used.
type EngineLoader struct {
	Engine *zend.Engine
	// Scratch is where eval'd source is written, since the engine compiles
	// files rather than strings.
	Scratch string
}

func (l *EngineLoader) CompileFile(path string) (*pb.Unit, error) {
	unit, err := l.Engine.CompileUnit(path)
	if err != nil {
		return nil, err
	}
	return EncodeUnit(unit), nil
}

func (l *EngineLoader) CompileString(code string) (*pb.Unit, error) {
	// eval's argument goes through the engine's own eval entry point, not
	// through the file compiler. The two end differently -- a file's op_array
	// closes with `RETURN 1` and eval'd code with `RETURN null` -- so
	// `eval("1;")` answers NULL only when it is compiled as eval.
	//
	// The name is where the caller's scratch file would have been; the include
	// path renames it to the `%s(%d) : eval()'d code` form afterwards.
	unit, err := l.Engine.CompileEvalString(code, filepath.Join(l.Scratch, "eval.php"))
	if err != nil {
		return nil, err
	}
	return EncodeUnit(unit), nil
}
