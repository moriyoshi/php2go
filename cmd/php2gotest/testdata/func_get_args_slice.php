<?php
// `array_slice(func_get_args(), N)` outside a namespace is folded by the
// compiler into ZEND_FUNC_GET_ARGS with N as its first operand.
function tail()
{
    return [array_slice(func_get_args(), 1), array_slice(func_get_args(), 5), func_get_args()];
}
var_dump(tail(1, 2, 3), tail());
function named($a, ...$rest)
{
    return array_slice(func_get_args(), 2);
}
var_dump(named(1, 2, 3, 4));
