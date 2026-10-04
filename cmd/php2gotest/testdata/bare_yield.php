<?php
// `yield;` with no value at all. The opline's op1 is IS_UNUSED, so the lowering
// carries no operand for it rather than a null, and the absence has to be read
// where the terminator renders. It yields NULL under the next automatic key.

function g() {
    $sent = yield;
    echo "sent: ";
    var_dump($sent);
    yield;
    yield 5;
    yield "k" => "v";
}

$it = g();
var_dump($it->key(), $it->current());
$it->send("S");
var_dump($it->key(), $it->current());
$it->next();
var_dump($it->key(), $it->current());
$it->next();
var_dump($it->key(), $it->current());
$it->next();
var_dump($it->valid());
var_dump($it->getReturn());

// A bare yield reached as the argument of a call under construction: the
// suspension happens with the call half-built, which is what makes the frame
// have to survive it.
class Foo {
    public $v;
    public function __construct($v = null) { $this->v = $v; }
}
function h() {
    $x = new Foo(yield);
    var_dump($x->v);
}
$i2 = h();
$i2->rewind();
$i2->send("built");

// get_class() with no argument answers with the class the call was *compiled*
// in, not the instance's: an inherited method still reports the declaring one.
class Base { function who() { var_dump(get_class()); } }
class Derived extends Base {}
(new Base)->who();
(new Derived)->who();
try { var_dump(get_class()); } catch (Error $e) { echo $e->getMessage(), "\n"; }
