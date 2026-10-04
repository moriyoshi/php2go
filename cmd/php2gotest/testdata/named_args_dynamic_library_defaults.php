<?php
// A dynamic call to a library function whose named arguments skip an
// optional parameter fills it with the engine's declared default, as a
// written call does; one declared with no default is the engine's own
// "must be passed explicitly" (named_params/call_user_func).
var_dump(call_user_func('array_slice', [1, 2, 3, 4, 'x' => 5], 3, preserve_keys: true));
var_dump(call_user_func('str_pad', 'a', 5, pad_type: STR_PAD_LEFT));
var_dump(call_user_func('str_pad', 'a', 5, pad_type: STR_PAD_BOTH));
var_dump(call_user_func('explode', ',', 'a,b,c', limit: 2));
var_dump(call_user_func('trim', characters: 'x', string: 'xxhixx'));
var_dump(call_user_func('number_format', 1234.567, thousands_separator: ' '));
var_dump(call_user_func('htmlspecialchars', '<a href="x">', double_encode: false));
var_dump(call_user_func('json_encode', ['a' => 1], depth: 5));
var_dump(call_user_func_array('array_slice', [[1, 2, 3], 1, 'preserve_keys' => true]));
try {
    var_dump(call_user_func('array_slice', [1, 2, 3], length: 2));
} catch (ArgumentCountError $e) {
    echo $e->getMessage(), "\n";
}
try {
    var_dump(call_user_func('array_keys', [1, 2, 1], strict: true));
} catch (ArgumentCountError $e) {
    echo $e->getMessage(), "\n";
}
$f = 'array_fill_keys';
var_dump($f(value: 0, keys: ['a', 'b']));

// invokeArgs and newInstanceArgs spread their array as call_user_func_array
// does: a string key is a named argument, not the next position.
function three($a = 'a', $b = 'b', $c = 'c') { echo "a=$a b=$b c=$c\n"; }
class Three {
    function __construct($a = 'a', $b = 'b', $c = 'c') { echo "new a=$a b=$b c=$c\n"; }
    function m($a = 'a', $b = 'b', $c = 'c') { echo "m a=$a b=$b c=$c\n"; }
    static function s($a = 'a', $b = 'b', $c = 'c') { echo "s a=$a b=$b c=$c\n"; }
}
(new ReflectionFunction('three'))->invokeArgs(['A', 'c' => 'C']);
(new ReflectionFunction(fn($x = 1, $y = 2) => print("x=$x y=$y\n")))->invokeArgs(['y' => 9]);
(new ReflectionMethod('Three', 'm'))->invokeArgs(new Three, ['c' => 'C', 'a' => 'A']);
(new ReflectionMethod('Three', 's'))->invokeArgs(null, ['b' => 'B']);
(new ReflectionClass('Three'))->newInstanceArgs(['A', 'c' => 'C']);
try {
    (new ReflectionFunction('three'))->invokeArgs(['c' => 'C', 'A']);
} catch (Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
    (new ReflectionFunction('three'))->invokeArgs(['d' => 'D']);
} catch (Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
function bump(&$n) { $n++; }
$v = 1;
$args = [&$v];
(new ReflectionFunction('bump'))->invokeArgs($args);
var_dump($v);
