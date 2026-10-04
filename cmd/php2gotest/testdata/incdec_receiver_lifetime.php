<?php
// `$o->a++` asks whether its receiver is set -- for the undefined-variable
// warning -- and that question is a read, not a use the object escapes
// through: the object still dies at the end of the call, and the next `new`
// reuses its handle (lazy_objects/fetch_op_initializes).

class C { public $a = 1; }

function bump(object $o) {
    $o->a++;
    var_dump($o->a);
}

bump(new C);
var_dump(new C);
$x = new C;
bump($x);
var_dump(new C);
