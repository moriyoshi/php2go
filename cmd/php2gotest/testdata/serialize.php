<?php
// `serialize` writes the **storage** property table -- `"\0*\0name"` for a
// protected property and `"\0Class\0name"` for a private -- which is what makes
// an unserialized private land back in the right class.
class P { public $pub = 1; protected $prot = 2; private $priv = 3; }
class Q extends P { private $qpriv = 4; public $qpub = 5; }
enum Suit: string { case Hearts = 'H'; }

var_dump(serialize(null), serialize(true), serialize(false), serialize(7), serialize(1.5));
var_dump(serialize("a\"b"), serialize("with\0nul"), serialize([1, 2]), serialize(["k" => "v"]));
echo str_replace("\0", "@", serialize(new P)), "\n";
echo str_replace("\0", "@", serialize(new Q)), "\n";

// An enum case serializes as its **name**, not its state: the case is the
// identity.
var_dump(serialize(Suit::Hearts));

// __serialize replaces the whole state; __sleep only selects which properties
// are written, in the order it returns them. __serialize wins when a class has
// both.
class Sleeper { public $a = 1; public $b = 2; public $c = 3; function __sleep() { return ["c", "a"]; } }
class Described { public $x = 1; function __serialize(): array { return ["k" => "v"]; } }
class Both { public $x = 1; function __sleep() { return ["x"]; } function __serialize(): array { return ["s" => 1]; } }
var_dump(serialize(new Sleeper), serialize(new Described), serialize(new Both));

// A name __sleep returns that the class never declared earns a warning; one
// that is declared but holds nothing is left out silently.
class Odd { public $here = 1; public int $blank; function __sleep() { return ["here", "blank", "nope"]; } }
var_dump(serialize(new Odd));

// The same object again is written as a back-reference, which is also what
// terminates a cycle. A reference repeated is `R:`, an object `r:`.
$o = new P;
var_dump(serialize([$o, $o]));
$r = 1; $pair = [&$r, &$r];
var_dump(serialize($pair));

// The specials are spelled out.
var_dump(serialize(NAN), serialize(INF), serialize(-INF));

// **The engine's own classes that refuse to be written at all**: their state has
// no serialized form. A program that merely *uses* one never declares it, so the
// harvest's per-class flag does not cover it -- runtime/php owns the set.
function gen() { yield 1; }
foreach ([gen(), function () {}] as $v) {
    try { serialize($v); echo "allowed\n"; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
}

// **A library function gets a frame too**, which is what the trace names when
// one of them throws -- and the stack has to be attached while that frame is
// still on it, or the trace opens at the caller instead.
function willRefuse() { yield 1; }
try { serialize(willRefuse()); } catch (Exception $e) {
    echo $e->getMessage(), "\n";
    // The frame's own line number is the call site's, which moves with the
    // fixture; the function it names is the point.
    foreach ($e->getTrace() as $i => $frame) { echo $i, ": ", $frame["function"], "\n"; }
}
