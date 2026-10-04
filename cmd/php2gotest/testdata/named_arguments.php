<?php
// A named argument binds by the callee's parameter name, not by position. The
// emitted call is a positional slice, so the binding happens at the call site
// -- which is sound because the lowering has already evaluated every argument
// into a value before the call node.

function f($a, $b = "B", $c = "C") { echo "$a|$b|$c\n"; }

f(1);
f(a: 1, b: 2);
f(b: 2, a: 1);
f(1, c: 3);
// A hole: `$b` is skipped and takes its default.
f(a: 1, c: 3);

class K {
    public function m($x, $y = 9) { echo "m $x/$y\n"; }
    public static function s($p, $q = 8) { echo "s $p-$q\n"; }
    public function __construct($one = "1", $two = "2") { echo "ctor $one,$two\n"; }
}

$k = new K(two: "TWO");
$k->m(y: 1, x: 2);
K::s(q: 7, p: 6);

// A name no parameter claims is an Error the program can catch.
class Plain {}
try { new Plain(x: "nope"); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { f(1, zz: 5); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// A name landing on a slot an earlier argument already filled, whether that one
// was positional or named.
function d($a) {}
try { d(a: 1, a: 2); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { d(1, a: 2); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// A by-reference parameter reached by name has no place to bind.
function r($a, &$e) {}
try { r(e: 42); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// A hole over a *required* parameter is that parameter reporting itself
// missing, and it is required by position: `$a` has a default and is still
// required here, because a required parameter follows it.
function g($a, $b, $c, $d) {}
try { g(a: 'a', d: 'd'); } catch (ArgumentCountError $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
