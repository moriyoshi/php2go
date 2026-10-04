package lower

import (
	"strconv"
	"strings"
)

// IntVersions gives a function a second body compiled on the assumption that
// some of its untyped parameters hold ints, and returns how many of its values
// that version carries unboxed beyond what the general body does. Zero means no
// version was made.
//
// # Why a version rather than a type
//
// `function fibo($n)` says nothing about `$n`, so nothing computed from it is
// known to be an int, and a loop bounded by it -- `for ($i = 0; $i < $n; $i++)`
// -- has no bound the int representation can use. Proving from the call sites
// that `$n` is always an int is not possible in general: a function is reached
// by name through `call_user_func`, `array_map`, reflection and a dynamic
// `$f()`, with names computed at run time, and the bundled library itself calls
// callbacks that way.
//
// A version needs no proof. The general body checks, once, after binding its
// arguments, whether each assumed parameter arrived as a plain int, and if so
// calls the version; otherwise it runs as it always did. The version's
// parameters are then ints by construction, which the inference reads exactly
// as it reads a declared `int` (Function.AssumedInt) -- and ints within
// AssumedIntRange, which the overflow proof uses.
//
// # Which parameters
//
// Each untyped by-value parameter is tried alone, and kept if assuming it
// unboxes more values than the general body does. The version assumes every
// parameter that paid. A parameter that is an array or a string at run time
// therefore costs one kind test per call and nothing else.
//
// # What it refuses
//
// A body that owns state keyed by its Go name would get two copies of it:
// `static` variables, and anything the body declares (closures, classes,
// functions). Resumable bodies, by-reference and variadic parameters are left
// alone too.
func IntVersions(fn *Function, constants map[string]string) int {
	if !versionable(fn) {
		return 0
	}
	base := unboxedCount(fn.clone(), constants, nil)
	var keep []int
	for i, p := range fn.Params {
		// A parameter the frame reads through its slot (LiveParams) stays a
		// zval, so it is never assumed an int.
		if !untypedByValue(p) || paramIsLive(fn, p) {
			continue
		}
		if unboxedCount(fn.clone(), constants, []int{i}) > base {
			keep = append(keep, i)
		}
	}
	if len(keep) == 0 {
		return 0
	}
	v := fn.clone()
	gain := unboxedCount(v, constants, keep) - base
	if gain <= 0 {
		return 0
	}
	fn.IntVersion = v
	return gain
}

// unboxedCount assumes the given parameters are ints, runs the representation
// passes over fn, and counts what they unboxed.
func unboxedCount(fn *Function, constants map[string]string, assumed []int) int {
	if len(assumed) > 0 {
		fn.AssumedInt = map[int]bool{}
		for _, i := range assumed {
			fn.AssumedInt[i] = true
		}
		// The variants were chosen without the assumption; with it, `$n - 2`
		// is `sub.int` and needs no operand guard.
		Specialize(fn, constants)
	}
	return IntReps(fn, constants) + FloatReps(fn, constants)
}

func versionable(fn *Function) bool {
	if len(fn.States) > 0 || len(fn.Frame) > 0 || fn.SuspendsToCaller || len(fn.Statics) > 0 ||
		strings.HasPrefix(fn.Name, "{") || len(fn.Params) == 0 {
		return false
	}
	for _, p := range fn.Params {
		if ParamIsByRef(p) {
			return false
		}
	}
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			switch in.Opcode {
			case "recv.variadic", "closure", "closure.from_call", "bind_static", "declare_class",
				"declare_anon_class", "declare_function", "declare_const":
				return false
			}
		}
	}
	return true
}

// untypedByValue reports a by-value `.params` entry that declares no type:
// `0 $n ""`, where a typed one reads `1 $m "int"`.
func untypedByValue(param string) bool {
	if ParamIsByRef(param) {
		return false
	}
	q := strings.Index(param, `"`)
	if q < 0 {
		return true
	}
	decl, err := strconv.Unquote(strings.TrimSpace(param[q:]))
	return err == nil && decl == ""
}

// AssumedIntRange bounds an assumed int's magnitude: the entry test admits
// only ints within ±2^62, so a step by less than 2^62 in either direction
// stays inside the int64 range. `$n - 1` is otherwise a float for
// PHP_INT_MIN, and a loop counter seeded from it could not be an int. An int
// outside the range is rare and simply runs the general body.
const AssumedIntRange = 1 << 62

// AssumedIntGuards lists the parameter positions a version assumes, in order,
// for the emitter's entry test.
func (fn *Function) AssumedIntGuards() []int {
	var out []int
	for i := range fn.Params {
		if fn.AssumedInt[i] {
			out = append(out, i)
		}
	}
	return out
}

// clone copies a function deeply enough that passes rewriting instructions,
// block parameters or value types on the copy leave the original alone. The
// analysis maps are shared: they are read, never written, after lowering.
func (fn *Function) clone() *Function {
	c := *fn
	c.Blocks = make([]*Block, len(fn.Blocks))
	for i, b := range fn.Blocks {
		nb := *b
		nb.Params = append([]string(nil), b.Params...)
		nb.Succs = append([]int(nil), b.Succs...)
		nb.InFinallyOf = append([]int(nil), b.InFinallyOf...)
		nb.Insts = make([]Inst, len(b.Insts))
		for j, in := range b.Insts {
			ni := in
			if in.Result != nil {
				r := *in.Result
				ni.Result = &r
			}
			ni.Operands = append([]string(nil), in.Operands...)
			ni.Attrs = append([]string(nil), in.Attrs...)
			nb.Insts[j] = ni
		}
		c.Blocks[i] = &nb
	}
	c.Params = append([]string(nil), fn.Params...)
	c.ParamInfo = append([]FunctionParameter(nil), fn.ParamInfo...)
	c.AssumedInt = nil
	c.IntVersion = nil
	return &c
}

// assumedInt reports whether a version assumes parameter i (a recv operand's
// text) holds an int.
func (fn *Function) assumedInt(operand string) bool {
	i, err := strconv.Atoi(strings.TrimSpace(operand))
	return err == nil && fn.AssumedInt[i]
}

// paramIsLive reports a `.params` entry naming one of fn.LiveParams.
func paramIsLive(fn *Function, param string) bool {
	fields := strings.SplitN(strings.TrimSpace(param), " ", 3)
	if len(fields) < 2 {
		return false
	}
	_, live := fn.LiveParams[strings.TrimPrefix(fields[1], "&")]
	return live
}
