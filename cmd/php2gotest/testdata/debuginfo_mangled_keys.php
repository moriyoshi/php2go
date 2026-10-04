<?php
// Whatever `__debugInfo` returns is shown as the object's properties, and a
// mangled key in it is unmangled for display -- protected, private with its
// class -- whatever the class really declares. Returning null shows none.

class Foo {
    public $d = 4;
    public function __debugInfo() {
        return ['a' => 1, "\0*\0b" => 2, "\0Foo\0c" => 3, "\0Other\0x" => 4, 7 => 'int'];
    }
}
class Bar {
    public $val = 123;
    public function __debugInfo() {
        return null;
    }
}
var_dump(new Foo);
var_dump(new Bar);
print_r(new Foo);
echo "\n";
print_r(new Bar);
echo "\n";
var_dump([new Foo]);
