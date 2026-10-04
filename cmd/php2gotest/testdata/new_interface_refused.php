<?php
// `new` of an interface is a catchable Error raised by the `new` itself,
// named as the interface was declared -- before anything could use the
// object, so `throw new I()` never reaches the throw.

interface Shape {}

try {
    new shape();
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
try {
    throw new Shape();
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
$name = 'Shape';
try {
    new $name();
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
