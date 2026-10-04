<?php
// zend.exception_string_param_max_len: how much of a string argument a trace
// and an unhandled match show, 0 to a million, 15 by default.
function main($arg) {
    echo (new Exception())->getTraceAsString(), "\n";
}
var_dump(ini_set('zend.exception_string_param_max_len', '-1'));
var_dump(ini_set('zend.exception_string_param_max_len', '1000001'));
main('123456789012345678901234567890');
var_dump(ini_set('zend.exception_string_param_max_len', '20'));
main('123456789012345678901234567890');
var_dump(ini_get('zend.exception_string_param_max_len'));
try {
    echo match ('abcdefghijklmnopqrstuvwxyz') {};
} catch (\UnhandledMatchError $e) {
    echo $e->getMessage(), "\n";
}
ini_set('zend.exception_string_param_max_len', '0');
main('short');
main('');
