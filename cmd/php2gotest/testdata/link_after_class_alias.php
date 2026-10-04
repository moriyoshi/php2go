<?php
// A property type naming a class alias made at run time is the same type as
// the class aliased, so the override is compatible -- which a link tried
// before `class_alias` runs cannot know; a script that aliases classes gets
// no trial verdict (variance/property_types_early_bind).

class X {}
class_alias('X', 'Y');

class A {
    public X $prop;
}
class B extends A {
    public Y $prop;
}
var_dump(new B instanceof A, get_parent_class(new B));
