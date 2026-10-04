<?php
// An object a store refuses dies at the refusal, before the catch runs, and
// hands its number to the next object; a catch that overwrites its variable
// frees the exception the variable held; a method call that throws frees a
// temporary receiver. The negative cases are ones where program code kept the
// object, so the engine keeps it too.
class D { public $n; function __construct($n) { $this->n = $n; } function __destruct() { echo "dtor {$this->n}\n"; } }
class DT { function __destruct() { echo "dtor throws\n"; throw new RuntimeException("from dtor"); } }
class T { public int $i = 0; public ?T $self = null; public static int $s = 0; }
class Ret { public function make(): static { return new self; } public function num(): int { return new stdClass; } }
class Sub extends Ret {}
class Magic { public $kept = []; function __set($n, $v) { $this->kept[] = $v; throw new Exception("no $n"); } }
class Str { public static $held = []; function __toString(): string { self::$held[] = $this; throw new Exception("no string"); } }
class S2 { public string $s = ""; }
class Leaky { public static $all = []; function __construct() { self::$all[] = $this; } }
trait Tr { public static self $selfProp; }
class UsesTr { use Tr; }
function boom($x) { throw new Exception("boom"); }
function localRefused($t) {
    $w = new stdClass;
    try { $t->i = $w; } catch (TypeError $e) { echo "caught in function\n"; }
    var_dump(new stdClass);
}
class Box { public $items = []; function put($x) { $this->items[] = $x; } }
class Hooked { public $kept = []; public $h { set { $this->kept[] = $value; throw new Exception("hook"); } } }

echo "-- catch overwrite\n";
try { throw new Exception("a"); } catch (Exception $e) { echo $e->getMessage(), "\n"; }
try { throw new LogicException("b"); } catch (Exception $e) { echo $e->getMessage(), "\n"; }
var_dump(new stdClass);

echo "-- typed property\n";
$t = new T;
try { $t->i = new stdClass; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump(new stdClass);

echo "-- static property\n";
try { T::$s = new stdClass; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump(new stdClass);

echo "-- return check\n";
try { (new Sub)->make(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump(new stdClass);
$r = new Ret;
try { $r->num(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump(new stdClass);

echo "-- destructor at the refusal\n";
try { $t->i = new D(1); } catch (TypeError $e) { echo "caught\n"; }
echo "after\n";
// $y is an argument below, so a trace may hold it: it is never released.
try { $t->i = new DT; } catch (Exception $y) { echo get_class($y), ": ", $y->getMessage(), " / ", get_class($y->getPrevious()), "\n"; }
var_dump(new stdClass);

echo "-- kept by __set\n";
$m = new Magic;
try { $m->nope = new stdClass; } catch (Exception $e) { echo $e->getMessage(), "\n"; }
var_dump(new stdClass, $m->kept);

echo "-- kept by __toString\n";
$s2 = new S2;
try { $s2->s = new Str; } catch (Exception $e) { echo $e->getMessage(), "\n"; }
var_dump(new stdClass, count(Str::$held));

echo "-- kept by a set hook\n";
$h = new Hooked;
try { $h->h = new stdClass; } catch (Exception $e) { echo $e->getMessage(), "\n"; }
var_dump(new stdClass, $h->kept);

echo "-- trait static property\n";
try { UsesTr::$selfProp = new stdClass; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump(new stdClass);

echo "-- kept by its own constructor\n";
try { $t->i = new Leaky; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump(new stdClass, count(Leaky::$all));

echo "-- also assigned to a variable\n";
try { $t->i = $w = new stdClass; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump(new stdClass, $w);

echo "-- also stored somewhere first\n";
$u = new stdClass;
try { $t->i = $u->v = new stdClass; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump(new stdClass, $u->v);

echo "-- a caught exception in another exception's trace\n";
try { throw new Exception("t1"); } catch (Exception $q) { try { boom($q); } catch (Exception $held) { echo "boom\n"; } }
try { throw new Exception("t2"); } catch (Exception $q) { echo $q->getMessage(), "\n"; }
var_dump(new stdClass, $held->getTrace()[0]['args'][0]->getMessage());

echo "-- a caught exception kept elsewhere\n";
$keep = [];
try { throw new Exception("k1"); } catch (Exception $k) { $keep[] = $k; }
try { throw new Exception("k2"); } catch (Exception $k) { echo $k->getMessage(), "\n"; }
var_dump(new stdClass, $keep[0]->getMessage());

echo "-- a caught exception handed to a method that keeps it\n";
$box = new Box;
try { throw new Exception("p1"); } catch (Exception $p) { $box->put($p); }
try { throw new Exception("p2"); } catch (Exception $p) { echo $p->getMessage(), "\n"; }
var_dump(new stdClass, $box->items[0]->getMessage());

echo "-- stored, not refused\n";
$t->self = new T;
var_dump(new stdClass, $t->self);

// Last, because when a local's object dies is a separate question: the
// refusal must not free it while the local still names it.
echo "-- a local refused: it lives to the end of its frame\n";
localRefused($t);
