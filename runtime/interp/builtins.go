package interp

import (
	cryptorand "crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/moriyoshi/php2go/runtime/libc"
	"github.com/moriyoshi/php2go/runtime/php"
	"github.com/moriyoshi/php2go/runtime/stdlib"
)

// allocationCeiling is where php2go stops trying. PHP has memory_limit and
// reports exhausting it as a fatal error naming the byte counts; php2go has no
// such limit to report, so an allocation this size is a gap rather than a
// wrong answer -- the alternative is a Go panic, which loses the whole run.
const allocationCeiling = 1 << 32

// affordable refuses an allocation php2go cannot make.
func affordable(size int64) error {
	if size < 0 || size > allocationCeiling {
		return &UnsupportedError{Opcode: "allocation of " + strconv.FormatInt(size, 10) +
			" bytes, which needs the memory limit php2go does not model"}
	}
	return nil
}

// builtinFunc is an engine-provided function implemented in Go.
type builtinFunc func(ctx *php.Ctx, args []php.Value) (php.Value, error)

// builtins is small and grows by evidence: the .phpt histogram says which
// function blocks the most tests, so there is no need to guess.

var builtins = map[string]builtinFunc{
	"debug_zval_dump": func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		for _, argument := range args {
			dumped, err := php.DebugZvalDump(ctx, argument)
			if err != nil {
				return php.Undef(), err
			}
			ctx.Write(dumped)
		}
		return php.Null(), nil
	},
	"localeconv": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		convention, available := libc.LocaleConv()
		if !available {
			return php.Undef(), &UnsupportedError{Opcode: "libc localeconv without cgo"}
		}
		result := php.NewArray()
		for _, entry := range []struct {
			name  string
			value string
		}{
			{"decimal_point", convention.DecimalPoint},
			{"thousands_sep", convention.ThousandsSep},
			{"int_curr_symbol", convention.IntCurrSymbol},
			{"currency_symbol", convention.CurrencySymbol},
			{"mon_decimal_point", convention.MonDecimalPoint},
			{"mon_thousands_sep", convention.MonThousandsSep},
			{"positive_sign", convention.PositiveSign},
			{"negative_sign", convention.NegativeSign},
		} {
			result.Set(php.StringKey(entry.name), php.String(entry.value))
		}
		for _, entry := range []struct {
			name  string
			value int
		}{
			{"int_frac_digits", convention.IntFracDigits},
			{"frac_digits", convention.FracDigits},
			{"p_cs_precedes", convention.PCSPrecedes},
			{"p_sep_by_space", convention.PSepBySpace},
			{"n_cs_precedes", convention.NCSPrecedes},
			{"n_sep_by_space", convention.NSepBySpace},
			{"p_sign_posn", convention.PSignPosn},
			{"n_sign_posn", convention.NSignPosn},
		} {
			result.Set(php.StringKey(entry.name), php.Int(int64(entry.value)))
		}
		for _, entry := range []struct {
			name   string
			values []int
		}{
			{"grouping", convention.Grouping},
			{"mon_grouping", convention.MonGrouping},
		} {
			values := php.NewArray()
			for _, value := range entry.values {
				values.Append(php.Int(int64(value)))
			}
			result.Set(php.StringKey(entry.name), php.Arr(values))
		}
		return php.Arr(result), nil
	},
	"nl_langinfo": func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		item := int(args[0].Deref().Int())
		if !validLangInfoItem(item) {
			ctx.Warn("nl_langinfo(): Item '%d' is not valid", item)
			return php.Bool(false), nil
		}
		value, available := libc.NlLangInfo(item)
		if !available {
			return php.Undef(), &UnsupportedError{Opcode: "libc nl_langinfo without cgo"}
		}
		return php.String(value), nil
	},
	"register_tick_function": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("register_tick_function escaped interpreter dispatch")
	},
	// Userland replacements use these reserved primitives when an internal
	// function must raise an engine diagnostic rather than the observably
	// distinct E_USER_* level available through trigger_error(). The clock and
	// the diagnostics moved to runtime/stdlib when the library started being
	// linked into compiled programs, which need the same host.
	"php2go\\internal\\unique_id": internalUniqueID,
	"php2go\\internal\\register_superglobal": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal register_superglobal escaped interpreter dispatch")
	},
	// The embed SAPI has no request script metadata, but process identity remains
	// a real host fact. PHP facades keep the three fixed request answers separate.
	"php2go\\internal\\process_identity": stdlib.ProcessIdentity,
	"php2go\\internal\\resource_usage":   internalResourceUsage,
	"php2go\\internal\\network_database": networkDatabaseBuiltin,
	"php2go\\internal\\crypt":            internalCrypt,
	"php2go\\internal\\password_salt":    internalPasswordSalt,
	"php2go\\internal\\password_verify":  internalPasswordVerify,
	"php2go\\internal\\strptime":         internalStrptime,
	"php2go\\internal\\array_random": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal array_random escaped interpreter dispatch")
	},
	"php2go\\internal\\mt_range": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal mt_range escaped interpreter dispatch")
	},
	// INI restoration mutates per-program settings and is dispatched with Unit.
	"php2go\\internal\\ini_restore": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal ini_restore escaped interpreter dispatch")
	},
	"php2go\\internal\\output_rewriter": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal output_rewriter escaped interpreter dispatch")
	},

	// read_file is handled by streamBuiltin because it needs the installed
	// source loader. The map entry keeps the reserved function discoverable.
	"php2go\\internal\\read_file": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal read_file escaped interpreter dispatch")
	},
	"php2go\\internal\\realpath_cache": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal realpath_cache escaped interpreter dispatch")
	},
	"php2go\\internal\\stream_wrapper_registry": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal stream_wrapper_registry escaped interpreter dispatch")
	},
	// Include-path resolution is loader-aware and therefore dispatched beside
	// stdlib.Stream/file operations. This entry keeps the reserved leaf discoverable.
	"php2go\\internal\\resolve_include_path": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal resolve_include_path escaped interpreter dispatch")
	},
	// Path predicates need loader ownership and host filesystem access.
	// The public signatures and names remain in the bundled PHP library.
	"php2go\\internal\\path_predicate": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal path_predicate escaped interpreter dispatch")
	},
	// Raw path metadata shares the loader/host boundary with predicates. Public
	// warnings and result shaping stay in the bundled PHP library.
	"php2go\\internal\\read_link": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal read_link escaped interpreter dispatch")
	},
	"php2go\\internal\\path_stat": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal path_stat escaped interpreter dispatch")
	},
	"php2go\\internal\\path_mutation": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal path_mutation escaped interpreter dispatch")
	},
	"php2go\\internal\\directory_operation": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal directory_operation escaped interpreter dispatch")
	},
	"php2go\\internal\\path_topology": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal path_topology escaped interpreter dispatch")
	},
	"php2go\\internal\\path_relation": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal path_relation escaped interpreter dispatch")
	},
	"php2go\\internal\\process_umask": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal process_umask escaped interpreter dispatch")
	},
	"php2go\\internal\\copy_path": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal copy_path escaped interpreter dispatch")
	},
	"php2go\\internal\\temporary_file": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal temporary_file escaped interpreter dispatch")
	},

	// Filesystem capacity shares the loader/host boundary with raw metadata.
	// PHP retains public validation, warnings, aliases and result selection.
	"php2go\\internal\\path_space": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal path_space escaped interpreter dispatch")
	},

	// Stream option mutation needs the resource payload owned by streamBuiltin.
	// The map entry makes the reserved leaf visible while keeping public
	// validation and aliases in the bundled PHP library.
	// Stream contexts are resource payload state and therefore share the stdlib.Stream
	// dispatcher. Public normalization and diagnostics remain in bundled PHP.

	// Metadata is irreducible resource introspection and is handled by
	// The four socket operations that build a stdlib.Stream. They are named entries
	// rather than one `stream_socket` switched on a string; the five that act
	// on an existing stdlib.Stream are registered by `runtime/stdlib` instead, which
	// is why they are not here.
	"php2go\\internal\\stream_socket_server": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal stream_socket_server escaped interpreter dispatch")
	},
	"php2go\\internal\\stream_socket_client": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal stream_socket_client escaped interpreter dispatch")
	},
	"php2go\\internal\\stream_socket_accept": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal stream_socket_accept escaped interpreter dispatch")
	},
	"php2go\\internal\\stream_socket_pair": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal stream_socket_pair escaped interpreter dispatch")
	},
	"php2go\\internal\\stream_select": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal stream_select escaped interpreter dispatch")
	},
	"php2go\\internal\\stream_metadata": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal stream_metadata escaped interpreter dispatch")
	},
	"php2go\\internal\\glob": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal glob escaped interpreter dispatch")
	},
	"php2go\\internal\\mail_send": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal mail_send escaped interpreter dispatch")
	},
	"php2go\\internal\\sapi_log": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal sapi_log escaped interpreter dispatch")
	},
	"php2go\\internal\\append_file": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal append_file escaped interpreter dispatch")
	},

	// streamBuiltin. Its historical socket_get_status alias remains userland.
	"stream_get_meta_data": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("stream_get_meta_data escaped interpreter dispatch")
	},
	// IEEE-754 bit casts are the only irreducible part of pack/unpack. Their
	// format grammar, integer wrapping, cursors and diagnostics remain in the
	// userland library; these leaves only cross the float/byte representation
	// boundary PHP itself cannot express.
	"php2go\\internal\\float_bytes": floatBytesBuiltin,
	"php2go\\internal\\bytes_float": bytesFloatBuiltin,

	// These two need the interpreter to resolve and invoke PHP callbacks. Their
	// map entries make them visible to callable/function-existence checks; the
	// real implementations run earlier in callbackBuiltin.
	"php2go\\internal\\validate_callback": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("php2go internal validate_callback escaped interpreter dispatch")
	},
	"unregister_tick_function": func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Undef(), fmt.Errorf("unregister_tick_function escaped interpreter dispatch")
	},
}

