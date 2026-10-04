<?php
// json_decode as ext/json scans and parses it, json_last_error() after it.
function show($label, $v) {
    echo $label, ': ', str_replace("\n", ' ', var_export($v, true)), ' | ', json_last_error(), ' ', json_last_error_msg(), "\n";
}
$texts = [
    '1', '-0', '-0.0', '1e2', '1E+2', '0.1', '123.456e-2', '1.0', '1e400', '-1e400',
    '9223372036854775807', '9223372036854775808', '-9223372036854775809',
    '12345678901234567890', '""', '"\u00e9\ud83d\ude00"', '"\/\b\f\n\r\t\"\\\\"', '"\u0000a"',
    'true', 'false', 'null', ' [1 , 2 ] ', "\r\n[\r\n]", '[]', '[[[1]]]',
    '1.', '.5', '01', '-', '1e', '1e+', '[1,]', '{"a":1,}', '', ' ', "\x00", '"\ud800"',
    '"\udc00x"', '"\ud800\u0041"', "\"a\x01\"", "\"a\x7f\"", "\"\xff\"", "\"\xc3\"", "\xff",
    '[1]x', 'nul', 'TRUE', '"abc', 'NaN', '"\x"', '"\u12"', '"\u12g4"', '{"a" 1}', '{1:1}',
    '[1 2]', 'tru', "1\x0b", "\xef\xbb\xbf1", '[1}', '{"a":1]', '{"a":[}', "[\x01", '[1,2', '{"a"',
];
foreach ($texts as $i => $t) {
    show($i, json_decode($t));
}
foreach (['{"a":1,"a":2,"b":3}', '{"0":1,"1":2,"x":3,"1":4}', '{"123abc":1,"-5":2,"07":3,"9223372036854775808":4}', '{"":1}', '{"\u0000a":1}'] as $i => $t) {
    show("A$i", json_decode($t, true));
}
var_dump(json_decode('-9223372036854775808'));
// A failed decode frees what it built; the next objects reuse the numbers.
// Each result is kept, so no number depends on when a temporary dies.
$o1 = json_decode('{"a":{"b":{}},"c":[{"d":1}],}');
$o2 = json_decode('{"x":{}}');
var_dump($o1, $o2);
$o3 = json_decode('{"a":1,"b":{"c":2},"d":[{"e":3}],"f":{}}');
var_dump($o3);
$o4 = json_decode('{"b":{"c":2},"a":1}');
var_dump($o4);
$o5 = json_decode('{"0":1,"":2,"a b":3}');
var_dump($o5);
show('prop', json_decode('{"\u0000a":1}'));
show('flag', json_decode('{"a":{"b":1}}', null, 512, JSON_OBJECT_AS_ARRAY));
$o6 = json_decode('{"a":{"b":1}}', false, 512, JSON_OBJECT_AS_ARRAY);
var_dump($o6);
show('big', json_decode('[12345678901234567890, -99999999999999999999, 1.5e30, 7]', true, 512, JSON_BIGINT_AS_STRING));
show('d1', json_decode('[[1]]', false, 1));
show('d2', json_decode('[[1]]', false, 2));
show('d3', json_decode('[[1]]', true, 3));
show('d4', json_decode('1', false, 1));
show('d5', json_decode('[[1]', false, 2));
show('ign', json_decode("\"a\xffb\"", false, 512, JSON_INVALID_UTF8_IGNORE));
show('sub', json_decode("\"a\xffb\"", false, 512, JSON_INVALID_UTF8_SUBSTITUTE));
show('both', json_decode("\"a\xffb\"", false, 512, JSON_INVALID_UTF8_IGNORE | JSON_INVALID_UTF8_SUBSTITUTE));
show('conv', [json_decode(1.5), json_decode(true)]);
json_decode('[');
try {
    json_decode('[', false, 512, JSON_THROW_ON_ERROR);
} catch (JsonException $e) {
    echo get_class($e), ' ', $e->getMessage(), ' ', $e->getCode(), "\n";
}
show('kept', null);
json_decode('1');
try {
    json_decode('[', false, 512, JSON_THROW_ON_ERROR);
} catch (JsonException $e) {
}
show('kept none', null);
try {
    json_decode("\"\xff\"", false, 512, JSON_THROW_ON_ERROR);
} catch (JsonException $e) {
    echo $e->getMessage(), ' ', $e->getCode(), "\n";
}
try {
    json_decode('[[1]]', false, 1, JSON_THROW_ON_ERROR);
} catch (JsonException $e) {
    echo $e->getMessage(), ' ', $e->getCode(), "\n";
}
foreach ([0, -1, PHP_INT_MAX] as $depth) {
    try {
        json_decode('1', false, $depth);
    } catch (ValueError $e) {
        echo $e->getMessage(), "\n";
    }
}
json_decode('[');
json_encode(1);
show('reset', null);
json_decode('[');
json_last_error_msg();
show('msg keeps', null);
enum P { case A; }
var_dump(json_encode(P::A));
show('enc enum', null);
var_dump(json_encode(INF));
show('enc inf', null);
try {
    json_encode(NAN, JSON_THROW_ON_ERROR);
} catch (JsonException $e) {
    echo $e->getMessage(), ' ', $e->getCode(), "\n";
}
show('enc throw keeps', null);
$f = 'json_decode';
var_dump($f('[1]'));
