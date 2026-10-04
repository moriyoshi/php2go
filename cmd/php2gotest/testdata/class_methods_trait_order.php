<?php
// get_class_methods lists a class's table in the engine's order: own methods,
// inherited ones, then each trait's table -- a trait's own `use` (and its
// aliases) included, an alias just before the method it names.
trait A { function a1() {} function a2() {} }
trait B { use A { a1 as b_alias; } function b1() {} }
trait C2 { function c1() {} }
class X { use B, C2 { c1 as x_alias; } function x1() {} }
class Y extends X { use A; function y1() {} }
print_r(get_class_methods('X'));
print_r(get_class_methods('Y'));
print_r(get_class_methods('B'));
trait ParentTrait { public function testMethod() {} }
trait ChildTrait { use ParentTrait { testMethod as testMethodFromParentTrait; } public function testMethod() {} }
class TestClass { use ChildTrait; }
print_r(get_class_methods(new TestClass));
