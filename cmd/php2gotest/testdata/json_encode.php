<?php
// json_encode asks the Ctx for everything it needs about an object: whether the
// class implements JsonSerializable, the call that replaces the value when it
// does, and the property table global scope would see. So the compiled program
// answers it the same way the interpreter does.
class P { public $pub = 1; protected $prot = 2; private $priv = 3; }
class Q extends P { private $qpriv = 4; public $qpub = 5; }
class J implements JsonSerializable { public function jsonSerialize(): mixed { return ["j" => 1]; } }
enum Suit: string { case Hearts = 'H'; }
var_dump(json_encode(new Q), json_encode(new J), json_encode(Suit::Hearts));
var_dump(json_encode([1, 2, 3]), json_encode(["a" => 1]), json_encode("h\u{e9}llo"), json_encode("a/b"));
var_dump(json_encode(NAN), json_encode(1.0), json_encode(1.0, JSON_PRESERVE_ZERO_FRACTION));
var_dump(json_encode((object) ["x" => 1]), json_encode(null), json_encode(true));
try { json_encode(NAN, JSON_THROW_ON_ERROR); } catch (JsonException $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// A private name reached through a subclass is not public whichever back-end
// carries it: one mangles the key, the other filters by visibility.
class Shadow { private $hidden = "no"; public $shown = "yes"; }
var_dump(json_encode(new Shadow), json_encode([new Shadow, Suit::Hearts]));
