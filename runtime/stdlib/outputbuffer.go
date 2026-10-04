package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// Output buffering. The stack itself lives on the `Ctx` -- every write goes
// through it -- and what kept these functions in the interpreter was the one
// thing the stack cannot do for itself: `ob_start` takes a PHP callable, and
// running one needs a function and class table. `Ctx.Call` is that hook, so the
// bodies belong here where compiled code can reach them too.
//
// The rule that shapes all of them: **the filter runs when the buffer ends, not
// when it is read.** `ob_get_contents` and `ob_get_clean` answer with the raw
// text; what a flush *writes* is whatever the filter returned. Returning false
// means "use the original" -- anything else is taken as a string, so returning
// null empties the output. A discarded buffer still runs its filter; only the
// result is dropped.

func init() {
	php.RegisterBuiltin("ob_start", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		var filter any
		handlerName := "default output handler"
		if len(args) > 0 {
			if given := args[0].Deref(); given.Kind() != php.KindNull && !given.IsUndef() {
				// **A bad handler is not a TypeError.** Every other builtin's
				// callback parameter raises one; `ob_start` warns with the
				// same reason text, adds a notice, and answers false -- read
				// back from the engine, which reports
				//
				//	Warning: ob_start(): function "nope" not found or invalid function name
				//	Notice: ob_start(): Failed to create buffer
				//
				// and carries on with no buffer opened.
				if reason, decided := ctx.CallableReasonFor(given); decided && reason != "" {
					ctx.Warn("ob_start(): %s", reason)
					ctx.Notice("ob_start(): Failed to create buffer")
					return php.Bool(false), nil
				}
				filter = given
				handlerName = outputHandlerName(given)
			}
		}
		chunk := int64(0)
		if len(args) > 1 {
			chunk = args[1].Deref().Int()
		}
		flags := php.OutputHandlerStdFlags
		if len(args) > 2 {
			flags = args[2].Deref().Int()
		}
		ctx.PushBufferConfigured(filter, handlerName, chunk, flags)
		return php.Bool(true), nil
	})

	php.RegisterBuiltin("ob_clean", retainedBuffer("ob_clean", false))
	php.RegisterBuiltin("ob_flush", retainedBuffer("ob_flush", true))

	php.RegisterBuiltin("ob_end_clean", endBuffer("ob_end_clean"))
	php.RegisterBuiltin("ob_end_flush", endBuffer("ob_end_flush"))
	php.RegisterBuiltin("ob_get_clean", endBuffer("ob_get_clean"))
	php.RegisterBuiltin("ob_get_flush", endBuffer("ob_get_flush"))

	php.RegisterBuiltin("ob_get_contents", func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		held, ok := ctx.BufferContents()
		if !ok {
			return php.Bool(false), nil
		}
		return php.String(held), nil
	})
	php.RegisterBuiltin("ob_get_length", func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		held, ok := ctx.BufferContents()
		if !ok {
			return php.Bool(false), nil
		}
		return php.Int(int64(len(held))), nil
	})
	php.RegisterBuiltin("ob_get_level", func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Int(int64(ctx.BufferLevel())), nil
	})
	php.RegisterBuiltin(`php2go\internal\output_buffer_status`, func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		result := php.NewArray()
		for _, status := range ctx.BufferStatuses() {
			entry := php.NewArray()
			entry.Set(php.StringKey("name"), php.String(status.Name))
			entry.Set(php.StringKey("type"), php.Int(status.Type))
			entry.Set(php.StringKey("flags"), php.Int(status.Flags))
			entry.Set(php.StringKey("level"), php.Int(status.Level))
			entry.Set(php.StringKey("chunk_size"), php.Int(status.ChunkSize))
			entry.Set(php.StringKey("buffer_size"), php.Int(status.BufferSize))
			entry.Set(php.StringKey("buffer_used"), php.Int(status.BufferUsed))
			result.Append(php.Arr(entry))
		}
		return php.Arr(result), nil
	})
}

