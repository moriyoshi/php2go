<?php
// A value sent to a by-reference parameter through an unqualified call in a
// namespace: the compiler cannot tell which function the name resolves to,
// so it sends SEND_VAL_EX and the engine refuses at run time, naming the
// function that resolved -- the global one when the namespace declares none,
// the namespaced one when it does (bug78154).

namespace Foo {
    function byRef(&$r) { $r++; }
    function strlen($s) { return -1; }

    try {
        var_dump(similar_text('a', 'a', $d = 5));
    } catch (\Error $e) {
        echo $e->getMessage(), "\n";
    }
    try {
        byRef($x = 1);
    } catch (\Error $e) {
        echo $e->getMessage(), "\n";
    }
    // Declared here by value: no refusal.
    var_dump(strlen($s = "abc"));
    // A variable is sent as one, and binds.
    $p = 0;
    similar_text('ab', 'ab', $p);
    var_dump($p);
}

// A prefer-reference parameter takes a value as well (bug69532).
namespace Bar {
    $a = [3, 1, 2];
    $b = ["c", "a", "b"];
    var_dump(array_multisort($a, SORT_ASC, $b, SORT_DESC));
    var_dump($a, $b);
    var_dump(extract($e = ["k" => 1]), $k);
}
