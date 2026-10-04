<?php
// An eval of a computed string runs the code the build compiled for that
// string: each string the engine evaluated at the site, chosen by content.

// Classes declared per iteration, each its own string at one site.
foreach (['Red', 'Green', 'Blue'] as $n) {
    eval("class $n { const NAME = '$n'; function hi() { return static::NAME; } }");
}
var_dump((new Green)->hi(), class_exists('Blue', false), class_exists('Cyan', false));

// Closures made by different strings at one site stay apart.
function maker(string $type) {
    return eval("return function($type \$arg) { return \$arg; };");
}
$asInt = maker('int');
$asString = maker('string');
var_dump($asInt("42"), $asString(42));
try {
    $asInt("x");
} catch (TypeError $e) {
    echo get_class($e), "\n";
}

// The eval runs over the caller's variables: a function's locals.
function scale($by) {
    $total = 10;
    $expr = "\$total * $by";
    $out = eval("return $expr;");
    eval("\$total = \$total + $by;");
    return [$out, $total];
}
var_dump(scale(2), scale(3));

// And the file's own, at global scope.
$counter = 1;
foreach (['+= 5', '*= 3'] as $op) {
    eval("\$counter $op;");
}
var_dump($counter);

// A string converts as eval converts it.
class Code {
    function __construct(private string $src) {}
    function __toString(): string { return $this->src; }
}
eval(new Code('echo "from __toString\n";'));

// Code that does not compile is thrown where the eval runs.
foreach (['this is not php', 'class M { public private $x; }'] as $bad) {
    try {
        eval($bad);
    } catch (Error $e) {
        echo get_class($e), ": ", $e->getMessage(), " line ", $e->getLine(), "\n";
    }
}

// A diagnostic in the eval'd code names the code.
foreach ([1, 2] as $i) {
    eval("trigger_error('from eval $i');");
}

// An eval'd string that evals a computed string of its own.
$inner = 'return strtoupper("nested");';
var_dump(eval('return eval($inner);'));

// A function declared conditionally, with a static, in eval and outside.
$code = <<<'CODE'
if (true) {
    function tick() {
        static $n = 0;
        return ++$n;
    }
}
CODE;
eval($code);
var_dump(tick(), tick());
if (true) {
    function tock() {
        static $n = 10;
        return ++$n;
    }
}
var_dump(tock(), tock());

// A class declared at run time may override a constructor or a private
// method with any signature.
class Hint1 {}
class Hint2 {}
class Base {
    public function __construct(Hint1 $x) {}
    private function secret(Hint1 $x) {}
}
eval('class Child extends Base { public function __construct(Hint2 $x) {} public function secret(Hint2 $x) {} }');
var_dump(get_class(new Child(new Hint2)));

// A computed path to no file fails as a literal one does.
$missing = 'no-such-' . 'file.inc';
var_dump(@include $missing);
try {
    require $missing;
} catch (Error $e) {
    echo $e->getMessage(), "\n";
    echo $e->getTraceAsString(), "\n";
}
function load($path) {
    require_once $path;
}
try {
    load($missing);
} catch (Error $e) {
    echo $e->getTraceAsString(), "\n";
}
