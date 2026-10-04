<?php
// A value sent where a library function takes a reference: an assignment
// or other expression is refused at the send -- `could not be passed by
// reference`, naming the library's parameter -- and a call result notices
// and passes a copy. A variable is passed as the reference it is.

try {
    usort($a = ['y', 'x'], fn ($p, $q) => $p <=> $q);
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
try {
    array_push($b = [1], 2);
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
sort(array_values([2, 1]));
$c = [3, 1];
sort($c);
var_dump($c);