func validLangInfoItem(item int) bool {
	return item == 14 ||
		item >= 65536 && item <= 65537 ||
		item >= 131072 && item <= 131121 ||
		item >= 262144 && item <= 262159 ||
		item >= 327680 && item <= 327683
}

func first(args []php.Value) php.Value { return stdlib.FirstArg(args) }

func internalResourceUsage(_ *php.Ctx, args []php.Value) (php.Value, error) {
	who := syscall.RUSAGE_SELF
	if first(args).Int() == 1 {
		who = syscall.RUSAGE_CHILDREN
	}
	var usage syscall.Rusage
	if err := syscall.Getrusage(who, &usage); err != nil {
		return php.Bool(false), nil
	}
	result := php.NewArray()
	set := func(name string, value int64) {
		result.Set(php.StringKey(name), php.Int(value))
	}
	set("ru_oublock", usage.Oublock)
	set("ru_inblock", usage.Inblock)
	set("ru_msgsnd", usage.Msgsnd)
	set("ru_msgrcv", usage.Msgrcv)
	set("ru_maxrss", usage.Maxrss)
	set("ru_ixrss", usage.Ixrss)
	set("ru_idrss", usage.Idrss)
	set("ru_minflt", usage.Minflt)
	set("ru_majflt", usage.Majflt)
	set("ru_nsignals", usage.Nsignals)
	set("ru_nvcsw", usage.Nvcsw)
	set("ru_nivcsw", usage.Nivcsw)
	set("ru_nswap", usage.Nswap)
	set("ru_utime.tv_usec", usage.Utime.Usec)
	set("ru_utime.tv_sec", usage.Utime.Sec)
	set("ru_stime.tv_usec", usage.Stime.Usec)
	set("ru_stime.tv_sec", usage.Stime.Sec)
	return php.Arr(result), nil
}

