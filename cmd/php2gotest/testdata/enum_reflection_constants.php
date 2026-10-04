<?php
// ReflectionClass::getConstants() on an enum lists its cases as constants,
// interleaved with the declared constants in the order they were written.

enum Foo { case Bar; case Baz; }
var_dump((new ReflectionClass(Foo::class))->getConstants());
enum Back: string { const A = 'a'; case Bar = 'b'; const B = self::Bar; case Baz = 'z'; }
var_dump(array_keys((new ReflectionClass(Back::class))->getConstants()));
var_dump((new ReflectionClass(Back::class))->getConstants()["B"] === Back::Bar);
