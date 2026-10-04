package lower

import (
	"strconv"
	"strings"
)

// FoldAttr marks a library call answered at build time: `fold=<key>` names
// the engine's answer in gogen.Program.Folded.
const FoldAttr = "fold="

// foldTargets are the library functions whose answer depends on nothing but
// their arguments and the engine build -- the tokenizer and the highlight.*
// defaults for highlight_string, the version for zend_version. The compiler
// links that engine, so for literal arguments it can ask it, the way constant
// folding asks it about an expression.
var foldTargets = map[string]bool{
	"highlight_string": true,
	"zend_version":     true,
}

// FoldTarget reports whether name is a library function FoldArgs may answer.
func FoldTarget(name string) bool { return foldTargets[strings.ToLower(name)] }

// FoldCallee is the library function a `call.fn` names, lower-cased, or "".
// A namespaced call that falls back to a global name is not one: which
// function runs is decided at run time.
func FoldCallee(in Inst) string {
	if in.Opcode != "call.fn" || len(in.Operands) == 0 {
		return ""
	}
	for _, a := range in.Attrs {
		if strings.HasPrefix(a, "fallback=") {
			return ""
		}
	}
	name, err := strconv.Unquote(strings.TrimSpace(in.Operands[0]))
	if err != nil {
		return ""
	}
	return strings.ToLower(name)
}

// FoldArgs is the literal text of every argument of the call at
// fn.Blocks[bi].Insts[ii], when each is a compile-time literal.
//
// A plain literal operand is one. So is a **read of a local written with a
// literal** earlier in the entry block of the program's own file body (entry:
// an included file's body starts with its includer's variables set), when nothing between the start
// of the block and the call can run program code: every instruction there is
// a write to a local the block had not mentioned before (so no old value dies
// and runs a destructor), a local read, or another foldable call.
// Under that rule the local holds exactly the literal at the read -- no
// handler, destructor, include or include-time global write ran in between.
func FoldArgs(fn *Function, bi, ii int, entry bool) ([]string, bool) {
	if bi >= len(fn.Blocks) || ii >= len(fn.Blocks[bi].Insts) {
		return nil, false
	}
	b := fn.Blocks[bi]
	call := b.Insts[ii]
	args, ok := splitFoldArgs(call.ArgList)
	if !ok {
		return nil, false
	}
	var known map[string]string
	if entry && bi == 0 {
		known, ok = foldPrefix(b.Insts[:ii])
		if !ok {
			known = nil
		}
	}
	out := make([]string, 0, len(args))
	for _, a := range args {
		tok, ok := strings.CutPrefix(a, "byval ")
		if !ok {
			return nil, false
		}
		tok = strings.TrimSpace(tok)
		if foldLiteral(tok) {
			out = append(out, tok)
			continue
		}
		lit, ok := known[tok]
		if !ok {
			return nil, false
		}
		out = append(out, lit)
	}
	return out, true
}

// foldPrefix walks the entry block up to a call and answers, for each value
// that reads a literal-written local, the literal it holds. It fails when any
// instruction could run program code.
func foldPrefix(insts []Inst) (map[string]string, bool) {
	written := map[string]string{}
	mentioned := map[string]bool{}
	values := map[string]string{}
	for i, in := range insts {
		switch {
		case in.Opcode == lineDirective:
			// A position, not code.
		case in.Opcode == "call.intrinsic" && foldIntrinsic(in) == "write_var":
			args, ok := splitFoldArgs(in.ArgList)
			if !ok || len(args) != 2 {
				return nil, false
			}
			local, value := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
			// A local the block had not mentioned holds nothing whose
			// death could run a destructor, so writing it runs no code --
			// whatever is written. Only a literal is remembered.
			if !strings.HasPrefix(local, "$") || mentioned[local] {
				return nil, false
			}
			mentioned[local] = true
			if foldLiteral(value) {
				written[local] = value
			}
		case in.Opcode == "call.intrinsic" && foldIntrinsic(in) == "read_var":
			args, ok := splitFoldArgs(in.ArgList)
			if !ok || len(args) != 1 || in.Result == nil {
				return nil, false
			}
			local := strings.TrimSpace(args[0])
			mentioned[local] = true
			if lit, ok := written[local]; ok {
				values[in.Result.String()] = lit
			}
		case FoldTarget(FoldCallee(in)):
			// Another answer the engine gives: it runs no program code, and
			// its own arguments must be foldable too.
			for _, a := range mustSplit(in.ArgList) {
				tok := strings.TrimSpace(strings.TrimPrefix(a, "byval "))
				if !foldLiteral(tok) {
					if _, ok := values[tok]; !ok {
						return nil, false
					}
				}
			}
			_ = i
		default:
			return nil, false
		}
	}
	return values, true
}

func foldIntrinsic(in Inst) string {
	if len(in.Operands) == 0 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(in.Operands[0]), `"`)
}

// foldLiteral reports a scalar literal operand: a quoted string, an integer,
// a float, true, false or null. Arrays are left out; nothing folded takes one.
func foldLiteral(tok string) bool {
	switch tok {
	case "true", "false", "null":
		return true
	}
	if strings.HasPrefix(tok, `"`) {
		_, err := strconv.Unquote(tok)
		return err == nil
	}
	if _, err := strconv.ParseInt(tok, 10, 64); err == nil {
		return true
	}
	if _, err := strconv.ParseFloat(tok, 64); err == nil && !strings.ContainsAny(tok, "xXpP") {
		return true
	}
	return false
}

func mustSplit(list string) []string {
	args, _ := splitFoldArgs(list)
	return args
}

// splitFoldArgs splits a parenthesised argument list at its top-level commas,
// keeping quoted strings whole.
func splitFoldArgs(list string) ([]string, bool) {
	inner := strings.TrimSpace(list)
	inner = strings.TrimSuffix(strings.TrimPrefix(inner, "("), ")")
	if strings.TrimSpace(inner) == "" {
		return nil, true
	}
	var out []string
	start := 0
	for i := 0; i < len(inner); {
		switch inner[i] {
		case '"':
			q, err := strconv.QuotedPrefix(inner[i:])
			if err != nil {
				return nil, false
			}
			i += len(q)
		case '(', '[':
			return nil, false
		case ',':
			out = append(out, strings.TrimSpace(inner[start:i]))
			i++
			start = i
		default:
			i++
		}
	}
	out = append(out, strings.TrimSpace(inner[start:]))
	return out, true
}
