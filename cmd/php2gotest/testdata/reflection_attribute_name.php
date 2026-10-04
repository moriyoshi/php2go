<?php
// A ReflectionAttribute carries one property, the attribute's name as
// written, resolved against the namespace.

namespace N;

#[\Attribute] class MA {}
#[MA, \Attribute] class X {}

$rc = new \ReflectionClass(X::class);
$list = $rc->getAttributes();
var_dump($list[0]);
var_dump($list[1]->name, isset($list[0]->name));
print_r($list[0]);
echo "\n";
var_dump((array) $list[1]);
var_dump(get_object_vars($list[0]));
