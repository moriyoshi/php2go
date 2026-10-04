<?php
// A generator aborted by an exception has no return value to give. A
// delegator part-way through `yield from` it gets ClosedGeneratorException
// thrown in at the `yield from`; one that only now starts delegating to it
// gets an Error, also at the `yield from`.

function from() {
    yield 0;
    throw new Exception("from failed");
}
function gen($g, $tag) {
    try {
        $r = yield from $g;
        echo "$tag returned\n";
    } catch (Throwable $e) {
        echo "$tag caught ", get_class($e), ": ", $e->getMessage(), "\n";
    }
}

$shared = from();
$a = gen($shared, "a");
$b = gen($shared, "b");
$a->current();
$b->current();
try {
    $a->next();
} catch (Exception $e) {
    echo "caller caught: ", $e->getMessage(), "\n";
}
$b->next();
var_dump($b->valid());

$c = gen($shared, "c");
$c->current();
var_dump($c->valid());
