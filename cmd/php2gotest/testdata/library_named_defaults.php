<?php
// A named argument that skips an optional parameter of a library function
// fills it with the engine's declared default: null, a bool, an integer, a
// string, a constant, or integer flags or-ed together -- and one the arginfo
// gives no default at all is the engine's own refusal (named_params/internal,
// missing_param, bug80096).

var_dump(array_slice([1, 2, 3, 4, 5], 1, preserve_keys: true));
var_dump(str_pad("ab", 5, pad_type: STR_PAD_LEFT));
var_dump(str_pad("ab", 5, pad_string: "*"));
var_dump(number_format(1234.567, 2, thousands_separator: " "));
var_dump(htmlentities("<'a'> &amp;", double_encode: false));
var_dump(trim(" xy\n", characters: " "));
var_dump(array_keys([41, 42, "42"], filter_value: 42, strict: true));
try {
    array_keys([1], strict: true);
} catch (ArgumentCountError $e) {
    echo $e->getMessage(), "\n";
}
