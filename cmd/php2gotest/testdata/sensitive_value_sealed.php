<?php
// A SensitiveParameterValue refuses dynamic properties outright, and still
// answers getValue().

$v = new SensitiveParameterValue('secret');
try { $v->foo = 1; } catch (Error $e) { echo $e->getMessage(), "\n"; }
var_dump($v->getValue());
