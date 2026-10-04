<?php
// A static property is **one slot shared down the hierarchy** unless a subclass
// redeclares it; a class constant is resolved against the declaring class; an
// enum case is a singleton object; and `clone` is shallow, so a nested object
// stays shared while an array does not.

echo "--- static properties and inheritance\n";
class A { public static $shared = 1; public static array $list = []; const K = "ak"; }
class B extends A { }
class C extends A { public static $shared = 100; }
A::$shared = 5;
var_dump(A::$shared, B::$shared, C::$shared);
B::$shared = 7;
var_dump(A::$shared, C::$shared);
A::$list[] = "x"; B::$list[] = "y";
var_dump(A::$list);
var_dump(A::K, B::K, C::K);
echo "--- class constants and interfaces\n";
interface I { const IK = "ik"; }
class D implements I { const DK = self::IK . "!"; final const F = 1; }
var_dump(D::IK, D::DK, D::F, I::IK);
echo "--- enums\n";
enum Suit: string {
    case Hearts = 'H';
    case Spades = 'S';
    const Wild = self::Hearts;
    public function color(): string { return match($this) { Suit::Hearts => 'Red', Suit::Spades => 'Black' }; }
    public static function fromChar(string $c): self { return self::from($c); }
}
var_dump(Suit::Hearts, Suit::Hearts->value, Suit::Hearts->name, Suit::Hearts->color());
var_dump(Suit::from('S'), Suit::tryFrom('X'), Suit::cases(), Suit::Wild);
var_dump(Suit::fromChar('H') === Suit::Hearts, Suit::Hearts instanceof Suit);
enum Plain { case One; case Two; }
var_dump(Plain::One, Plain::cases(), Plain::One === Plain::One);
echo "--- clone and readonly\n";
class E { public $a = 1; public array $b = [1]; public ?object $o = null;
  public function __clone(): void { echo "  cloned\n"; } }
$e = new E; $e->o = new stdClass; $e->o->v = 1;
$f = clone $e;
$f->a = 2; $f->b[] = 2; $f->o->v = 9;
var_dump($e->a, $e->b, $e->o->v);
class R { public function __construct(public readonly array $r) {} }
$r1 = new R([1]);
$r2 = clone $r1;
var_dump($r2->r);
try { $r2->r = [2]; } catch (Error $x) { echo "  ", $x->getMessage(), "\n"; }
