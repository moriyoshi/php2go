<?php
// A closure's attributes evaluate their constant expressions in the class
// that declares the closure -- `self::class`, a private constant -- and keep
// doing so after `bindTo` moves the closure to another scope.

#[Attribute]
class A1 {
    public function __construct(public $a, public $b) {}
}

#[Attribute(Attribute::TARGET_ALL | Attribute::IS_REPEATABLE)]
class A2 {
    public function __construct(public $made) {}
}

class Other {
    private const FOO = 'other';
}

class C1 {
    private const FOO = 'foo';

    public static function make() {
        return #[A1(self::class, self::FOO)] #[A2(new self)] function () {};
    }
}

$ref = new ReflectionFunction(C1::make());
print_r($ref->getAttributes()[0]->getArguments());
$ref = new ReflectionFunction(C1::make()->bindTo(null, Other::class));
print_r($ref->getAttributes()[0]->getArguments());
var_dump($ref->getAttributes()[0]->newInstance()->a);
var_dump(get_class($ref->getAttributes()[1]->getArguments()[0]));
$ref = new ReflectionFunction(C1::make());
var_dump(get_class($ref->getAttributes()[1]->getArguments()[0]));

$plain = #[A1('file', 'scope')] fn() => 1;
print_r((new ReflectionFunction($plain))->getAttributes()[0]->getArguments());

// A closure body's own constant expressions follow its bound scope too: a
// parameter default naming `self::FOO` or `self::class`.
class D1 {
    const FOO = 'd1';
    public static function make() {
        return function ($a = self::FOO, $b = self::class) { return [$a, $b]; };
    }
}
class D2 {
    const FOO = 'd2';
}
print_r(D1::make()());
print_r(Closure::bind(D1::make(), null, D2::class)());
