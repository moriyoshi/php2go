<?php
// What an uninitialized lazy object shows under var_dump.
//
// A file of its own, with no object temporaries before it: php2go does not
// reuse the handle of a temporary the engine has already freed. That has now
// forced four fixtures apart, so it is a shape rather than a surprise -- any
// fixture dumping whole objects wants nothing ahead of it.
// What a dump of an **uninitialized** lazy object shows: its typed properties,
// named but with no value. An untyped property has no type to name, so PHP does
// not list it at all, and the header count excludes the uninitialized ones.
// Dumping must not initialize -- the initializer below never runs.
class Shown
{
    public int $typed = 1;
    public $untyped = 2;
    public string $also = 'x';
}

$shownReflector = new ReflectionClass('Shown');
$shown = $shownReflector->newLazyGhost(function (Shown $o) {
    echo "never\n";
});
var_dump($shown);

// A class declaring no properties has nothing to defer, so the engine hands
// back an ordinary object: no label, and the initializer is never called.
class Bare
{
}

$bareReflector = new ReflectionClass('Bare');
$bare = $bareReflector->newLazyGhost(function (Bare $o) {
    echo "never either\n";
});
var_dump($bare, $bareReflector->isUninitializedLazyObject($bare));
