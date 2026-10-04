<?php
// Reflection reads a trait's constants, which direct access refuses:
// `TestTrait::Constant` is `Cannot access trait constant ... directly`, while
// ReflectionClass's getConstant/getConstants/hasConstant answer for the trait
// and for a class composing it (traits/constant_019).

trait TestTrait {
    public const Constant = 42;
    public const Other = 'other';
}
class TestClass {
    use TestTrait;
}

$t = new ReflectionClass(TestTrait::class);
var_dump($t->getConstant('Constant'));
try {
    var_dump(TestTrait::Other);
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
var_dump($t->hasConstant('Other'), $t->getConstants());
$c = new ReflectionClass(TestClass::class);
var_dump($c->getConstant('Constant'), $c->getConstants());
var_dump(TestClass::Constant);
try {
    var_dump(TestTrait::Constant);
} catch (Error $e) {
    echo $e->getMessage(), "\n";
}
