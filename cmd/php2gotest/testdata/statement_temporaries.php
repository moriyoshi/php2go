<?php
// A fresh object nothing keeps dies at the end of its statement in every
// body, not only `{main}`: a bare `new`, an unread call result that is a
// fresh object, an unread clone. One held by a local lives to the frame's
// end, and one whose constructor keeps `$this` outlives the frame.
class D {
    public function __construct(public $n) {}
    public function __destruct() { echo "destroy {$this->n}\n"; }
}
class Keeps {
    public function __construct(public $n) { $GLOBALS['kept'][] = $this; }
    public function __destruct() { echo "kept {$this->n}\n"; }
}
function make($n) { return new D($n); }
class M {
    public function m() {
        new D("m1");
        $a = new D("m2");
        new D("m3");
        echo "m end\n";
    }
    public static function s() {
        new D("s1");
        $a = new D("s2");
        echo "s end\n";
    }
    public function make($n) { return new D($n); }
}
function f() {
    new D("x");
    $y = new D("y");
    new D("z");
    make("call");
    clone new D("cloned");
    (new M)->make("method call");
    new Keeps("k");
    echo "f end\n";
}
function loop() {
    for ($i = 0; $i < 2; $i++) {
        new D("l$i");
        $x = new D("lx$i");
        echo "iter $i\n";
    }
    echo "loop end\n";
}
function gen() {
    new D("g1");
    yield 1;
    $h = new D("g2");
    yield 2;
}
function ids() {
    new D("i1");
    new D("i2");
    $o = new D("i3");
    var_dump(spl_object_id($o));
}
f();
(new M)->m();
M::s();
loop();
ids();
foreach (gen() as $v) {
    echo "got $v\n";
}
$c = function () {
    new D("cl1");
    $z = new D("cl2");
    echo "closure end\n";
};
$c();
echo "end of script\n";
// Last, so the frame's end and the script's end meet: a call result bound
// to a local the body never reads again is still the local's.
function last() {
    $held = make("held call");
    echo "last end\n";
}
last();
