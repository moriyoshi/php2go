<?php
// PHP answers integer overflow with a float, and an `int` declaration refuses
// that -- so `++` at PHP_INT_MAX fails rather than widening, the property keeps
// its value, and the message is its own rather than the generic refused-write
// one. Which declarations refuse a float is left to the coercion: `int`,
// `?int` and `int|bool` all want this wording.
class C {
    public int $n = 0;
    public ?int $q = 0;
    public int|bool $u = 0;
    public float $f = 0.0;
    public $untyped = 0;
}

$c = new C();

$c->n = PHP_INT_MAX;
try { $c->n++; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
var_dump($c->n);

$c->n = PHP_INT_MIN;
try { $c->n--; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
var_dump($c->n);

$c->q = PHP_INT_MIN;
try { $c->q--; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
var_dump($c->q);

$c->u = PHP_INT_MAX;
try { $c->u++; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
var_dump($c->u);

// A float property widens happily, and an untyped one becomes a float.
$c->f = PHP_INT_MAX;
$c->f++;
var_dump($c->f);
$c->untyped = PHP_INT_MAX;
$c->untyped++;
var_dump($c->untyped);

// Nowhere near the end, ++ is just ++.
$c->n = 5;
$c->n++;
$c->n--;
var_dump($c->n);

// Through a **reference** bound to the property, the engine names it
// differently again -- and the store there is a plain `write_var` carrying no
// sign that a step produced the value, so the guard is emitted ahead of it from
// what the emitter knows: it has just rendered the increment.
$d = new C();
$r = &$d->n;
$r = PHP_INT_MAX;
try { $r++; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
var_dump($d->n);

$r = PHP_INT_MIN;
try { $r--; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
var_dump($d->n);

$rq = &$d->q;
$rq = PHP_INT_MIN;
try { $rq--; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

$ru = &$d->u;
$ru = PHP_INT_MAX;
try { $ru++; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// A reference to a float property widens, and a plain variable always did.
$rf = &$d->f;
$rf = PHP_INT_MAX;
$rf++;
var_dump($d->f);
$plain = PHP_INT_MAX;
$plain++;
var_dump($plain);
