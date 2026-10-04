// Package userland catalogs builtin functions whose bodies are implemented in PHP.
package userland

import _ "embed"

//go:generate go run ./cmd/surfacegen -root ../.. -output internal/userland/surface.tsv
//go:generate go run ./cmd/shapegen -root ../.. -output internal/userland/shape.tsv

// Function maps an engine-visible builtin name to a private implementation in
// its library source.
type Function struct {
	Name           string
	Implementation string
}

// Library is one independently harvestable PHP source and its public aliases.
type Library struct {
	VirtualPath  string
	Source       []byte
	Initializers []string
	Functions    []Function
}

//go:embed ext/standard.php
var standardSource []byte

//go:embed ext/streams.php
var streamsSource []byte

// Libraries returns a fresh catalog. Source bytes and slices are copied so a
// caller cannot mutate the process-wide embedded definitions.
func Libraries() []Library {
	return []Library{
		{
			VirtualPath: "/app/.php2go/ext/standard.php",
			Source:      append([]byte(nil), standardSource...),
			Initializers: []string{
				"PHP2Go\\Extension\\Standard\\register_superglobals_impl",
			},
			Functions: []Function{
				{
					Name:           "dns_check_record",
					Implementation: "PHP2Go\\Extension\\Standard\\dns_check_record_impl",
				},
				{
					Name:           "checkdnsrr",
					Implementation: "PHP2Go\\Extension\\Standard\\checkdnsrr_impl",
				},
				{
					Name:           "dns_get_record",
					Implementation: "PHP2Go\\Extension\\Standard\\dns_get_record_impl",
				},
				{
					Name:           "dns_get_mx",
					Implementation: "PHP2Go\\Extension\\Standard\\dns_get_mx_impl",
				},
				{
					Name:           "getmxrr",
					Implementation: "PHP2Go\\Extension\\Standard\\getmxrr_impl",
				},
				{
					Name:           "error_log",
					Implementation: "PHP2Go\\Extension\\Standard\\error_log_impl",
				},
				{
					Name:           "mail",
					Implementation: "PHP2Go\\Extension\\Standard\\mail_impl",
				},
				{
					Name:           "gethostbyaddr",
					Implementation: "PHP2Go\\Extension\\Standard\\gethostbyaddr_impl",
				},
				{
					Name:           "gethostbyname",
					Implementation: "PHP2Go\\Extension\\Standard\\gethostbyname_impl",
				},
				{
					Name:           "gethostbynamel",
					Implementation: "PHP2Go\\Extension\\Standard\\gethostbynamel_impl",
				},
				{
					Name:           "get_html_translation_table",
					Implementation: "PHP2Go\\Extension\\Standard\\get_html_translation_table_impl",
				},
				{
					Name:           "getprotobyname",
					Implementation: "PHP2Go\\Extension\\Standard\\getprotobyname_impl",
				},
				{
					Name:           "getprotobynumber",
					Implementation: "PHP2Go\\Extension\\Standard\\getprotobynumber_impl",
				},
				{
					Name:           "getservbyname",
					Implementation: "PHP2Go\\Extension\\Standard\\getservbyname_impl",
				},
				{
					Name:           "getservbyport",
					Implementation: "PHP2Go\\Extension\\Standard\\getservbyport_impl",
				},
				{
					Name:           "net_get_interfaces",
					Implementation: "PHP2Go\\Extension\\Standard\\net_get_interfaces_impl",
				},
				{
					Name:           "header",
					Implementation: "PHP2Go\\Extension\\Standard\\header_impl",
				},
				{
					Name:           "header_register_callback",
					Implementation: "PHP2Go\\Extension\\Standard\\header_register_callback_impl",
				},
				{
					Name:           "header_remove",
					Implementation: "PHP2Go\\Extension\\Standard\\header_remove_impl",
				},
				{
					Name:           "headers_list",
					Implementation: "PHP2Go\\Extension\\Standard\\headers_list_impl",
				},
				{
					Name:           "headers_sent",
					Implementation: "PHP2Go\\Extension\\Standard\\headers_sent_impl",
				},
				{
					Name:           "http_response_code",
					Implementation: "PHP2Go\\Extension\\Standard\\http_response_code_impl",
				},
				{
					Name:           "setcookie",
					Implementation: "PHP2Go\\Extension\\Standard\\setcookie_impl",
				},
				{
					Name:           "setrawcookie",
					Implementation: "PHP2Go\\Extension\\Standard\\setrawcookie_impl",
				},
				{
					Name:           "addcslashes",
					Implementation: "PHP2Go\\Extension\\Standard\\addcslashes_impl",
				},
				{
					Name:           "str_shuffle",
					Implementation: "PHP2Go\\Extension\\Standard\\str_shuffle_impl",
				},
				{
					Name:           "print_r",
					Implementation: "PHP2Go\\Extension\\Standard\\print_r_impl",
				},
				{
					Name:           "ucfirst",
					Implementation: "PHP2Go\\Extension\\Standard\\ucfirst_impl",
				},
				{
					Name:           "lcfirst",
					Implementation: "PHP2Go\\Extension\\Standard\\lcfirst_impl",
				},
				{
					Name:           "ucwords",
					Implementation: "PHP2Go\\Extension\\Standard\\ucwords_impl",
				},
				{
					Name:           "nl2br",
					Implementation: "PHP2Go\\Extension\\Standard\\nl2br_impl",
				},
				{
					Name:           "quotemeta",
					Implementation: "PHP2Go\\Extension\\Standard\\quotemeta_impl",
				},
				{
					Name:           "strrev",
					Implementation: "PHP2Go\\Extension\\Standard\\strrev_impl",
				},
				{
					Name:           "str_pad",
					Implementation: "PHP2Go\\Extension\\Standard\\str_pad_impl",
				},
				{
					Name:           "array_sum",
					Implementation: "PHP2Go\\Extension\\Standard\\array_sum_impl",
				},
				{
					Name:           "array_product",
					Implementation: "PHP2Go\\Extension\\Standard\\array_product_impl",
				},
				{
					Name:           "array_flip",
					Implementation: "PHP2Go\\Extension\\Standard\\array_flip_impl",
				},
				{
					Name:           "array_all",
					Implementation: "PHP2Go\\Extension\\Standard\\array_all_impl",
				},
				{
					Name:           "array_any",
					Implementation: "PHP2Go\\Extension\\Standard\\array_any_impl",
				},
				{
					Name:           "array_change_key_case",
					Implementation: "PHP2Go\\Extension\\Standard\\array_change_key_case_impl",
				},
				{
					Name:           "array_diff_uassoc",
					Implementation: "PHP2Go\\Extension\\Standard\\array_diff_uassoc_impl",
				},
				{
					Name:           "array_diff_ukey",
					Implementation: "PHP2Go\\Extension\\Standard\\array_diff_ukey_impl",
				},
				{
					Name:           "array_find",
					Implementation: "PHP2Go\\Extension\\Standard\\array_find_impl",
				},
				{
					Name:           "array_find_key",
					Implementation: "PHP2Go\\Extension\\Standard\\array_find_key_impl",
				},
				{
					Name:           "array_intersect_uassoc",
					Implementation: "PHP2Go\\Extension\\Standard\\array_intersect_uassoc_impl",
				},
				{
					Name:           "array_intersect_ukey",
					Implementation: "PHP2Go\\Extension\\Standard\\array_intersect_ukey_impl",
				},
				{
					Name:           "array_multisort",
					Implementation: "PHP2Go\\Extension\\Standard\\array_multisort_impl",
				},
				{
					Name:           "array_rand",
					Implementation: "PHP2Go\\Extension\\Standard\\array_rand_impl",
				},
				{
					Name:           "array_replace",
					Implementation: "PHP2Go\\Extension\\Standard\\array_replace_impl",
				},
				{
					Name:           "array_replace_recursive",
					Implementation: "PHP2Go\\Extension\\Standard\\array_replace_recursive_impl",
				},
				{
					Name:           "array_udiff",
					Implementation: "PHP2Go\\Extension\\Standard\\array_udiff_impl",
				},
				{
					Name:           "array_udiff_assoc",
					Implementation: "PHP2Go\\Extension\\Standard\\array_udiff_assoc_impl",
				},
				{
					Name:           "array_udiff_uassoc",
					Implementation: "PHP2Go\\Extension\\Standard\\array_udiff_uassoc_impl",
				},
				{
					Name:           "array_uintersect",
					Implementation: "PHP2Go\\Extension\\Standard\\array_uintersect_impl",
				},
				{
					Name:           "array_uintersect_assoc",
					Implementation: "PHP2Go\\Extension\\Standard\\array_uintersect_assoc_impl",
				},
				{
					Name:           "array_uintersect_uassoc",
					Implementation: "PHP2Go\\Extension\\Standard\\array_uintersect_uassoc_impl",
				},
				{
					Name:           "array_walk_recursive",
					Implementation: "PHP2Go\\Extension\\Standard\\array_walk_recursive_impl",
				},
				{
					Name:           "assert_options",
					Implementation: "PHP2Go\\Extension\\Standard\\assert_options_impl",
				},
				{
					Name:           "base_convert",
					Implementation: "PHP2Go\\Extension\\Standard\\base_convert_impl",
				},
				{
					Name:           "bindec",
					Implementation: "PHP2Go\\Extension\\Standard\\bindec_impl",
				},
				{
					Name:           "chgrp",
					Implementation: "PHP2Go\\Extension\\Standard\\chgrp_impl",
				},
				{
					Name:           "chmod",
					Implementation: "PHP2Go\\Extension\\Standard\\chmod_impl",
				},
				{
					Name:           "chown",
					Implementation: "PHP2Go\\Extension\\Standard\\chown_impl",
				},
				{
					Name:           "clearstatcache",
					Implementation: "PHP2Go\\Extension\\Standard\\clearstatcache_impl",
				},
				{
					Name:           "closedir",
					Implementation: "PHP2Go\\Extension\\Standard\\closedir_impl",
				},
				{
					Name:           "copy",
					Implementation: "PHP2Go\\Extension\\Standard\\copy_impl",
				},
				{
					Name:           "dir",
					Implementation: "PHP2Go\\Extension\\Standard\\dir_impl",
				},
				{
					Name:           "count_chars",
					Implementation: "PHP2Go\\Extension\\Standard\\count_chars_impl",
				},
				{
					Name:           "crypt",
					Implementation: "PHP2Go\\Extension\\Standard\\crypt_impl",
				},
				{
					Name:           "connection_aborted",
					Implementation: "PHP2Go\\Extension\\Standard\\connection_aborted_impl",
				},
				{
					Name:           "connection_status",
					Implementation: "PHP2Go\\Extension\\Standard\\connection_status_impl",
				},
				{
					Name:           "convert_uudecode",
					Implementation: "PHP2Go\\Extension\\Standard\\convert_uudecode_impl",
				},
				{
					Name:           "convert_uuencode",
					Implementation: "PHP2Go\\Extension\\Standard\\convert_uuencode_impl",
				},
				{
					Name:           "decbin",
					Implementation: "PHP2Go\\Extension\\Standard\\decbin_impl",
				},
				{
					Name:           "dechex",
					Implementation: "PHP2Go\\Extension\\Standard\\dechex_impl",
				},
				{
					Name:           "decoct",
					Implementation: "PHP2Go\\Extension\\Standard\\decoct_impl",
				},
				{
					Name:           "disk_free_space",
					Implementation: "PHP2Go\\Extension\\Standard\\disk_free_space_impl",
				},
				{
					Name:           "disk_total_space",
					Implementation: "PHP2Go\\Extension\\Standard\\disk_total_space_impl",
				},
				{
					Name:           "diskfreespace",
					Implementation: "PHP2Go\\Extension\\Standard\\diskfreespace_impl",
				},
				{
					Name:           "dl",
					Implementation: "PHP2Go\\Extension\\Standard\\dl_impl",
				},
				{
					Name:           "doubleval",
					Implementation: "PHP2Go\\Extension\\Standard\\doubleval_impl",
				},
				{
					Name:           "escapeshellarg",
					Implementation: "PHP2Go\\Extension\\Standard\\escapeshellarg_impl",
				},
				{
					Name:           "escapeshellcmd",
					Implementation: "PHP2Go\\Extension\\Standard\\escapeshellcmd_impl",
				},
				{
					Name:           "file",
					Implementation: "PHP2Go\\Extension\\Standard\\file_impl",
				},
				{
					Name:           "file_exists",
					Implementation: "PHP2Go\\Extension\\Standard\\file_exists_impl",
				},
				{
					Name:           "fileatime",
					Implementation: "PHP2Go\\Extension\\Standard\\fileatime_impl",
				},
				{
					Name:           "filectime",
					Implementation: "PHP2Go\\Extension\\Standard\\filectime_impl",
				},
				{
					Name:           "filegroup",
					Implementation: "PHP2Go\\Extension\\Standard\\filegroup_impl",
				},
				{
					Name:           "fileinode",
					Implementation: "PHP2Go\\Extension\\Standard\\fileinode_impl",
				},
				{
					Name:           "filemtime",
					Implementation: "PHP2Go\\Extension\\Standard\\filemtime_impl",
				},
				{
					Name:           "fileowner",
					Implementation: "PHP2Go\\Extension\\Standard\\fileowner_impl",
				},
				{
					Name:           "fileperms",
					Implementation: "PHP2Go\\Extension\\Standard\\fileperms_impl",
				},
				{
					Name:           "filesize",
					Implementation: "PHP2Go\\Extension\\Standard\\filesize_impl",
				},
				{
					Name:           "filetype",
					Implementation: "PHP2Go\\Extension\\Standard\\filetype_impl",
				},
				{
					Name:           "linkinfo",
					Implementation: "PHP2Go\\Extension\\Standard\\linkinfo_impl",
				},
				{
					Name:           "lchgrp",
					Implementation: "PHP2Go\\Extension\\Standard\\lchgrp_impl",
				},
				{
					Name:           "lchown",
					Implementation: "PHP2Go\\Extension\\Standard\\lchown_impl",
				},
				{
					Name:           "readlink",
					Implementation: "PHP2Go\\Extension\\Standard\\readlink_impl",
				},
				{
					Name:           "lstat",
					Implementation: "PHP2Go\\Extension\\Standard\\lstat_impl",
				},
				{
					Name:           "stat",
					Implementation: "PHP2Go\\Extension\\Standard\\stat_impl",
				},
				{
					Name:           "fpow",
					Implementation: "PHP2Go\\Extension\\Standard\\fpow_impl",
				},
				{
					Name:           "ftok",
					Implementation: "PHP2Go\\Extension\\Standard\\ftok_impl",
				},
				{
					Name:           "get_browser",
					Implementation: "PHP2Go\\Extension\\Standard\\get_browser_impl",
				},
				{
					Name:           "get_cfg_var",
					Implementation: "PHP2Go\\Extension\\Standard\\get_cfg_var_impl",
				},
				{
					Name:           "get_current_user",
					Implementation: "PHP2Go\\Extension\\Standard\\get_current_user_impl",
				},
				{
					Name:           "get_headers",
					Implementation: "PHP2Go\\Extension\\Standard\\get_headers_impl",
				},
				{
					Name:           "getlastmod",
					Implementation: "PHP2Go\\Extension\\Standard\\getlastmod_impl",
				},
				{
					Name:           "getrusage",
					Implementation: "PHP2Go\\Extension\\Standard\\getrusage_impl",
				},
				{
					Name:           "glob",
					Implementation: "PHP2Go\\Extension\\Standard\\glob_impl",
				},
				{
					Name:           "getmygid",
					Implementation: "PHP2Go\\Extension\\Standard\\getmygid_impl",
				},
				{
					Name:           "getmyinode",
					Implementation: "PHP2Go\\Extension\\Standard\\getmyinode_impl",
				},
				{
					Name:           "getmypid",
					Implementation: "PHP2Go\\Extension\\Standard\\getmypid_impl",
				},
				{
					Name:           "getmyuid",
					Implementation: "PHP2Go\\Extension\\Standard\\getmyuid_impl",
				},
				{
					Name:           "gettimeofday",
					Implementation: "PHP2Go\\Extension\\Standard\\gettimeofday_impl",
				},
				{
					Name:           "getopt",
					Implementation: "PHP2Go\\Extension\\Standard\\getopt_impl",
				},
				{
					Name:           "hrtime",
					Implementation: "PHP2Go\\Extension\\Standard\\hrtime_impl",
				},
				{
					Name:           "microtime",
					Implementation: "PHP2Go\\Extension\\Standard\\microtime_impl",
				},
				{
					Name:           "shuffle",
					Implementation: "PHP2Go\\Extension\\Standard\\shuffle_impl",
				},
				{
					Name:           "sleep",
					Implementation: "PHP2Go\\Extension\\Standard\\sleep_impl",
				},
				{
					Name:           "strptime",
					Implementation: "PHP2Go\\Extension\\Standard\\strptime_impl",
				},
				{
					Name:           "time_nanosleep",
					Implementation: "PHP2Go\\Extension\\Standard\\time_nanosleep_impl",
				},
				{
					Name:           "time_sleep_until",
					Implementation: "PHP2Go\\Extension\\Standard\\time_sleep_until_impl",
				},
				{
					Name:           "touch",
					Implementation: "PHP2Go\\Extension\\Standard\\touch_impl",
				},
				{
					Name:           "usleep",
					Implementation: "PHP2Go\\Extension\\Standard\\usleep_impl",
				},
				{
					Name:           "uniqid",
					Implementation: "PHP2Go\\Extension\\Standard\\uniqid_impl",
				},
				{
					Name:           "gethostname",
					Implementation: "PHP2Go\\Extension\\Standard\\gethostname_impl",
				},
				{
					Name:           "get_include_path",
					Implementation: "PHP2Go\\Extension\\Standard\\get_include_path_impl",
				},
				{
					Name:           "get_meta_tags",
					Implementation: "PHP2Go\\Extension\\Standard\\get_meta_tags_impl",
				},
				{
					Name:           "hebrev",
					Implementation: "PHP2Go\\Extension\\Standard\\hebrev_impl",
				},
				{
					Name:           "hexdec",
					Implementation: "PHP2Go\\Extension\\Standard\\hexdec_impl",
				},
				{
					Name:           "http_build_query",
					Implementation: "PHP2Go\\Extension\\Standard\\http_build_query_impl",
				},
				{
					Name:           "http_clear_last_response_headers",
					Implementation: "PHP2Go\\Extension\\Standard\\http_clear_last_response_headers_impl",
				},
				{
					Name:           "http_get_last_response_headers",
					Implementation: "PHP2Go\\Extension\\Standard\\http_get_last_response_headers_impl",
				},
				{
					Name:           "request_parse_body",
					Implementation: "PHP2Go\\Extension\\Standard\\request_parse_body_impl",
				},
				{
					Name:           "ignore_user_abort",
					Implementation: "PHP2Go\\Extension\\Standard\\ignore_user_abort_impl",
				},
				{
					Name:           "image_type_to_extension",
					Implementation: "PHP2Go\\Extension\\Standard\\image_type_to_extension_impl",
				},
				{
					Name:           "image_type_to_mime_type",
					Implementation: "PHP2Go\\Extension\\Standard\\image_type_to_mime_type_impl",
				},
				{
					Name:           "inet_ntop",
					Implementation: "PHP2Go\\Extension\\Standard\\inet_ntop_impl",
				},
				{
					Name:           "inet_pton",
					Implementation: "PHP2Go\\Extension\\Standard\\inet_pton_impl",
				},
				{
					Name:           "ini_alter",
					Implementation: "PHP2Go\\Extension\\Standard\\ini_alter_impl",
				},
				{
					Name:           "ini_parse_quantity",
					Implementation: "PHP2Go\\Extension\\Standard\\ini_parse_quantity_impl",
				},
				{
					Name:           "ini_restore",
					Implementation: "PHP2Go\\Extension\\Standard\\ini_restore_impl",
				},
				{
					Name:           "ip2long",
					Implementation: "PHP2Go\\Extension\\Standard\\ip2long_impl",
				},
				{
					Name:           "iptcembed",
					Implementation: "PHP2Go\\Extension\\Standard\\iptcembed_impl",
				},
				{
					Name:           "iptcparse",
					Implementation: "PHP2Go\\Extension\\Standard\\iptcparse_impl",
				},
				{
					Name:           "is_countable",
					Implementation: "PHP2Go\\Extension\\Standard\\is_countable_impl",
				},
				{
					Name:           "is_dir",
					Implementation: "PHP2Go\\Extension\\Standard\\is_dir_impl",
				},
				{
					Name:           "is_executable",
					Implementation: "PHP2Go\\Extension\\Standard\\is_executable_impl",
				},
				{
					Name:           "is_file",
					Implementation: "PHP2Go\\Extension\\Standard\\is_file_impl",
				},
				{
					Name:           "is_iterable",
					Implementation: "PHP2Go\\Extension\\Standard\\is_iterable_impl",
				},
				{
					Name:           "is_link",
					Implementation: "PHP2Go\\Extension\\Standard\\is_link_impl",
				},
				{
					Name:           "is_readable",
					Implementation: "PHP2Go\\Extension\\Standard\\is_readable_impl",
				},
				{
					Name:           "is_uploaded_file",
					Implementation: "PHP2Go\\Extension\\Standard\\is_uploaded_file_impl",
				},
				{
					Name:           "is_writable",
					Implementation: "PHP2Go\\Extension\\Standard\\is_writable_impl",
				},
				{
					Name:           "is_writeable",
					Implementation: "PHP2Go\\Extension\\Standard\\is_writeable_impl",
				},
				{
					Name:           "join",
					Implementation: "PHP2Go\\Extension\\Standard\\join_impl",
				},
				{
					Name:           "key_exists",
					Implementation: "PHP2Go\\Extension\\Standard\\key_exists_impl",
				},
				{
					Name:           "link",
					Implementation: "PHP2Go\\Extension\\Standard\\link_impl",
				},
				{
					Name:           "long2ip",
					Implementation: "PHP2Go\\Extension\\Standard\\long2ip_impl",
				},
				{
					Name:           "md5_file",
					Implementation: "PHP2Go\\Extension\\Standard\\md5_file_impl",
				},
				{
					Name:           "memory_reset_peak_usage",
					Implementation: "PHP2Go\\Extension\\Standard\\memory_reset_peak_usage_impl",
				},
				{
					Name:           "metaphone",
					Implementation: "PHP2Go\\Extension\\Standard\\metaphone_impl",
				},
				{
					Name:           "move_uploaded_file",
					Implementation: "PHP2Go\\Extension\\Standard\\move_uploaded_file_impl",
				},
				{
					Name:           "mkdir",
					Implementation: "PHP2Go\\Extension\\Standard\\mkdir_impl",
				},
				{
					Name:           "natcasesort",
					Implementation: "PHP2Go\\Extension\\Standard\\natcasesort_impl",
				},
				{
					Name:           "natsort",
					Implementation: "PHP2Go\\Extension\\Standard\\natsort_impl",
				},
				{
					Name:           "ob_get_status",
					Implementation: "PHP2Go\\Extension\\Standard\\ob_get_status_impl",
				},
				{
					Name:           "ob_implicit_flush",
					Implementation: "PHP2Go\\Extension\\Standard\\ob_implicit_flush_impl",
				},
				{
					Name:           "ob_list_handlers",
					Implementation: "PHP2Go\\Extension\\Standard\\ob_list_handlers_impl",
				},
				{
					Name:           "opendir",
					Implementation: "PHP2Go\\Extension\\Standard\\opendir_impl",
				},
				{
					Name:           "output_reset_rewrite_vars",
					Implementation: "PHP2Go\\Extension\\Standard\\output_reset_rewrite_vars_impl",
				},
				{
					Name:           "output_add_rewrite_var",
					Implementation: "PHP2Go\\Extension\\Standard\\output_add_rewrite_var_impl",
				},
				{
					Name:           "octdec",
					Implementation: "PHP2Go\\Extension\\Standard\\octdec_impl",
				},
				{
					Name:           "pack",
					Implementation: "PHP2Go\\Extension\\Standard\\pack_impl",
				},
				{
					Name:           "parse_ini_file",
					Implementation: "PHP2Go\\Extension\\Standard\\parse_ini_file_impl",
				},
				{
					Name:           "parse_ini_string",
					Implementation: "PHP2Go\\Extension\\Standard\\parse_ini_string_impl",
				},
				{
					Name:           "parse_str",
					Implementation: "PHP2Go\\Extension\\Standard\\parse_str_impl",
				},
				{
					Name:           "parse_url",
					Implementation: "PHP2Go\\Extension\\Standard\\parse_url_impl",
				},
				{
					Name:           "password_algos",
					Implementation: "PHP2Go\\Extension\\Standard\\password_algos_impl",
				},
				{
					Name:           "password_get_info",
					Implementation: "PHP2Go\\Extension\\Standard\\password_get_info_impl",
				},
				{
					Name:           "password_hash",
					Implementation: "PHP2Go\\Extension\\Standard\\password_hash_impl",
				},
				{
					Name:           "password_needs_rehash",
					Implementation: "PHP2Go\\Extension\\Standard\\password_needs_rehash_impl",
				},
				{
					Name:           "password_verify",
					Implementation: "PHP2Go\\Extension\\Standard\\password_verify_impl",
				},
				{
					Name:           "php_ini_loaded_file",
					Implementation: "PHP2Go\\Extension\\Standard\\php_ini_loaded_file_impl",
				},
				{
					Name:           "php_ini_scanned_files",
					Implementation: "PHP2Go\\Extension\\Standard\\php_ini_scanned_files_impl",
				},
				{
					Name:           "phpcredits",
					Implementation: "PHP2Go\\Extension\\Standard\\phpcredits_impl",
				},
				{
					Name:           "phpinfo",
					Implementation: "PHP2Go\\Extension\\Standard\\phpinfo_impl",
				},
				{
					Name:           "phpversion",
					Implementation: "PHP2Go\\Extension\\Standard\\phpversion_impl",
				},
				{
					Name:           "quoted_printable_decode",
					Implementation: "PHP2Go\\Extension\\Standard\\quoted_printable_decode_impl",
				},
				{
					Name:           "quoted_printable_encode",
					Implementation: "PHP2Go\\Extension\\Standard\\quoted_printable_encode_impl",
				},
				{
					Name:           "rawurldecode",
					Implementation: "PHP2Go\\Extension\\Standard\\rawurldecode_impl",
				},
				{
					Name:           "rawurlencode",
					Implementation: "PHP2Go\\Extension\\Standard\\rawurlencode_impl",
				},
				{
					Name:           "readfile",
					Implementation: "PHP2Go\\Extension\\Standard\\readfile_impl",
				},
				{
					Name:           "readdir",
					Implementation: "PHP2Go\\Extension\\Standard\\readdir_impl",
				},
				{
					Name:           "rename",
					Implementation: "PHP2Go\\Extension\\Standard\\rename_impl",
				},
				{
					Name:           "rewinddir",
					Implementation: "PHP2Go\\Extension\\Standard\\rewinddir_impl",
				},
				{
					Name:           "rmdir",
					Implementation: "PHP2Go\\Extension\\Standard\\rmdir_impl",
				},
				{
					Name:           "scandir",
					Implementation: "PHP2Go\\Extension\\Standard\\scandir_impl",
				},
				{
					Name:           "set_include_path",
					Implementation: "PHP2Go\\Extension\\Standard\\set_include_path_impl",
				},
				{
					Name:           "sha1_file",
					Implementation: "PHP2Go\\Extension\\Standard\\sha1_file_impl",
				},
				{
					Name:           "soundex",
					Implementation: "PHP2Go\\Extension\\Standard\\soundex_impl",
				},
				{
					Name:           "sscanf",
					Implementation: "PHP2Go\\Extension\\Standard\\sscanf_impl",
				},
				{
					Name:           "str_decrement",
					Implementation: "PHP2Go\\Extension\\Standard\\str_decrement_impl",
				},
				{
					Name:           "str_increment",
					Implementation: "PHP2Go\\Extension\\Standard\\str_increment_impl",
				},
				{
					Name:           "str_ireplace",
					Implementation: "PHP2Go\\Extension\\Standard\\str_ireplace_impl",
				},
				{
					Name:           "str_rot13",
					Implementation: "PHP2Go\\Extension\\Standard\\str_rot13_impl",
				},
				{
					Name:           "strcspn",
					Implementation: "PHP2Go\\Extension\\Standard\\strcspn_impl",
				},
				{
					Name:           "strchr",
					Implementation: "PHP2Go\\Extension\\Standard\\strchr_impl",
				},
				{
					Name:           "stripcslashes",
					Implementation: "PHP2Go\\Extension\\Standard\\stripcslashes_impl",
				},
				{
					Name:           "strspn",
					Implementation: "PHP2Go\\Extension\\Standard\\strspn_impl",
				},
				{
					Name:           "strtok",
					Implementation: "PHP2Go\\Extension\\Standard\\strtok_impl",
				},
				{
					Name:           "symlink",
					Implementation: "PHP2Go\\Extension\\Standard\\symlink_impl",
				},
				{
					Name:           "sys_get_temp_dir",
					Implementation: "PHP2Go\\Extension\\Standard\\sys_get_temp_dir_impl",
				},
				{
					Name:           "tempnam",
					Implementation: "PHP2Go\\Extension\\Standard\\tempnam_impl",
				},
				{
					Name:           "tmpfile",
					Implementation: "PHP2Go\\Extension\\Standard\\tmpfile_impl",
				},
				{
					Name:           "unpack",
					Implementation: "PHP2Go\\Extension\\Standard\\unpack_impl",
				},
				{
					Name:           "umask",
					Implementation: "PHP2Go\\Extension\\Standard\\umask_impl",
				},
				{
					Name:           "urldecode",
					Implementation: "PHP2Go\\Extension\\Standard\\urldecode_impl",
				},
				{
					Name:           "urlencode",
					Implementation: "PHP2Go\\Extension\\Standard\\urlencode_impl",
				},
				{
					Name:           "utf8_decode",
					Implementation: "PHP2Go\\Extension\\Standard\\utf8_decode_impl",
				},
				{
					Name:           "utf8_encode",
					Implementation: "PHP2Go\\Extension\\Standard\\utf8_encode_impl",
				},
				{
					Name:           "version_compare",
					Implementation: "PHP2Go\\Extension\\Standard\\version_compare_impl",
				},
			},
		},
		{
			VirtualPath: "/app/.php2go/ext/streams.php",
			Source:      append([]byte(nil), streamsSource...),
			Functions: []Function{
				{
					Name:           "fgetc",
					Implementation: "PHP2Go\\Extension\\Streams\\fgetc_impl",
				},
				{
					Name:           "fdatasync",
					Implementation: "PHP2Go\\Extension\\Streams\\fdatasync_impl",
				},
				{
					Name:           "fflush",
					Implementation: "PHP2Go\\Extension\\Streams\\fflush_impl",
				},
				{
					Name:           "flock",
					Implementation: "PHP2Go\\Extension\\Streams\\flock_impl",
				},
				{
					Name:           "fsockopen",
					Implementation: "PHP2Go\\Extension\\Streams\\fsockopen_impl",
				},
				{
					Name:           "fstat",
					Implementation: "PHP2Go\\Extension\\Streams\\fstat_impl",
				},
				{
					Name:           "pfsockopen",
					Implementation: "PHP2Go\\Extension\\Streams\\pfsockopen_impl",
				},
				{
					Name:           "fsync",
					Implementation: "PHP2Go\\Extension\\Streams\\fsync_impl",
				},
				{
					Name:           "ftruncate",
					Implementation: "PHP2Go\\Extension\\Streams\\ftruncate_impl",
				},
				{
					Name:           "fgetcsv",
					Implementation: "PHP2Go\\Extension\\Streams\\fgetcsv_impl",
				},
				{
					Name:           "fprintf",
					Implementation: "PHP2Go\\Extension\\Streams\\fprintf_impl",
				},
				{
					Name:           "fscanf",
					Implementation: "PHP2Go\\Extension\\Streams\\fscanf_impl",
				},
				{
					Name:           "fpassthru",
					Implementation: "PHP2Go\\Extension\\Streams\\fpassthru_impl",
				},
				{
					Name:           "fputcsv",
					Implementation: "PHP2Go\\Extension\\Streams\\fputcsv_impl",
				},
				{
					Name:           "set_file_buffer",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_set_write_buffer_impl",
				},
				{
					Name:           "socket_get_status",
					Implementation: "PHP2Go\\Extension\\Streams\\socket_get_status_impl",
				},
				{
					Name:           "socket_set_blocking",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_set_blocking_impl",
				},
				{
					Name:           "socket_set_timeout",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_set_timeout_impl",
				},
				{
					Name:           "stream_context_create",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_context_create_impl",
				},
				{
					Name:           "stream_context_get_default",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_context_get_default_impl",
				},
				{
					Name:           "stream_context_get_options",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_context_get_options_impl",
				},
				{
					Name:           "stream_context_get_params",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_context_get_params_impl",
				},
				{
					Name:           "stream_context_set_default",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_context_set_default_impl",
				},
				{
					Name:           "stream_context_set_option",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_context_set_option_impl",
				},
				{
					Name:           "stream_context_set_options",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_context_set_options_impl",
				},
				{
					Name:           "stream_context_set_params",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_context_set_params_impl",
				},
				{
					Name:           "stream_get_transports",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_get_transports_impl",
				},
				{
					Name:           "stream_get_wrappers",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_get_wrappers_impl",
				},
				{
					Name:           "stream_is_local",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_is_local_impl",
				},
				{
					Name:           "stream_isatty",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_isatty_impl",
				},
				{
					Name:           "stream_set_blocking",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_set_blocking_impl",
				},
				{
					Name:           "stream_set_chunk_size",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_set_chunk_size_impl",
				},
				{
					Name:           "stream_set_read_buffer",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_set_read_buffer_impl",
				},
				{
					Name:           "stream_set_timeout",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_set_timeout_impl",
				},
				{
					Name:           "stream_set_write_buffer",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_set_write_buffer_impl",
				},
				{
					Name:           "stream_supports_lock",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_supports_lock_impl",
				},
				{
					Name:           "stream_get_contents",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_get_contents_impl",
				},
				{
					Name:           "stream_get_line",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_get_line_impl",
				},
				{
					Name:           "stream_resolve_include_path",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_resolve_include_path_impl",
				},
				{
					Name:           "stream_select",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_select_impl",
				},
				{
					Name:           "stream_socket_accept",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_socket_accept_impl",
				},
				{
					Name:           "stream_socket_client",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_socket_client_impl",
				},
				{
					Name:           "stream_socket_pair",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_socket_pair_impl",
				},
				{
					Name:           "stream_socket_server",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_socket_server_impl",
				},
				{
					Name:           "stream_socket_get_name",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_socket_get_name_impl",
				},
				{
					Name:           "stream_socket_recvfrom",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_socket_recvfrom_impl",
				},
				{
					Name:           "stream_socket_sendto",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_socket_sendto_impl",
				},
				{
					Name:           "stream_socket_shutdown",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_socket_shutdown_impl",
				},
				{
					Name:           "stream_socket_enable_crypto",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_socket_enable_crypto_impl",
				},
				{
					Name:           "str_getcsv",
					Implementation: "PHP2Go\\Extension\\Streams\\str_getcsv_impl",
				},
				{
					Name:           "stream_copy_to_stream",
					Implementation: "PHP2Go\\Extension\\Streams\\stream_copy_to_stream_impl",
				},
				{
					Name:           "vfprintf",
					Implementation: "PHP2Go\\Extension\\Streams\\vfprintf_impl",
				},
			},
		},
	}
}
