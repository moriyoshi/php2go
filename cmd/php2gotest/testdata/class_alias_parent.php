<?php
// A class may extend an alias the program made: the alias is the same
// class, and the parent is reported by its own name.
class Base {
    public function who() { return static::class . " from " . self::class; }
}
class_alias('Base', 'Nick');
class Child extends Nick {}
$c = new Child;
var_dump($c->who(), get_parent_class($c), $c instanceof Nick, $c instanceof Base);
