<?php
// A named argument to a **library function** binds against the engine's own
// arginfo, which the Program carries. It used to be refused as `named argument
// to an unknown callee` -- the names were harvested, they just were not
// reachable from the emitter.
var_dump(str_replace(subject: "aaa", search: "a", replace: "b"));
var_dump(in_array(needle: 2, haystack: [1, 2, 3], strict: true));
var_dump(array_slice(array: [1, 2, 3, 4], offset: 1, length: 2));
var_dump(substr(string: "hello", offset: 1, length: 3));
var_dump(str_repeat(times: 3, string: "ab"));
var_dump(substr(offset: 1, string: "hello"));
var_dump(implode(array: ["a", "b"], separator: "-"));
// Mixed: positional first, then named.
var_dump(substr("hello", 1, length: 2));

// A name the function has no parameter for.
try {
    substr(string: "x", nope: 1);
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
// The same parameter twice.
try {
    substr("x", 0, length: 1, offset: 2);
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
// A **required** parameter skipped is an ArgumentCountError naming it.
try {
    array_slice(array: [1, 2, 3], length: 2);
} catch (ArgumentCountError $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
