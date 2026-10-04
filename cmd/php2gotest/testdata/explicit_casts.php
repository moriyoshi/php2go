<?php
// Every scalar cast, over every scalar shape.
foreach ([null, false, true, 5, 5.9, -5.9, "12abc", "abc", [1,2], []] as $v) {
    var_dump((int)$v, (float)$v, (array)$v, (bool)$v, (string)$v);
}
// `(array)` of an object hands out MANGLED keys for the non-public properties,
// which is the only place those names are visible.
class C { public $a = 1; protected $b = 2; private $c = 3; }
var_dump((array)new C);
// `(object)` of an array keeps the keys, of a scalar uses `scalar`, of null is
// empty. Dumped as arrays, because object HANDLE numbering is a separate gap
// and would make this fixture fail for an unrelated reason.
var_dump((array)(object)["x" => 1, 2 => "y"]);
var_dump((array)(object)"s");
var_dump((array)(object)null);
var_dump(get_class((object)"s"));
// An object warns on a numeric cast and still converts, to 1.
$c = new C;
var_dump((int)$c, (float)$c);
// An object cast of an object is the same object.
var_dump((object)$c === $c);
echo "done\n";
