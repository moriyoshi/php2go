<?php
// Calling with too few arguments has **two** messages, and which one depends on
// how the argument went missing: a named call that skips a slot leaves a hole
// and reports `f(): Argument #1 ($a) not passed`, while a short positional call
// reports the count and the call site. Only the first ever fired -- the guard
// was `pos < len(args)`, so a call with no arguments at all took neither branch
// and the parameter silently bound null.

function one($a) {}
function opt($a, $b = 1) {}
function variadic($a, ...$rest) {}
class C {
    function m($a, $b) {}
    static function s($a) {}
}

echo "short: ";
try { one(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "optional: ";
try { opt(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "variadic: ";
try { variadic(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "method: ";
try { (new C)->m(1); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "static: ";
try { C::s(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "closure: ";
$f = function ($a) {};
try { $f(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "hole: ";
function two($a, $b) {}
try { two(b: 2); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "enough: ";
one(1); opt(1); variadic(1); (new C)->m(1, 2); C::s(1); $f(1);
echo "ok\n";
