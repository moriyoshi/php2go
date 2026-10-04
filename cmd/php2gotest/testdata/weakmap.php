<?php
// WeakMap and WeakReference. Both hold their key or target by pointer and take
// no strong reference.
//
// **What is not modelled here is the weakness itself.** Dropping an entry when
// its key dies needs to know that it died, which is the refcount compiled
// output does not have yet -- so entries persist. That is right for every use
// of a WeakMap except the one that makes it weak, and it is the same lifetime
// gap destructor timing has rather than anything particular to weak maps. This
// fixture therefore keeps every key alive.
$map = new WeakMap();
$first = new stdClass();
$second = new stdClass();

$map[$first] = 1;
$map[$second] = 'two';
var_dump(count($map), $map[$first], $map[$second]);
var_dump(isset($map[$first]), isset($map[$second]));

unset($map[$first]);
var_dump(count($map), isset($map[$first]));

// A WeakMap has no key to invent for an appended value.
try {
    $map[] = 5;
} catch (Throwable $e) {
    var_dump(get_class($e), $e->getMessage());
}

// A key the map does not hold is an Error, not a warning.
try {
    $gone = $map[$first];
} catch (Throwable $e) {
    var_dump(get_class($e));
}

// WeakReference has no usable constructor: the static factory is the only way
// to make one, and the refusal names `create` **without** parentheses.
try {
    new WeakReference();
} catch (Throwable $e) {
    var_dump($e->getMessage());
}
$ref = WeakReference::create($second);
var_dump($ref->get() === $second);
