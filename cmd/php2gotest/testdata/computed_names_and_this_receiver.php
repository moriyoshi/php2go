<?php
// A method name may be **computed** -- `$o->$n()`, `$o->{expr}()` -- and the
// callee is decided at run time exactly as it is for a named one. Nothing is
// settled at compile time, so a named argument there is refused rather than
// bound to a parameter guessed from one declaration.

class Target {
    public $prop = 1;
    public array $list = [1];
    public int $counter = 0;

    public function greet(string $who): string { return "hello $who"; }
    public static function make(int $n): string { return "made $n"; }

    // `$this->x op= y` leaves the receiver operand unused, which is the shape
    // that used to lose it.
    public function bump(): void { $this->counter += 1; }
    public function append(string $s): void { $this->list[] = $s; }
    public function concat(string $s): void { $this->prop .= $s; }
}

$t = new Target;
$method = "greet";
var_dump($t->$method("world"));
var_dump($t->{"gre" . "et"}("again"));

$static = "make";
var_dump(Target::$static(1));
$class = "Target";
var_dump($class::$static(2));
var_dump((new $class)->$method("fresh"));

// Computed property names, read and written, including through a place.
$name = "prop";
var_dump($t->$name);
$t->$name = "set";
var_dump($t->prop);
$listName = "list";
$t->$listName[] = "pushed";
var_dump($t->list);

// The compound forms with an implicit `$this` receiver.
$t->bump(); $t->bump();
var_dump($t->counter);
$t->append("added");
var_dump($t->list);
$t->concat("!");
var_dump($t->prop);

// An undefined computed method is still an ordinary refusal.
$missing = "nope";
try { $t->$missing(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
