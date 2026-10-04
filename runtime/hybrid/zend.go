// Package hybrid connects the Zend compiler to a bundled Go interpreter.
// Generated bundles import it only when built with the php2go_hybrid tag.
package hybrid

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/moriyoshi/php2go/internal/ir"
	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/internal/zend"
	"github.com/moriyoshi/php2go/runtime/bundle"
	"github.com/moriyoshi/php2go/runtime/interp"
	"github.com/moriyoshi/php2go/runtime/php"
)

// Zend permits one active embedded engine. Keep each request on its pinned
// goroutine from Start through Close and serialize hybrid requests.
var engineMu sync.Mutex

type compiler struct{ engine *zend.Engine }

type scopedEvalState struct{ runtime *interp.Interp }

var scopedEvalKey = &struct{}{}

func scopedRuntime(ctx *php.Ctx, manifest *pb.Manifest) *interp.Interp {
	state := ctx.RuntimeState(scopedEvalKey, func() any {
		return &scopedEvalState{runtime: interp.New(manifest)}
	}).(*scopedEvalState)
	return state.runtime
}

// FunctionParameter and FunctionSignature describe emitted Go function
// parameters to eval without importing the interpreter into ordinary bundles.
type FunctionParameter = interp.ScopedFunctionParameter
type FunctionSignature = interp.ScopedFunctionSignature

func newCompiler(manifest *pb.Manifest) (bundle.EvalCompiler, error) {
	engineMu.Lock()
	engine, err := zend.Start()
	if err != nil {
		engineMu.Unlock()
		return nil, err
	}
	provenance := manifest.GetProvenance()
	layout := zend.DetectLayout()
	if provenance == nil || provenance.GetPhpVersion() != zend.Version() ||
		provenance.GetSizeofZnodeOp() != uint32(layout.SizeofZnodeOp) ||
		provenance.GetAbsoluteJumpAddresses() != layout.AbsoluteJumpAddresses ||
		provenance.GetAbsoluteConstantAddresses() != layout.AbsoluteConstantAddresses ||
		!reflect.DeepEqual(manifest.GetOpcodeNames(), zend.OpcodeNames()) {
		engine.Close()
		engineMu.Unlock()
		return nil, fmt.Errorf("hybrid: linked Zend compiler does not match the bundle's harvested IR")
	}
	return &compiler{engine: engine}, nil
}

func (c *compiler) CompileString(code string) (*pb.Unit, error) {
	unit, err := c.engine.CompileEvalString(code, "php2go-eval")
	if err != nil {
		return nil, err
	}
	return ir.EncodeUnit(unit), nil
}

func (c *compiler) Close() {
	c.engine.Close()
	engineMu.Unlock()
}

// EnableZend opts an App into compiling eval strings at runtime. The Go
// interpreter executes the harvested IR in the caller's existing scope.
func EnableZend(app *bundle.App) { app.SetEvalCompilerFactory(newCompiler) }

// EvalScoped compiles a new eval string with Zend, then executes only that
// body in the generated program's existing context and lowered local scope.
func EvalScoped(ctx *php.Ctx, code string, vars map[string]*php.Value, order *[]string) (php.Value, error) {
	return EvalScopedWithFunctions(ctx, code, vars, order, nil, nil, nil)
}

// EvalScopedWithFunctions is EvalScoped with access to the generated program's
// global functions. dispatch must perform the generated program's normal call
// framing and invoke the named function.
func EvalScopedWithFunctions(ctx *php.Ctx, code string, vars map[string]*php.Value, order *[]string,
	names []string, signatures map[string]FunctionSignature,
	dispatch func(*php.Ctx, string, []php.Value) (php.Value, error),
) (php.Value, error) {
	engineMu.Lock()
	engine, err := zend.Start()
	if err != nil {
		engineMu.Unlock()
		return php.Undef(), err
	}
	compiled, err := engine.CompileEvalString(code, "php2go-eval")
	manifest, manifestErr := harvestManifest(engine)
	engine.Close()
	engineMu.Unlock()
	if manifestErr != nil {
		return php.Undef(), manifestErr
	}
	runtime := scopedRuntime(ctx, manifest)
	if err != nil {
		return php.Undef(), classifyScopedError(runtime.CompileEvalFailure(ctx, err))
	}
	value, err := runtime.RunScopedEvalWithFunctions(ctx, ir.EncodeUnit(compiled), vars, order, names, signatures, dispatch)
	return value, classifyScopedError(err)
}

// IncludeScoped compiles a resolved host file on demand. The caller handles
// include_path, _once identity, and the generated program's variable map.
func IncludeScoped(ctx *php.Ctx, kind, path string, vars map[string]*php.Value, order *[]string) (php.Value, error) {
	engineMu.Lock()
	engine, err := zend.Start()
	if err != nil {
		engineMu.Unlock()
		return php.Undef(), err
	}
	compiled, err := engine.CompileUnit(path)
	manifest, manifestErr := harvestManifest(engine)
	engine.Close()
	engineMu.Unlock()
	if manifestErr != nil {
		return php.Undef(), manifestErr
	}
	runtime := interp.New(manifest)
	if err != nil {
		return php.Undef(), classifyScopedError(runtime.CompileFileFailure(ctx, err, path))
	}
	value, err := runtime.RunScopedInclude(ctx, ir.EncodeUnit(compiled), vars, order, kind, path)
	return value, classifyScopedError(err)
}

// classifyScopedError keeps interpreter refusals in the generated program's
// unsupported bucket. Without this conversion, generated Go treated an
// interp.UnsupportedError as an ordinary runtime error and printed no PHP
// diagnostic, turning a known gap into a failed differential case.
func classifyScopedError(err error) error {
	if err == nil {
		return nil
	}
	var unsupported *interp.UnsupportedError
	if errors.As(err, &unsupported) {
		return php.Unsupported("hybrid interpreter: %s", unsupported.Error())
	}
	return err
}

func harvestManifest(engine *zend.Engine) (*pb.Manifest, error) {
	builtins, err := engine.Builtins()
	if err != nil {
		return nil, err
	}
	constants, err := engine.Constants()
	if err != nil {
		return nil, err
	}
	frameless, err := engine.FramelessFunctions()
	if err != nil {
		return nil, err
	}
	classes, err := engine.InternalClasses()
	if err != nil {
		return nil, err
	}
	iniEntries, err := engine.IniEntries()
	if err != nil {
		return nil, err
	}
	extensions, err := engine.Extensions()
	if err != nil {
		return nil, err
	}
	return ir.EncodeManifest(ir.Harvest{
		Version: zend.Version(), Layout: zend.DetectLayout(), Opcodes: zend.OpcodeNames(),
		Builtins: builtins, Constants: constants, BuiltinClasses: classes,
		FramelessFunctions: frameless, DefaultErrorReporting: engine.DefaultErrorReporting(),
		Assertions: engine.Assertions(), IniEntries: iniEntries, Extensions: extensions,
	}), nil
}
