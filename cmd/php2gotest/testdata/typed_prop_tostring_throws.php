<?php
// Assigning an object to a string-typed property runs its __toString; one that
// throws is that exception, not a refused assignment, and the property keeps
// what it had.

class C { public function __toString() { throw new Exception(__METHOD__); } }
class N { public function __toString() { return "ok"; } }
class D { public string $prop = "orig"; public ?string $n = null; }
$d = new D;
try { $d->prop = new C; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
var_dump($d->prop);
$d->n = new N;
var_dump($d->n);
