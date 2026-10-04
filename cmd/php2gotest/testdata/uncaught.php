<?php
class C { public function go($x) { throw new Exception("from a method"); } }
function inner($a) { (new C())->go("str-that-is-quite-long-indeed"); }
function outer() { inner(42); }
echo "before\n";
outer();
echo "never\n";