func floatBytesBuiltin(_ *php.Ctx, args []php.Value) (php.Value, error) {
	size := int(args[1].Int())
	var bits uint64
	switch size {
	case 4:
		bits = uint64(math.Float32bits(float32(args[0].Float())))
	case 8:
		bits = math.Float64bits(args[0].Float())
	default:
		return php.Undef(), fmt.Errorf("php2go internal float_bytes requires width 4 or 8")
	}
	order, err := packedByteOrder(args[2].Int())
	if err != nil {
		return php.Undef(), err
	}
	bytes := make([]byte, size)
	if size == 4 {
		order.PutUint32(bytes, uint32(bits))
	} else {
		order.PutUint64(bytes, bits)
	}
	return php.String(string(bytes)), nil
}

func bytesFloatBuiltin(_ *php.Ctx, args []php.Value) (php.Value, error) {
	bytes := []byte(args[0].String())
	if len(bytes) != 4 && len(bytes) != 8 {
		return php.Undef(), fmt.Errorf("php2go internal bytes_float requires 4 or 8 bytes")
	}
	order, err := packedByteOrder(args[1].Int())
	if err != nil {
		return php.Undef(), err
	}
	if len(bytes) == 4 {
		return php.Float(float64(math.Float32frombits(order.Uint32(bytes)))), nil
	}
	return php.Float(math.Float64frombits(order.Uint64(bytes))), nil
}

