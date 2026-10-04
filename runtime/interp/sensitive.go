package interp

import (
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// SensitiveParameterValue is the wrapper `#[SensitiveParameter]` puts around an
// argument in a backtrace, so a password does not end up in a log. It holds one
// value and shows nothing: var_dump, print_r, var_export, a cast to array and
// json_encode all see an object with no properties, because the value is not in
// the property table at all.
//
// That is the whole point of it -- anything that walks properties must not
// reach the value -- so the value lives in Internal and only getValue() answers.

// newSensitiveParameterValue is the constructor. It is an ordinary one: a
// program may build these itself, and the tests do.
//
// **The payload is runtime/php's**, because the compiled back-end builds one of
// these too and neither arm should have its own idea of what it holds.
func newSensitiveParameterValue(object *php.Object, args []php.Value) {
	held := php.Null()
	if len(args) > 0 {
		held = args[0]
	}
	php.InitSensitiveParameterValue(object, held)
}

// sensitiveParameterValue builds one of these around a value, which is what a
// backtrace puts in place of an argument the declaration marked sensitive.
func (u *Unit) sensitiveParameterValue(held php.Value) *php.Object {
	object := u.born(php.NewObject("SensitiveParameterValue", u.handle()))
	newSensitiveParameterValue(object, []php.Value{held})
	return object
}

// sensitiveHider answers the replacement a backtrace puts in place of an
// argument the declaration marked sensitive. It is handed to whatever builds
// the trace rather than called eagerly: nothing should be built at all unless a
// frame actually has one.
func (u *Unit) sensitiveHider() func(php.Value) php.Value {
	return func(held php.Value) php.Value {
		return php.Obj(u.sensitiveParameterValue(held))
	}
}

// sensitiveAttribute is the attribute that marks a parameter, spelled without
// its leading separator the way the harvest records it.
const sensitiveAttribute = "SensitiveParameter"

// sensitiveArgs marks the arguments of a call whose parameter carries
// `#[\SensitiveParameter]`, which is what keeps them out of a backtrace. A
// variadic parameter marks every argument that reached it, since they are all
// the one declaration.
func (in *Interp) sensitiveArgs(unit *Unit, call *pendingCall, count int) []bool {
	parameters, _, known := in.calleeParameters(unit, call)
	if !known || len(parameters) == 0 {
		return nil
	}
	var marks []bool
	for i := 0; i < count; i++ {
		at := i
		if at >= len(parameters) {
			if !parameters[len(parameters)-1].GetVariadic() {
				break
			}
			at = len(parameters) - 1
		}
		if !declaresSensitive(parameters[at]) {
			continue
		}
		for len(marks) <= i {
			marks = append(marks, false)
		}
		marks[i] = true
	}
	return marks
}

func declaresSensitive(arg *pb.ArgInfo) bool {
	for _, attribute := range arg.GetAttributes() {
		if strings.EqualFold(strings.TrimPrefix(attribute.GetName(), "\\"), sensitiveAttribute) {
			return true
		}
	}
	return false
}

// sensitiveMethod answers getValue(), the only method the class has.
func sensitiveMethod(call *pendingCall) (php.Value, bool, error) {
	held, ok := call.this.Internal.(*php.SensitiveValue)
	if !ok {
		return php.Undef(), false, nil
	}
	if strings.EqualFold(call.builtinMethod, "getvalue") {
		return held.Held(), true, nil
	}
	return php.Undef(), false, nil
}

// hidesProperties reports a class whose property table is not shown. The engine
// gives SensitiveParameterValue a handler that answers with an empty table, so
// var_dump, print_r, var_export, a cast to array, json_encode and foreach all
// see nothing -- while reflection, which reads the slot rather than the table,
// still finds the value. Hiding it is the whole purpose of the class.
func hidesProperties(object *php.Object) bool { return php.HidesProperties(object) }
