<?php
// A stack frame names a method as it was **declared** -- the declaring class
// and the declaration's own spelling -- not as the call site wrote it and not
// folded. `(new B)->instmeth()` on `A::InstMeth` renders `A->InstMeth()`, and
// `B::statmeth()` renders `A::StatMeth()`. `methodOwnerOf` has answered the
// declared spelling all along and the static dispatcher used it; the instance
// dispatcher built its frame from the folded name instead.
//
// A name that is **not** a method gets no frame of its own. `A::nope()` on a
// class with `__callStatic` renders `A::__callStatic('nope', Array)` and
// nothing else; without one, the undefined-method Error names no frame either.
// The instance dispatcher already decided this before pushing; the static one
// pushed first and put a spurious `A->nope()` underneath.
//
// A **trait alias** renames the method for the trace too: `use T { m as Ali; }`
// gives one body two names and each frame names the one that was called. The
// function keeps the trait's own name -- the body is shared with every class
// that composes it -- so the alias clause is the only place that spelling
// exists. `zend_find_alias_name` is the engine doing the same lookup.
function show(callable $f) {
    try { $f(); } catch (Throwable $e) { echo $e->getTraceAsString(), "\n--\n"; }
}
class A {
    public function InstMeth() { throw new Exception("i"); }
    public static function StatMeth() { throw new Exception("s"); }
    public function __call($n, $a) { throw new Exception("c"); }
    public static function __callStatic($n, $a) { throw new Exception("cs"); }
}
class B extends A {}
class Plain { public function Only() {} public static function SOnly() {} }
show(function () { (new A)->INSTMETH(); });
show(function () { (new B)->instmeth(); });
show(function () { A::STATMETH(); });
show(function () { B::statmeth(); });
show(function () { (new A)->NoSuchThing(); });
show(function () { A::NoSuchStatic(); });
show(function () { B::NoSuchStatic(); });
show(function () { Plain::Nope(); });
show(function () { (new Plain)->Nope(); });
show(function () { $m = 'INSTMETH'; (new A)->$m(); });
show(function () { call_user_func([new A, 'INSTMETH']); });
show(function () { call_user_func(['B', 'STATMETH']); });

trait T {
    public function TraitMeth() { throw new Exception("t"); }
    public static function TraitStat() { throw new Exception("ts"); }
}
class Uses {
    use T { TraitMeth as Aliased; TraitStat as AliasedStat; }
}
class SubUses extends Uses {}
show(function () { (new Uses)->Aliased(); });
show(function () { (new Uses)->ALIASED(); });
show(function () { (new Uses)->TraitMeth(); });
show(function () { (new SubUses)->aliased(); });
show(function () { Uses::AliasedStat(); });
show(function () { Uses::TraitStat(); });
show(function () { SubUses::aliasedstat(); });
show(function () { call_user_func([new Uses, 'ALIASED']); });