// packedByteOrder keeps native-order formats portable without exposing a
// third runtime primitive: 0 is native, 1 little endian and 2 big endian.
func packedByteOrder(order int64) (binary.ByteOrder, error) {
	switch order {
	case 0:
		return binary.NativeEndian, nil
	case 1:
		return binary.LittleEndian, nil
	case 2:
		return binary.BigEndian, nil
	default:
		return nil, fmt.Errorf("php2go internal float bit cast requires byte order 0, 1 or 2")
	}
}

func internalUniqueID(_ *php.Ctx, args []php.Value) (php.Value, error) {
	seconds, microseconds, available := libc.UniqueIDTime()
	if !available {
		return php.Undef(), &UnsupportedError{Opcode: "libc uniqid clock without cgo"}
	}
	result := php.NewArray()
	result.Append(php.Int(int64(seconds)))
	result.Append(php.Int(int64(microseconds)))
	if len(args) > 0 && args[0].Deref().Bool() {
		var bytes [4]byte
		if _, err := cryptorand.Read(bytes[:]); err != nil {
			binary.NativeEndian.PutUint32(bytes[:],
				uint32(time.Now().UnixNano())^seconds^microseconds)
		}
		result.Append(php.Int(int64(binary.NativeEndian.Uint32(bytes[:]))))
	}
	return php.Arr(result), nil
}

func internalCrypt(_ *php.Ctx, args []php.Value) (php.Value, error) {
	if len(args) < 2 {
		return php.Undef(), fmt.Errorf("php2go internal crypt requires a password and salt")
	}
	result, available := libc.Crypt(
		args[0].Deref().String(),
		args[1].Deref().String(),
	)
	if !available {
		return php.Undef(), &UnsupportedError{Opcode: "native crypt_r unavailable"}
	}
	return php.String(result), nil
}

func internalPasswordSalt(_ *php.Ctx, _ []php.Value) (php.Value, error) {
	// php_password_make_salt(22) fills length*3/4+1 bytes, base64 encodes
	// them, takes the first 22 encoded bytes, and substitutes '.' for '+'.
	var entropy [17]byte
	if _, err := cryptorand.Read(entropy[:]); err != nil {
		return php.Bool(false), nil
	}
	encoded := base64.StdEncoding.EncodeToString(entropy[:])
	return php.String(strings.ReplaceAll(encoded[:22], "+", ".")), nil
}

func internalPasswordVerify(_ *php.Ctx, args []php.Value) (php.Value, error) {
	if len(args) < 2 {
		return php.Undef(), fmt.Errorf("php2go internal password_verify requires a password and hash")
	}
	hash := args[1].Deref().String()
	if len(hash) < 13 {
		return php.Bool(false), nil
	}
	result, available := libc.Crypt(args[0].Deref().String(), hash)
	if !available {
		return php.Undef(), &UnsupportedError{Opcode: "native crypt_r unavailable"}
	}
	return php.Bool(subtle.ConstantTimeCompare([]byte(result), []byte(hash)) == 1), nil
}

