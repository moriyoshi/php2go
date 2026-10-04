<?php
// The container fetch of a nested unset creates an absent dynamic property
// as NULL -- with the dynamic-property deprecation where the class calls for
// it -- and brings back an unset untyped declared one as NULL, while a
// typed uninitialized one stays uninitialized and a plain unset of an absent
// property creates nothing (bug78644).

$a = new stdClass; unset($a->b->c); unset($a->b->c["d"]); var_dump($a);
class P {} $p = new P; unset($p->x->y); var_dump($p);
class Q { public $d; } $q = new Q; unset($q->d->y); var_dump($q);
#[AllowDynamicProperties] class R {} $r = new R; unset($r->x["k"]); var_dump($r);
$o = new stdClass; unset($o->b); var_dump($o);
class T { public int $d; } $t = new T; unset($t->d->y); var_dump($t);
class U { public $e; } $u = new U; unset($u->e); unset($u->e->y); var_dump($u);
