<?php
// A by-reference parameter given something that is not a reference.
//
// Two shapes, both measured against the engine. An argument list built without
// the caller's slots -- `call_user_func`, `call_user_func_array`, a callback an
// internal function invokes -- warns `must be passed by reference, value given`
// and the callee's writes go nowhere. A call result sent straight to a
// by-reference parameter notices `Only variables should be passed by
// reference`, and the callee must not reach whatever the result came from.

function setTo24(&$ref)
{
    $ref = 24;
}

// call_user_func with a literal name is expanded in place (ZEND_SEND_USER);
// a reference local is still sent by value.
$x = 42;
$alias = &$x;
call_user_func('setTo24', $x);
var_dump($x);

// The same through a callable held in a variable, which is the library call.
$name = 'setTo24';
call_user_func($name, $x);
var_dump($x);

// Two by-reference parameters, and call_user_func_array: an element that is a
// reference is passed as one and written back; a plain one warns.
function both(&$a, &$b)
{
    $a += 1;
    $b -= 1;
    return "both";
}
$i = $j = 0;
var_dump(call_user_func('both', $i, $j), $i, $j);
var_dump(call_user_func_array('both', [$i, $j]), $i, $j);
var_dump(call_user_func_array('both', [&$i, $j]), $i, $j);

// A library function's own by-reference parameter.
var_dump(call_user_func('sort', [3, 1, 2]));

// A callback from inside a library function warns at the script's call.
$data = [3, 1, 2];
usort($data, function (&$l, &$r) {
    return $l <=> $r;
});
echo implode(",", $data), "\n";
var_dump(array_map(function (&$v) { return $v * 2; }, [1, 2]));

// Methods, closures and the Closure's own __invoke are named as the engine
// names them.
class Box
{
    function fill(&$into)
    {
        $into = "filled";
    }
    static function fillStatic(&$into)
    {
        $into = "filled";
    }
}
$slot = "empty";
call_user_func([new Box, 'fill'], $slot);
call_user_func(['Box', 'fillStatic'], $slot);
call_user_func('Box::fillStatic', $slot);
$closure = function (&$arg) { $arg = "set"; };
call_user_func($closure, $slot);
call_user_func([$closure, '__invoke'], $slot);
var_dump($slot);

// A handler that throws from the warning stops the call before it runs.
set_error_handler(function ($type, $message) {
    throw new Exception($message);
});
try {
    call_user_func(function (array &$never) { echo "not reached\n"; }, 'x');
} catch (Exception $e) {
    echo "caught: ", $e->getMessage(), "\n";
}
restore_error_handler();

// A call result sent to a by-reference parameter.
class Holder
{
    protected static $list = ["a", "b", "c"];
    static function get()
    {
        return self::$list;
    }
    static function &getRef()
    {
        return self::$list;
    }
}
array_splice(Holder::get(), 1, 1);
print_r(Holder::get());
var_dump(array_pop((Holder::get())));
print_r(Holder::get());
// Returned by reference: a reference, so no notice, and the write lands.
array_pop(Holder::getRef());
print_r(Holder::get());

// A by-reference function declared further down is settled from the unit.
takesRef(strtoupper("later"));
function takesRef(&$v)
{
    var_dump($v);
}
