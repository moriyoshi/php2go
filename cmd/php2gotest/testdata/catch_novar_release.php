<?php
// `catch (E)` with no variable releases the caught exception at the catch:
// its destructor runs there, before the handler, and what the destructor
// throws replaces the handler and is thrown from the catch's position
// (try/catch_novar_2). A clause that does not match passes it on untouched.

class E extends Exception {
    public function __construct(string $m, public string $tag) { parent::__construct($m); }
    public function __destruct() { echo "dtor {$this->tag}\n"; }
}

class Boom extends Exception {
    public function __destruct() {
        echo "Boom dtor\n";
        throw new RuntimeException("from dtor");
    }
}

try {
    throw new E("x", "one");
} catch (E) {
    echo "caught one\n";
}
echo "after one\n";

function f() {
    try {
        throw new E("y", "two");
    } catch (Exception) {
        echo "caught two\n";
    }
    echo "after two\n";
}
f();

try {
    try {
        throw new Boom("b");
    } catch (Boom) {
        echo "not reached\n";
    }
    echo "not reached either\n";
} catch (RuntimeException $r) {
    echo "caught ", $r->getMessage(), " at line ", $r->getLine(), "\n";
}
unset($r);

try {
    throw new E("z", "three");
} catch (TypeError) {
    echo "no\n";
} catch (E) {
    echo "caught three\n";
}
var_dump(spl_object_id(new stdClass));
echo "end\n";
