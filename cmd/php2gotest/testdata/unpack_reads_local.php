<?php
// An `unpack` send **reads** the variable it unpacks. Both `f(...$args)` and
// `call_user_func_array($cb, $args)` compile to one, and a rewrite that only
// treated `byval` as a read left the argument a bare place nothing read -- so
// the local was promoted away as unused and the call said
// `Undefined variable $args`, in a body whose `var_dump($args)` one line
// earlier printed it perfectly well.
function take($a, $b, $c) { return "$a-$b-$c"; }

$args = [1, 2, 3];
var_dump(take(...$args));
var_dump(call_user_func_array('take', $args));
var_dump($args);                       // still bound afterwards

// Through a method, and through __call re-entering a private method -- the
// shape that first showed this.
class A {
    private function priv($x = 'none') { return "priv:$x"; }
    public function __call($name, array $rest = array()) {
        return call_user_func_array([$this, $name], $rest);
    }
    public function spread(...$parts) { return implode('/', $parts); }
}
$a = new A();
var_dump($a->priv());
var_dump($a->priv('given'));
var_dump($a->spread(...$args));

// A variadic collecting then re-unpacking.
function forward(...$rest) { return take(...$rest); }
var_dump(forward(4, 5, 6));
var_dump(forward(...$args));

// Unpacking an empty array, and one built inline.
$none = [];
var_dump(take(...[7, 8, 9]));
var_dump(count($none));
