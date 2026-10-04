package stdlib

import (
	"math"
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// `var_export` and `debug_zval_dump`. Both render a value, and the one thing
// they needed the interpreter for is what an *object* shows -- which is a
// different question from `var_dump`'s, so it is a second hook: plain names,
// duplicates kept, a get hook run where one exists, and a virtual property
// included even though it has no slot. See `php.ExportProperties`.

func init() {
	php.RegisterBuiltin("var_export", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.Null(), nil
		}
		// **A lazy object is initialized before it is looked at**, exactly as
		// `serialize` and `json_encode` do it -- `Materialize`'s own doc names
		// all three. Measured against the engine: `var_export` initialises,
		// while `print_r` and `var_dump` deliberately do not.
		// An incomplete object is not lazy; what it holds is what shows.
		subject := args[0].Deref()
		if !php.IsIncomplete(subject.Object()) {
			var err error
			if subject, err = ctx.Materialize(subject); err != nil {
				return php.Undef(), err
			}
		}
		text, err := VarExport(ctx, subject.Deref(), 1)
		if err != nil {
			return php.Undef(), err
		}
		// The second argument asks for the text back instead of printing it.
		if len(args) > 1 && args[1].Deref().Bool() {
			return php.String(text), nil
		}
		ctx.Write(text)
		return php.Null(), nil
	})
	php.RegisterBuiltin("debug_zval_dump", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		var rendered strings.Builder
		for _, arg := range args {
			text, err := php.DebugZvalDump(ctx, arg)
			if err != nil {
				return php.Undef(), err
			}
			rendered.WriteString(text)
		}
		ctx.Write(rendered.String())
		return php.Null(), nil
	})
}

// VarExport renders a value as PHP source, which is what distinguishes it from
// var_dump: the output has to parse back to the same value.
//
// The indentation is not a plain two-per-level: an array's elements are indented
// by level+1 and an object's by level+2, and a nested container starts on its
// own line at level-1. Those offsets are the engine's, and reproducing them
// exactly is the whole contract with the tests.
func VarExport(ctx *php.Ctx, v php.Value, level int) (string, error) {
	indent := func(n int) string { return strings.Repeat(" ", n) }

	switch v.Kind() {
	case php.KindUndef, php.KindNull:
		return "NULL", nil
	case php.KindTrue:
		return "true", nil
	case php.KindFalse:
		return "false", nil
	case php.KindInt:
		return strconv.FormatInt(v.Int(), 10), nil
	case php.KindFloat:
		return ExportFloat(v.Float()), nil
	case php.KindString:
		return exportString(v.String()), nil

	case php.KindArray:
		var b strings.Builder
		if level > 1 {
			b.WriteString("\n" + indent(level-1))
		}
		b.WriteString("array (\n")
		var failed error
		v.Array().Each(func(k php.Key, item php.Value) bool {
			b.WriteString(indent(level + 1))
			if k.IsInt {
				b.WriteString(strconv.FormatInt(k.Int, 10))
			} else {
				b.WriteString(exportString(k.Str))
			}
			b.WriteString(" => ")
			rendered, err := VarExport(ctx, item.Deref(), level+2)
			if err != nil {
				failed = err
				return false
			}
			b.WriteString(rendered)
			b.WriteString(",\n")
			return true
		})
		if failed != nil {
			return "", failed
		}
		if level > 1 {
			b.WriteString(indent(level - 1))
		}
		b.WriteString(")")
		return b.String(), nil

	case php.KindObject:
		object := v.Object()
		if object == nil {
			return "NULL", nil
		}
		// A date object exports its date, zone type and zone, which are not
		// held.
		if php.HidesDateState(object) {
			return "", php.DateGap(object, "var_export")
		}
		var b strings.Builder
		if level > 1 {
			b.WriteString("\n" + indent(level-1))
		}
		if object.EnumCase != "" {
			// An enum case is written as the case itself, with no properties
			// and no closing paren.
			b.WriteString("\\" + php.DisplayName(object.ClassName) + "::" + object.EnumCase)
			return b.String(), nil
		}
		plain := object.ClassName == "stdClass"
		if plain {
			// stdClass has no __set_state, so it is written as a cast instead.
			b.WriteString("(object) array(\n")
		} else {
			b.WriteString("\\" + php.DisplayName(object.ClassName) + "::__set_state(array(\n")
		}
		entries, err := ctx.ExportProperties(object)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			b.WriteString(indent(level + 2))
			switch {
			case entry.Key != nil && entry.Key.IsInt:
				b.WriteString(strconv.FormatInt(entry.Key.Int, 10))
			case entry.Key != nil:
				b.WriteString(exportString(entry.Key.Str))
			default:
				b.WriteString(exportString(entry.Name))
			}
			b.WriteString(" => ")
			rendered, err := VarExport(ctx, entry.Value.Deref(), level+2)
			if err != nil {
				return "", err
			}
			b.WriteString(rendered)
			b.WriteString(",\n")
		}
		if level > 1 {
			b.WriteString(indent(level - 1))
		}
		if plain {
			b.WriteString(")")
		} else {
			b.WriteString("))")
		}
		return b.String(), nil
	}
	return "NULL", nil
}

// exportString quotes a string as PHP source. A NUL cannot appear inside single
// quotes, so it is spliced in as a separate double-quoted piece the way the
// engine does.
func exportString(s string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s)
	escaped = strings.ReplaceAll(escaped, "\x00", `' . "\0" . '`)
	return "'" + escaped + "'"
}

// ExportFloat writes a float that will parse back as a float: a value with no
// fractional part still gets one, so 2.0 does not become the integer 2.
//
// Exported because a diagnostic that names a float *value* spells it the same
// way -- "on 2.0", not "on 2" -- so the two must not drift.
func ExportFloat(value float64) string {
	// PHP spells these INF, -INF and NAN wherever a float is rendered; Go's
	// formatter writes +Inf and NaN, which is neither valid PHP nor what
	// var_export is supposed to produce -- its output has to be re-parseable.
	switch {
	case math.IsInf(value, 1):
		return "INF"
	case math.IsInf(value, -1):
		return "-INF"
	case math.IsNaN(value):
		return "NAN"
	}
	return php.ReprFloat(value, 'E', true)
}
