<?php
// class_alias onto a name already taken warns and returns false. The kind
// and the `previously declared` site are the aliased class's, not the taken
// name's; the name is the alias as written -- including a name an earlier
// alias took, and an engine class's (class_alias_004, _010).

class Foo {}
interface I {}
trait T {}
enum E {}
class Taken {}

var_dump(class_alias("Foo", "Taken"));
var_dump(class_alias("I", "taken"));
var_dump(class_alias("T", "Taken"));
var_dump(class_alias("E", "Taken"));
var_dump(class_alias("Foo", "stdClass"));
var_dump(class_alias("Foo", "Countable"));
var_dump(class_alias("Foo", "Al"));
var_dump(class_alias("I", "Al"));
var_dump(class_alias("Foo", "Foo"));
var_dump(new Al instanceof Foo);
