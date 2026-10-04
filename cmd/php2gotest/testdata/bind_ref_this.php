<?php
// `$a = &$this` binds `$a` to a fresh slot holding the object -- `$this`
// has none to share -- so writing `$a` does not touch `$this`, and a
// closure capturing `$a` by reference sees what was bound (closure_020,
// bug34358).

class T {
    public $name = 't';
    function run() {
        $a = &$this;
        $f = function () use (&$a) { return $a; };
        var_dump($f() === $this);
        $a = 'replaced';
        var_dump($this->name, $f());
    }
}
(new T)->run();
