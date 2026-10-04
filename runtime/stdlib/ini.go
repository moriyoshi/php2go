package stdlib

import (
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// `ini_get` and `ini_set`. The settings themselves are request state and live
// on the Ctx; what is here is the rule -- which names php2go models, and the
// handful the engine refuses to let a script change.

// modelledIni are the settings php2go both stores and acts on. Anything else
// reads back as false, which is what PHP answers for a setting that does not
// exist -- and is honest for one that does but that php2go disregards.
var modelledIni = map[string]string{
	"arg_separator.output":       "&",
	"zend.enable_gc":             "1",
	"fiber.stack_size":           "",
	"zend.exception_ignore_args": "0",
	// Read by the trace renderer (php.SetTraceStringLimit).
	"zend.exception_string_param_max_len": "15",
	"include_path":                        "",
	"highlight.comment":                   "#FF8000",
	"highlight.default":                   "#0000BB",
	"highlight.html":                      "#000000",
	"highlight.keyword":                   "#007700",
	"highlight.string":                    "#DD0000",
	"realpath_cache_size":                 "4096K",
	"realpath_cache_ttl":                  "120",
	"syslog.filter":                       "no-ctrl",
	"url_rewriter.hosts":                  "",
	"url_rewriter.tags":                   "form=",
	// Stored so a program that sets it is not ignored: unserialize refuses
	// an unknown class while it names a callback (unserialize.go).
	"unserialize_callback_func": "",
}

// IniModelled is a setting's default and whether php2go models it at all.
func IniModelled(name string) (string, bool) {
	value, modelled := modelledIni[name]
	return value, modelled
}

// IniValue reports a modelled setting's current value.
func IniValue(ctx *php.Ctx, name string) (string, bool) {
	fallback, modelled := modelledIni[name]
	if !modelled {
		return "", false
	}
	if stored, held := ctx.IniRead(name); held {
		return stored, true
	}
	return fallback, true
}

func init() {
	php.RegisterBuiltin("ini_get", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.Bool(false), nil
		}
		value, modelled := IniValue(ctx, args[0].Deref().String())
		if !modelled {
			// A setting php2go does not model has no value to report, and
			// inventing one would be worse than saying so.
			return php.Bool(false), nil
		}
		return php.String(value), nil
	})

	php.RegisterBuiltin("ini_set", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 2 {
			return php.Bool(false), nil
		}
		name := args[0].Deref().String()
		previous, modelled := IniValue(ctx, name)
		if !modelled {
			return php.Bool(false), nil
		}
		// **Not every modelled setting is writable.** The realpath cache is
		// sized once at start-up, and the engine answers false rather than
		// taking the value.
		if name == "realpath_cache_size" || name == "realpath_cache_ttl" {
			return php.Bool(false), nil
		}
		value := args[1].Deref().String()
		if name == "syslog.filter" {
			switch value {
			case "all", "no-ctrl", "ascii", "raw":
			default:
				return php.Bool(false), nil
			}
		}
		// **Between 0 and a million**, read as atol reads it; anything else
		// is refused and the setting keeps what it had (exception_025).
		if name == "zend.exception_string_param_max_len" {
			n := iniAtol(value)
			if n < 0 || n > 1000000 {
				return php.Bool(false), nil
			}
			ctx.IniWrite(name, strconv.FormatInt(n, 10))
			php.SetTraceStringLimit(int(n))
			return php.String(previous), nil
		}
		if name == "fiber.stack_size" && value != "" && IniQuantity(value) < 0 {
			// A negative size is refused outright and the setting keeps what it
			// had. Zero is not: it is accepted here and refused later, when a
			// fiber actually needs a stack.
			ctx.Warn("fiber.stack_size must be a positive number")
			return php.Bool(false), nil
		}
		ctx.IniWrite(name, value)
		return php.String(previous), nil
	})
}

// iniAtol reads a leading integer as C's atol does: optional space and sign,
// then digits, and 0 when there are none.
func iniAtol(text string) int64 {
	text = strings.TrimLeft(text, " \t\n\r\v\f")
	negative := false
	if text != "" && (text[0] == '-' || text[0] == '+') {
		negative = text[0] == '-'
		text = text[1:]
	}
	var n int64
	for i := 0; i < len(text) && text[i] >= '0' && text[i] <= '9'; i++ {
		n = n*10 + int64(text[i]-'0')
		if n > 1<<40 {
			break
		}
	}
	if negative {
		return -n
	}
	return n
}

// IniQuantity reads a size written the way an INI setting writes one: a plain
// integer, or one with a K, M or G suffix. An unreadable quantity is zero,
// which is what the engine carries on with after warning.
func IniQuantity(text string) int64 {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	scale := int64(1)
	switch text[len(text)-1] {
	case 'k', 'K':
		scale, text = 1<<10, text[:len(text)-1]
	case 'm', 'M':
		scale, text = 1<<20, text[:len(text)-1]
	case 'g', 'G':
		scale, text = 1<<30, text[:len(text)-1]
	}
	value, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		// The engine warns and carries on with zero, which is what an
		// unreadable quantity means.
		return 0
	}
	return value * scale
}
