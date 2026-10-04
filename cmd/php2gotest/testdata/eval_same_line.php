<?php
// Two evals on one line share the name the engine gives eval'd code, but each
// runs its own code; the same eval run twice runs the same code twice.

eval('class A { function hi() { return "A"; } }'); eval('use Foo\A; function f() { return __FUNCTION__; }');
var_dump((new A)->hi(), f());

eval('echo basename(__FILE__), " ", __LINE__, "\n";'); eval('echo "second ", __LINE__, "\n"; trigger_error("from the second");');

for ($i = 0; $i < 2; $i++) { eval('echo "loop ", $i, "\n";'); eval('echo "other ", $i * 10, "\n";'); }

function g() { return eval('return 1;') + eval('return 2;'); }
var_dump(g());

try { eval('return 1;'); eval('throw new Exception("x");'); } catch (Exception $e) { echo basename($e->getFile()), ":", $e->getLine(), "\n"; }

eval('return 0;'); eval('class B { const K = "5 apples" + 1; }');
var_dump(B::K);
eval('class C { const K = "7 pears" * 2; }');
var_dump(C::K);
