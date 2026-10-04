<?php
// A union of classes and `string`, given an object none of the classes
// accepts: `string` takes it only through `__toString`, so a plain object is
// refused -- named, for an anonymous class, as `class@anonymous`, the rest of
// its internal name cut at the NUL (union_types/anonymous_class) -- while a
// Stringable one becomes its string.

class Named {}
class Str { function __toString(): string { return "str!"; } }

function takes(Named|string $v) { return is_object($v) ? get_class($v) : $v; }
function gives($v): Named|string { return $v; }

$anon = new class {
    public function take(self|string $a) { return $a; }
    public function give($v): self|string { return $v; }
};

foreach ([new Named, new Str, new stdClass, new Exception("ex"), "plain"] as $v) {
    foreach ([fn() => takes($v), fn() => gives($v), fn() => $anon->take($v), fn() => $anon->give($v)] as $f) {
        try {
            $r = $f();
            echo is_object($r) ? get_class($r) : var_export($r, true), "\n";
        } catch (TypeError $e) {
            echo $e->getMessage(), "\n";
        }
    }
}

class Holder { public Named|string $p = ""; }
$h = new Holder;
foreach ([new Str, new stdClass] as $v) {
    try {
        $h->p = $v;
        var_dump($h->p);
    } catch (TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}
