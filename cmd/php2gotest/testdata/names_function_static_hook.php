<?php
// get_defined_functions and PropertyHookType named only in a static method
// or a hook are still known to the program.
function mine() {}
class M { static function list() { return get_defined_functions(); } }
$f = M::list();
var_dump(in_array('mine', $f['user']), in_array('strlen', $f['internal']), count($f['internal']) > 100);

class P {
    public int $x { get => 1; }
    public $y { get { return (new ReflectionProperty('P', 'x'))->getHook(PropertyHookType::Get)->getName(); } }
}
var_dump((new P)->y);
