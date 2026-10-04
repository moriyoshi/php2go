<?php
// The rules that hold for an enum rather than for its cases: no `new`, no
// dynamic properties, and `name`/`value` are readonly. `enum Empty` has no
// cases at all, which is why these key on the class being an enum rather than
// on its case list.

enum Suit: string
{
    case Hearts = 'H';
    case Spades = 'S';
}

enum Pure
{
    case One;
}

enum Nothing {}

foreach ([Suit::class, Pure::class, Nothing::class] as $c) {
    try {
        new $c();
    } catch (\Error $e) {
        echo get_class($e), ': ', $e->getMessage(), "\n";
    }
}

// `from` binds its argument to the backing type first, so the value is
// reported after coercion -- and a string quoted -- while an argument the
// backing type cannot take is a TypeError from the binding instead.
foreach ([42, 'A', 'H'] as $v) {
    try {
        var_dump(Suit::from($v));
    } catch (\Error $e) {
        echo get_class($e), ': ', $e->getMessage(), "\n";
    }
}
var_dump(Suit::tryFrom('A'), Suit::tryFrom('S'));

$h = Suit::Hearts;
try {
    $h->name = 'x';
} catch (\Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
    $h->value = 'x';
} catch (\Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
    $h->nope = 'x';
} catch (\Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
try {
    unset($h->name);
} catch (\Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}

// A pure case has no `value` slot at all, so writing one is the dynamic
// property refusal rather than the readonly one.
$o = Pure::One;
try {
    $o->value = 1;
} catch (\Error $e) {
    echo get_class($e), ': ', $e->getMessage(), "\n";
}
var_dump($h->name, $h->value, $o->name);
