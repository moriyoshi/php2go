<?php
// define() refuses `__COMPILER_HALT_OFFSET__` by its exact spelling, whether
// or not a file used __halt_compiler(): a warning, false, and the constant
// stays undefined. Any other casing is an ordinary name (halt_compiler3).
var_dump(define('__COMPILER_HALT_OFFSET__', 1));
var_dump(defined('__COMPILER_HALT_OFFSET__'));
var_dump(define('__compiler_halt_offset__', 2));
var_dump(constant('__compiler_halt_offset__'));
var_dump(define('__COMPILER_HALT_OFFSET__X', 3), __COMPILER_HALT_OFFSET__X);
