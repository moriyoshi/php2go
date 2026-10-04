<?php
// An object passed to a constructor lives as long as whatever the constructor
// put it in. `new Holder(new Part('a'))` makes `Part` the contents of `Holder`,
// so it must survive `make()` returning -- the escape analysis once read every
// mention in a constructor call as the receiver and destroyed `Part` at the
// frame's end while the returned `Holder` still held it.
//
// Both holders stay live to the script's end on purpose. Releasing one earlier
// is a refcount question -- the part must die when its holder does, which is
// D3.3's work and not something frame confinement can answer -- so the case
// grades only the thing the fix changed: nothing dies while it is still held.
class Part
{
    public function __construct(public string $tag)
    {
    }

    public function __destruct()
    {
        echo "d{$this->tag}\n";
    }
}

class Holder
{
    public $part;

    public function __construct($part)
    {
        $this->part = $part;
    }
}

function make()
{
    return new Holder(new Part('a'));
}

function nested()
{
    return new Holder(new Holder(new Part('b')));
}

$h = make();
echo "after make\n";
var_dump($h->part->tag);

$n = nested();
echo "after nested\n";
var_dump($n->part->part->tag);
echo "end\n";
