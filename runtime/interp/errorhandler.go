package interp

import "github.com/moriyoshi/php2go/runtime/php"

// errorConstant names the E_* constant for a diagnostic level. The value is
// looked up in the harvested table rather than written out, since the numbers
// belong to the linked build.
func errorConstant(level string) string {
	switch level {
	case "Warning":
		return "E_WARNING"
	case "Notice":
		return "E_NOTICE"
	case "Deprecated":
		return "E_DEPRECATED"
	case "UserDeprecated":
		return "E_USER_DEPRECATED"
	case "UserWarning":
		return "E_USER_WARNING"
	case "UserNotice":
		return "E_USER_NOTICE"
	}
	return "E_USER_ERROR"
}

// installErrorHandler wires the context's diagnostics to whatever
// set_error_handler installed. The handler is consulted for every diagnostic,
// including one that error_reporting would hide or @ would suppress: the mask
// gates the default handler, not the callback.
func (in *Interp) installErrorHandler(ctx *php.Ctx, unit *Unit) {
	ctx.SetErrorHandler(func(level, message, file string, line uint32) (bool, error) {
		// The stack lives on the Ctx now, and so does the rule about
		// set_error_handler(null) being *on* it rather than clearing it.
		handler, ok := ctx.CurrentErrorHandler()
		if !ok {
			return false, nil
		}
		code, _ := in.lookupConstant(ctx, errorConstant(level))
		// Off the stack while it runs (EnterErrorHandler).
		restore := ctx.EnterErrorHandler()
		answer, err := in.callValueAt(ctx, unit, handler, []php.Value{
			code, php.String(message), php.String(file), php.Int(int64(line)),
		}, file, line, 0)
		restore()
		if err != nil {
			// The handler threw. That counts as handling the diagnostic -- the
			// default text is not printed -- and the exception is carried out.
			return true, err
		}
		// Only an explicit false asks for the default handling as well; a
		// handler that returns nothing has dealt with it.
		return answer.Deref().Kind() != php.KindFalse, nil
	})
}
