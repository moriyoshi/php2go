<?php
// The byte-string library moved from `runtime/interp` into the shared
// `runtime/php`, which is what gives *compiled* code a route to it: the emitter
// refuses a call to any function the runtime does not register, so every one of
// these was `unsupported` before the move. The two ValueError cases are here
// because a moved function has to keep raising what it raised.
echo str_repeat("ab", 3), "\n";
echo strtoupper("hello"), strtolower("WORLD"), "\n";
echo trim("  x  "), "|", ltrim("  x  "), "|", rtrim("  x  "), "|\n";
var_dump(strcmp("a", "b"), strcasecmp("A", "a"));
var_dump(strpos("hello", "l"), strrpos("hello", "l"), strpos("hello", "z"));
var_dump(str_contains("hello", "ell"), str_starts_with("hello", "he"), str_ends_with("hello", "lo"));
echo substr("hello", 1, 3), "|", substr("hello", -2), "\n";
echo str_replace("l", "L", "hello"), "\n";
echo strrev("abc"), "\n";
echo str_pad("5", 3, "0", STR_PAD_LEFT), "\n";
try { str_repeat("a", -1); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }
try { strpos("abc", "a", 99); } catch (ValueError $e) { echo $e->getMessage(), "\n"; }
