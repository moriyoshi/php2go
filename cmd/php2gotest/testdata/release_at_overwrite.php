<?php
// PHP destroys what a local held at the line that overwrites or unsets it --
// inside a function, not only at file scope, and not at the frame's end.
//
// **The obstacle was slot promotion.** It removes the write, and with it the
// only place a release could be emitted; the analysis was correct and had
// nowhere to attach. The renamer is the last pass that still knows -- it walks
// in order and holds the value the local is dropping -- so it now leaves a
// marker where the store was. An `unset` needs no marker (it survives
// promotion, which is what disqualifies its local from being promoted at all)
// but did need the escape walk to stop reading it as a use: unsetting a local
// is the opposite of letting the object escape.
class D {
    public $t;
    function __construct($t) { $this->t = $t; }
    function __destruct() { echo "dtor {$this->t}\n"; }
}

function reassigned() {
    $a = new D('one');
    echo "before\n";
    $a = new D('two');          // dtor one, AFTER two is constructed
    echo "after\n";
}
reassigned();
echo "--\n";

function nulled() {
    $b = new D('null-me');
    echo "before\n";
    $b = null;                  // dtor null-me
    echo "after\n";
}
nulled();
echo "--\n";

function unsetted() {
    $c = new D('unset-me');
    echo "before\n";
    unset($c);                  // dtor unset-me
    echo "after\n";
}
unsetted();
echo "--\n";

// The local is READ between the writes, so the slot survives differently --
// both orders must still come out right.
function read_between() {
    $d = new D('first');
    echo $d->t, "\n";
    $d = new D('second');
    echo $d->t, "\n";
}
read_between();
echo "--\n";

// **The negative case.** A local whose object is also held elsewhere must NOT
// be released when the local is overwritten: the object is still reachable,
// and freeing it destroys something live.
$kept = [];
function escapes(array &$box) {
    $e = new D('escaped');
    $box[] = $e;
    $e = null;                  // NO dtor here: $box still holds it
    echo "after escape\n";
}
escapes($kept);
var_dump($kept[0]->t);
echo "end\n";
