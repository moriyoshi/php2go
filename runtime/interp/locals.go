package interp

import (
	"github.com/moriyoshi/php2go/runtime/php"
)

// The three functions that read or write the caller's variables by name:
// get_defined_vars, compact and extract. They reach the frame through the
// unit's frame stack, since a builtin has none of its own.
//
// php2go has compiled variables rather than a symbol table, so a name the body
// never mentions literally has no slot. The frame's overflow table is where
// those go -- the same place $$n puts one -- and all three of these functions
// read and write through it, so extract() into a name that only ever appears as
// a string still produces a variable the rest of the frame can see.

// callerFrame is the frame of whoever called the builtin now running.
func (u *Unit) callerFrame() (*frame, bool) {
	if len(u.frames) == 0 {
		return nil, false
	}
	return u.frames[len(u.frames)-1], true
}

// localsBuiltin implements the three. It reports handled=false for anything
// else.
func (in *Interp) localsBuiltin(ctx *php.Ctx, unit *Unit, name string,
	args []php.Value) (php.Value, bool, error) {
	f, ok := unit.callerFrame()
	if !ok {
		return php.Undef(), false, nil
	}
	switch name {
	case "get_defined_vars":
		out := php.NewArray()
		if f.global {
			// The top level's variables are the global table, in its order:
			// the superglobals, then the compiled variables bound at start,
			// then whatever was created since. A name holding nothing -- a
			// compiled variable never assigned -- is not defined.
			// A compiled variable is read through its frame slot, which is
			// what knows whether it is a program-visible reference (`$b =
			// &$a` dumps both as `&int(1)`); the table's own entry is the
			// binding box and would say so of every variable.
			cvs := map[string]int{}
			for slot, varName := range f.op.GetVarNames() {
				cvs[varName] = slot
			}
			ctx.Globals.Each(func(k php.Key, v php.Value) bool {
				if privateGlobal(ctx, k) {
					return true
				}
				if slot, isCV := cvs[k.String()]; isCV && !k.IsInt && slot < len(f.slots) {
					v = f.slots[slot]
				}
				if !v.Deref().IsUndef() {
					out.Set(k, php.CopyElement(v))
				}
				return true
			})
			for _, varName := range sortedKeys(f.extra) {
				if _, present := out.Get(php.StringKey(varName)); present {
					continue
				}
				if held := f.extra[varName].Get(); !held.IsUndef() {
					out.Set(php.StringKey(varName), php.CopyElement(held))
				}
			}
			return php.Arr(out), true, nil
		}
		for slot, varName := range f.op.GetVarNames() {
			value := f.slots[slot]
			// Deref first: at global scope every compiled variable is bound to
			// its entry in the globals table, so a name that has never been
			// assigned is a *reference* holding undef rather than undef
			// itself -- and it is not a defined variable.
			if value.Deref().IsUndef() {
				continue
			}
			// A program-visible reference stays one: get_defined_vars()
			// inside `function f(&$r)` answers with `&string(...)`, and the
			// element tracks a later write to $r.
			out.Set(php.StringKey(varName), php.CopyElement(value))
		}
		for _, varName := range sortedKeys(f.extra) {
			if held := f.extra[varName].Get(); !held.IsUndef() {
				out.Set(php.StringKey(varName), php.CopyElement(held))
			}
		}
		return php.Arr(out), true, nil

	case "compact":
		out := php.NewArray()
		var failure error
		var gather func(php.Value)
		gather = func(value php.Value) {
			value = value.Deref()
			if value.Kind() == php.KindArray {
				// compact() flattens one array argument into its names, and
				// nests: an array inside an array is walked too.
				value.Array().Each(func(_ php.Key, v php.Value) bool {
					gather(v)
					return true
				})
				return
			}
			if value.Kind() != php.KindString {
				ctx.Warn("compact(): Argument #%d ($var_name) must be string or array of strings",
					out.Len()+1)
				return
			}
			held, defined := f.namedValue(value.String())
			if !defined {
				// php_error_docref prepends the active function's name, so the
				// CLI wording is "compact(): Undefined variable $x". The
				// differential harness's embed SAPI has no active function name
				// to prepend and renders it as a request-startup warning
				// instead, which is why this line has no differential case.
				ctx.Warn("compact(): Undefined variable $%s", value.String())
				return
			}
			out.Set(php.StringKey(value.String()), held.Deref().Copy())
		}
		for _, arg := range args {
			gather(arg)
		}
		return php.Arr(out), true, failure

	case "extract":
		source := php.Undef()
		if len(args) > 0 {
			source = args[0].Deref()
		}
		if source.Kind() != php.KindArray {
			return php.Undef(), true, Raise("TypeError",
				"extract(): Argument #1 ($array) must be of type array, %s given",
				givenTypeName(source))
		}
		flags := int64(extrOverwrite)
		if len(args) > 1 {
			flags = args[1].Deref().Int()
		}
		prefix := ""
		if len(args) > 2 {
			prefix = args[2].Deref().String()
		}
		written, err := extractInto(f, source.Array(), flags, prefix)
		if err != nil {
			return php.Undef(), true, err
		}
		return php.Int(int64(written)), true, nil
	}
	return php.Undef(), false, nil
}

// extract's flags. The values are the engine's, harvested as constants; these
// names are only for reading the code below.
const (
	extrOverwrite      = 0
	extrSkip           = 1
	extrPrefixAll      = 3
	extrPrefixInvalid  = 4
	extrIfExists       = 6
	extrPrefixIfExists = 5
	extrPrefixSame     = 2
	extrRefs           = 0x100
)

// extractInto writes each entry of source into the frame's variable of the same
// name, and answers how many it wrote.
func extractInto(f *frame, source *php.Array, flags int64, prefix string) (int, error) {
	mode := flags &^ extrRefs
	written := 0
	var failure error
	source.Each(func(key php.Key, value php.Value) bool {
		name := key.String()
		if name == "this" {
			// $this is not a variable that can be written, and extract does not
			// skip it quietly: the whole call fails where it reaches one. It
			// does so at global scope too, where there is no $this to protect.
			failure = Raise("Error", "Cannot re-assign $this")
			return false
		}
		_, defined := f.namedValue(name)
		valid := validVariableName(name)
		switch mode {
		case extrSkip:
			if defined {
				return true
			}
		case extrPrefixSame:
			if defined {
				name = prefix + "_" + name
			}
		case extrPrefixAll:
			name = prefix + "_" + name
		case extrPrefixInvalid:
			if !valid {
				name = prefix + "_" + name
			}
		case extrIfExists:
			if !defined {
				return true
			}
		case extrPrefixIfExists:
			if !defined {
				return true
			}
			name = prefix + "_" + name
		}
		if !validVariableName(name) {
			return true
		}
		ref, ok := f.namedRef(name, true)
		if !ok {
			return true
		}
		ref.Set(value.Deref().Copy())
		written++
		return true
	})
	return written, failure
}

// validVariableName reports whether a string can name a PHP variable, which is
// what decides whether extract will write it.
func validVariableName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_', c >= 0x80:
		case c >= '0' && c <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
