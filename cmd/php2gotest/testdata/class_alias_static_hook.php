<?php
// A class_alias written only in a static method, or only in a property
// hook, still makes the alias a class another may extend.
class Foo { function who() { return "foo"; } }
class M { static function alias() { class_alias('Foo', 'Bar'); } }
M::alias();
class Baz extends Bar {}
var_dump(get_parent_class(new Baz), (new Baz)->who());

class Qux { function who() { return "qux"; } }
class P { public $x { get { class_alias('Qux', 'Bar2'); return 1; } } }
$p = new P;
$p->x;
class Baz2 extends Bar2 {}
var_dump(get_parent_class(new Baz2), (new Baz2)->who());
