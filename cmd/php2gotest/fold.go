package main

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/internal/gogen"
	"github.com/moriyoshi/php2go/internal/lower"
	"github.com/moriyoshi/php2go/internal/zend"
)

// foldCache is the engine's answer to each folded call, by call text.
var foldCache = map[string]*gogen.FoldedCall{}

// foldEngineCalls answers, with the engine the compiler links, the library
// calls whose result depends on nothing but literal arguments and the build
// (lower.FoldTarget), and marks each with `fold=<key>` for the emitter.
//
// **Only what the engine answers the same way at run time.** highlight_string
// also reads the highlight.* INI settings, so a program that can change any
// setting (ini_set, ini_alter, ini_restore, mentioned anywhere) is not folded.
// Arguments must have the parameter's own type -- a string source, a bool
// return flag -- so no coercion, deprecation or strict_types TypeError is left
// for run time to decide. A call that prints a diagnostic, throws, or answers
// with anything but a string or a bool is left to the runtime's refusal.
func foldEngineCalls(engine *zend.Engine, prog *gogen.Program) {
	if engine == nil {
		return
	}
	bodies := append([]*lower.Function{}, gogen.Bodies(prog)...)
	for _, c := range prog.Classes {
		for _, fn := range c.StaticMethods {
			bodies = append(bodies, fn)
		}
	}
	iniTouched := false
	for _, fn := range bodies {
		if fn == nil {
			continue
		}
		text := strings.ToLower(fn.String())
		if strings.Contains(text, "ini_set") || strings.Contains(text, "ini_alter") || strings.Contains(text, "ini_restore") {
			iniTouched = true
		}
	}
	for _, fn := range bodies {
		if fn == nil {
			continue
		}
		for bi, b := range fn.Blocks {
			for ii := range b.Insts {
				in := &b.Insts[ii]
				name := lower.FoldCallee(*in)
				if !lower.FoldTarget(name) {
					continue
				}
				args, ok := lower.FoldArgs(fn, bi, ii, fn == prog.File)
				if !ok || !foldArgsTyped(name, args) || (name == "highlight_string" && iniTouched) {
					continue
				}
				key := name + "(" + strings.Join(args, ", ") + ")"
				f, seen := foldCache[key]
				if !seen {
					f = askEngine(engine, name, args)
					foldCache[key] = f
				}
				if f == nil {
					continue
				}
				if prog.Folded == nil {
					prog.Folded = map[string]gogen.FoldedCall{}
				}
				id := strconv.Itoa(len(prog.Folded))
				prog.Folded[id] = *f
				in.Attrs = append(in.Attrs, lower.FoldAttr+id)
			}
		}
	}
}

// foldArgsTyped checks each argument against the parameter's own type.
func foldArgsTyped(name string, args []string) bool {
	switch name {
	case "zend_version":
		return len(args) == 0
	case "highlight_string":
		if len(args) < 1 || len(args) > 2 || !strings.HasPrefix(args[0], `"`) {
			return false
		}
		return len(args) == 1 || args[1] == "true" || args[1] == "false"
	}
	return false
}

// askEngine runs the call in the engine and returns what it printed and
// returned, or nil when the answer is not one to embed.
func askEngine(engine *zend.Engine, name string, args []string) *gogen.FoldedCall {
	rendered := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, `"`) {
			s, err := strconv.Unquote(a)
			if err != nil {
				return nil
			}
			rendered = append(rendered, fmt.Sprintf("base64_decode('%s')", base64.StdEncoding.EncodeToString([]byte(s))))
			continue
		}
		rendered = append(rendered, a)
	}
	code := fmt.Sprintf(`error_clear_last();
ob_start();
try { $__r = \%s(%s); $__x = null; } catch (\Throwable $__e) { $__x = $__e; $__r = null; }
$__o = ob_get_clean();
if ($__x !== null || error_get_last() !== null || !(is_string($__r) || is_bool($__r))) {
	echo "refuse";
} else {
	echo is_string($__r) ? "s:" . base64_encode($__r) : ($__r ? "t" : "f"), "\n", base64_encode($__o);
}`, name, strings.Join(rendered, ", "))
	out, err := engine.Eval(code)
	if err != nil {
		return nil
	}
	lines := strings.SplitN(out, "\n", 2)
	if len(lines) != 2 {
		return nil
	}
	printed, err := base64.StdEncoding.DecodeString(lines[1])
	if err != nil {
		return nil
	}
	f := &gogen.FoldedCall{Output: string(printed)}
	switch r := lines[0]; {
	case r == "t":
		f.Result = "true"
	case r == "f":
		f.Result = "false"
	case strings.HasPrefix(r, "s:"):
		s, err := base64.StdEncoding.DecodeString(r[2:])
		if err != nil {
			return nil
		}
		f.Result = fmt.Sprintf("%q", string(s))
	default:
		return nil
	}
	return f
}
