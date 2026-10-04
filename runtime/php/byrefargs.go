package php

// Which internal functions take an argument **by reference**, and at which
// positions.
//
// This is what settles a `ZEND_SEND_VAR_EX` -- the engine's "the callee's
// arginfo decides at run time" send, emitted wherever a callee is not resolved
// at compile time, which is most calls. The caller passes its storage and the
// receiving end decides: a by-reference position keeps the box, and every other
// one takes the value out of it.
//
// The table is the **engine's**, read back rather than recalled, by walking
// `get_defined_functions()["internal"]` through Reflection and recording every
// parameter with `isPassedByReference()`. It therefore covers names php2go does
// not implement yet: a name that is not in the library surface is refused
// before this is consulted, and having the answer already right is what stops
// the day `sort` lands from being the day by-value arguments start aliasing.
var builtinRefArgs = map[string][]int{
	"array_multisort": {0, 1},
	"array_pop":       {0},
	"array_push":      {0},
	"array_shift":     {0},
	"array_splice":    {0},
	"array_unshift":   {0},
	"array_walk":      {0},

	"array_walk_recursive": {0},
	"arsort":               {0},
	"asort":                {0},
	"dns_get_mx":           {1, 2},
	"dns_get_record":       {2, 3},
	"end":                  {0},
	"exec":                 {1, 2},
	"extract":              {0},
	"flock":                {2},
	"fscanf":               {2},
	"fsockopen":            {2, 3},

	"getimagesize":           {1},
	"getimagesizefromstring": {1},
	"getmxrr":                {1, 2},
	"getopt":                 {2},
	"headers_sent":           {0, 1},
	"is_callable":            {2},
	"krsort":                 {0},
	"ksort":                  {0},
	"natcasesort":            {0},
	"natsort":                {0},
	"next":                   {0},
	"parse_str":              {1},
	"passthru":               {1},
	"pfsockopen":             {2, 3},

	"preg_filter":                 {4},
	"preg_match":                  {2},
	"preg_match_all":              {2},
	"preg_replace":                {4},
	"preg_replace_callback":       {4},
	"preg_replace_callback_array": {3},
	"prev":                        {0},
	"reset":                       {0},
	"rsort":                       {0},
	"settype":                     {0},
	"shuffle":                     {0},
	"similar_text":                {2},
	"sort":                        {0},
	"sscanf":                      {2},

	"str_ireplace":           {3},
	"str_replace":            {3},
	"stream_select":          {0, 1, 2},
	"stream_socket_accept":   {2},
	"stream_socket_client":   {1, 2},
	"stream_socket_recvfrom": {3},
	"stream_socket_server":   {1, 2},
	"system":                 {1},
	"uasort":                 {0},
	"uksort":                 {0},
	"usort":                  {0},
}

// internalRefArgs is the same question for php2go's own `PHP2Go\Internal\*`
// leaves. The engine has never heard of them, so Reflection has nothing to say
// and they cannot go in the table above without breaking what its comment
// claims it to be.
//
// `array_id` answers a value's **identity**, which is the one question a copy
// destroys: taking it by value gave the id of php2go's own clone, so a userland
// recursion guard built on it never fired. It is the whole reason the table
// exists.
var internalRefArgs = map[string][]int{
	`php2go\internal\array_id`: {0},
}

// BuiltinTakesRef reports whether an internal function's nth parameter is
// declared by reference. Names are matched case-insensitively, as PHP matches
// function names.
func BuiltinTakesRef(name string, n int) bool {
	positions, known := builtinRefArgs[lowerASCII(name)]
	if !known {
		positions, known = internalRefArgs[lowerASCII(name)]
	}
	if !known {
		return false
	}
	for _, p := range positions {
		if p == n {
			return true
		}
	}
	return false
}

// UnboxBuiltinArgs takes the values out of the boxes an undecided send passed,
// leaving the positions the callee declared by reference alone.
//
// It answers the same slice when nothing needed unboxing, which is the ordinary
// case: a send that could settle the mode at compile time passed a value
// already.
func UnboxBuiltinArgs(name string, args []Value) []Value {
	var out []Value
	for i, a := range args {
		if _, boxed := a.RefOf(); !boxed || BuiltinTakesRef(name, i) {
			if out != nil {
				out = append(out, a)
			}
			continue
		}
		if out == nil {
			out = append(out, args[:i]...)
		}
		out = append(out, a.Deref().Copy())
	}
	if out == nil {
		return args
	}
	return out
}

// UnboxArgs is UnboxBuiltinArgs for a callee that takes **everything** by
// value: the engine's own constructors and methods -- Throwable, Closure,
// Generator -- none of which declare a by-reference parameter. They read
// `args[i]` directly rather than through a compiled `recv`, so the undecided
// send has to be settled for them here.
func UnboxArgs(args []Value) []Value { return UnboxBuiltinArgs("", args) }

// lowerASCII lowercases a function name. PHP function names are
// case-insensitive and ASCII-folded, which is all this needs.
func lowerASCII(s string) string {
	needs := false
	for i := 0; i < len(s); i++ {
		if s[i] >= 'A' && s[i] <= 'Z' {
			needs = true
			break
		}
	}
	if !needs {
		return s
	}
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
