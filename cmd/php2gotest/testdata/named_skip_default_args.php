<?php
// A parameter a named call skipped takes its default, and the argument
// list the function reports carries that default in its place:
// func_get_args(), func_get_arg() and func_num_args() all see it, typed or
// not.

function test($a, $b = 'b', int $c = 3, $d = 'd') {
    var_dump(func_num_args());
    var_dump(func_get_args());
    var_dump(func_get_arg(1), func_get_arg(2));
}

test(d: 'D', a: 'A');
test('A', c: 7);
