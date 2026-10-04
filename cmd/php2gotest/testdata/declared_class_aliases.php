<?php
// A class_alias is listed by get_declared_classes (or _interfaces, for an
// interface) after the classes bound before it, under its folded name.

class a {}
interface I {}
class_alias("a", "b");
class_alias("a", "BB");
class_alias("I", "JJ");
$c = get_declared_classes();
var_dump(end($c), prev($c), prev($c));
$i = get_declared_interfaces();
var_dump(end($i), prev($i));
trait T {}
class_alias("T", "TA");
$t = get_declared_traits();
var_dump(end($t), prev($t));
