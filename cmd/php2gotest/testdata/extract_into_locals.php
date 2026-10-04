<?php
// extract() writes into the calling frame: every flag (overwrite, skip,
// prefix-same, prefix-all, prefix-invalid, if-exists, prefix-if-exists),
// EXTR_REFS binding the locals to the array's elements, names that are not
// valid identifiers skipped, and `$this` refused. Called by a literal name
// through call_user_func it still extracts; called through a variable, by
// array_map or by `$f()`, it is refused before any by-reference complaint.

function flags() {
    $a = 'old';
    $src = ['a' => 'new', 'b' => 'bee', '1x' => 'bad', 'c d' => 'bad'];
    var_dump(extract($src), $a, $b);
    $a = 'old';
    var_dump(extract($src, EXTR_SKIP), $a);
    var_dump(extract($src, EXTR_PREFIX_SAME, 'p'), $a, $p_a);
    var_dump(extract($src, EXTR_PREFIX_ALL, 'q'), $q_a, $q_b);
    var_dump(extract([0 => 'zero', 'ok' => 1], EXTR_PREFIX_INVALID, 'r'), $r_0, $ok);
    unset($b);
    var_dump(extract(['a' => 'ife', 'b' => 'no'], EXTR_IF_EXISTS), $a, isset($b));
    var_dump(extract(['a' => 'pife', 'zz' => 'no'], EXTR_PREFIX_IF_EXISTS, 's'), $s_a, isset($s_zz));
}
flags();

function refs() {
    $arr = ['x' => 1, 'y' => 2];
    extract($arr, EXTR_REFS);
    $x = 10;
    $y++;
    var_dump($arr);
    debug_zval_dump($arr['x']);
    debug_zval_dump($arr);
}
refs();

class C {
    function m() {
        try {
            extract(['this' => 1]);
        } catch (Error $e) {
            echo get_class($e), ': ', $e->getMessage(), "\n";
        }
        var_dump($this instanceof C);
    }
}
(new C)->m();

function literal() {
    call_user_func('extract', ['lit' => 'via call_user_func']);
    var_dump($lit);
}
literal();

function dynamic($fn) {
    $i = 1;
    foreach ([
        fn() => array_map($fn, [['i' => 2]]),
        fn() => $fn(['i' => 3]),
        fn() => call_user_func($fn, ['i' => 4]),
    ] as $call) {
        try {
            $call();
        } catch (Error $e) {
            echo $e->getMessage(), "\n";
        }
    }
    var_dump($i);
}
dynamic('extract');
dynamic('compact');
dynamic('func_get_args');

// The refusal comes after parameter parsing: arity and type first.
function parsed($fn) {
    foreach ([[], ['s'], [[], 1, 'p', 4]] as $args) {
        try {
            $fn(...$args);
        } catch (Error $e) {
            echo get_class($e), ': ', $e->getMessage(), "\n";
        }
    }
}
parsed('extract');
parsed('func_get_arg');
parsed('compact');
parsed('get_defined_vars');
