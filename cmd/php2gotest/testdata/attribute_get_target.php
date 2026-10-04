<?php
// ReflectionAttribute::getTarget() says where the attribute was written, as an
// Attribute::TARGET_* constant.

#[A] class C { #[A] public $p; #[A] function m() {} }
#[A] function f() {}
$r = new ReflectionClass('C');
var_dump(
    $r->getAttributes()[0]->getTarget() === Attribute::TARGET_CLASS,
    $r->getProperty('p')->getAttributes()[0]->getTarget() === Attribute::TARGET_PROPERTY,
    $r->getMethod('m')->getAttributes()[0]->getTarget() === Attribute::TARGET_METHOD,
    (new ReflectionFunction('f'))->getAttributes()[0]->getTarget() === Attribute::TARGET_FUNCTION,
    (new ReflectionFunction(#[A] fn() => 1))->getAttributes()[0]->getTarget(),
);
// isRepeated(): the same name written more than once on the target, however
// the list was filtered.
#[A] #[B] #[A] class R {}
$attrs = (new ReflectionClass('R'))->getAttributes();
var_dump($attrs[0]->isRepeated(), $attrs[1]->isRepeated(), (new ReflectionClass('R'))->getAttributes('A')[0]->isRepeated());
