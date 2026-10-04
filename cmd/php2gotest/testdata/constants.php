<?php
const MINE = 7;
var_dump(define("RUNTIME", 8));
var_dump(define("RUNTIME", 9));
echo MINE, "\n";
echo RUNTIME, "\n";
var_dump(defined("MINE"), defined("NOPE"));
var_dump(constant("MINE"));
try { echo NOPE; } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { constant("NOPE"); } catch (Error $e) { echo $e->getMessage(), "\n"; }
if (false) { define("NEVER", 1); }
try { echo NEVER; } catch (Error $e) { echo $e->getMessage(), "\n"; }
