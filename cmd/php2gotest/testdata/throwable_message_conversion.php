<?php
// An exception's message is converted as PHP converts, wherever the engine
// reads it as a string: getMessage() and Exception::__toString. An object
// with no __toString throws from inside the method's frame, an array warns,
// a number becomes its digits. getCode() is untyped and converts nothing.
class Named { function __toString(): string { return "named"; } }
class WithMessage extends Exception {
    function __construct($message, $code = 0) { $this->message = $message; $this->code = $code; }
}
function show(Throwable $e) {
    echo get_class($e), ": ", $e->getMessage(), " @", $e->getLine(), "\n", $e->getTraceAsString(), "\n";
}
function stringify($x) { return (string)$x; }

var_dump((new WithMessage(new Named))->getMessage());
var_dump((new WithMessage(42))->getMessage());
var_dump((new WithMessage(3.5, "c"))->getMessage(), (new WithMessage(3.5, "c"))->getCode());
var_dump((new WithMessage(null))->getMessage());
echo "[", (new WithMessage([1, 2]))->getMessage(), "]\n";

$bad = new WithMessage(new stdClass);
try { $bad->getMessage(); } catch (Error $e) { show($e); }
try { echo $bad; } catch (Error $e) { show($e); }
try { stringify($bad); } catch (Error $e) { show($e); }
// Through the previous-chain too: the outer converts first, then its cause.
$outer = new Exception("outer", 0, new WithMessage(new stdClass));
try { echo $outer; } catch (Error $e) { show($e); }
echo strlen((string)new WithMessage([1])), "\n";
// A property keeps what was stored: the conversion is only for the reading.
// (print_r rather than var_dump: object ids depend on when temporaries die.)
print_r([$bad->getPrevious(), (fn() => $this->message)->call($bad)]);
echo "\n";

// An Error subclass writes its protected properties, and dumps the private
// ones as Error's -- the root, not Exception.
class Failure extends Error {
    function __construct() { $this->message = "set"; $this->file = "f.php"; $this->line = 7; $this->code = 3; }
    function peek() { return [$this->message, $this->file, $this->line, $this->code]; }
}
class Mistyped extends TypeError {}
$f = new Failure;
echo $f->getMessage(), " ", $f->getFile(), ":", $f->getLine(), " ", $f->getCode(), "\n";
var_dump($f->peek());
print_r(new Mistyped("n", 1, new Failure));
echo "\n";
// Casting stores the rendering in the root's private `string`.
$m = new Mistyped("cast");
$s = (string)$m;
var_dump(str_contains(print_r($m, true), "[string:Error:private] => Mistyped: cast in "));
try { throw new Mistyped("chained", 0, new Failure); } catch (Error $e) { show($e->getPrevious()); }
