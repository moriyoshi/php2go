<?php
// A static call to a method an engine class does not have is `Call to
// undefined method` for the classes whose methods are all known -- stdClass,
// which has none, and the throwables (indirect_call_array_002, ns_038).

foreach (['stdclass::b', 'Exception::bar', 'RuntimeException::nope', 'ArrayObject::nope'] as $f) {
    try {
        $f();
    } catch (Error $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
try {
    \Exception::bar();
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
