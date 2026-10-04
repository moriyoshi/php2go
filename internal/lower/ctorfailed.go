package lower

import "strings"

// What dies with an object whose constructor threw.
//
//	class B { public $a;
//	    function __construct() { $this->a = new A(); throw new Exception("1"); } }
//	try { $b = new B(); } catch (Exception $e) { echo $e->getMessage(); }
//
// The engine frees B as the exception leaves `new` -- without its
// destructor, which a failed constructor never earns -- and freeing B frees
// its properties, so A's destructor runs there, before the catch
// (bug49893). The emitter already hands B's number back when the
// constructor keeps nothing of `$this`; what the properties hold is the
// other half, and it is only safe for an object nothing but the property
// can have reached.

// CtorFreshProps answers the properties a constructor body fills with
// objects nothing else can reach: the names of `$this->p = new X(...)`
// stores whose value is a `new` of a class whose constructor keeps nothing
// of its `$this` (ctorLeaks), used by its own constructor call and that one
// store alone. nil when the body could let anything else see `$this` or
// those objects:
//
//   - any other use of `$this` -- a read (`$this->a` could be copied out), a
//     method call, a reference, a dynamic name, a terminator;
//   - any call that may pass `$this` without naming it (`parent::`,
//     `self::`, `static::` calls) and any closure, which binds `$this`;
//   - a body this analysis cannot see into (escapeOpaque, `$$`, `yield`).
func CtorFreshProps(fn *Function, ctorLeaks func(class string) bool) []string {
	if fn == nil {
		return nil
	}
	text := fn.String()
	for _, name := range escapeOpaque {
		if strings.Contains(text, `"`+name+`"`) {
			return nil
		}
	}
	if strings.Contains(text, "dynvar") || strings.Contains(text, "yield") {
		return nil
	}
	var props []string
	seen := map[string]bool{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		if escapeToken(b.Term, "$this") {
			return nil
		}
		for _, in := range b.Insts {
			if strings.Contains(in.Opcode, "closure") || strings.Contains(in.Opcode, "callable") {
				return nil
			}
			if strings.HasPrefix(in.Opcode, "call.") && in.Opcode != "call.fn" &&
				in.Opcode != "call.ctor" && in.Opcode != "call.intrinsic" {
				return nil
			}
			args := escapeArgs(in.ArgList)
			if !escapeMentions(in.Operands, "$this") && !escapeMentions(args, "$this") {
				continue
			}
			if in.Opcode != "call.intrinsic" || len(in.Operands) == 0 ||
				strings.Trim(in.Operands[0], `"`) != "prop.set" || len(args) != 3 ||
				strings.TrimSpace(args[0]) != "$this" || escapeMentions(in.Operands[1:], "$this") {
				return nil
			}
			name := strings.TrimSpace(args[1])
			if !strings.HasPrefix(name, `"`) {
				return nil
			}
			value := strings.TrimSpace(args[2])
			def := definition(fn, value)
			if def == nil || def.Opcode != "new" || len(def.Operands) == 0 || fn.FrameBound[value] {
				return nil
			}
			class := strings.TrimSpace(def.Operands[0])
			if !strings.HasPrefix(class, `"`) || ctorLeaks(strings.ToLower(strings.Trim(class, `"`))) {
				return nil
			}
			if !onlyConstructedAndStored(fn, value, in, 2) {
				return nil
			}
			if n := strings.Trim(name, `"`); !seen[n] {
				seen[n] = true
				props = append(props, n)
			}
		}
	}
	return props
}
