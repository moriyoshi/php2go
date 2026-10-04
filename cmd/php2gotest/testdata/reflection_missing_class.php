<?php
// Reflecting a class that does not exist is a ReflectionException, through
// ReflectionClass, ReflectionObject's sibling spellings, ReflectionMethod
// and ReflectionProperty alike. (An engine class the program does not
// declare exists, and is reported unsupported instead -- not tested here,
// since a fixture must pass.)

foreach ([
    fn() => new ReflectionClass('NoSuchClass'),
    fn() => new ReflectionMethod('NoSuchClass', 'm'),
    fn() => new ReflectionMethod('NoSuchClass::m'),
    fn() => new ReflectionProperty('NoSuchClass', 'p'),
] as $make) {
    try {
        $make();
    } catch (ReflectionException $e) {
        echo $e->getMessage(), "\n";
    }
}
class Real { public $p; function m() {} }
var_dump((new ReflectionClass('real'))->getName());
var_dump((new ReflectionMethod('Real', 'm'))->class);

// A property the program's own class does not have is the same exception,
// through the constructor and through getProperty.
foreach ([
    fn() => new ReflectionProperty('Real', 'nope'),
    fn() => (new ReflectionClass('Real'))->getProperty('nope'),
] as $make) {
    try {
        $make();
    } catch (ReflectionException $e) {
        echo $e->getMessage(), "\n";
    }
}

// A function that does not exist is the same exception. (A library function
// exists, and is reported unsupported rather than tested here.)
try {
    new ReflectionFunction('no_such_function_here');
} catch (ReflectionException $e) {
    echo $e->getMessage(), "\n";
}
function mine() {}
var_dump((new ReflectionFunction('mine'))->getName());