// retainedBuffer is ob_clean and ob_flush: the buffer is emptied and the level
// *stays*, which is the whole difference from ob_end_clean and ob_end_flush.
// A failure names the buffer it could not act on -- the engine distinguishes
// having no buffer from having one that refuses the operation, and says so.
func retainedBuffer(name string, flush bool) php.Builtin {
	operation := int64(2)
	verb, noun := "delete", "delete"
	if flush {
		operation = 4
		verb, noun = "flush", "flush"
	}
	return func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		statuses := ctx.BufferStatuses()
		ok, err := ctx.ProcessBuffer(operation, flush)
		if !ok {
			if len(statuses) == 0 {
				ctx.Notice("%s(): Failed to %s buffer. No buffer to %s", name, verb, noun)
			} else {
				status := statuses[len(statuses)-1]
				ctx.Notice("%s(): Failed to %s buffer of %s (%d)", name, verb, status.Name, status.Level)
			}
			return php.Bool(false), nil
		}
		if err != nil {
			return php.Undef(), err
		}
		return php.Bool(true), nil
	}
}

// endBuffer is the four functions that pop a level. They differ in two bits --
// whether what was held is written out, and whether it is returned -- and share
// everything else, including the phase word the filter is told about.
func endBuffer(name string) php.Builtin {
	flush := name == "ob_end_flush" || name == "ob_get_flush"
	answers := name == "ob_get_clean" || name == "ob_get_flush"
	return func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		phase := int64(8) // PHP_OUTPUT_HANDLER_FINAL
		if name == "ob_end_clean" || name == "ob_get_clean" {
			phase |= 2 // ... | PHP_OUTPUT_HANDLER_CLEAN
		}
		statuses := ctx.BufferStatuses()
		if len(statuses) > 0 && statuses[len(statuses)-1].Flags&0x1000 == 0 {
			phase |= 1 // the handler has not run yet, so this is also its start
		}
		filter, filtered := ctx.BufferFilter()
		if len(statuses) > 0 && statuses[len(statuses)-1].Flags&0x2000 != 0 {
			// A handler that declined stays on the stack disabled; it is not
			// asked again.
			filtered = false
		}
		held, ok := ctx.PopBuffer(false)
		if !ok {
			return php.Bool(false), nil
		}
		written := held
		if filtered {
			// The filter's own output goes nowhere: it runs while no buffer of
			// its own is open and the one it belongs to is already gone.
			produced, accepted, err := ctx.RunBufferFilter(filter, held, phase)
			if err != nil {
				// The filter failed. What it was given is written out
				// unchanged -- the same as declining -- and the throw travels.
				if flush {
					ctx.Write(held)
				}
				return php.Undef(), err
			}
			if accepted {
				written = produced
			}
		}
		if flush {
			ctx.Write(written)
		}
		if answers {
			// What is *returned* is the raw buffer, whatever the filter made of it.
			return php.String(held), nil
		}
		return php.Bool(true), nil
	}
}

// outputHandlerName is what ob_get_status and ob_list_handlers report for a
// buffer's handler. A Closure has no name of its own, so PHP names the method
// that would be invoked -- which the object branch already produces, since a
// Closure's class name is `Closure`.
func outputHandlerName(value php.Value) string {
	held := value.Deref()
	switch held.Kind() {
	case php.KindString:
		return held.String()
	case php.KindObject:
		return php.DisplayName(held.Object().ClassName) + "::__invoke"
	case php.KindArray:
		target, _ := held.Array().Get(php.IntKey(0))
		method, _ := held.Array().Get(php.IntKey(1))
		target = target.Deref()
		prefix := target.String()
		if target.Kind() == php.KindObject {
			prefix = php.DisplayName(target.Object().ClassName)
		}
		return prefix + "::" + method.Deref().String()
	default:
		return "default output handler"
	}
}
