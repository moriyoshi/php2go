<?php
// A name the callee does not declare, or one that overwrites an argument,
// is refused at the call: the callee's own frame is not in the trace.
function f($b) { }
class C { function m($b) {} static function s($b) {} }
function report(Throwable $e) { echo $e->getMessage(), " @", $e->getLine(), "\n", $e->getTraceAsString(), "\n"; }
function t(callable $c) { try { $c(a: 1); } catch (Error $e) { report($e); } }
t('f');
t(function ($b) {});
t([new C, 'm']);
t('C::s');
t(fn($b) => 1);
try { call_user_func('f', a: 1); } catch (Error $e) { report($e); }
try { $cl = function ($b) {}; $cl->__invoke(a: 1); } catch (Error $e) { report($e); }
function g($a, $b) {}
try { g(...[1, 2], b: 3); } catch (Error $e) { report($e); }
function h() { try { g(...[1], a: 3); } catch (Error $e) { report($e); } }
h();
$n = 'g';
try { $n(1, 2, c: 4); } catch (Error $e) { report($e); }
