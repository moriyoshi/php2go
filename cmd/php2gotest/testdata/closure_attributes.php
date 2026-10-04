<?php
// A closure's and an arrow function's `#[...]` are reflected on it, filtered
// by name like any function's.

$ref = new ReflectionFunction(#[A1] #[A2] function () { });
var_dump(count($ref->getAttributes()), count($ref->getAttributes(A2::class)), $ref->getAttributes(A2::class)[0]->getName());
$arrow = new ReflectionFunction(#[A3(1, 2)] fn() => 1);
var_dump($arrow->getAttributes()[0]->getName(), $arrow->getAttributes()[0]->getArguments());
var_dump(count((new ReflectionFunction(function () {}))->getAttributes()));
