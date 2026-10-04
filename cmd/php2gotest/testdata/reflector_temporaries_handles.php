<?php
// A reflector made for one call dies when the call returns -- its accessors
// keep no reference to it -- so the next object takes its handle: the
// instance an attribute's newInstance() builds is #1 when nothing else is
// alive (attributes/032_attribute_validation_scope, enum/enum-attributes).
// What newInstance() returns is not claimed: it runs a constructor that
// may keep `$this`.

#[Attribute]
class Tag {
    public function __construct(public string $v = "t") {}
}

#[Tag("on class")]
class C {
    #[Tag] const K = 1;
    public function m() {}
}

var_dump((new ReflectionClass(C::class))->getAttributes()[0]->newInstance());
