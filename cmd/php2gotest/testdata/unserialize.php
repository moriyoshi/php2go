<?php
// `unserialize` is the mirror of the writer: everything it needs about a class
// it asks for -- whether the name is declared, whether `new` would refuse it,
// an instance laid out **without** its constructor, an enum's case, and whether
// a property is one there is anywhere to put a value.
class P { public $pub = 1; protected $prot = 2; private $priv = 3; }
enum Suit: string { case Hearts = 'H'; case Spades = 'S'; }
class Waker { public $a = 1; public $woke = false; function __wakeup(): void { $this->woke = true; } }
class Taker { public $x = 0; function __unserialize(array $d): void { $this->x = $d["x"] * 10; } }

foreach ([null, true, false, 7, 1.5, "s", "", [1, 2], ["k" => "v"], [[1], [2]]] as $v) {
    var_dump(unserialize(serialize($v)));
}

// An object round-trips its storage table, and __wakeup runs after the
// properties are written while __unserialize replaces that step entirely.
var_dump(get_object_vars(unserialize(serialize(new Waker))));
var_dump(unserialize(serialize(new Taker))->x);
var_dump(str_replace("\0", "@", serialize(unserialize(serialize(new P)))));

// An enum case is its identity: what comes back is the same case object.
var_dump(unserialize(serialize(Suit::Hearts)) === Suit::Hearts);

// A repeated object comes back as one object, and a repeated reference as one
// box -- writing through either end is visible at the other.
$o = new P;
$both = unserialize(serialize([$o, $o]));
var_dump($both[0] === $both[1]);
$r = 1; $pair = [&$r, &$r];
$back = unserialize(serialize($pair));
$back[0] = 9;
var_dump($back[1]);

// Malformed input answers false and says where it gave up.
var_dump(unserialize('i:1;x'));
var_dump(unserialize('a:2:{i:0;i:1;}'));
var_dump(unserialize('junk'));
var_dump(unserialize(''));
var_dump(unserialize('s:5:"ab";'));
var_dump(unserialize('E:9:"Suit:Nope";'));
// (An `O:` naming a class the program never declared builds a
// `__PHP_Incomplete_Class` in the engine, which php2go does not model in either
// back-end -- so it is not asserted here.)
