<?php
// String conversion, which is not pure: an object re-enters `__toString`, an
// array warns, and an object without `__toString` throws.
//
// Compiled code did not convert objects **at all** — `php.Concat` and the rope
// builder both used the value's plain string form, which has no ctx and so
// cannot call a method. `echo "x$obj"` printed `xy`. The catalogue had said
// Concat `ReEnters` the whole time: a stated effect the implementation cannot
// perform is the same defect as an uncatalogued function, one level in.

class S {
    public function __construct(private string $tag) {}
    public function __toString(): string {
        echo "[to-string {$this->tag}]";
        return "<{$this->tag}>";
    }
}

$a = new S("a");
$b = new S("b");

// Interpolation converts each piece AS IT IS ADDED, so the echoes appear in
// build order, before the finished string.
echo "start {$a} middle {$b} end\n";

// Concatenation converts too, left then right.
echo $a . "-" . $b . "\n";
echo "pre" . $a . "\n";

// (string) and echo are the same conversion.
var_dump((string) $a);
echo $b, "\n";

// An object with no __toString is an Error, and the message names the class.
$plain = new stdClass();
try { echo "x$plain"; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { echo $plain . "y"; } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { var_dump((string) $plain); } catch (Error $e) { echo $e->getMessage(), "\n"; }

// An array converts to the literal "Array", with a warning.
$arr = [1, 2];
echo "arr $arr\n";
echo $arr . "\n";
var_dump((string) $arr);

// A __toString that throws propagates out of the conversion.
class Boom {
    public function __toString(): string { throw new RuntimeException("boom"); }
}
try { echo "z" . new Boom(); } catch (RuntimeException $e) { echo "caught: ", $e->getMessage(), "\n"; }

// Building a long string stays correct however it is built.
function build(array $parts): string {
    $out = "";
    foreach ($parts as $p) {
        $out .= "[$p]";
    }
    return $out;
}
var_dump(build(["a", "b", "c"]));
var_dump(build([]));