func internalStrptime(_ *php.Ctx, args []php.Value) (php.Value, error) {
	if len(args) < 2 {
		return php.Undef(), fmt.Errorf("php2go internal strptime requires a timestamp and format")
	}
	if !libc.Available() {
		return php.Undef(), &UnsupportedError{Opcode: "libc strptime without cgo"}
	}
	parsed, ok := libc.Strptime(
		args[0].Deref().String(),
		args[1].Deref().String(),
	)
	if !ok {
		return php.Bool(false), nil
	}
	result := php.NewArray()
	for _, value := range []int{
		parsed.Second,
		parsed.Minute,
		parsed.Hour,
		parsed.MonthDay,
		parsed.Month,
		parsed.Year,
		parsed.WeekDay,
		parsed.YearDay,
	} {
		result.Append(php.Int(int64(value)))
	}
	result.Append(php.String(parsed.Unparsed))
	return php.Arr(result), nil
}

// internalDiagnostic raises from the public builtin call site rather than the
// hidden library opline that invokes this primitive. Hidden helper frames all
// have the current library filename; the first older frame with a different
// file is where the aliased internal function was called.
// varDump reproduces var_dump's exact layout: two spaces per level, a length
// prefix on strings, and a trailing newline per value. .phpt tests compare this
// byte for byte, so the formatting is the contract.
// dumper carries what var_dump and print_r need beyond the value. __debugInfo
// is an ordinary method call, so rendering an object can run user code -- which
// a ctx-only builtin cannot do.
// dumper is now only the *hook* var_dump asks for an object's properties: the
// rendering moved to runtime/php, and what stayed is the part needing a class
// table -- visibility, __debugInfo, whether the class is internal, and what a
// Closure shows.
type dumper struct {
	in   *Interp
	unit *Unit
	ctx  *php.Ctx
	// seen is print_r's and var_export's recursion guard. var_dump's moved to
	// runtime/php with the rendering; these two have not moved yet.
	seen map[*php.Array]bool
	// fatal is a __debugInfo that did not return an array, held so it can
	// replace the output rather than follow it. var_dump gets the same
	// condition as an error from the hook; print_r still reads it here.
}

func newDumper(in *Interp, unit *Unit, ctx *php.Ctx) *dumper {
	return &dumper{in: in, unit: unit, ctx: ctx, seen: map[*php.Array]bool{}}
}

// storageOwner names the class whose private `storage` a dump shows. It is the
// engine class that declares it -- ArrayObject, ArrayIterator,
// SplObjectStorage -- rather than whatever subclass the program wrote, so the
// walk stops at the first ancestor the engine supplied.
func (d *dumper) storageOwner(o *php.Object) string {
	class, known := d.unit.findClass(o.ClassName)
	if !known {
		return o.ClassName
	}
	for current := class; current != nil; {
		if d.unit.isBuiltinOnly(current) {
			return current.Name
		}
		if current.Parent == "" {
			break
		}
		parent, ok := d.unit.findClass(current.Parent)
		if !ok {
			break
		}
		current = parent
	}
	return class.Name
}

