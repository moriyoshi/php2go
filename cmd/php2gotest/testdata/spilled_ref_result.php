<?php
// A by-reference call result that has to wait for a nullsafe branch before
// it is sent keeps being the reference it is: the by-reference parameter
// binds the global behind it, even while that global is still unset.

function takes_ref(&$foo) { $foo = 'foo'; }
function &returns_ref($ignored) { global $foo; return $foo; }

$null = null;
takes_ref(returns_ref($null?->method()));
var_dump($foo);

$obj = new class { function m() { return 1; } };
takes_ref(returns_ref($obj?->m()));
var_dump($foo);
