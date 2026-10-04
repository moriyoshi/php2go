<?php
// A backed enum's cases are validated when anything **reaches one**. The values
// are literals, so the fault is a compile-time fact; only the raising is
// deferred. Probed rather than assumed, and three things surprised me:
//
//   - `cases()` is **exempt**, while `from`, `tryFrom` and any case constant
//     access all throw;
//   - reaching a *valid* case still throws, because the check is about the
//     enum rather than the case;
//   - it throws on **every** access, not once.

enum Dup: int
{
    case A = 1;
    case B = 2;
    case C = 2;
}

enum Mismatch: int
{
    case Ok = 1;
    case Bad = 'not an int';
}

enum Fine: string
{
    case X = 'x';
    case Y = 'y';
}

// cases() is exempt: it answers even though the enum is rejected everywhere
// else.
var_dump(count(Dup::cases()));
var_dump(count(Mismatch::cases()));

// A *valid* case still throws -- the enum is what is being validated. The
// message names the first case holding the value and the duplicate, not the
// case being read.
try {
    Dup::A;
} catch (\Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
    Dup::C;
} catch (\Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}

// Twice, to show it is not a once-only check.
for ($i = 0; $i < 2; $i++) {
    try {
        Dup::from(1);
    } catch (\Error $e) {
        echo get_class($e), ': ', $e->getMessage(), "\n";
    }
    try {
        Dup::tryFrom(99);
    } catch (\Error $e) {
        echo get_class($e), ': ', $e->getMessage(), "\n";
    }
}

// A type mismatch is a TypeError and names the types, not the case.
try {
    Mismatch::Ok;
} catch (\TypeError $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}

// **The argument binding runs before the enum validation.** A bad argument to a
// broken enum reports the TypeError, not the duplicate -- validating first is
// the plausible order and the wrong one, and it agrees with the engine on
// every case except this one.
try {
    Dup::tryFrom('not an int');
} catch (\TypeError $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
    Dup::from('not an int');
} catch (\TypeError $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}

// A sound enum is untouched by any of this.
var_dump(Fine::X, Fine::from('y'), Fine::tryFrom('nope'));
