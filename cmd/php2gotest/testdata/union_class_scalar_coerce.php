<?php
// A union naming classes still coerces a non-object through its scalar
// members in weak mode: `X|Y|int` given "42" is int(42), for a property, a
// parameter and a return; an object is judged against the class members, and
// a value no scalar member takes is a TypeError naming the whole union.

class Test {
    public X|Y|int $prop;
    public Y|float|null $f;
    public function method(X|Y|int $arg): X|Y|int { return $arg; }
    public function ret($v): Y|string { return $v; }
}
$t = new Test;
$t->prop = "42";
var_dump($t->prop);
var_dump($t->method("7"));
$t->f = "1.5";
var_dump($t->f);
$t->f = null;
var_dump($t->f);
var_dump($t->ret(12));
try { $t->prop = "abc"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $t->method(new stdClass); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
class X {}
$t->prop = new X;
var_dump($t->prop instanceof X);
