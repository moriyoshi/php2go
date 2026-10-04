<?php
// **Which operations initialise a lazy object**, enumerated against the engine
// rather than reasoned about one at a time. The split is not obvious and not
// guessable: reading properties *for their values* initialises, while the
// **dumps** deliberately do not.
//
//   initialise:  read, write, clone, serialize, json_encode, get_object_vars,
//                foreach, isset, unset, ==
//   do not:      var_dump, print_r, (array), count
//
// php2go did none of them. The ones that must stay lazy are pinned here as
// firmly as the ones that must not: a fix aimed at the first group is one edit
// away from breaking the second, and this file is what would catch that.
//
// `serialize` and `==` are **not** here yet -- they still fail to initialise,
// and are filed rather than asserted, because a fixture that always fails stops
// being a signal. Both live in `runtime/php` rather than in emitted code, which
// is the only reason they lag behind the rest.
class C { public int $a = 0; public function __construct() { $this->a = 1; } }
$r = new ReflectionClass(C::class);
function probe($label, $fn) {
  global $r;
  $g = $r->newLazyGhost(function ($o) { echo "[init] "; $o->__construct(); });
  echo str_pad($label, 20);
  try { $fn($g); } catch (Throwable $e) { echo "(", get_class($e), ") "; }
  echo $r->isUninitializedLazyObject($g) ? "still lazy\n" : "initialised\n";
}
probe("json_encode",    function ($g) { json_encode($g); });
probe("get_object_vars",function ($g) { get_object_vars($g); });
probe("foreach",        function ($g) { foreach ($g as $k => $v) {} });
probe("isset",          function ($g) { isset($g->a); });
probe("unset",          function ($g) { unset($g->a); });
probe("print_r",        function ($g) { ob_start(); print_r($g); ob_end_clean(); });
probe("var_dump",       function ($g) { ob_start(); var_dump($g); ob_end_clean(); });
probe("(array) cast",   function ($g) { (array)$g; });

// **A magic accessor answers without initialising.** `unset($g->undeclared)` on
// a class with `__unset` calls the handler and leaves the object lazy, where
// `unset($g->declared)` initialises. Same for `__isset`.
//
// runtime/php had asked this all along through a hook nothing installed, so the
// guard was dead in compiled output -- invisible until something started
// resolving on unset at all, and then it looked like the resolve was wrong.
class M {
    public int $declared = 1;
    public function __unset($n) { echo "__unset($n)\n"; }
    public function __isset($n) { echo "__isset($n)\n"; return false; }
}
$mr = new ReflectionClass(M::class);

$m1 = $mr->newLazyGhost(function ($o) { echo "[init] "; });
unset($m1->undeclared);
var_dump($mr->isUninitializedLazyObject($m1));

$m2 = $mr->newLazyGhost(function ($o) { echo "[init] "; });
isset($m2->undeclared);
var_dump($mr->isUninitializedLazyObject($m2));

$m3 = $mr->newLazyGhost(function ($o) { echo "[init] "; });
unset($m3->declared);
var_dump($mr->isUninitializedLazyObject($m3));
