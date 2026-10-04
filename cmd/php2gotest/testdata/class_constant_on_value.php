<?php
// `$x::class` names an object's class, through a reference too, and refuses
// every other value -- a string included -- with a TypeError naming it.

$obj = new ArrayObject([]);
$ref = &$obj;
var_dump($obj::class, $ref::class);
foreach ([null, true, "stdClass", 1, [], 1.5] as $v) {
    try {
        var_dump($v::class);
    } catch (TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}
