<?php
// An exception subclass with a constructor of its own: the engine gives the
// object its message, code, file, line and trace when it is created, so a
// constructor that never calls the parent's still has them, one that writes
// `$this->line` writes the slot the accessors read, and
// `parent::__construct(...)` writes what it is given over them.

class Quiet extends Exception {
    public function __construct() {
        // no parent call
    }
}
$q = new Quiet;
var_dump($q);
var_dump($q->getMessage(), $q->getCode(), $q->getLine(), $q->getFile() === __FILE__);

class Writes extends Exception {
    public function __construct() {
        $this->line = 0;
        $this->code = 5;
    }
}
$w = new Writes;
var_dump($w->getLine(), $w->getCode(), count((array) $w));

class Calls extends Exception {
    public function __construct($m, $extra) {
        $x = $extra;

        parent::__construct($m, 42, new LogicException("prev"));
    }
}
function make() {
    return new Calls("called", 1);
}
$c = make();
var_dump($c->getMessage(), $c->getCode(), $c->getLine(), get_class($c->getPrevious()));
var_dump($c->getTrace()[0]['function']);

class Severe extends ErrorException {
    public function __construct($m) {
        parent::__construct($m, 0, E_WARNING);
    }
}
$s = new Severe("sev");
var_dump($s->getSeverity(), $s->getMessage());

class Twice extends RuntimeException {
    public function __construct() {
        parent::__construct("first");
        parent::__construct("second", 2);
    }
}
$t = new Twice;
var_dump($t->getMessage(), $t->getCode());

class Zero extends exception {
    public function __construct() {
        $this->line = 0;
    }
}
throw new Zero;
