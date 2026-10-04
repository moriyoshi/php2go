<?php
// A class constant's initializer resolves `self::` and `parent::` against the
// class that **declared** it, not the class it is read through. The engine
// flattens inherited constants into every subclass's table; copying the scope
// along with them is what makes `B::selfN` read `B::N`.
class P { const N = 'P'; }
class A extends P {
  const selfN = self::N;
  const parentN = parent::N;
  const N = 'A';
}
class B extends A { const N = 'B'; }
var_dump(B::selfN, B::parentN, A::selfN, A::parentN);
class A2 { const selfN = self::N; const N = 'A2'; }
class B2 extends A2 { const indSelfN = self::selfN; const N = 'B2'; }
var_dump(B2::indSelfN, B2::selfN);
class S { const X = 'S'; public static function f() { return static::X; } public static function g() { return self::X; } }
class T extends S { const X = 'T'; }
var_dump(T::f(), T::g());

// A typed constant's refusal names the declaring class for the same reason.
class TP { const int TN = 1; }
class TC extends TP {}
var_dump(TC::TN, TP::TN);

// An interface constant reached through an implementor.
interface I { const IC = 'I'; }
class Impl implements I { const Mine = self::IC; }
var_dump(Impl::Mine, Impl::IC);

// A **trait's** constant belongs to the composing class: the engine copies it
// in, so `self::PRIVATE` inside a method of the user resolves against the user.
trait TraitConsts {
    public const PUB = 'public';
    private const PRIV = 'private';
    public function show(): void {
        echo self::PUB, " via self\n";
        echo static::PUB, " via static\n";
        echo self::PRIV, " private via self\n";
    }
}
class Uses { use TraitConsts; }
(new Uses)->show();
var_dump(Uses::PUB);
