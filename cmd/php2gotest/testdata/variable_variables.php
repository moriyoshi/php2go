<?php
// `$$n` names a variable at **run time**, so the names have to exist then. The
// body keeps a second view of its slots as a name->address map: the map holds
// addresses, so it and the Go variables never disagree, and a name no slot
// answers to is created in the map alone.

$name = "x";
$$name = 42;
var_dump($x, $$name, ${$name});

// Writing through the name and reading through the variable, and back.
$x = 7;
var_dump($$name);
$$name = 9;
var_dump($x);

// A name the compiler never saw as a variable at all.
$other = "brandnew";
$$other = "made";
var_dump(${$other});

// Reading a name nothing answers to is an undefined variable, and it must not
// bring the variable into existence.
$missing = "nope";
var_dump(@$$missing);
var_dump($$missing);
var_dump(@$$missing);

// Inside a function the slots are the function's own, not the file body's.
$x = "file-level";
function f() {
    $a = 1;
    $n = "a";
    var_dump($$n);
    $n = "b";
    $$n = 2;
    var_dump($b, $a);
    // A file-level variable is not in scope here even by name.
    $n = "x";
    var_dump(@$$n);
}
f();
var_dump($x);

// At top level the variables **are** the globals, so a name reaches the same
// slot `global` does.
$shared = "top";
function reader() {
    global $shared;
    return $shared;
}
var_dump(reader());
$n = "shared";
$$n = "changed";
var_dump(reader(), $shared);

// The name is an ordinary expression, evaluated as a string.
$parts = ["sh", "ared"];
var_dump(${$parts[0] . $parts[1]});
