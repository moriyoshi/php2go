<?php
// A keyed foreach whose key also names a dynamic property in the body binds
// the key: the key travels through a compiler temporary, and the variable
// that receives it is kept (it is a property name) while the temporary is
// promoted -- the store into the kept variable has to take the promoted
// value, not the temporary's empty slot.

class P { public $a = 1; public $b = 2; }

function double($o) {
    foreach ($o as $k => $v) {
        echo "$k => $v\n";
        $o->{$k} = $v * 10;
    }
    return $o;
}
var_dump(double(new P));

function viaVar($o) {
    foreach ($o as $name => $value) {
        $o->$name = strtoupper((string) $value);
    }
    return get_object_vars($o);
}
var_dump(viaVar((object) ['x' => 'a', 'y' => 'b']));

function arrays($arr) {
    foreach ($arr as $k => $v) {
        $arr[$k] = $v + 1;
    }
    return $arr;
}
var_dump(arrays([1, 2]));
