<?php
namespace test\ns1;

const LOCAL = 1;
const PHP_EOL = "shadowed";

// Unqualified: the namespaced one when it exists, the global one otherwise.
var_dump(LOCAL);
var_dump(PHP_EOL);
var_dump(INI_ALL);
var_dump(PHP_INT_SIZE);
// Fully qualified reaches the global one past the shadow.
var_dump(\PHP_EOL);
var_dump(\test\ns1\LOCAL);
// The refusal names the namespaced form even though two were tried.
try { var_dump(NOPE); } catch (\Error $e) { echo $e->getMessage(), "\n"; }
try { var_dump(\NOPE); } catch (\Error $e) { echo $e->getMessage(), "\n"; }
echo "done\n";
