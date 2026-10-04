<?php
// A namespaced constant's namespace is case-insensitive and its own name is
// not: the engine folds the namespace part when it registers the constant
// and when it looks one up, and reports the name as the program wrote it.

define('NS1\ns2\const1', 'value1');
define('ns1\NS2\coNSt3', 'value3');
echo ns1\ns2\const1, " ", NS1\NS2\const1, " ", ns1\ns2\coNSt3, "\n";
var_dump(defined('ns1\NS2\const1'), defined('NS1\ns2\CONST1'), constant('Ns1\Ns2\coNSt3'));
var_dump(define('ns1\ns2\const1', 'again'));
try {
    echo NS1\ns2\coNSt1;
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
