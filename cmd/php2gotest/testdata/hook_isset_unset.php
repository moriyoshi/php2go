<?php
// `isset` and `empty` on a hooked property **run the get hook** -- the hook's
// own output is what makes that visible -- so a write-only property refuses
// there exactly as a read does rather than answering a plausible `false`.
// `unset` on a hooked property is refused unconditionally: not from outside,
// not from inside the hook, and `__unset` is never consulted.

class Both
{
    private $store = null;

    public $p {
        get { echo "GET\n"; return $this->store; }
        set { echo "SET\n"; $this->store = $value; }
    }

    public function __unset($name)
    {
        echo "__unset must not run\n";
    }
}

class WriteOnly
{
    public $seen;
    public $p {
        set { $this->seen = $value; }
    }
}

class Backed
{
    // The hook reads the property itself, so this one has a real slot. It is
    // still hooked, and unset still refuses.
    public $p = 7 {
        get { return $this->p; }
    }
}

$b = new Both;
var_dump(isset($b->p));
$b->p = 42;
var_dump(isset($b->p));
var_dump(empty($b->p));
try {
    unset($b->p);
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
var_dump($b->p);

$w = new WriteOnly;
$w->p = 1;
var_dump($w->seen);
try {
    var_dump($w->p);
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
try {
    var_dump(isset($w->p));
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}

$k = new Backed;
var_dump($k->p, isset($k->p));
try {
    unset($k->p);
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
var_dump($k->p);
