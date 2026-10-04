<?php
// `parent::class` in a trait method is the using class's parent, and an Error
// when that class has none -- not the empty name.
trait T { public function f() { var_dump(parent::class, self::class); } }
class P {}
class C extends P { use T; }
class D { use T; }
(new C)->f();
try { (new D)->f(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
