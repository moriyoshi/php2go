<?php
// A closure captured **by reference** shares the variable's storage, which is
// what makes a recursive closure work: the box is bound before the closure is
// stored into it, and the store has to go *through* the box rather than
// replacing it.
$fact = function (int $k) use (&$fact): int { return $k <= 1 ? 1 : $k * $fact($k - 1); };
var_dump($fact(5));

$n = 1;
$byValue = function () use ($n) { return $n; };
$byRef = function () use (&$n) { return $n; };
$n = 2;
var_dump($byValue(), $byRef());

// An arrow function captures by value, at the point it is written.
$arrow = fn() => $n;
$n = 3;
var_dump($arrow());

// Mutual recursion through two by-reference captures.
$isEven = function (int $k) use (&$isOdd): bool { return $k === 0 ? true : $isOdd($k - 1); };
$isOdd = function (int $k) use (&$isEven): bool { return $k === 0 ? false : $isEven($k - 1); };
var_dump($isEven(4), $isOdd(4));

// A **static** closure binds no `$this`, even declared inside a method beside
// an ordinary one that does.
class Host {
    private $secret = "s";
    public function make(): array {
        return [
            static function () { return isset($this) ? "has this" : "no this"; },
            function () { return isset($this) ? "has this" : "no this"; },
        ];
    }
    public function reader(): Closure { return fn() => $this->secret; }
}
$host = new Host;
[$staticOne, $ordinary] = $host->make();
var_dump($staticOne(), $ordinary());

// Binding an instance to a static closure is refused with a warning, and the
// result is null rather than a new Closure.
$standalone = static function () { return "ran"; };
var_dump($standalone->bindTo($host));
var_dump(Closure::bind($standalone, $host));
// Binding only a scope to one is still allowed.
$scoped = $standalone->bindTo(null, Host::class);
var_dump($scoped());

// The ordinary binding operations still work.
$reader = $host->reader();
var_dump($reader());
$unbound = function () { return $this->secret; };
var_dump(Closure::bind($unbound, $host, Host::class)());
var_dump($unbound->bindTo($host, Host::class)());
var_dump($unbound->call($host));
