<?php
// A conditionally declared function keeps its statics across calls.
if (true) {
    function tock() {
        static $n = 10;
        static $calls;
        $calls = ($calls ?? 0) + 1;
        return [++$n, $calls];
    }
}
var_dump(tock(), tock());

// A class declared at run time may override a constructor or a private
// method with any signature; a public method it may not override so.
class Hint1 {}
class Hint2 {}
class Base {
    public function __construct(Hint1 $x) {}
    private function secret(Hint1 $x) {}
}
if (true) {
    class Child extends Base {
        public function __construct(Hint2 $x) {}
        public function secret(Hint2 $x) {}
    }
}
var_dump(get_class(new Child(new Hint2)));

// A literal eval whose compile throws a CompileError is caught where the
// eval runs; a ParseError likewise.
try {
    eval('class M { public private $x; }');
} catch (CompileError $e) {
    echo get_class($e), ": ", $e->getMessage(), " line ", $e->getLine(), "\n";
}
try {
    eval('this is not php');
} catch (ParseError $e) {
    echo get_class($e), ": ", $e->getMessage(), " line ", $e->getLine(), "\n";
}

// A require of no file: the Error's trace has no frame for the require.
try {
    require 'no-such-file.inc';
} catch (Error $e) {
    echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
}
function loadLiteral() {
    require_once 'no-such-file.inc';
}
try {
    loadLiteral();
} catch (Error $e) {
    echo $e->getTraceAsString(), "\n";
}

// A computed path to no file fails as a literal one does.
$missing = 'no-such-' . 'other.inc';
var_dump(@include $missing);
var_dump(@include_once $missing);
class Path {
    function __construct(private string $p) {}
    function __toString(): string { return $this->p; }
}
var_dump(@include new Path('via-' . 'tostring.inc'));
try {
    require $missing;
} catch (Error $e) {
    echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
}
function load($path) {
    require_once $path;
}
try {
    load($missing);
} catch (Error $e) {
    echo $e->getTraceAsString(), "\n";
}
include $missing;
echo "done\n";
