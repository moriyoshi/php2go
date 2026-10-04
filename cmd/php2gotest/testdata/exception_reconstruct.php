<?php
// Calling `__construct` again on a live exception runs the engine's
// constructor over it: it writes only what it is given -- message, code,
// previous -- and keeps the file, line and trace of the `new`. The writes go
// through the object's handlers, so a set hook on `$message` or `$code` runs,
// and one that throws stops the constructor there (exception_028).

class E extends Exception {}
class Er extends Error {}
class Ee extends ErrorException {}

$e = new E("a", 1, new Exception("first"));
$line = $e->getLine();
$e->__construct("b");
var_dump($e->getMessage(), $e->getCode(), $e->getLine() === $line, $e->getPrevious()->getMessage());
$e->__construct("c", 5, null);
var_dump($e->getMessage(), $e->getCode(), $e->getPrevious()->getMessage());
$e->__construct("d", 6, new LogicException("second"));
var_dump(get_class($e->getPrevious()));
count($e->getTrace()) === 0 and print("trace kept\n");

$x = new Exception("m", 3);
$x->__construct();
var_dump($x->getMessage(), $x->getCode());

$r = new Er("err");
$r->__construct("err2", 9);
var_dump($r->getMessage(), $r->getCode());

$ee = new Ee("x", 1, E_NOTICE, "f1", 7);
$ee->__construct("y");
var_dump($ee->getMessage(), $ee->getSeverity(), $ee->getFile(), $ee->getLine());
$ee->__construct("z", 2, E_WARNING, "f2", 9);
var_dump($ee->getSeverity(), $ee->getFile(), $ee->getLine());

try {
    $e->__construct(new stdClass);
} catch (TypeError $t) {
    echo $t->getMessage(), "\n";
}
var_dump($e->getMessage());

class Hooked extends Exception {
    public int $writes = 0;
    protected $message {
        set($value) {
            $this->writes++;
            if ($value === "boom") {
                throw new RuntimeException("refused");
            }
            $this->message = strtoupper($value);
        }
    }
}
$h = new Hooked("one", 1);
var_dump($h->getMessage(), $h->writes);
$h->__construct("two", 2);
var_dump($h->getMessage(), $h->getCode(), $h->writes);
try {
    $h->__construct("boom", 3);
} catch (RuntimeException $t) {
    echo $t->getMessage(), "\n";
}
var_dump($h->getMessage(), $h->getCode(), $h->writes);

// A subclass redeclaring the engine's protected properties shares their
// slots: one `message`, keeping its declared default when `new` passes none.
class Plain extends Exception {
    protected $message = "default";
    protected $code = 7;
}
var_dump((new Plain)->getMessage(), (new Plain)->getCode());
$p = new Plain("given", 8);
var_dump($p->getMessage(), $p->getCode(), count((array) $p));
$p->__construct("again");
var_dump($p->getMessage(), $p->getCode());
print_r($p);

// The engine's properties come first, in the engine's order, then the
// subclass's own -- including after ErrorException's severity.
class MyEx extends Exception { public $extra = 1; }
print_r(new MyEx("m"));
class Re extends Exception { protected $code = 7; public $after = 2; }
print_r(new Re("m"));
class EeExtra extends ErrorException { public $x = 1; }
print_r(new EeExtra("e", 1, E_WARNING));
