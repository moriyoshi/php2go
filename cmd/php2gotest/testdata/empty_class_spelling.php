<?php
// A class with no parent, method or property is still known by its declared
// spelling: diagnostics and names reached through a lowercase reference say
// `A`, not `a`.

class A {}
interface Iface {}
try { a::y("test"); } catch (Error $e) { var_dump($e->getMessage()); }
$n = "a";
try { $n::y(); } catch (Error $e) { var_dump($e->getMessage()); }
var_dump(get_class(new a), (new ReflectionClass("iface"))->getName());
var_dump(a::class, new A instanceof a, get_parent_class(new a));
class B { function m() {} }
var_dump((new ReflectionMethod("b", "m"))->class);
