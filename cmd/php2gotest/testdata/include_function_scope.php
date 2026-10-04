<?php
// An include inside a function runs over the function's variables: it
// reads and writes them, creates new ones the function then sees, and
// unset() there removes the function's own.
function f() {
    $local = "f's local";
    $doomed = "about to go";
    $result = include __DIR__ . '/include_function_scope.inc';
    var_dump($result, $seen, $made, isset($doomed));
}
f();
var_dump(isset($made), isset($seen));

class Host {
    public function run() {
        $local = "method local";
        include __DIR__ . '/include_function_scope.inc';
        return $seen;
    }
}
var_dump((new Host)->run());

$c = function () {
    $local = "closure local";
    include __DIR__ . '/include_function_scope.inc';
    return [$seen, $made];
};
var_dump($c());
