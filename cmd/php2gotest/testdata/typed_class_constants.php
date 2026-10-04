<?php
// PHP 8.3 typed class constants. The check is **not** the one a parameter or a
// property gets: nothing is coerced except the int-to-float widening the
// language always allows, and it happens where the constant is *read*, because
// a deferred initializer may name something that does not exist yet.

interface HasName {}

class S implements HasName
{
    public function __toString(): string { return 'S'; }
}

class Good
{
    const int I = 1;
    const float F = 2;
    const float G = 2.5;
    const string T = 'ok';
    const bool B = true;
    const array A = [1, 2];
    const int|string U = 'either';
    const ?int N = null;
    const HasName O = ONE;
    const mixed M = ONE;
    const iterable IT = [1];
}

class Bad
{
    const int I = LATER;
    const string T = ONE;
    // A *literal* here would be a compile-time fatal instead: the engine
    // checks what it can fold. Only a deferred value reaches the read.
    const S CLS = LATER;
}

define('LATER', 'a string');
define('ONE', new S());

var_dump(Good::I, Good::F, Good::G, Good::T, Good::B, Good::A, Good::U, Good::N);
var_dump(Good::O instanceof S, Good::M instanceof S, Good::IT);

// Read each failing one twice: the type is checked before the value is cached,
// so a refused constant must refuse again rather than answer with the value it
// just rejected.
for ($i = 0; $i < 2; $i++) {
    try {
        var_dump(Bad::I);
    } catch (\TypeError $e) {
        echo $e->getMessage(), "\n";
    }
    try {
        var_dump(Bad::T);
    } catch (\TypeError $e) {
        echo $e->getMessage(), "\n";
    }
    try {
        var_dump(Bad::CLS);
    } catch (\TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}
