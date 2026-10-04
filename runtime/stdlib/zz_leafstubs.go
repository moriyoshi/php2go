package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// **A leaf only the interpreter answers is refused where it is called, not
// where it is linked.** The harvested library reaches these
// `php2go\internal\...` primitives from bodies a compiled program links
// wholesale -- error_log reaches mail delivery, file functions reach the
// interpreter's path layer -- and a leaf missing from this runtime used to
// refuse the whole program at link time, even when the path that calls it
// never runs (bug39542: error_log linked by an autoloader that never logs).
//
// Each stub reports the gap when it is actually called, which is the same
// honest answer for the runs that reach it. An UnsupportedError is no PHP
// throwable, so no catch in the program can swallow it. A real
// implementation registered anywhere (this package or runtime/php) wins: the
// stub only fills an empty name, and this file sorts last so this package's
// own registrations run first. stream_wrapper_registry is answered by the
// generated program itself and is not stubbed.
var interpreterOnlyLeaves = []string{
	"bytes_float",
	"copy_path",
	"crypt",
	"float_bytes",
	"glob",
	"ini_restore",
	"network_database",
	"output_rewriter",
	"password_salt",
	"password_verify",
	"path_mutation",
	"path_predicate",
	"path_relation",
	"path_space",
	"path_stat",
	"path_topology",
	"process_umask",
	"read_link",
	"realpath_cache",
	"resolve_include_path",
	"resource_usage",
	"stream_select",
	"stream_socket_accept",
	"stream_socket_client",
	"stream_socket_pair",
	"stream_socket_server",
	"strptime",
	"temporary_file",
	"unique_id",
	"validate_callback",
	"mail_send",
}

func init() {
	for _, leaf := range interpreterOnlyLeaves {
		name := `php2go\internal\` + leaf
		if _, taken := php.LookupBuiltin(name); taken {
			continue
		}
		php.RegisterBuiltin(name, func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
			return php.Undef(), php.Unsupported("userland leaf %s is not in the compiled runtime", name)
		})
	}
}
