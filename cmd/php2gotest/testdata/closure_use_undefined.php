<?php
// `use ($x)` on an undefined variable warns when the closure is created and
// captures null, so reading it in the body is silent (closure_027). By
// reference it is created as null, without a warning.

function make() {
    echo "creating\n";
    $f = function () use ($missing) {
        echo "body\n";
        var_dump($missing);
    };
    echo "created\n";
    return $f;
}
$f = make();
$f();

echo "file scope\n";
$g = function () use ($alsoMissing) {
    var_dump($alsoMissing);
};
echo "calling\n";
$g();

$h = function () use (&$byRef) {
    var_dump($byRef);
};
$h();
var_dump($byRef);

// An arrow function's capture is implicit: an undefined variable binds
// nothing and says nothing at creation; reading it in the body warns there.
function arrow() {
    echo "arrow created\n";
    $f = fn () => $notThere;
    echo "arrow calling\n";
    var_dump($f());
    $nested = fn ($list) => array_map(fn ($a) => $a * 2, $list);
    var_dump($nested([1, 2]));
}
arrow();
