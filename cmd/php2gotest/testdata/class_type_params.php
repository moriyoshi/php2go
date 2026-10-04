<?php
// A class type declaration checked **nothing** -- `ParseDeclaredType` returns
// false for a class name, and the caller then accepted the value unchanged.
//
// The check is deliberately one-sided: it refuses only what it can prove wrong,
// because a false rejection turns a working program into a TypeError, which is
// worse than the silent acceptance it replaces. Everything below that is *not*
// a plain unrelated class stays accepted.
class A {}
class Sub extends A {}
class Unrelated {}
interface I {}
class Impl implements I {}

function takesA(A $a) { echo "A ok\n"; }
function takesI(I $i) { echo "I ok\n"; }
// `get_class` rather than var_dump: dumping an object prints its handle
// number, and php2go does not yet reuse the handles of freed temporaries --
// an unrelated gap this file should not depend on.
function takesNullableA(?A $a) { var_dump($a ? get_class($a) : "null"); }
function takesUnion(A|Unrelated $x) { echo "union ok\n"; }

takesA(new A());
takesA(new Sub());          // a subclass satisfies it
takesI(new Impl());         // and so does an interface, which a parent-only
                            // walk would have refused
takesNullableA(null);
takesNullableA(new A());
takesUnion(new Unrelated()); // unions are left alone rather than guessed at

try { takesA(new Unrelated()); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }

class Holder { public function me(self $o): self { echo "self ok\n"; return $o; } }
$h = new Holder();
$h->me($h);

// A union or intersection of **class names only** is the same provable question
// asked several times. A union mentioning a scalar is still left alone: weak
// mode coerces across the alternatives in a preference order -- `int|string`
// given 1.5 stores int(1) with the precision deprecation -- and guessing at
// that would corrupt values rather than merely miss a diagnostic.
interface Ia {}
interface Ib {}
class Both implements Ia, Ib {}
class OnlyIa implements Ia {}

function classUnion(A|Unrelated $x) { echo "union ok\n"; }
function intersect(Ia&Ib $x) { echo "intersection ok\n"; }

classUnion(new A());
classUnion(new Unrelated());
intersect(new Both());
try { classUnion(new Both()); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { intersect(new OnlyIa()); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
