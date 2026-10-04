<?php
// **stdClass contributes nothing to inherit** -- no methods, no properties -- so
// a class chain reaching it is complete. Everything a subclass needs from the
// name is keyed off the recorded parent, including the dynamic-property
// exemption the engine grants stdClass by name.
class Sub extends stdClass { public $declared = 1; }
$s = new Sub;
$s->dynamic = 2;
var_dump($s, get_object_vars($s), get_parent_class($s), $s instanceof stdClass, get_class($s));
var_dump((array) $s, json_encode($s));
foreach ($s as $k => $v) { echo "$k=$v\n"; }
var_dump(new Sub == new Sub);
class Deeper extends Sub {}
var_dump(get_parent_class(new Deeper), (new Deeper) instanceof stdClass);
