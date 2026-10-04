<?php
// **Reaching a property as storage initializes a lazy object.** Every form
// below was measured against the engine, and every one initializes. The read
// and write paths already asked; the *reference* paths did not, so `&$g->a`
// handed back the uninitialized slot and the initializer never ran.
//
// `&$g->a` arrives at `refBox` rather than `ref` -- taking a reference needs
// the box -- which is why fixing only the obvious one changed nothing.
class C { public $a = 'x'; public array $arr = []; public function __construct() { $this->a = 'ctor'; $this->arr = [9]; } }
$r = new ReflectionClass(C::class);
function fresh($r) { return $r->newLazyGhost(function ($o) { $o->__construct(); }); }
function show($r, $g, $label) { printf("%-22s %s\n", $label, $r->isUninitializedLazyObject($g) ? "still lazy" : "initialised"); }
$g = fresh($r); $ref = &$g->a;      show($r, $g, 'ref');
$g = fresh($r); $g->a .= 'y';       show($r, $g, 'concat-assign');
$g = fresh($r); $g->arr[] = 1;      show($r, $g, 'append');
$g = fresh($r); $g->arr[0] = 1;     show($r, $g, 'dim-write');
$g = fresh($r); unset($g->a);       show($r, $g, 'unset');
$g = fresh($r); $g->a++;            show($r, $g, 'increment');
$g = fresh($r); $x = $g->a;         show($r, $g, 'read');
$g = fresh($r); $g->a = 'z';        show($r, $g, 'write');