// properties is what a dump shows for an object: whatever __debugInfo returns
// if the class declares one, and the real property table otherwise. The second
// result says which, since only real properties carry visibility annotations.
func (d *dumper) properties(_ *php.Ctx, o *php.Object) (*php.Array, bool, error) {
	if lazyUninitialized(o) {
		return lazyVisible(o), false, nil
	}
	if o.Lazy != nil && o.Lazy.Instance != nil {
		// An initialized proxy is still a wrapper: what it shows is the one
		// object it forwards to, under the name the engine gives it.
		wrapper := php.NewArray()
		wrapper.Set(php.StringKey("instance"), php.Obj(o.Lazy.Instance))
		return wrapper, true, nil
	}
	if hidesProperties(o) {
		return php.NewArray(), false, nil
	}
	if d.in == nil || d.unit == nil {
		return o.Props, false, nil
	}
	if info, ok := weakRefDebugInfo(o); ok {
		return info, true, nil
	}
	if info, ok := weakMapDebugInfo(d.ctx, d.unit, o); ok {
		// A WeakMap has no properties either: its keys are objects, which no
		// property table can hold, so the engine describes it as a list of
		// key/value pairs.
		return info, true, nil
	}
	if store, ok := php.AsArrayStore(o); ok {
		// An ArrayObject keeps its elements in an internal store rather than in
		// properties, and the engine shows that store under the name it uses
		// for it -- private to ArrayObject.
		wrapper := php.NewArray()
		wrapper.Set(php.StringKey("storage"), php.Arr(store.Items()))
		// Not "described": the entry carries a visibility annotation, since
		// the engine names it as the private property it is.
		o.Declare("storage", php.Declaration{
			Class: d.storageOwner(o), Visibility: "private",
		})
		return wrapper, false, nil
	}
	if held, ok := o.Internal.(*generator); ok {
		// A Generator has no properties either: the engine describes it
		// through a debug-info handler naming the function it is running.
		return held.debugInfo(), true, nil
	}
	if fn, ok := o.Internal.(*closure); ok {
		// A Closure has no properties: the engine describes it through a
		// debug-info handler instead, and the shape is nothing like a
		// property table.
		return d.unit.closureDebugInfo(d.in, fn), true, nil
	}
	class, known := d.unit.findClass(o.ClassName)
	if !known {
		return o.Props, false, nil
	}
	body, declaring, found := d.unit.resolveMethod(class, "__debugInfo")
	if !found {
		return o.Props, false, nil
	}
	described, err := d.in.invoke(d.ctx, d.unit, body, o, declaring, class, nil, 0)
	if err != nil {
		return o.Props, false, nil
	}
	if described.Deref().Kind() != php.KindArray {
		// **The fatal replaces the dump**, so it is written here and the
		// request ends: whatever the renderer had built is in a local nobody
		// will read again. It used to travel back as a private error type that
		// each caller unwrapped, which stopped working the moment a caller was
		// PHP -- `print_r` is harvested now, and a Go error means nothing to
		// it. `php.Exit` is the shared way to say this.
		file, line := d.ctx.Location()
		d.ctx.Write(fmt.Sprintf(
			"\nFatal error: __debuginfo() must return an array in %s on line %d\n", file, line))
		return o.Props, false, php.Exit(255)
	}
	return described.Deref().Array(), true, nil
}

// sortBuiltin builds sort() and rsort(), which discard the keys and renumber.
// sortTarget and replaceList moved to `runtime/php` with the sort family; the
// interpreter's own sorts -- the ones that call back into user code, so they
// could not move -- still need them.
func sortTarget(args []php.Value) (*php.Array, bool) { return stdlib.SortTarget(args) }

func replaceList(target *php.Array, values []php.Value) { stdlib.ReplaceList(target, values) }

// replaceArray rewrites an array in place from another, so every holder of the
// original box sees the new contents.
func replaceArray(target *php.Array, source *php.Array) {
	for _, key := range target.Keys() {
		target.Unset(key)
	}
	target.Renumber()
	source.Each(func(k php.Key, v php.Value) bool {
		if k.IsInt {
			target.Append(v)
			return true
		}
		target.Set(k, v)
		return true
	})
}

// sharedBuiltins bridges the functions that live in runtime/php into this
// table, so compiled code and the interpreter run one implementation.
//
// They are registered *here*, in `builtins`, rather than dispatched earlier --
// and that is not a detail. A builtin reached through this table passes
// `verifyBuiltinArgs` first, which applies the engine's arginfo: an object
// handed to a `string` parameter is converted through `__toString` on the way
// in. Routing printf past that instead made `printf("[%s]", $objectWithToString)`
// print nothing, with the moved code byte-identical to the original.
//
// The rule the mistake produced: **a moved builtin goes back on the path it was
// on.** Where it is dispatched is part of its behaviour, because the pipeline
// around it does work the function itself never sees.
func init() {
	for _, name := range php.BuiltinNames() {
		if _, taken := builtins[name]; taken {
			continue
		}
		switch name {
		case "set_error_handler", "restore_error_handler":
			// Dispatched in program.go, which is the path these were on.
			continue
		}
		fn, _ := php.LookupBuiltin(name)
		builtins[name] = builtinFunc(fn)
	}
}
